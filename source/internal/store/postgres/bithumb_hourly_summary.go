package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// RefreshBithumbHourly24h stores an absolute observed summary of the 24 completed
// UTC hours ending at end. Its only numerical input is the 3600-second trade
// projection, never a daily candle or rolling legacy ticker snapshot. Even 24
// present rows remain OBSERVED_PARTIAL: row existence is not coverage evidence.
func (r *Repository) RefreshBithumbHourly24h(ctx context.Context, end time.Time) error {
	return refreshBithumbHourly24h(ctx, r.pool, end, time.Now())
}

type bithumbHourlySummaryExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func refreshBithumbHourly24h(ctx context.Context, db bithumbHourlySummaryExecutor, end, now time.Time) error {
	if end.IsZero() || end.Unix() < 86400 || !end.Equal(end.Truncate(time.Hour)) || end.After(now.Truncate(time.Hour)) {
		return errors.New("Bithumb hourly summary requires a completed UTC hour boundary")
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	_, err := db.Exec(ctx, bithumbHourlySummarySQL, end.Unix(), end.Add(-24*time.Hour).Unix())
	return err
}

// The bounded hourly scan also includes removed markets with observations in
// this window. Active registry markets with no observations produce explicit
// missing summaries. Registry membership here is a current discovery aid, not
// evidence of historical market or transport coverage.
const bithumbHourlySummarySQL = `WITH hourly AS MATERIALIZED (
 SELECT market,base,quote,start_sec,quote_volume,base_volume,close_price
 FROM market_candles
 WHERE exchange_name='bithumb' AND interval_sec=3600
   AND start_sec >= $2 AND start_sec < $1
   AND source_kind='WEBSOCKET_TRADE'
), observed AS (
 SELECT market,min(base) AS base,min(quote) AS quote,
        count(*) AS observed_hours,
        count(DISTINCT base)=1 AND count(DISTINCT quote)=1 AS identity_consistent,
        sum(quote_volume::numeric) AS observed_quote_volume,
        sum(base_volume::numeric) AS observed_base_volume,
        (array_agg(close_price::numeric ORDER BY start_sec DESC))[1] AS last_observed_close,
        min(start_sec) AS first_observed_hour,max(start_sec) AS last_observed_hour
 FROM hourly GROUP BY market
), markets AS (
 SELECT market,base,quote FROM observed
 UNION ALL
 SELECT r.market,r.base,r.quote FROM market_registry_current r
 WHERE r.exchange_name='bithumb' AND r.active
   AND r.first_discovered_at_ms < $1 * 1000
   AND NOT EXISTS (SELECT 1 FROM observed o WHERE o.market=r.market)
)
INSERT INTO bithumb_hourly_24h AS target
 (exchange_name,market,base,quote,start_sec,end_sec,quote_volume,base_volume,close_price,
  source_kind,payload_json,observed_hours,coverage_status)
SELECT 'bithumb',m.market,m.base,m.quote,$2,$1,
 CASE WHEN o.observed_hours=24 AND o.identity_consistent THEN o.observed_quote_volume END,
 CASE WHEN o.observed_hours=24 AND o.identity_consistent THEN o.observed_base_volume END,
 CASE WHEN o.observed_hours=24 AND o.identity_consistent THEN o.last_observed_close END,
 'WEBSOCKET_HOURLY_OBSERVED_24H',
 jsonb_build_object('source_interval_sec',3600,'window_start_sec',$2::bigint,'window_end_sec',$1::bigint,
   'observed_hours',coalesce(o.observed_hours,0),'required_hours',24,
   'identity_consistent',coalesce(o.identity_consistent,true),
   'observed_quote_volume',o.observed_quote_volume,'observed_base_volume',o.observed_base_volume,
   'last_observed_close',o.last_observed_close,
   'first_observed_hour',o.first_observed_hour,'last_observed_hour',o.last_observed_hour,
   'coverage_status','OBSERVED_PARTIAL'),
 coalesce(o.observed_hours,0),'OBSERVED_PARTIAL'
FROM markets m LEFT JOIN observed o ON o.market=m.market
ON CONFLICT(market,end_sec) DO UPDATE SET
 base=excluded.base,quote=excluded.quote,start_sec=excluded.start_sec,
 quote_volume=excluded.quote_volume,base_volume=excluded.base_volume,close_price=excluded.close_price,
 source_kind=excluded.source_kind,payload_json=excluded.payload_json,
 observed_hours=excluded.observed_hours,coverage_status=excluded.coverage_status
WHERE target.coverage_status='OBSERVED_PARTIAL'
 AND (target.base,target.quote,target.start_sec,target.quote_volume,target.base_volume,target.close_price,
 target.source_kind,target.payload_json,target.observed_hours,target.coverage_status)
 IS DISTINCT FROM (excluded.base,excluded.quote,excluded.start_sec,excluded.quote_volume,excluded.base_volume,excluded.close_price,
 excluded.source_kind,excluded.payload_json,excluded.observed_hours,excluded.coverage_status)`
