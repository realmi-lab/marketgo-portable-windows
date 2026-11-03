package postgres

import (
	"context"
	"github.com/jackc/pgx/v5/pgconn"
	"market-share-go/services/collector/candlewire"
	"time"
)

// All native writes execute on the lease-owning session, not a checked-then-
// reused pool connection. Disconnecting that session fences the SQL itself.
func (w *IngestionWriter) SaveNativeWSCandle(ctx context.Context, c wscandle.Candle, received time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := w.admit(ctx, true); err != nil {
		return err
	}
	defer w.mu.Unlock()
	if err := w.ensureSessionLocked(ctx); err != nil {
		return err
	}
	return w.repository.saveNativeWSCandleUsing(ctx, ingestionNativeExecutor{w}, c, received)
}
func (w *IngestionWriter) SaveBithumbWSTicker(ctx context.Context, c wscandle.BithumbTicker, received time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := w.admit(ctx, true); err != nil {
		return err
	}
	defer w.mu.Unlock()
	if err := w.ensureSessionLocked(ctx); err != nil {
		return err
	}
	return w.repository.saveBithumbWSTickerUsing(ctx, ingestionNativeExecutor{w}, c, received)
}
func (w *IngestionWriter) SaveCoinoneWSChart(ctx context.Context, c wscandle.CoinoneChart, received time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := w.admit(ctx, true); err != nil {
		return err
	}
	defer w.mu.Unlock()
	if err := w.ensureSessionLocked(ctx); err != nil {
		return err
	}
	return w.repository.saveCoinoneWSChartUsing(ctx, ingestionNativeExecutor{w}, c, received)
}
func (w *IngestionWriter) SaveBithumbHourly24h(ctx context.Context, hours []wscandle.Candle, received time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := w.admit(ctx, true); err != nil {
		return err
	}
	defer w.mu.Unlock()
	if err := w.ensureSessionLocked(ctx); err != nil {
		return err
	}
	return w.repository.saveBithumbHourly24hUsing(ctx, ingestionNativeExecutor{w}, hours, received)
}
func (w *IngestionWriter) RefreshBithumbHourly24h(ctx context.Context, end time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := w.admit(ctx, false); err != nil {
		return err
	}
	defer w.mu.Unlock()
	if err := w.ensureSessionLocked(ctx); err != nil {
		return err
	}
	if !w.summaryEnd.IsZero() && end.Equal(w.summaryEnd) {
		return ctx.Err()
	}
	if err := refreshBithumbHourly24h(ctx, ingestionNativeExecutor{w}, end, time.Now()); err != nil {
		return err
	}
	w.summaryEnd = end
	return nil
}
func (w *IngestionWriter) RepairNativeNormalization(ctx context.Context, limit int) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := w.admit(ctx, false); err != nil {
		return 0, err
	}
	defer w.mu.Unlock()
	if err := w.ensureSessionLocked(ctx); err != nil {
		return 0, err
	}
	return repairNativeNormalization(ctx, ingestionNativeExecutor{w}, limit)
}

// Exec runs only while the caller holds the writer mutex. Validation happens
// in the ordinary helpers; only SQL/session failures receive retry tags.
type ingestionNativeExecutor struct{ writer *IngestionWriter }

func (db ingestionNativeExecutor) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	tag, err := db.writer.conn.Exec(ctx, sql, args...)
	return tag, db.writer.sessionErrorLocked(err)
}
