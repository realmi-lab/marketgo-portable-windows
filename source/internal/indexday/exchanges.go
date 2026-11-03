package indexday

func buildExchange(name string, rows []Row) Exchange {
	item := Exchange{Exchange: name, Status: "INCOMPLETE"}
	var sum, observed float64
	for _, r := range rows {
		if r.Exchange != name {
			continue
		}
		item.MarketCount++
		if r.ObservedVolumeKRW != nil {
			item.ObservedMarkets++
			observed += *r.ObservedVolumeKRW
		}
		if r.VolumeKRW != nil {
			item.CompleteMarkets++
			sum += *r.VolumeKRW
		}
	}
	// A verified subset is not the complete exchange denominator.
	if item.ObservedMarkets > 0 {
		item.ObservedVolumeKRW = number(observed)
	}
	if item.CompleteMarkets > 0 {
		item.ConfirmedVolumeKRW = number(sum)
	}
	if item.MarketCount > 0 && item.CompleteMarkets == item.MarketCount {
		item.VolumeKRW = number(sum)
		if item.VolumeKRW != nil {
			item.Status = "COMPLETE"
		}
	}
	return item
}

func buildExchanges(rows []Row) ([]Exchange, string) {
	items := make([]Exchange, 0, len(Exchanges))
	status := "PARTIAL"
	all := true
	total := 0.0
	for _, name := range Exchanges {
		item := buildExchange(name, rows)
		if item.VolumeKRW == nil {
			all = false
		} else {
			total += *item.VolumeKRW
		}
		items = append(items, item)
	}
	// The current registry cannot prove complete historical membership.
	if all && finite(total) && total > 0 {
		status = "AVAILABLE_CURRENT_UNIVERSE"
		for i := range items {
			items[i].SharePct = number(*items[i].VolumeKRW / total * 100)
		}
	}
	observedTotal := 0.0
	for _, item := range items {
		if item.ObservedVolumeKRW != nil {
			observedTotal += *item.ObservedVolumeKRW
		}
	}
	if finite(observedTotal) && observedTotal > 0 {
		for i := range items {
			item := &items[i]
			if item.ObservedVolumeKRW != nil {
				item.ObservedSharePct = number(*item.ObservedVolumeKRW / observedTotal * 100)
			} else if item.Exchange == "korbit" || item.Exchange == "gopax" {
				// Unsupported-turnover display policy, not observed evidence.
				item.ObservedSharePct = number(0)
			}
		}
	}
	return items, status
}

// RollingExchanges summarizes the latest rolling 24-hour rows per exchange.
// An exchange without any known amount has no volume or share, not 0 won/0%.
func RollingExchanges(rows []Row, domestic DomesticGrid) []Exchange {
	items := make([]Exchange, 0, len(Exchanges))
	for _, ex := range Exchanges {
		item := Exchange{Exchange: ex, Status: "COMPLETE"}
		var total *float64
		invalidTotal := false
		for _, row := range rows {
			if row.Exchange != ex {
				continue
			}
			item.MarketCount++
			if row.Status == "ROLLING_OK" {
				item.CompleteMarkets++
			} else {
				item.Status = "INCOMPLETE"
			}
			if row.VolumeKRW != nil {
				item.ObservedMarkets++
				total, invalidTotal = addAmount(total, row.VolumeKRW, invalidTotal)
			}
		}
		if item.MarketCount == 0 || invalidTotal {
			item.Status = "INCOMPLETE"
		}
		item.VolumeKRW = total
		item.ObservedVolumeKRW = total
		if total != nil {
			item.SharePct = domestic.Totals[ex].Share
		}
		item.ObservedSharePct = item.SharePct
		items = append(items, item)
	}
	return items
}
