package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"market-share-go/services/collector/candlewire"
)

// saveNativeWSCandleUsing replaces the current value in the same candle interval.
// Historical intervals remain; cumulative snapshot volumes are never added.
func (r *Repository) saveNativeWSCandleUsing(ctx context.Context, db nativeObservationExecer, c wscandle.Candle, receivedAt time.Time) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Exchange == "bithumb" && c.Interval == 24*time.Hour {
		return errors.New("Bithumb daily candles are disabled; use the hourly aggregate")
	}
	return r.saveNativeWSObservationUsing(ctx, db, c.Exchange, c.Market, c.Asset, c.Quote, int64(c.Interval/time.Second), c.Start.Unix(), c.End.Unix(), c.Close, c.Source, c.BoundaryVerified, c.Closed, receivedAt, c)
}

// saveBithumbWSTickerUsing does not assign a UTC start/end to an undocumented rolling
// legacy ticker. Its original date/time and decimal numbers survive in JSON.
func (r *Repository) saveBithumbWSTickerUsing(ctx context.Context, db nativeObservationExecer, t wscandle.BithumbTicker, receivedAt time.Time) error {
	if err := t.Validate(); err != nil {
		return err
	}
	if t.TickType != "1H" {
		return errors.New("only Bithumb 1H ticker is enabled")
	}
	return r.saveNativeWSObservationUsing(ctx, db, "bithumb", t.Symbol, t.Asset, t.Quote, int64(t.Interval/time.Second), 0, 0, t.Close, "BITHUMB_LEGACY_WS_"+t.TickType, false, false, receivedAt, t)
}

// saveBithumbHourly24hUsing is the only native Bithumb 24h synthesis path. The caller
// must supply 24 source-verified closed hours; receipt-time sampling of 1H
// tickers is never enough. Missing/conflicting hours do not produce a row.
func (r *Repository) saveBithumbHourly24hUsing(ctx context.Context, db nativeObservationExecer, hours []wscandle.Candle, receivedAt time.Time) error {
	c, err := wscandle.Rollup24Hours(hours)
	if err != nil {
		return err
	}
	if c.Exchange != "bithumb" {
		return errors.New("Bithumb hourly rollup requires Bithumb evidence")
	}
	if receivedAt.IsZero() {
		return errors.New("receipt time is missing")
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tag, err := db.Exec(ctx, `INSERT INTO bithumb_hourly_24h(exchange_name,market,base,quote,start_sec,end_sec,quote_volume,base_volume,close_price,source_kind,payload_json)
	VALUES('bithumb',$1,$2,$3,$4,$5,$6::numeric,$7::numeric,$8::numeric,$9,$10::jsonb)
	ON CONFLICT(market,end_sec) DO UPDATE SET payload_json=excluded.payload_json,
	quote_volume=excluded.quote_volume,base_volume=excluded.base_volume,close_price=excluded.close_price,
	source_kind=excluded.source_kind,coverage_status='VERIFIED',observed_hours=24
	WHERE bithumb_hourly_24h.coverage_status<>'VERIFIED' OR bithumb_hourly_24h.payload_json=excluded.payload_json`, c.Market, c.Asset, c.Quote, c.Start.Unix(), c.End.Unix(), c.QuoteVolume, c.BaseVolume, c.Close, c.Source, string(payload))
	if err == nil && tag.RowsAffected() != 1 {
		return errors.New("Bithumb hourly 24h evidence conflict")
	}
	return err
}

// saveCoinoneWSChartUsing preserves native OHLC and original volume metadata. The
// documented rolling-24h volume is not interpreted as candle-period volume.
func (r *Repository) saveCoinoneWSChartUsing(ctx context.Context, db nativeObservationExecer, c wscandle.CoinoneChart, receivedAt time.Time) error {
	if err := c.Validate(); err != nil {
		return err
	}
	return r.saveNativeWSObservationUsing(ctx, db, c.Exchange, c.Market, c.Asset, c.Quote, int64(c.Interval/time.Second), c.Start.Unix(), c.End.Unix(), c.Close, c.Source, true, false, receivedAt, c)
}

type nativeObservationExecer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func (r *Repository) saveNativeWSObservationUsing(ctx context.Context, db nativeObservationExecer, exchange, market, base, quote string, interval, start, end int64, close, source string, boundary, closed bool, received time.Time, value any) error {
	if received.IsZero() || received.UnixNano() <= 0 {
		return errors.New("native websocket receipt time missing")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(payload)
	slot, order, err := nativeWSObservationSlot(value)
	if err != nil {
		return err
	}
	var startValue, endValue any
	if start > 0 && end > start {
		startValue = start
		endValue = end
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err = db.Exec(ctx, `INSERT INTO native_ws_candle_observations
 (evidence_hash,exchange_name,market,base,quote,interval_sec,start_sec,end_sec,close_price,source_kind,boundary_verified,closed,received_at_ns,payload_json,observation_slot,source_order)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::numeric,$10,$11,$12,$13,$14::jsonb,$15,$16)
 ON CONFLICT(exchange_name,market,interval_sec,source_kind,observation_slot) DO UPDATE SET
 evidence_hash=excluded.evidence_hash,close_price=excluded.close_price,
 boundary_verified=excluded.boundary_verified,closed=excluded.closed,
 received_at_ns=excluded.received_at_ns,payload_json=excluded.payload_json,source_order=excluded.source_order
 WHERE (excluded.source_order,excluded.received_at_ns)>(native_ws_candle_observations.source_order,native_ws_candle_observations.received_at_ns)
 AND (excluded.source_order,excluded.evidence_hash) IS DISTINCT FROM
 (native_ws_candle_observations.source_order,native_ws_candle_observations.evidence_hash)`, hash[:], exchange, market, base, quote, interval, startValue, endValue, close, source, boundary, closed, received.UnixNano(), string(payload), slot, order)
	return err
}

func nativeWSObservationSlot(value any) (string, int64, error) {
	switch v := value.(type) {
	case wscandle.Candle:
		return fmt.Sprintf("interval:%d", v.Start.Unix()), v.SourceTimestampMS, nil
	case wscandle.CoinoneChart:
		return fmt.Sprintf("interval:%d", v.Start.Unix()), v.SourceTimestampMS, nil
	case wscandle.BithumbTicker:
		if err := v.Validate(); err != nil {
			return "", 0, err
		}
		order, err := strconv.ParseInt(v.SourceDate+v.SourceTime, 10, 64)
		// This is only a source-clock observation slot. It does not assign an
		// undocumented timezone, candle start/end, or finality to legacy 1H.
		return "source-hour:" + v.SourceDate + v.SourceTime[:2], order, err
	default:
		return "", 0, errors.New("unsupported native observation")
	}
}
