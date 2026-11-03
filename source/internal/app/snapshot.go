package app

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"market-share-go/internal/aggregate"
	"market-share-go/contracts/collection/stream"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
	"market-share-go/internal/fx"
	"market-share-go/internal/notice"
	"market-share-go/internal/reconcile"
	"market-share-go/contracts/collection/registry"
)

const (
	snapshotAggregationAvailable   = "AVAILABLE"
	snapshotAggregationQuarantined = "QUARANTINED_NUMERIC_AGGREGATION"
)

func (s *Service) Snapshot(ctx context.Context, includeMarkets bool) (map[string]any, error) {
	snapshotStarted := time.Now()
	now := snapshotStarted
	endSecond := now.Unix() - 1
	asOfSecond := endSecond + 1
	operationalStarted := time.Now()
	operational := s.operationalState(now)
	operationalMS := elapsedMS(operationalStarted)
	health := operational.health
	domesticWSStatus := "DEGRADED"
	if health.RealtimeReady {
		domesticWSStatus = "LIVE"
	}
	domesticWindows := make(map[string]any, len(aggregate.DefaultWindows))
	globalWindows := make(map[string]any, len(aggregate.DefaultWindows))
	intervals := make([]aggregate.IntervalRequest, len(aggregate.DefaultWindows))
	coverageIntervals := make([]coverageInterval, 0, 2*len(aggregate.DefaultWindows))
	for index, window := range aggregate.DefaultWindows {
		intervals[index] = aggregate.IntervalRequest{
			Start: domain.UnixSecond(endSecond - window),
			End:   domain.UnixSecond(endSecond),
		}
		coverageIntervals = append(coverageIntervals, coverageInterval{start: endSecond - window + 1, end: endSecond})
	}
	for _, window := range aggregate.DefaultWindows {
		coverageIntervals = append(coverageIntervals, coverageInterval{
			start:  endSecond - window + 1,
			end:    endSecond,
			global: true,
		})
	}
	aggregationStarted := time.Now()
	read, err := s.runtime.atomicRead(ctx, intervals, coverageIntervals)
	if err != nil {
		return nil, err
	}
	if len(read.intervals) != len(intervals) || len(read.coverage) != len(coverageIntervals) {
		return nil, errors.New("snapshot atomic read returned an incomplete result")
	}
	coverageState := read.coverageState
	collectorHealth := operational.collectors
	globalWSStatus := globalSnapshotWSStatus(collectorHealth)
	coverageAge := int64(0)
	if coverageState.LastObserved > 0 {
		coverageAge = max(int64(0), now.Unix()-coverageState.LastObserved)
	}
	for index, window := range aggregate.DefaultWindows {
		if !read.intervals[index].OK {
			return nil, ErrUnsupportedWindow
		}
		values, windowCoverage := read.intervals[index].Values, read.coverage[index]
		globalCoverage := read.coverage[len(aggregate.DefaultWindows)+index]
		domesticWindow := snapshotWindow(values, domain.ScopeDomestic, windowCoverage, domesticWSStatus, asOfSecond)
		globalWindow := snapshotWindow(values, domain.ScopeGlobal, globalCoverage, globalWSStatus, asOfSecond)
		domesticWindow["coverage_age_sec"], globalWindow["coverage_age_sec"] = coverageAge, nil
		domesticWindows[aggregate.WindowName(window)] = domesticWindow
		globalWindows[aggregate.WindowName(window)] = globalWindow
	}
	aggregationMS := elapsedMS(aggregationStarted)
	metrics := operational.metrics
	registryHealth := operational.registry
	fxStarted := time.Now()
	fxHealth := s.runtime.FXSnapshot(now)
	fxMS := elapsedMS(fxStarted)
	bookStarted := time.Now()
	bookEvidence, err := s.orderBookEvidence(ctx, now, operational.universe)
	if err != nil {
		return nil, err
	}
	bookMS := elapsedMS(bookStarted)
	queueSize, _ := s.runtime.QueueState()
	engine := operational.engine
	domestic24h := domesticWindows["24h"].(map[string]any)
	global24h := globalWindows["24h"].(map[string]any)
	payload := map[string]any{
		"version":             metrics.TradesProcessed + metrics.OrderBooksProcessed,
		"aggregation_version": read.version,
		"generated_at":        float64(now.UnixNano()) / 1e9,
		"aggregation_as_of_s": asOfSecond, "running": operational.running,
		"aggregation_engine": engine.Name, "aggregation_engine_abi": engine.ABIVersion,
		"native_acceleration": engine.Native, "aggregation_engine_error": "",
		"observation_gaps": qualityObservationGaps(coverageState, now.Unix()),
		"domestic":         map[string]any{"windows": domesticWindows},
		"global":           map[string]any{"windows": globalWindows},
		"registry":         cloneRegistrySnapshot(registryHealth),
		"collectors":       collectorSnapshot(collectorHealth),
		"fx":               fxHealth,
		"orderbooks":       bookEvidence.Health,
		"readiness":        cloneReadiness(operational.readiness),
		"data_quality":     snapshotDataQualityWithOrderBooks(health, metrics, queueSize, collectorHealth, registryHealth, fxHealth, domestic24h, global24h, operational.consumerAlive, bookEvidence),
	}
	noticeStarted := time.Now()
	if notices, err := s.NoticeSnapshot(ctx); err == nil {
		payload["notices"] = notices
		quality := payload["data_quality"].(map[string]any)
		quality["notice_source_errors"] = noticeSourceErrors(notices)
	}
	noticeMS := elapsedMS(noticeStarted)
	if s.officialDomestic != nil {
		payload["official_domestic_24h"] = s.officialDomestic.Snapshot()
	}
	payload["official_binance_24h"] = s.officialBinance24hSnapshot(now)
	if s.movementTracker != nil {
		payload["official_share_monitor"] = s.movementTracker.State()
	}
	payload["official_ticker_1m"] = s.officialTickerTrendState()
	payload["realtime_flow"] = s.RealtimeFlowState()
	if includeMarkets {
		payload["markets"] = marketRowsFromValues(read.intervals[len(read.intervals)-1].Values, "", "")
	}
	quality := payload["data_quality"].(map[string]any)
	quality["aggregation_engine"] = engine.Name
	quality["aggregation_engine_abi"] = engine.ABIVersion
	quality["native_acceleration"] = engine.Native
	quality["observation_gaps"] = payload["observation_gaps"]
	quality["global_realtime_policy"] = "POLICY_DISABLED"
	quality["binance_reference_source"] = "OFFICIAL_ROLLING_24H_TICKER_REFERENCE_ONLY"
	quality["snapshot_duration_ms"] = elapsedMS(snapshotStarted)
	quality["snapshot_component_ms"] = map[string]float64{
		"aggregation": aggregationMS, "notices": noticeMS, "collectors": operationalMS,
		"orderbooks": bookMS, "fx": fxMS,
	}
	return payload, nil
}

