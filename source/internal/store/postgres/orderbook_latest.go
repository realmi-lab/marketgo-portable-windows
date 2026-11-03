package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"market-share-go/internal/domain"
)

// UpsertOrderBookLatest stores one bounded current row per exchange/market.
// The caller already samples each market to at most once per received second;
// this absolute upsert additionally rejects an older retry without generating
// a no-op UPDATE and unnecessary WAL.
// Equal receipt milliseconds use strictly increasing non-null sequences. Without
// comparable sequences, retain the stored value rather than guess retry order.
func (r *Repository) UpsertOrderBookLatest(ctx context.Context, snapshots []domain.OrderBookTopSnapshot) error {
	return upsertOrderBookLatest(ctx, r.pool, snapshots)
}

// ReadOrderBookLatest returns only the bounded current row per market. It does
// not scan or recreate the discarded order-book frame history.
func (r *Repository) ReadOrderBookLatest(ctx context.Context) ([]domain.OrderBookTopSnapshot, error) {
	return r.ReadOrderBookLatestSince(ctx, nil)
}

type OrderBookLatestVersion struct {
	Exchange   domain.Exchange
	Market     string
	ReceivedTS domain.UnixMilli
	Sequence   *int64
}

// ReadOrderBookLatestSince filters before fetching/decoding JSON. A per-market
// version is essential: a global timestamp can lose a late commit or a quieter
// exchange whose latest update precedes another exchange's watermark.
func (r *Repository) ReadOrderBookLatestSince(ctx context.Context, seen []OrderBookLatestVersion) ([]domain.OrderBookTopSnapshot, error) {
	exchanges := make([]string, len(seen))
	markets := make([]string, len(seen))
	stamps := make([]int64, len(seen))
	sequences := make([]*int64, len(seen))
	for i, version := range seen {
		exchanges[i], markets[i], stamps[i] = string(version.Exchange), version.Market, int64(version.ReceivedTS)
		sequences[i] = version.Sequence
	}
	rows, err := r.pool.Query(ctx, `SELECT b.exchange_name,b.market,b.base,b.quote,b.event_at_ms,b.received_at_ms,b.sequence,b.bids_json,b.asks_json
FROM orderbook_latest b
LEFT JOIN unnest($1::text[], $2::text[], $3::bigint[], $4::bigint[]) AS seen(exchange_name, market, received_at_ms, sequence)
ON seen.exchange_name=b.exchange_name AND seen.market=b.market
WHERE seen.received_at_ms IS NULL OR b.received_at_ms > seen.received_at_ms
 OR (b.received_at_ms = seen.received_at_ms AND b.sequence > seen.sequence)
ORDER BY b.exchange_name,b.market`, exchanges, markets, stamps, sequences)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []domain.OrderBookTopSnapshot
	for rows.Next() {
		var exchange string
		var eventAt, receivedAt int64
		var sequence *int64
		var bidsRaw, asksRaw []byte
		var snapshot domain.OrderBookTopSnapshot
		if err := rows.Scan(&exchange, &snapshot.Market, &snapshot.Base, &snapshot.Quote, &eventAt, &receivedAt, &sequence, &bidsRaw, &asksRaw); err != nil {
			return nil, err
		}
		snapshot.Exchange, snapshot.EventTS, snapshot.ReceivedTS, snapshot.Sequence = domain.Exchange(exchange), domain.UnixMilli(eventAt), domain.UnixMilli(receivedAt), sequence
		if err := unmarshalPriceQuantityLevels(bidsRaw, &snapshot.Bids); err != nil {
			return nil, err
		}
		if err := unmarshalPriceQuantityLevels(asksRaw, &snapshot.Asks); err != nil {
			return nil, err
		}
		if err := snapshot.Validate(); err != nil {
			return nil, fmt.Errorf("validate stored order-book latest: %w", err)
		}
		result = append(result, snapshot)
	}
	return result, rows.Err()
}

