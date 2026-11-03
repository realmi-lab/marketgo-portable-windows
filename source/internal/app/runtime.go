package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
	"market-share-go/internal/fx"
	"market-share-go/internal/orderbook"
	store "market-share-go/internal/store/postgres"
)

var ErrStopped = errors.New("market-share runtime is stopping")

const maxOrderBookBatchSize = 128

type AggregationStore interface {
	FlushWithCoverage(
		context.Context,
		[]aggregate.Bucket,
		[]aggregate.WatermarkRow,
		[]aggregate.RecentIDRow,
		*coverage.State,
	) error
}

type bookRequest struct {
	event         domain.OrderBookEvent
	response      chan bookResponse
	events        []domain.OrderBookEvent
	batchAccepted []bool
	batchDone     chan struct{}
}

type queuedTrade struct {
	event      domain.TradeEvent
	sequence   uint64
	historical bool
}

type bookResponse struct {
	accepted bool
	err      error
}

type calculatedBookSecondKey struct {
	exchange domain.Exchange
	market   string
}

type snapshotRequest struct {
	window   int64
	response chan snapshotResponse
}

type snapshotResponse struct {
	values map[aggregate.VolumeKey]aggregate.Totals
	ok     bool
}

type comparisonRequest struct {
	window   int64
	now      domain.UnixSecond
	response chan comparisonResponse
}

type comparisonResponse struct {
	current  map[aggregate.VolumeKey]aggregate.Totals
	previous map[aggregate.VolumeKey]aggregate.Totals
	ok       bool
}

type intervalRequest struct {
	start    domain.UnixSecond
	end      domain.UnixSecond
	response chan intervalResponse
}

type intervalResponse struct {
	values map[aggregate.VolumeKey]aggregate.Totals
	ok     bool
}

type intervalBucketsRequest struct {
	start    domain.UnixSecond
	end      domain.UnixSecond
	response chan intervalBucketsResponse
}

type intervalBucketsResponse struct {
	values []aggregate.Bucket
	ok     bool
}

type coverageInterval struct {
	start  int64
	end    int64
	global bool
}

type atomicReadRequest struct {
	ctx                           context.Context
	includeCoverageState          bool
	includeShareSeriesCorrections bool
	shareSeriesSince              uint64
	intervals                     []aggregate.IntervalRequest
	bucketIntervals               []bucketIntervalRequest
	coverage                      []coverageInterval
	tradeTarget                   uint64
	coverageTarget                uint64
	response                      chan atomicReadResult
}

type bucketIntervalRequest struct {
	Start       domain.UnixSecond
	End         domain.UnixSecond
	Asset       string
	MarketScope string
}

type atomicReadResult struct {
	err                    error
	version                uint64
	intervals              []aggregate.IntervalSnapshotResult
	buckets                [][]aggregate.Bucket
	coverage               []coverage.Result
	coverageState          coverage.State
	journalHealthSecond    int64
	shareSeriesCorrections shareSeriesCorrectionSnapshot
	caughtUp               bool
}

type orderBookRowsRequest struct {
	markets  []domain.MarketPair
	notional float64
	quiet    map[string]bool
	response chan []orderbook.Row
}

type orderBookHealthRequest struct {
	markets  []domain.MarketPair
	now      time.Time
	response chan orderbook.Health
}

type orderBookSnapshotRequest struct {
	markets  []domain.MarketPair
	notional float64
	quiet    map[string]bool
	now      time.Time
	response chan OrderBookSnapshot
}

// OrderBookSnapshot is one immutable owner-turn view of order-book rows and
// health. Version and AsOf let callers prove that both projections describe
// the same book state instead of joining two independently timed reads.
type OrderBookSnapshot struct {
	Rows    []orderbook.Row
	Health  orderbook.Health
	Version int64
	AsOf    time.Time
}

type orderBookDetailRequest struct {
	pair     domain.MarketPair
	notional float64
	response chan orderbook.Detail
}

type stopRequest struct {
	ctx      context.Context
	response chan error
}

type coverageRequest struct {
	start          int64
	end            int64
	global         bool
	coverageTarget uint64
	response       chan coverage.Result
}

type coverageStateRequest struct {
	coverageTarget uint64
	response       chan coverage.State
}

type coverageInvalidationRequest struct {
	start    int64
	detected int64
	context  coverage.GapContext
	sequence uint64
	global   bool
	response chan struct{}
}

type ObservationState struct {
	Complete    bool
	InvalidFrom int64
	Context     coverage.GapContext
}

type RuntimeMetrics struct {
	IngestionMinuteRecoveryEvents uint64 `json:"ingestion_minute_recovery_events"`
	TradesSubmitted               uint64
	TradesProcessed               uint64
	JournalTradesSubmitted        uint64
	JournalReplay                 bool
	HistoricalTradesSubmitted     uint64
	HistoricalTradesProcessed     uint64
	HistoricalAccepted            uint64
	HistoricalDuplicates          uint64
	HistoricalInvalid             uint64
	AggregatorAccepted            uint64
	DuplicateTrades               uint64
	InvalidTrades                 uint64
	AcceptedObserverFailures      uint64
	TradeBackpressure             uint64
	TradeQueueP99UpperBoundMS     int64
	TradeQueueMaxMS               int64
	OrderBooksProcessed           uint64
	OrderBookSnapshotsQueued      uint64
	FlushFailures                 uint64
	LastFlush                     time.Time
	LastFlushError                string
	LastFlushErrorAt              time.Time
}

type Runtime struct {
	ingestion                   *ingestionRuntimeState
	shareSeriesCorrections      shareSeriesCorrectionLog
	aggregator                  *aggregate.Aggregator
	engineInfo                  aggregate.EngineInfo
	retentionSec                int64
	deploymentCapture           *runtimeDeploymentCapture
	deploymentCaptureGeneration uint64
	handoffRequests             chan handoffOwnerRequest
	books                       *orderbook.Store
	oracle                      *fx.Oracle
	coverage                    *coverage.Tracker
	globalCoverage              *coverage.Tracker
	coverageDirty               bool
	store                       AggregationStore
	logger                      *slog.Logger
	observerMu                  sync.RWMutex
	onAccepted                  func(aggregate.AcceptedTrade) error
	onBookSnapshot              func(domain.OrderBookTopSnapshot)
	bookSnapshotSecond          map[calculatedBookSecondKey]int64

	tradeQueue        chan queuedTrade
	bookQueue         chan bookRequest
	snapshots         chan snapshotRequest
	comparisons       chan comparisonRequest
	intervals         chan intervalRequest
	intervalBuckets   chan intervalBucketsRequest
	atomicReads       chan atomicReadRequest
	orderBookRows     chan orderBookRowsRequest
	orderBookHealth   chan orderBookHealthRequest
	orderBookSnapshot chan orderBookSnapshotRequest
	orderBookDetails  chan orderBookDetailRequest
	coverageRequests  chan coverageRequest
	coverageStates    chan coverageStateRequest
	coverageInvalid   chan coverageInvalidationRequest
	stop              chan stopRequest
	done              chan struct{}

	acceptMu                  sync.RWMutex
	tradeSubmitMu             runtimeSubmissionGate
	coverageSubmitMu          runtimeSubmissionGate
	accepting                 bool
	nextTradeSequence         uint64
	admittedTradeSequence     atomic.Uint64
	processedTradeSequence    uint64
	nextCoverageSequence      uint64
	admittedCoverageSequence  atomic.Uint64
	processedCoverageSequence atomic.Uint64
	started                   atomic.Bool
	metricsMu                 sync.RWMutex
	metrics                   RuntimeMetrics
	observation               atomic.Pointer[ObservationState]
	globalObservation         atomic.Pointer[ObservationState]
	tradeLatency              [14]uint64
	storeTimeout              time.Duration
}

const (
	aggregationStoreTimeout  = 5 * time.Second
	coverageInvalidQueueSize = 4096
	coverageDrainPerTurn     = 256
)

