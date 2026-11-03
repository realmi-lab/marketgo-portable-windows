package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"market-share-go/internal/notice"
)

func pipelineSnapshot(ctx context.Context, tx pgx.Tx) ([]notice.Item, error) {
	rows, err := tx.Query(ctx, `SELECT r.payload,h.current_fact_set_id::text,h.current_summary_id::text,d.payload,
 greatest(r.created_at,coalesce((SELECT max(a.finished_at) FROM notice_v2_jobs j JOIN notice_v2_attempts a ON a.job_id=j.job_id WHERE j.exchange=h.exchange AND j.notice_id=h.notice_id AND j.stage='BODY' AND j.status='SUCCEEDED'),r.created_at)),
 EXISTS(SELECT 1 FROM notice_v2_jobs j WHERE j.exchange=h.exchange AND j.notice_id=h.notice_id AND j.stage='BODY' AND j.status IN ('PENDING','RUNNING','RETRY_WAIT')),
 coalesce((SELECT status FROM notice_v2_jobs j WHERE j.input_revision_id=r.revision_id AND j.stage IN ('SEMANTIC_GENERATE','SEMANTIC_AUDIT') ORDER BY j.created_at DESC LIMIT 1),'WAITING_INPUT')
 FROM notice_v2_heads h JOIN notice_v2_sources s ON s.exchange=h.exchange JOIN notice_v2_revisions r ON r.revision_id=h.current_revision_id LEFT JOIN notice_v2_derivations d ON d.derivation_id=h.current_fact_set_id WHERE s.read_mode='V2' ORDER BY r.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []notice.Item
	for rows.Next() {
		var raw, derived []byte
		var factID, summaryID *string
		var created time.Time
		var pending bool
		var modelState string
		if err = rows.Scan(&raw, &factID, &summaryID, &derived, &created, &pending, &modelState); err != nil {
			return nil, err
		}
		var revision notice.PipelineRevision
		if err = json.Unmarshal(raw, &revision); err != nil {
			return nil, err
		}
		item := revision.Item
		anchor := notice.RevisionTimestampMS(item.PublishedTSMS, item.UpdatedTSMS, item.DetectedTSMS)
		if anchor < time.Now().Add(-30*24*time.Hour).UnixMilli() {
			continue
		}
		q := &notice.PipelineQuality{SchemaVersion: 2, RevisionID: &revision.ID, FactSetID: factID, SummaryID: summaryID, BodyState: revision.Availability, FactsState: "PENDING", SummaryState: "PENDING", Freshness: "CURRENT_AS_OF", AnswerableScope: "STORED_ORIGINAL", RequiredConditions: []string{}}
		if parts, partErr := notice.PipelinePartitions(revision); partErr == nil {
			q.TotalPartitions = len(parts)
		}
		if modelState == "WAITING_INPUT" || modelState == "WAITING_BUDGET" || modelState == "NEEDS_REVIEW" || modelState == "CANCELLED" {
			q.FactsState = modelState
			q.SummaryState = modelState
		}
		if pending {
			q.Freshness = "UPDATE_PENDING"
		} else if time.Since(created) > 24*time.Hour {
			q.Freshness = "STALE"
		}
		item.Summary = ""
		item.Actions = nil
		item.Schedules = nil
		item.AnalysisStatus = "ANALYSIS_DEFERRED_QUOTA"
		item.ContentSource = "HERMES_RAW"
		item.SourceType = "HERMES"
		item.ContentHash = notice.PipelineDigest(revision)
		item.DetailCheckedTSMS = created.UnixMilli()
		item.BodyExcerpt = item.OriginalBodyText
		if len(derived) > 0 {
			var d notice.PipelineDerivation
			if err = json.Unmarshal(derived, &d); err != nil {
				return nil, err
			}
			q.FactsState = "COMPLETE"
			q.SummaryState = "NEEDS_REVIEW"
			if summaryID != nil {
				q.SummaryState = "COMPLETE"
				var lines []string
				for _, line := range d.Summary {
					lines = append(lines, line.Text)
				}
				item.Summary = strings.Join(lines, "\n")
				q.RequiredConditions = append([]string{}, d.RequiredConditions...)
				q.HasMoreConditions = len(q.RequiredConditions) > 0
			}
		}
		item.QualityV2 = q
		if strings.TrimSpace(item.OriginalBodyText) == "" {
			q.AnswerableScope = "IMAGE_ONLY"
			q.FactsState = "WAITING_INPUT"
			q.SummaryState = "WAITING_INPUT"
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	// Discovery-only heads remain visible to retrieval as unclassified source
	// candidates. A missing body must not become evidence of absence.
	pendingRows, err := tx.Query(ctx, `SELECT DISTINCT ON (h.exchange,h.notice_id) j.payload,j.status
 FROM notice_v2_heads h JOIN notice_v2_sources s ON s.exchange=h.exchange JOIN notice_v2_jobs j ON j.exchange=h.exchange AND j.notice_id=h.notice_id AND j.stage='BODY'
 WHERE h.current_revision_id IS NULL AND s.read_mode='V2' ORDER BY h.exchange,h.notice_id,j.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer pendingRows.Close()
	for pendingRows.Next() {
		var raw []byte
		var status string
		if err = pendingRows.Scan(&raw, &status); err != nil {
			return nil, err
		}
		var item notice.Item
		if err = json.Unmarshal(raw, &item); err != nil {
			return nil, err
		}
		if notice.RevisionTimestampMS(item.PublishedTSMS, item.UpdatedTSMS, item.DetectedTSMS) < time.Now().Add(-30*24*time.Hour).UnixMilli() {
			continue
		}
		item.Summary = ""
		item.OriginalBodyText = ""
		item.AnalysisStatus = "ANALYSIS_DEFERRED_QUOTA"
		item.ContentSource = "HERMES_RAW"
		item.SourceType = "HERMES"
		item.QualityV2 = &notice.PipelineQuality{SchemaVersion: 2, BodyState: status, FactsState: "WAITING_INPUT", SummaryState: "WAITING_INPUT", Freshness: "UNKNOWN", AnswerableScope: "LIST_ONLY", RequiredConditions: []string{}}
		items = append(items, item)
	}
	if err = pendingRows.Err(); err != nil {
		return nil, err
	}
	pendingRows.Close()
	if err = pipelineSectionQuality(ctx, tx, items); err != nil {
		return nil, err
	}
	return items, nil
}

func pipelineSectionQuality(ctx context.Context, tx pgx.Tx, items []notice.Item) error {
	byRevision := map[string]*notice.PipelineQuality{}
	itemByRevision := map[string]*notice.Item{}
	for i := range items {
		if q := items[i].QualityV2; q != nil && q.RevisionID != nil {
			byRevision[*q.RevisionID] = q
			itemByRevision[*q.RevisionID] = &items[i]
		}
	}
	rows, err := tx.Query(ctx, `SELECT d.revision_id::text,d.payload FROM notice_v2_derivations d JOIN notice_v2_heads h ON h.current_revision_id=d.revision_id JOIN notice_v2_sources s ON s.exchange=h.exchange WHERE s.read_mode='V2' AND d.kind='AUDITED' ORDER BY d.created_at`)
	if err != nil {
		return err
	}
	seen := map[string]map[int]bool{}
	for rows.Next() {
		var id string
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		q := byRevision[id]
		if q == nil {
			continue
		}
		var d notice.PipelineDerivation
		if err = json.Unmarshal(raw, &d); err != nil {
			rows.Close()
			return err
		}
		if d.Partition == nil || d.Partition.Total < 2 {
			continue
		}
		q.TotalPartitions = d.Partition.Total
		if d.Status != "COMPLETE" && d.Status != "PARTIAL" {
			continue
		}
		if seen[id] == nil {
			seen[id] = map[int]bool{}
		}
		if seen[id][d.Partition.Ordinal] {
			continue
		}
		seen[id][d.Partition.Ordinal] = true
		q.ReviewedPartitions++
		q.AnswerableScope = "REVIEWED_SECTIONS"
		q.FactsState = "PARTIAL"
		q.SummaryState = "SECTION_ONLY"
		if d.Status == "COMPLETE" {
			for _, line := range d.Summary {
				q.SectionSummaries = append(q.SectionSummaries, line.Text)
			}
			q.RequiredConditions = append(q.RequiredConditions, d.RequiredConditions...)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	rows, err = tx.Query(ctx, `SELECT DISTINCT ON(o.revision_id) o.revision_id::text,o.payload FROM notice_v2_ocr o JOIN notice_v2_heads h ON h.current_revision_id=o.revision_id ORDER BY o.revision_id,o.created_at DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			return err
		}
		if q := byRevision[id]; q != nil {
			var d notice.PipelineOCR
			if err = json.Unmarshal(raw, &d); err != nil {
				return err
			}
			q.OCRState = d.Status
			itemByRevision[id].ImageAnalysis = d.Rows
		}
	}
	return rows.Err()
}
