package app

import (
	"context"
	"time"

	"market-share-go/contracts/collection/registry"
	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
	"market-share-go/internal/movement"
	"market-share-go/internal/notice"
	"market-share-go/internal/reconcile"
	"market-share-go/internal/tickertrend"
)

type gatedServiceRepository struct {
	serviceRepository
	gate *deploymentWriterGate
}

func (s *gatedServiceRepository) replaceDeploymentAggregation(ctx context.Context, buckets []aggregate.Bucket, watermarks []aggregate.WatermarkRow, recent []aggregate.RecentIDRow, observation *coverage.State) error {
	buckets = append([]aggregate.Bucket(nil), buckets...)
	watermarks = append([]aggregate.WatermarkRow(nil), watermarks...)
	recent = append([]aggregate.RecentIDRow(nil), recent...)
	observation = cloneCoverageState(observation)
	bytes := int64(len(buckets))*192 + int64(len(watermarks))*96 + int64(len(recent))*96 + coverageStateBytes(observation)
	return s.gate.replaceBufferedAggregation(ctx, bufferedDeploymentWrite{name: "aggregation", bytes: bytes, run: func(writeCtx context.Context) error {
		return s.serviceRepository.FlushWithCoverage(writeCtx, buckets, watermarks, recent, observation)
	}})
}

func (s *gatedServiceRepository) FlushWithCoverage(
	ctx context.Context,
	buckets []aggregate.Bucket,
	watermarks []aggregate.WatermarkRow,
	recent []aggregate.RecentIDRow,
	observation *coverage.State,
) error {
	originalObservation := observation
	buckets = append([]aggregate.Bucket(nil), buckets...)
	watermarks = append([]aggregate.WatermarkRow(nil), watermarks...)
	recent = append([]aggregate.RecentIDRow(nil), recent...)
	observation = cloneCoverageState(observation)
	bytes := int64(len(buckets))*192 + int64(len(watermarks))*96 + int64(len(recent))*96 + coverageStateBytes(observation)
	executed, err := s.gate.dispatch(ctx, bufferedDeploymentWrite{
		name: "aggregation", bytes: bytes,
		run: func(writeCtx context.Context) error {
			return s.serviceRepository.FlushWithCoverage(writeCtx, buckets, watermarks, recent, observation)
		},
	})
	if executed && err == nil && originalObservation != nil && observation != nil {
		// Only the synchronous owner call may receive assigned IDs. A buffered
		// closure must never mutate a caller-owned snapshot after it returned.
		ids := make(map[uint64]int64, len(observation.Gaps))
		for _, gap := range observation.Gaps {
			if gap.PersistenceToken != 0 && gap.PersistenceID > 0 {
				ids[gap.PersistenceToken] = gap.PersistenceID
			}
		}
		for i := range originalObservation.Gaps {
			if id := ids[originalObservation.Gaps[i].PersistenceToken]; id > 0 {
				originalObservation.Gaps[i].PersistenceID = id
			}
		}
	}
	return err
}

func (s *gatedServiceRepository) SaveMarketRegistryObservation(ctx context.Context, snapshot registry.Snapshot, universe domain.MarketUniverse, exchanges []domain.Exchange) error {
	snapshot = cloneRegistrySnapshot(snapshot)
	var err error
	universe, err = domain.NewMarketUniverse(universe.Markets())
	if err != nil {
		return err
	}
	exchanges = append([]domain.Exchange(nil), exchanges...)
	_, err = s.gate.dispatch(ctx, bufferedDeploymentWrite{name: "market_registry", bytes: 4096, run: func(writeCtx context.Context) error {
		return s.serviceRepository.SaveMarketRegistryObservation(writeCtx, snapshot, universe, exchanges)
	}})
	return err
}

func (s *gatedServiceRepository) SaveMovementSample(ctx context.Context, snapshot *movement.Snapshot) error {
	copySnapshot := cloneMovementSnapshot(snapshot)
	_, err := s.gate.dispatch(ctx, bufferedDeploymentWrite{name: "movement_sample", bytes: 16 << 10, run: func(writeCtx context.Context) error {
		return s.serviceRepository.SaveMovementSample(writeCtx, copySnapshot)
	}})
	return err
}

