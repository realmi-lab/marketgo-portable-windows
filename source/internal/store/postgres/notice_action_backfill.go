package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"market-share-go/internal/notice"
)

// NoticeTitleActionBackfillResult reports an idempotent deterministic data
// upgrade. It does not change analysis_status, analysis_policy_version, or the
// grounded actions_json field and therefore never represents a title rule as a
// successful LLM analysis.
type NoticeTitleActionBackfillResult struct {
	Scanned      int64
	Inferred     int64
	Unclassified int64
	Unchanged    int64
	Updated      int64
}

type noticeTitleActionCandidate struct {
	exchange string
	noticeID string
	title    string
	category string
	current  string
}

func (r *Repository) BackfillNoticeTitleActions(
	ctx context.Context,
	since time.Time,
) (NoticeTitleActionBackfillResult, error) {
	result := NoticeTitleActionBackfillResult{}
	rows, err := r.pool.Query(ctx, `
		SELECT exchange_name, notice_id, title, category, title_actions_json
		FROM exchange_notices
		WHERE revision_ts_ms >= $1
		ORDER BY exchange_name, notice_id`, since.UnixMilli())
	if err != nil {
		return result, fmt.Errorf("read notice title-action backfill candidates: %w", err)
	}
	candidates := make([]noticeTitleActionCandidate, 0, 512)
	for rows.Next() {
		var candidate noticeTitleActionCandidate
		if err := rows.Scan(
			&candidate.exchange, &candidate.noticeID, &candidate.title,
			&candidate.category, &candidate.current,
		); err != nil {
			rows.Close()
			return result, fmt.Errorf("scan notice title-action backfill candidate: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return result, fmt.Errorf("iterate notice title-action backfill candidates: %w", err)
	}
	rows.Close()
	result.Scanned = int64(len(candidates))

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return result, fmt.Errorf("begin notice title-action backfill: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, candidate := range candidates {
		actions := notice.InferTitleActions(candidate.title, candidate.category)
		if len(actions) == 0 {
			result.Unclassified++
		} else {
			result.Inferred++
		}
		encoded, err := json.Marshal(actions)
		if err != nil {
			return result, fmt.Errorf("encode notice title actions: %w", err)
		}
		desired := string(encoded)
		if desired == candidate.current {
			result.Unchanged++
			continue
		}
		command, err := tx.Exec(ctx, `
			UPDATE exchange_notices
			SET title_actions_json=$6
			WHERE exchange_name=$1 AND notice_id=$2
			  AND title=$3 AND category=$4 AND title_actions_json=$5`,
			candidate.exchange, candidate.noticeID, candidate.title, candidate.category,
			candidate.current, desired)
		if err != nil {
			return result, fmt.Errorf("backfill %s notice %s title actions: %w", candidate.exchange, candidate.noticeID, err)
		}
		result.Updated += command.RowsAffected()
	}
	if err := tx.Commit(ctx); err != nil {
		return result, fmt.Errorf("commit notice title-action backfill: %w", err)
	}
	return result, nil
}
