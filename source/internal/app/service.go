package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/config"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
	"market-share-go/internal/fx"
	"market-share-go/internal/hermesrunner"
	"market-share-go/internal/movement"
	"market-share-go/internal/notice"
	"market-share-go/internal/orderbook"
	"market-share-go/internal/reconcile"
	"market-share-go/internal/store/postgres"
	sqlitestore "market-share-go/internal/store/sqlite"
	"market-share-go/internal/tickertrend"
	officialdata "market-share-go/services/collector/official"
	"market-share-go/services/collector/registry"
	"market-share-go/services/collector/restbudget"
	"market-share-go/services/collector/stream"
)

const hydrateRetention = 2*24*time.Hour + 5*time.Second

const recentTradeIDRetention = 5 * time.Minute

var ErrLLMDisabled = errors.New("LLM features are disabled for this runtime")

const (
	recentTradeIDDatabaseRetention = 4 * time.Hour
	recentTradeIDPruneInterval     = time.Minute
	recentTradeIDPruneTimeout      = 5 * time.Second
	recentTradeIDPruneBatchSize    = 50_000
	rollingTrackerMetadataKey      = "summary2.rolling_share_tracker.v1"
	rollingTrackerPersistEvery     = 30 * time.Second
	rollingTrackerObserveEvery     = 10 * time.Second
	realtimeFlowSampleEvery        = time.Second
	realtimeFlowPersistEvery       = 10 * time.Second
	databaseHealthInterval         = 5 * time.Second
	databaseHealthTimeout          = 2 * time.Second
)

type HealthState struct {
	Status                 string
	Running                bool
	RealtimeReady          bool
	RegistryFallbackActive bool
	DatabaseReady          bool
	Unhealthy              []string
	ExcludedExchanges      []string
}

type Service struct {
	collectorURL                string
	collectorToken              string
	computeOnly                 bool
	freshStartSecond            int64
	intelligenceEvidence        IntelligenceEvidenceProvider
	cancel                      context.CancelFunc
	repository                  serviceRepository
	postgresRepository          *postgres.Repository
	storageMode                 string
	llmEnabled                  bool
	questionLearningEnabled     bool
	recentTradeIDPruner         recentTradeIDPruner
	websocketFrameStore         *postgres.WebSocketFrameStore
	websocketArchiver           websocketFrameArchiver
	websocketRetentionState     atomic.Pointer[WebSocketRetentionMetrics]
	websocketJournal            *websocketFrameJournal
	orderBookLatest             *orderBookLatestWriter
	runtime                     *Runtime
	budgets                     *restbudget.Manager
	registry                    *registry.Registry
	collectors                  *collector.Manager
	coordinator                 *RegistryCoordinator
	reconciler                  *reconcile.MinuteService
	officialDomestic            *officialdata.TickerService
	officialAlignedDomestic     *officialdata.AlignedCandleService
	binanceTicker               *officialdata.BinanceTickerService
	movementTracker             *movement.Tracker
	tickerTrend                 *tickertrend.Tracker
	noticeMonitor               *notice.Monitor
	noticePoll                  time.Duration
	noticeOCRBackend            string
	hermesNoticeIngestToken     string
	noticePipelineV2Enabled     bool
	hermesQuestionConfig        hermesrunner.MarketQuestionConfig
	marketQuestionTimeout       time.Duration
	marketQuestionCaptureInput  bool
	hermesQuestionMu            sync.Mutex
	hermesQuestionRunGate       chan struct{}
	hermesQuestionCache         marketQuestionCacheEntry
	hermesNoticeQuestionMu      sync.Mutex
	hermesNoticeQuestionRunGate chan struct{}
	hermesNoticeQuestionCache   noticeQuestionAnswerCache
	hermesNoticeQuestionFlights map[string]*noticeQuestionFlight
	noticeQuestionRAGMu         sync.Mutex
	noticeQuestionRAGIndex      domesticNoticeRAGIndex
	analysisMemo                *analysisMemo
	analysisMemoOnce            sync.Once
	analysisReadMemo            *analysisReadMemo
	analysisReadMemoOnce        sync.Once
	eventStudyMemo              *eventStudyMemo
	eventStudyMemoOnce          sync.Once
	noticeMemo                  *noticeSnapshotMemo
	noticeMemoOnce              sync.Once
	realtimeHistoryMemo         *realtimeHistoryMemo
	realtimeHistoryMemoOnce     sync.Once
	realtimeSummaryRing         *realtimeSummaryRing
	realtimePeriodSummaryMemo   *realtimePeriodSummaryCache
	realtimeSummaryOnce         sync.Once
	registryHistoryMemo         *registryHistoryMemo
	registryHistoryMemoOnce     sync.Once
	activeNoticeNetworkMemo     *activeNoticeNetworkMemo
	activeNoticeNetworkMemoOnce sync.Once
	rollingTracker              *marketanalysis.RollingMovementTracker
	realtimeFlow                *marketanalysis.RealtimeFlowAnalyzer
	whaleAlerts                 *marketanalysis.WhaleAlertDetector
	microstructureHistory       *orderbook.MicrostructureHistory
	referenceOrderKRW           float64
	priceGapQuarantineBPS       float64
	contractIdentities          map[string]marketanalysis.ContractIdentityProof
	logger                      *slog.Logger
	movementWait                sync.WaitGroup
	backgroundWait              sync.WaitGroup
	rollingPersistMu            sync.Mutex
	rollingPersistedAt          time.Time
	deploymentDerivedMu         sync.Mutex
	deploymentDerivedGeneration atomic.Uint64
	realtimeSampleMu            sync.Mutex
	realtimeSampleGeneration    uint64
	lastRealtimeSecond          int64
	lastRealtimeBookSlot        int64
	lastRealtimeStoreSlot       int64
	liveCoins                   liveCoinStream
	shareSeries                 shareSeriesMemo
	startedAt                   time.Time
	operationalMu               sync.Mutex
	operational                 atomic.Pointer[operationalSnapshot]
	runtimeStateMu              sync.Mutex
	runtimeStateCachedAt        time.Time
	runtimeStateCache           map[string]any

	marketQuestionACEnabled         bool
	marketQuestionEvidenceRetention time.Duration
	marketQuestionScopeKey          [32]byte
	marketQuestionScopeKeyReady     bool
	// operationalSource is a test seam for proving that every public read
	// surface shares one collector/registry/runtime capture. Production leaves
	// it nil and readOperationalInputs reads the concrete dependencies.
	operationalSource func(time.Time) operationalInputs

	running                    atomic.Bool
	databaseReady              atomic.Bool
	retentionPruneFailures     atomic.Uint64
	retentionRowsPruned        atomic.Uint64
	retentionLastSuccess       atomic.Int64
	stopOnce                   sync.Once
	stopErr                    error
	deployment                 *deploymentWriterGate
	deploymentMu               sync.Mutex
	deploymentCheckpoint       *preparedRuntimeCheckpoint
	deploymentPrepared         atomic.Bool
	deploymentPreparedCapture  atomic.Pointer[runtimeDeploymentCapture]
	deploymentCheckpointResult atomic.Pointer[DeploymentCheckpointResult]
}

