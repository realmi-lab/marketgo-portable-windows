package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"market-share-go/internal/notice"
)

var noticeExchanges = [...]string{"upbit", "bithumb", "coinone", "korbit", "gopax", "binance", "bybit", "okx"}

func (r *Repository) LoadNoticeSources(ctx context.Context) (map[string]notice.Source, error) {
	snapshot := notice.Snapshot{Sources: make(map[string]notice.Source, len(noticeExchanges))}
	for _, exchange := range noticeExchanges {
		snapshot.Sources[exchange] = notice.Source{HistoryStatus: "PENDING"}
	}
	if err := r.loadNoticeSources(ctx, &snapshot); err != nil {
		return nil, err
	}
	if err := r.loadNoticeErrors(ctx, &snapshot); err != nil {
		return nil, err
	}
	return snapshot.Sources, nil
}

func (r *Repository) LoadNoticeItem(ctx context.Context, exchange, noticeID string) (notice.Item, bool, error) {
	var item notice.Item
	var assets, actions, titleActions, schedules, riskTags, imageURLs, imageAnalysis string
	err := r.pool.QueryRow(ctx, `
		SELECT exchange_name, notice_id, title, url, category, published_ts_ms,
		       detected_ts_ms, timestamp_precision, assets_json, source_type,
		       updated_ts_ms, body_text, summary, schedules_json, risk_tags_json,
		       image_urls_json, image_text, image_analysis_json, content_source,
		       analysis_status, analysis_confidence, content_hash, detail_checked_ts_ms,
		       original_language, original_title, original_body_text,
		       translated_title_ko, translated_body_text_ko, translation_status,
		       importance, analysis_provider, analysis_model, reasoning_effort,
		       escalation_reason, analysis_policy_version, actions_json,
		       title_actions_json
		FROM exchange_notices WHERE exchange_name=$1 AND notice_id=$2`, exchange, noticeID).Scan(
		&item.Exchange, &item.NoticeID, &item.Title, &item.URL, &item.Category,
		&item.PublishedTSMS, &item.DetectedTSMS, &item.TimestampPrecision, &assets,
		&item.SourceType, &item.UpdatedTSMS, &item.BodyExcerpt, &item.Summary,
		&schedules, &riskTags, &imageURLs, &item.ImageText, &imageAnalysis,
		&item.ContentSource, &item.AnalysisStatus, &item.AnalysisConfidence,
		&item.ContentHash, &item.DetailCheckedTSMS, &item.OriginalLanguage,
		&item.OriginalTitle, &item.OriginalBodyText, &item.TranslatedTitleKO,
		&item.TranslatedBodyKO, &item.TranslationStatus, &item.Importance,
		&item.AnalysisProvider, &item.AnalysisModel, &item.ReasoningEffort,
		&item.EscalationReason, &item.AnalysisPolicy, &actions, &titleActions,
	)
	if err == pgx.ErrNoRows {
		return notice.Item{}, false, nil
	}
	if err != nil {
		return notice.Item{}, false, fmt.Errorf("read notice item: %w", err)
	}
	for _, field := range []struct {
		raw    string
		target any
	}{
		{assets, &item.Assets},
		{actions, &item.Actions},
		{titleActions, &item.TitleActions},
		{schedules, &item.Schedules},
		{riskTags, &item.RiskTags},
		{imageURLs, &item.ImageURLs},
		{imageAnalysis, &item.ImageAnalysis},
	} {
		if err := decodeNoticeJSON(field.raw, field.target); err != nil {
			return notice.Item{}, false, err
		}
	}
	return item, true, nil
}

