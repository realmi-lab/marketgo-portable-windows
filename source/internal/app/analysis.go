package app

import (
	"context"
	"errors"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	officialdata "market-share-go/contracts/collection/official"
	"market-share-go/contracts/collection/registry"
	"market-share-go/contracts/collection/stream"
	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
	"market-share-go/internal/fx"
	"market-share-go/internal/notice"
	"market-share-go/internal/orderbook"
	"market-share-go/internal/reconcile"
)

var ErrUnsupportedWindow = errors.New("unsupported rolling window")
var ErrUnsupportedMarketScope = errors.New("market_scope must be KRW or ALL_DOMESTIC_CONVERTED")

type analysisOperationalSnapshot = operationalSnapshot

func (s *Service) Analysis(ctx context.Context, window int64, marketScope string, requestedNotional float64) (marketanalysis.Analysis, error) {
	return s.AnalysisWithOptions(ctx, window, marketScope, requestedNotional, marketanalysis.RequestOptions{})
}

func (s *Service) AnalysisWithOptions(ctx context.Context, window int64, marketScope string, requestedNotional float64, options marketanalysis.RequestOptions) (marketanalysis.Analysis, error) {
	if s.intelligenceEvidence != nil {
		return s.intelligenceEvidence.AnalysisWithOptions(ctx, window, marketScope, requestedNotional, options)
	}
	if !aggregate.SupportsWindow(window) {
		return marketanalysis.Analysis{}, ErrUnsupportedWindow
	}
	if marketScope != "KRW" && marketScope != "ALL_DOMESTIC_CONVERTED" {
		return marketanalysis.Analysis{}, ErrUnsupportedMarketScope
	}
	if s.runtime == nil || s.registry == nil {
		return marketanalysis.Analysis{}, ErrStopped
	}
	notional, err := s.referenceNotional(requestedNotional)
	if err != nil {
		return marketanalysis.Analysis{}, err
	}
	options = normalizedAnalysisOptions(options)
	s.analysisMemoOnce.Do(func() { s.analysisMemo = newAnalysisMemo() })
	key := analysisKey{window: window, scope: marketScope, notional: notional, reactionPreWindowSec: options.ReactionPreWindowSec, reactionPostWindowSec: options.ReactionPostWindowSec}
	return s.analysisMemo.get(ctx, key, func(calculationCtx context.Context) (marketanalysis.Analysis, error) {
		return s.calculateAnalysis(calculationCtx, window, marketScope, notional, options)
	})
}

func normalizedAnalysisOptions(options marketanalysis.RequestOptions) marketanalysis.RequestOptions {
	if options.ReactionPreWindowSec < 1 {
		options.ReactionPreWindowSec = 1800
	}
	if options.ReactionPostWindowSec < 1 {
		options.ReactionPostWindowSec = 1800
	}
	return options
}

func (s *Service) calculateAnalysis(ctx context.Context, window int64, marketScope string, notional float64, options marketanalysis.RequestOptions) (marketanalysis.Analysis, error) {
	mark := s.analysisTiming(window, marketScope)
	defer mark("finish")
	// Confirmed analysis stops at the last fully closed second. The sub-second
	// realtime tick remains available separately as a provisional view.
	trendState := s.officialTickerTrendState()
	trendStart, trendEnd, trendIntervalReady := officialTickerTrendInterval(trendState)
	snapshot, err := s.sharedAnalysisRead(ctx, time.Now().Unix()-1, trendStart, trendEnd, trendIntervalReady)
	mark("shared_read")
	if err != nil {
		return marketanalysis.Analysis{}, err
	}
	endSecond := domain.UnixSecond(snapshot.endSecond)
	asOf := endSecond + 1
	read := snapshot.view
	operational := snapshot.operational
	windows := cloneAnalysisWindows(snapshot.windows[marketScope])
	var official24hSummary officialdata.DomesticTickerSummary
	var official24hRows []marketanalysis.Row
	official24hAvailable := false
	if s.officialDomestic != nil {
		official24hSummary = s.officialDomestic.Snapshot()
		if marketScope == "KRW" {
			if officialWindow, rows, ok := officialTicker24hProjection(official24hSummary); ok {
				windows[marketanalysis.RollingWindowSec] = officialWindow
				official24hRows = rows
				official24hAvailable = true
			}
		}
	}
	currentRows, previousRows, comparison := analysisWindowRows(snapshot, window, marketScope)
	if window == marketanalysis.RollingWindowSec && official24hAvailable {
		currentRows = replaceDomesticAnalysisRows(currentRows, official24hRows)
	}
	markets := filterAnalysisMarkets(operational.universe.Markets(), marketScope)
	collectorHealth := operational.collectors
	registryHealth := operational.registry
	globalCoverage := read.globalCoverageFor(int64(endSecond)-window+1, int64(endSecond))
	crossScopeComplete := windows[window].WindowComplete && globalCoverage.WindowComplete &&
		globalAnalysisReady(markets, registryHealth, collectorHealth)
	result, err := marketanalysis.BuildUnfinalizedContext(ctx, marketanalysis.BuildInput{
		Window: window, Rows: currentRows, PreviousRows: previousRows,
		ComparisonRows:         filterAnalysisRowsByExchanges(currentRows, comparison.Included),
		PreviousComparisonRows: filterAnalysisRowsByExchanges(previousRows, comparison.Included),
		Markets:                markets, DomesticWindows: windows, ContractIdentities: s.contractIdentities,
		Comparison: comparison, CrossScopeComplete: crossScopeComplete,
	})
	if err != nil {
		return marketanalysis.Analysis{}, err
	}
	result.Rolling24h = snapshot.rolling
	result.OfficialDomestic24h = official24hSummary
	result.OfficialTicker1m = enrichOfficialTickerTrend(trendState, read, trendStart, trendEnd, trendIntervalReady)
	mark("base_analysis")
	bookMarkets := orderBookMarkets(markets)
	activity := s.collectors.MarketActivitySnapshot(operational.generatedAt)
	quiet := quietOrderBookMarkets(activity, s.runtime.OrderBookStaleAfterSec())
	bookSnapshot, err := s.runtime.OrderBookSnapshotWithQuiet(
		ctx, bookMarkets, notional, quiet, time.Time{},
	)
	mark("book_snapshot")
	if err != nil {
		return marketanalysis.Analysis{}, err
	}
	bookRows := bookSnapshot.Rows
	weights := make(map[string]float64, len(result.Assets))
	for _, asset := range result.Assets {
		if asset.DomesticVolumeKRW != nil {
			weights[asset.Asset] = *asset.DomesticVolumeKRW
		}
	}
	result.Liquidity, err = marketanalysis.BuildLiquidityContext(ctx, bookRows, marketanalysis.LiquidityOptions{
		NotionalKRW: notional, SlippageNotionalsKRW: slippageScenarioNotionalsKRW(), QuarantineBPS: s.priceGapQuarantineBPS, Weights: weights,
		TakerFees:              liquidityTakerFees(markets),
		ContractIdentities:     s.contractIdentities,
		GlobalCoverageComplete: crossScopeComplete,
	})
	if err != nil {
		return marketanalysis.Analysis{}, err
	}
	mark("liquidity")
	hasGlobalBookMarket := false
	for _, market := range markets {
		if market.Scope == domain.ScopeGlobal && collectorHealth[market.Exchange].OrderBookEnabled {
			hasGlobalBookMarket = true
			break
		}
	}
	if !hasGlobalBookMarket {
		result.Liquidity.GlobalOrderBookStatus = "DISABLED"
	}
	microstructure := make(map[string]marketanalysis.AssetMicrostructure, len(result.Liquidity.Assets))
	for _, row := range result.Liquidity.Assets {
		microstructure[row.Asset] = row
	}
	for index := range result.Assets {
		result.Assets[index].Microstructure = microstructure[result.Assets[index].Asset]
	}
	bookEvidence := buildOrderBookEvidence(
		bookSnapshot.Health, activity, s.runtime.OrderBookStaleAfterSec(),
	)
	if err := s.attachAnalysisEnvelope(
		ctx, &result, asOf, read.version, marketScope, windows[window],
		globalCoverage, crossScopeComplete, options, operational, markets, bookEvidence, bookSnapshot,
	); err != nil {
		return marketanalysis.Analysis{}, err
	}
	mark("envelope")
	// The quality screen lists these records from the analysis; only the
	// snapshot carried them, so the screen always said none had arrived.
	result.DataQuality["observation_gaps"] = qualityObservationGaps(read.coverageState, int64(asOf))
	result.OfficialBinance24h = attachBinancePremiumBithumbMidpoints(
		result.OfficialBinance24h, bookRows, result.Liquidity.Assets, operational.generatedAt,
	)
	if err := ctx.Err(); err != nil {
		return marketanalysis.Analysis{}, err
	}
	result = marketanalysis.Finalize(result)
	result.DataQuality["numeric_integrity"] = marketanalysis.CheckNumericIntegrity(result)
	if err := ctx.Err(); err != nil {
		return marketanalysis.Analysis{}, err
	}
	if err := assignAnalysisContextID(&result); err != nil {
		return marketanalysis.Analysis{}, err
	}
	return result, nil
}

