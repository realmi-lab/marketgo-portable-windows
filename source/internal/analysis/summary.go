package analysis

import (
	"sort"

	"market-share-go/internal/domain"
)

func (s *buildState) buildQuotes() []QuoteAnalysis {
	quotes := make(map[string]struct{}, len(s.quoteMarkets))
	for quote := range s.quoteMarkets {
		quotes[quote] = struct{}{}
	}
	for _, row := range s.domesticRows {
		if row.Quote != "" {
			quotes[row.Quote] = struct{}{}
		}
	}
	result := make([]QuoteAnalysis, 0, len(quotes))
	for quote := range quotes {
		rows := filterRows(s.domesticRows, func(row Row) bool { return row.Quote == quote })
		bithumbRows := filterRows(rows, func(row Row) bool { return row.Exchange == domain.ExchangeBithumb })
		volume := sumRows(rows, func(row Row) float64 { return row.VolumeKRW })
		bithumbVolume := sumRows(bithumbRows, func(row Row) float64 { return row.VolumeKRW })
		registered, exchanges := 0, make([]string, 0, len(s.quoteMarkets[quote]))
		for exchange, markets := range s.quoteMarkets[quote] {
			registered += len(markets)
			exchanges = append(exchanges, string(exchange))
		}
		sort.Strings(exchanges)
		traded, unresolved := 0, int64(0)
		for _, row := range rows {
			if row.TradeCount > 0 {
				traded++
			}
			unresolved = saturatingAddInt64(unresolved, row.UnresolvedFXCount)
		}
		result = append(result, QuoteAnalysis{
			Quote: quote, VolumeKRW: volume,
			MarketSharePct:        optionalPercent(volume, s.domesticTotal, s.domesticInputValid && s.domesticFXComplete),
			BithumbVolumeKRW:      bithumbVolume,
			BithumbCapturePct:     optionalPercent(bithumbVolume, volume, rowsFXComplete(rows)),
			RegisteredMarketCount: registered,
			BithumbMarketCount:    len(s.quoteMarkets[quote][domain.ExchangeBithumb]),
			TradedMarketCount:     traded, UnresolvedFXCount: unresolved, Exchanges: exchanges,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].VolumeKRW != result[j].VolumeKRW {
			return result[i].VolumeKRW > result[j].VolumeKRW
		}
		return result[i].Quote < result[j].Quote
	})
	return result
}

func (s *buildState) buildSummary(assets []AssetAnalysis) Summary {
	domesticAssets := make(map[string]struct{})
	for _, exchange := range domain.DomesticExchanges {
		for asset := range s.listedAssets[exchange] {
			domesticAssets[asset] = struct{}{}
		}
	}
	coveredVolume := s.coveredVolume(s.listedAssets[domain.ExchangeBithumb])
	upbitCoveredVolume := s.coveredVolume(s.listedAssets[domain.ExchangeUpbit])
	unlistedGap, upbitGap := 0.0, 0.0
	for _, asset := range assets {
		if asset.GapType == "UNLISTED" {
			unlistedGap = finiteSaturatingAdd(unlistedGap, asset.CompetitorVolumeKRW)
		}
		if !contains(asset.DomesticExchanges, string(domain.ExchangeUpbit)) {
			upbitGap = finiteSaturatingAdd(upbitGap, pointerValue(asset.DomesticVolumeKRW))
		}
	}
	bithumbTrades := sumTrades(s.rowsByExchange[domain.ExchangeBithumb])
	selected := s.input.DomesticWindows[s.input.Window]
	return Summary{
		DomesticVolumeKRW: s.domesticTotal, GlobalContextVolumeKRW: s.globalTotal,
		BithumbVolumeKRW: s.bithumbVolume, BithumbSharePct: s.bithumbShare,
		BithumbShareStatus: s.domesticShareStatus(),
		DomesticTradeCount: s.domesticTradeCount, BithumbTradeCount: bithumbTrades,
		BithumbTradeCountSharePct: percent(float64(bithumbTrades), float64(s.domesticTradeCount)),
		DomesticUnresolvedFXCount: unresolvedCount(s.domesticRows),
		GlobalUnresolvedFXCount:   unresolvedCount(s.globalRows), DomesticAssetCount: len(domesticAssets),
		BithumbListedAssetCount:                 len(s.listedAssets[domain.ExchangeBithumb]),
		BithumbListingCoveragePct:               percent(float64(len(s.listedAssets[domain.ExchangeBithumb])), float64(len(domesticAssets))),
		BithumbVolumeWeightedListingCoveragePct: optionalPercent(coveredVolume, s.domesticTotal, s.domesticInputValid && s.domesticFXComplete),
		UnlistedCompetitorVolumeKRW:             unlistedGap,
		UpbitListedAssetCount:                   len(s.listedAssets[domain.ExchangeUpbit]),
		UpbitListingCoveragePct:                 percent(float64(len(s.listedAssets[domain.ExchangeUpbit])), float64(len(domesticAssets))),
		UpbitVolumeWeightedListingCoveragePct:   optionalPercent(upbitCoveredVolume, s.domesticTotal, s.domesticInputValid && s.domesticFXComplete),
		UpbitUnlistedCompetitorVolumeKRW:        upbitGap, AnalyzedAssetCount: len(assets),
		AnalysisHasAssetLimit: false, SelectedWindowComplete: selected.WindowComplete,
		SelectedWindowCoverageSec: min(s.input.Window, selected.CoverageSec),
		SelectedWindowRequiredSec: s.input.Window,
		BithumbSharedListing:      s.buildSharedListingSummary(assets),
	}
}