func (r *Repository) NoticeSnapshot(ctx context.Context, since time.Time) (notice.Snapshot, error) {
	snapshot := notice.Snapshot{
		PollIntervalSec: 600, HistoryDays: notice.HistoryDays, OCRBackend: "unavailable",
		Sources: make(map[string]notice.Source, len(noticeExchanges)), Items: []notice.Item{}, ErrorHistory: []notice.SourceError{},
	}
	for _, exchange := range noticeExchanges {
		snapshot.Sources[exchange] = notice.Source{HistoryStatus: "PENDING", ErrorKind: "NOT_POLLED", Error: "not_polled"}
	}
	if err := r.loadNoticeSources(ctx, &snapshot); err != nil {
		return notice.Snapshot{}, err
	}
	if err := r.loadNoticeErrors(ctx, &snapshot); err != nil {
		return notice.Snapshot{}, err
	}
	if err := r.loadNoticeItems(ctx, since.UnixMilli(), &snapshot); err != nil {
		return notice.Snapshot{}, err
	}
	snapshot.Items = notice.AssignEventGroupIDs(snapshot.Items)
	snapshot.SourceCount = len(snapshot.Sources)
	for _, source := range snapshot.Sources {
		if source.Error == "" {
			snapshot.HealthySourceCount++
		}
		if noticeHistoryIsComplete(source.HistoryStatus) {
			snapshot.CompleteHistorySourceCount++
		}
	}
	return snapshot, nil
}

func noticeHistoryIsComplete(status string) bool {
	switch status {
	case "COMPLETE", "HERMES_30D_ACTIVE":
		return true
	default:
		return false
	}
}