// Opt-in phase timings include no request bodies, SQL, or private evidence.
// They distinguish CPU work from time waiting for shared owners/providers.
func (s *Service) analysisTiming(window int64, scope string) func(string) {
	if os.Getenv("MARKET_SHARE_ANALYSIS_TIMING") != "1" || s.logger == nil {
		return func(string) {}
	}
	started := time.Now()
	last := started
	return func(phase string) {
		now := time.Now()
		s.logger.Info("analysis phase", "phase", phase, "window", window, "scope", scope,
			"duration_ms", now.Sub(last).Milliseconds(), "total_ms", now.Sub(started).Milliseconds())
		last = now
	}
}

func (s *Service) captureAnalysisOperational(now time.Time) *analysisOperationalSnapshot {
	return s.operationalState(now)
}

func globalAnalysisReady(
	markets []domain.MarketPair,
	registryHealth registry.Snapshot,
	collectorHealth map[domain.Exchange]collector.ExchangeHealth,
) bool {
	expected := make(map[domain.Exchange][]domain.MarketPair)
	for _, market := range markets {
		if market.Scope == domain.ScopeGlobal && domain.IsGlobalReferenceExchange(market.Exchange) {
			expected[market.Exchange] = append(expected[market.Exchange], market)
		}
	}
	if len(expected) == 0 {
		return false
	}
	for exchange, exchangeMarkets := range expected {
		marketCount := len(exchangeMarkets)
		registered := registryHealth.Exchanges[exchange]
		collected := collectorHealth[exchange]
		if marketCount <= 0 || registered.Error != "" || registered.MarketCount != marketCount ||
			!collected.Enabled || !collected.RealtimeReady || !collected.TradeStreamReady ||
			collected.MarketCount != marketCount || collected.SubscribedMarketCount != marketCount ||
			collected.ConnectedMarketCount != marketCount || collected.ActiveTradeParseErrorChunks > 0 ||
			collected.ActiveSubscriptionErrorChunks > 0 ||
			!collectorSubscriptionSetMatches(exchangeMarkets, collected) {
			return false
		}
	}
	return true
}

func liquidityTakerFees(markets []domain.MarketPair) map[string]marketanalysis.TakerFee {
	result := make(map[string]marketanalysis.TakerFee)
	for _, market := range markets {
		if market.TakerFee == nil {
			continue
		}
		result[marketanalysis.LiquidityFeeKey(market.Exchange, market.Symbol)] = marketanalysis.TakerFee{Rate: *market.TakerFee, Source: market.FeeSource}
	}
	return result
}