var tradeQueueLatencyBoundsMS = [...]int64{1, 2, 5, 10, 20, 50, 100, 250, 500, 1_000, 2_000, 5_000, 10_000}

// KRW is the dominant domestic quote and its conversion is always the
// identity. Reusing one immutable value avoids constructing an FX resolution
// and an escaping float pointer for every KRW trade.
var krwVolumeRate = 1.0

func (r *Runtime) Running() bool {
	if r == nil || !r.started.Load() {
		return false
	}
	select {
	case <-r.done:
		return false
	default:
		return true
	}
}

func (r *Runtime) FXSnapshot(now time.Time) fx.Snapshot {
	if now.IsZero() {
		now = time.Now()
	}
	return r.oracle.Snapshot(domain.UnixMilli(now.UnixMilli()))
}

func NewRuntime(
	aggregator *aggregate.Aggregator,
	books *orderbook.Store,
	oracle *fx.Oracle,
	store AggregationStore,
	queueSize int,
	logger *slog.Logger,
) (*Runtime, error) {
	if aggregator == nil || books == nil || oracle == nil || store == nil {
		return nil, errors.New("aggregator, order-book store, FX oracle, and persistence store are required")
	}
	if queueSize < 1 {
		return nil, errors.New("queue size must be positive")
	}
	if logger == nil {
		logger = slog.Default()
	}
	runtime := &Runtime{
		aggregator: aggregator, books: books, oracle: oracle, store: store, logger: logger,
		engineInfo: aggregator.EngineInfo(), retentionSec: aggregator.RetentionSec(),
		handoffRequests:    make(chan handoffOwnerRequest),
		bookSnapshotSecond: make(map[calculatedBookSecondKey]int64),
		tradeQueue:         make(chan queuedTrade, queueSize),
		bookQueue:          make(chan bookRequest, min(queueSize, 4096)),
		snapshots:          make(chan snapshotRequest),
		comparisons:        make(chan comparisonRequest),
		intervals:          make(chan intervalRequest),
		intervalBuckets:    make(chan intervalBucketsRequest),
		atomicReads:        make(chan atomicReadRequest),
		orderBookRows:      make(chan orderBookRowsRequest),
		orderBookHealth:    make(chan orderBookHealthRequest),
		orderBookSnapshot:  make(chan orderBookSnapshotRequest),
		orderBookDetails:   make(chan orderBookDetailRequest),
		coverageRequests:   make(chan coverageRequest),
		coverageStates:     make(chan coverageStateRequest),
		coverageInvalid:    make(chan coverageInvalidationRequest, coverageInvalidQueueSize),
		stop:               make(chan stopRequest, 1),
		done:               make(chan struct{}),
		accepting:          true,
		storeTimeout:       aggregationStoreTimeout,
	}
	runtime.coverage = coverage.New(time.Now().Unix(), false)
	runtime.globalCoverage = coverage.New(time.Now().Unix(), false)
	runtime.coverageDirty = true
	runtime.observation.Store(&ObservationState{})
	runtime.globalObservation.Store(&ObservationState{})
	return runtime, nil
}

func (r *Runtime) Hydrate(state store.State, now domain.UnixSecond) error {
	if r.started.Load() {
		return errors.New("cannot hydrate a running runtime")
	}
	if r.ingestion != nil && r.ingestion.cursor != 0 && !state.CoverageLoaded {
		return errors.New("committed ingestion cursor has no coverage checkpoint")
	}
	r.aggregator.Advance(now)
	for _, row := range state.Buckets {
		if err := r.aggregator.RestoreBucket(row, now); err != nil {
			return fmt.Errorf("restore aggregation bucket: %w", err)
		}
	}
	for _, row := range state.Watermarks {
		if err := r.aggregator.RestoreWatermark(row); err != nil {
			return fmt.Errorf("restore aggregation watermark: %w", err)
		}
	}
	for _, row := range state.RecentIDs {
		if err := r.aggregator.RestoreRecentID(row); err != nil {
			return fmt.Errorf("restore aggregation recent trade ID: %w", err)
		}
	}
	if state.CoverageLoaded {
		if r.ingestion != nil {
			// Calculator downtime is not collector downtime. Continue the exact
			// durable observation timeline and replay subsequent health/loss
			// inputs before confirming any later second.
			checkpoint := state.Coverage
			if checkpoint.InvalidThrough == 0 {
				for _, gap := range checkpoint.Gaps {
					if gap.Status == "OPEN" && gap.End == nil {
						// The relational checkpoint alone cannot identify when a
						// retroactive loss was detected. Retain the open loss
						// conservatively until the owner checkpoint restores that
						// boundary, or a fresh post-start observation arrives.
						checkpoint.InvalidThrough = int64(now)
						break
					}
				}
			}
			r.coverage = coverage.ContinueCheckpoint(checkpoint)
		} else {
			r.coverage = coverage.Restore(state.Coverage, int64(now), false)
		}
		r.coverageDirty = true
	} else if r.ingestion != nil {
		// A first calculator may start after the collector already journaled
		// healthy observations. With no committed checkpoint, begin empty;
		// the replayed collector timestamps establish the observation timeline.
		// Relational coverage requires a positive metadata start even for a
		// registry-only checkpoint before the first health. This anchor records
		// no observation: lastObserved/liveStart/segments/gaps remain empty.
		r.coverage = coverage.ContinueCheckpoint(coverage.State{CoverageStart: int64(now)})
		r.coverageDirty = true
	}
	return nil
}

func (r *Runtime) Start() error {
	if !r.started.CompareAndSwap(false, true) {
		return errors.New("runtime already started")
	}
	go r.run()
	return nil
}

func (r *Runtime) SubmitTrade(ctx context.Context, event domain.TradeEvent) error {
	return r.submitTrade(ctx, event, false)
}

// SubmitHistoricalTrade uses the same owner queue, dedupe, aggregation and
// persistence path as live trades, but keeps replay work out of live collector
// conservation and queue-latency metrics. Historical events also cannot update
// the live FX graph.
func (r *Runtime) SubmitHistoricalTrade(ctx context.Context, event domain.TradeEvent) error {
	return r.submitTrade(ctx, event, true)
}

func (r *Runtime) submitTrade(ctx context.Context, event domain.TradeEvent, historical bool) error {
	r.acceptMu.RLock()
	defer r.acceptMu.RUnlock()
	if !r.accepting || !r.started.Load() {
		return ErrStopped
	}
	if err := r.tradeSubmitMu.lockContext(ctx, r.done); err != nil {
		return err
	}
	defer r.tradeSubmitMu.Unlock()
	sequence := r.nextTradeSequence + 1
	if len(r.tradeQueue) == cap(r.tradeQueue) {
		r.updateMetrics(func(metrics *RuntimeMetrics) { metrics.TradeBackpressure++ })
	}
	select {
	case r.tradeQueue <- queuedTrade{event: event, sequence: sequence, historical: historical}:
		r.nextTradeSequence = sequence
		r.admittedTradeSequence.Store(sequence)
		r.updateMetrics(func(metrics *RuntimeMetrics) {
			if historical {
				metrics.HistoricalTradesSubmitted++
			} else {
				metrics.TradesSubmitted++
			}
		})
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return ErrStopped
	}
}

// SetAcceptedTradeObserver fans out only trades accepted by the canonical
// aggregator. Reconciliation therefore uses the same dedupe, timestamp, and
// FX decision as the headline share instead of independently re-accepting the
// raw collector event.
func (r *Runtime) SetAcceptedTradeObserver(observer func(aggregate.AcceptedTrade) error) {
	r.observerMu.Lock()
	r.onAccepted = observer
	r.observerMu.Unlock()
}

// SetOrderBookSnapshotObserver installs a non-blocking handoff for calculated
// top-ten snapshots. Runtime invokes it at most once per market and received
// second, after the event has been applied to the complete owner-held book.
func (r *Runtime) SetOrderBookSnapshotObserver(observer func(domain.OrderBookTopSnapshot)) {
	r.observerMu.Lock()
	r.onBookSnapshot = observer
	r.observerMu.Unlock()
}

