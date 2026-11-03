package indexday

import "sort"

// RealtimeRow is a monetary row with no inferred hourly coverage. Callers keep
// observed trades and official rolling ticker projections in separate responses.
type RealtimeRow struct {
	Market
	Amount  *float64
	Partial bool
}
type RealtimeFilter struct {
	Assets       map[string]bool
	FilterAssets bool
	Quote        string
	Venue        string
}
type RealtimeResponse struct {
	Response
	WindowSec      int64 `json:"window_sec"`
	AsOfSecond     int64 `json:"as_of_second"`
	WindowComplete bool  `json:"window_complete"`
	CoverageSec    int64 `json:"coverage_sec"`
}

// BuildRealtimeDomestic shares the grid's denominator rules but does not infer
// missing markets, candle counts, or unsupported-turnover zeros.
func BuildRealtimeDomestic(rows []RealtimeRow) DomesticGrid {
	rows = append([]RealtimeRow(nil), rows...)
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].Market, rows[j].Market
		if a.Exchange != b.Exchange {
			return a.Exchange < b.Exchange
		}
		if a.Base != b.Base {
			return a.Base < b.Base
		}
		if a.Quote != b.Quote {
			return a.Quote < b.Quote
		}
		return a.Market < b.Market
	})
	out := DomesticGrid{Totals: map[string]DomesticCell{}, Coins: []DomesticCoin{}}
	grouped := map[string]map[string]DomesticCell{}
	for _, row := range rows {
		if !domestic(row.Exchange) {
			continue
		}
		cells := grouped[row.Base]
		if cells == nil {
			cells = map[string]DomesticCell{}
			grouped[row.Base] = cells
		}
		cell := DomesticCell{Total: row.Amount, Partial: row.Partial || row.Amount == nil, Unavailable: row.Amount == nil}
		switch row.Quote {
		case "KRW":
			cell.KRW = row.Amount
		case "BTC":
			cell.BTC = row.Amount
		case "USDT":
			cell.USDT = row.Amount
		}
		cells[row.Exchange] = mergeCell(cells[row.Exchange], cell)
		out.Totals[row.Exchange] = mergeCell(out.Totals[row.Exchange], cell)
	}
	completeCells(out.Totals, nil, true)
	for base, cells := range grouped {
		completeCells(cells, out.Totals["domestic"].Total, false)
		out.Coins = append(out.Coins, DomesticCoin{Base: base, Cells: cells})
	}
	sort.Slice(out.Coins, func(i, j int) bool {
		a, b := out.Coins[i].Cells["domestic"].Total, out.Coins[j].Cells["domestic"].Total
		if a == nil || b == nil {
			if (a == nil) != (b == nil) {
				return a != nil
			}
		} else if *a != *b {
			return *a > *b
		}
		return out.Coins[i].Base < out.Coins[j].Base
	})
	return out
}
