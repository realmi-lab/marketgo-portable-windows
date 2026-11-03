package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"market-share-go/internal/domain"
	"market-share-go/internal/ingestlog"
)

func ingestionFrameTrades(payload []byte) ([]domain.TradeEvent, error) {
	var frame struct {
		Message          struct{ Trades []domain.TradeEvent }
		TradeMatchCounts []uint64
	}
	if err := json.Unmarshal(payload, &frame); err != nil {
		return nil, err
	}
	if len(frame.TradeMatchCounts) != len(frame.Message.Trades) {
		return nil, errors.New("ingestion trade match counts missing")
	}
	for i := range frame.Message.Trades {
		frame.Message.Trades[i].MatchCount = frame.TradeMatchCounts[i]
	}
	return frame.Message.Trades, nil
}

// Adjacent valid frame envelopes share at most 512 trades. Registry and large
// or malformed frames keep their original ordered savepoint boundary. If the
// optimistic group fails, roll it back and replay each original frame in order:
// a conflict cannot poison unrelated raw evidence or partial-frame projections.
func projectIngestionBatch(ctx context.Context, tx pgx.Tx, events []ingestlog.Event) error {
	for start := 0; start < len(events); {
		end := start
		var trades []domain.TradeEvent
		for end < len(events) && events[end].Kind == "frame" {
			values, err := ingestionFrameTrades(events[end].Payload)
			if err != nil || len(trades)+len(values) > 512 {
				break
			}
			trades = append(trades, values...)
			end++
		}
		if end > start+1 {
			group, err := tx.Begin(ctx)
			if err != nil {
				return err
			}
			err = ApplyMarketCandleTradesTx(ctx, group, trades)
			if err == nil {
				if err = group.Commit(ctx); err != nil {
					return err
				}
				start = end
				continue
			}
			if err = group.Rollback(ctx); err != nil {
				return err
			}
		}
		if end == start {
			end++
		}
		if err := projectIngestionFrames(ctx, tx, events[start:end]); err != nil {
			return err
		}
		start = end
	}
	return nil
}

func projectIngestionFrames(ctx context.Context, tx pgx.Tx, projected []ingestlog.Event) error {
	var err error
	for _, e := range projected {
		// A derived-data failure must never discard received exchange evidence.
		// Savepoints preserve raw input plus a visible blocked-projection marker.
		projection, beginErr := tx.Begin(ctx)
		if beginErr != nil {
			return beginErr
		}
		projectionErr := projectIngestionEvent(ctx, projection, e)
		if projectionErr == nil {
			if err = projection.Commit(ctx); err != nil {
				return err
			}
		} else {
			if err = projection.Rollback(ctx); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `UPDATE ingestion_events SET projection_error=$1 WHERE cursor=$2`, projectionErr.Error(), e.Cursor); err != nil {
				return err
			}
		}
	}
	return nil
}

// The collector lease excludes other projection writers. Invalidate before
// commit as an ambiguous response may still have persisted late evidence.
// Restart/reconnect always rechecks the summary; no persisted dirty flag is
// needed while projections and the journal remain in this one transaction.
func (w *IngestionWriter) invalidateHourlySummary(events []ingestlog.Event) {
	if w.summaryEnd.IsZero() {
		return
	}
	end := w.summaryEnd.Unix()
	start := end - 86400
	for _, event := range events {
		if event.Kind == "registry" {
			w.summaryEnd = time.Time{}
			return
		}
		trades, err := ingestionFrameTrades(event.Payload)
		if err != nil {
			w.summaryEnd = time.Time{}
			return
		}
		for _, trade := range trades {
			second := int64(trade.EventTS) / 1000
			if trade.Exchange == domain.ExchangeBithumb && second >= start && second < end {
				w.summaryEnd = time.Time{}
				return
			}
		}
	}
}