func (s *buildState) buildSharedListingSummary(assets []AssetAnalysis) SharedListingSummary {
	result := SharedListingSummary{Status: s.attribution.Status}
	currentReady := s.domesticInputValid && s.domesticFXComplete
	sharedAssets := make([]string, 0)
	for _, asset := range assets {
		if !asset.ListedOnBithumb || asset.DomesticExchangeCount < 2 {
			continue
		}
		sharedAssets = append(sharedAssets, asset.Asset)
		result.AssetCount++
		if asset.DomesticVolumeKRW == nil {
			currentReady = false
			continue
		}
		result.CurrentDomesticVolumeKRW = finiteSaturatingAdd(result.CurrentDomesticVolumeKRW, *asset.DomesticVolumeKRW)
		result.CurrentBithumbVolumeKRW = finiteSaturatingAdd(result.CurrentBithumbVolumeKRW, asset.BithumbVolumeKRW)
	}
	result.CurrentSharePct = optionalPercent(result.CurrentBithumbVolumeKRW, result.CurrentDomesticVolumeKRW, currentReady)
	result.DomesticVolumeCoveragePct = optionalPercent(result.CurrentDomesticVolumeKRW, s.domesticTotal, currentReady)
	if s.attribution.Status != "CONFIRMED" {
		return result
	}

	previousDomestic, previousBithumb := 0.0, 0.0
	previousReady := true
	for _, asset := range sharedAssets {
		row, ok := s.attributionByAsset[asset]
		if !ok {
			previousReady = false
			break
		}
		previousDomestic = finiteSaturatingAdd(previousDomestic, row.PreviousDomesticVolumeKRW)
		previousBithumb = finiteSaturatingAdd(previousBithumb, row.PreviousBithumbVolumeKRW)
	}
	if !previousReady {
		result.Status = "UNAVAILABLE"
		return result
	}
	result.PreviousDomesticVolumeKRW = pointer(previousDomestic)
	result.PreviousBithumbVolumeKRW = pointer(previousBithumb)
	result.PreviousSharePct = optionalPercent(previousBithumb, previousDomestic, true)
	result.ShareChangePP = difference(result.CurrentSharePct, result.PreviousSharePct)
	return result
}

func (s *buildState) coveredVolume(assets map[string]struct{}) float64 {
	total := 0.0
	for asset := range assets {
		total += sumRows(s.domesticByAsset[asset], func(row Row) float64 { return row.VolumeKRW })
	}
	return total
}

func unresolvedCount(rows []Row) int64 {
	total := int64(0)
	for _, row := range rows {
		total = saturatingAddInt64(total, row.UnresolvedFXCount)
	}
	return total
}
