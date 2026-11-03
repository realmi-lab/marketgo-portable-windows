package app

import (
	"market-share-go/internal/domain"
	"market-share-go/internal/indexday"
	official "market-share-go/services/collector/official"
	"math"
	"time"
)

// A current rolling ticker projection, never a replacement for archived hours.
func tickerIndex(day string, universe domain.MarketUniverse, snapshot official.DomesticTickerSummary) indexday.Response {
	end := int64(snapshot.AsOf)
	result := indexday.Response{ContractVersion: "INDEX_TICKER_V1", StorageMode: "TICKER_24H", Day: day, Start: end - 86400, End: end, Status: "COMPLETE", Membership: "CURRENT_KRW_MARKETS", Rows: []indexday.Row{}, Exchanges: []indexday.Exchange{}}
	values := []indexday.RealtimeRow{}
	for _, market := range universe.Markets() {
		if market.Scope != domain.ScopeDomestic || market.Quote != "KRW" {
			continue
		}
		source := snapshot.Exchanges[market.Exchange]
		var amount *float64
		if value, ok := source.Assets[market.Base]; ok && source.Status == "LIVE" && value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
			amount = &value
		}
		status := "COMPLETE"
		if amount == nil {
			status = "UNAVAILABLE"
			result.Status = "PARTIAL"
		}
		row := indexday.Row{Market: indexday.Market{Exchange: string(market.Exchange), Market: market.Symbol, Base: market.Base, Quote: "KRW"}, VolumeKRW: amount, QuoteVolume: amount, Status: status, Sources: []string{source.Source}}
		result.Rows = append(result.Rows, row)
		values = append(values, indexday.RealtimeRow{Market: row.Market, Amount: amount, Partial: amount == nil || snapshot.Status != "LIVE"})
	}
	if snapshot.Status != "LIVE" || end <= 0 || len(values) == 0 {
		result.Status = "PARTIAL"
	}
	result.Domestic = indexday.BuildRealtimeDomestic(values)
	return result
}

func (s *Service) currentTickerIndex(day string) (indexday.Response, error) {
	if s == nil || s.registry == nil || s.officialDomestic == nil || day != time.Now().In(indexday.Seoul).Format(time.DateOnly) {
		return indexday.Response{}, ErrUnsupportedWindow
	}
	return tickerIndex(day, s.registry.Universe(), s.officialDomestic.Snapshot()), nil
}
