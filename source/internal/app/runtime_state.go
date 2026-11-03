package app

import (
	"context"
	"time"
)

const runtimeStateCacheTTL = time.Second

func (s *Service) RuntimeState(ctx context.Context) map[string]any {
	now := time.Now()
	s.runtimeStateMu.Lock()
	defer s.runtimeStateMu.Unlock()
	if s.runtimeStateCache != nil && now.Sub(s.runtimeStateCachedAt) >= 0 && now.Sub(s.runtimeStateCachedAt) < runtimeStateCacheTTL {
		return cloneRuntimeState(s.runtimeStateCache)
	}

	metrics := s.runtime.Metrics()
	aggregationActive := aggregationErrorActive(metrics)
	lastAggregationError, lastAggregationErrorAt := metrics.LastFlushError, metrics.LastFlushErrorAt
	queueSize, queueCapacity := s.runtime.QueueState()
	engine := s.runtime.EngineInfo()
	state := map[string]any{
		"running": s.running.Load(), "queue_size": queueSize, "queue_capacity": queueCapacity,
		"queue_backpressure": metrics.TradeBackpressure, "queue_dropped": 0,
		"trades_enqueued": metrics.TradesSubmitted, "trades_processed": metrics.TradesProcessed,
		"historical_trades_enqueued":  metrics.HistoricalTradesSubmitted,
		"historical_trades_processed": metrics.HistoricalTradesProcessed,
		"trade_processing_errors":     0, "aggregation_loop_errors": metrics.FlushFailures,
		"aggregation_error_active": aggregationActive, "last_aggregation_error": lastAggregationError,
		"last_aggregation_error_ts":   unixTime(lastAggregationErrorAt),
		"last_aggregation_success_ts": unixTime(metrics.LastFlush), "last_trade_processing_error": "",
		"last_trade_processing_error_ts": float64(0), "aggregator_consumer_alive": s.runtime.Running(),
		"rest_budget": s.budgets.Snapshot(), "readiness": s.Readiness(time.Now()),
		"database_ready":     s.databaseReady.Load(),
		"storage_mode":       s.storageMode,
		"persistent_storage": s.storageMode == "postgres",
		"llm_enabled":        s.llmEnabled,
		"aggregation_engine": engine.Name, "aggregation_engine_abi": engine.ABIVersion,
		"native_acceleration": engine.Native,
		// Exact non-monotonic trade IDs are a restart/dedupe checkpoint, not an
		// analysis input. Keep four hours in PostgreSQL; analytical buckets and
		// monotonic watermarks remain untouched by this maintenance worker.
		"database_retention_enabled":        s.storageMode == "postgres",
		"recent_trade_id_retention_seconds": int64(recentTradeIDDatabaseRetention / time.Second),
		"retention_prune_batch_size":        recentTradeIDPruneBatchSize,
		"retention_prune_failures":          s.retentionPruneFailures.Load(),
		"retention_rows_pruned":             s.retentionRowsPruned.Load(),
		"last_retention_prune_success_ts":   float64(s.retentionLastSuccess.Load()),
	}
	if s.postgresRepository != nil {
		stats := s.postgresRepository.PoolStats()
		state["postgres_pool"] = map[string]any{
			"max_connections": stats.MaxConnections, "total_connections": stats.TotalConnections,
			"acquired_connections": stats.Acquired, "idle_connections": stats.Idle,
			"acquire_count":       stats.AcquireCount,
			"acquire_duration_ms": float64(stats.AcquireDuration.Nanoseconds()) / 1e6,
		}
	}
	if s.websocketJournal != nil {
		state["websocket_journal"] = s.websocketJournal.Metrics()
	}
	state["websocket_retention"] = s.webSocketRetentionMetrics(now)
	if s.orderBookLatest != nil {
		state["orderbook_latest"] = s.orderBookLatest.Metrics()
	}
	if s.realtimeSummaryRing != nil && s.realtimePeriodSummaryMemo != nil {
		state["realtime_24h_summary"] = map[string]any{
			"ring":         s.realtimeSummaryRing.metrics(),
			"result_cache": s.realtimePeriodSummaryMemo.metrics(),
		}
	}
	if s.websocketFrameStore != nil {
		stats := s.websocketFrameStore.PoolStats()
		state["websocket_journal_postgres_pool"] = map[string]any{
			"max_connections": stats.MaxConnections, "total_connections": stats.TotalConnections,
			"acquired_connections": stats.Acquired, "idle_connections": stats.Idle,
			"acquire_count":       stats.AcquireCount,
			"acquire_duration_ms": float64(stats.AcquireDuration.Nanoseconds()) / 1e6,
		}
	}
	if reconciliation, err := s.ReconciliationLatest(ctx); err == nil {
		state["minute_reconciliation"] = reconciliation
	}
	s.runtimeStateCache = state
	s.runtimeStateCachedAt = now
	return cloneRuntimeState(state)
}

func cloneRuntimeState(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func unixTime(value time.Time) float64 {
	if value.IsZero() {
		return 0
	}
	return float64(value.UnixNano()) / 1e9
}
