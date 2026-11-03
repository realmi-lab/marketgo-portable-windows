package postgres

import (
	"context"
	"errors"
	"time"
)

// SnapshotSourceBackfillResult describes one committed keyset batch, never
// completion of snapshot read performance or a runtime rollout.
type SnapshotSourceBackfillResult struct {
	After    int64 `json:"after"`
	Through  int64 `json:"through"`
	Next     int64 `json:"next"`
	Selected int   `json:"selected"`
	Updated  int   `json:"updated"`
	More     bool  `json:"more"`
}

func (r *Repository) IngestionSnapshotSourceUpperCursor(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var cursor int64
	err := r.pool.QueryRow(ctx, `SELECT committed_cursor FROM ingestion_stream_head WHERE singleton`).Scan(&cursor)
	return cursor, err
}

// BackfillIngestionSnapshotSources updates at most 100 metadata rows. A caller
// captures through once and persists Next only after a successful return.
func (r *Repository) BackfillIngestionSnapshotSources(ctx context.Context, after, through int64, limit int) (SnapshotSourceBackfillResult, error) {
	result := SnapshotSourceBackfillResult{After: after, Through: through, Next: after}
	if after < 0 || through < after || limit < 1 || limit > 100 {
		return result, errors.New("invalid snapshot Source backfill bounds")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(ctx, `SET LOCAL statement_timeout='15s'; SET LOCAL lock_timeout='1s'`); err != nil {
		return result, err
	}
	candidate := result
	err = tx.QueryRow(ctx, `WITH candidates AS MATERIALIZED (
 SELECT cursor FROM ingestion_events WHERE cursor>$1 AND cursor<=$2
 AND kind IN ('registry','health','official') ORDER BY cursor LIMIT $3
 ), updated AS (
 UPDATE ingestion_events e SET snapshot_source=COALESCE(e.payload->>'Source','')
 FROM candidates c WHERE e.cursor=c.cursor AND e.snapshot_source IS NULL RETURNING e.cursor
 ) SELECT COALESCE((SELECT max(cursor) FROM candidates),$1),
 (SELECT count(*) FROM candidates),(SELECT count(*) FROM updated)`, after, through, limit).Scan(&candidate.Next, &candidate.Selected, &candidate.Updated)
	if err != nil {
		return result, err
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ingestion_events WHERE cursor>$1 AND cursor<=$2 AND kind IN ('registry','health','official'))`, candidate.Next, through).Scan(&candidate.More); err != nil {
		return result, err
	}
	if err = tx.Commit(ctx); err != nil {
		return result, err
	}
	return candidate, nil
}
