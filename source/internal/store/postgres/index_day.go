package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"market-share-go/internal/indexday"
)

// IndexDay reads the permanent hourly archive only. Stored same-hour opening
// FX conversions are summed per hour, never replaced by a daily closing rate.
func (r *Repository) IndexDay(ctx context.Context, day string) (indexday.Response, error) {
	if strings.HasPrefix(day, "rolling:") {
		return r.rollingLatest(ctx, strings.TrimPrefix(day, "rolling:"))
	}
	now := time.Now()
	start, end, err := indexday.Window(day, now)
	if err != nil {
		return indexday.Response{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	markets, err := r.LoadActiveMarketRegistry(ctx)
	if err != nil {
		return indexday.Response{}, err
	}
	universe := make([]indexday.Market, 0, len(markets))
	for _, m := range markets {
		universe = append(universe, indexday.Market{Exchange: string(m.Exchange), Market: m.Symbol, Base: m.Base, Quote: m.Quote})
	}
	rows, err := r.pool.Query(ctx, `
 WITH occupied_hourly AS MATERIALIZED (
  SELECT exchange_name,market,hour_start
  FROM hourly_open_turnover
  WHERE hour_start >= $1 AND hour_start < $2
 )
 SELECT h.exchange_name,h.market,
  COALESCE(r.base, CASE WHEN split_part(h.market,'-',1)=h.quote
   THEN split_part(h.market,'-',2) ELSE split_part(h.market,'-',1) END),
  h.quote,h.hour_start,h.quote_turnover,
  CASE WHEN h.quote='KRW' THEN h.quote_turnover
   WHEN h.quote IN ('BTC','USDT') AND h.turnover_krw_status='ESTIMATED_AT_HOURLY_OPEN'
   THEN h.quote_turnover_krw END,
  h.source,false AS verified_empty
 FROM hourly_open_turnover_with_conversion h
 LEFT JOIN market_registry_current r USING(exchange_name,market)
 WHERE h.hour_start >= $1 AND h.hour_start < $2
 UNION ALL
 SELECT DISTINCT j.exchange_name,j.market,j.base,j.quote,g.hour_start,
  NULL::numeric,NULL::numeric,upper(j.exchange_name)||'_REST_60M_EMPTY_VERIFIED',true
 FROM hourly_open_turnover_verified_jobs j
 CROSS JOIN LATERAL generate_series(greatest(j.start_sec,$1),least(j.end_sec,$2)-1,3600) g(hour_start)
 WHERE j.start_sec < $2 AND j.end_sec > $1
  AND NOT EXISTS (SELECT 1 FROM occupied_hourly h
   WHERE h.exchange_name=j.exchange_name AND h.market=j.market AND h.hour_start=g.hour_start)
 ORDER BY 1,2,5`, start.Unix(), end.Unix())
	if err != nil {
		return indexday.Response{}, fmt.Errorf("read hourly index archive: %w", err)
	}
	defer rows.Close()
	hours := []indexday.Hour{}
	for rows.Next() {
		var h indexday.Hour
		if err := rows.Scan(&h.Exchange, &h.Market.Market, &h.Base, &h.Quote, &h.Start, &h.QuoteVolume, &h.VolumeKRW, &h.Source, &h.VerifiedEmpty); err != nil {
			return indexday.Response{}, err
		}
		hours = append(hours, h)
	}
	if err := rows.Err(); err != nil {
		return indexday.Response{}, err
	}
	return indexday.BuildAt(day, universe, hours, now)
}
