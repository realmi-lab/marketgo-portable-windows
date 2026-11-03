package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"market-share-go/internal/notice"
)

func (r *Repository) NoticePipeline(ctx context.Context, req notice.PipelineRequest) (notice.PipelineResponse, error) {
	if err := notice.ValidatePipelineRequest(req); err != nil {
		return notice.PipelineResponse{}, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return notice.PipelineResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out := notice.PipelineResponse{SchemaVersion: notice.PipelineSchemaVersion, Policy: notice.PipelinePolicy}
	digest := notice.PipelineDigest(struct {
		Operation string
		Request   notice.PipelineRequest
	}{req.Operation, req})
	if req.RequestID != "" {
		// Serialize replay of an operation before any source/job lock. A receipt
		// survives loss of the HTTP response and does not require a live lease.
		if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 27))", req.RequestID); err != nil {
			return out, err
		}
		var previousDigest, operation string
		var payload []byte
		err = tx.QueryRow(ctx, "SELECT operation,request_digest,response FROM notice_v2_receipts WHERE idempotency_key=$1", req.RequestID).Scan(&operation, &previousDigest, &payload)
		if err == nil {
			if operation != req.Operation || previousDigest != digest {
				return out, notice.ErrPipelineConflict
			}
			if err = json.Unmarshal(payload, &out); err != nil {
				return out, err
			}
			// The receipt attests historical persistence, not current adoption.
			out.ProjectionCurrent = false
			return out, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return out, err
		}
	}
	switch req.Operation {
	case "snapshot":
		out.Items, err = pipelineSnapshot(ctx, tx)
	case "capabilities":
		var rows pgx.Rows
		rows, err = tx.Query(ctx, "SELECT exchange,enabled,collector_mode,read_mode,source_generation FROM notice_v2_sources ORDER BY exchange")
		if err == nil {
			for rows.Next() {
				var s notice.PipelineSource
				if err = rows.Scan(&s.Exchange, &s.Enabled, &s.CollectorMode, &s.ReadMode, &s.Generation); err != nil {
					break
				}
				out.Sources = append(out.Sources, s)
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
		}
	case "source":
		out.Source, err = pipelineSource(ctx, tx, req)
	case "claim":
		out, err = pipelineClaim(ctx, tx, req)
	case "object":
		out, err = pipelineObject(ctx, tx, req)
	case "object_get":
		out, err = pipelineObjectRead(ctx, tx, req)
	default:
		var job notice.PipelineJob
		job, err = pipelineLiveJob(ctx, tx, req)
		if err == nil {
			switch req.Operation {
			case "heartbeat":
				err = tx.QueryRow(ctx, "UPDATE notice_v2_jobs SET lease_until=clock_timestamp()+interval '90 seconds' WHERE job_id=$1 RETURNING lease_until", job.ID).Scan(&job.LeaseUntil)
				out.Job = &job
			case "page":
				out, err = pipelinePage(ctx, tx, req, job)
			case "complete":
				out, err = pipelineComplete(ctx, tx, req, job)
			case "fail":
				err = pipelineFinish(ctx, tx, job, "RETRY_WAIT", req.ErrorCode)
			default:
				err = notice.ErrPipelineInvalid
			}
		}
	}
	if err != nil {
		return out, err
	}
	if req.RequestID != "" {
		payload, marshalErr := json.Marshal(out)
		if marshalErr != nil {
			return out, marshalErr
		}
		_, err = tx.Exec(ctx, "INSERT INTO notice_v2_receipts(idempotency_key,operation,request_digest,response) VALUES($1,$2,$3,$4)", req.RequestID, req.Operation, digest, payload)
		if err != nil {
			return out, err
		}
	}
	return out, tx.Commit(ctx)
}

