package analysis

import (
	"context"
	"math"
	"sort"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/domain"
)

const analysisDefinition = "KRW quotes are direct; every non-KRW quote is converted through fresh quote/USDT and Bithumb USDT/KRW websocket prices"

type BuildInput struct {
	ctx                    context.Context
	Window                 int64
	Rows                   []Row
	PreviousRows           []Row
	ComparisonRows         []Row
	PreviousComparisonRows []Row
	Markets                []domain.MarketPair
	DomesticWindows        map[int64]WindowState
	ContractIdentities     map[string]ContractIdentityProof
	Comparison             Comparison
	CrossScopeComplete     bool
}

type buildState struct {
	input              BuildInput
	domesticRows       []Row
	globalRows         []Row
	rowsByExchange     map[domain.Exchange][]Row
	marketsByExchange  map[domain.Exchange][]domain.MarketPair
	listedAssets       map[domain.Exchange]map[string]struct{}
	quoteMarkets       map[string]map[domain.Exchange]map[string]struct{}
	domesticByAsset    map[string][]Row
	globalByAsset      map[string][]Row
	domesticTotal      float64
	globalTotal        float64
	globalFXComplete   bool
	domesticFXComplete bool
	domesticInputValid bool
	domesticTradeCount int64
	bithumbVolume      float64
	bithumbShare       *float64
	attribution        Attribution
	attributionByAsset map[string]AttributionAsset
	rateDenominatorSec int64
}

func Build(input BuildInput) Analysis {
	return Finalize(BuildUnfinalized(input))
}

// BuildUnfinalized assembles the numerical analysis without deriving the
// presentation-only expert summary. Callers that still need to attach
// collectors, notices, liquidity, or reaction context can finalize once after
// those fields are present instead of doing the same derived work twice.
func analysisCanceled(ctx context.Context) bool { return ctx != nil && ctx.Err() != nil }

// BuildUnfinalizedContext never publishes partial work on cancellation.
func BuildUnfinalizedContext(ctx context.Context, input BuildInput) (Analysis, error) {
	input.ctx = ctx
	if err := ctx.Err(); err != nil {
		return Analysis{}, err
	}
	result := BuildUnfinalized(input)
	if err := ctx.Err(); err != nil {
		return Analysis{}, err
	}
	return result, nil
}
func BuildUnfinalized(input BuildInput) Analysis {

	state := prepareBuild(input)
	if analysisCanceled(input.ctx) {
		return Analysis{}
	}
	exchanges := state.buildExchanges()
	assets := state.buildAssets()
	if analysisCanceled(input.ctx) {
		return Analysis{}
	}
	quotes := state.buildQuotes()
	momentum := buildMomentum(input.DomesticWindows)
	result := Analysis{
		WindowSec: input.Window, Window: aggregate.WindowName(input.Window), Definition: analysisDefinition,
		Summary: state.buildSummary(assets), Findings: []Finding{},
		ChangeAttribution: state.attribution, Momentum: momentum,
		Exchanges: exchanges, QuoteMarkets: quotes, Assets: assets,
	}
	result.Findings = buildFindings(result)
	return result
}

