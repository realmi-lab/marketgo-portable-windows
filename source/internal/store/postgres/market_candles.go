package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"market-share-go/internal/domain"
)

// MarketCandle stores observed trades, never a claim of uninterrupted coverage.
// StartSec is UTC aligned; 86400 is a UTC day, not an exchange rolling ticker.
type MarketCandle struct {
	Exchange                                        domain.Exchange
	Market, Base, Quote                             string
	IntervalSec, StartSec                           int64
	Open, High, Low, Close, BaseVolume, QuoteVolume float64
	TradeCount                                      int64
	FirstTS, LastTS                                 domain.UnixMilli
	FirstID, LastID                                 string
}

var marketCandleIntervals = [...]int64{60, 600, 3600, 86400}

type candleKey struct {
	exchange        domain.Exchange
	market          string
	interval, start int64
}

func candleTradeHash(t domain.TradeEvent) ([32]byte, error) {
	if err := t.Validate(); err != nil {
		return [32]byte{}, err
	}
	if t.Scope != domain.ScopeDomestic {
		return [32]byte{}, errors.New("market candles require a domestic trade")
	}
	if v := t.NativeQuoteNotional(); v <= 0 || math.IsInf(v, 0) || math.IsNaN(v) {
		return [32]byte{}, errors.New("invalid native quote volume")
	}
	// Receipt time changes on reconnect; exchange evidence and match count do not.
	t.ReceivedTS = 1
	data, err := json.Marshal(struct {
		Trade   domain.TradeEvent
		Matches uint64
	}{t, t.EffectiveMatchCount()})
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(data), nil
}

func buildMarketCandles(trades []domain.TradeEvent) ([]MarketCandle, error) {
	return buildValidatedMarketCandles(trades, false)
}

func buildValidatedMarketCandles(trades []domain.TradeEvent, validated bool) ([]MarketCandle, error) {
	rows := make(map[candleKey]MarketCandle)
	for _, t := range trades {
		if !validated {
			if _, err := candleTradeHash(t); err != nil {
				return nil, err
			}
		}
		for _, interval := range marketCandleIntervals {
			// Bithumb never consumes or constructs a daily candle. Its 24-hour
			// metric is a separate sum of verified hourly evidence.
			if t.Exchange == domain.ExchangeBithumb && interval == 86400 {
				continue
			}
			start := int64(t.EventTS) / 1000 / interval * interval
			key := candleKey{t.Exchange, t.Market, interval, start}
			row, exists := rows[key]
			if !exists {
				row = MarketCandle{Exchange: t.Exchange, Market: t.Market, Base: strings.ToUpper(t.Base), Quote: strings.ToUpper(t.Quote), IntervalSec: interval, StartSec: start, Open: t.Price, High: t.Price, Low: t.Price, Close: t.Price, FirstTS: t.EventTS, LastTS: t.EventTS, FirstID: t.TradeID, LastID: t.TradeID}
			}
			if row.Base != strings.ToUpper(t.Base) || row.Quote != strings.ToUpper(t.Quote) {
				return nil, errors.New("conflicting market asset identity")
			}
			if t.EventTS < row.FirstTS || t.EventTS == row.FirstTS && t.TradeID < row.FirstID {
				row.Open = t.Price
				row.FirstTS = t.EventTS
				row.FirstID = t.TradeID
			}
			if t.EventTS > row.LastTS || t.EventTS == row.LastTS && t.TradeID > row.LastID {
				row.Close = t.Price
				row.LastTS = t.EventTS
				row.LastID = t.TradeID
			}
			row.High = math.Max(row.High, t.Price)
			row.Low = math.Min(row.Low, t.Price)
			row.BaseVolume += t.Quantity
			row.QuoteVolume += t.NativeQuoteNotional()
			if math.IsInf(row.BaseVolume, 0) || math.IsInf(row.QuoteVolume, 0) || uint64(row.TradeCount) > uint64(math.MaxInt64)-t.EffectiveMatchCount() {
				return nil, errors.New("market candle aggregate overflow")
			}
			row.TradeCount += int64(t.EffectiveMatchCount())
			rows[key] = row
		}
	}
	result := make([]MarketCandle, 0, len(rows))
	for _, row := range rows {
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.Exchange != b.Exchange {
			return a.Exchange < b.Exchange
		}
		if a.Market != b.Market {
			return a.Market < b.Market
		}
		if a.IntervalSec != b.IntervalSec {
			return a.IntervalSec < b.IntervalSec
		}
		return a.StartSec < b.StartSec
	})
	return result, nil
}

