package postgres

import (
	"context"
	"errors"
	"time"
)

// StartIngestionAtHead is called only under the calculator's active writer gate.
// It skips existing envelopes without reading their payloads. The metadata is
// an explicit skipped range, not a claim that historical trades were calculated.
func (r *Repository) StartIngestionAtHead(parent context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	var previous, head int64
	if err = tx.QueryRow(ctx, `SELECT applied_cursor FROM computation_checkpoint WHERE singleton FOR UPDATE`).Scan(&previous); err != nil {
		return 0, err
	}
	if err = tx.QueryRow(ctx, `SELECT committed_cursor FROM ingestion_stream_head WHERE singleton`).Scan(&head); err != nil {
		return 0, err
	}
	if head < previous {
		return 0, errors.New("journal head precedes calculation cursor")
	}
	if head > previous {
		if _, err = tx.Exec(ctx, `INSERT INTO aggregation_metadata(key,value)
 VALUES('ingestion-startup-skip-'||$2::bigint::text,
 jsonb_build_object('previous_cursor',$1::bigint,'through_cursor',$2::bigint,
 'reason','START_AT_CURRENT_HEAD','skipped_at',clock_timestamp())::text)
 ON CONFLICT(key) DO NOTHING`, previous, head); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `UPDATE computation_checkpoint SET applied_cursor=$1,committed_at=clock_timestamp() WHERE singleton`, head); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return head, nil
}