func (s *Service) attachAnalysisEnvelope(
	ctx context.Context,
	result *marketanalysis.Analysis,
	now domain.UnixSecond,
	aggregationVersion uint64,
	marketScope string,
	selected marketanalysis.WindowState,
	globalCoverage coverage.Result,
	crossScopeComplete bool,
	options marketanalysis.RequestOptions,
	operational *analysisOperationalSnapshot,
	markets []domain.MarketPair,
	bookEvidence orderBookEvidence,
	bookSnapshot OrderBookSnapshot,
) error {
	generatedAt := float64(now)
	metrics := operational.metrics
	version := aggregationVersion
	comparisonEnabled := result.WindowSec != 86400
	comparisonWindow := int64(0)
	var comparisonStart, comparisonEnd *float64
	if comparisonEnabled {
		comparisonWindow = result.WindowSec
		start := generatedAt - float64(2*result.WindowSec)
		end := generatedAt - float64(result.WindowSec)
		comparisonStart, comparisonEnd = &start, &end
	}
	status := selected.Status
	if status == "" {
		status = selected.CoverageStatus
	}
	if status == "CONFIRMED" && result.Summary.DomesticUnresolvedFXCount > 0 {
		status = "PROVISIONAL"
	}
	result.GeneratedAt = generatedAt
	result.Context = marketanalysis.Context{
		MarketScope: marketScope, MarketScopeLabel: marketScopeLabel(marketScope), WindowSec: result.WindowSec,
		ComparisonEnabled: comparisonEnabled, ComparisonWindowSec: comparisonWindow, AssetScope: "ALL", AsOf: generatedAt,
		AnalysisVersion: version, AggregationVersion: aggregationVersion,
		SelectedWindowComplete: selected.WindowComplete && (status == "CONFIRMED" || status == "PROVISIONAL"),
		SelectedWindowStatus:   status, SelectedWindowCoverageSec: selected.CoverageSec, SelectedWindowRequiredSec: result.WindowSec,
		CurrentIntervalStart: generatedAt - float64(result.WindowSec), CurrentIntervalEnd: generatedAt,
		ComparisonIntervalStart: comparisonStart, ComparisonIntervalEnd: comparisonEnd,
		DataSource:       selected.DataSource,
		OrderBookAsOf:    float64(bookSnapshot.AsOf.UnixNano()) / float64(time.Second),
		OrderBookVersion: bookSnapshot.Version,
	}
	result.OfficialBinance24h = s.officialBinance24hSnapshot(operational.generatedAt)
	if s.movementTracker != nil {
		result.OfficialShareMonitor = filterMovementStateNotices(s.movementTracker.State())
	}
	result.RealtimeFlow = s.RealtimeFlowState()
	if notices, err := s.NoticeSnapshot(ctx); err == nil {
		result.Notices = notices
	} else {
		s.logger.Warn("analysis notice snapshot unavailable", "error", err)
	}
	attachRollingNoticeEvidence(&result.Rolling24h, result.Notices, int64(generatedAt*1000))
	health := operational.health
	collectorHealth := operational.collectors
	registryHealth := operational.registry
	queueSize, _ := s.runtime.QueueState()
	fxHealth := s.runtime.FXSnapshot(time.Unix(int64(now), 0))
	result.DataQuality = analysisDataQuality(*result, selected, health, metrics, tradeAccounting(metrics, queueSize, collectorHealth), collectorHealth, registryHealth, fxHealth, operational.consumerAlive, operational.engine)
	result.DataQuality["aggregation_version"] = aggregationVersion
	result.DataQuality["as_of_kind"] = "LAST_CLOSED_SECOND_BOUNDARY"
	result.DataQuality["confirmation_semantics"] = "TRANSPORT_COVERAGE_ONLY"
	result.DataQuality["official_reconciled"] = false
	if selected.DataSource["official_24h_ticker_used"] == true {
		result.DataQuality["confirmation_semantics"] = "OFFICIAL_COMPLETE_ROLLING_24H_TICKER"
		result.DataQuality["official_reconciled"] = true
	}
	result.DataQuality["global_selected_window_status"] = "POLICY_DISABLED"
	result.DataQuality["global_selected_window_complete"] = false
	result.DataQuality["global_selected_window_coverage_sec"] = int64(0)
	result.DataQuality["global_selected_window_gap_sec"] = int64(0)
	result.DataQuality["global_realtime_policy"] = "POLICY_DISABLED"
	result.DataQuality["binance_reference_source"] = "OFFICIAL_ROLLING_24H_TICKER_REFERENCE_ONLY"
	result.DataQuality["cross_scope_release_ready"] = crossScopeComplete
	attachOrderBookQuality(result.DataQuality, bookEvidence)
	result.Collectors = collectorSnapshot(collectorHealth)
	result.Registry = registrySnapshot(registryHealth)
	result.Readiness = cloneReadiness(operational.readiness)
	result.FX = fxSnapshotMap(fxHealth)
	// Every pinned analysis stream for this observation shares identical raw
	// event-study reads. Per-scope attribution is still derived independently.
	// Listing evidence must use the same quote/scope filter as the requested
	// analysis. A BTC-quoted listing is not evidence of a KRW listing.
	listedExchangesByAsset := analysisListedExchanges(markets)
	reactionProviders := marketanalysis.ReactionProviders{PreWindowSec: options.ReactionPreWindowSec, PostWindowSec: options.ReactionPostWindowSec, Study: func(item notice.Item, asset string, anchorTSMS int64, _ string) (map[string]any, error) {
		normalizedAsset := strings.ToUpper(strings.TrimSpace(asset))
		anchor := anchorTSMS / 1000
		input := marketanalysis.EventEffectInput{
			Asset: normalizedAsset, Exchange: item.Exchange, MarketScope: marketScope, AnchorTSMS: anchorTSMS,
			PreWindowSec: options.ReactionPreWindowSec, PostWindowSec: options.ReactionPostWindowSec,
			Now: int64(now), RetentionSec: s.runtime.RetentionSec(), ListedExchanges: listedExchangesByAsset[normalizedAsset],
		}
		if proof, found := s.contractIdentities[normalizedAsset]; found {
			// EventEffect validates this proof against the exact market keys in
			// the historical buckets. Do not reuse a current-window verification
			// boolean here because market symbols/contracts can change over time.
			input.ContractIdentityProof = &proof
		}
		if anchor <= 0 || anchor >= int64(now) ||
			(input.RetentionSec > 0 && anchor-options.ReactionPreWindowSec <= int64(now)-input.RetentionSec) {
			return disableGlobalReactionStudy(marketanalysis.EventEffect(input)), nil
		}
		preStart, postEnd := anchor-options.ReactionPreWindowSec, min(int64(now), anchor+options.ReactionPostWindowSec)
		study, err := s.sharedEventStudy(
			ctx, domain.UnixSecond(preStart), domain.UnixSecond(postEnd), int64(now),
			normalizedAsset, marketScope,
		)
		if err != nil {
			return nil, err
		}
		input.Buckets = domesticEventBuckets(study.buckets)
		input.Coverage = study.domesticCoverage
		input.GlobalCoverage = coverage.Result{Status: "UNAVAILABLE"}
		return disableGlobalReactionStudy(marketanalysis.EventEffect(input)), nil
	}, Microstructure: func(asset string, anchorTSMS *int64, preWindowSec, postWindowSec, asOfMS int64) orderbook.HistoryComparison {
		anchor := int64(0)
		if anchorTSMS != nil {
			anchor = *anchorTSMS
		}
		return s.microstructureHistory.Compare(asset, anchor, int64(preWindowSec), int64(postWindowSec), &asOfMS)
	}}
	reactionNotices := filterMarketReactionSnapshot(result.Notices, int64(generatedAt*1000), result.WindowSec)
	contextualNotices, err := marketanalysis.ContextualizeNoticesWithProviders(*result, reactionNotices, generatedAt, reactionProviders)
	if err != nil {
		return err
	}
	result.Notices = contextualNotices
	reaction, err := marketanalysis.BuildReactionSummaryWithProviders(*result, result.Notices, generatedAt, reactionProviders)
	if err != nil {
		return err
	}
	result.ReactionSummary = disableGlobalReactionSummary(reaction)
	return nil
}

func analysisListedExchanges(markets []domain.MarketPair) map[string]map[domain.Exchange]bool {
	result := make(map[string]map[domain.Exchange]bool)
	for _, market := range markets {
		asset := strings.ToUpper(strings.TrimSpace(market.Base))
		if asset == "" || !analysisScopeMatchesExchange(market.Scope, market.Exchange) {
			continue
		}
		if result[asset] == nil {
			result[asset] = make(map[domain.Exchange]bool)
		}
		result[asset][market.Exchange] = true
	}
	return result
}

func domesticEventBuckets(buckets []aggregate.Bucket) []aggregate.Bucket {
	result := make([]aggregate.Bucket, 0, len(buckets))
	for _, bucket := range buckets {
		scope := domain.Scope(strings.ToLower(strings.TrimSpace(bucket.Key.Scope)))
		exchange := domain.Exchange(strings.ToLower(strings.TrimSpace(bucket.Key.Exchange)))
		if scope == domain.ScopeDomestic && isDomesticAnalysisExchange(exchange) {
			result = append(result, bucket)
		}
	}
	return result
}

