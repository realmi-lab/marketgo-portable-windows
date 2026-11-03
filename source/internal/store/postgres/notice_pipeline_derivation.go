package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"market-share-go/internal/notice"
)

func pipelineDerivation(ctx context.Context, tx pgx.Tx, req notice.PipelineRequest, job notice.PipelineJob) (notice.PipelineResponse, error) {
	out := notice.PipelineResponse{SchemaVersion: 2}
	if req.Derivation == nil {
		return out, notice.ErrPipelineInvalid
	}
	d := *req.Derivation
	var raw []byte
	err := tx.QueryRow(ctx, "SELECT payload FROM notice_v2_revisions WHERE revision_id=$1", job.RevisionID).Scan(&raw)
	if err != nil {
		return out, err
	}
	var revision notice.PipelineRevision
	if err = json.Unmarshal(raw, &revision); err != nil {
		return out, err
	}
	audited := job.Stage == "SEMANTIC_AUDIT"
	var jobPayload []byte
	if err = tx.QueryRow(ctx, "SELECT payload FROM notice_v2_jobs WHERE job_id=$1", job.ID).Scan(&jobPayload); err != nil {
		return out, err
	}
	if !audited {
		var expected notice.PipelinePartition
		if err = json.Unmarshal(jobPayload, &expected); err != nil {
			return out, err
		}
		if expected.Total > 0 && (d.Partition == nil || notice.PipelineDigest(*d.Partition) != notice.PipelineDigest(expected)) {
			return out, notice.ErrPipelineConflict
		}
	}
	scope := revision
	if d.Partition != nil {
		parts, partErr := notice.PipelinePartitions(revision)
		p := d.Partition
		if partErr != nil || p.Ordinal < 0 || p.Ordinal >= len(parts) || p.Total != len(parts) || notice.PipelineDigest(p.Revision) != notice.PipelineDigest(parts[p.Ordinal]) {
			return out, notice.ErrPipelineConflict
		}
		scope = parts[p.Ordinal]
	}
	if err = notice.ValidatePipelineDerivation(scope, d, audited); err != nil {
		return out, err
	}
	if err = pipelineObjectBindings(ctx, tx, job, d.RawObjectIDs); err != nil {
		return out, err
	}
	var current string
	err = tx.QueryRow(ctx, "SELECT coalesce(current_revision_id::text,'') FROM notice_v2_heads WHERE exchange=$1 AND notice_id=$2 FOR UPDATE", job.Exchange, job.NoticeID).Scan(&current)
	if err != nil {
		return out, err
	}
	kind := "CANDIDATE"
	d.Status = "CANDIDATE"
	parent := ""
	if audited {
		var candidateRaw []byte
		err = tx.QueryRow(ctx, "SELECT payload FROM notice_v2_jobs WHERE job_id=$1", job.ID).Scan(&candidateRaw)
		if err != nil {
			return out, err
		}
		var candidate notice.PipelineDerivation
		if err = json.Unmarshal(candidateRaw, &candidate); err != nil {
			return out, err
		}
		// Audit can attach a verdict; it may not rewrite the candidate facts.
		parent = candidate.ID
		candidate.Audit = d.Audit
		candidate.RawObjectIDs = d.RawObjectIDs
		candidate.Status = d.Status
		candidate.ID = d.ID
		if notice.PipelineDigest(candidate) != notice.PipelineDigest(d) {
			return out, notice.ErrPipelineConflict
		}
		kind = "AUDITED"
		d.Status = "REJECTED"
		if d.Audit.FactsVerdict == "SUPPORTED" && len(d.Audit.MissingFacts) == 0 {
			d.Status = "PARTIAL"
			if d.Audit.SummaryVerdict == "SUPPORTED" && len(d.Audit.MissingConditions) == 0 {
				d.Status = "COMPLETE"
			}
		}
	}
	d.ID = notice.PipelineID()
	encoded, err := json.Marshal(d)
	if err != nil {
		return out, err
	}
	_, err = tx.Exec(ctx, "INSERT INTO notice_v2_derivations(derivation_id,revision_id,kind,input_digest,policy,status,payload) VALUES($1,$2,$3,$4,$5,$6,$7)", d.ID, job.RevisionID, kind, job.InputDigest, d.Policy, d.Status, encoded)
	if err != nil {
		return out, err
	}
	if audited {
		_, err = tx.Exec(ctx, "INSERT INTO notice_v2_derivation_inputs(derivation_id,input_derivation_id,role) VALUES($1,$2,'CANDIDATE')", d.ID, parent)
		if err != nil {
			return out, err
		}
		if current == job.RevisionID && d.Status != "REJECTED" && (d.Partition == nil || d.Partition.Total == 1) {
			for i, f := range d.Facts {
				payload, encodeErr := json.Marshal(f)
				if encodeErr != nil {
					return out, encodeErr
				}
				_, err = tx.Exec(ctx, "INSERT INTO notice_v2_facts(fact_set_id,ordinal,payload) VALUES($1,$2,$3)", d.ID, i, payload)
				if err != nil {
					return out, err
				}
			}
			var summary any
			if d.Status == "COMPLETE" {
				summary = d.ID
			}
			command, updateErr := tx.Exec(ctx, "UPDATE notice_v2_heads SET current_fact_set_id=$4,current_summary_id=$5,projection_generation=projection_generation+1 WHERE exchange=$1 AND notice_id=$2 AND current_revision_id=$3", job.Exchange, job.NoticeID, job.RevisionID, d.ID, summary)
			if updateErr != nil {
				return out, updateErr
			}
			out.ProjectionCurrent = command.RowsAffected() == 1
		}
	} else {
		err = pipelineEnqueue(ctx, tx, notice.PipelineJob{Exchange: job.Exchange, NoticeID: job.NoticeID, Stage: "SEMANTIC_AUDIT", RevisionID: job.RevisionID, InputDigest: notice.PipelineDigest(d), Generation: job.Generation}, d.ID+":audit", d)
		if err != nil {
			return out, err
		}
	}
	out.SavedID = d.ID
	status, reason := "SUCCEEDED", ""
	if current != job.RevisionID {
		status, reason = "SUPERSEDED", "STALE_INPUT"
	}
	if d.Status == "REJECTED" {
		status, reason = "NEEDS_REVIEW", "UNSUPPORTED_OR_INCOMPLETE"
	}
	return out, pipelineFinish(ctx, tx, job, status, reason)
}
