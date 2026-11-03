package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"market-share-go/internal/notice"
)

func pipelineClaim(ctx context.Context, tx pgx.Tx, req notice.PipelineRequest) (notice.PipelineResponse, error) {
	out := notice.PipelineResponse{SchemaVersion: 2}
	rows, err := tx.Query(ctx, `SELECT exchange,source_generation FROM notice_v2_sources WHERE enabled AND collector_mode='V2' AND ($1='' OR exchange=$1) ORDER BY next_scan_at,exchange FOR UPDATE SKIP LOCKED`, req.Exchange)
	if err != nil {
		return out, err
	}
	type source struct {
		exchange   string
		generation int64
	}
	var sources []source
	for rows.Next() {
		var s source
		if err = rows.Scan(&s.exchange, &s.generation); err != nil {
			rows.Close()
			return out, err
		}
		sources = append(sources, s)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	for _, s := range sources {
		_, err = tx.Exec(ctx, `UPDATE notice_v2_attempts a SET finished_at=clock_timestamp(),outcome='LEASE_EXPIRED' FROM notice_v2_jobs j WHERE a.job_id=j.job_id AND j.exchange=$1 AND j.status='RUNNING' AND j.lease_until<=clock_timestamp() AND a.finished_at IS NULL`, s.exchange)
		if err != nil {
			return out, err
		}
		_, err = tx.Exec(ctx, `UPDATE notice_v2_jobs SET status=CASE WHEN attempt_count>=5 THEN 'NEEDS_REVIEW' ELSE 'RETRY_WAIT' END,reason='LEASE_EXPIRED',lease_token=NULL,lease_until=NULL WHERE exchange=$1 AND status='RUNNING' AND lease_until<=clock_timestamp()`, s.exchange)
		if err != nil {
			return out, err
		}
		if req.Stage == "LIST_PAGE" {
			var due bool
			err = tx.QueryRow(ctx, `SELECT next_scan_at<=clock_timestamp() AND NOT EXISTS(SELECT 1 FROM notice_v2_jobs WHERE exchange=$1 AND stage='LIST_PAGE' AND status IN ('PENDING','RUNNING','RETRY_WAIT')) FROM notice_v2_sources WHERE exchange=$1`, s.exchange).Scan(&due)
			if err != nil {
				return out, err
			}
			if due {
				job := notice.PipelineJob{Exchange: s.exchange, Stage: "LIST_PAGE", Generation: s.generation, InputDigest: notice.PipelineDigest(s.generation)}
				err = pipelineEnqueue(ctx, tx, job, notice.PipelineID(), struct{}{})
				if err != nil {
					return out, err
				}
				_, err = tx.Exec(ctx, "UPDATE notice_v2_sources SET next_scan_at=clock_timestamp()+interval '600 seconds' WHERE exchange=$1", s.exchange)
				if err != nil {
					return out, err
				}
			}
		}
		var job notice.PipelineJob
		var payload []byte
		err = tx.QueryRow(ctx, `SELECT job_id::text,exchange,notice_id,stage,coalesce(input_revision_id::text,''),input_digest,source_generation,attempt_count,payload FROM notice_v2_jobs WHERE exchange=$1 AND stage=$2 AND source_generation=$3 AND status IN ('PENDING','RETRY_WAIT','WAITING_BUDGET') AND due_at<=clock_timestamp() ORDER BY created_at,job_id LIMIT 1 FOR UPDATE SKIP LOCKED`, s.exchange, req.Stage, s.generation).Scan(&job.ID, &job.Exchange, &job.NoticeID, &job.Stage, &job.RevisionID, &job.InputDigest, &job.Generation, &job.Attempt, &payload)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return out, err
		}
		var jobPolicy string
		if err = tx.QueryRow(ctx, "SELECT policy FROM notice_v2_jobs WHERE job_id=$1", job.ID).Scan(&jobPolicy); err != nil {
			return out, err
		}
		if jobPolicy != notice.PipelinePolicy {
			if _, err = tx.Exec(ctx, "UPDATE notice_v2_jobs SET status='NEEDS_REVIEW',reason='POLICY_CHANGED' WHERE job_id=$1", job.ID); err != nil {
				return out, err
			}
			continue
		}
		if job.Stage == "BODY" {
			var item notice.Item
			if err = json.Unmarshal(payload, &item); err != nil {
				return out, err
			}
			job.Item = &item
			anchor := notice.RevisionTimestampMS(item.PublishedTSMS, item.UpdatedTSMS, item.DetectedTSMS)
			if anchor < time.Now().Add(-30*24*time.Hour).UnixMilli() {
				_, err = tx.Exec(ctx, "UPDATE notice_v2_jobs SET status='SUPERSEDED',reason='OUT_OF_SCOPE' WHERE job_id=$1", job.ID)
				if err != nil {
					return out, err
				}
				continue
			}
		}
		if job.RevisionID != "" && job.Stage != "BODY" {
			var current string
			err = tx.QueryRow(ctx, "SELECT coalesce(current_revision_id::text,'') FROM notice_v2_heads WHERE exchange=$1 AND notice_id=$2 FOR UPDATE", job.Exchange, job.NoticeID).Scan(&current)
			if err != nil {
				return out, err
			}
			if current != job.RevisionID {
				_, err = tx.Exec(ctx, "UPDATE notice_v2_jobs SET status='SUPERSEDED',reason='STALE_INPUT' WHERE job_id=$1", job.ID)
				if err != nil {
					return out, err
				}
				continue
			}
			var raw []byte
			err = tx.QueryRow(ctx, "SELECT payload FROM notice_v2_revisions WHERE revision_id=$1", job.RevisionID).Scan(&raw)
			if err != nil {
				return out, err
			}
			var revision notice.PipelineRevision
			if err = json.Unmarshal(raw, &revision); err != nil {
				return out, err
			}
			out.Revision = &revision
			if job.Stage == "SEMANTIC_GENERATE" {
				var partition notice.PipelinePartition
				if err = json.Unmarshal(payload, &partition); err != nil {
					return out, err
				}
				if partition.Total > 0 {
					out.Revision = &partition.Revision
					out.Derivation = &notice.PipelineDerivation{Partition: &partition}
				}
			}
			if len(out.Revision.Blocks) == 0 && job.Stage == "SEMANTIC_GENERATE" {
				_, err = tx.Exec(ctx, "UPDATE notice_v2_jobs SET status='WAITING_INPUT',reason='FULL_REVIEW_INPUT_LIMIT' WHERE job_id=$1", job.ID)
				if err != nil {
					return out, err
				}
				out.Revision = nil
				continue
			}
			if job.Stage == "SEMANTIC_GENERATE" || job.Stage == "SEMANTIC_AUDIT" {
				var used, reserved int
				err = tx.QueryRow(ctx, `UPDATE notice_v2_heads SET budget_day_kst=(clock_timestamp() AT TIME ZONE 'Asia/Seoul')::date,spent_calls=CASE WHEN budget_day_kst=(clock_timestamp() AT TIME ZONE 'Asia/Seoul')::date THEN spent_calls ELSE 0 END,reserved_calls=CASE WHEN budget_day_kst=(clock_timestamp() AT TIME ZONE 'Asia/Seoul')::date THEN reserved_calls ELSE 0 END WHERE exchange=$1 AND notice_id=$2 RETURNING spent_calls,reserved_calls`, job.Exchange, job.NoticeID).Scan(&used, &reserved)
				if err != nil {
					return out, err
				}
				reservationChange := 1
				if job.Stage == "SEMANTIC_AUDIT" {
					reservationChange = -min(1, reserved)
				}
				if used+reserved+1+reservationChange > 6 {
					_, err = tx.Exec(ctx, `UPDATE notice_v2_jobs SET status='WAITING_BUDGET',reason='DAILY_MODEL_LIMIT',due_at=((clock_timestamp() AT TIME ZONE 'Asia/Seoul')::date+1)::timestamp AT TIME ZONE 'Asia/Seoul' WHERE job_id=$1`, job.ID)
					if err != nil {
						return out, err
					}
					out.Revision = nil
					continue
				}
				// Generation reserves its audit atomically. Every actual attempt,
				// including audit retries and calls after midnight, is charged.
				// Abandoned reservations remain conservative until the day resets.
				_, err = tx.Exec(ctx, "UPDATE notice_v2_heads SET spent_calls=spent_calls+1,reserved_calls=reserved_calls+$3 WHERE exchange=$1 AND notice_id=$2", job.Exchange, job.NoticeID, reservationChange)
				if err != nil {
					return out, err
				}
			}
			if job.Stage == "SEMANTIC_AUDIT" {
				var d notice.PipelineDerivation
				if err = json.Unmarshal(payload, &d); err != nil {
					return out, err
				}
				out.Derivation = &d
				if d.Partition != nil {
					out.Revision = &d.Partition.Revision
				}
			}
		}
		job.Attempt++
		job.LeaseToken = notice.PipelineID()
		err = tx.QueryRow(ctx, `UPDATE notice_v2_jobs SET status='RUNNING',attempt_count=$2,lease_token=$3,lease_until=clock_timestamp()+interval '90 seconds',worker_id=$4 WHERE job_id=$1 RETURNING lease_until`, job.ID, job.Attempt, job.LeaseToken, req.WorkerID).Scan(&job.LeaseUntil)
		if err != nil {
			return out, err
		}
		_, err = tx.Exec(ctx, "INSERT INTO notice_v2_attempts(attempt_id,job_id,attempt_no,lease_token) VALUES($1,$2,$3,$4)", notice.PipelineID(), job.ID, job.Attempt, job.LeaseToken)
		if err != nil {
			return out, err
		}
		out.Job = &job
		return out, nil
	}
	return out, nil
}