// disableGlobalReactionStudy is the production boundary for the retired
// global WebSocket event study. EventEffect keeps its generic legacy contract
// for offline parity tests, but production responses must not serialize an
// unavailable Binance/global zero as an observation.
func disableGlobalReactionStudy(study map[string]any) map[string]any {
	if study == nil {
		return nil
	}
	for _, key := range []string{
		"global_pre_volume_krw", "global_post_volume_krw",
		"global_pre_volume_per_sec_krw", "global_post_volume_per_sec_krw",
		"global_volume_multiplier", "global_reaction_start_ts_ms", "binance_reaction_start_ts_ms",
	} {
		study[key] = nil
	}
	study["global_pre_observed_seconds"] = int64(0)
	study["global_post_observed_seconds"] = int64(0)
	study["contiguous_global_coverage"] = false
	study["global_contract_identity_verified"] = false
	study["global_contract_identity_status"] = "UNAVAILABLE"
	study["global_signal_status"] = "UNAVAILABLE"
	if rows, ok := study["exchange_reactions"].(map[string]any); ok {
		for _, exchange := range domain.GlobalReferenceExchanges {
			row, ok := rows[string(exchange)].(map[string]any)
			if !ok {
				continue
			}
			for _, key := range []string{
				"pre_volume_krw", "post_volume_krw", "pre_volume_per_sec_krw", "post_volume_per_sec_krw",
				"volume_multiplier", "pre_trade_count", "post_trade_count",
			} {
				row[key] = nil
			}
			row["pre_observed_seconds"] = int64(0)
			row["post_observed_seconds"] = int64(0)
			row["data_status"] = "UNAVAILABLE"
		}
	}
	return study
}

func disableGlobalReactionSummary(summary map[string]any) map[string]any {
	if summary == nil {
		return nil
	}
	if context, ok := summary["no_notice_context"].(map[string]any); ok {
		context["global_context_volume_krw"] = nil
		context["binance_volume_krw"] = nil
		context["global_context_status"] = "UNAVAILABLE"
		context["binance_context_status"] = "UNAVAILABLE"
		if context["status"] == "NO_RELEVANT_NOTICE" {
			context["message"] = "관련 국내 공지가 없습니다. 아래 값은 국내 5개 거래소의 현재 시장 자료이며, Binance 공식 24시간 참고값은 공지 반응에 연결하지 않습니다."
		}
	}
	sanitizeEvent := func(event map[string]any) {
		if study, ok := event["reaction_study"].(map[string]any); ok {
			event["reaction_study"] = disableGlobalReactionStudy(study)
		}
		if origin, ok := event["origin"].(map[string]any); ok {
			origin["global_reaction_time_ms"] = nil
			origin["binance_reaction_time_ms"] = nil
			origin["lead_seconds"] = nil
		}
		for _, key := range []string{"global_context", "binance_context"} {
			if context, ok := event[key].(map[string]any); ok {
				for _, valueKey := range []string{
					"before_volume_krw", "after_volume_krw", "volume_multiplier", "volume_change_pct",
					"reaction_start_ts_ms", "price_change_pct", "before_price_krw", "after_price_krw",
					"before_depth_krw", "after_depth_krw", "depth_change_pct", "current_price_krw", "current_depth_krw",
				} {
					context[valueKey] = nil
				}
				context["status"] = "UNAVAILABLE"
				context["included_exchanges"] = []any{}
				context["asset_snapshots"] = []any{}
				context["microstructure_status"] = "DISABLED"
				context["note"] = "해외 실시간 흐름과 공지의 연결 분석은 제공하지 않습니다. 바이낸스 24시간 자료는 참고값과 코인별 가격 차이 표시에만 사용합니다."
			}
		}
	}
	if events, ok := summary["events"].([]any); ok {
		for _, value := range events {
			if event, ok := value.(map[string]any); ok {
				sanitizeEvent(event)
			}
		}
	}
	if top, ok := summary["top_notice"].(map[string]any); ok {
		sanitizeEvent(top)
	}
	return summary
}

func (s *Service) recordNoticeMicrostructure(snapshot notice.Snapshot, assets []marketanalysis.AssetMicrostructure, asOfMS int64) {
	if s.microstructureHistory == nil {
		return
	}
	wanted := marketNoticeAssetsForMicrostructure(snapshot, asOfMS)
	rows := make([]orderbook.MicrostructureRow, 0, len(assets))
	for _, asset := range assets {
		rows = append(rows, orderbook.MicrostructureRow{Asset: asset.Asset, Values: map[string]*float64{
			"bithumb_mid_krw": asset.BithumbMidKRW, "bithumb_spread_bps": asset.BithumbMedianSpreadBPS,
			"bithumb_depth_krw": asset.BithumbDepthKRW, "bithumb_buy_slippage_bps": asset.BithumbBuySlippageBPS,
			"bithumb_sell_slippage_bps": asset.BithumbSellSlippageBPS, "bithumb_buy_fill_pct": asset.BithumbBuyFillPct,
			"bithumb_sell_fill_pct": asset.BithumbSellFillPct, "global_reference_mid_krw": asset.GlobalReferenceMidKRW,
			"global_depth_krw": asset.GlobalDepthKRW,
		}})
	}
	s.microstructureHistory.Record(rows, asOfMS, wanted)
}

