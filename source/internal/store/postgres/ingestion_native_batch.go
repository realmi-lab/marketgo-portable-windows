package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	wscandle "market-share-go/services/collector/candlewire"
	"time"
)

// These native observation helpers validate inputs and ignore command tags.
// Collect their SQL before taking the shared journal writer lock; execution
// errors are checked from SendBatch before the enclosing transaction commits.
type nativeObservationBatch struct{ pgx.Batch }

func (b *nativeObservationBatch) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if err := ctx.Err(); err != nil {
		return pgconn.CommandTag{}, err
	}
	b.Queue(sql, args...)
	return pgconn.CommandTag{}, nil
}

// SaveNativeBatch preserves existing observation upserts and JSON, committing
// the ordered group atomically on the collector's lease-owning connection.
func (w *IngestionWriter) SaveNativeBatch(ctx context.Context, items []wscandle.Observation) (resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if len(items) == 0 {
		return nil
	}
	if len(items) > 64 {
		return errors.New("native batch exceeds 64 observations")
	}
	batch := &nativeObservationBatch{}
	for _, item := range items {
		count := 0
		if item.Candle != nil {
			count++
		}
		if item.Ticker != nil {
			count++
		}
		if item.Chart != nil {
			count++
		}
		if count != 1 {
			return errors.New("native observation requires exactly one value")
		}
		var err error
		switch {
		case item.Candle != nil:
			err = w.repository.saveNativeWSCandleUsing(ctx, batch, *item.Candle, item.Received)
		case item.Ticker != nil:
			err = w.repository.saveBithumbWSTickerUsing(ctx, batch, *item.Ticker, item.Received)
		case item.Chart != nil:
			err = w.repository.saveCoinoneWSChartUsing(ctx, batch, *item.Chart, item.Received)
		}
		if err != nil {
			return err
		}
	}
	if err := w.admit(ctx, true); err != nil {
		return err
	}
	defer w.mu.Unlock()
	defer func() { resultErr = w.sessionErrorLocked(resultErr) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := w.ensureSessionLocked(ctx); err != nil {
		return err
	}
	tx, err := w.conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	results := tx.SendBatch(ctx, &batch.Batch)
	for i := 0; i < batch.Len(); i++ {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return fmt.Errorf("native observation batch statement %d: %w", i, err)
		}
	}
	if err := results.Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