func prepareBuild(input BuildInput) *buildState {
	referenceMarkets := make([]domain.MarketPair, 0, len(input.Markets))
	for _, market := range input.Markets {
		if analysisCanceled(input.ctx) {
			return nil
		}
		if market.Scope == domain.ScopeGlobal && !domain.IsGlobalReferenceExchange(market.Exchange) {
			continue
		}
		referenceMarkets = append(referenceMarkets, market)
	}
	input.Markets = referenceMarkets
	state := &buildState{
		input: input, rowsByExchange: make(map[domain.Exchange][]Row),
		marketsByExchange:  make(map[domain.Exchange][]domain.MarketPair),
		listedAssets:       make(map[domain.Exchange]map[string]struct{}),
		globalFXComplete:   true,
		domesticFXComplete: true,
		quoteMarkets:       make(map[string]map[domain.Exchange]map[string]struct{}),
		domesticByAsset:    make(map[string][]Row), globalByAsset: make(map[string][]Row),
	}
	for _, exchange := range domain.DomesticExchanges {
		if analysisCanceled(input.ctx) {
			return nil
		}
		state.listedAssets[exchange] = make(map[string]struct{})
	}
	domesticMarketInputValid := true
	for _, market := range input.Markets {
		if analysisCanceled(input.ctx) {
			return nil
		}
		if market.Scope != domain.ScopeDomestic {
			continue
		}
		if !isDomesticAnalysisExchange(market.Exchange) {
			domesticMarketInputValid = false
			continue
		}
		state.marketsByExchange[market.Exchange] = append(state.marketsByExchange[market.Exchange], market)
		state.listedAssets[market.Exchange][market.Base] = struct{}{}
		if state.quoteMarkets[market.Quote] == nil {
			state.quoteMarkets[market.Quote] = make(map[domain.Exchange]map[string]struct{})
		}
		if state.quoteMarkets[market.Quote][market.Exchange] == nil {
			state.quoteMarkets[market.Quote][market.Exchange] = make(map[string]struct{})
		}
		state.quoteMarkets[market.Quote][market.Exchange][market.Symbol] = struct{}{}
	}
	var domesticRowsValid bool
	state.domesticRows, domesticRowsValid = canonicalDomesticRows(input.Rows)
	state.domesticInputValid = domesticMarketInputValid && domesticRowsValid
	for _, row := range state.domesticRows {
		if analysisCanceled(input.ctx) {
			return nil
		}
		state.rowsByExchange[row.Exchange] = append(state.rowsByExchange[row.Exchange], row)
		state.domesticByAsset[row.Base] = append(state.domesticByAsset[row.Base], row)
		state.domesticTotal = finiteSaturatingAdd(state.domesticTotal, row.VolumeKRW)
		state.domesticTradeCount = saturatingAddInt64(state.domesticTradeCount, row.TradeCount)
		if row.Exchange == domain.ExchangeBithumb {
			state.bithumbVolume = finiteSaturatingAdd(state.bithumbVolume, row.VolumeKRW)
		}
		if row.UnresolvedFXCount > 0 {
			state.domesticFXComplete = false
		}
	}
	for _, row := range input.Rows {
		if analysisCanceled(input.ctx) {
			return nil
		}
		switch row.Scope {
		case domain.ScopeGlobal:
			if !domain.IsGlobalReferenceExchange(row.Exchange) {
				continue
			}
			state.globalRows = append(state.globalRows, row)
			state.globalByAsset[row.Base] = append(state.globalByAsset[row.Base], row)
			state.globalTotal = finiteSaturatingAdd(state.globalTotal, row.VolumeKRW)
			if row.UnresolvedFXCount > 0 {
				state.globalFXComplete = false
			}
		}
	}
	state.bithumbShare = optionalPercent(
		state.bithumbVolume,
		state.domesticTotal,
		state.domesticInputValid && state.domesticFXComplete,
	)
	comparisonRows := input.Rows
	previousComparisonRows := input.PreviousRows
	if input.ComparisonRows != nil {
		comparisonRows = input.ComparisonRows
	}
	if input.PreviousComparisonRows != nil {
		previousComparisonRows = input.PreviousComparisonRows
	}
	state.attribution = CompareWindows(comparisonRows, previousComparisonRows, input.Comparison)
	state.attributionByAsset = make(map[string]AttributionAsset, len(state.attribution.Assets))
	for _, row := range state.attribution.Assets {
		if analysisCanceled(input.ctx) {
			return nil
		}
		state.attributionByAsset[row.Asset] = row
	}
	selected := input.DomesticWindows[input.Window]
	state.rateDenominatorSec = max(1, min(input.Window, selected.CoverageSec))
	return state
}