func (s *gatedServiceRepository) SaveMovementEvent(ctx context.Context, event *movement.Event, previous, current *movement.Snapshot) error {
	copyEvent := cloneMovementEvent(event)
	copyPrevious := cloneMovementSnapshot(previous)
	copyCurrent := cloneMovementSnapshot(current)
	_, err := s.gate.dispatch(ctx, bufferedDeploymentWrite{name: "movement_event", bytes: 64 << 10, run: func(writeCtx context.Context) error {
		return s.serviceRepository.SaveMovementEvent(writeCtx, copyEvent, copyPrevious, copyCurrent)
	}})
	return err
}

func (s *gatedServiceRepository) SaveOfficialTickerMinuteSample(ctx context.Context, sample tickertrend.Sample) error {
	copySample := sample
	copySample.Snapshot = *cloneMovementSnapshot(&sample.Snapshot)
	copySample.BithumbClosePricesKRW = cloneFloatMap(sample.BithumbClosePricesKRW)
	_, err := s.gate.dispatch(ctx, bufferedDeploymentWrite{name: "official_ticker_sample", bytes: 32 << 10, run: func(writeCtx context.Context) error {
		return s.serviceRepository.SaveOfficialTickerMinuteSample(writeCtx, copySample)
	}})
	return err
}

func (s *gatedServiceRepository) PruneRecentTradeIDs(ctx context.Context, before domain.UnixSecond, limit int) (int64, error) {
	var pruned int64
	// Maintenance is not handoff evidence: never queue it or acknowledge it
	// before an active writer has actually completed the deletion.
	err := s.gate.dispatchActiveOnly(ctx, bufferedDeploymentWrite{name: "recent_trade_prune", bytes: 64, run: func(writeCtx context.Context) error {
		var writeErr error
		pruned, writeErr = s.serviceRepository.PruneRecentTradeIDs(writeCtx, before, limit)
		return writeErr
	}})
	return pruned, err
}

func (s *gatedServiceRepository) WriteMetadata(ctx context.Context, key, value string) error {
	return s.gate.dispatchActiveOnly(ctx, bufferedDeploymentWrite{name: "metadata", bytes: int64(len(key) + len(value)), run: func(writeCtx context.Context) error {
		return s.serviceRepository.WriteMetadata(writeCtx, key, value)
	}})
}

func (s *gatedServiceRepository) SaveRealtimeFlowStateWithSummary(ctx context.Context, state marketanalysis.RealtimeFlowState) (marketanalysis.RealtimeFlowState, error) {
	state = marketanalysis.CloneRealtimeFlowState(state)
	var stored marketanalysis.RealtimeFlowState
	err := s.gate.dispatchActiveOnly(ctx, bufferedDeploymentWrite{name: "realtime_flow", bytes: 128 << 10, run: func(writeCtx context.Context) error {
		var writeErr error
		stored, writeErr = s.serviceRepository.SaveRealtimeFlowStateWithSummary(writeCtx, state)
		return writeErr
	}})
	return stored, err
}

func (s *gatedServiceRepository) SaveHermesNoticeBatch(ctx context.Context, request notice.HermesIngestRequest) (notice.HermesIngestResult, error) {
	var result notice.HermesIngestResult
	err := s.gate.dispatchActiveOnly(ctx, bufferedDeploymentWrite{name: "hermes_notice_batch", run: func(writeCtx context.Context) error {
		var writeErr error
		result, writeErr = s.serviceRepository.SaveHermesNoticeBatch(writeCtx, request)
		return writeErr
	}})
	return result, err
}

func (s *gatedServiceRepository) RelinkRecentMovementNotices(ctx context.Context) (map[string][]movement.NoticeEvidence, error) {
	var result map[string][]movement.NoticeEvidence
	err := s.gate.dispatchActiveOnly(ctx, bufferedDeploymentWrite{name: "relink_movement_notices", run: func(writeCtx context.Context) error {
		var writeErr error
		result, writeErr = s.serviceRepository.RelinkRecentMovementNotices(writeCtx)
		return writeErr
	}})
	return result, err
}