type recentTradeIDPruner interface {
	PruneRecentTradeIDs(context.Context, domain.UnixSecond, int) (int64, error)
}

func StartService(parent context.Context, settings config.Config, logger *slog.Logger) (*Service, error) {
	ctx, cancel := context.WithCancel(parent)
	service := &Service{cancel: cancel, logger: logger, startedAt: time.Now()}
	if err := service.start(ctx, settings); err != nil {
		cancel()
		service.cleanupFailedStart()
		return nil, err
	}
	service.running.Store(true)
	service.invalidateOperationalSnapshot()
	return service, nil
}

func (s *Service) start(ctx context.Context, settings config.Config) error {
	if s.computeOnly {
		// The isolated calculator is the single canonical state writer. This
		// lease is independent of the collector's append session.
		settings.DeploymentMode = config.DeploymentActive
		settings.DeploymentInstance = "marketgo2-calculator"
		settings.HermesNoticeAutostart = false
	}
	s.storageMode = settings.StorageMode
	s.llmEnabled = settings.LLMEnabled
	s.questionLearningEnabled = settings.QuestionLearningEnabled
	s.marketQuestionACEnabled = settings.QuestionACEnabled
	s.marketQuestionEvidenceRetention = time.Duration(settings.QuestionEvidenceRetentionDays) * 24 * time.Hour
	s.analysisMemoOnce.Do(func() { s.analysisMemo = newAnalysisMemo() })
	s.initRealtimeSummaryState()
	s.rollingTracker = marketanalysis.NewRollingMovementTracker()
	s.realtimeFlow = marketanalysis.NewRealtimeFlowAnalyzer(marketanalysis.RealtimeFlowConfig{})
	s.whaleAlerts = marketanalysis.NewWhaleAlertDetector(marketanalysis.WhaleAlertConfig{})
	s.microstructureHistory = orderbook.NewMicrostructureHistory(orderbook.DefaultHistoryRetentionSeconds, orderbook.DefaultHistoryMaxPoints)
	s.referenceOrderKRW = settings.ReferenceOrderKRW
	s.priceGapQuarantineBPS = settings.PriceGapQuarantineBPS
	s.contractIdentities = settings.ContractIdentities
	s.noticePoll = settings.NoticePoll
	s.noticeOCRBackend = "hermes_remote_vision"
	if strings.TrimSpace(settings.VisionOCRBin) != "" {
		s.noticeOCRBackend = "apple_vision+hermes_remote_vision"
	}
	s.hermesNoticeIngestToken = settings.HermesNoticeIngestToken
	s.marketQuestionTimeout = settings.DevelopmentQuestionTimeout
	s.marketQuestionCaptureInput = settings.DevelopmentQuestionCaptureInput
	s.noticePipelineV2Enabled = settings.NoticePipelineV2Enabled
	s.hermesQuestionConfig = hermesrunner.MarketQuestionConfig{
		HermesBin: settings.HermesNoticeBin,
		OutputDir: settings.HermesNoticeOutputDir,
		// Leave the shared transport model unset: notice owns its Sol default,
		// while marketShareQuestionConfig selects Astra independently.
		MaxTokens: hermesrunner.MaxInteractiveQuestionOutputTokens,
	}
	var repository serviceRepository
	var err error
	if settings.StorageMode == config.StorageSQLiteMemory {
		repository, err = sqlitestore.OpenMemory(ctx)
	} else {
		var postgresRepository *postgres.Repository
		if s.computeOnly {
			postgresRepository, err = postgres.OpenWhenReady(ctx, settings.DatabaseURL, 8)
		} else {
			postgresRepository, err = postgres.Open(ctx, settings.DatabaseURL, 8)
		}
		repository = postgresRepository
		s.postgresRepository = postgresRepository
	}
	if err != nil {
		return err
	}
	if s.postgresRepository != nil && !s.computeOnly {
		// Validate the journal/retention schema before a writer lease, readiness,
		// or any background work. SHADOW journal acknowledgements do not run SQL.
		s.websocketFrameStore, err = postgres.OpenWebSocketFrameStore(ctx, settings.DatabaseURL)
		if err != nil {
			repository.Close()
			return err
		}
	}
	var acquireLease func(context.Context) (deploymentLease, error)
	if s.postgresRepository != nil {
		acquireLease = func(leaseCtx context.Context) (deploymentLease, error) {
			return s.postgresRepository.TryAcquireWriterLease(leaseCtx, settings.DeploymentInstance)
		}
	}
	deployment, err := newDeploymentWriterGate(ctx, settings, acquireLease)
	if err != nil {
		repository.Close()
		return err
	}
	s.deployment = deployment
	gatedRepository := &gatedServiceRepository{serviceRepository: repository, gate: deployment}
	repository = gatedRepository
	s.repository = gatedRepository
	s.recentTradeIDPruner = gatedRepository
	if s.postgresRepository != nil && !s.computeOnly {
		s.websocketArchiver = gatedWebSocketArchiver{next: s.postgresRepository, gate: deployment}
	}
	s.databaseReady.Store(true)
	if s.freshStartSecond == 0 {
		s.restoreRollingTracker(ctx)
	}

	aggregator, err := aggregate.New(nil)
	if err != nil {
		return err
	}
	aggregatorOwned := true
	defer func() {
		if aggregatorOwned {
			aggregator.Close()
		}
	}()
	engine := aggregator.EngineInfo()
	if engine.Name != settings.AggregationEngine {
		return fmt.Errorf("aggregation engine mismatch: configured %q, binary provides %q", settings.AggregationEngine, engine.Name)
	}
	if settings.AggregationEngine == "rust-ffi" && (!engine.Native || engine.ABIVersion != aggregate.RustRollingABIVersion) {
		return fmt.Errorf("rust aggregation engine is not production-ready: native=%t abi=%d", engine.Native, engine.ABIVersion)
	}
	books := orderbook.NewStore(settings.OrderBookStale)
	oracle := fx.New(settings.FXMaxAge.Milliseconds(), nil)
	runtime, err := NewRuntime(aggregator, books, oracle, repository, settings.QueueSize, s.logger)
	if err != nil {
		return err
	}
	var ingestionCursor int64
	if s.computeOnly {
		// The writer lease is already held. Record the startup skip atomically;
		// never decode or replay envelopes accumulated while this process was off.
		err = deployment.dispatchActiveOnly(ctx, bufferedDeploymentWrite{name: "startup_ingestion_head", run: func(writeCtx context.Context) error {
			ingestionCursor, err = s.postgresRepository.StartIngestionAtHead(writeCtx)
			return err
		}})
		if err != nil {
			return fmt.Errorf("load computation checkpoint: %w", err)
		}
		runtime.enableIngestion(gatedIngestionCheckpointStore{next: s.postgresRepository, gate: deployment}, ingestionCursor)
		runtime.ingestion.latestBooks = true
	}
	now := domain.UnixSecond(time.Now().Unix())
	if s.freshStartSecond > 0 {
		if err := runtime.Hydrate(postgres.State{CoverageLoaded: true, Coverage: coverage.State{CoverageStart: s.freshStartSecond}}, now); err != nil {
			return err
		}
	} else if err := hydrateRuntimeStreaming(
		ctx,
		gatedRepository.serviceRepository,
		runtime,
		now-domain.UnixSecond(hydrateRetention/time.Second),
		now-domain.UnixSecond(recentTradeIDRetention/time.Second),
		now,
	); err != nil {
		return err
	}
	if err := runtime.Start(); err != nil {
		return err
	}
	s.runtime = runtime
	aggregatorOwned = false

	httpClient := sharedHTTPClient()
	budgets, err := restbudget.New(ctx, restbudget.MaxRateFraction, nil)
	if err != nil {
		return err
	}
	s.budgets = budgets
	official := &restbudget.Client{HTTP: httpClient, Budgets: budgets, Attempts: 4}
	officialDomestic := officialdata.NewTickerService(official, officialdata.DefaultTickerRefresh)
	s.officialDomestic = officialDomestic
	officialAlignedDomestic := officialdata.NewAlignedCandleService(official)
	s.officialAlignedDomestic = officialAlignedDomestic
	binanceTicker := officialdata.NewBinanceTickerService(official, settings.BinanceTickerRefresh)
	s.binanceTicker = binanceTicker
	s.movementTracker = movement.NewTracker(movement.Config{
		SampleInterval:    officialdata.DefaultAlignedCandleRefresh,
		Source:            officialdata.Aligned24hSource,
		RestoreMaxAge:     2*time.Hour + 15*time.Minute,
		MaxObservationGap: time.Hour + 15*time.Minute,
	}, repository)
	if s.freshStartSecond == 0 {
		if err := s.movementTracker.Restore(ctx); err != nil {
			return err
		}
	}
	s.tickerTrend = tickertrend.NewTracker(repository)
	// Official hourly history is independent of the live observation window.
	if err := s.tickerTrend.Restore(ctx); err != nil {
		return err
	}
	marketRegistry, err := registry.New(httpClient, official, nil, nil)
	if err != nil {
		return err
	}
	storedMarkets, err := repository.LoadActiveMarketRegistry(ctx)
	if err != nil {
		return err
	}
	if len(storedMarkets) > 0 {
		if err := marketRegistry.Restore(storedMarkets); err != nil {
			return fmt.Errorf("restore market registry: %w", err)
		}
	}
	s.registry = marketRegistry
	officialDomestic.SetExpectedMarketsProvider(func(exchangeName domain.Exchange) []domain.MarketPair {
		return marketRegistry.Universe().ForExchange(exchangeName)
	})
	officialAlignedDomestic.SetExpectedMarketsProvider(func(exchangeName domain.Exchange) []domain.MarketPair {
		return marketRegistry.Universe().ForExchange(exchangeName)
	})
	binanceTicker.SetExpectedMarketsProvider(func() []domain.MarketPair {
		return marketRegistry.Universe().ForExchange(domain.ExchangeBinance)
	})
	if !s.computeOnly {
		movementUpdates := officialAlignedDomestic.Updates()
		s.movementWait.Add(1)
		go func() {
			defer s.movementWait.Done()
			s.monitorOfficialMovements(ctx, movementUpdates)
		}()
	}
	var collectors *collector.Manager
	if s.postgresRepository != nil && !s.computeOnly {
		s.websocketJournal = newWebSocketFrameJournal(
			gatedWebSocketFrameStore{next: s.websocketFrameStore, gate: deployment},
			settings.QueueSize,
			s.logger,
		)
		s.orderBookLatest = newOrderBookLatestWriter(
			gatedOrderBookLatestStore{next: s.postgresRepository, gate: deployment},
			orderBookLatestFlushInterval,
			s.logger,
		)
		runtime.SetOrderBookSnapshotObserver(s.orderBookLatest.Submit)
		reconciler, reconcileErr := reconcile.NewMinuteService(
			gatedReconciliationStore{next: s.postgresRepository, gate: deployment},
			official,
			reconcile.ServiceConfig{
				MinimumClockSamples: reconcile.DefaultClockSamples,
				Tick:                250 * time.Millisecond,
				Markets: func() []domain.MarketPair {
					return marketRegistry.Universe().Markets()
				},
				ReadGaps: func(readCtx context.Context) ([]coverage.Gap, error) {
					stateCtx, cancel := context.WithTimeout(readCtx, time.Second)
					defer cancel()
					state, stateErr := runtime.CoverageState(stateCtx)
					if stateErr != nil {
						return nil, stateErr
					}
					return state.Gaps, nil
				},
				Readiness: func() map[domain.Exchange]bool {
					return s.exchangeRealtimeReadiness(time.Now())
				},
				// Historical automatic raw replay is disabled; realtime finalization remains active.
			},
		)
		if reconcileErr != nil {
			return reconcileErr
		}
		s.reconciler = reconciler
		runtime.SetAcceptedTradeObserver(func(trade aggregate.AcceptedTrade) error {
			s.whaleAlerts.Observe(trade, time.Now())
			_, observeErr := reconciler.ObserveAccepted(trade)
			return observeErr
		})
	} else {
		// Ephemeral desktop mode keeps only the bounded live owner state. Raw
		// frames, order-book snapshots, and closed-minute recovery are not copied
		// into SQLite and therefore cannot accidentally survive the process.
		runtime.SetAcceptedTradeObserver(func(trade aggregate.AcceptedTrade) error {
			s.whaleAlerts.Observe(trade, time.Now())
			return nil
		})
	}

	collectorEnabled := collector.DefaultEnabled()
	registryEnabled := append(append([]domain.Exchange(nil), collectorEnabled...), domain.ExchangeBinance)
	collectorOptions := collector.Options{
		StaleAfter: settings.OrderBookStale,
		Logger:     s.logger,
	}
	if s.websocketJournal != nil {
		collectorOptions.FrameSink = s.websocketJournal.Submit
		collectorOptions.FrameWaiter = s.websocketJournal.Wait
	}
	collectorOptions.ObservationLoss = func(lossCtx context.Context, loss collector.ObservationLoss) error {
		domestic := isDomesticRealtimeExchange(string(loss.Exchange))
		global := isGlobalRealtimeExchange(loss.Exchange)
		if !domestic && !global {
			return nil
		}
		if domestic && !observationLossAffectsKRW(loss) {
			return nil
		}
		start, second := observationLossBounds(loss)
		invalidationCtx, cancel := context.WithTimeout(context.WithoutCancel(lossCtx), time.Second)
		defer cancel()
		gapContext := coverage.GapContext{
			AffectedExchanges: []string{string(loss.Exchange)},
			WeightWindowSec:   10,
			ReasonCodes:       observationLossReasonCodes(loss),
		}
		if global {
			return runtime.QueueGlobalCoverageInvalidation(invalidationCtx, start, second, gapContext)
		}
		return runtime.QueueCoverageInvalidation(invalidationCtx, start, second, gapContext)
	}
	collectors = collector.NewManager(
		ctx,
		collector.WebSocketDialer{HTTPClient: httpClient},
		func(ctx context.Context, event domain.TradeEvent) error {
			if !analysisScopeMatchesExchange(event.Scope, event.Exchange) {
				return errors.New("non-domestic WebSocket trades are policy-disabled")
			}
			return runtime.SubmitTrade(ctx, event)
		},
		func(ctx context.Context, event domain.OrderBookEvent) (bool, error) {
			if !analysisScopeMatchesExchange(event.Scope, event.Exchange) {
				return false, nil
			}
			return runtime.SubmitOrderBook(ctx, event)
		},
		collectorEnabled,
		collectorOptions,
	)
	s.collectors = collectors
	if s.computeOnly {
		// Consumers restore only durable input. In particular, creating the
		// compatibility read views above does not start exchange connections.
		ingestionRestored := s.startIngestionConsumer(ctx, ingestionCursor)
		s.startBackground(ctx, func(ctx context.Context) { s.monitorIngestionMaintenance(ctx, ingestionRestored) })
		s.startBackground(ctx, func(ctx context.Context) { s.monitorOrderBookLatest(ctx, ingestionRestored) })
		s.startBackground(ctx, s.monitorDatabase)
		s.startBackground(ctx, s.monitorRecentTradeIDRetention)
		s.startBackground(ctx, s.monitorRollingTracker)
		s.startBackground(ctx, s.monitorRealtimeFlow)
		return nil
	}
	if s.reconciler != nil {
		if err := s.reconciler.Start(ctx); err != nil {
			return err
		}
	}
	coordinator := NewRegistryCoordinator(
		marketRegistry, collectors, repository, oracle, registryEnabled,
		settings.RegistryRefresh, s.logger,
	)
	s.coordinator = coordinator
	initialCtx, cancelInitial := context.WithTimeout(ctx, 30*time.Second)
	err = coordinator.Sync(initialCtx)
	cancelInitial()
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	if settings.StorageMode == config.StorageSQLiteMemory && settings.NoticeEnabled {
		noticeMonitor, monitorErr := notice.NewMonitor(
			official,
			repository,
			notice.DefaultSources(),
			settings.NoticePoll,
			func() map[string]struct{} { return noticeAssets(marketRegistry.Universe()) },
			s.logger,
		)
		if monitorErr != nil {
			return monitorErr
		}
		s.noticeMonitor = noticeMonitor
		if monitorErr := noticeMonitor.Start(ctx); monitorErr != nil {
			return monitorErr
		}
	} else if settings.StorageMode == config.StorageSQLiteMemory {
		s.logger.Info("portable notice collection disabled")
	}
	officialDomestic.Start(ctx)
	officialAlignedDomestic.Start(ctx)
	binanceTicker.Start(ctx)
	s.startBackground(ctx, coordinator.Run)
	s.updateObservation()
	s.startBackground(ctx, s.monitorObservation)
	s.startBackground(ctx, s.monitorDatabase)
	s.startBackground(ctx, s.monitorRecentTradeIDRetention)
	s.startBackground(ctx, s.monitorWebSocketRetention)
	s.startBackground(ctx, s.monitorRollingTracker)
	s.startBackground(ctx, s.monitorRealtimeFlow)
	return nil
}