func (s *buildState) buildExchanges() []ExchangeAnalysis {
	result := make([]ExchangeAnalysis, 0, len(domain.DomesticExchanges))
	for _, exchange := range domain.DomesticExchanges {
		rows := s.rowsByExchange[exchange]
		totals := marketTotals(rows)
		assets := make(map[string]struct{})
		for _, market := range s.marketsByExchange[exchange] {
			assets[market.Base] = struct{}{}
		}
		tradedMarkets := 0
		for _, row := range rows {
			if row.TradeCount > 0 {
				tradedMarkets++
			}
		}
		result = append(result, ExchangeAnalysis{
			Exchange: exchange, MarketTotals: totals,
			SharePct:                             optionalPercent(totals.VolumeKRW, s.domesticTotal, s.domesticInputValid && s.domesticFXComplete),
			TradeCountSharePct:                   percent(float64(totals.TradeCount), float64(s.domesticTradeCount)),
			TradesPerSecond:                      float64(totals.TradeCount) / float64(s.rateDenominatorSec),
			RegisteredMarketCount:                len(s.marketsByExchange[exchange]),
			RegisteredAssetCount:                 len(assets),
			ListedAssetDomesticVolumeCoveragePct: optionalPercent(s.coveredVolume(assets), s.domesticTotal, s.domesticInputValid && s.domesticFXComplete),
			TradedMarketCount:                    tradedMarkets,
			Concentration:                        concentration(rows),
		})
	}
	return result
}

func marketTotals(rows []Row) MarketTotals {
	result := MarketTotals{}
	supplementalVolume := 0.0
	detailIncomplete := false
	for _, row := range rows {
		detailIncomplete = detailIncomplete || row.TradeDetailIncomplete
		result.OfficialReplacementVolumeKRW = finiteSaturatingAdd(result.OfficialReplacementVolumeKRW, row.OfficialReplacementVolumeKRW)
		result.VolumeKRW = finiteSaturatingAdd(result.VolumeKRW, row.VolumeKRW)
		supplementalVolume = finiteSaturatingAdd(supplementalVolume, row.SupplementalVolumeKRW)
		result.TradeCount = saturatingAddInt64(result.TradeCount, row.TradeCount)
		result.UnresolvedFXCount = saturatingAddInt64(result.UnresolvedFXCount, row.UnresolvedFXCount)
		result.BuyVolumeKRW = finiteSaturatingAdd(result.BuyVolumeKRW, row.BuyVolumeKRW)
		result.SellVolumeKRW = finiteSaturatingAdd(result.SellVolumeKRW, row.SellVolumeKRW)
	}
	resolvedTrades := result.TradeCount - result.UnresolvedFXCount
	result.TradeDetailIncomplete = detailIncomplete
	if resolvedTrades > 0 && !detailIncomplete {
		result.AverageTradeKRW = pointer(max(0, result.VolumeKRW-supplementalVolume) / float64(resolvedTrades))
	}
	result.AggressorBuyPct = percent(result.BuyVolumeKRW, result.BuyVolumeKRW+result.SellVolumeKRW)
	if detailIncomplete {
		result.AggressorBuyPct = nil
	}
	return result
}

func concentration(rows []Row) Concentration {
	volumes := make(map[string]float64)
	for _, row := range rows {
		volumes[row.Base] += row.VolumeKRW
	}
	type item struct {
		asset  string
		volume float64
	}
	ranked := make([]item, 0, len(volumes))
	result := Concentration{}
	total := 0.0
	valid := true
	for asset, volume := range volumes {
		ranked = append(ranked, item{asset, volume})
		total += volume
		valid = valid && validAnalysisNumber(volume)
		if volume > 0 {
			result.TradedAssetCount++
		}
	}
	// A valid market amount can overflow when combined with another market.
	// Keep the known asset count, but do not publish ratios of invalid totals.
	if !valid || !validAnalysisNumber(total) {
		return result
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].volume != ranked[j].volume {
			return ranked[i].volume > ranked[j].volume
		}
		return ranked[i].asset < ranked[j].asset
	})
	shares := make([]float64, 0, len(ranked))
	for _, row := range ranked {
		if total > 0 {
			shares = append(shares, row.volume/total*100)
		}
	}
	if len(shares) == 0 {
		return result
	}
	hhi := 0.0
	for _, share := range shares {
		hhi += share * share
	}
	result.HHI = pointer(hhi)
	if ranked[0].volume > 0 {
		result.LeadingAsset = ranked[0].asset
	}
	result.LeadingAssetSharePct = pointer(shares[0])
	top5, top10 := 0.0, 0.0
	for index, share := range shares {
		if index < 5 {
			top5 += share
		}
		if index < 10 {
			top10 += share
		}
	}
	result.Top5SharePct, result.Top10SharePct = pointer(top5), pointer(top10)
	return result
}