func globalSnapshotWSStatus(values map[domain.Exchange]collector.ExchangeHealth) string {
	return "POLICY_DISABLED"
}

func elapsedMS(start time.Time) float64 { return float64(time.Since(start).Nanoseconds()) / 1e6 }

func noticeSourceErrors(snapshot notice.Snapshot) []string {
	result := []string{}
	for exchange, source := range snapshot.Sources {
		pending := source.Error == "not_polled" || source.ErrorKind == "NOT_POLLED"
		if source.Error != "" && !pending {
			result = append(result, exchange)
		}
	}
	sort.Strings(result)
	return result
}

func qualityObservationGaps(state coverage.State, now int64) []map[string]any {
	start := max(0, len(state.Gaps)-100)
	result := make([]map[string]any, 0, len(state.Gaps)-start)
	for index := len(state.Gaps) - 1; index >= start; index-- {
		gap := state.Gaps[index]
		gapID, persistencePending := qualityObservationGapID(gap)
		result = append(result, map[string]any{
			"gap_id": gapID, "persistence_pending": persistencePending,
			"start_second": gap.Start, "end_second": gap.End,
			"status": gap.Status, "duration_sec": gap.Duration(now),
			"affected_exchanges": gap.Context.AffectedExchanges, "affected_markets": gap.Context.AffectedMarkets,
			"observed_volume_krw": gap.Context.ObservedVolumeKRW, "observed_total_volume_krw": gap.Context.ObservedTotalVolumeKRW,
			"observed_volume_at_risk_pct": gap.Context.ObservedVolumeAtRisk, "weight_window_sec": gap.Context.WeightWindowSec,
			"reason_codes": gap.Context.ReasonCodes, "created_at": unixTime(gap.CreatedAt), "closed_at": optionalUnixTime(gap.ClosedAt),
		})
	}
	return result
}