func (s *Service) startBackground(ctx context.Context, run func(context.Context)) {
	s.backgroundWait.Add(1)
	go func() {
		defer s.backgroundWait.Done()
		run(ctx)
	}()
}

type startupHydrationStore interface {
	StreamBuckets(context.Context, domain.UnixSecond, func(aggregate.Bucket) error) error
	LoadHydrationState(context.Context, domain.UnixSecond, domain.UnixSecond) (postgres.State, error)
}

func hydrateRuntimeStreaming(
	ctx context.Context,
	store startupHydrationStore,
	runtime *Runtime,
	bucketSince domain.UnixSecond,
	recentSince domain.UnixSecond,
	now domain.UnixSecond,
) error {
	runtime.aggregator.Advance(now)
	if checkpoint, ok := store.(deploymentHydrationStore); ok {
		stream := checkpoint.StreamHydrationCheckpoint
		if cold, ok := store.(interface {
			StreamStartupHydrationCheckpoint(context.Context, domain.UnixSecond, domain.UnixSecond, func(aggregate.Bucket) error) (postgres.State, error)
		}); ok {
			stream = cold.StreamStartupHydrationCheckpoint
		}
		state, err := stream(ctx, bucketSince, recentSince, func(row aggregate.Bucket) error {
			return runtime.aggregator.RestoreBucket(row, now)
		})
		if err != nil {
			return fmt.Errorf("stream consistent aggregation checkpoint: %w", err)
		}
		if len(state.Buckets) != 0 {
			return errors.New("checkpoint unexpectedly materialized buckets")
		}
		return runtime.Hydrate(state, now)
	}
	if err := store.StreamBuckets(ctx, bucketSince, func(row aggregate.Bucket) error {
		if err := runtime.aggregator.RestoreBucket(row, now); err != nil {
			return fmt.Errorf("restore aggregation bucket: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("stream persisted aggregation buckets: %w", err)
	}
	state, err := store.LoadHydrationState(ctx, bucketSince, recentSince)
	if err != nil {
		return fmt.Errorf("load aggregation hydration state: %w", err)
	}
	if len(state.Buckets) != 0 {
		return errors.New("aggregation hydration state unexpectedly materialized buckets")
	}
	if err := runtime.Hydrate(state, now); err != nil {
		return err
	}
	return nil
}

func isGlobalRealtimeExchange(exchange domain.Exchange) bool {
	return domain.IsGlobalReferenceExchange(exchange)
}

func observationLossBounds(loss collector.ObservationLoss) (start, detected int64) {
	detected = loss.DetectedAt.Unix()
	start = detected
	if loss.EarliestEventTS > 0 {
		start = min(start, int64(loss.EarliestEventTS)/1_000)
	}
	return start, detected
}

func observationLossReasonCodes(loss collector.ObservationLoss) []string {
	reasons := []string{loss.Reason}
	missingMatches := loss.MissingTradeMatches
	if missingMatches == 0 && loss.MissingTradeEvents > 0 {
		missingMatches = uint64(loss.MissingTradeEvents)
	}
	if missingMatches > 0 {
		// Persist the magnitude in the existing reason-code JSON so it survives a
		// restart without requiring a PostgreSQL schema change.
		reasons = append(reasons, fmt.Sprintf("MISSING_TRADE_EVENTS:%d", missingMatches))
	}
	if loss.MissingTradeEvents > 0 && uint64(loss.MissingTradeEvents) != missingMatches {
		reasons = append(reasons, fmt.Sprintf("MISSING_TRADE_ROWS:%d", loss.MissingTradeEvents))
	}
	return reasons
}

func (s *Service) restoreRollingTracker(ctx context.Context) {
	raw, found, err := s.repository.ReadMetadata(ctx, rollingTrackerMetadataKey)
	if err != nil {
		s.logger.Warn("rolling tracker state could not be read", "error", err)
		return
	}
	if !found {
		return
	}
	var state marketanalysis.RollingTrackerState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		s.logger.Warn("rolling tracker state is invalid", "error", err)
		return
	}
	if s.rollingTracker.RestoreState(state, time.Now().UnixMilli()) {
		s.rollingPersistedAt = time.Now()
	}
}

func (s *Service) persistRollingTracker(ctx context.Context, force bool) {
	s.persistRollingTrackerGeneration(ctx, force, s.deploymentDerivedGeneration.Load())
}

func (s *Service) persistRollingTrackerGeneration(ctx context.Context, force bool, generation uint64) {
	s.deploymentDerivedMu.Lock()
	defer s.deploymentDerivedMu.Unlock()
	if !s.deploymentDerivedActive() || generation != s.deploymentDerivedGeneration.Load() {
		return
	}
	s.rollingPersistMu.Lock()
	defer s.rollingPersistMu.Unlock()
	if !force && time.Since(s.rollingPersistedAt) < rollingTrackerPersistEvery {
		return
	}
	if s.repository == nil || s.rollingTracker == nil {
		return
	}
	payload, err := json.Marshal(s.rollingTracker.ExportState())
	if err != nil {
		s.logger.Warn("rolling tracker state could not be encoded", "error", err)
		return
	}
	persistCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := s.repository.WriteMetadata(persistCtx, rollingTrackerMetadataKey, string(payload)); err != nil {
		s.logger.Warn("rolling tracker state could not be persisted", "error", err)
		return
	}
	s.rollingPersistedAt = time.Now()
}

func (s *Service) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() {
		s.running.Store(false)
		s.databaseReady.Store(false)
		s.invalidateOperationalSnapshot()
		s.cancel()
		s.backgroundWait.Wait()
		s.persistRollingTracker(ctx, true)
		if s.collectors != nil {
			s.collectors.Stop()
		}
		if s.websocketJournal != nil {
			if err := s.websocketJournal.Stop(ctx); err != nil && s.stopErr == nil {
				s.stopErr = err
			}
		}
		if s.reconciler != nil {
			if err := s.reconciler.Stop(ctx); err != nil && s.stopErr == nil {
				s.stopErr = err
			}
		}
		if s.runtime != nil {
			if err := s.runtime.Stop(ctx); err != nil && s.stopErr == nil {
				s.stopErr = err
			}
		}
		if s.orderBookLatest != nil {
			if err := s.orderBookLatest.Stop(ctx); err != nil && s.stopErr == nil {
				s.stopErr = err
			}
		}
		if s.officialDomestic != nil {
			s.officialDomestic.Stop()
		}
		if s.officialAlignedDomestic != nil {
			s.officialAlignedDomestic.Stop()
		}
		if s.binanceTicker != nil {
			s.binanceTicker.Stop()
		}
		s.movementWait.Wait()
		if s.budgets != nil {
			s.budgets.Close()
		}
		if s.deployment != nil {
			if err := s.deployment.close(ctx); err != nil && s.stopErr == nil {
				s.stopErr = err
			}
		}
		if s.repository != nil {
			s.deploymentMu.Lock()
			s.closePreparedCheckpoint()
			s.deploymentMu.Unlock()
			s.repository.Close()
		}
		if s.websocketFrameStore != nil {
			s.websocketFrameStore.Close()
		}
		s.databaseReady.Store(false)
	})
	return s.stopErr
}

