package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"market-share-go/contracts/collection/registry"
	"market-share-go/internal/domain"
	"market-share-go/internal/ingestlog"
	"math"
)

// LoadActiveMarketRegistry reads only this writer's already selected database.
// It never discovers defaults or creates a second connection to another store.
func (w *IngestionWriter) LoadActiveMarketRegistry(ctx context.Context) (markets []domain.MarketPair, resultErr error) {
	if err := w.admit(ctx, false); err != nil {
		return nil, err
	}
	defer w.mu.Unlock()
	defer func() { resultErr = w.sessionErrorLocked(resultErr) }()
	if err := w.ensureSessionLocked(ctx); err != nil {
		return nil, err
	}
	return loadActiveMarketRegistry(ctx, w.conn)
}

func projectIngestionEvent(ctx context.Context, tx pgx.Tx, event ingestlog.Event) error {
	if event.Kind == "frame" {
		return projectIngestionFrame(ctx, tx, event.Payload)
	}
	if event.Kind != "registry" {
		return errors.New("unsupported ingestion projection kind")
	}
	// Do not let a repaired old poll overwrite membership from a newer poll.
	// Later registry inputs remain durable and replay after the earlier failure.
	var blocked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM ingestion_events WHERE kind='registry' AND cursor<$1 AND projection_error<>'' AND archived_at IS NULL)`, event.Cursor).Scan(&blocked); err != nil {
		return err
	}
	if blocked {
		return errors.New("earlier registry projection must be repaired first")
	}
	var value struct {
		Snapshot  registry.Snapshot
		Markets   []domain.MarketPair
		Exchanges []domain.Exchange
	}
	if err := json.Unmarshal(event.Payload, &value); err != nil {
		return err
	}
	if len(value.Exchanges) == 0 {
		return errors.New("registry observation has no explicit polled exchanges")
	}
	observed, err := registryObservedAtMS(value.Snapshot.UpdatedAt)
	if err != nil {
		return err
	}
	if value.Snapshot.Version > math.MaxInt64 {
		return errors.New("registry version exceeds PostgreSQL BIGINT")
	}
	names, err := normalizedRegistryExchanges(value.Exchanges)
	if err != nil {
		return err
	}
	universe, err := domain.NewMarketUniverse(value.Markets)
	if err != nil {
		return err
	}
	for _, name := range names {
		row, ok := value.Snapshot.Exchanges[name]
		if !ok {
			return errors.New("registry snapshot missing polled exchange")
		}
		if err := saveExchangeRegistryObservation(ctx, tx, observed, int64(value.Snapshot.Version), name, row, universe.ForExchange(name)); err != nil {
			return err
		}
	}
	return nil
}