func (s *gatedServiceRepository) SaveNoticePoll(ctx context.Context, exchange string, items []notice.Item, source notice.Source) error {
	return s.gate.dispatchActiveOnly(ctx, bufferedDeploymentWrite{name: "notice_poll", run: func(writeCtx context.Context) error {
		return s.serviceRepository.SaveNoticePoll(writeCtx, exchange, items, source)
	}})
}

func (s *gatedServiceRepository) SaveNoticePollError(ctx context.Context, exchange, code, message string, at time.Time) error {
	return s.gate.dispatchActiveOnly(ctx, bufferedDeploymentWrite{name: "notice_poll_error", run: func(writeCtx context.Context) error {
		return s.serviceRepository.SaveNoticePollError(writeCtx, exchange, code, message, at)
	}})
}

type gatedWebSocketFrameStore struct {
	next WebSocketFrameBatchStore
	gate *deploymentWriterGate
}

func (s gatedWebSocketFrameStore) AppendWebSocketFrames(ctx context.Context, frames []domain.WebSocketFrame) error {
	frames = cloneWebSocketFrames(frames)
	var bytes int64
	for _, frame := range frames {
		bytes += int64(len(frame.Payload)) + 96
	}
	_, err := s.gate.dispatch(ctx, bufferedDeploymentWrite{name: "websocket_frames", bytes: bytes, run: func(writeCtx context.Context) error {
		return s.next.AppendWebSocketFrames(writeCtx, frames)
	}})
	return err
}

type gatedOrderBookLatestStore struct {
	next orderBookLatestStore
	gate *deploymentWriterGate
}

func (s gatedOrderBookLatestStore) UpsertOrderBookLatest(ctx context.Context, snapshots []domain.OrderBookTopSnapshot) error {
	snapshots = cloneOrderBookSnapshots(snapshots)
	_, err := s.gate.dispatch(ctx, bufferedDeploymentWrite{name: "orderbook_latest", bytes: int64(len(snapshots)) * 256, run: func(writeCtx context.Context) error {
		return s.next.UpsertOrderBookLatest(writeCtx, snapshots)
	}})
	return err
}

type gatedReconciliationStore struct {
	next reconcile.ReconciliationStore
	gate *deploymentWriterGate
}

func (s gatedReconciliationStore) SaveWSMinute(ctx context.Context, value reconcile.WSTradeMinute) (bool, error) {
	value = cloneWSTradeMinute(value)
	var inserted bool
	executed, err := s.gate.dispatch(ctx, bufferedDeploymentWrite{name: "reconcile_ws_minute", bytes: 512, run: func(writeCtx context.Context) error {
		if !s.gate.reconciliationMinuteAllowed(int64(value.MinuteStart)) {
			return nil
		}
		var writeErr error
		inserted, writeErr = s.next.SaveWSMinute(writeCtx, value)
		return writeErr
	}})
	if !executed && err == nil {
		return true, nil
	}
	return inserted, err
}

func (s gatedReconciliationStore) SaveMinuteSeals(ctx context.Context, values []reconcile.MinuteSeal) error {
	values = append([]reconcile.MinuteSeal(nil), values...)
	_, err := s.gate.dispatch(ctx, bufferedDeploymentWrite{name: "reconcile_minute_seals", bytes: int64(len(values)) * 512, run: func(writeCtx context.Context) error {
		allowed := make([]reconcile.MinuteSeal, 0, len(values))
		for _, value := range values {
			if s.gate.reconciliationMinuteAllowed(int64(value.MinuteStart)) {
				allowed = append(allowed, value)
			}
		}
		if len(allowed) == 0 {
			return nil
		}
		return s.next.SaveMinuteSeals(writeCtx, allowed)
	}})
	return err
}

func (s gatedReconciliationStore) GetWSMinute(ctx context.Context, exchange domain.Exchange, market string, minute domain.UnixSecond) (*reconcile.WSTradeMinute, error) {
	return s.next.GetWSMinute(ctx, exchange, market, minute)
}