func qualityObservationGapID(gap coverage.Gap) (int64, bool) {
	if gap.PersistenceID > 0 {
		return gap.PersistenceID, false
	}
	if gap.PersistenceToken > 0 && gap.PersistenceToken <= uint64(math.MaxInt64) {
		return -int64(gap.PersistenceToken), true
	}
	return 0, true
}

func optionalUnixTime(value *time.Time) *float64 {
	if value == nil {
		return nil
	}
	result := unixTime(*value)
	return &result
}

func snapshotDataQualityWithOrderBooks(
	health HealthState,
	metrics RuntimeMetrics,
	queueSize int,
	collectors map[domain.Exchange]collector.ExchangeHealth,
	registryHealth registry.Snapshot,
	fxHealth fx.Snapshot,
	domestic24h, global24h map[string]any,
	consumerAlive bool,
	books orderBookEvidence,
) map[string]any {
	quality := snapshotDataQuality(health, metrics, queueSize, collectors, registryHealth, fxHealth, domestic24h, global24h, consumerAlive)
	attachOrderBookQuality(quality, books)
	return quality
}

func snapshotDataQuality(
	health HealthState,
	metrics RuntimeMetrics,
	queueSize int,
	collectors map[domain.Exchange]collector.ExchangeHealth,
	registryHealth registry.Snapshot,
	fxHealth fx.Snapshot,
	domestic24h, global24h map[string]any,
	consumerAlive bool,
) map[string]any {
	accounting := tradeAccounting(metrics, queueSize, collectors)
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
	tradeShareStatus := "HEALTHY"
	if !health.RealtimeReady || aggregationActive || activeParseErrors > 0 || !consumerAlive || len(domesticRegistryErrors) > 0 || accounting["status"] == "MISMATCH" {
		tradeShareStatus = "DEGRADED"
	}
	domesticUnresolved := snapshotUint64(domestic24h["unresolved_fx_count"])
	globalUnresolved := snapshotUint64(global24h["unresolved_fx_count"])
	fxStatus := "HEALTHY"
	if domesticUnresolved+globalUnresolved > 0 {
		fxStatus = "PROVISIONAL_FX"
	}
	overallStatus := tradeShareStatus
	if overallStatus == "HEALTHY" && fxStatus != "HEALTHY" {
		overallStatus = fxStatus
	}
	enabled, disabled, unhealthy := collectorExchangeLists(collectors)
	globalReady := globalCollectorsReady(collectors)
	lossActive := aggregationActive || activeParseErrors > 0
	return map[string]any{
		"status":                            overallStatus,
		"domestic_ws_realtime_complete":     health.RealtimeReady,
		"global_ws_realtime_complete":       globalReady,
		"public_ws_is_lossless":             false,
		"live_market_data_transport":        "WEBSOCKET_ONLY",
		"rest_market_data_fallback_enabled": false,
		"rest_usage":                        []string{"MARKET_REGISTRY", "NOTICE_DISCOVERY_AND_DETAIL"},
		"enabled_ws_exchanges":              enabled,
		"disabled_ws_exchanges":             disabled,
		"disabled_orderbook_exchanges":      disabledOrderBookExchanges(collectors),
		"unhealthy_collectors":              unhealthy,
		"registry_errors":                   registryErrors,
		"unresolved_quote_assets":           copyStrings(fxHealth.UnresolvedQuoteAssets),
		"queue_loss_detected":               false,
		"trade_event_loss_detected":         lossActive || unparsedEvents > 0,
		"trade_event_loss_active":           lossActive,
		"accepted_trade_observer_failures":  metrics.AcceptedObserverFailures,
		"directional_side_quality":          directionalSideQuality(unknownTradeSides),
		"aggregation_engine":                "GO",
		"native_acceleration":               true,
		"aggregation_engine_error":          "",
		"observation_gaps":                  []any{},
		"notice_source_errors":              []string{},
		"note":                              "분석 가능 여부는 저장 이력과 현재 실시간 체결을 합친 실제 관측 구간과 공백으로 판단합니다.",
		"volume_filter_applied":             false,
		"market_filter_applied":             false,
		"coin_filter_applied":               false,
		"trade_accounting":                  accounting,
		"components": map[string]any{
			"trade_share": map[string]any{
				"status": tradeShareStatus, "registry_errors": domesticRegistryErrors,
				"domestic_ws_realtime_complete": health.RealtimeReady,
				"unhealthy_collectors":          copyStrings(health.Unhealthy), "queue_dropped": uint64(0),
				"unobserved_trade_chunks":         unobservedChunks,
				"unparsed_trade_events":           unparsedEvents,
				"active_trade_parse_error_chunks": activeParseErrors,
				"collector_trade_events":          accounting["collector_trade_events"],
				"trades_enqueued":                 metrics.TradesSubmitted, "trades_processed": metrics.TradesProcessed,
				"aggregator_accepted_trades": metrics.AggregatorAccepted,
				"duplicate_trade_events":     metrics.DuplicateTrades, "invalid_trade_events": metrics.InvalidTrades,
				"trade_processing_errors": uint64(0), "aggregation_loop_errors": metrics.FlushFailures,
				"aggregation_error_active":  aggregationActive,
				"last_aggregation_error":    metrics.LastFlushError,
				"last_aggregation_error_ts": unixTime(metrics.LastFlushErrorAt),
				"aggregator_consumer_alive": consumerAlive,
			},
			"fx": map[string]any{
				"status": fxStatus, "unresolved_quote_assets": copyStrings(fxHealth.UnresolvedQuoteAssets),
				"domestic_unresolved_trade_count": domesticUnresolved,
				"global_unresolved_trade_count":   globalUnresolved,
				"unresolved_trade_count":          domesticUnresolved + globalUnresolved,
			},
			"history_24h": map[string]any{
				"status": domestic24h["coverage_status"], "coverage_sec": domestic24h["coverage_sec"],
				"coverage_age_sec": snapshotInt64(domestic24h["coverage_age_sec"]),
				"window_complete":  domestic24h["window_complete"], "gap_seconds": domestic24h["gap_seconds"],
				"data_source": domestic24h["data_source"], "required_sec": int64(86_400),
			},
			"global_history_24h": map[string]any{
				"status": global24h["coverage_status"], "coverage_sec": global24h["coverage_sec"],
				"coverage_age_sec": global24h["coverage_age_sec"],
				"window_complete":  global24h["window_complete"], "gap_seconds": global24h["gap_seconds"],
				"data_source": global24h["data_source"], "required_sec": int64(86_400),
			},
		},
	}
}