func analysisDataQuality(result marketanalysis.Analysis, selected marketanalysis.WindowState, health HealthState, metrics RuntimeMetrics, accounting map[string]any, collectors map[domain.Exchange]collector.ExchangeHealth, registryHealth registry.Snapshot, fxHealth fx.Snapshot, consumerAlive bool, engine aggregate.EngineInfo) map[string]any {
	aggregationActive := aggregationErrorActive(metrics)
	registryErrors, domesticRegistryErrors := make([]string, 0), make([]string, 0)
	activeParseErrors, unobservedChunks := 0, 0
	unparsedEvents, unknownTradeSides := uint64(0), uint64(0)
	for _, exchange := range domain.DomesticExchanges {
		if registryHealth.Exchanges[exchange].Error != "" {
			registryErrors = append(registryErrors, string(exchange))
			domesticRegistryErrors = append(domesticRegistryErrors, string(exchange))
		}
		activeParseErrors += collectors[exchange].KRWActiveTradeParseErrorChunks
		unobservedChunks += collectors[exchange].KRWNeverReceivedTradeChunks
		unparsedEvents += collectors[exchange].KRWUnparsedTradeEvents
		unknownTradeSides += collectors[exchange].KRWUnknownTradeSides
	}
	for _, exchange := range domain.GlobalReferenceExchanges {
		if registryHealth.Exchanges[exchange].Error != "" {
			registryErrors = append(registryErrors, string(exchange))
		}
	}
	sort.Strings(registryErrors)
	sort.Strings(domesticRegistryErrors)
	tradeStatus := "HEALTHY"
	if !health.RealtimeReady || aggregationActive || activeParseErrors > 0 || !consumerAlive || len(domesticRegistryErrors) > 0 || accounting["status"] == "MISMATCH" || metrics.AcceptedObserverFailures > 0 {
		tradeStatus = "DEGRADED"
	}
	fxStatus := "HEALTHY"
	if result.Summary.DomesticUnresolvedFXCount > 0 {
		fxStatus = "PROVISIONAL_FX"
	}
	status := tradeStatus
	if status == "HEALTHY" && fxStatus != "HEALTHY" {
		status = fxStatus
	}
	enabled, disabled, unhealthy := collectorExchangeLists(collectors)
	lossActive := aggregationActive || activeParseErrors > 0
	return map[string]any{
		"status": status, "unhealthy_collectors": unhealthy,
		"domestic_ws_realtime_complete": health.RealtimeReady, "live_market_data_transport": "WEBSOCKET_ONLY",
		"global_ws_realtime_complete":       globalCollectorsReady(collectors),
		"rest_market_data_fallback_enabled": false, "public_ws_is_lossless": false,
		"rest_usage":           []string{"MARKET_REGISTRY", "NOTICE_DISCOVERY_AND_DETAIL"},
		"enabled_ws_exchanges": enabled, "disabled_ws_exchanges": disabled,
		"registry_errors":         registryErrors,
		"unresolved_quote_assets": copyStrings(fxHealth.UnresolvedQuoteAssets),
		"queue_loss_detected":     false, "trade_event_loss_detected": lossActive || unparsedEvents > 0,
		"trade_event_loss_active": lossActive,
		"aggregation_engine":      engine.Name, "aggregation_engine_abi": engine.ABIVersion,
		"native_acceleration":              engine.Native,
		"accepted_trade_observer_failures": metrics.AcceptedObserverFailures,
		"directional_side_quality":         directionalSideQuality(unknownTradeSides),
		"trade_accounting":                 accounting,
		"components": map[string]any{
			"trade_share": map[string]any{
				"status": tradeStatus, "domestic_ws_realtime_complete": health.RealtimeReady,
				"trades_enqueued": metrics.TradesSubmitted, "trades_processed": metrics.TradesProcessed,
				"queue_dropped": uint64(0), "registry_errors": domesticRegistryErrors,
				"unobserved_trade_chunks": unobservedChunks, "unparsed_trade_events": unparsedEvents,
				"active_trade_parse_error_chunks": activeParseErrors,
				"collector_trade_events":          accounting["collector_trade_events"],
				"aggregator_accepted_trades":      metrics.AggregatorAccepted,
				"duplicate_trade_events":          metrics.DuplicateTrades, "invalid_trade_events": metrics.InvalidTrades,
				"trade_processing_errors": uint64(0), "aggregation_loop_errors": metrics.FlushFailures,
				"aggregation_error_active": aggregationActive,
				"last_aggregation_error":   metrics.LastFlushError, "aggregator_consumer_alive": consumerAlive,
				"last_aggregation_error_ts": unixTime(metrics.LastFlushErrorAt),
			},
			"fx": map[string]any{
				"status": fxStatus, "unresolved_quote_assets": copyStrings(fxHealth.UnresolvedQuoteAssets),
				"domestic_unresolved_trade_count": result.Summary.DomesticUnresolvedFXCount,
				"global_unresolved_trade_count":   result.Summary.GlobalUnresolvedFXCount,
				"unresolved_trade_count":          result.Summary.DomesticUnresolvedFXCount, "window_sec": result.WindowSec,
			},
			"history_24h": analysisHistory24hQuality(result.Rolling24h.Rolling24h.Coverage),
		},
	}
}

// Historical transport completeness belongs to the actual rolling observation,
// never to the selected dashboard window or the official ticker projection.
func analysisHistory24hQuality(observed coverage.Result) map[string]any {
	if observed.RequiredSec != marketanalysis.RollingWindowSec {
		observed = coverage.Result{Status: "UNAVAILABLE", RequiredSec: marketanalysis.RollingWindowSec, GapSeconds: marketanalysis.RollingWindowSec}
	}
	return map[string]any{
		"status": observed.Status, "coverage_sec": observed.CoverageSec,
		"required_sec": observed.RequiredSec, "gap_seconds": observed.GapSeconds,
		"window_complete":                  observed.WindowComplete,
		"websocket_coverage_sec":           observed.WebSocketCoverage,
		"official_rest_exact_coverage_sec": observed.HistoricalCoverage,
		"data_source": map[string]any{
			"volume_basis":             "EXACT_TRADE_OBSERVATION",
			"official_24h_ticker_used": false, "official_minute_supplement_used": false,
		},
	}
}

func fxSnapshotMap(snapshot fx.Snapshot) map[string]any {
	return map[string]any{
		"transport": snapshot.Transport, "rest_fallback_enabled": snapshot.RESTFallbackEnabled,
		"volume_conversion_policy":          snapshot.VolumeConversionPolicy,
		"volume_conversion_anchor_exchange": snapshot.VolumeConversionAnchorExchange,
		"volume_conversion_anchor_pair":     snapshot.VolumeConversionAnchorPair,
		"max_age_ms":                        snapshot.MaxAgeMS, "max_hops": snapshot.MaxHops,
		"updated_at_ms": snapshot.UpdatedAtMS, "valuation_edge_count": snapshot.ValuationEdgeCount,
		"executable_edge_count":   snapshot.ExecutableEdgeCount,
		"registered_market_count": snapshot.RegisteredMarketCount,
		"quote_assets":            snapshot.QuoteAssets,
		"unresolved_quote_assets": snapshot.UnresolvedQuoteAssets,
	}
}

func collectorSnapshot(values map[domain.Exchange]collector.ExchangeHealth) map[string]any {
	result := make(map[string]any, len(values))
	for exchange, health := range values {
		result[string(exchange)] = cloneCollectorHealth(health)
	}
	return result
}

func registrySnapshot(snapshot registry.Snapshot) map[string]any {
	cloned := cloneRegistrySnapshot(snapshot)
	return map[string]any{"version": cloned.Version, "updated_at": cloned.UpdatedAt, "market_count": cloned.MarketCount, "asset_count": cloned.AssetCount, "exchanges": cloned.Exchanges}
}

func lowerScope(scope string) string {
	if scope == "ALL_DOMESTIC_CONVERTED" {
		return "all_domestic_converted"
	}
	return "krw"
}

func marketScopeLabel(scope string) string {
	if scope == "ALL_DOMESTIC_CONVERTED" {
		return "전체 국내 현물·KRW 환산"
	}
	return "국내 KRW 현물"
}

func (s *Service) analysisWindows(read analysisReadView, now int64, marketScope string, health HealthState) map[int64]marketanalysis.WindowState {
	return s.analysisWindowsWithSupplements(read, now, marketScope, health, nil)
}