func (s *Service) HealthState() HealthState {
	return cloneHealthState(s.operationalState(time.Now()).health)
}

func buildHealthState(
	running, databaseReady, consumerAlive, aggregationActive bool,
	registryHealth registry.Snapshot,
	collectorHealth map[domain.Exchange]collector.ExchangeHealth,
) HealthState {
	state := HealthState{Status: "starting", Running: running, DatabaseReady: databaseReady, Unhealthy: []string{}, ExcludedExchanges: []string{}}
	domesticReady := running && databaseReady && consumerAlive && !aggregationActive
	registryFresh := true
	for _, name := range domain.DomesticExchanges {
		registered := registryHealth.Exchanges[name]
		collected := collectorHealth[name]
		if registered.Error != "" {
			registryFresh = false
			if registered.MarketCount > 0 {
				state.RegistryFallbackActive = true
			}
		}
		if registered.MarketCount == 0 || !domesticKRWCollectorReady(name, collected) {
			state.Unhealthy = append(state.Unhealthy, string(name))
			domesticReady = false
			if registered.MarketCount > 0 && registered.Error != "" {
				state.ExcludedExchanges = append(state.ExcludedExchanges, string(name))
			}
		}
	}
	for _, name := range domain.GlobalExchanges {
		collected := collectorHealth[name]
		if !collected.Enabled {
			continue
		}
		registered := registryHealth.Exchanges[name]
		if registered.MarketCount == 0 || registered.Error != "" ||
			!collected.RealtimeReady || !collected.TradeStreamReady || collected.ActiveTradeParseErrorChunks > 0 {
			state.Unhealthy = append(state.Unhealthy, string(name))
		}
	}
	state.RealtimeReady = domesticReady
	if state.RealtimeReady && registryFresh {
		state.Status = "healthy"
	} else if running {
		state.Status = "degraded"
	}
	return state
}

