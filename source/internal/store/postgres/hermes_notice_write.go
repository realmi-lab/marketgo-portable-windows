package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"market-share-go/internal/notice"
)

func (r *Repository) SaveHermesNoticeBatch(
	ctx context.Context,
	request notice.HermesIngestRequest,
) (notice.HermesIngestResult, error) {
	result := notice.HermesIngestResult{
		BatchID:       request.BatchID,
		Accepted:      len(request.Items),
		WindowStartMS: request.WindowStartMS,
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return notice.HermesIngestResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := pipelineLegacyAdmission(ctx, tx, request); err != nil { return notice.HermesIngestResult{}, err }
	if err := checkHermesNoticePreconditions(ctx, tx, request); err != nil { return notice.HermesIngestResult{}, err }

	exchanges := make(map[string]struct{}, len(request.ScannedExchanges)+len(request.Items))
	for _, exchange := range request.ScannedExchanges {
		exchanges[exchange] = struct{}{}
	}
	for _, item := range request.Items {
		payload, marshalErr := json.Marshal(item)
		if marshalErr != nil {
			return notice.HermesIngestResult{}, fmt.Errorf("marshal Hermes notice %s/%s: %w", item.Exchange, item.NoticeID, marshalErr)
		}
		imageURLs, marshalErr := json.Marshal(nonNilStrings(item.ImageURLs))
		if marshalErr != nil {
			return notice.HermesIngestResult{}, fmt.Errorf("marshal Hermes image URLs %s/%s: %w", item.Exchange, item.NoticeID, marshalErr)
		}
		command, insertErr := tx.Exec(ctx, `INSERT INTO hermes_notice_revisions(
            exchange_name, notice_id, content_hash, original_language,
            original_title, original_body_text, image_urls_json,
            source_payload, fetched_ts_ms
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9)
        ON CONFLICT(exchange_name, notice_id, content_hash) DO NOTHING`,
			item.Exchange, item.NoticeID, item.ContentHash, item.OriginalLanguage,
			item.OriginalTitle, item.OriginalBodyText, string(imageURLs), string(payload),
			item.DetailCheckedTSMS,
		)
		if insertErr != nil {
			return notice.HermesIngestResult{}, fmt.Errorf("save Hermes revision %s/%s: %w", item.Exchange, item.NoticeID, insertErr)
		}
		if command.RowsAffected() > 0 {
			result.CreatedRevisions++
		}
		if err := upsertNotice(ctx, tx, item); err != nil {
			return notice.HermesIngestResult{}, err
		}
		if _, insertErr = tx.Exec(ctx, `INSERT INTO hermes_notice_analysis_runs(
            batch_id, exchange_name, notice_id, content_hash, status,
            provider, model, reasoning_effort, escalation_reason,
            policy_version, result_payload
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb)
        ON CONFLICT(batch_id, exchange_name, notice_id) DO NOTHING`,
			request.BatchID, item.Exchange, item.NoticeID, item.ContentHash,
			item.AnalysisStatus, item.AnalysisProvider, item.AnalysisModel,
			item.ReasoningEffort, item.EscalationReason, item.AnalysisPolicy,
			string(payload),
		); insertErr != nil {
			return notice.HermesIngestResult{}, fmt.Errorf("save Hermes analysis run %s/%s: %w", item.Exchange, item.NoticeID, insertErr)
		}
		// An item receipt proves persistence, not completion of a 30-day scan.
		// Only the collector's explicit completed-source heartbeat advances it.
	}

	now := time.Now()
	for exchange := range exchanges {
		_, err = tx.Exec(ctx, `INSERT INTO notice_source_state(
            exchange_name, last_history_scan_ts, history_status, history_note,
            oldest_fetched_ts_ms, pages_scanned
        ) VALUES ($1,$2,'HERMES_30D_ACTIVE','Hermes recent 30-day ingest', $3, 0)
        ON CONFLICT(exchange_name) DO UPDATE SET
            last_history_scan_ts=EXCLUDED.last_history_scan_ts,
            history_status=EXCLUDED.history_status,
            history_note=EXCLUDED.history_note,
            oldest_fetched_ts_ms=CASE
                WHEN notice_source_state.oldest_fetched_ts_ms=0 THEN EXCLUDED.oldest_fetched_ts_ms
                ELSE LEAST(notice_source_state.oldest_fetched_ts_ms, EXCLUDED.oldest_fetched_ts_ms)
            END`,
			exchange, float64(now.UnixNano())/1e9, request.WindowStartMS,
		)
		if err != nil {
			return notice.HermesIngestResult{}, fmt.Errorf("save Hermes source state %s: %w", exchange, err)
		}
		_, err = tx.Exec(ctx, `UPDATE notice_source_errors SET resolved_ts=$2
            WHERE exchange_name=$1 AND resolved_ts=0`, exchange, float64(now.UnixNano())/1e9)
		if err != nil {
			return notice.HermesIngestResult{}, fmt.Errorf("resolve notice source errors %s: %w", exchange, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return notice.HermesIngestResult{}, fmt.Errorf("commit Hermes notice batch: %w", err)
	}
	result.ScannedSources = len(exchanges)
	if result.Accepted > 0 {
		r.movementCandidates.invalidate()
	}
	result.Unchanged = result.Accepted - result.CreatedRevisions
	return result, nil
}