func directionalSideQuality(unknown uint64) map[string]any {
	status := "HEALTHY"
	if unknown > 0 {
		status = "PROVISIONAL"
	}
	return map[string]any{"status": status, "unknown_trade_sides": unknown}
}

func disabledOrderBookExchanges(values map[domain.Exchange]collector.ExchangeHealth) []string {
	result := []string{}
	for _, exchange := range append(append([]domain.Exchange{}, domain.DomesticExchanges[:]...), domain.GlobalExchanges[:]...) {
		health := values[exchange]
		if health.Enabled && !health.OrderBookEnabled {
			result = append(result, string(exchange))
		}
	}
	return result
}

func snapshotInt64(value any) int64 {
	switch number := value.(type) {
	case int64:
		return number
	case int:
		return int64(number)
	case float64:
		return int64(number)
	}
	return 0
}

func snapshotUint64(value any) uint64 {
	switch number := value.(type) {
	case uint64:
		return number
	case int64:
		if number > 0 {
			return uint64(number)
		}
	case int:
		if number > 0 {
			return uint64(number)
		}
	}
	return 0
}

func collectorExchangeLists(values map[domain.Exchange]collector.ExchangeHealth) (enabled, disabled, unhealthy []string) {
	enabled, disabled, unhealthy = []string{}, []string{}, []string{}
	for _, exchange := range append(append([]domain.Exchange{}, domain.DomesticExchanges[:]...), domain.GlobalExchanges[:]...) {
		health := values[exchange]
		if health.Enabled {
			enabled = append(enabled, string(exchange))
			ready := health.TradeStreamReady
			if isDomesticExchange(exchange) {
				ready = domesticKRWCollectorReady(exchange, health)
			}
			if !ready {
				unhealthy = append(unhealthy, string(exchange))
			}
		} else {
			disabled = append(disabled, string(exchange))
		}
	}
	return enabled, disabled, unhealthy
}