func (s *Service) monitorDatabase(ctx context.Context) {
	ticker := time.NewTicker(databaseHealthInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.probeDatabase(ctx)
		}
	}
}

func (s *Service) monitorRecentTradeIDRetention(ctx context.Context) {
	s.pruneRecentTradeIDs(ctx, time.Now())
	ticker := time.NewTicker(recentTradeIDPruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.pruneRecentTradeIDs(ctx, now)
		}
	}
}

func (s *Service) pruneRecentTradeIDs(parent context.Context, now time.Time) {
	if s.recentTradeIDPruner == nil || now.IsZero() {
		return
	}
	ctx, cancel := context.WithTimeout(parent, recentTradeIDPruneTimeout)
	cutoff := domain.UnixSecond(now.Add(-recentTradeIDDatabaseRetention).Unix())
	deleted, err := s.recentTradeIDPruner.PruneRecentTradeIDs(ctx, cutoff, recentTradeIDPruneBatchSize)
	cancel()
	if err != nil {
		if parent.Err() != nil || errors.Is(err, ErrDeploymentNotActive) {
			return
		}
		s.retentionPruneFailures.Add(1)
		if s.logger != nil {
			s.logger.Warn("recent trade ID retention failed", "cutoff", int64(cutoff), "error", err)
		}
		return
	}
	s.retentionRowsPruned.Add(uint64(deleted))
	s.retentionLastSuccess.Store(now.Unix())
}