func buildMomentum(windows map[int64]WindowState) []Momentum {
	result := make([]Momentum, 0, len(aggregate.DefaultWindows))
	for _, duration := range aggregate.DefaultWindows {
		window := windows[duration]
		coverage := min(duration, window.CoverageSec)
		denominator := max(int64(1), coverage)
		if window.RateDenominatorSec > 0 {
			denominator = min(duration, window.RateDenominatorSec)
		}
		status := window.CoverageStatus
		if status == "" {
			status = window.Status
		}
		if status == "" {
			status = "UNAVAILABLE"
		}
		shareStatus := window.BithumbShareStatus
		if shareStatus == "" {
			shareStatus = "UNAVAILABLE"
			if window.BithumbShare != nil {
				shareStatus = "AVAILABLE"
			}
		}
		volume := window.Exchanges[domain.ExchangeBithumb].VolumeKRW
		result = append(result, Momentum{
			Window: aggregate.WindowName(duration), DurationSec: duration, SharePct: window.BithumbShare,
			ShareStatus: shareStatus, UnresolvedFXCount: window.UnresolvedFXCount,
			VolumeKRW: volume, VolumePerSecKRW: volume / float64(denominator),
			MarketVolumePerSec: window.TotalVolumeKRW / float64(denominator),
			RateDenominatorSec: denominator, Status: window.Status,
			CoverageStatus: status, WSStatus: window.WSStatus, CoverageSec: coverage,
			GapSeconds: window.GapSeconds, DataSource: nonnilMap(window.DataSource),
			WindowComplete: window.WindowComplete,
		})
	}
	return result
}

func (s *buildState) domesticShareStatus() string {
	switch {
	case !s.domesticInputValid:
		return "INVALID_DOMESTIC_INPUT"
	case !s.domesticFXComplete:
		return "UNRESOLVED_FX"
	case s.domesticTotal <= 0:
		return "NO_DOMESTIC_VOLUME"
	default:
		return "AVAILABLE"
	}
}

func nonnilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

func hasObservedRows(rows []Row) bool {
	for _, row := range rows {
		if row.TradeCount > 0 || row.SupplementalVolumeKRW > 0 || row.TradeDetailIncomplete {
			return true
		}
	}
	return false
}

func sumRows(rows []Row, value func(Row) float64) float64 {
	total := 0.0
	for _, row := range rows {
		total = finiteSaturatingAdd(total, value(row))
	}
	return total
}

func sumTrades(rows []Row) int64 {
	total := int64(0)
	for _, row := range rows {
		total = saturatingAddInt64(total, row.TradeCount)
	}
	return total
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func finiteOrZero(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

func finiteSaturatingAdd(left, right float64) float64 {
	if math.IsNaN(left) || math.IsInf(left, 0) || left < 0 {
		left = 0
	}
	if math.IsNaN(right) || math.IsInf(right, 0) || right < 0 {
		return left
	}
	if left > math.MaxFloat64-right {
		return math.MaxFloat64
	}
	return left + right
}

func saturatingAddInt64(left, right int64) int64 {
	if left < 0 {
		left = 0
	}
	if right < 0 {
		return left
	}
	if left > math.MaxInt64-right {
		return math.MaxInt64
	}
	return left + right
}