func globalCollectorsReady(values map[domain.Exchange]collector.ExchangeHealth) bool {
	return false
}

func copyStrings(values []string) []string {
	result := make([]string, len(values))
	copy(result, values)
	return result
}

func tradeAccounting(metrics RuntimeMetrics, queueSize int, collectors map[domain.Exchange]collector.ExchangeHealth) map[string]any {
	collectorTrades := uint64(0)
	for _, health := range collectors {
		if health.Enabled {
			collectorTrades += health.Trades
		}
	}
	liveAccepted := saturatingSubtract(metrics.AggregatorAccepted, metrics.HistoricalAccepted)
	liveDuplicates := saturatingSubtract(metrics.DuplicateTrades, metrics.HistoricalDuplicates)
	liveInvalid := saturatingSubtract(metrics.InvalidTrades, metrics.HistoricalInvalid)
	processedError := int64(metrics.TradesProcessed) - int64(liveAccepted) - int64(liveDuplicates) - int64(liveInvalid)
	enqueuedError := int64(metrics.TradesSubmitted) - int64(metrics.TradesProcessed) - int64(queueSize)
	var collectorLag any = int64(collectorTrades) - int64(metrics.TradesSubmitted)
	inputSource, collectorLagStatus := "COLLECTOR_QUEUE", "AVAILABLE"
	if metrics.JournalReplay {
		// Collector and calculator processes restart independently. Their
		// lifetime counters do not measure the durable journal's cursor lag.
		inputSource, collectorLagStatus = "DURABLE_JOURNAL", "NOT_COMPARABLE_PROCESS_LIFETIMES"
		collectorLag = nil
	}
	status := "MISMATCH"
	if processedError == 0 && enqueuedError == 0 {
		status = "BALANCED"
		if lag, comparable := collectorLag.(int64); comparable && lag != 0 {
			status = "COLLECTOR_LAG"
		}
	}
	return map[string]any{
		"status": status, "collector_trade_events": collectorTrades,
		"input_source": inputSource, "collector_lag_status": collectorLagStatus,
		"journal_trades_submitted": metrics.JournalTradesSubmitted,
		"trades_enqueued":          metrics.TradesSubmitted, "trades_processed": metrics.TradesProcessed,
		"aggregator_accepted_trades": liveAccepted,
		"duplicate_trade_events":     liveDuplicates, "invalid_trade_events": liveInvalid,
		"historical_trades_submitted": metrics.HistoricalTradesSubmitted,
		"historical_trades_processed": metrics.HistoricalTradesProcessed,
		"historical_accepted_trades":  metrics.HistoricalAccepted,
		"historical_duplicate_trades": metrics.HistoricalDuplicates,
		"historical_invalid_trades":   metrics.HistoricalInvalid,
		"trade_processing_errors":     uint64(0), "queue_in_flight": queueSize,
		"processed_conservation_error": processedError, "enqueued_conservation_error": enqueuedError,
		"collector_lag": collectorLag,
	}
}

func saturatingSubtract(total, historical uint64) uint64 {
	if historical >= total {
		return 0
	}
	return total - historical
}

func (s *Service) ReconciliationLatest(ctx context.Context) (any, error) {
	if s.postgresRepository == nil {
		return reconcile.Summary{
			Status:      reconcile.StatusCandleUnavailable,
			Exchanges:   map[string]reconcile.ExchangeSummary{},
			MinuteSeals: map[string]reconcile.MinuteSeal{},
		}, nil
	}
	return s.postgresRepository.LatestReconciliationSummary(ctx)
}