func (s *Service) monitorRollingTracker(ctx context.Context) {
	ticker := time.NewTicker(rollingTrackerObserveEvery)
	defer ticker.Stop()
	nextHistoryRefresh := time.Now().Add(time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if s.tickerTrend != nil && !now.Before(nextHistoryRefresh) && s.deploymentDerivedActive() {
				refreshCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				err := s.tickerTrend.Restore(refreshCtx)
				cancel()
				nextHistoryRefresh = now.Add(time.Minute)
				if err != nil && ctx.Err() == nil {
					s.logger.Warn("official hourly history refresh failed", "error", err)
				}
			}
			if err := s.sampleRollingTracker(ctx, now); err != nil && ctx.Err() == nil {
				s.logger.Warn("background rolling-share sample failed", "error", err)
			}
		}
	}
}

func (s *Service) sampleRollingTracker(ctx context.Context, now time.Time) error {
	if !s.deploymentDerivedActive() {
		return nil
	}
	generation := s.deploymentDerivedGeneration.Load()
	if s.runtime == nil || s.rollingTracker == nil {
		return ErrStopped
	}
	trendStart, trendEnd, trendReady := officialTickerTrendInterval(s.officialTickerTrendState())
	snapshot, err := s.sharedAnalysisRead(ctx, now.Unix()-1, trendStart, trendEnd, trendReady)
	if err != nil {
		return err
	}
	monitor := s.observeRollingMonitorGeneration(ctx, snapshot.rollingBase, generation)
	if monitor.DataQuality == nil {
		return errors.New("background rolling-share monitor has no data-quality state")
	}
	return nil
}

func (s *Service) monitorRealtimeFlow(ctx context.Context) {
	ticker := time.NewTicker(realtimeFlowSampleEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := s.sampleRealtimeFlow(ctx, now); err != nil && ctx.Err() == nil {
				s.logger.Warn("realtime WebSocket flow sample failed", "error", err)
			}
		}
	}
}

func (s *Service) sampleRealtimeFlow(ctx context.Context, now time.Time) error {
	s.realtimeSampleMu.Lock()
	defer s.realtimeSampleMu.Unlock()
	if !s.deploymentDerivedActive() {
		return nil
	}
	generation := s.deploymentDerivedGeneration.Load()
	if s.realtimeSampleGeneration != generation {
		s.lastRealtimeSecond, s.lastRealtimeBookSlot, s.lastRealtimeStoreSlot = 0, 0, 0
		s.realtimeSampleGeneration = generation
	}
	if s.runtime == nil || s.realtimeFlow == nil || s.repository == nil {
		return ErrStopped
	}
	endSecond, err := s.newestAppliedSecond(ctx, now)
	if err != nil {
		return err
	}
	if endSecond <= s.lastRealtimeSecond {
		return nil
	}
	startSecond := s.lastRealtimeSecond + 1
	if s.lastRealtimeSecond == 0 {
		startSecond = endSecond
	}
	// Bound catch-up work per wake while retaining the pending position. Every
	// available second is read from the runtime, including genuinely missing ones.
	endSecond = min(endSecond, startSecond+9)
	for second := startSecond; second <= endSecond; second++ {
		if err := s.sampleRealtimeFlowSecond(ctx, now, second, generation); err != nil {
			return err
		}
	}
	return nil
}

// newestAppliedSecond is the last closed second whose trades and health are
// applied. Journal replay can lag wall time without losing a received frame,
// so a per-second reading never finalizes a second ahead of the applied
// health watermark. A known transport gap clears LastObserved, but its
// durable health event still advances replay, so incomplete seconds are read
// instead of freezing the last good state until all transport recovers.
func (s *Service) newestAppliedSecond(ctx context.Context, now time.Time) (int64, error) {
	second := now.Unix() - 1
	if s.runtime.ingestion != nil {
		frontier, err := s.runtime.atomicReadIntervals(ctx, nil, nil)
		if err != nil {
			return 0, err
		}
		second = min(second, frontier.journalHealthSecond)
	}
	return second, nil
}

// Called under realtimeSampleMu; the cursor advances only after required writes.
func (s *Service) sampleRealtimeFlowSecond(ctx context.Context, now time.Time, endSecond int64, generation uint64) error {
	read, err := s.runtime.atomicReadIntervals(ctx, []aggregate.IntervalRequest{{
		Start: domain.UnixSecond(endSecond - 1), End: domain.UnixSecond(endSecond),
	}}, []coverageInterval{{start: endSecond, end: endSecond}})
	if err != nil {
		return err
	}
	if len(read.intervals) != 1 || len(read.coverage) != 1 || !read.intervals[0].OK {
		return errors.New("realtime WebSocket flow read is incomplete")
	}
	rows := analysisRows(filterDomesticKRW(read.intervals[0].Values))
	boundarySecond := endSecond + 1
	storeSlot := boundarySecond / int64(realtimeFlowPersistEvery/time.Second)
	books := []orderbook.Row(nil)
	if storeSlot != s.lastRealtimeBookSlot && s.collectors != nil && endSecond >= now.Unix()-2 {
		operational := s.operationalState(now)
		markets := make([]domain.MarketPair, 0)
		for _, market := range operational.universe.Markets() {
			if market.Scope == domain.ScopeDomestic && strings.EqualFold(strings.TrimSpace(market.Quote), "KRW") {
				markets = append(markets, market)
			}
		}
		activity := s.collectors.MarketActivitySnapshot(now)
		quiet := quietOrderBookMarkets(activity, s.runtime.OrderBookStaleAfterSec())
		bookSnapshot, bookErr := s.runtime.OrderBookSnapshotWithQuiet(
			ctx, markets, s.referenceOrderKRW, quiet, time.Time{},
		)
		if bookErr == nil {
			books = bookSnapshot.Rows
			if notices, noticeErr := s.NoticeSnapshot(ctx); noticeErr == nil {
				liquidity := marketanalysis.BuildLiquidityWithOptions(bookSnapshot.Rows, marketanalysis.LiquidityOptions{
					NotionalKRW: s.referenceOrderKRW, SlippageNotionalsKRW: slippageScenarioNotionalsKRW(),
					QuarantineBPS: s.priceGapQuarantineBPS, ContractIdentities: s.contractIdentities,
				})
				s.recordNoticeMicrostructure(notices, liquidity.Assets, bookSnapshot.AsOf.UnixMilli())
			} else if ctx.Err() == nil {
				s.logger.Debug("notice microstructure sample skipped", "error", noticeErr)
			}
			s.lastRealtimeBookSlot = storeSlot
		} else if ctx.Err() == nil {
			s.logger.Debug("realtime orderbook context unavailable", "error", bookErr)
		}
	}
	complete := read.coverage[0].WindowComplete && (s.runtime.ingestion != nil || s.operationalState(now).health.RealtimeReady)
	observation := buildRealtimeFlowObservation(boundarySecond*1_000, complete, rows, books)
	s.deploymentDerivedMu.Lock()
	defer s.deploymentDerivedMu.Unlock()
	if !s.deploymentDerivedActive() || generation != s.deploymentDerivedGeneration.Load() {
		return nil
	}
	state, err := s.realtimeFlow.Observe(observation)
	if err != nil {
		return err
	}
	if storeSlot == s.lastRealtimeStoreSlot {
		s.lastRealtimeSecond = endSecond
		return nil
	}
	persistCtx, persistCancel := context.WithTimeout(ctx, time.Second)
	defer persistCancel()
	summaryState, err := s.repository.SaveRealtimeFlowStateWithSummary(persistCtx, state)
	if err != nil {
		return err
	}
	s.initRealtimeSummaryState()
	s.realtimeSummaryRing.observe(summaryState)
	s.realtimePeriodSummaryMemo.invalidateFrom(summaryState.AsOfMS)
	s.lastRealtimeStoreSlot = storeSlot
	s.lastRealtimeSecond = endSecond
	return nil
}