func (s *Service) analysisWindowsWithSupplements(
	read analysisReadView,
	now int64,
	marketScope string,
	health HealthState,
	preparedSupplements map[int64]minuteSupplementApplication,
) map[int64]marketanalysis.WindowState {
	result := make(map[int64]marketanalysis.WindowState, len(aggregate.DefaultWindows))
	wsStatus := "DEGRADED"
	if health.RealtimeReady {
		wsStatus = "LIVE"
	}
	for _, window := range aggregate.DefaultWindows {
		rows := read.interval(now-window, now)
		coverage := read.coverageFor(now-window+1, now)
		filteredRows := filterAnalysisValues(rows, marketScope)
		domestic := aggregateDomesticValues(filteredRows)
		supplementRows, supplementStatus := read.supplementsForWindow(window)
		precisionDifferences := sourcePrecisionDifferenceCount(supplementRows, now-window+1, now)
		if precisionDifferences > 0 {
			supplementStatus = reconcile.StatusSourcePrecisionDifference
		}
		supplement, prepared := preparedSupplements[window]
		if !prepared {
			supplement = applyMinuteSupplements(supplementRows, read.coverageState, now-window+1, now)
		}
		if supplement.totalVolumeKRW > 0 || supplement.replacementCount > 0 {
			domestic.totalVolume += supplement.totalVolumeKRW
			domestic.bithumbVolume += supplement.bithumbVolumeKRW
			for exchange, volume := range supplement.byExchange {
				value := domestic.byExchange[exchange]
				value.volume += volume
				domestic.byExchange[exchange] = value
			}
		}
		share, shareStatus := domesticVolumeShare(domestic.bithumbVolume, domestic.totalVolume, domestic.unresolved, domestic.valid)
		status := coverage.Status
		if status == "CONFIRMED" && health.RegistryFallbackActive {
			status = "PROVISIONAL"
		}
		if status == "CONFIRMED" && (domestic.unresolved > 0 || !domestic.valid) {
			status = "PROVISIONAL"
		}
		exchanges := make(map[domain.Exchange]marketanalysis.WindowExchange, len(domain.DomesticExchanges))
		for _, exchange := range domain.DomesticExchanges {
			exchanges[exchange] = marketanalysis.WindowExchange{}
		}
		for exchange, value := range domestic.byExchange {
			exchanges[exchange] = marketanalysis.WindowExchange{VolumeKRW: value.volume}
		}
		dataSource := map[string]any{
			"bithumb_share_status": shareStatus, "unresolved_fx_count": domestic.unresolved,
			"domestic_input_valid": domestic.valid, "official_minute_supplement_status": supplementStatus,
			"official_minute_supplement_used":                       supplement.totalVolumeKRW > 0 || supplement.replacementCount > 0,
			"official_minute_replacement_used":                      supplement.replacementCount > 0,
			"official_minute_replacement_count":                     supplement.replacementCount,
			"official_minute_replacement_volume_krw":                supplement.replacementVolumeKRW,
			"official_minute_replacement_adjustment_krw":            supplement.replacementAdjustmentKRW,
			"official_candle_supplement_interval_sec":               supplementIntervalForWindow(window),
			"official_minute_supplement_conflict_count":             read.supplementConflictCount,
			"official_minute_supplement_precision_difference_count": precisionDifferences,
			"official_minute_supplement_precision_policy":           "PROVEN_COINONE_SINGLE_TRADE_RAW_DECIMAL_ZERO_ONLY",
			"official_minute_supplement_policy":                     "CLOSED_WHOLE_MINUTE_CURRENT_WS_RESIDUAL",
		}
		if health.RegistryFallbackActive {
			dataSource["registry_fallback_active"] = true
			dataSource["excluded_exchanges"] = append([]string(nil), health.ExcludedExchanges...)
		}
		rateDenominator := int64(0)
		if supplement.totalVolumeKRW > 0 || supplement.replacementCount > 0 {
			// One recovered venue/market does not prove a whole-market recovered
			// second. Rates use the declared full interval; exact coverage stays
			// independently partial until exact trade evidence repairs it.
			rateDenominator = window
			dataSource["volume_basis"] = supplementBasis(supplement.intervalSec)
			dataSource["official_minute_supplement_volume_krw"] = supplement.totalVolumeKRW - supplement.replacementAdjustmentKRW
			dataSource["official_minute_supplement_bithumb_volume_krw"] = supplement.bithumbVolumeKRW - supplement.replacementBithumbAdjustmentKRW
			dataSource["official_minute_supplement_row_count"] = supplement.rowCount
			dataSource["official_minute_supplement_exchange_count"] = len(supplement.exchanges)
			dataSource["official_minute_supplement_exchanges"] = supplement.exchanges
			dataSource["official_minute_supplement_gap_sec"] = supplement.gapSeconds
			dataSource["official_minute_supplement_subminute_estimate"] = supplement.subminuteEstimate
			dataSource["official_minute_supplement_unavailable_exchanges"] = []string{"gopax", "korbit"}
			if supplement.replacementCount > 0 {
				dataSource["trade_detail_recovered"] = false
				dataSource["volume_basis"] = "WEBSOCKET_WITH_OFFICIAL_WHOLE_MINUTE_REPLACEMENT"
				dataSource["official_minute_supplement_policy"] = "SELECT_OFFICIAL_WHOLE_MARKET_MINUTE_PRESERVE_RAW_TRADES"
			}
		}
		result[window] = marketanalysis.WindowState{
			Status: status, CoverageStatus: coverage.Status, WSStatus: wsStatus,
			CoverageSec: coverage.CoverageSec, GapSeconds: coverage.GapSeconds,
			WindowComplete: coverage.WindowComplete, RateDenominatorSec: rateDenominator,
			DataSource:     dataSource,
			TotalVolumeKRW: domestic.totalVolume, BithumbShare: share, BithumbShareStatus: shareStatus,
			UnresolvedFXCount: domestic.unresolved,
			Exchanges:         exchanges,
		}
	}
	return result
}

func filterAnalysisValues(values map[aggregate.VolumeKey]aggregate.Totals, marketScope string) map[aggregate.VolumeKey]aggregate.Totals {
	result := make(map[aggregate.VolumeKey]aggregate.Totals, len(values))
	for key, value := range values {
		scope, exchange := domain.Scope(strings.ToLower(strings.TrimSpace(key.Scope))), domain.Exchange(strings.ToLower(strings.TrimSpace(key.Exchange)))
		if scope == domain.ScopeDomestic {
			if marketScope == "KRW" && !strings.EqualFold(strings.TrimSpace(key.Quote), "KRW") {
				continue
			}
			// Preserve a domestic row with an impossible exchange long enough for
			// the common domestic aggregator and Build boundary to quarantine the
			// headline. Silently dropping it would turn corrupted input into a
			// seemingly complete denominator.
			result[key] = value
			continue
		}
		if !analysisScopeMatchesExchange(scope, exchange) {
			continue
		}
		result[key] = value
	}
	return result
}