// SaveMarketCandleTrades is the collector hook. Retry the same batch after any
// error, including ambiguous commit: exact IDs and four resolutions commit
// together. Call only from the collector writer, not a SHADOW calculation API.
// IDs are retained with candles so replay cannot silently double their volume.
func (r *Repository) SaveMarketCandleTrades(ctx context.Context, trades []domain.TradeEvent) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	if err := ApplyMarketCandleTradesTx(ctx, tx, trades); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ApplyMarketCandleTradesTx shares the collection journal transaction. The
// caller owns commit/rollback and must rollback on every returned error.
func ApplyMarketCandleTradesTx(ctx context.Context, tx pgx.Tx, trades []domain.TradeEvent) error {
	if len(trades) == 0 {
		return nil
	}
	if len(trades) > 512 {
		return errors.New("market candle batch exceeds 512 trades")
	}
	ordered := append([]domain.TradeEvent(nil), trades...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].DedupeKey() < ordered[j].DedupeKey() })
	hashes := make([][32]byte, len(ordered))
	for i, t := range ordered {
		hash, err := candleTradeHash(t)
		if err != nil {
			return err
		}
		hashes[i] = hash
	}
	accepted, err := acceptCandleTrades(ctx, tx, ordered, hashes)
	if err != nil {
		return err
	}
	rows, err := buildValidatedMarketCandles(accepted, true)
	if err != nil {
		return err
	}
	batch := &pgx.Batch{}
	for _, v := range rows {
		batch.Queue(marketCandleUpsert, v.Exchange, v.Market, v.Base, v.Quote, v.IntervalSec, v.StartSec, v.Open, v.High, v.Low, v.Close, v.BaseVolume, v.QuoteVolume, v.TradeCount, v.FirstTS, v.FirstID, v.LastTS, v.LastID)
	}
	results := tx.SendBatch(ctx, batch)
	for range rows {
		tag, execErr := results.Exec()
		if execErr != nil {
			_ = results.Close()
			return execErr
		}
		if tag.RowsAffected() != 1 {
			_ = results.Close()
			return errors.New("market candle asset identity conflict")
		}
	}
	if err = results.Close(); err != nil {
		return err
	}
	return nil
}

const marketCandleUpsert = `INSERT INTO market_candles AS c
(exchange_name,market,base,quote,interval_sec,start_sec,open_price,high_price,low_price,close_price,base_volume,quote_volume,trade_count,first_ts_ms,first_trade_id,last_ts_ms,last_trade_id)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT(exchange_name,market,interval_sec,start_sec) DO UPDATE SET
open_price=CASE WHEN (excluded.first_ts_ms,excluded.first_trade_id)<(c.first_ts_ms,c.first_trade_id) THEN excluded.open_price ELSE c.open_price END,
close_price=CASE WHEN (excluded.last_ts_ms,excluded.last_trade_id)>(c.last_ts_ms,c.last_trade_id) THEN excluded.close_price ELSE c.close_price END,
high_price=greatest(c.high_price,excluded.high_price), low_price=least(c.low_price,excluded.low_price),
base_volume=c.base_volume+excluded.base_volume,quote_volume=c.quote_volume+excluded.quote_volume,trade_count=c.trade_count+excluded.trade_count,
first_trade_id=CASE WHEN (excluded.first_ts_ms,excluded.first_trade_id)<(c.first_ts_ms,c.first_trade_id) THEN excluded.first_trade_id ELSE c.first_trade_id END,
last_trade_id=CASE WHEN (excluded.last_ts_ms,excluded.last_trade_id)>(c.last_ts_ms,c.last_trade_id) THEN excluded.last_trade_id ELSE c.last_trade_id END,
first_ts_ms=least(c.first_ts_ms,excluded.first_ts_ms),last_ts_ms=greatest(c.last_ts_ms,excluded.last_ts_ms),updated_at=clock_timestamp()
WHERE c.base=excluded.base AND c.quote=excluded.quote`