func (s *Service) RealtimeFlowState() marketanalysis.RealtimeFlowState {
	if s.realtimeFlow == nil {
		return marketanalysis.NewRealtimeFlowAnalyzer(marketanalysis.RealtimeFlowConfig{}).State()
	}
	return s.realtimeFlow.State()
}

func (s *Service) WhaleAlertState() marketanalysis.WhaleAlertState {
	if s.whaleAlerts == nil {
		return marketanalysis.NewWhaleAlertDetector(marketanalysis.WhaleAlertConfig{}).State()
	}
	return s.whaleAlerts.State()
}

func (s *Service) RealtimeFlowHistory(ctx context.Context, filter marketanalysis.RealtimeFlowHistoryFilter) ([]marketanalysis.RealtimeFlowState, error) {
	if s.repository == nil {
		return nil, ErrStopped
	}
	s.realtimeHistoryMemoOnce.Do(func() { s.realtimeHistoryMemo = newRealtimeHistoryMemo() })
	return s.realtimeHistoryMemo.get(ctx, filter, func(queryCtx context.Context) ([]marketanalysis.RealtimeFlowState, error) {
		return s.repository.RealtimeFlowHistory(queryCtx, filter)
	})
}

func (s *Service) RealtimeFlowPeriodSummary(ctx context.Context, toMS, windowSec int64) (marketanalysis.RealtimeFlowPeriodSummary, error) {
	if s.repository == nil {
		return marketanalysis.RealtimeFlowPeriodSummary{}, ErrStopped
	}
	if windowSec != 86_400 {
		return marketanalysis.RealtimeFlowPeriodSummary{}, errors.New("realtime flow summary window must be 86400 seconds")
	}
	if toMS <= 0 {
		toMS = s.RealtimeFlowState().AsOfMS
	}
	toMS = toMS / 10_000 * 10_000
	if toMS <= 0 {
		return marketanalysis.RealtimeFlowPeriodSummary{}, errors.New("realtime flow summary timestamp is unavailable")
	}
	fromMS := toMS - windowSec*1_000
	s.initRealtimeSummaryState()
	return s.realtimePeriodSummaryMemo.get(ctx, toMS, func(loadCtx context.Context) (marketanalysis.RealtimeFlowPeriodSummary, error) {
		items, err := s.realtimeSummaryRing.history(loadCtx, fromMS, toMS, func(queryCtx context.Context) ([]marketanalysis.RealtimeFlowState, error) {
			return s.repository.RealtimeFlowSummaryHistory(queryCtx, fromMS, toMS)
		})
		if err != nil {
			return marketanalysis.RealtimeFlowPeriodSummary{}, err
		}
		return marketanalysis.SummarizeRealtimeFlowPeriod(items, fromMS, toMS), nil
	})
}

func (s *Service) probeDatabase(parent context.Context) {
	if s.repository == nil {
		s.databaseReady.Store(false)
		return
	}
	ctx, cancel := context.WithTimeout(parent, databaseHealthTimeout)
	err := s.repository.Ping(ctx)
	cancel()
	ready := err == nil
	if ready && s.websocketJournal != nil {
		if journalError := s.websocketJournal.Metrics().LastError; journalError != "" {
			ready = false
			err = fmt.Errorf("WebSocket journal persistence: %s", journalError)
		}
	}
	previous := s.databaseReady.Swap(ready)
	if ready != previous {
		s.invalidateOperationalSnapshot()
	}
	if ready == previous || parent.Err() != nil {
		return
	}
	if ready {
		s.logger.Info("storage readiness recovered", "storage_mode", s.storageMode)
	} else {
		s.logger.Error("storage readiness failed", "storage_mode", s.storageMode, "error", err)
	}
}

func (s *Service) monitorObservation(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.updateObservation()
		}
	}
}