func analysisComparisonExchangeSets(
	marketScope string,
	collectors map[domain.Exchange]collector.ExchangeHealth,
) ([]domain.Exchange, []domain.Exchange) {
	included := make([]domain.Exchange, 0, len(domain.DomesticExchanges))
	excluded := make([]domain.Exchange, 0, len(domain.DomesticExchanges))
	for _, exchange := range domain.DomesticExchanges {
		health := collectors[exchange]
		ready := domesticKRWCollectorReady(exchange, health)
		if marketScope != "KRW" {
			ready = health.Enabled && health.RealtimeReady && health.TradeStreamReady &&
				health.ActiveTradeParseErrorChunks == 0 && health.ActiveSubscriptionErrorChunks == 0
		}
		if ready {
			included = append(included, exchange)
		} else {
			excluded = append(excluded, exchange)
		}
	}
	return included, excluded
}

func filterAnalysisRowsByExchanges(rows []marketanalysis.Row, included []domain.Exchange) []marketanalysis.Row {
	allowed := make(map[domain.Exchange]struct{}, len(included))
	for _, exchange := range included {
		allowed[exchange] = struct{}{}
	}
	result := make([]marketanalysis.Row, 0, len(rows))
	for _, row := range rows {
		if row.Scope != domain.ScopeDomestic {
			result = append(result, row)
			continue
		}
		if _, ok := allowed[row.Exchange]; ok {
			result = append(result, row)
		}
	}
	return result
}

func containsExchange(exchanges []domain.Exchange, target domain.Exchange) bool {
	for _, exchange := range exchanges {
		if exchange == target {
			return true
		}
	}
	return false
}

func filterAnalysisMarkets(markets []domain.MarketPair, marketScope string) []domain.MarketPair {
	result := make([]domain.MarketPair, 0, len(markets))
	for _, market := range markets {
		if !analysisScopeMatchesExchange(market.Scope, market.Exchange) {
			continue
		}
		if market.Scope == domain.ScopeDomestic && marketScope == "KRW" && !strings.EqualFold(strings.TrimSpace(market.Quote), "KRW") {
			continue
		}
		result = append(result, market)
	}
	return result
}

func (s *Service) snapshotRollingMonitor(monitor marketanalysis.RollingMonitor) marketanalysis.RollingMonitor {
	monitor = cloneRollingTrackerInput(monitor)
	if s.rollingTracker != nil {
		monitor = s.rollingTracker.Snapshot(monitor)
	}
	return monitor
}

func (s *Service) observeRollingMonitorValue(ctx context.Context, monitor marketanalysis.RollingMonitor) marketanalysis.RollingMonitor {
	return s.observeRollingMonitorGeneration(ctx, monitor, s.deploymentDerivedGeneration.Load())
}

func (s *Service) observeRollingMonitorGeneration(ctx context.Context, monitor marketanalysis.RollingMonitor, generation uint64) marketanalysis.RollingMonitor {
	monitor = cloneRollingTrackerInput(monitor)
	s.deploymentDerivedMu.Lock()
	if !s.deploymentDerivedActive() || generation != s.deploymentDerivedGeneration.Load() {
		s.deploymentDerivedMu.Unlock()
		return monitor
	}
	if s.rollingTracker != nil {
		monitor = s.rollingTracker.Observe(monitor)
	}
	s.deploymentDerivedMu.Unlock()
	s.persistRollingTrackerGeneration(ctx, false, generation)
	return monitor
}

// RollingMovementTracker decorates horizons and data-quality metadata in
// place. Shared analysis reads retain an undecorated base so the background
// writer and browser readers can reuse one expensive rolling calculation
// without mutating each other's immutable snapshot.
func cloneRollingTrackerInput(monitor marketanalysis.RollingMonitor) marketanalysis.RollingMonitor {
	monitor.Horizons = append([]marketanalysis.RollingHorizon(nil), monitor.Horizons...)
	if monitor.DataQuality != nil {
		quality := make(map[string]any, len(monitor.DataQuality))
		for key, value := range monitor.DataQuality {
			quality[key] = value
		}
		monitor.DataQuality = quality
	}
	return monitor
}

func buildRollingMonitor(read analysisReadView, now int64) marketanalysis.RollingMonitor {
	currentValues := read.interval(now-marketanalysis.RollingWindowSec, now)
	currentCoverage := read.coverageFor(now-marketanalysis.RollingWindowSec+1, now)
	input := marketanalysis.RollingInput{
		AsOf: now + 1, Current: analysisRows(filterDomesticKRW(currentValues)),
		CurrentCoverage: currentCoverage,
		Horizons:        make(map[int64]marketanalysis.RollingInterval),
		ClosedMinutes:   make(map[int64]marketanalysis.ClosedMinuteInterval),
	}
	for _, horizon := range rollingFlowHorizons {
		incoming := read.interval(now-horizon, now)
		expiring := read.interval(now-marketanalysis.RollingWindowSec-horizon, now-marketanalysis.RollingWindowSec)
		previousCoverage := read.coverageFor(now-horizon-marketanalysis.RollingWindowSec+1, now-horizon)
		incomingCoverage := read.coverageFor(now-horizon+1, now)
		expiringCoverage := read.coverageFor(now-marketanalysis.RollingWindowSec-horizon+1, now-marketanalysis.RollingWindowSec)
		input.Horizons[horizon] = marketanalysis.RollingInterval{
			Incoming: analysisRows(filterDomesticKRW(incoming)), Expiring: analysisRows(filterDomesticKRW(expiring)),
			PreviousCoverage: previousCoverage, IncomingCoverage: incomingCoverage, ExpiringCoverage: expiringCoverage,
		}
	}
	closedEnd := (now+1)/60*60 - 1
	if closedEnd > 0 {
		for _, duration := range closedMinuteDurations {
			currentStart := closedEnd - duration + 1
			previousStart, previousEnd := currentStart-duration, currentStart-1
			previous := read.interval(previousStart-1, previousEnd)
			current := read.interval(currentStart-1, closedEnd)
			previousCoverage := read.coverageFor(previousStart, previousEnd)
			currentCoverage := read.coverageFor(currentStart, closedEnd)
			input.ClosedMinutes[duration] = marketanalysis.ClosedMinuteInterval{
				Previous:         analysisRows(filterDomesticKRW(previous)),
				Current:          analysisRows(filterDomesticKRW(current)),
				PreviousCoverage: previousCoverage,
				CurrentCoverage:  currentCoverage,
			}
		}
	}
	return marketanalysis.BuildRollingMonitor(input)
}

func filterDomesticKRW(values map[aggregate.VolumeKey]aggregate.Totals) map[aggregate.VolumeKey]aggregate.Totals {
	result := make(map[aggregate.VolumeKey]aggregate.Totals)
	for key, value := range values {
		if domain.Scope(strings.ToLower(strings.TrimSpace(key.Scope))) == domain.ScopeDomestic &&
			isDomesticAnalysisExchange(domain.Exchange(strings.ToLower(strings.TrimSpace(key.Exchange)))) &&
			strings.EqualFold(strings.TrimSpace(key.Quote), "KRW") {
			result[key] = value
		}
	}
	return result
}

