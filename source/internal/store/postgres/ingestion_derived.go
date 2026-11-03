package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"market-share-go/internal/domain"
	"market-share-go/internal/reconcile"
)

type ingestionQueryExecutor interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// IngestionDerived contains absolute owner results belonging to this exact
// checkpoint. It shares the bucket/cursor transaction; no second writer races
// an uncommitted batch or loses a closed minute after a committed cursor.
type IngestionDerived struct {
	Version    int                           `json:"version"`
	Minutes    []reconcile.WSTradeMinute     `json:"minutes,omitempty"`
	Seals      []reconcile.MinuteSeal        `json:"seals,omitempty"`
	Books      []domain.OrderBookTopSnapshot `json:"books,omitempty"`
	Recoveries []IngestionMinuteRecovery     `json:"recoveries,omitempty"`
}

type IngestionMinuteRecovery struct {
	Exchange    domain.Exchange `json:"exchange"`
	Market      string          `json:"market"`
	MinuteStart int64           `json:"minute_start"`
	Events      uint64          `json:"events"`
}

// Clone freezes transaction results independently of mutable owner state while
// a checkpoint is retried. Nullable numeric and ordering fields remain nullable.
func (d IngestionDerived) Clone() *IngestionDerived {
	c := d
	c.Minutes = append([]reconcile.WSTradeMinute(nil), d.Minutes...)
	for i := range c.Minutes {
		m := &c.Minutes[i]
		m.Open, m.High, m.Low, m.Close = cloneDerivedPointer(m.Open), cloneDerivedPointer(m.High), cloneDerivedPointer(m.Low), cloneDerivedPointer(m.Close)
		m.KRWVolume, m.BuyKRWVolume, m.SellKRWVolume = cloneDerivedPointer(m.KRWVolume), cloneDerivedPointer(m.BuyKRWVolume), cloneDerivedPointer(m.SellKRWVolume)
		m.FirstTradeTS, m.LastTradeTS = cloneDerivedPointer(m.FirstTradeTS), cloneDerivedPointer(m.LastTradeTS)
	}
	c.Seals = append([]reconcile.MinuteSeal(nil), d.Seals...)
	c.Recoveries = append([]IngestionMinuteRecovery(nil), d.Recoveries...)
	c.Books = append([]domain.OrderBookTopSnapshot(nil), d.Books...)
	for i := range c.Books {
		b := &c.Books[i]
		b.Sequence = cloneDerivedPointer(b.Sequence)
		b.Bids, b.Asks = cloneDerivedLevels(b.Bids), cloneDerivedLevels(b.Asks)
	}
	return &c
}

func cloneDerivedPointer[T any](v *T) *T {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

func cloneDerivedLevels(levels []domain.OrderBookLevel) []domain.OrderBookLevel {
	c := append([]domain.OrderBookLevel(nil), levels...)
	for i := range c {
		c[i].OrderCount = cloneDerivedPointer(c[i].OrderCount)
		c[i].UpdateID = cloneDerivedPointer(c[i].UpdateID)
	}
	return c
}

func decodeIngestionDerived(checkpoint []byte) (*IngestionDerived, error) {
	var envelope struct {
		Derived *IngestionDerived `json:"derived"`
	}
	if err := json.Unmarshal(checkpoint, &envelope); err != nil {
		return nil, fmt.Errorf("decode calculation checkpoint: %w", err)
	}
	return envelope.Derived, nil
}

func flushIngestionDerived(ctx context.Context, tx pgx.Tx, derived *IngestionDerived) error {
	if derived == nil {
		return nil
	}
	if derived.Version != 1 {
		return errors.New("unsupported derived checkpoint version")
	}
	for _, minute := range derived.Minutes {
		if _, err := saveWSMinute(ctx, tx, minute); err != nil {
			return err
		}
	}
	if err := saveMinuteSeals(ctx, tx, derived.Seals); err != nil {
		return err
	}
	for _, recovery := range derived.Recoveries {
		if !recovery.Exchange.Valid() || recovery.MinuteStart < 0 || recovery.MinuteStart%60 != 0 {
			return errors.New("invalid ingestion minute recovery identity")
		}
		if _, err := tx.Exec(ctx, `UPDATE minute_observation_seals SET status='MISSING',coverage_seconds=0,gap_seconds=60,observed_zero_count=0,unknown_market_count=registry_market_count-observed_market_count,gap_market_count=registry_market_count,updated_at=clock_timestamp() WHERE exchange_name=$1 AND minute_start=$2`, recovery.Exchange, recovery.MinuteStart); err != nil {
			return err
		}
	}
	return upsertOrderBookLatest(ctx, tx, derived.Books)
}