func (s gatedReconciliationStore) SaveOfficialCandle(ctx context.Context, value reconcile.OfficialMinuteCandle) (bool, error) {
	value = cloneOfficialMinuteCandle(value)
	var inserted bool
	executed, err := s.gate.dispatch(ctx, bufferedDeploymentWrite{name: "reconcile_official_candle", bytes: 512, run: func(writeCtx context.Context) error {
		var writeErr error
		inserted, writeErr = s.next.SaveOfficialCandle(writeCtx, value)
		return writeErr
	}})
	if !executed && err == nil {
		return true, nil
	}
	return inserted, err
}

func (s gatedReconciliationStore) ReconcileMinute(ctx context.Context, exchange domain.Exchange, market string, minute domain.UnixSecond, expected int64, at time.Time) (reconcile.Result, error) {
	var result reconcile.Result
	executed, err := s.gate.dispatch(ctx, bufferedDeploymentWrite{name: "reconcile_minute", bytes: 256, run: func(writeCtx context.Context) error {
		if !s.gate.reconciliationMinuteAllowed(int64(minute)) {
			return nil
		}
		var writeErr error
		result, writeErr = s.next.ReconcileMinute(writeCtx, exchange, market, minute, expected, at)
		return writeErr
	}})
	if !executed && err == nil {
		return reconcile.Result{}, nil
	}
	return result, err
}

func (s gatedReconciliationStore) FinalizeExchangeMinute(ctx context.Context, exchange domain.Exchange, minute domain.UnixSecond, expected int64, at time.Time) (reconcile.Summary, error) {
	var result reconcile.Summary
	executed, err := s.gate.dispatch(ctx, bufferedDeploymentWrite{name: "reconcile_finalize", bytes: 256, run: func(writeCtx context.Context) error {
		if !s.gate.reconciliationMinuteAllowed(int64(minute)) {
			return nil
		}
		var writeErr error
		result, writeErr = s.next.FinalizeExchangeMinute(writeCtx, exchange, minute, expected, at)
		return writeErr
	}})
	if !executed && err == nil {
		return reconcile.Summary{}, nil
	}
	return result, err
}

func (s gatedReconciliationStore) LatestReconciliationSummary(ctx context.Context) (reconcile.Summary, error) {
	return s.next.LatestReconciliationSummary(ctx)
}

func cloneCoverageState(source *coverage.State) *coverage.State {
	if source == nil {
		return nil
	}
	result := *source
	result.Segments = append([]coverage.Segment(nil), source.Segments...)
	result.HistoricalRecoveries = append([]coverage.HistoricalRecovery(nil), source.HistoricalRecoveries...)
	result.Gaps = append([]coverage.Gap(nil), source.Gaps...)
	for index := range result.Gaps {
		gap := &result.Gaps[index]
		gap.Context.AffectedExchanges = append([]string(nil), gap.Context.AffectedExchanges...)
		gap.Context.AffectedMarkets = append([]string(nil), gap.Context.AffectedMarkets...)
		gap.Context.ReasonCodes = append([]string(nil), gap.Context.ReasonCodes...)
		if gap.End != nil {
			value := *gap.End
			gap.End = &value
		}
		if gap.ClosedAt != nil {
			value := *gap.ClosedAt
			gap.ClosedAt = &value
		}
		gap.Context.ObservedVolumeKRW = clonePointer(gap.Context.ObservedVolumeKRW)
		gap.Context.ObservedTotalVolumeKRW = clonePointer(gap.Context.ObservedTotalVolumeKRW)
		gap.Context.ObservedVolumeAtRisk = clonePointer(gap.Context.ObservedVolumeAtRisk)
	}
	return &result
}

func coverageStateBytes(state *coverage.State) int64 {
	if state == nil {
		return 0
	}
	return 128 + int64(len(state.Segments))*16 + int64(len(state.HistoricalRecoveries))*32 + int64(len(state.Gaps))*512
}