// One set insert admits new identities; a second statement checks existing
// hashes using a fresh READ COMMITTED snapshot after any concurrent conflict.
// Never use a same-statement SELECT that can miss the conflicting writer.
func acceptCandleTrades(ctx context.Context, tx pgx.Tx, ordered []domain.TradeEvent, hashes [][32]byte) ([]domain.TradeEvent, error) {
	type identity struct{ exchange, market, id string }
	positions := make(map[identity]int, len(ordered))
	var exchanges, markets, ids []string
	var evidence [][]byte
	var timestamps []int64
	for i, t := range ordered {
		k := identity{string(t.Exchange), t.Market, t.TradeID}
		if previous, ok := positions[k]; ok {
			if hashes[previous] != hashes[i] {
				return nil, fmt.Errorf("market candle trade evidence conflict: %s %s %s", t.Exchange, t.Market, t.TradeID)
			}
			continue
		}
		positions[k] = i
		exchanges = append(exchanges, k.exchange)
		markets = append(markets, k.market)
		ids = append(ids, k.id)
		evidence = append(evidence, hashes[i][:])
		timestamps = append(timestamps, int64(t.EventTS))
	}
	rows, err := tx.Query(ctx, `INSERT INTO market_candle_trade_ids(exchange_name,market,trade_id,evidence_hash,event_ts_ms)
 SELECT * FROM unnest($1::text[],$2::text[],$3::text[],$4::bytea[],$5::bigint[])
 ON CONFLICT DO NOTHING RETURNING exchange_name,market,trade_id`, exchanges, markets, ids, evidence, timestamps)
	if err != nil {
		return nil, err
	}
	added := make(map[identity]bool, len(ids))
	for rows.Next() {
		var k identity
		if err := rows.Scan(&k.exchange, &k.market, &k.id); err != nil {
			rows.Close()
			return nil, err
		}
		added[k] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(added) != len(ids) {
		rows, err = tx.Query(ctx, `SELECT d.exchange_name,d.market,d.trade_id,d.evidence_hash FROM market_candle_trade_ids d
 JOIN unnest($1::text[],$2::text[],$3::text[]) AS v(exchange_name,market,trade_id)
 USING(exchange_name,market,trade_id)`, exchanges, markets, ids)
		if err != nil {
			return nil, err
		}
		found := 0
		for rows.Next() {
			var k identity
			var stored []byte
			if err := rows.Scan(&k.exchange, &k.market, &k.id, &stored); err != nil {
				rows.Close()
				return nil, err
			}
			i, ok := positions[k]
			if !ok || string(stored) != string(hashes[i][:]) {
				rows.Close()
				return nil, fmt.Errorf("market candle trade evidence conflict: %s %s %s", k.exchange, k.market, k.id)
			}
			found++
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if found != len(ids) {
			return nil, errors.New("market candle trade identity disappeared")
		}
	}
	accepted := make([]domain.TradeEvent, 0, len(added))
	for _, t := range ordered {
		k := identity{string(t.Exchange), t.Market, t.TradeID}
		if added[k] {
			accepted = append(accepted, t)
			delete(added, k)
		}
	}
	return accepted, nil
}