func unmarshalPriceQuantityLevels(raw []byte, target *[]domain.OrderBookLevel) error {
	// A fixed-size array silently fills missing/null numbers with zero and
	// discards extra fields. Require the exact stored pair before accepting a
	// quantity as an observed zero.
	var values [][]*float64
	if err := json.Unmarshal(raw, &values); err != nil {
		return err
	}
	levels := make([]domain.OrderBookLevel, len(values))
	for index, value := range values {
		if len(value) != 2 || value[0] == nil || value[1] == nil {
			return fmt.Errorf("stored order-book level %d requires a numeric price/quantity pair", index)
		}
		level := domain.OrderBookLevel{Price: *value[0], Quantity: *value[1]}
		if err := level.Validate(); err != nil {
			return fmt.Errorf("validate stored order-book level %d: %w", index, err)
		}
		levels[index] = level
	}
	*target = levels
	return nil
}

func upsertOrderBookLatest(ctx context.Context, query ingestionQueryExecutor, snapshots []domain.OrderBookTopSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}
	exchanges := make([]string, len(snapshots))
	markets := make([]string, len(snapshots))
	bases := make([]string, len(snapshots))
	quotes := make([]string, len(snapshots))
	eventAtMS := make([]int64, len(snapshots))
	receivedAtMS := make([]int64, len(snapshots))
	sequences := make([]int64, len(snapshots))
	hasSequence := make([]bool, len(snapshots))
	bidsJSON := make([]string, len(snapshots))
	asksJSON := make([]string, len(snapshots))
	for index, snapshot := range snapshots {
		if err := snapshot.Validate(); err != nil {
			return fmt.Errorf("validate calculated order-book snapshot: %w", err)
		}
		bids, err := marshalPriceQuantityLevels(snapshot.Bids)
		if err != nil {
			return err
		}
		asks, err := marshalPriceQuantityLevels(snapshot.Asks)
		if err != nil {
			return err
		}
		exchanges[index] = string(snapshot.Exchange)
		markets[index] = snapshot.Market
		bases[index] = snapshot.Base
		quotes[index] = snapshot.Quote
		eventAtMS[index] = int64(snapshot.EventTS)
		receivedAtMS[index] = int64(snapshot.ReceivedTS)
		if snapshot.Sequence != nil {
			sequences[index] = *snapshot.Sequence
			hasSequence[index] = true
		}
		bidsJSON[index] = string(bids)
		asksJSON[index] = string(asks)
	}
	if _, err := query.Exec(ctx, `
		INSERT INTO orderbook_latest (
			exchange_name, market, base, quote, event_at_ms, received_at_ms,
			sequence, bids_json, asks_json
		)
		SELECT incoming.exchange_name, incoming.market, incoming.base, incoming.quote,
			incoming.event_at_ms, incoming.received_at_ms,
			CASE WHEN incoming.has_sequence THEN incoming.sequence ELSE NULL END,
			incoming.bids_json::jsonb, incoming.asks_json::jsonb
		FROM unnest(
			$1::text[], $2::text[], $3::text[], $4::text[], $5::bigint[],
			$6::bigint[], $7::bigint[], $8::boolean[], $9::text[], $10::text[]
		) AS incoming(
			exchange_name, market, base, quote, event_at_ms, received_at_ms,
			sequence, has_sequence, bids_json, asks_json
		)
		ON CONFLICT (exchange_name, market) DO UPDATE SET
			base=EXCLUDED.base,
			quote=EXCLUDED.quote,
			event_at_ms=EXCLUDED.event_at_ms,
			received_at_ms=EXCLUDED.received_at_ms,
			sequence=EXCLUDED.sequence,
			bids_json=EXCLUDED.bids_json,
			asks_json=EXCLUDED.asks_json,
			updated_at=now()
		WHERE orderbook_latest.received_at_ms < EXCLUDED.received_at_ms
		 OR (orderbook_latest.received_at_ms = EXCLUDED.received_at_ms
		     AND orderbook_latest.sequence < EXCLUDED.sequence)`,
		exchanges, markets, bases, quotes, eventAtMS, receivedAtMS, sequences, hasSequence, bidsJSON, asksJSON,
	); err != nil {
		return fmt.Errorf("upsert calculated order-book latest batch: %w", err)
	}
	return nil
}

func marshalPriceQuantityLevels(levels []domain.OrderBookLevel) ([]byte, error) {
	values := make([][2]float64, len(levels))
	for index, level := range levels {
		if err := level.Validate(); err != nil {
			return nil, errors.New("calculated order-book snapshot contains an invalid level")
		}
		values[index] = [2]float64{level.Price, level.Quantity}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encode calculated order-book levels: %w", err)
	}
	return encoded, nil
}