func analysisRows(values map[aggregate.VolumeKey]aggregate.Totals) []marketanalysis.Row {
	result := make([]marketanalysis.Row, 0, len(values))
	for key, totals := range values {
		result = append(result, marketanalysis.Row{
			Scope: domain.Scope(strings.ToLower(strings.TrimSpace(key.Scope))), Exchange: domain.Exchange(strings.ToLower(strings.TrimSpace(key.Exchange))),
			Market: strings.ToUpper(strings.TrimSpace(key.Market)), Base: strings.ToUpper(strings.TrimSpace(key.Base)), Quote: strings.ToUpper(strings.TrimSpace(key.Quote)),
			VolumeKRW: totals.KRWVolume, TradeCount: safeInt64(totals.TradeCount),
			UnresolvedFXCount: safeInt64(totals.UnresolvedCount),
			BuyVolumeKRW:      totals.BuyKRWVolume, SellVolumeKRW: totals.SellKRWVolume,
			BaseQuantity: totals.BaseVolume,
		})
	}
	return result
}

func domesticVolumes(values map[aggregate.VolumeKey]aggregate.Totals) (float64, float64) {
	result := aggregateDomesticValues(values)
	return result.totalVolume, result.bithumbVolume
}

type domesticExchangeValues struct {
	volume             float64
	trades, unresolved uint64
}

type domesticValuesSnapshot struct {
	byExchange                 map[domain.Exchange]domesticExchangeValues
	totalVolume, bithumbVolume float64
	trades, unresolved         uint64
	valid                      bool
}

type domesticValueRow struct {
	exchange domain.Exchange
	market   string
	base     string
	quote    string
	totals   aggregate.Totals
}

func aggregateDomesticValues(values map[aggregate.VolumeKey]aggregate.Totals) domesticValuesSnapshot {
	return aggregateDomesticValuesForScope(values, "ALL_DOMESTIC_CONVERTED")
}

// aggregateDomesticValuesForScope filters the quote while gathering domestic
// rows, avoiding an intermediate map without changing their validation or sum order.
func aggregateDomesticValuesForScope(values map[aggregate.VolumeKey]aggregate.Totals, marketScope string) domesticValuesSnapshot {
	result := domesticValuesSnapshot{byExchange: make(map[domain.Exchange]domesticExchangeValues), valid: true}
	rows := make([]domesticValueRow, 0, len(values))
	for key, row := range values {
		scope := domain.Scope(strings.ToLower(strings.TrimSpace(key.Scope)))
		if scope != domain.ScopeDomestic {
			continue
		}
		if marketScope == "KRW" && !strings.EqualFold(strings.TrimSpace(key.Quote), "KRW") {
			continue
		}
		exchange := domain.Exchange(strings.ToLower(strings.TrimSpace(key.Exchange)))
		market := strings.ToUpper(strings.TrimSpace(key.Market))
		base := strings.ToUpper(strings.TrimSpace(key.Base))
		quote := strings.ToUpper(strings.TrimSpace(key.Quote))
		if !isDomesticAnalysisExchange(exchange) || market == "" || base == "" || quote == "" || row.Validate() != nil ||
			row.UnresolvedCount > row.TradeCount || (row.TradeCount == 0 && (row.KRWVolume > 0 || row.BuyKRWVolume > 0 || row.SellKRWVolume > 0 || row.BaseVolume > 0)) {
			result.valid = false
			continue
		}
		rows = append(rows, domesticValueRow{exchange: exchange, market: market, base: base, quote: quote, totals: row})
	}
	sort.Slice(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		if left.exchange != right.exchange {
			return left.exchange < right.exchange
		}
		if left.market != right.market {
			return left.market < right.market
		}
		if left.base != right.base {
			return left.base < right.base
		}
		if left.quote != right.quote {
			return left.quote < right.quote
		}
		if left.totals.KRWVolume != right.totals.KRWVolume {
			return left.totals.KRWVolume < right.totals.KRWVolume
		}
		return left.totals.TradeCount < right.totals.TradeCount
	})
	var previous *domesticValueRow
	for index := range rows {
		row := rows[index]
		if previous != nil && previous.exchange == row.exchange && previous.market == row.market {
			result.valid = false
			continue
		}
		previous = &rows[index]
		exchange := result.byExchange[row.exchange]
		rowValid := addFiniteVolume(&exchange.volume, row.totals.KRWVolume)
		if !addUint64(&exchange.trades, row.totals.TradeCount) {
			rowValid = false
		}
		if !addUint64(&exchange.unresolved, row.totals.UnresolvedCount) {
			rowValid = false
		}
		if !addFiniteVolume(&result.totalVolume, row.totals.KRWVolume) {
			rowValid = false
		}
		if !addUint64(&result.trades, row.totals.TradeCount) {
			rowValid = false
		}
		if !addUint64(&result.unresolved, row.totals.UnresolvedCount) {
			rowValid = false
		}
		if !rowValid {
			result.valid = false
		}
		result.byExchange[row.exchange] = exchange
		if row.exchange == domain.ExchangeBithumb && !addFiniteVolume(&result.bithumbVolume, row.totals.KRWVolume) {
			result.valid = false
		}
	}
	return result
}

func addFiniteVolume(total *float64, value float64) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return false
	}
	if *total > math.MaxFloat64-value {
		*total = math.MaxFloat64
		return false
	}
	*total += value
	return true
}

func addUint64(total *uint64, value uint64) bool {
	if math.MaxUint64-*total < value {
		*total = math.MaxUint64
		return false
	}
	*total += value
	return true
}

func domesticVolumeShare(part, whole float64, unresolved uint64, valid bool) (*float64, string) {
	if !valid {
		return nil, "INVALID_DOMESTIC_INPUT"
	}
	if unresolved > 0 {
		return nil, "UNRESOLVED_FX"
	}
	share := ratioPercent(part, whole)
	if share == nil {
		return nil, "NO_DOMESTIC_VOLUME"
	}
	return share, "AVAILABLE"
}

func analysisScopeMatchesExchange(scope domain.Scope, exchange domain.Exchange) bool {
	if scope == domain.ScopeDomestic {
		return isDomesticAnalysisExchange(exchange)
	}
	// Global exchange data is reference-only and is exposed through the
	// separate official_binance_24h snapshot. It must not enter Bithumb share,
	// change attribution, reaction, or liquidity calculations.
	return false
}

func isDomesticAnalysisExchange(exchange domain.Exchange) bool {
	for _, candidate := range domain.DomesticExchanges {
		if exchange == candidate {
			return true
		}
	}
	return false
}

func ratioPercent(part, whole float64) *float64 {
	if math.IsNaN(part) || math.IsInf(part, 0) || part < 0 || math.IsNaN(whole) || math.IsInf(whole, 0) || whole <= 0 {
		return nil
	}
	value := part / whole * 100
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

func safeInt64(value uint64) int64 {
	if value > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(value)
}