func snapshotWindow(values map[aggregate.VolumeKey]aggregate.Totals, scope domain.Scope, windowCoverage coverage.Result, wsStatus string, asOf int64) map[string]any {
	if scope == domain.ScopeGlobal {
		// Historical global buckets may still be present after an upgrade. Never
		// expose them as current reference data: Binance is now REST-only and its
		// rolling 24-hour snapshot has a separate contract.
		values = nil
		windowCoverage = coverage.Result{Status: "UNAVAILABLE"}
		wsStatus = "POLICY_DISABLED"
	}
	totalVolume := 0.0
	totalTrades, unresolved := uint64(0), uint64(0)
	totalIssues := make(map[string]struct{})
	type exchangeAccumulator struct {
		volume     float64
		trades     uint64
		unresolved uint64
		issues     map[string]struct{}
	}
	accumulators := make(map[string]*exchangeAccumulator)
	keys := make([]aggregate.VolumeKey, 0, len(values))
	for key := range values {
		if domain.Scope(key.Scope) != scope ||
			(scope == domain.ScopeGlobal && !strings.EqualFold(key.Exchange, string(domain.ExchangeBinance))) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Exchange != keys[j].Exchange {
			return keys[i].Exchange < keys[j].Exchange
		}
		if keys[i].Market != keys[j].Market {
			return keys[i].Market < keys[j].Market
		}
		if keys[i].Base != keys[j].Base {
			return keys[i].Base < keys[j].Base
		}
		return keys[i].Quote < keys[j].Quote
	})
	for _, key := range keys {
		value := values[key]
		calendarAddFinite(&totalVolume, value.KRWVolume, "total_volume_krw", totalIssues)
		calendarAddUint64(&totalTrades, value.TradeCount, "trade_count", totalIssues)
		calendarAddUint64(&unresolved, value.UnresolvedCount, "unresolved_fx_count", totalIssues)

		row := accumulators[key.Exchange]
		if row == nil {
			row = &exchangeAccumulator{issues: make(map[string]struct{})}
			accumulators[key.Exchange] = row
		}
		calendarAddFinite(&row.volume, value.KRWVolume, "volume_krw", row.issues)
		calendarAddUint64(&row.trades, value.TradeCount, "trade_count", row.issues)
		calendarAddUint64(&row.unresolved, value.UnresolvedCount, "unresolved_fx_count", row.issues)
	}

	aggregationStatus := snapshotAggregationAvailable
	reportedStatus, reportedComplete := windowCoverage.Status, windowCoverage.WindowComplete
	dataSource := snapshotDataSourceForCoverage(asOf, windowCoverage, scope)
	if len(totalIssues) > 0 {
		aggregationStatus = snapshotAggregationQuarantined
		totalVolume, totalTrades, unresolved = 0, 0, 0
		if reportedStatus == "CONFIRMED" {
			reportedStatus = "PROVISIONAL"
		}
		reportedComplete = false
		dataSource["transport_window_complete"] = windowCoverage.WindowComplete
	}
	dataSource["numeric_aggregation_status"] = aggregationStatus
	if len(totalIssues) > 0 {
		dataSource["numeric_aggregation_issues"] = sortedSet(totalIssues)
	}

	exchanges := make(map[string]map[string]any, len(accumulators))
	for exchange, accumulator := range accumulators {
		rowStatus := snapshotAggregationAvailable
		volume, trades, rowUnresolved := accumulator.volume, accumulator.trades, accumulator.unresolved
		if len(accumulator.issues) > 0 {
			rowStatus = snapshotAggregationQuarantined
			volume, trades, rowUnresolved = 0, 0, 0
		}
		row := map[string]any{
			"aggregation_status":    rowStatus,
			"volume_krw":            volume,
			"trade_count":           trades,
			"unresolved_fx_count":   rowUnresolved,
			"share_pct":             nil,
			"trade_count_share_pct": nil,
		}
		if len(accumulator.issues) > 0 {
			row["numeric_aggregation_issues"] = sortedSet(accumulator.issues)
		}
		if aggregationStatus == snapshotAggregationAvailable && rowStatus == snapshotAggregationAvailable {
			row["share_pct"] = ratioPercent(volume, totalVolume)
			row["trade_count_share_pct"] = ratioUint(trades, totalTrades)
		}
		exchanges[exchange] = row
	}
	return map[string]any{
		"status": reportedStatus, "coverage_status": windowCoverage.Status, "ws_status": wsStatus,
		"coverage_sec": windowCoverage.CoverageSec, "gap_seconds": windowCoverage.GapSeconds, "window_complete": reportedComplete,
		"aggregation_status": aggregationStatus,
		"total_volume_krw":   totalVolume, "trade_count": totalTrades, "unresolved_fx_count": unresolved,
		"exchanges": exchanges, "data_source": dataSource,
	}
}

