package app

import (
	"math"
	"sort"
	"strings"

	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
	"market-share-go/services/collector/official"
)

const officialDomestic24hBasis = "OFFICIAL_DOMESTIC_24H_TICKERS"

// officialTicker24hProjection turns one complete five-exchange rolling 24h
// ticker snapshot into the canonical analysis window and asset rows. Official
// ticker rows intentionally carry no invented trade count, side, or base
// quantity; SupplementalVolumeKRW marks the entire volume as official evidence
// without trade-detail support.
func officialTicker24hProjection(summary official.DomesticTickerSummary) (marketanalysis.WindowState, []marketanalysis.Row, bool) {
	return projectOfficialTicker24h(summary, false)
}

// officialTickerCoinRows is the projection's coin rows, also from a STALE
// summary: it still holds every exchange, a late one with its last values.
func officialTickerCoinRows(summary official.DomesticTickerSummary) ([]marketanalysis.Row, bool) {
	_, rows, ok := projectOfficialTicker24h(summary, true)
	return rows, ok
}

func projectOfficialTicker24h(summary official.DomesticTickerSummary, acceptStale bool) (marketanalysis.WindowState, []marketanalysis.Row, bool) {
	usable := func(status string) bool { return status == "LIVE" || acceptStale && status == "STALE" }
	if !usable(summary.Status) || summary.TotalVolumeKRW == nil || summary.BithumbVolumeKRW == nil ||
		summary.ExpectedExchangeCount != len(domain.DomesticExchanges) ||
		summary.ReadyExchangeCount != len(domain.DomesticExchanges) ||
		!finiteNonnegative(*summary.TotalVolumeKRW) || !finiteNonnegative(*summary.BithumbVolumeKRW) ||
		*summary.TotalVolumeKRW <= 0 || *summary.BithumbVolumeKRW > *summary.TotalVolumeKRW {
		return marketanalysis.WindowState{}, nil, false
	}

	exchanges := make(map[domain.Exchange]marketanalysis.WindowExchange, len(domain.DomesticExchanges))
	rows := make([]marketanalysis.Row, 0)
	totalFromExchanges := 0.0
	for _, exchange := range domain.DomesticExchanges {
		exchangeRow, found := summary.Exchanges[exchange]
		if !found || !usable(exchangeRow.Status) || exchangeRow.VolumeKRW == nil ||
			!finiteNonnegative(*exchangeRow.VolumeKRW) || len(exchangeRow.Assets) != exchangeRow.MarketCount ||
			exchangeRow.MarketCount < 1 {
			return marketanalysis.WindowState{}, nil, false
		}
		assets := make([]string, 0, len(exchangeRow.Assets))
		for asset := range exchangeRow.Assets {
			assets = append(assets, asset)
		}
		sort.Strings(assets)
		exchangeTotal := 0.0
		for _, rawAsset := range assets {
			asset := strings.ToUpper(strings.TrimSpace(rawAsset))
			volume := exchangeRow.Assets[rawAsset]
			if asset == "" || !finiteNonnegative(volume) {
				return marketanalysis.WindowState{}, nil, false
			}
			exchangeTotal += volume
			if volume == 0 {
				continue
			}
			rows = append(rows, marketanalysis.Row{
				Scope: domain.ScopeDomestic, Exchange: exchange,
				Market: official.CanonicalKRWMarketID(asset), Base: asset, Quote: "KRW",
				VolumeKRW: volume, SupplementalVolumeKRW: volume,
			})
		}
		if !officialTickerAmountMatches(exchangeTotal, *exchangeRow.VolumeKRW) {
			return marketanalysis.WindowState{}, nil, false
		}
		exchanges[exchange] = marketanalysis.WindowExchange{VolumeKRW: *exchangeRow.VolumeKRW}
		totalFromExchanges += *exchangeRow.VolumeKRW
	}
	if !officialTickerAmountMatches(totalFromExchanges, *summary.TotalVolumeKRW) ||
		!officialTickerAmountMatches(exchanges[domain.ExchangeBithumb].VolumeKRW, *summary.BithumbVolumeKRW) {
		return marketanalysis.WindowState{}, nil, false
	}

	share := 100 * *summary.BithumbVolumeKRW / *summary.TotalVolumeKRW
	return marketanalysis.WindowState{
		Status: "CONFIRMED", CoverageStatus: "CONFIRMED", WSStatus: "LIVE",
		CoverageSec: 86_400, GapSeconds: 0, WindowComplete: true, RateDenominatorSec: 86_400,
		DataSource: map[string]any{
			"bar_type":                               "OFFICIAL_ROLLING_24H_TICKER",
			"volume_basis":                           officialDomestic24hBasis,
			"official_24h_ticker_used":               true,
			"official_24h_ticker_status":             summary.Status,
			"official_24h_ticker_as_of_ms":           int64(summary.AsOf * 1_000),
			"official_24h_ticker_collection_span_ms": summary.CollectionSpanMS,
			"official_24h_ticker_exchange_count":     summary.ReadyExchangeCount,
			"bithumb_share_status":                   "AVAILABLE",
			"domestic_input_valid":                   true,
			"unresolved_fx_count":                    uint64(0),
		},
		TotalVolumeKRW: *summary.TotalVolumeKRW, BithumbShare: &share,
		BithumbShareStatus: "AVAILABLE", Exchanges: exchanges,
	}, rows, true
}

func officialTickerAmountMatches(actual, expected float64) bool {
	if !finiteNonnegative(actual) || !finiteNonnegative(expected) {
		return false
	}
	return math.Abs(actual-expected) <= math.Max(1, math.Abs(expected)*1e-9)
}

func finiteNonnegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func cloneAnalysisWindows(source map[int64]marketanalysis.WindowState) map[int64]marketanalysis.WindowState {
	result := make(map[int64]marketanalysis.WindowState, len(source))
	for window, state := range source {
		state.DataSource = cloneStringAnyMap(state.DataSource)
		state.Exchanges = cloneWindowExchanges(state.Exchanges)
		result[window] = state
	}
	return result
}

func cloneStringAnyMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneWindowExchanges(source map[domain.Exchange]marketanalysis.WindowExchange) map[domain.Exchange]marketanalysis.WindowExchange {
	result := make(map[domain.Exchange]marketanalysis.WindowExchange, len(source))
	for exchange, value := range source {
		result[exchange] = value
	}
	return result
}

func replaceDomesticAnalysisRows(current, officialRows []marketanalysis.Row) []marketanalysis.Row {
	result := append([]marketanalysis.Row(nil), officialRows...)
	for _, row := range current {
		if row.Scope != domain.ScopeDomestic {
			result = append(result, row)
		}
	}
	return result
}