func (r *Runtime) SubmitOrderBook(ctx context.Context, event domain.OrderBookEvent) (bool, error) {
	r.acceptMu.RLock()
	defer r.acceptMu.RUnlock()
	if !r.accepting || !r.started.Load() {
		return false, ErrStopped
	}
	request := bookRequest{event: event, response: make(chan bookResponse, 1)}
	select {
	case r.bookQueue <- request:
	case <-ctx.Done():
		return false, ctx.Err()
	case <-r.done:
		return false, ErrStopped
	}
	select {
	case response := <-request.response:
		return response.accepted, response.err
	case <-ctx.Done():
		return false, ctx.Err()
	case <-r.done:
		return false, ErrStopped
	}
}

// SubmitOrderBooks admits one bounded FIFO batch in a single owner turn. The
// caller chunks larger frames so reads and persistence turns remain schedulable.
func (r *Runtime) SubmitOrderBooks(ctx context.Context, events []domain.OrderBookEvent) ([]bool, error) {
	if len(events) == 0 {
		return []bool{}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(events) > maxOrderBookBatchSize {
		return nil, fmt.Errorf("order-book batch exceeds %d events", maxOrderBookBatchSize)
	}
	r.acceptMu.RLock()
	defer r.acceptMu.RUnlock()
	if !r.accepting || !r.started.Load() {
		return nil, ErrStopped
	}
	request := bookRequest{events: events, batchAccepted: make([]bool, len(events)), batchDone: make(chan struct{}, 1)}
	select {
	case r.bookQueue <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.done:
		return nil, ErrStopped
	}
	select {
	case <-request.batchDone:
		return request.batchAccepted, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.done:
		return nil, ErrStopped
	}
}

func (r *Runtime) Snapshot(ctx context.Context, window int64) (map[aggregate.VolumeKey]aggregate.Totals, bool, error) {
	if !r.started.Load() {
		return nil, false, ErrStopped
	}
	request := snapshotRequest{window: window, response: make(chan snapshotResponse, 1)}
	select {
	case r.snapshots <- request:
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case <-r.done:
		return nil, false, ErrStopped
	}
	select {
	case response := <-request.response:
		return response.values, response.ok, nil
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case <-r.done:
		return nil, false, ErrStopped
	}
}

// ComparisonSnapshot returns the current window and its immediately preceding
// peer from one owner-goroutine read, so callers cannot observe mismatched
// versions while trades are being ingested.
func (r *Runtime) ComparisonSnapshot(
	ctx context.Context,
	window int64,
) (map[aggregate.VolumeKey]aggregate.Totals, map[aggregate.VolumeKey]aggregate.Totals, bool, error) {
	return r.ComparisonSnapshotAt(ctx, window, domain.UnixSecond(time.Now().Unix()))
}

func (r *Runtime) ComparisonSnapshotAt(
	ctx context.Context,
	window int64,
	now domain.UnixSecond,
) (map[aggregate.VolumeKey]aggregate.Totals, map[aggregate.VolumeKey]aggregate.Totals, bool, error) {
	if !r.started.Load() {
		return nil, nil, false, ErrStopped
	}
	request := comparisonRequest{window: window, now: now, response: make(chan comparisonResponse, 1)}
	select {
	case r.comparisons <- request:
	case <-ctx.Done():
		return nil, nil, false, ctx.Err()
	case <-r.done:
		return nil, nil, false, ErrStopped
	}
	select {
	case response := <-request.response:
		return response.current, response.previous, response.ok, nil
	case <-ctx.Done():
		return nil, nil, false, ctx.Err()
	case <-r.done:
		return nil, nil, false, ErrStopped
	}
}

func (r *Runtime) IntervalSnapshot(
	ctx context.Context,
	start domain.UnixSecond,
	end domain.UnixSecond,
) (map[aggregate.VolumeKey]aggregate.Totals, error) {
	if !r.started.Load() {
		return nil, ErrStopped
	}
	if end < start {
		return nil, errors.New("interval end must not precede start")
	}
	request := intervalRequest{start: start, end: end, response: make(chan intervalResponse, 1)}
	select {
	case r.intervals <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.done:
		return nil, ErrStopped
	}
	select {
	case response := <-request.response:
		if !response.ok {
			return nil, errors.New("invalid aggregation interval")
		}
		return response.values, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.done:
		return nil, ErrStopped
	}
}

func (r *Runtime) IntervalBuckets(ctx context.Context, start, end domain.UnixSecond) ([]aggregate.Bucket, error) {
	if !r.started.Load() {
		return nil, ErrStopped
	}
	if end < start {
		return nil, errors.New("interval end must not precede start")
	}
	request := intervalBucketsRequest{start: start, end: end, response: make(chan intervalBucketsResponse, 1)}
	select {
	case r.intervalBuckets <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.done:
		return nil, ErrStopped
	}
	select {
	case response := <-request.response:
		if !response.ok {
			return nil, errors.New("invalid aggregation interval")
		}
		return response.values, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.done:
		return nil, ErrStopped
	}
}

// atomicRead returns all aggregation and coverage intervals from one owner
// goroutine turn. No trade can be ingested between the individual interval
// reads, and the version identifies the exact accepted-trade state.
func (r *Runtime) atomicRead(
	ctx context.Context,
	intervals []aggregate.IntervalRequest,
	coverageIntervals []coverageInterval,
) (atomicReadResult, error) {
	return r.atomicReadAll(ctx, intervals, nil, coverageIntervals)
}

func (r *Runtime) atomicReadAll(
	ctx context.Context,
	intervals []aggregate.IntervalRequest,
	bucketIntervals []bucketIntervalRequest,
	coverageIntervals []coverageInterval,
) (atomicReadResult, error) {
	return r.atomicReadSelected(ctx, intervals, bucketIntervals, coverageIntervals, true)
}

// atomicReadIntervals retains the same owner and trade/coverage barriers but
// omits the full historical coverage checkpoint when only interval results and
// the journal frontier are consumed.
func (r *Runtime) atomicReadIntervals(ctx context.Context, intervals []aggregate.IntervalRequest, coverageIntervals []coverageInterval) (atomicReadResult, error) {
	return r.atomicReadSelected(ctx, intervals, nil, coverageIntervals, false)
}

func (r *Runtime) atomicReadSelected(
	ctx context.Context,
	intervals []aggregate.IntervalRequest,
	bucketIntervals []bucketIntervalRequest,
	coverageIntervals []coverageInterval,
	includeCoverageState bool,
) (atomicReadResult, error) {
	return r.atomicReadSelectedWithCorrections(ctx, intervals, bucketIntervals, coverageIntervals, includeCoverageState, nil)
}

func (r *Runtime) atomicReadSelectedWithCorrections(
	ctx context.Context,
	intervals []aggregate.IntervalRequest,
	bucketIntervals []bucketIntervalRequest,
	coverageIntervals []coverageInterval,
	includeCoverageState bool,
	shareSeriesSince *uint64,
) (atomicReadResult, error) {
	if err := ctx.Err(); err != nil {
		return atomicReadResult{}, err
	}
	if !r.started.Load() {
		return atomicReadResult{}, ErrStopped
	}
	for _, interval := range intervals {
		if interval.End < interval.Start {
			return atomicReadResult{}, errors.New("interval end must not precede start")
		}
	}
	for _, interval := range bucketIntervals {
		if interval.End < interval.Start {
			return atomicReadResult{}, errors.New("bucket interval end must not precede start")
		}
	}
	for _, interval := range coverageIntervals {
		if interval.end < interval.start {
			return atomicReadResult{}, errors.New("coverage end must not precede start")
		}
	}
	request := atomicReadRequest{
		ctx:                  ctx,
		includeCoverageState: includeCoverageState,
		intervals:            append([]aggregate.IntervalRequest(nil), intervals...),
		bucketIntervals:      append([]bucketIntervalRequest(nil), bucketIntervals...),
		coverage:             append([]coverageInterval(nil), coverageIntervals...),
		tradeTarget:          r.tradeBarrierTarget(),
		coverageTarget:       r.coverageBarrierTarget(),
		response:             make(chan atomicReadResult, 1),
	}
	if shareSeriesSince != nil {
		request.includeShareSeriesCorrections = true
		request.shareSeriesSince = *shareSeriesSince
	}
	select {
	case r.atomicReads <- request:
	case <-ctx.Done():
		return atomicReadResult{}, ctx.Err()
	case <-r.done:
		return atomicReadResult{}, ErrStopped
	}
	select {
	case result := <-request.response:
		if result.err != nil {
			return atomicReadResult{}, result.err
		}
		if !result.caughtUp {
			return atomicReadResult{}, errors.New("aggregation trade barrier is unavailable while persistence is blocked")
		}
		return result, nil
	case <-ctx.Done():
		return atomicReadResult{}, ctx.Err()
	case <-r.done:
		return atomicReadResult{}, ErrStopped
	}
}

type eventStudyRead struct {
	version          uint64
	buckets          []aggregate.Bucket
	domesticCoverage coverage.Result
	globalCoverage   coverage.Result
}

// eventStudy reads notice-aligned buckets and both transport coverage tracks
// after one exact queue barrier and from one owner turn.
func (r *Runtime) eventStudy(
	ctx context.Context,
	startInclusive domain.UnixSecond,
	endExclusive domain.UnixSecond,
	asset string,
	marketScope string,
) (eventStudyRead, error) {
	if endExclusive <= startInclusive {
		return eventStudyRead{}, errors.New("event study interval must be non-empty")
	}
	result, err := r.atomicReadAll(ctx, nil, []bucketIntervalRequest{{
		Start: startInclusive - 1,
		End:   endExclusive - 1,
		Asset: asset, MarketScope: marketScope,
	}}, []coverageInterval{
		{start: int64(startInclusive), end: int64(endExclusive - 1)},
		{start: int64(startInclusive), end: int64(endExclusive - 1), global: true},
	})
	if err != nil {
		return eventStudyRead{}, err
	}
	if len(result.buckets) != 1 || len(result.coverage) != 2 {
		return eventStudyRead{}, errors.New("event study atomic read returned an incomplete result")
	}
	return eventStudyRead{
		version: result.version, buckets: result.buckets[0],
		domesticCoverage: result.coverage[0], globalCoverage: result.coverage[1],
	}, nil
}

func (r *Runtime) RetentionSec() int64 { return r.retentionSec }

func (r *Runtime) OrderBookStaleAfterSec() float64 { return r.books.StaleAfterSec() }

func (r *Runtime) OrderBookRows(ctx context.Context, markets []domain.MarketPair, notional float64) ([]orderbook.Row, error) {
	return r.OrderBookRowsWithQuiet(ctx, markets, notional, nil)
}

func (r *Runtime) OrderBookRowsWithQuiet(ctx context.Context, markets []domain.MarketPair, notional float64, quiet map[string]bool) ([]orderbook.Row, error) {
	if !r.started.Load() {
		return nil, ErrStopped
	}
	request := orderBookRowsRequest{markets: append([]domain.MarketPair(nil), markets...), notional: notional, quiet: copyBoolMap(quiet), response: make(chan []orderbook.Row, 1)}
	select {
	case r.orderBookRows <- request:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.done:
		return nil, ErrStopped
	}
	select {
	case rows := <-request.response:
		return rows, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.done:
		return nil, ErrStopped
	}
}

func (r *Runtime) OrderBookHealth(ctx context.Context, markets []domain.MarketPair, now time.Time) (orderbook.Health, error) {
	if !r.started.Load() {
		return orderbook.Health{}, ErrStopped
	}
	request := orderBookHealthRequest{markets: append([]domain.MarketPair(nil), markets...), now: now, response: make(chan orderbook.Health, 1)}
	select {
	case r.orderBookHealth <- request:
	case <-ctx.Done():
		return orderbook.Health{}, ctx.Err()
	case <-r.done:
		return orderbook.Health{}, ErrStopped
	}
	select {
	case health := <-request.response:
		return health, nil
	case <-ctx.Done():
		return orderbook.Health{}, ctx.Err()
	case <-r.done:
		return orderbook.Health{}, ErrStopped
	}
}

func (r *Runtime) OrderBookSnapshotWithQuiet(
	ctx context.Context,
	markets []domain.MarketPair,
	notional float64,
	quiet map[string]bool,
	now time.Time,
) (OrderBookSnapshot, error) {
	if !r.started.Load() {
		return OrderBookSnapshot{}, ErrStopped
	}
	request := orderBookSnapshotRequest{
		markets: append([]domain.MarketPair(nil), markets...), notional: notional,
		quiet: copyBoolMap(quiet), now: now, response: make(chan OrderBookSnapshot, 1),
	}
	select {
	case r.orderBookSnapshot <- request:
	case <-ctx.Done():
		return OrderBookSnapshot{}, ctx.Err()
	case <-r.done:
		return OrderBookSnapshot{}, ErrStopped
	}
	select {
	case snapshot := <-request.response:
		return snapshot, nil
	case <-ctx.Done():
		return OrderBookSnapshot{}, ctx.Err()
	case <-r.done:
		return OrderBookSnapshot{}, ErrStopped
	}
}

func (r *Runtime) OrderBookDetail(ctx context.Context, pair domain.MarketPair, notional float64) (orderbook.Detail, error) {
	if !r.started.Load() {
		return orderbook.Detail{}, ErrStopped
	}
	request := orderBookDetailRequest{pair: pair, notional: notional, response: make(chan orderbook.Detail, 1)}
	select {
	case r.orderBookDetails <- request:
	case <-ctx.Done():
		return orderbook.Detail{}, ctx.Err()
	case <-r.done:
		return orderbook.Detail{}, ErrStopped
	}
	select {
	case detail := <-request.response:
		return detail, nil
	case <-ctx.Done():
		return orderbook.Detail{}, ctx.Err()
	case <-r.done:
		return orderbook.Detail{}, ErrStopped
	}
}

func (r *Runtime) Coverage(ctx context.Context, start, end int64) (coverage.Result, error) {
	return r.coverageFor(ctx, start, end, false)
}

func (r *Runtime) GlobalCoverage(ctx context.Context, start, end int64) (coverage.Result, error) {
	return r.coverageFor(ctx, start, end, true)
}

func (r *Runtime) coverageFor(ctx context.Context, start, end int64, global bool) (coverage.Result, error) {
	if !r.started.Load() {
		return coverage.Result{}, ErrStopped
	}
	if end < start {
		return coverage.Result{}, errors.New("coverage end must not precede start")
	}
	request := coverageRequest{
		start: start, end: end, global: global,
		coverageTarget: r.coverageBarrierTarget(),
		response:       make(chan coverage.Result, 1),
	}
	select {
	case r.coverageRequests <- request:
	case <-ctx.Done():
		return coverage.Result{}, ctx.Err()
	case <-r.done:
		return coverage.Result{}, ErrStopped
	}
	select {
	case result := <-request.response:
		return result, nil
	case <-ctx.Done():
		return coverage.Result{}, ctx.Err()
	case <-r.done:
		return coverage.Result{}, ErrStopped
	}
}

func (r *Runtime) CoverageState(ctx context.Context) (coverage.State, error) {
	if !r.started.Load() {
		return coverage.State{}, ErrStopped
	}
	request := coverageStateRequest{
		coverageTarget: r.coverageBarrierTarget(),
		response:       make(chan coverage.State, 1),
	}
	select {
	case r.coverageStates <- request:
	case <-ctx.Done():
		return coverage.State{}, ctx.Err()
	case <-r.done:
		return coverage.State{}, ErrStopped
	}
	select {
	case state := <-request.response:
		return state, nil
	case <-ctx.Done():
		return coverage.State{}, ctx.Err()
	case <-r.done:
		return coverage.State{}, ErrStopped
	}
}

// InvalidateCoverage records a known observation loss directly in the owner
// goroutine. Unlike sampled health state, this cannot miss a transient parser
// failure that recovers before the next one-second readiness check.
func (r *Runtime) InvalidateCoverage(
	ctx context.Context,
	start int64,
	detected int64,
	gapContext coverage.GapContext,
) error {
	if !r.started.Load() {
		return ErrStopped
	}
	response := make(chan struct{}, 1)
	request, err := makeCoverageInvalidationRequest(start, detected, gapContext, response)
	if err != nil {
		return err
	}
	if err := r.submitCoverageInvalidation(ctx, request); err != nil {
		return err
	}
	select {
	case <-request.response:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return ErrStopped
	}
}

// QueueCoverageInvalidation durably hands a loss signal to the runtime's
// buffered owner queue without waiting for a database flush to finish. Loss
// callbacks must not time out merely because the single owner is temporarily
// blocked on persistence.
func (r *Runtime) QueueCoverageInvalidation(
	ctx context.Context,
	start int64,
	detected int64,
	gapContext coverage.GapContext,
) error {
	if !r.started.Load() {
		return ErrStopped
	}
	request, err := makeCoverageInvalidationRequest(start, detected, gapContext, nil)
	if err != nil {
		return err
	}
	return r.submitCoverageInvalidation(ctx, request)
}

func (r *Runtime) QueueGlobalCoverageInvalidation(
	ctx context.Context,
	start int64,
	detected int64,
	gapContext coverage.GapContext,
) error {
	if !r.started.Load() {
		return ErrStopped
	}
	request, err := makeCoverageInvalidationRequest(start, detected, gapContext, nil)
	if err != nil {
		return err
	}
	request.global = true
	return r.submitCoverageInvalidation(ctx, request)
}

// submitCoverageInvalidation assigns the same sequence used by the owner
// queue. Keeping assignment, enqueue, and publication under one lock makes a
// read barrier immune to another producer substituting its completed item.
func (r *Runtime) submitCoverageInvalidation(
	ctx context.Context,
	request coverageInvalidationRequest,
) error {
	if err := r.coverageSubmitMu.lockContext(ctx, r.done); err != nil {
		return err
	}
	defer r.coverageSubmitMu.Unlock()
	request.sequence = r.nextCoverageSequence + 1
	select {
	case r.coverageInvalid <- request:
		r.nextCoverageSequence = request.sequence
		r.admittedCoverageSequence.Store(request.sequence)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return ErrStopped
	}
}

func makeCoverageInvalidationRequest(
	start int64,
	detected int64,
	gapContext coverage.GapContext,
	response chan struct{},
) (coverageInvalidationRequest, error) {
	if start <= 0 || detected <= 0 || start > detected {
		return coverageInvalidationRequest{}, errors.New("coverage invalidation requires 0 < start <= detected")
	}
	gapContext.AffectedExchanges = append([]string(nil), gapContext.AffectedExchanges...)
	gapContext.AffectedMarkets = append([]string(nil), gapContext.AffectedMarkets...)
	gapContext.ReasonCodes = append([]string(nil), gapContext.ReasonCodes...)
	return coverageInvalidationRequest{
		start: start, detected: detected, context: gapContext, response: response,
	}, nil
}

func (r *Runtime) SetObservation(complete bool, context coverage.GapContext) {
	r.SetObservationFrom(complete, 0, context)
}

func (r *Runtime) SetObservationFrom(complete bool, invalidFrom int64, context coverage.GapContext) {
	context.AffectedExchanges = append([]string(nil), context.AffectedExchanges...)
	context.AffectedMarkets = append([]string(nil), context.AffectedMarkets...)
	context.ReasonCodes = append([]string(nil), context.ReasonCodes...)
	r.observation.Store(&ObservationState{Complete: complete, InvalidFrom: invalidFrom, Context: context})
}

func (r *Runtime) SetGlobalObservationFrom(complete bool, invalidFrom int64, context coverage.GapContext) {
	context.AffectedExchanges = append([]string(nil), context.AffectedExchanges...)
	context.AffectedMarkets = append([]string(nil), context.AffectedMarkets...)
	context.ReasonCodes = append([]string(nil), context.ReasonCodes...)
	r.globalObservation.Store(&ObservationState{Complete: complete, InvalidFrom: invalidFrom, Context: context})
}

// Stop must be called after collector producers have stopped. It rejects new
// submissions, drains every accepted event, and commits the current second.
func (r *Runtime) Stop(ctx context.Context) error {
	if !r.started.Load() {
		return nil
	}
	r.acceptMu.Lock()
	if !r.accepting {
		r.acceptMu.Unlock()
		select {
		case <-r.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.accepting = false
	r.acceptMu.Unlock()

	response := make(chan error, 1)
	select {
	case r.stop <- stopRequest{ctx: ctx, response: response}:
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return nil
	}
	select {
	case err := <-response:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runtime) Metrics() RuntimeMetrics {
	r.metricsMu.RLock()
	defer r.metricsMu.RUnlock()
	return r.metrics
}

func (r *Runtime) EngineInfo() aggregate.EngineInfo {
	return r.engineInfo
}

func (r *Runtime) QueueState() (size, capacity int) {
	return len(r.tradeQueue), cap(r.tradeQueue)
}

func (r *Runtime) run() {
	defer close(r.done)
	defer func() { r.aggregator.Close() }()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	pending := newPendingFlush()
	lastCoverageSecond := int64(0)
	persistenceBlocked := false
	for {
		tradeQueue := r.tradeQueue
		if persistenceBlocked {
			// Do not let fast database failures turn the retry buffer into an
			// unbounded second trade queue. The configured input queue applies
			// backpressure to collectors until the exact pending state commits.
			tradeQueue = nil
		}
		select {
		case request := <-r.handoffRequests:
			err := request.ctx.Err()
			if err == nil {
				r.catchUpCoverageInvalidations(request.coverageTarget)
				if persistenceBlocked {
					err = errors.New("deployment owner persistence is blocked")
				}
				for err == nil && r.processedTradeSequence < request.tradeTarget {
					select {
					case <-request.ctx.Done():
						err = request.ctx.Err()
					case trade := <-r.tradeQueue:
						r.processQueuedTrade(trade)
					default:
						err = errors.New("deployment trade barrier was not reached")
					}
				}
			}
			if err == nil {
				err = request.run(pending)
			}
			request.response <- err
		case trade := <-tradeQueue:
			r.processQueuedTrade(trade)
		case request := <-r.bookQueue:
			r.processBook(request)
		case request := <-r.snapshots:
			values, ok := r.aggregator.Snapshot(request.window)
			request.response <- snapshotResponse{values: values, ok: ok}
		case request := <-r.comparisons:
			current, previous, ok := r.aggregator.ComparisonSnapshot(request.window, request.now)
			request.response <- comparisonResponse{current: current, previous: previous, ok: ok}
		case request := <-r.intervals:
			values, ok := r.aggregator.IntervalSnapshot(request.start, request.end)
			request.response <- intervalResponse{values: values, ok: ok}
		case request := <-r.intervalBuckets:
			values, ok := r.aggregator.IntervalBuckets(request.start, request.end)
			request.response <- intervalBucketsResponse{values: values, ok: ok}
		case request := <-r.atomicReads:
			if request.ctx == nil {
				request.ctx = context.Background()
			}
			if err := request.ctx.Err(); err != nil {
				request.response <- atomicReadResult{err: err}
				continue
			}
			r.catchUpCoverageInvalidations(request.coverageTarget)
			caughtUp := r.processedTradeSequence >= request.tradeTarget
			var catchUpErr error
			if !caughtUp && !persistenceBlocked {
				caughtUp, catchUpErr = r.catchUpTrades(request.ctx, request.tradeTarget)
			}
			result := atomicReadResult{
				caughtUp: caughtUp,
				version:  r.aggregator.Metrics().Version,
				err:      catchUpErr,
			}
			if caughtUp {
				result.version = r.aggregator.Metrics().Version
				result.intervals, result.err = r.aggregator.IntervalSnapshotsContext(request.ctx, request.intervals)
				if result.err != nil {
					request.response <- result
					continue
				}
				result.buckets = make([][]aggregate.Bucket, len(request.bucketIntervals))
				for index, interval := range request.bucketIntervals {
					if result.err = request.ctx.Err(); result.err != nil {
						break
					}
					values, ok := r.aggregator.IntervalBucketsForAsset(
						interval.Start, interval.End, interval.Asset, interval.MarketScope,
					)
					if !ok {
						caughtUp = false
						result.caughtUp = false
						break
					}
					result.buckets[index] = values
				}
				if !caughtUp {
					request.response <- result
					continue
				}
				result.coverage = make([]coverage.Result, len(request.coverage))
				if request.includeCoverageState {
					result.coverageState = r.coverage.State()
				}
				if r.ingestion != nil {
					result.journalHealthSecond = r.ingestion.minuteState.LastHealthAt
				}
				if request.includeShareSeriesCorrections {
					result.shareSeriesCorrections = r.shareSeriesCorrections.since(request.shareSeriesSince)
				}
				for index, interval := range request.coverage {
					if result.err = request.ctx.Err(); result.err != nil {
						break
					}
					tracker := r.coverage
					if interval.global {
						tracker = r.globalCoverage
					}
					result.coverage[index] = tracker.Interval(interval.start, interval.end)
				}
			}
			request.response <- result
		case request := <-r.orderBookRows:
			options := r.orderBookOptions(request.notional)
			options.QuietMarkets = request.quiet
			request.response <- r.books.Rows(request.markets, options)
		case request := <-r.orderBookHealth:
			request.response <- r.books.Health(request.markets, request.now)
		case request := <-r.orderBookSnapshot:
			asOf := request.now
			if asOf.IsZero() {
				asOf = time.Now()
			}
			options := r.orderBookOptionsAt(request.notional, asOf)
			options.QuietMarkets = request.quiet
			request.response <- OrderBookSnapshot{
				Rows:    r.books.Rows(request.markets, options),
				Health:  r.books.Health(request.markets, asOf),
				Version: r.books.Version,
				AsOf:    asOf,
			}
		case request := <-r.orderBookDetails:
			request.response <- r.books.Detail(request.pair, r.orderBookOptions(request.notional))
		case request := <-r.coverageRequests:
			r.catchUpCoverageInvalidations(request.coverageTarget)
			tracker := r.coverage
			if request.global {
				tracker = r.globalCoverage
			}
			request.response <- tracker.Interval(request.start, request.end)
		case request := <-r.coverageStates:
			r.catchUpCoverageInvalidations(request.coverageTarget)
			request.response <- r.coverage.State()
		case request := <-r.coverageInvalid:
			r.applyCoverageInvalidation(request)
		case now := <-ticker.C:
			if r.ingestion != nil {
				r.aggregator.Advance(domain.UnixSecond(now.Unix()))
				continue
			}
			r.drainCoverageInvalidations(coverageDrainPerTurn)
			if now.Unix() != lastCoverageSecond {
				observation := r.observation.Load()
				invalidFrom := int64(0)
				if !observation.Complete {
					invalidFrom = observation.InvalidFrom
				}
				r.captureDeploymentObservation(now.Unix(), invalidFrom, observation.Complete, observation.Context)
				if !observation.Complete && observation.InvalidFrom > 0 {
					r.invalidateDomesticCoverage(observation.InvalidFrom, now.Unix(), observation.Context)
				} else {
					r.coverage.Advance(now.Unix(), observation.Complete, observation.Context)
				}
				// Gaps opened by queued losses or rejected trades since the last
				// tick are weighed here too, before the flush persists them.
				r.weighOpenGap()
				r.coverageDirty = true
				globalObservation := r.globalObservation.Load()
				if !globalObservation.Complete && globalObservation.InvalidFrom > 0 {
					r.globalCoverage.InvalidateFrom(globalObservation.InvalidFrom, now.Unix(), globalObservation.Context)
				} else {
					r.globalCoverage.Advance(now.Unix(), globalObservation.Complete, globalObservation.Context)
				}
				lastCoverageSecond = now.Unix()
			}
			persistenceBlocked = r.flushWithTimeout(
				context.Background(), domain.UnixSecond(now.Unix()), pending,
			) != nil
		case request := <-r.stop:
			if r.ingestion != nil {
				request.response <- nil
				return
			}
			r.drainQueues()
			now := domain.UnixSecond(time.Now().Unix() + 1)
			if latest := r.aggregator.LatestBucketSecond(); latest >= now && latest < domain.UnixSecond(math.MaxInt64) {
				now = latest + 1
			}
			err := r.flushUntilCommitted(request.ctx, now, pending)
			request.response <- err
			return
		}
	}
}

func copyBoolMap(source map[string]bool) map[string]bool {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]bool, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (r *Runtime) orderBookOptions(notional float64) orderbook.ViewOptions {
	return r.orderBookOptionsAt(notional, time.Now())
}

func (r *Runtime) orderBookOptionsAt(notional float64, now time.Time) orderbook.ViewOptions {
	if now.IsZero() {
		now = time.Now()
	}
	resolve := func(quote string) (float64, bool) {
		rate := r.oracle.RateToKRW(quote, domain.UnixMilli(now.UnixMilli()))
		return resolvedRate(rate)
	}
	executable := func(side string) orderbook.RateResolver {
		return func(quote string) (float64, bool) {
			rate, err := r.oracle.ExecutableRateToKRW(quote, side, domain.UnixMilli(now.UnixMilli()))
			if err != nil {
				return 0, false
			}
			return resolvedRate(rate)
		}
	}
	return orderbook.ViewOptions{MidRate: resolve, BuyRate: executable("buy"), SellRate: executable("sell"), NotionalKRW: notional, SlippageNotionalsKRW: slippageScenarioNotionalsKRW(), Now: now}
}

func slippageScenarioNotionalsKRW() []float64 {
	return []float64{10_000_000, 50_000_000, 100_000_000}
}

func resolvedRate(rate *float64) (float64, bool) {
	if rate == nil || *rate <= 0 || math.IsNaN(*rate) || math.IsInf(*rate, 0) {
		return 0, false
	}
	return *rate, true
}

func (r *Runtime) processQueuedTrade(trade queuedTrade) {
	r.processTradeInput(trade.event, trade.historical, true)
	r.processedTradeSequence = trade.sequence
}

func (r *Runtime) processTrade(event domain.TradeEvent, historical bool) {
	r.processTradeInput(event, historical, false)
}

func (r *Runtime) processTradeInput(event domain.TradeEvent, historical, alreadySubmitted bool) {
	queueLatency := int64(0)
	if !historical {
		queueLatency = time.Now().UnixMilli() - int64(event.ReceivedTS)
		if queueLatency < 0 {
			queueLatency = 0
		}
		r.recordTradeQueueLatency(queueLatency)
	}
	timely := !historical && int64(event.ReceivedTS)-int64(event.EventTS) <= aggregate.MaxLiveEventReceiveSkew
	quoteIsKRW := strings.EqualFold(strings.TrimSpace(event.Quote), "KRW")
	rate := &krwVolumeRate
	if !quoteIsKRW {
		rate = r.oracle.VolumeRateToKRW(event.Quote, event.ReceivedTS)
	}
	if !timely && !quoteIsKRW {
		// A receive-time FX quote must not be projected backwards onto a delayed
		// exchange event. Keep the native volume and make the converted result
		// explicitly unresolved instead of recording a plausible but wrong KRW value.
		rate = nil
	}
	now := domain.UnixSecond(time.Now().Unix())
	beforeAggregation := r.aggregator.Metrics()
	acceptedTrade, accepted := r.aggregator.IngestWithResult(event, rate, now)
	afterAggregation := r.aggregator.Metrics()
	if !accepted && afterAggregation.InvalidEvents > beforeAggregation.InvalidEvents {
		r.invalidateRejectedTrade(event, now)
	}
	if accepted {
		if event.Scope == domain.ScopeDomestic {
			r.recordShareSeriesTradeCorrection(int64(acceptedTrade.EventSecond), event.Quote, int64(now))
		}
		if r.ingestion != nil {
			if err := r.ingestion.observeMinute(acceptedTrade); err != nil {
				r.ingestion.derivedError = err
			}
			r.updateMetrics(func(m *RuntimeMetrics) { m.IngestionMinuteRecoveryEvents = r.ingestion.minuteState.RecoveryEvents })
		}
		r.captureDeploymentTrade(event, rate, now)
		if timely {
			// ObserveTrade timestamps the price with ReceivedTS. A delayed event is
			// historical evidence and must not overwrite the live FX graph.
			r.oracle.ObserveTrade(event)
		}
		r.observerMu.RLock()
		observer := r.onAccepted
		r.observerMu.RUnlock()
		if observer != nil {
			if err := observer(acceptedTrade); err != nil {
				r.logger.Error("accepted trade observer failed", "error", err)
				r.updateMetrics(func(metrics *RuntimeMetrics) { metrics.AcceptedObserverFailures++ })
			}
		}
	}
	aggregation := afterAggregation
	r.updateMetrics(func(metrics *RuntimeMetrics) {
		// Journal batches enter on the owner directly, bypassing submitTrade.
		// Count their admission and result together. A checkpoint retry never
		// calls this method again, and queue inputs were counted on submission.
		if r.ingestion != nil && !alreadySubmitted {
			if historical {
				metrics.HistoricalTradesSubmitted++
			} else {
				metrics.TradesSubmitted++
				metrics.JournalTradesSubmitted++
			}
		}
		if historical {
			metrics.HistoricalTradesProcessed++
			if accepted {
				metrics.HistoricalAccepted++
			} else if afterAggregation.Duplicates > beforeAggregation.Duplicates {
				metrics.HistoricalDuplicates++
			} else if afterAggregation.InvalidEvents > beforeAggregation.InvalidEvents {
				metrics.HistoricalInvalid++
			}
		} else {
			metrics.TradesProcessed++
		}
		metrics.AggregatorAccepted = aggregation.IngestedTrades
		metrics.DuplicateTrades = aggregation.Duplicates
		metrics.InvalidTrades = aggregation.InvalidEvents
		metrics.TradeQueueP99UpperBoundMS = r.tradeQueuePercentile(0.99)
		if !historical && queueLatency > metrics.TradeQueueMaxMS {
			metrics.TradeQueueMaxMS = queueLatency
		}
	})
}

func (r *Runtime) invalidateRejectedTrade(event domain.TradeEvent, detected domain.UnixSecond) {
	start := domain.UnixSecond(int64(event.EventTS) / 1000)
	retainedAfter := detected - domain.UnixSecond(r.aggregator.RetentionSec())
	if start <= retainedAfter {
		// The event belongs entirely outside the queryable retention window.
		return
	}
	if start <= 0 || start > detected {
		start = detected
	}
	gapContext := coverage.GapContext{
		AffectedExchanges: []string{string(event.Exchange)},
		AffectedMarkets:   []string{event.Market},
		WeightWindowSec:   10,
		ReasonCodes: []string{
			"AGGREGATOR_REJECTED_TRADE",
			fmt.Sprintf("MISSING_TRADE_EVENTS:%d", event.EffectiveMatchCount()),
		},
	}
	if event.Scope == domain.ScopeGlobal {
		r.globalCoverage.InvalidateFrom(int64(start), int64(detected), gapContext)
		return
	}
	r.invalidateDomesticCoverage(int64(start), int64(detected), gapContext)
	r.captureDeploymentObservation(int64(detected), int64(start), false, gapContext)
	r.coverageDirty = true
}

func (r *Runtime) recordTradeQueueLatency(milliseconds int64) {
	for index, bound := range tradeQueueLatencyBoundsMS {
		if milliseconds <= bound {
			r.tradeLatency[index]++
			return
		}
	}
	r.tradeLatency[len(r.tradeLatency)-1]++
}

func (r *Runtime) tradeQueuePercentile(percentile float64) int64 {
	var total uint64
	for _, count := range r.tradeLatency {
		total += count
	}
	if total == 0 {
		return 0
	}
	target := uint64(math.Ceil(float64(total) * percentile))
	var seen uint64
	for index, count := range r.tradeLatency {
		seen += count
		if seen < target {
			continue
		}
		if index < len(tradeQueueLatencyBoundsMS) {
			return tradeQueueLatencyBoundsMS[index]
		}
		return tradeQueueLatencyBoundsMS[len(tradeQueueLatencyBoundsMS)-1] + 1
	}
	return tradeQueueLatencyBoundsMS[len(tradeQueueLatencyBoundsMS)-1] + 1
}

func (r *Runtime) processBook(request bookRequest) {
	if request.batchDone != nil {
		for index, event := range request.events {
			request.batchAccepted[index] = r.applyBook(event)
		}
		request.batchDone <- struct{}{}
		return
	}
	request.response <- bookResponse{accepted: r.applyBook(request.event)}
}

func (r *Runtime) applyBook(event domain.OrderBookEvent) bool {
	accepted := r.books.Ingest(event)
	if accepted {
		best, bestPricesValid := r.books.BestPrices(event.Exchange, event.Market)
		if bestPricesValid {
			r.oracle.UpdateOrderBook(
				event.Base, event.Quote, best.Bid, best.Ask,
				string(event.Exchange), best.ReceivedTS,
			)
		}
		r.updateMetrics(func(metrics *RuntimeMetrics) { metrics.OrderBooksProcessed++ })
		r.observeCalculatedBook(event.Exchange, event.Market, best.ReceivedTS)
	}
	return accepted
}

func (r *Runtime) observeCalculatedBook(exchange domain.Exchange, market string, receivedTS domain.UnixMilli) {
	r.observerMu.RLock()
	observer := r.onBookSnapshot
	r.observerMu.RUnlock()
	if observer == nil || receivedTS <= 0 {
		return
	}
	second := int64(receivedTS) / 1000
	key := calculatedBookSecondKey{exchange: exchange, market: strings.ToUpper(strings.TrimSpace(market))}
	if previous, exists := r.bookSnapshotSecond[key]; exists && second <= previous {
		return
	}
	snapshot, ok := r.books.TopSnapshot(exchange, market)
	if !ok {
		return
	}
	r.bookSnapshotSecond[key] = second
	observer(snapshot)
	r.updateMetrics(func(metrics *RuntimeMetrics) { metrics.OrderBookSnapshotsQueued++ })
}

func (r *Runtime) drainQueues() {
	for {
		select {
		case trade := <-r.tradeQueue:
			r.processQueuedTrade(trade)
		case request := <-r.bookQueue:
			r.processBook(request)
		case request := <-r.coverageInvalid:
			r.applyCoverageInvalidation(request)
		default:
			return
		}
	}
}

func (r *Runtime) applyCoverageInvalidation(request coverageInvalidationRequest) {
	tracker := r.coverage
	if request.global {
		tracker = r.globalCoverage
	} else {
		r.coverageDirty = true
		r.captureDeploymentObservation(request.detected, request.start, false, request.context)
	}
	if request.global {
		tracker.InvalidateFrom(request.start, request.detected, request.context)
	} else {
		r.invalidateDomesticCoverage(request.start, request.detected, request.context)
	}
	r.processedCoverageSequence.Store(request.sequence)
	if request.response != nil {
		request.response <- struct{}{}
	}
}

func (r *Runtime) drainCoverageInvalidations(limit int) {
	for processed := 0; processed < limit; processed++ {
		select {
		case request := <-r.coverageInvalid:
			r.applyCoverageInvalidation(request)
		default:
			return
		}
	}
}

func (r *Runtime) catchUpCoverageInvalidations(target uint64) {
	for r.processedCoverageSequence.Load() < target {
		request := <-r.coverageInvalid
		r.applyCoverageInvalidation(request)
	}
}

func (r *Runtime) catchUpTrades(ctx context.Context, target uint64) (bool, error) {
	for r.processedTradeSequence < target {
		// A foreground read must not retain the sole owner after its caller has
		// gone away. Check before taking the next admitted trade; once dequeued,
		// process it completely so cancellation cannot lose or reorder input.
		if err := ctx.Err(); err != nil {
			return false, err
		}
		select {
		case trade := <-r.tradeQueue:
			r.processQueuedTrade(trade)
		default:
			return false, nil
		}
	}
	return true, nil
}

func (r *Runtime) tradeBarrierTarget() uint64 {
	// Publish only after enqueue, before submission returns. A read therefore
	// includes every completed submission without waiting behind a producer
	// blocked on a full queue beyond the read's context deadline.
	return r.admittedTradeSequence.Load()
}

func (r *Runtime) coverageBarrierTarget() uint64 {
	return r.admittedCoverageSequence.Load()
}

func (r *Runtime) flush(ctx context.Context, now domain.UnixSecond, pending *pendingFlush) error {
	return r.flushThrough(ctx, now, now, pending)
}

func (r *Runtime) flushThrough(ctx context.Context, now, before domain.UnixSecond, pending *pendingFlush) error {
	r.aggregator.Advance(now)
	buckets, watermarks, recent := pending.drain(r.aggregator, before)
	hadBacklog := !pending.empty()
	if hadBacklog {
		pending.add(buckets, watermarks, recent)
		buckets, watermarks, recent = pending.rows()
	}
	if len(buckets) > 0 || len(watermarks) > 0 || len(recent) > 0 || r.coverageDirty {
		observation := r.coverage.State()
		if err := r.store.FlushWithCoverage(ctx, buckets, watermarks, recent, &observation); err != nil {
			// Healthy operation sends the freshly drained slices directly to the
			// synchronous store. Materialize the retry maps only after a failure;
			// this keeps failure idempotency without copying every successful
			// one-second batch twice.
			if !hadBacklog {
				pending.add(buckets, watermarks, recent)
			}
			r.updateMetrics(func(metrics *RuntimeMetrics) {
				metrics.FlushFailures++
				metrics.LastFlushError = err.Error()
				metrics.LastFlushErrorAt = time.Now()
			})
			r.logger.Error("aggregation flush failed", "error", err)
			return err
		}
		r.coverage.ApplyPersistenceIdentities(observation)
		if hadBacklog {
			pending.clear()
		}
		r.coverageDirty = false
		r.updateMetrics(func(metrics *RuntimeMetrics) {
			metrics.LastFlush = time.Now()
		})
	}
	return nil
}

func aggregationErrorActive(metrics RuntimeMetrics) bool {
	return !metrics.LastFlushErrorAt.IsZero() && metrics.LastFlushErrorAt.After(metrics.LastFlush)
}

func (r *Runtime) flushWithTimeout(parent context.Context, now domain.UnixSecond, pending *pendingFlush) error {
	timeout := r.storeTimeout
	if timeout <= 0 {
		timeout = aggregationStoreTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	return r.flush(ctx, now, pending)
}

func (r *Runtime) flushUntilCommitted(ctx context.Context, now domain.UnixSecond, pending *pendingFlush) error {
	const attempts = 3
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		err = r.flushWithTimeout(ctx, now, pending)
		if err == nil {
			return nil
		}
		if attempt < attempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 100 * time.Millisecond):
			}
		}
	}
	return fmt.Errorf("flush shutdown state after %d attempts: %w", attempts, err)
}

func (r *Runtime) updateMetrics(change func(*RuntimeMetrics)) {
	r.metricsMu.Lock()
	change(&r.metrics)
	r.metricsMu.Unlock()
}

type bucketKey struct {
	second domain.UnixSecond
	key    aggregate.VolumeKey
}

type recentKey struct {
	exchange domain.Exchange
	market   string
	tradeID  string
}

type watermarkKey struct {
	exchange domain.Exchange
	market   string
}

const maxRetainedPendingFlushRows = 16_384

type pendingFlush struct {
	buckets    map[bucketKey]aggregate.Bucket
	watermarks map[watermarkKey]aggregate.WatermarkRow
	recent     map[recentKey]aggregate.RecentIDRow

	bucketRows    []aggregate.Bucket
	watermarkRows []aggregate.WatermarkRow
	recentRows    []aggregate.RecentIDRow

	drainBuckets    []aggregate.Bucket
	drainWatermarks []aggregate.WatermarkRow
	drainRecent     []aggregate.RecentIDRow
}

func newPendingFlush() *pendingFlush {
	return &pendingFlush{
		buckets:    make(map[bucketKey]aggregate.Bucket),
		watermarks: make(map[watermarkKey]aggregate.WatermarkRow),
		recent:     make(map[recentKey]aggregate.RecentIDRow),
	}
}

func (p *pendingFlush) add(
	buckets []aggregate.Bucket,
	watermarks []aggregate.WatermarkRow,
	recent []aggregate.RecentIDRow,
) {
	for _, row := range buckets {
		p.buckets[bucketKey{second: row.Second, key: row.Key}] = row
	}
	for _, row := range watermarks {
		key := watermarkKey{exchange: row.Exchange, market: row.Market}
		if previous, ok := p.watermarks[key]; !ok || row.TradeID > previous.TradeID {
			p.watermarks[key] = row
		}
	}
	for _, row := range recent {
		key := recentKey{exchange: row.Exchange, market: row.Market, tradeID: row.TradeID}
		if previous, ok := p.recent[key]; !ok || row.EventSecond > previous.EventSecond {
			p.recent[key] = row
		}
	}
}

func (p *pendingFlush) drain(
	aggregator *aggregate.Aggregator,
	current domain.UnixSecond,
) ([]aggregate.Bucket, []aggregate.WatermarkRow, []aggregate.RecentIDRow) {
	p.drainBuckets = clearOrReleaseRows(p.drainBuckets)
	p.drainWatermarks = clearOrReleaseRows(p.drainWatermarks)
	p.drainRecent = clearOrReleaseRows(p.drainRecent)
	p.drainBuckets, p.drainWatermarks, p.drainRecent = aggregator.DrainClosedInto(
		current, p.drainBuckets, p.drainWatermarks, p.drainRecent,
	)
	return p.drainBuckets, p.drainWatermarks, p.drainRecent
}

func (p *pendingFlush) empty() bool {
	return len(p.buckets) == 0 && len(p.watermarks) == 0 && len(p.recent) == 0
}

func (p *pendingFlush) rows() ([]aggregate.Bucket, []aggregate.WatermarkRow, []aggregate.RecentIDRow) {
	p.bucketRows = p.bucketRows[:0]
	for _, row := range p.buckets {
		p.bucketRows = append(p.bucketRows, row)
	}
	p.watermarkRows = p.watermarkRows[:0]
	for _, row := range p.watermarks {
		p.watermarkRows = append(p.watermarkRows, row)
	}
	p.recentRows = p.recentRows[:0]
	for _, row := range p.recent {
		p.recentRows = append(p.recentRows, row)
	}
	return p.bucketRows, p.watermarkRows, p.recentRows
}

func (p *pendingFlush) clear() {
	p.buckets = clearOrReleaseMap(p.buckets)
	p.watermarks = clearOrReleaseMap(p.watermarks)
	p.recent = clearOrReleaseMap(p.recent)
	p.bucketRows = clearOrReleaseRows(p.bucketRows)
	p.watermarkRows = clearOrReleaseRows(p.watermarkRows)
	p.recentRows = clearOrReleaseRows(p.recentRows)
}

func clearOrReleaseMap[K comparable, V any](values map[K]V) map[K]V {
	if len(values) > maxRetainedPendingFlushRows {
		return make(map[K]V)
	}
	clear(values)
	return values
}

func clearOrReleaseRows[T any](rows []T) []T {
	clear(rows)
	if cap(rows) > maxRetainedPendingFlushRows {
		return nil
	}
	return rows[:0]
}