func snapshotDataSourceForCoverage(asOf int64, windowCoverage coverage.Result, scope domain.Scope) map[string]any {
	var liveWatermark any
	var liveAuthoritativeFrom any
	if windowCoverage.WebSocketCoverage > 0 {
		liveWatermark = asOf
		liveAuthoritativeFrom = asOf
	}
	var historicalWatermark any
	if windowCoverage.HistoricalUsed {
		historicalWatermark = windowCoverage.HistoricalEnd
	}
	recoveryMode := "WEBSOCKET_ONLY"
	if windowCoverage.HistoricalUsed {
		recoveryMode = "OFFICIAL_RAW_TRADE_REHYDRATED"
		if windowCoverage.WebSocketCoverage > 0 {
			recoveryMode = "WEBSOCKET_PLUS_OFFICIAL_RAW_TRADE_REHYDRATED"
		}
	}
	result := map[string]any{
		"aggregation_version": "SECOND_BUCKET_V1", "asset_registry_version": "NORMALIZED_BASE_SYMBOL_V1",
		"backfill_loaded_through": historicalWatermark, "backfill_read_mode": "MEMORY_REHYDRATED", "bar_type": "SECOND_AGGREGATE",
		"fx_method_version": "BITHUMB_USDT_KRW_WEBSOCKET_V1",
		"gap_seconds":       windowCoverage.GapSeconds, "historical_authoritative_until": historicalWatermark, "historical_used": windowCoverage.HistoricalUsed,
		"historical_watermark": historicalWatermark, "live_authoritative_from": liveAuthoritativeFrom, "live_used": windowCoverage.WebSocketCoverage > 0, "live_watermark": liveWatermark,
		"micro_gap_tolerance_sec": int64(120), "micro_gap_tolerated": false,
		"official_rest_estimated_coverage_sec": int64(0), "official_rest_exact_coverage_sec": windowCoverage.HistoricalCoverage, "official_rest_used": windowCoverage.HistoricalUsed,
		"official_reconciled": false,
		"overlap_resolved":    true, "recovered_gap_ids": append([]int64{}, windowCoverage.RecoveredGapIDs...), "recovery_mode": recoveryMode,
	}
	if scope == domain.ScopeGlobal {
		result["global_exchange_set_version"] = "none"
		result["global_value_scope"] = "POLICY_DISABLED"
		result["coverage_exchange_set_version"] = "none"
		result["coverage_tracking"] = "GLOBAL_WEBSOCKET_POLICY_DISABLED"
		result["confirmation_semantics"] = "UNAVAILABLE"
	} else {
		result["domestic_exchange_set_version"] = "upbit,bithumb,coinone,korbit,gopax"
	}
	return result
}

// snapshotDataSource retains the legacy scalar helper for recorded contract
// fixtures. Live snapshot construction uses snapshotDataSourceForCoverage so
// source-specific coverage cannot be collapsed into one ambiguous number.
func snapshotDataSource(asOf, coverageSec, gapSeconds int64, scope domain.Scope) map[string]any {
	result := snapshotDataSourceForCoverage(asOf, coverage.Result{
		CoverageSec: coverageSec, WebSocketCoverage: coverageSec, GapSeconds: gapSeconds,
	}, scope)
	result["historical_authoritative_until"] = asOf - 1
	result["live_authoritative_from"] = asOf
	delete(result, "official_reconciled")
	return result
}
