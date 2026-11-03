package postgres

import (
	"context"
	"fmt"
	"market-share-go/internal/indexday"
	"time"
)

func (r *Repository) rollingLatest(ctx context.Context, day string) (indexday.Response, error) {
	now := time.Now()
	if day != now.In(indexday.Seoul).Format(time.DateOnly) {
		return indexday.Response{}, fmt.Errorf("rolling latest supports today only")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out := indexday.Response{Day: day, ContractVersion: "INDEX_DAY_HOURLY_V1", StorageMode: "ROLLING_24H_LATEST", Membership: "CURRENT_REGISTRY", Status: "ROLLING_AVAILABLE", Rows: []indexday.Row{}, Exchanges: []indexday.Exchange{}}
	rows, err := r.pool.Query(ctx, `SELECT exchange_name,market,base,quote,window_end,quote_turnover::double precision,turnover_krw::double precision,known_hours,status FROM hourly_rolling_turnover_latest WHERE status<>'INACTIVE' ORDER BY exchange_name,market`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	expected := now.UTC().Add(-2 * time.Minute).Truncate(time.Hour).Unix()
	for rows.Next() {
		var row indexday.Row
		var end int64
		var quote, krw float64
		var known int
		var state string
		if err = rows.Scan(&row.Exchange, &row.Market.Market, &row.Base, &row.Quote, &end, &quote, &krw, &known, &state); err != nil {
			return out, err
		}
		if out.End != 0 && out.End != end {
			return out, fmt.Errorf("mixed rolling generations")
		}
		out.End = end
		out.Start = end - 86400
		if end != expected {
			state = "STALE"
		}
		row.Status = "ROLLING_" + state
		row.Hours = known
		row.AmountHours = known
		row.ObservedAmountHours = known
		row.Sources = []string{"OFFICIAL_1H_SUM_24H"}
		// The publisher stores COALESCE(sum, 0); with no known hour that 0 is
		// a placeholder, not an observed 0 won (Korbit/GOPAX, for example).
		if known > 0 {
			row.QuoteVolume = &quote
			row.VolumeKRW = &krw
			row.ObservedVolumeKRW = &krw
		}
		if state != "OK" {
			out.FailedMarkets++
			out.Status = "ROLLING_PARTIAL"
		}
		out.Rows = append(out.Rows, row)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Rows) == 0 {
		return out, fmt.Errorf("rolling generation unavailable")
	}
	out.Domestic = indexday.BuildDomestic(out.Rows)
	out.Exchanges = indexday.RollingExchanges(out.Rows, out.Domestic)
	return out, nil
}
