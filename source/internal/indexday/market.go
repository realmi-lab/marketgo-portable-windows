package indexday

import "sort"

// Borrow the caller's immutable candles for the 24 closed hourly slots. A nil
// slot is unknown; it never becomes a zero-valued or verified-empty candle.
type marketHours struct {
	market   Market
	byHour   [24]*Hour
	conflict bool
}

func groupMarketHours(markets []Market, hours []Hour, start, end int64) map[string]*marketHours {
	groups := make(map[string]*marketHours, len(markets))
	for _, m := range markets {
		if domestic(m.Exchange) {
			groups[key(m)] = &marketHours{market: m}
		}
	}
	for i := range hours {
		h := &hours[i]
		if !domestic(h.Exchange) || h.Start < start || h.Start > end-3600 || h.Start%3600 != 0 {
			continue
		}
		k := key(h.Market)
		g := groups[k]
		if g == nil {
			g = &marketHours{market: h.Market}
			groups[k] = g
		}
		if h.Source == "CONFLICTING_HOURLY_SOURCES" {
			g.conflict = true
		}
		slot := (h.Start - start) / 3600
		if g.byHour[slot] != nil {
			g.conflict = true
			continue
		}
		g.byHour[slot] = h
	}
	return groups
}

type marketAmounts struct {
	base, quote, krw    float64
	baseHours, krwHours int
}

func buildMarketRow(g *marketHours) Row {
	row := Row{Market: g.market, Status: "MISSING", Sources: []string{}}
	amounts := marketAmounts{}
	conflict := g.conflict
	sources := map[string]bool{}
	// The window contains 24 closed hours. Fixed chronological traversal makes
	// float64 reduction reproducible without sorting or modifying caller input.
	for _, h := range g.byHour {
		if h == nil {
			continue
		}
		if h.VerifiedEmpty {
			if h.QuoteVolume != nil || h.VolumeKRW != nil || h.BaseVolume != nil {
				conflict = true
			}
			row.IgnoredEmptyHours++
			continue
		}
		row.Hours++
		sources[h.Source] = true
		if h.BaseVolume != nil && finite(*h.BaseVolume) {
			amounts.base += *h.BaseVolume
			amounts.baseHours++
		}
		if h.QuoteVolume != nil && finite(*h.QuoteVolume) && h.VolumeKRW != nil && finite(*h.VolumeKRW) {
			amounts.krw += *h.VolumeKRW
			amounts.krwHours++
		}
		if h.QuoteVolume != nil && finite(*h.QuoteVolume) {
			amounts.quote += *h.QuoteVolume
			row.AmountHours++
		}
	}
	for source := range sources {
		row.Sources = append(row.Sources, source)
	}
	sort.Strings(row.Sources)
	return completeMarketRow(row, amounts, conflict)
}

func completeMarketRow(row Row, a marketAmounts, conflict bool) Row {
	if row.Hours > 0 || row.IgnoredEmptyHours > 0 {
		row.Status = "INCOMPLETE_HOURS"
	}
	if a.baseHours > 0 && a.baseHours+row.IgnoredEmptyHours == 24 {
		row.BaseVolume = number(a.base)
	}
	if row.AmountHours > 0 && row.AmountHours+row.IgnoredEmptyHours == 24 {
		row.QuoteVolume = number(a.quote)
	}
	if row.Hours > 0 && row.Hours+row.IgnoredEmptyHours == 24 && row.QuoteVolume == nil {
		row.Status = "TURNOVER_UNAVAILABLE"
		if row.BaseVolume != nil {
			row.Status = "BASE_VOLUME_ONLY"
		}
	}
	if row.AmountHours+row.IgnoredEmptyHours == 24 && !finite(a.quote) {
		row.Status = "INVALID_AMOUNT"
	}
	if row.QuoteVolume != nil {
		if row.Quote == "KRW" {
			row.VolumeKRW = number(a.quote)
		} else if a.krwHours+row.IgnoredEmptyHours == 24 {
			row.VolumeKRW = number(a.krw)
		}
		row.Status = "FX_MISSING"
		if row.VolumeKRW != nil {
			row.Status = "COMPLETE"
			if row.Quote != "KRW" {
				row.Status = "COMPLETE_ESTIMATED"
			}
		}
	}
	// Publish available amounts separately from a complete day. Verified empty
	// intervals contribute zero; unknown intervals never do.
	if row.Quote == "KRW" {
		row.ObservedAmountHours = row.AmountHours + row.IgnoredEmptyHours
		if row.ObservedAmountHours > 0 {
			row.ObservedVolumeKRW = number(a.quote)
		}
	} else {
		row.ObservedAmountHours = a.krwHours + row.IgnoredEmptyHours
		if row.ObservedAmountHours > 0 {
			row.ObservedVolumeKRW = number(a.krw)
		}
	}
	if row.IgnoredEmptyHours == 24 {
		row.VolumeKRW = number(0)
		row.QuoteVolume = number(0)
		row.Status = "COMPLETE"
	}
	if conflict {
		row.Status = "CONFLICT"
		row.BaseVolume = nil
		row.QuoteVolume = nil
		row.VolumeKRW = nil
		row.ObservedVolumeKRW = nil
		row.ObservedAmountHours = 0
	}
	return row
}