func cloneWebSocketFrames(source []domain.WebSocketFrame) []domain.WebSocketFrame {
	result := append([]domain.WebSocketFrame(nil), source...)
	for index := range result {
		result[index].Payload = append([]byte(nil), source[index].Payload...)
	}
	return result
}

func cloneMovementSnapshot(source *movement.Snapshot) *movement.Snapshot {
	if source == nil {
		return nil
	}
	result := *source
	result.Assets = make(map[movement.AssetKey]float64, len(source.Assets))
	for key, value := range source.Assets {
		result.Assets[key] = value
	}
	result.Exchanges = make(map[string]movement.ExchangeSnapshot, len(source.Exchanges))
	for key, value := range source.Exchanges {
		value.Assets = cloneFloatMap(value.Assets)
		value.ExchangeTimestampMS = clonePointer(value.ExchangeTimestampMS)
		result.Exchanges[key] = value
	}
	return &result
}

func cloneMovementEvent(source *movement.Event) *movement.Event {
	if source == nil {
		return nil
	}
	result := *source
	result.ConfirmedAtMS = clonePointer(source.ConfirmedAtMS)
	result.EndedAtMS = clonePointer(source.EndedAtMS)
	result.Drivers = append([]movement.Driver(nil), source.Drivers...)
	result.ExchangeDrivers = append([]movement.ExchangeDriver(nil), source.ExchangeDrivers...)
	result.FlowEvidence = append([]movement.FlowEvidence(nil), source.FlowEvidence...)
	for index := range result.FlowEvidence {
		result.FlowEvidence[index].Payload = append([]byte(nil), source.FlowEvidence[index].Payload...)
	}
	result.Notices = append([]movement.NoticeEvidence(nil), source.Notices...)
	for index := range result.Notices {
		result.Notices[index].MatchedAssets = append([]string(nil), source.Notices[index].MatchedAssets...)
	}
	return &result
}

func cloneOrderBookSnapshots(source []domain.OrderBookTopSnapshot) []domain.OrderBookTopSnapshot {
	result := append([]domain.OrderBookTopSnapshot(nil), source...)
	for index := range result {
		result[index].Sequence = clonePointer(source[index].Sequence)
		result[index].Bids = cloneOrderBookLevels(source[index].Bids)
		result[index].Asks = cloneOrderBookLevels(source[index].Asks)
	}
	return result
}

func cloneOrderBookLevels(source []domain.OrderBookLevel) []domain.OrderBookLevel {
	result := append([]domain.OrderBookLevel(nil), source...)
	for index := range result {
		result[index].OrderCount = clonePointer(source[index].OrderCount)
		result[index].UpdateID = clonePointer(source[index].UpdateID)
	}
	return result
}

func cloneWSTradeMinute(source reconcile.WSTradeMinute) reconcile.WSTradeMinute {
	result := source
	result.Open = clonePointer(source.Open)
	result.High = clonePointer(source.High)
	result.Low = clonePointer(source.Low)
	result.Close = clonePointer(source.Close)
	result.KRWVolume = clonePointer(source.KRWVolume)
	result.BuyKRWVolume = clonePointer(source.BuyKRWVolume)
	result.SellKRWVolume = clonePointer(source.SellKRWVolume)
	result.FirstTradeTS = clonePointer(source.FirstTradeTS)
	result.LastTradeTS = clonePointer(source.LastTradeTS)
	return result
}

func cloneOfficialMinuteCandle(source reconcile.OfficialMinuteCandle) reconcile.OfficialMinuteCandle {
	result := source
	result.Open = clonePointer(source.Open)
	result.High = clonePointer(source.High)
	result.Low = clonePointer(source.Low)
	result.Close = clonePointer(source.Close)
	result.QuoteVolume = clonePointer(source.QuoteVolume)
	return result
}

func clonePointer[T any](source *T) *T {
	if source == nil {
		return nil
	}
	result := *source
	return &result
}

func cloneFloatMap(source map[string]float64) map[string]float64 {
	if source == nil {
		return nil
	}
	result := make(map[string]float64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
