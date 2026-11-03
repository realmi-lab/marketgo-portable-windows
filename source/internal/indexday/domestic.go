package indexday

import (
	"sort"
	"strings"
)

// DomesticGrid is the display contract for the domestic exchange comparison.
type DomesticGrid struct {
	Totals map[string]DomesticCell `json:"totals"`
	Coins  []DomesticCoin          `json:"coins"`
}
type DomesticCoin struct {
	Base  string                  `json:"base"`
	Cells map[string]DomesticCell `json:"cells"`
}
type DomesticCell struct {
	Total       *float64 `json:"total"`
	KRW         *float64 `json:"krw"`
	BTC         *float64 `json:"btc"`
	USDT        *float64 `json:"usdt"`
	Share       *float64 `json:"share"`
	Partial     bool     `json:"partial"`
	Unavailable bool     `json:"unavailable"`

	invalidTotal, invalidKRW, invalidBTC, invalidUSDT bool
}

// Missing inputs can be skipped, but an invalid sum must remain unavailable
// after later rows are added. Keep that state separate from an initial nil.
func addAmount(a, b *float64, invalid bool) (*float64, bool) {
	if invalid || (a != nil && !finite(*a)) || (b != nil && !finite(*b)) {
		return nil, true
	}
	if a == nil {
		return b, false
	}
	if b == nil {
		return a, false
	}
	sum := number(*a + *b)
	return sum, sum == nil
}
func mergeCell(a, b DomesticCell) DomesticCell {
	out := DomesticCell{Partial: a.Partial || b.Partial, Unavailable: a.Unavailable || b.Unavailable}
	out.Total, out.invalidTotal = addAmount(a.Total, b.Total, a.invalidTotal || b.invalidTotal)
	out.KRW, out.invalidKRW = addAmount(a.KRW, b.KRW, a.invalidKRW || b.invalidKRW)
	out.BTC, out.invalidBTC = addAmount(a.BTC, b.BTC, a.invalidBTC || b.invalidBTC)
	out.USDT, out.invalidUSDT = addAmount(a.USDT, b.USDT, a.invalidUSDT || b.invalidUSDT)
	out.Partial = out.Partial || out.invalidTotal || out.invalidKRW || out.invalidBTC || out.invalidUSDT
	return out
}
func completeCells(cells map[string]DomesticCell, denominator *float64, aggregate bool) {
	both := mergeCell(cells["bithumb"], cells["upbit"])
	cells["combined"] = both
	all := DomesticCell{}
	for _, e := range Exchanges {
		all = mergeCell(all, cells[e])
	}
	cells["domestic"] = all
	for key, c := range cells {
		den := all.Total
		if key == "domestic" && !aggregate {
			den = denominator
		}
		if c.Total != nil && den != nil && *den > 0 {
			c.Share = number(*c.Total / *den * 100)
		}
		cells[key] = c
	}
}
func BuildDomestic(rows []Row) DomesticGrid {
	out := DomesticGrid{Totals: map[string]DomesticCell{}, Coins: []DomesticCoin{}}
	grouped := map[string]map[string]DomesticCell{}
	for _, r := range rows {
		cells := grouped[r.Base]
		if cells == nil {
			cells = map[string]DomesticCell{}
			grouped[r.Base] = cells
		}
		value := r.ObservedVolumeKRW
		unsupported := r.Exchange == "korbit" || r.Exchange == "gopax"
		if value == nil && unsupported {
			value = number(0)
		}
		c := DomesticCell{Total: value, Partial: r.ObservedAmountHours < 24 || (strings.HasPrefix(r.Status, "ROLLING_") && r.Status != "ROLLING_OK"), Unavailable: unsupported && r.ObservedVolumeKRW == nil}
		switch r.Quote {
		case "KRW":
			c.KRW = value
		case "BTC":
			c.BTC = value
		case "USDT":
			c.USDT = value
		}
		cells[r.Exchange] = mergeCell(cells[r.Exchange], c)
		out.Totals[r.Exchange] = mergeCell(out.Totals[r.Exchange], c)
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
