package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"market-share-go/internal/notice"
)

func (r *Repository) SaveNoticePoll(
	ctx context.Context,
	exchange string,
	items []notice.Item,
	source notice.Source,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, item := range items {
		if err := upsertNotice(ctx, tx, item); err != nil {
			return err
		}
	}
	now := time.Now()
	_, err = tx.Exec(ctx, `INSERT INTO notice_source_state(
        exchange_name, last_history_scan_ts, history_status, history_note,
        oldest_fetched_ts_ms, pages_scanned
    ) VALUES ($1,$2,$3,$4,$5,$6)
    ON CONFLICT(exchange_name) DO UPDATE SET
        last_history_scan_ts=EXCLUDED.last_history_scan_ts,
        history_status=EXCLUDED.history_status,
        history_note=EXCLUDED.history_note,
        oldest_fetched_ts_ms=EXCLUDED.oldest_fetched_ts_ms,
        pages_scanned=EXCLUDED.pages_scanned`,
		exchange, source.LastHistoryScanTS, source.HistoryStatus, source.HistoryNote,
		source.OldestFetchedTSMS, source.PagesScanned)
	if err != nil {
		return fmt.Errorf("save notice source state: %w", err)
	}
	_, err = tx.Exec(ctx, `UPDATE notice_source_errors SET resolved_ts=$2
        WHERE exchange_name=$1 AND resolved_ts=0`, exchange, float64(now.UnixNano())/1e9)
	if err != nil {
		return fmt.Errorf("resolve notice source errors: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	r.movementCandidates.invalidate()
	return nil
}

func upsertNotice(ctx context.Context, tx pgx.Tx, item notice.Item) error {
	assets, err := json.Marshal(nonNilStrings(item.Assets))
	if err != nil {
		return err
	}
	actions, err := json.Marshal(nonNilActions(item.Actions))
	if err != nil {
		return err
	}
	titleActions, err := json.Marshal(nonNilActions(notice.InferTitleActions(item.Title, item.Category)))
	if err != nil {
		return err
	}
	schedules, err := json.Marshal(nonNilSchedules(item.Schedules))
	if err != nil {
		return err
	}
	riskTags, err := json.Marshal(nonNilStrings(item.RiskTags))
	if err != nil {
		return err
	}
	imageURLs, err := json.Marshal(nonNilStrings(item.ImageURLs))
	if err != nil {
		return err
	}
	imageAnalysis := item.ImageAnalysis
	if imageAnalysis == nil {
		imageAnalysis = []map[string]any{}
	}
	imageAnalysisJSON, err := json.Marshal(imageAnalysis)
	if err != nil {
		return err
	}
	sortTS := notice.PublicationTimestampMS(item.PublishedTSMS, item.UpdatedTSMS, item.DetectedTSMS)
	revisionTS := notice.RevisionTimestampMS(item.PublishedTSMS, item.UpdatedTSMS, item.DetectedTSMS)
	command, err := tx.Exec(ctx, `INSERT INTO exchange_notices(
        exchange_name, notice_id, title, url, category, published_ts_ms,
        detected_ts_ms, timestamp_precision, assets_json, source_type,
        updated_ts_ms, sort_ts_ms, body_text, summary, schedules_json,
        risk_tags_json, image_urls_json, image_text, image_analysis_json,
        content_source, analysis_status, analysis_confidence, content_hash,
        detail_checked_ts_ms, original_language, original_title,
        original_body_text, translated_title_ko, translated_body_text_ko,
        translation_status, importance, analysis_provider, analysis_model,
        reasoning_effort, escalation_reason, analysis_policy_version,
        actions_json, title_actions_json, revision_ts_ms
    ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39)
    ON CONFLICT(exchange_name, notice_id) DO UPDATE SET
        title=EXCLUDED.title, url=EXCLUDED.url, category=EXCLUDED.category,
        published_ts_ms=EXCLUDED.published_ts_ms,
        timestamp_precision=EXCLUDED.timestamp_precision,
        assets_json=CASE
            WHEN EXCLUDED.source_type='HERMES' THEN EXCLUDED.assets_json
            WHEN EXCLUDED.assets_json <> '[]' THEN EXCLUDED.assets_json
            ELSE exchange_notices.assets_json END,
        actions_json=CASE
            WHEN EXCLUDED.source_type='HERMES' THEN EXCLUDED.actions_json
            WHEN EXCLUDED.actions_json <> '[]' THEN EXCLUDED.actions_json
            ELSE exchange_notices.actions_json END,
        title_actions_json=EXCLUDED.title_actions_json,
        source_type=EXCLUDED.source_type, updated_ts_ms=EXCLUDED.updated_ts_ms,
        sort_ts_ms=EXCLUDED.sort_ts_ms, revision_ts_ms=EXCLUDED.revision_ts_ms,
        body_text=CASE WHEN EXCLUDED.source_type='HERMES' OR EXCLUDED.body_text <> '' THEN EXCLUDED.body_text ELSE exchange_notices.body_text END,
        summary=CASE WHEN EXCLUDED.source_type='HERMES' OR EXCLUDED.summary <> '' THEN EXCLUDED.summary ELSE exchange_notices.summary END,
        schedules_json=CASE WHEN EXCLUDED.source_type='HERMES' OR EXCLUDED.schedules_json <> '[]' THEN EXCLUDED.schedules_json ELSE exchange_notices.schedules_json END,
        risk_tags_json=CASE WHEN EXCLUDED.source_type='HERMES' OR EXCLUDED.risk_tags_json <> '[]' THEN EXCLUDED.risk_tags_json ELSE exchange_notices.risk_tags_json END,
        image_urls_json=CASE WHEN EXCLUDED.source_type='HERMES' OR EXCLUDED.image_urls_json <> '[]' THEN EXCLUDED.image_urls_json ELSE exchange_notices.image_urls_json END,
        image_text=CASE WHEN EXCLUDED.source_type='HERMES' OR EXCLUDED.image_text <> '' THEN EXCLUDED.image_text ELSE exchange_notices.image_text END,
        image_analysis_json=CASE WHEN EXCLUDED.source_type='HERMES' OR EXCLUDED.image_analysis_json <> '[]' THEN EXCLUDED.image_analysis_json ELSE exchange_notices.image_analysis_json END,
        content_source=CASE WHEN EXCLUDED.content_source <> 'LIST_ONLY' THEN EXCLUDED.content_source ELSE exchange_notices.content_source END,
        analysis_status=CASE
            WHEN EXCLUDED.content_hash=exchange_notices.content_hash
                 AND exchange_notices.analysis_status='ANALYZED'
                 AND EXCLUDED.analysis_status IN ('ANALYSIS_DEFERRED_QUOTA','TRANSLATION_PENDING','ANALYSIS_FAILED')
            THEN exchange_notices.analysis_status
            WHEN EXCLUDED.analysis_status <> 'LIST_ONLY' THEN EXCLUDED.analysis_status
            ELSE exchange_notices.analysis_status END,
        analysis_confidence=CASE WHEN EXCLUDED.content_hash<>exchange_notices.content_hash
            THEN EXCLUDED.analysis_confidence
            ELSE GREATEST(exchange_notices.analysis_confidence, EXCLUDED.analysis_confidence) END,
        content_hash=CASE WHEN EXCLUDED.content_hash <> '' THEN EXCLUDED.content_hash ELSE exchange_notices.content_hash END,
        detail_checked_ts_ms=GREATEST(exchange_notices.detail_checked_ts_ms, EXCLUDED.detail_checked_ts_ms),
        original_language=CASE WHEN EXCLUDED.original_language<>'' THEN EXCLUDED.original_language ELSE exchange_notices.original_language END,
        original_title=CASE WHEN EXCLUDED.original_title<>'' THEN EXCLUDED.original_title ELSE exchange_notices.original_title END,
        original_body_text=CASE WHEN EXCLUDED.source_type='HERMES' OR EXCLUDED.original_body_text<>'' THEN EXCLUDED.original_body_text ELSE exchange_notices.original_body_text END,
        translated_title_ko=CASE
            WHEN EXCLUDED.content_hash<>exchange_notices.content_hash THEN EXCLUDED.translated_title_ko
            WHEN EXCLUDED.translated_title_ko<>'' THEN EXCLUDED.translated_title_ko
            ELSE exchange_notices.translated_title_ko END,
        translated_body_text_ko=CASE
            WHEN EXCLUDED.content_hash<>exchange_notices.content_hash THEN EXCLUDED.translated_body_text_ko
            WHEN EXCLUDED.translated_body_text_ko<>'' THEN EXCLUDED.translated_body_text_ko
            ELSE exchange_notices.translated_body_text_ko END,
        translation_status=CASE
            WHEN EXCLUDED.content_hash<>exchange_notices.content_hash THEN EXCLUDED.translation_status
            WHEN EXCLUDED.translation_status<>'' THEN EXCLUDED.translation_status
            ELSE exchange_notices.translation_status END,
        importance=CASE WHEN EXCLUDED.importance<>'' THEN EXCLUDED.importance ELSE exchange_notices.importance END,
        analysis_provider=CASE
            WHEN EXCLUDED.content_hash<>exchange_notices.content_hash THEN EXCLUDED.analysis_provider
            WHEN EXCLUDED.analysis_provider<>'' THEN EXCLUDED.analysis_provider
            ELSE exchange_notices.analysis_provider END,
        analysis_model=CASE
            WHEN EXCLUDED.content_hash<>exchange_notices.content_hash THEN EXCLUDED.analysis_model
            WHEN EXCLUDED.analysis_model<>'' THEN EXCLUDED.analysis_model
            ELSE exchange_notices.analysis_model END,
        reasoning_effort=CASE
            WHEN EXCLUDED.content_hash<>exchange_notices.content_hash THEN EXCLUDED.reasoning_effort
            WHEN EXCLUDED.reasoning_effort<>'' THEN EXCLUDED.reasoning_effort
            ELSE exchange_notices.reasoning_effort END,
        escalation_reason=CASE
            WHEN EXCLUDED.content_hash<>exchange_notices.content_hash THEN EXCLUDED.escalation_reason
            WHEN EXCLUDED.escalation_reason<>'' THEN EXCLUDED.escalation_reason
            ELSE exchange_notices.escalation_reason END,
        analysis_policy_version=CASE WHEN EXCLUDED.analysis_policy_version<>'' THEN EXCLUDED.analysis_policy_version ELSE exchange_notices.analysis_policy_version END
	WHERE NOT (
		EXCLUDED.analysis_status='ANALYSIS_DEFERRED_QUOTA'
		AND exchange_notices.analysis_status='ANALYZED'
		AND (
			EXCLUDED.content_source='HERMES_RAW'
			OR EXCLUDED.content_hash=exchange_notices.content_hash
		)
	) AND NOT (
		EXCLUDED.content_source='HERMES_RAW'
		AND EXCLUDED.analysis_status='ANALYSIS_DEFERRED_QUOTA'
		AND exchange_notices.content_source IN ('HERMES_RAW_DETAIL','HERMES_RAW_DETAIL_EMPTY')
	)`,
		item.Exchange, item.NoticeID, item.Title, item.URL, item.Category,
		item.PublishedTSMS, item.DetectedTSMS, item.TimestampPrecision, string(assets),
		item.SourceType, item.UpdatedTSMS, sortTS, item.BodyExcerpt, item.Summary,
		string(schedules), string(riskTags), string(imageURLs), item.ImageText,
		string(imageAnalysisJSON), item.ContentSource, item.AnalysisStatus,
		item.AnalysisConfidence, item.ContentHash, item.DetailCheckedTSMS,
		item.OriginalLanguage, item.OriginalTitle, item.OriginalBodyText,
		item.TranslatedTitleKO, item.TranslatedBodyKO, item.TranslationStatus,
		item.Importance, item.AnalysisProvider, item.AnalysisModel,
		item.ReasoningEffort, item.EscalationReason, item.AnalysisPolicy,
		string(actions), string(titleActions), revisionTS)
	if err != nil {
		return fmt.Errorf("upsert %s notice %s: %w", item.Exchange, item.NoticeID, err)
	}
	// Discovery checkpoints make new notices durable before detail fetch, and
	// detail checkpoints make official evidence durable before model analysis.
	// A stale or concurrent collector must not use either one to erase richer
	// evidence or a same-revision analyzed result already persisted by another
	// run. When the conflict WHERE above rejects the update, preserve normalized
	// schedules as well.
	if command.RowsAffected() == 0 {
		return nil
	}
	if strings.EqualFold(item.SourceType, "HERMES") || len(item.Schedules) > 0 {
		if err := replaceNoticeSchedules(ctx, tx, item); err != nil {
			return err
		}
	}
	return nil
}

func replaceNoticeSchedules(ctx context.Context, tx pgx.Tx, item notice.Item) error {
	if _, err := tx.Exec(ctx, `DELETE FROM exchange_notice_schedules
        WHERE exchange_name=$1 AND notice_id=$2`, item.Exchange, item.NoticeID); err != nil {
		return fmt.Errorf("clear %s notice %s schedules: %w", item.Exchange, item.NoticeID, err)
	}
	for index, schedule := range item.Schedules {
		_, err := tx.Exec(ctx, `INSERT INTO exchange_notice_schedules(
            exchange_name, notice_id, schedule_no, kind, start_ts_ms, end_ts_ms,
            timezone, precision_value, evidence
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			item.Exchange, item.NoticeID, index, schedule.Kind,
			schedule.StartTSMS, schedule.EndTSMS, schedule.Timezone,
			schedule.Precision, schedule.Evidence)
		if err != nil {
			return fmt.Errorf("insert %s notice %s schedule %d: %w", item.Exchange, item.NoticeID, index, err)
		}
	}
	return nil
}

func (r *Repository) SaveNoticePollError(
	ctx context.Context,
	exchange, errorKind, message string,
	blockedUntil time.Time,
) error {
	now := float64(time.Now().UnixNano()) / 1e9
	blocked := float64(0)
	if !blockedUntil.IsZero() {
		blocked = float64(blockedUntil.UnixNano()) / 1e9
	}
	command, err := r.pool.Exec(ctx, `UPDATE notice_source_errors SET
        message=$3, last_seen_ts=$4, occurrence_count=occurrence_count+1,
        blocked_until_ts=$5
        WHERE id=(SELECT id FROM notice_source_errors
            WHERE exchange_name=$1 AND error_kind=$2 AND resolved_ts=0
            ORDER BY id DESC LIMIT 1)`, exchange, errorKind, message, now, blocked)
	if err != nil {
		return fmt.Errorf("update notice poll error: %w", err)
	}
	if command.RowsAffected() > 0 {
		return nil
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO notice_source_errors(
        exchange_name, error_kind, message, started_ts, last_seen_ts,
        occurrence_count, blocked_until_ts
    ) VALUES ($1,$2,$3,$4,$4,1,$5)`, exchange, errorKind, message, now, blocked)
	if err != nil {
		return fmt.Errorf("insert notice poll error: %w", err)
	}
	return nil
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilSchedules(values []notice.Schedule) []notice.Schedule {
	if values == nil {
		return []notice.Schedule{}
	}
	return values
}

func nonNilActions(values []notice.Action) []notice.Action {
	if values == nil {
		return []notice.Action{}
	}
	return values
}