func pipelineSource(ctx context.Context, tx pgx.Tx, req notice.PipelineRequest) (*notice.PipelineSource, error) {
	s := req.Source
	_, err := tx.Exec(ctx, "INSERT INTO notice_v2_sources(exchange) VALUES($1) ON CONFLICT DO NOTHING", req.Exchange)
	if err != nil {
		return nil, err
	}
	var generation int64
	err = tx.QueryRow(ctx, "SELECT source_generation FROM notice_v2_sources WHERE exchange=$1 FOR UPDATE", req.Exchange).Scan(&generation)
	if err != nil {
		return nil, err
	}
	if s.Generation != generation {
		return nil, notice.ErrPipelineConflict
	}
	_, err = tx.Exec(ctx, `UPDATE notice_v2_sources SET enabled=$2,collector_mode=$3,read_mode=$4,source_generation=source_generation+1,next_scan_at=clock_timestamp() WHERE exchange=$1`, req.Exchange, s.Enabled, s.CollectorMode, s.ReadMode)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE notice_v2_attempts a SET finished_at=clock_timestamp(),outcome='SOURCE_CHANGED' FROM notice_v2_jobs j WHERE a.job_id=j.job_id AND j.exchange=$1 AND a.finished_at IS NULL`, req.Exchange)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE notice_v2_jobs SET status='CANCELLED',reason='SOURCE_CHANGED',lease_token=NULL,lease_until=NULL WHERE exchange=$1 AND status IN ('PENDING','RUNNING','RETRY_WAIT','WAITING_INPUT','WAITING_BUDGET')`, req.Exchange)
	if err != nil {
		return nil, err
	}
	// A fresh job identity resumes work without reviving an old lease/receipt.
	rows, err := tx.Query(ctx, `SELECT job_id::text,notice_id,stage,coalesce(input_revision_id::text,''),input_digest,payload FROM notice_v2_jobs WHERE exchange=$1 AND source_generation=$2 AND status='CANCELLED' AND reason='SOURCE_CHANGED' AND stage<>'LIST_PAGE'`, req.Exchange, generation)
	if err != nil {
		return nil, err
	}
	type retry struct {
		job notice.PipelineJob
		raw json.RawMessage
	}
	var pending []retry
	for rows.Next() {
		var v retry
		v.job.Exchange = req.Exchange
		v.job.Generation = generation + 1
		if err = rows.Scan(&v.job.ID, &v.job.NoticeID, &v.job.Stage, &v.job.RevisionID, &v.job.InputDigest, &v.raw); err != nil {
			rows.Close()
			return nil, err
		}
		pending = append(pending, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, v := range pending {
		if err = pipelineEnqueue(ctx, tx, v.job, v.job.ID+":resume", v.raw); err != nil {
			return nil, err
		}
	}
	copy := *s
	copy.Exchange = req.Exchange
	copy.Generation = generation + 1
	return &copy, err
}

func pipelineLiveJob(ctx context.Context, tx pgx.Tx, req notice.PipelineRequest) (notice.PipelineJob, error) {
	var job notice.PipelineJob
	// Always lock source, then job. Source transition cannot overtake commit.
	var generation int64
	var enabled bool
	var mode string
	err := tx.QueryRow(ctx, "SELECT source_generation,enabled,collector_mode FROM notice_v2_sources WHERE exchange=$1 FOR UPDATE", req.Exchange).Scan(&generation, &enabled, &mode)
	if err != nil {
		return job, err
	}
	if !enabled || mode != "V2" {
		return job, notice.ErrPipelineConflict
	}
	err = tx.QueryRow(ctx, `SELECT job_id::text,exchange,notice_id,stage,coalesce(input_revision_id::text,''),input_digest,source_generation,lease_token::text,lease_until,attempt_count
 FROM notice_v2_jobs WHERE job_id=$1 AND exchange=$2 AND status='RUNNING' AND lease_token=$3 AND lease_until>clock_timestamp() AND source_generation=$4 FOR UPDATE`, req.JobID, req.Exchange, req.LeaseToken, generation).Scan(&job.ID, &job.Exchange, &job.NoticeID, &job.Stage, &job.RevisionID, &job.InputDigest, &job.Generation, &job.LeaseToken, &job.LeaseUntil, &job.Attempt)
	if errors.Is(err, pgx.ErrNoRows) {
		return job, notice.ErrPipelineConflict
	}
	return job, err
}

func pipelineObject(ctx context.Context, tx pgx.Tx, req notice.PipelineRequest) (notice.PipelineResponse, error) {
	out := notice.PipelineResponse{SchemaVersion: 2}
	// Expired-worker responses may be archived, but never promoted. The job and
	// its source generation must have actually existed (spool reconciliation).
	var generation int64
	err := tx.QueryRow(ctx, "SELECT source_generation FROM notice_v2_jobs WHERE job_id=$1 AND exchange=$2", req.JobID, req.Exchange).Scan(&generation)
	if err != nil {
		return out, err
	}
	if generation != req.SourceGeneration {
		return out, notice.ErrPipelineConflict
	}
	obj := *req.Object
	obj.SHA256 = notice.PipelineBytesDigest(obj.Bytes)
	if req.Object.SHA256 != "" && req.Object.SHA256 != obj.SHA256 {
		return out, notice.ErrPipelineInvalid
	}
	_, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,28))", obj.SHA256)
	if err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, "SELECT object_id::text,bytes FROM notice_v2_objects WHERE sha256=$1 AND byte_length=$2", obj.SHA256, len(obj.Bytes))
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var id string
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return out, err
		}
		if bytes.Equal(raw, obj.Bytes) {
			obj.ID = id
			break
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if obj.ID == "" {
		obj.ID = notice.PipelineID()
		_, err = tx.Exec(ctx, "INSERT INTO notice_v2_objects(object_id,sha256,byte_length,media_type,bytes) VALUES($1,$2,$3,$4,$5)", obj.ID, obj.SHA256, len(obj.Bytes), obj.MediaType, obj.Bytes)
		if err != nil {
			return out, err
		}
	}
	_, err = tx.Exec(ctx, "INSERT INTO notice_v2_observations(observation_id,job_id,object_id,source_generation) VALUES($1,$2,$3,$4) ON CONFLICT(job_id,object_id) DO NOTHING", notice.PipelineID(), req.JobID, obj.ID, generation)
	obj.Bytes = nil
	out.Object = &obj
	out.SavedID = obj.ID
	return out, err
}