func pipelinePage(ctx context.Context, tx pgx.Tx, req notice.PipelineRequest, job notice.PipelineJob) (notice.PipelineResponse, error) {
	out := notice.PipelineResponse{SchemaVersion: 2}
	if job.Stage != "LIST_PAGE" || req.Page == nil {
		return out, notice.ErrPipelineInvalid
	}
	if err := pipelineObjectBindings(ctx, tx, job, req.Page.ObjectIDs); err != nil {
		return out, err
	}
	for _, item := range req.Page.Items {
		if item.Exchange != job.Exchange || !notice.OfficialNoticeURLMatchesID(item.Exchange, item.NoticeID, item.URL) {
			return out, notice.ErrPipelineInvalid
		}
		anchor := notice.RevisionTimestampMS(item.PublishedTSMS, item.UpdatedTSMS, item.DetectedTSMS)
		if anchor < time.Now().Add(-30*24*time.Hour).UnixMilli() {
			continue
		}
		if item.NoticeID == "" || len(item.NoticeID) > 256 || len(item.Title) > 2000 {
			return out, notice.ErrPipelineInvalid
		}
		_, err := tx.Exec(ctx, "INSERT INTO notice_v2_heads(exchange,notice_id) VALUES($1,$2) ON CONFLICT DO NOTHING", item.Exchange, item.NoticeID)
		if err != nil {
			return out, err
		}
		// A periodic scan is a revalidation generation, not a new source version.
		key := fmt.Sprintf("%s:body:%s", job.ID, item.NoticeID)
		body := notice.PipelineJob{Exchange: job.Exchange, NoticeID: item.NoticeID, Stage: "BODY", Generation: job.Generation, InputDigest: notice.PipelineDigest(item)}
		err = tx.QueryRow(ctx, "SELECT coalesce(current_revision_id::text,'') FROM notice_v2_heads WHERE exchange=$1 AND notice_id=$2", item.Exchange, item.NoticeID).Scan(&body.RevisionID)
		if err != nil {
			return out, err
		}
		if err = pipelineEnqueue(ctx, tx, body, key, item); err != nil {
			return out, err
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO notice_v2_scans(scan_id,exchange,page_count) VALUES($1,$2,1) ON CONFLICT(scan_id) DO UPDATE SET page_count=notice_v2_scans.page_count+1`, job.ID, job.Exchange)
	out.SavedID = job.ID
	return out, err
}

func pipelineComplete(ctx context.Context, tx pgx.Tx, req notice.PipelineRequest, job notice.PipelineJob) (notice.PipelineResponse, error) {
	out := notice.PipelineResponse{SchemaVersion: 2}
	switch job.Stage {
	case "LIST_PAGE":
		if req.Page == nil || !req.Page.Complete {
			return out, notice.ErrPipelineInvalid
		}
		_, err := tx.Exec(ctx, "UPDATE notice_v2_scans SET status='COMPLETE',finished_at=clock_timestamp() WHERE scan_id=$1", job.ID)
		if err != nil {
			return out, err
		}
	case "BODY":
		return pipelineRevision(ctx, tx, req, job)
	case "OCR":
		return pipelineOCR(ctx, tx, req, job)
	case "SEMANTIC_GENERATE", "SEMANTIC_AUDIT":
		return pipelineDerivation(ctx, tx, req, job)
	default:
		return out, notice.ErrPipelineInvalid
	}
	out.SavedID = job.ID
	return out, pipelineFinish(ctx, tx, job, "SUCCEEDED", "")
}

func pipelineRevision(ctx context.Context, tx pgx.Tx, req notice.PipelineRequest, job notice.PipelineJob) (notice.PipelineResponse, error) {
	out := notice.PipelineResponse{SchemaVersion: 2}
	if req.Revision == nil {
		return out, notice.ErrPipelineInvalid
	}
	rev := *req.Revision
	if rev.Item.Exchange != job.Exchange || rev.Item.NoticeID != job.NoticeID {
		return out, notice.ErrPipelineConflict
	}
	if err := notice.ValidatePipelineRevision(&rev); err != nil {
		return out, err
	}
	if err := pipelineObjectBindings(ctx, tx, job, rev.ObjectIDs); err != nil {
		return out, err
	}
	if rev.BodyMarkupObjectID != "" {
		if err := pipelineObjectBindings(ctx, tx, job, []string{rev.BodyMarkupObjectID}); err != nil {
			return out, err
		}
		var markup []byte
		if err := tx.QueryRow(ctx, "SELECT bytes FROM notice_v2_objects WHERE object_id=$1", rev.BodyMarkupObjectID).Scan(&markup); err != nil {
			return out, err
		}
		text, blocks, err := notice.NormalizePipelineHTML(string(markup))
		if err != nil {
			return out, err
		}
		if len(text) > 1_000_000 {
			return out, notice.ErrPipelineInvalid
		}
		rev.Item.OriginalBodyText = text
		rev.Blocks = blocks
	}
	for _, media := range rev.Media {
		if media.ObjectID != "" {
			if err := pipelineObjectBindings(ctx, tx, job, []string{media.ObjectID}); err != nil {
				return out, err
			}
		}
	}
	rev.ID = ""
	encoded, err := pipelineRevisionManifest(rev)
	if err != nil {
		return out, err
	}
	digest := notice.PipelineBytesDigest(encoded)
	var current string
	err = tx.QueryRow(ctx, "SELECT coalesce(current_revision_id::text,'') FROM notice_v2_heads WHERE exchange=$1 AND notice_id=$2 FOR UPDATE", job.Exchange, job.NoticeID).Scan(&current)
	if err != nil {
		return out, err
	}
	if current != job.RevisionID {
		return out, pipelineFinish(ctx, tx, job, "SUPERSEDED", "STALE_INPUT")
	}
	// Reuse requires equal full manifests; hashes only narrow lookup. Fetch
	// observations carry their own times and do not manufacture new revisions.
	if current != "" {
		var previousRaw []byte
		err = tx.QueryRow(ctx, "SELECT payload FROM notice_v2_revisions WHERE revision_id=$1", current).Scan(&previousRaw)
		if err != nil {
			return out, err
		}
		var previous notice.PipelineRevision
		if err = json.Unmarshal(previousRaw, &previous); err != nil {
			return out, err
		}
		previousManifest, encodeErr := pipelineRevisionManifest(previous)
		if encodeErr != nil {
			return out, encodeErr
		}
		if bytes.Equal(previousManifest, encoded) {
			out.SavedID = current
			out.ProjectionCurrent = true
			return out, pipelineFinish(ctx, tx, job, "SUCCEEDED", "")
		}
	}
	rev.ID = notice.PipelineID()
	encoded, err = json.Marshal(rev)
	if err != nil {
		return out, err
	}
	_, err = tx.Exec(ctx, "INSERT INTO notice_v2_revisions(revision_id,exchange,notice_id,manifest_digest,payload) VALUES($1,$2,$3,$4,$5)", rev.ID, job.Exchange, job.NoticeID, digest, encoded)
	if err != nil {
		return out, err
	}
	for i, media := range rev.Media {
		_, err = tx.Exec(ctx, "INSERT INTO notice_v2_revision_media(revision_id,ordinal,original_url,object_id,fetch_status,error_code) VALUES($1,$2,$3,$4,$5,$6)", rev.ID, i, media.URL, nullPipelineID(media.ObjectID), media.Status, media.ErrorCode)
		if err != nil {
			return out, err
		}
	}
	_, err = tx.Exec(ctx, "UPDATE notice_v2_heads SET current_revision_id=$3,current_fact_set_id=NULL,current_summary_id=NULL,evidence_generation=evidence_generation+1,projection_generation=projection_generation+1 WHERE exchange=$1 AND notice_id=$2", job.Exchange, job.NoticeID, rev.ID)
	if err != nil {
		return out, err
	}
	if rev.Item.OriginalBodyText != "" {
		parts, partitionErr := notice.PipelinePartitions(rev)
		if partitionErr != nil {
			parts = nil
		}
		if len(parts) == 0 {
			parts = []notice.PipelineRevision{{ID: rev.ID}}
		}
		for i, part := range parts {
			partition := notice.PipelinePartition{Ordinal: i, Total: len(parts), Revision: part}
			err = pipelineEnqueue(ctx, tx, notice.PipelineJob{Exchange: job.Exchange, NoticeID: job.NoticeID, RevisionID: rev.ID, Stage: "SEMANTIC_GENERATE", Generation: job.Generation, InputDigest: notice.PipelineDigest(part)}, fmt.Sprintf("%s:%s:%d", rev.ID, notice.PipelinePolicy, i), partition)
			if err != nil {
				return out, err
			}
		}
	}
	if len(rev.Media) > 0 {
		err = pipelineEnqueue(ctx, tx, notice.PipelineJob{Exchange: job.Exchange, NoticeID: job.NoticeID, RevisionID: rev.ID, Stage: "OCR", Generation: job.Generation, InputDigest: notice.PipelineDigest(rev.Media)}, rev.ID+":ocr:apple-vision-1", struct{}{})
		if err != nil {
			return out, err
		}
	}
	out.SavedID = rev.ID
	out.ProjectionCurrent = true
	return out, pipelineFinish(ctx, tx, job, "SUCCEEDED", "")
}

func pipelineRevisionManifest(revision notice.PipelineRevision) ([]byte, error) {
	revision.ID = ""
	revision.Item.DetectedTSMS = 0
	revision.Item.DetailCheckedTSMS = 0
	return json.Marshal(revision)
}