func (s *Service) updateObservation() {
	if s.runtime == nil || s.registry == nil || s.collectors == nil {
		return
	}
	now := time.Now()
	operational := s.operationalState(now)
	registryHealth := operational.registry
	universe := operational.universe
	collectorHealth := operational.collectors
	complete := true
	invalidFrom := int64(0)
	detectedAt := now.Unix()
	context := coverage.GapContext{WeightWindowSec: 10}
	reasons := make(map[string]struct{})
	for _, name := range domain.DomesticExchanges {
		registered := registryHealth.Exchanges[name]
		collected := collectorHealth[name]
		exactSubscription := collectorKRWSubscriptionSetMatches(universe.ForExchange(name), collected)
		registryFallback := registered.MarketCount > 0 && registered.Error != ""
		affected := registered.MarketCount == 0 ||
			(!registryFallback && (!exactSubscription || !domesticKRWCollectorReady(name, collected)))
		if affected {
			complete = false
			context.AffectedExchanges = append(context.AffectedExchanges, string(name))
			candidate := observationKRWInvalidFrom(collected, detectedAt)
			if invalidFrom == 0 || candidate < invalidFrom {
				invalidFrom = candidate
			}
		}
		if registered.MarketCount == 0 {
			reasons["REGISTRY_UNAVAILABLE"] = struct{}{}
		} else if registered.Error != "" {
			reasons["REGISTRY_REFRESH_STALE"] = struct{}{}
		}
		// An exchange counted as observed (a quiet one too) gives no stale reason.
		observed := domesticKRWCollectorReady(name, collected)
		if !collected.KRWRealtimeReady && !observed {
			reasons["WEBSOCKET_STALE"] = struct{}{}
		}
		if !collected.KRWTradeStreamReady && !observed {
			reasons["TRADE_STREAM_NOT_READY"] = struct{}{}
		}
		if !exactSubscription {
			reasons["REGISTRY_SUBSCRIPTION_SET_MISMATCH"] = struct{}{}
		}
		if collected.KRWActiveTradeParseErrorChunks > 0 {
			reasons["TRADE_PARSE_ERROR"] = struct{}{}
		}
		if collected.KRWActiveSubscriptionErrorChunks > 0 {
			reasons["SUBSCRIPTION_ERROR"] = struct{}{}
		}
	}
	for reason := range reasons {
		context.ReasonCodes = append(context.ReasonCodes, reason)
	}
	s.runtime.SetObservationFrom(complete, invalidFrom, context)

	globalComplete := true
	globalEnabled := 0
	globalInvalidFrom := int64(0)
	globalContext := coverage.GapContext{WeightWindowSec: 10}
	globalReasons := make(map[string]struct{})
	for _, name := range domain.GlobalReferenceExchanges {
		registered := registryHealth.Exchanges[name]
		collected := collectorHealth[name]
		if !collected.Enabled {
			continue
		}
		globalEnabled++
		exactSubscription := registered.MarketCount > 0 &&
			registered.MarketCount == collected.MarketCount &&
			registered.MarketCount == collected.SubscribedMarketCount &&
			registered.MarketCount == collected.ConnectedMarketCount &&
			collectorSubscriptionSetMatches(universe.ForExchange(name), collected)
		affected := registered.Error != "" || !exactSubscription ||
			!collected.RealtimeReady || !collected.TradeStreamReady ||
			collected.ActiveTradeParseErrorChunks > 0 || collected.ActiveSubscriptionErrorChunks > 0
		if affected {
			globalComplete = false
			globalContext.AffectedExchanges = append(globalContext.AffectedExchanges, string(name))
			candidate := observationInvalidFrom(collected, detectedAt)
			if globalInvalidFrom == 0 || candidate < globalInvalidFrom {
				globalInvalidFrom = candidate
			}
		}
		if registered.MarketCount == 0 || registered.Error != "" {
			globalReasons["REGISTRY_UNAVAILABLE"] = struct{}{}
		}
		if !exactSubscription {
			globalReasons["REGISTRY_SUBSCRIPTION_SET_MISMATCH"] = struct{}{}
		}
		if !collected.RealtimeReady {
			globalReasons["WEBSOCKET_STALE"] = struct{}{}
		}
		if !collected.TradeStreamReady {
			globalReasons["TRADE_STREAM_NOT_READY"] = struct{}{}
		}
		if collected.ActiveTradeParseErrorChunks > 0 {
			globalReasons["TRADE_PARSE_ERROR"] = struct{}{}
		}
		if collected.ActiveSubscriptionErrorChunks > 0 {
			globalReasons["SUBSCRIPTION_ERROR"] = struct{}{}
		}
	}
	if globalEnabled == 0 {
		globalComplete = false
		globalInvalidFrom = detectedAt
		globalReasons["GLOBAL_WEBSOCKET_POLICY_DISABLED"] = struct{}{}
	}
	for reason := range globalReasons {
		globalContext.ReasonCodes = append(globalContext.ReasonCodes, reason)
	}
	s.runtime.SetGlobalObservationFrom(globalComplete, globalInvalidFrom, globalContext)
}

func collectorSubscriptionSetMatches(markets []domain.MarketPair, health collector.ExchangeHealth) bool {
	if len(markets) == 0 || len(health.SubscribedMarketSymbols) != len(markets) {
		return false
	}
	expected := make(map[string]struct{}, len(markets))
	for _, market := range markets {
		symbol := strings.ToUpper(strings.TrimSpace(market.Symbol))
		if symbol == "" {
			return false
		}
		expected[symbol] = struct{}{}
	}
	if len(expected) != len(markets) {
		return false
	}
	for _, symbol := range health.SubscribedMarketSymbols {
		if _, ok := expected[strings.ToUpper(strings.TrimSpace(symbol))]; !ok {
			return false
		}
	}
	return true
}

func observationInvalidFrom(health collector.ExchangeHealth, detectedAt int64) int64 {
	// A fresh order book, control frame, or pong cannot prove that the trade
	// channel remained observable. Once trade freshness fails, invalidate from
	// the oldest chunk's last received trade second. That whole second is
	// conservative because the channel may have stalled immediately afterwards.
	if health.OldestChunkTradeTS > 0 {
		return min(detectedAt, int64(health.OldestChunkTradeTS))
	}
	if health.OldestChunkMessageTS > 0 {
		return min(detectedAt, int64(health.OldestChunkMessageTS))
	}
	return detectedAt
}

func officialTickerRegistryMismatches(summary officialdata.DomesticTickerSummary, universe domain.MarketUniverse) []domain.Exchange {
	if summary.AsOf <= 0 {
		return nil
	}
	result := make([]domain.Exchange, 0)
	for _, exchange := range domain.DomesticExchanges {
		registeredMarkets := make(map[string]struct{})
		registeredCount := 0
		invalidRegistryIdentity := false
		for _, market := range universe.ForExchange(exchange) {
			if market.Scope == domain.ScopeDomestic && market.Quote == "KRW" {
				registeredCount++
				marketID := officialdata.CanonicalKRWMarketID(market.Base)
				if marketID == "" {
					invalidRegistryIdentity = true
					continue
				}
				if _, duplicate := registeredMarkets[marketID]; duplicate {
					invalidRegistryIdentity = true
				}
				registeredMarkets[marketID] = struct{}{}
			}
		}
		ticker := summary.Exchanges[exchange]
		if invalidRegistryIdentity || registeredCount != len(registeredMarkets) ||
			ticker.MarketCount != len(ticker.Markets) ||
			!sameMarketSet(registeredMarkets, ticker.Markets) {
			result = append(result, exchange)
		}
	}
	return result
}

func sameMarketSet(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for marketID := range left {
		if _, exists := right[marketID]; !exists {
			return false
		}
	}
	return true
}

func (s *Service) Runtime() *Runtime { return s.runtime }

func (s *Service) Registry() *registry.Registry { return s.registry }

func (s *Service) Collectors() *collector.Manager { return s.collectors }

func (s *Service) Reconciler() *reconcile.MinuteService { return s.reconciler }

func (s *Service) OfficialDomestic24h() *officialdata.TickerService { return s.officialDomestic }

func (s *Service) BinanceTicker() *officialdata.BinanceTickerService { return s.binanceTicker }

func (s *Service) cleanupFailedStart() {
	s.databaseReady.Store(false)
	s.backgroundWait.Wait()
	if s.collectors != nil {
		s.collectors.Stop()
	}
	if s.websocketJournal != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.websocketJournal.Stop(ctx)
		cancel()
	}
	if s.reconciler != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.reconciler.Stop(ctx)
		cancel()
	}
	if s.officialDomestic != nil {
		s.officialDomestic.Stop()
	}
	if s.officialAlignedDomestic != nil {
		s.officialAlignedDomestic.Stop()
	}
	if s.binanceTicker != nil {
		s.binanceTicker.Stop()
	}
	s.movementWait.Wait()
	if s.runtime != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.runtime.Stop(ctx)
		cancel()
	}
	if s.orderBookLatest != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.orderBookLatest.Stop(ctx)
		cancel()
	}
	if s.budgets != nil {
		s.budgets.Close()
	}
	if s.deployment != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.deployment.close(ctx)
		cancel()
	}
	if s.repository != nil {
		s.repository.Close()
	}
	if s.websocketFrameStore != nil {
		s.websocketFrameStore.Close()
	}
	s.databaseReady.Store(false)
}

func sharedHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		MaxConnsPerHost:       40,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	return &http.Client{Transport: transport}
}