func pipelineObjectRead(ctx context.Context, tx pgx.Tx, req notice.PipelineRequest) (notice.PipelineResponse, error) {
	out := notice.PipelineResponse{SchemaVersion: 2}
	job, err := pipelineLiveJob(ctx, tx, req)
	if err != nil {
		return out, err
	}
	var obj notice.PipelineObject
	// Inputs can only reference objects observed by this job or its revision's
	// BODY job. Arbitrary archive object IDs are not a read capability.
	err = tx.QueryRow(ctx, `SELECT o.object_id::text,o.sha256,o.media_type,o.bytes FROM notice_v2_objects o WHERE o.object_id=$1 AND EXISTS(
 SELECT 1 FROM notice_v2_observations v JOIN notice_v2_jobs j ON j.job_id=v.job_id WHERE v.object_id=o.object_id AND
 (j.job_id=$2 OR (j.exchange=$3 AND j.notice_id=$4 AND j.stage='BODY' AND EXISTS(SELECT 1 FROM notice_v2_revisions r WHERE r.revision_id=$5 AND r.payload->'object_ids' ? o.object_id::text))))`, req.ObjectID, job.ID, job.Exchange, job.NoticeID, nullPipelineID(job.RevisionID)).Scan(&obj.ID, &obj.SHA256, &obj.MediaType, &obj.Bytes)
	out.Object = &obj
	return out, err
}

func nullPipelineID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func pipelineFinish(ctx context.Context, tx pgx.Tx, job notice.PipelineJob, status, reason string) error {
	if status == "RETRY_WAIT" && reason == "FULL_REVIEW_INPUT_LIMIT" {
		status = "WAITING_INPUT"
	}
	if status == "RETRY_WAIT" && job.Attempt >= 5 {
		status = "NEEDS_REVIEW"
	}
	_, err := tx.Exec(ctx, `UPDATE notice_v2_attempts SET finished_at=clock_timestamp(),outcome=$3 WHERE job_id=$1 AND attempt_no=$2 AND finished_at IS NULL`, job.ID, job.Attempt, status+":"+reason)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE notice_v2_jobs SET status=$2,reason=$3,lease_token=NULL,lease_until=NULL,due_at=clock_timestamp()+interval '30 seconds'*least(attempt_count,10) WHERE job_id=$1`, job.ID, status, reason)
	return err
}

func pipelineEnqueue(ctx context.Context, tx pgx.Tx, job notice.PipelineJob, key string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO notice_v2_jobs(job_id,work_key,exchange,notice_id,stage,input_revision_id,input_digest,source_generation,policy,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING`, notice.PipelineID(), key, job.Exchange, job.NoticeID, job.Stage, nullPipelineID(job.RevisionID), job.InputDigest, job.Generation, notice.PipelinePolicy, encoded)
	return err
}

func pipelineObjectBindings(ctx context.Context, tx pgx.Tx, job notice.PipelineJob, ids []string) error {
	if len(ids) == 0 {
		return fmt.Errorf("%w: missing original objects", notice.ErrPipelineInvalid)
	}
	for _, id := range ids {
		var exists bool
		err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM notice_v2_observations WHERE job_id=$1 AND object_id=$2)", job.ID, id).Scan(&exists)
		if err != nil {
			return err
		}
		if !exists {
			return notice.ErrPipelineConflict
		}
	}
	return nil
}

func pipelineEncode(value any) ([]byte, error) { return json.Marshal(value) }

func pipelineCleanReason(reason string) string { return strings.TrimSpace(reason) }