func (r *Repository) loadNoticeSources(ctx context.Context, snapshot *notice.Snapshot) error {
	rows, err := r.pool.Query(ctx, `
		SELECT exchange_name, last_history_scan_ts, history_status, history_note,
		       oldest_fetched_ts_ms, pages_scanned
		FROM notice_source_state`)
	if err != nil {
		return fmt.Errorf("read notice sources: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var exchange string
		var source notice.Source
		if err := rows.Scan(&exchange, &source.LastHistoryScanTS, &source.HistoryStatus, &source.HistoryNote, &source.OldestFetchedTSMS, &source.PagesScanned); err != nil {
			return err
		}
		source.ErrorKind = ""
		source.Error = ""
		snapshot.Sources[exchange] = source
	}
	return rows.Err()
}

func (r *Repository) loadNoticeErrors(ctx context.Context, snapshot *notice.Snapshot) error {
	rows, err := r.pool.Query(ctx, `
		SELECT exchange_name, error_kind, message, started_ts, last_seen_ts,
		       resolved_ts, occurrence_count, blocked_until_ts
		FROM notice_source_errors ORDER BY started_ts DESC LIMIT 100`)
	if err != nil {
		return fmt.Errorf("read notice source errors: %w", err)
	}
	defer rows.Close()
	activeSeen := make(map[string]bool)
	for rows.Next() {
		var item notice.SourceError
		if err := rows.Scan(&item.Exchange, &item.ErrorKind, &item.Message, &item.StartedTS, &item.LastSeenTS, &item.ResolvedTS, &item.OccurrenceCount, &item.BlockedUntilTS); err != nil {
			return err
		}
		item.Active = item.ResolvedTS == 0
		snapshot.ErrorHistory = append(snapshot.ErrorHistory, item)
		if item.LastSeenTS*1000 > float64(snapshot.Version) {
			snapshot.Version = int64(item.LastSeenTS * 1000)
		}
		if !item.Active || activeSeen[item.Exchange] {
			continue
		}
		activeSeen[item.Exchange] = true
		snapshot.ActiveErrorCount++
		source := snapshot.Sources[item.Exchange]
		source.ErrorKind, source.Error = item.ErrorKind, item.Message
		source.ErrorStartedTS, source.ErrorLastSeenTS = item.StartedTS, item.LastSeenTS
		source.ErrorOccurrences, source.BlockedUntilTS = item.OccurrenceCount, item.BlockedUntilTS
		snapshot.Sources[item.Exchange] = source
	}
	return rows.Err()
}

func (r *Repository) loadNoticeItems(ctx context.Context, sinceMS int64, snapshot *notice.Snapshot) error {
	rows, err := r.pool.Query(ctx, `
		SELECT exchange_name, notice_id, title, url, category, published_ts_ms,
		       detected_ts_ms, timestamp_precision, assets_json, source_type,
		       updated_ts_ms, LEFT(body_text, 1500), summary, schedules_json,
		       risk_tags_json, image_urls_json, LEFT(image_text, 1500),
		       image_analysis_json, content_source, analysis_status,
		       analysis_confidence, content_hash, detail_checked_ts_ms, revision_ts_ms,
		       original_language, original_title, original_body_text,
		       translated_title_ko, translated_body_text_ko, translation_status,
		       importance, analysis_provider, analysis_model, reasoning_effort,
		       escalation_reason, analysis_policy_version, actions_json,
		       title_actions_json
		FROM exchange_notices
		WHERE revision_ts_ms >= $1
		ORDER BY sort_ts_ms DESC, exchange_name, notice_id`, sinceMS)
	if err != nil {
		return fmt.Errorf("read exchange notices: %w", err)
	}
	defer rows.Close()
	return scanNoticeItems(rows, snapshot)
}

func scanNoticeItems(rows pgx.Rows, snapshot *notice.Snapshot, itemVersions ...map[string]string) error {
	for rows.Next() {
		var item notice.Item
		var assets, actions, titleActions, schedules, riskTags, imageURLs, imageAnalysis string
		var revisionTS int64
		var itemVersion string
		targets := []any{
			&item.Exchange, &item.NoticeID, &item.Title, &item.URL, &item.Category,
			&item.PublishedTSMS, &item.DetectedTSMS, &item.TimestampPrecision, &assets,
			&item.SourceType, &item.UpdatedTSMS, &item.BodyExcerpt, &item.Summary,
			&schedules, &riskTags, &imageURLs, &item.ImageText, &imageAnalysis,
			&item.ContentSource, &item.AnalysisStatus, &item.AnalysisConfidence,
			&item.ContentHash, &item.DetailCheckedTSMS, &revisionTS,
			&item.OriginalLanguage, &item.OriginalTitle, &item.OriginalBodyText,
			&item.TranslatedTitleKO, &item.TranslatedBodyKO, &item.TranslationStatus,
			&item.Importance, &item.AnalysisProvider, &item.AnalysisModel,
			&item.ReasoningEffort, &item.EscalationReason, &item.AnalysisPolicy, &actions, &titleActions,
		}
		if len(itemVersions) > 0 {
			targets = append(targets, &itemVersion)
		}
		if err := rows.Scan(targets...); err != nil {
			return err
		}
		if len(itemVersions) > 0 {
			itemVersions[0][item.Exchange+":"+item.NoticeID] = itemVersion
		}
		if err := decodeNoticeJSON(assets, &item.Assets); err != nil {
			return err
		}
		if err := decodeNoticeJSON(actions, &item.Actions); err != nil {
			return err
		}
		if err := decodeNoticeJSON(titleActions, &item.TitleActions); err != nil {
			return err
		}
		if err := decodeNoticeJSON(schedules, &item.Schedules); err != nil {
			return err
		}
		if err := decodeNoticeJSON(riskTags, &item.RiskTags); err != nil {
			return err
		}
		if err := decodeNoticeJSON(imageURLs, &item.ImageURLs); err != nil {
			return err
		}
		if err := decodeNoticeJSON(imageAnalysis, &item.ImageAnalysis); err != nil {
			return err
		}
		snapshot.Items = append(snapshot.Items, item)
		if revisionTS > snapshot.Version {
			snapshot.Version = revisionTS
		}
	}
	return rows.Err()
}

func decodeNoticeJSON(raw string, target any) error {
	if raw == "" {
		raw = "[]"
	}
	if err := json.Unmarshal([]byte(raw), target); err != nil {
		return fmt.Errorf("decode notice JSON: %w", err)
	}
	return nil
}
