package postgres

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"market-share-go/internal/notice"
)

func pipelineOCR(ctx context.Context, tx pgx.Tx, req notice.PipelineRequest, job notice.PipelineJob) (notice.PipelineResponse, error) {
	out := notice.PipelineResponse{SchemaVersion: 2}
	d := req.OCR
	if d == nil || d.RevisionID != job.RevisionID || d.Engine != "apple_vision" || len(d.Rows) > 16 || (d.Status != "COMPLETE" && d.Status != "PARTIAL" && d.Status != "UNAVAILABLE") {
		return out, notice.ErrPipelineInvalid
	}
	if err := pipelineObjectBindings(ctx, tx, job, d.RawObjectIDs); err != nil {
		return out, err
	}
	var raw []byte
	if err := tx.QueryRow(ctx, "SELECT payload FROM notice_v2_revisions WHERE revision_id=$1", job.RevisionID).Scan(&raw); err != nil {
		return out, err
	}
	var rev notice.PipelineRevision
	if err := json.Unmarshal(raw, &rev); err != nil {
		return out, err
	}
	seen := map[string]bool{}
	for _, row := range d.Rows {
		status, _ := row["status"].(string)
		engine, _ := row["ocr_engine"].(string)
		text, _ := row["text"].(string)
		if engine != "apple_vision" || (status != "ANALYZED" && status != "NO_TEXT") || (status == "ANALYZED" && text == "") || (status == "NO_TEXT" && text != "") {
			return out, notice.ErrPipelineInvalid
		}
		url, _ := row["url"].(string)
		digest, _ := row["content_sha256"].(string)
		if seen[url] {
			return out, notice.ErrPipelineInvalid
		}
		seen[url] = true
		found := false
		for _, m := range rev.Media {
			if m.URL == url && m.ObjectID != "" {
				var expected string
				if err := tx.QueryRow(ctx, "SELECT sha256 FROM notice_v2_objects WHERE object_id=$1", m.ObjectID).Scan(&expected); err != nil {
					return out, err
				}
				if expected != digest {
					return out, notice.ErrPipelineConflict
				}
				found = true
			}
		}
		if !found {
			return out, notice.ErrPipelineInvalid
		}
	}
	if d.Status == "COMPLETE" {
		for _, m := range rev.Media {
			if !seen[m.URL] {
				return out, notice.ErrPipelineInvalid
			}
		}
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return out, err
	}
	out.SavedID = notice.PipelineID()
	_, err = tx.Exec(ctx, "INSERT INTO notice_v2_ocr(artifact_id,revision_id,payload) VALUES($1,$2,$3)", out.SavedID, job.RevisionID, raw)
	if err != nil {
		return out, err
	}
	status := "SUCCEEDED"
	if d.Status == "UNAVAILABLE" {
		status = "WAITING_INPUT"
	}
	return out, pipelineFinish(ctx, tx, job, status, d.Status)
}
