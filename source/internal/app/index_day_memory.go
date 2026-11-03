package app

import (
	"market-share-go/internal/domain"
	"market-share-go/internal/indexday"
	officialdata "market-share-go/services/collector/official"
	"strings"
	"time"
)

// The portable view reads only the local collector's bounded candle cache.
// It shares the archive's aggregation and missing-value rules; no history is fetched.
func memoryIndexDay(day string, universe domain.MarketUniverse, cached []officialdata.CachedAlignedHour, now time.Time) (indexday.Response, error) {
	day = strings.TrimPrefix(day, "rolling:")
	markets := make([]indexday.Market, 0)
	byKey := make(map[string]indexday.Market)
	for _, market := range universe.Markets() {
		if market.Scope != domain.ScopeDomestic {
			continue
		}
		row := indexday.Market{Exchange: string(market.Exchange), Market: market.Symbol, Base: market.Base, Quote: market.Quote}
		markets = append(markets, row)
		byKey[row.Exchange+"\x00"+row.Market] = row
	}
	hours := make([]indexday.Hour, 0, len(cached))
	for _, candle := range cached {
		market, ok := byKey[string(candle.Exchange)+"\x00"+candle.Market]
		if !ok || market.Quote != "KRW" {
			continue
		}
		amount := candle.QuoteVolume
		hours = append(hours, indexday.Hour{Market: market, Start: candle.StartMS / 1000, QuoteVolume: &amount, VolumeKRW: &amount, Source: officialdata.Aligned24hSource})
	}
	result, err := indexday.BuildAt(day, markets, hours, now)
	if err == nil {
		result.StorageMode = "MEMORY_HOURLY"
		if day == now.In(indexday.Seoul).Format(time.DateOnly) {
			result.StorageMode = "ROLLING_24H_LATEST"
		}
	}
	return result, err
}
