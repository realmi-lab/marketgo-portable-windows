package postgres

import (
	"context"
	"errors"
	"fmt"
	"market-share-go/internal/ingestlog"
	"time"
)

const IngestionProjectionRepairMaxBatch = 32

// RetryPendingProjections repairs derived candles using unchanged durable raw
// evidence. A deterministic source conflict remains visible and blocks compute;
// a transient database constraint/failure can recover without deleting input.
func (w *IngestionWriter) RetryPendingProjections(parent context.Context, limit int) (repairedCount int, resultErr error) {
	if limit < 1 || limit > IngestionProjectionRepairMaxBatch {
		return 0, errors.New("invalid ingestion projection repair batch")
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	if err := w.admit(ctx, false); err != nil {
		return 0, err
	}
	defer w.mu.Unlock()
	defer func() { resultErr = w.sessionErrorLocked(resultErr) }()
	if err := w.ensureSessionLocked(ctx); err != nil {
		return 0, err
	}
	tx, err := w.conn.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	rows, err := tx.Query(ctx, `SELECT cursor,event_id,schema_version,kind,received_at_ns,payload FROM ingestion_events WHERE projection_error<>'' AND archived_at IS NULL ORDER BY cursor LIMIT $1 FOR UPDATE`, limit)
	if err != nil {
		return 0, err
	}
	var events []ingestlog.Event
	for rows.Next() {
		var event ingestlog.Event
		if err = rows.Scan(&event.Cursor, &event.ID, &event.Version, &event.Kind, &event.ReceivedAtNS, &event.Payload); err != nil {
			rows.Close()
			return 0, err
		}
		events = append(events, event)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	if err = resolveCompactIngestion(ctx, tx, events, true); err != nil {
		return 0, err
	}
	w.invalidateHourlySummary(events)
	repaired := 0
	var failures error
	for _, event := range events {
		if err = event.Validate(); err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		projection, err := tx.Begin(ctx)
		if err != nil {
			return 0, err
		}
		if err = projectIngestionEvent(ctx, projection, event); err != nil {
			if rollbackErr := projection.Rollback(ctx); rollbackErr != nil {
				return 0, rollbackErr
			}
			failures = errors.Join(failures, fmt.Errorf("projection cursor %d: %w", event.Cursor, err))
			continue
		}
		if err = projection.Commit(ctx); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `UPDATE ingestion_events SET projection_error='' WHERE cursor=$1`, event.Cursor); err != nil {
			return 0, err
		}
		repaired++
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return repaired, failures
}
