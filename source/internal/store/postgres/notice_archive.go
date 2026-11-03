package postgres

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"market-share-go/internal/notice"
	"time"
)

// Each page reads one repeatable snapshot. A bounded set fingerprint includes
// xmin so body/analysis updates and publication-date revisions invalidate resume.
// This is archive consistency, not proof that official-source collection is complete.
func (r *Repository) NoticeArchivePage(ctx context.Context, q notice.ArchiveQuery) (notice.ArchivePage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cursor, err := q.Validate()
	if err != nil {
		return notice.ArchivePage{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return notice.ArchivePage{}, err
	}
	defer tx.Rollback(ctx)
	var count int
	var revision string
	err = tx.QueryRow(ctx, `SELECT count(*), md5(COALESCE(string_agg(identity, ',' ORDER BY identity), '')) FROM (
		SELECT json_build_array(exchange_name, notice_id, published_ts_ms, xmin::text)::text AS identity
		FROM exchange_notices WHERE published_ts_ms >= $1 AND published_ts_ms < $2
		AND exchange_name IN ('upbit','bithumb','coinone','korbit','gopax')
		LIMIT 10001) bounded`, q.FromTSMS, q.ToTSMS).Scan(&count, &revision)
	if err != nil {
		return notice.ArchivePage{}, err
	}
	if count > 10000 {
		return notice.ArchivePage{}, fmt.Errorf("%w: notice archive exceeds 10000 rows; narrow the interval", notice.ErrHermesIngestInvalid)
	}
	if q.Cursor != "" && cursor.Revision != revision {
		return notice.ArchivePage{}, fmt.Errorf("%w: notice archive changed; restart interval read before using any pages", notice.ErrHermesIngestInvalid)
	}
	rows, err := tx.Query(ctx, `SELECT exchange_name, notice_id, title, url, category, published_ts_ms,
	 detected_ts_ms, timestamp_precision, assets_json, source_type,
	 updated_ts_ms, body_text, summary, schedules_json, risk_tags_json, image_urls_json,
	 image_text, image_analysis_json, content_source, analysis_status, analysis_confidence,
	 content_hash, detail_checked_ts_ms, revision_ts_ms, original_language, original_title,
	 original_body_text, translated_title_ko, translated_body_text_ko, translation_status,
	 importance, analysis_provider, analysis_model, reasoning_effort, escalation_reason,
	 analysis_policy_version, actions_json, title_actions_json, xmin::text
	 FROM exchange_notices WHERE published_ts_ms >= $1 AND published_ts_ms < $2
	 AND exchange_name IN ('upbit','bithumb','coinone','korbit','gopax')
	 AND ($3::bigint = 0 OR (published_ts_ms, exchange_name, notice_id) > ($3,$4,$5))
	 ORDER BY published_ts_ms, exchange_name, notice_id LIMIT $6`, q.FromTSMS, q.ToTSMS, cursor.PublishedTSMS, cursor.Exchange, cursor.NoticeID, q.Limit+1)
	if err != nil {
		return notice.ArchivePage{}, err
	}
	snapshot := notice.Snapshot{Items: []notice.Item{}}
	versions := make(map[string]string)
	err = scanNoticeItems(rows, &snapshot, versions)
	rows.Close()
	if err != nil {
		return notice.ArchivePage{}, err
	}
	page := notice.ArchivePage{Items: snapshot.Items, FromTSMS: q.FromTSMS, ToTSMS: q.ToTSMS, Revision: revision, ItemRevisions: versions}
	if len(page.Items) > q.Limit {
		omitted := page.Items[q.Limit]
		delete(page.ItemRevisions, omitted.Exchange+":"+omitted.NoticeID)
		page.Items = page.Items[:q.Limit]
		page.HasMore = true
		page.NextCursor = q.CursorAfter(page.Items[len(page.Items)-1], revision)
	}
	if err := tx.Commit(ctx); err != nil {
		return notice.ArchivePage{}, err
	}
	return page, nil
}
