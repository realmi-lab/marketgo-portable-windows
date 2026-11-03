package reconcile

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
	"market-share-go/services/collector/official"
	"market-share-go/services/collector/restbudget"
)

type ReconciliationStore interface {
	SaveWSMinute(context.Context, WSTradeMinute) (bool, error)
	SaveMinuteSeals(context.Context, []MinuteSeal) error
	GetWSMinute(context.Context, domain.Exchange, string, domain.UnixSecond) (*WSTradeMinute, error)
	SaveOfficialCandle(context.Context, OfficialMinuteCandle) (bool, error)
	ReconcileMinute(context.Context, domain.Exchange, string, domain.UnixSecond, int64, time.Time) (Result, error)
	FinalizeExchangeMinute(context.Context, domain.Exchange, domain.UnixSecond, int64, time.Time) (Summary, error)
	LatestReconciliationSummary(context.Context) (Summary, error)
}

type JSONClient interface {
	GetJSON(context.Context, restbudget.Request, any) error
}

type MarketsProvider func() []domain.MarketPair
type GapsProvider func() []coverage.Gap
type GapStateProvider func(context.Context) ([]coverage.Gap, error)
type ReadinessProvider func() map[domain.Exchange]bool

type ServiceConfig struct {
	MinimumClockSamples int
	Tick                time.Duration
	Markets             MarketsProvider
	Gaps                GapsProvider
	// ReadGaps is preferred for fallible runtime reads. Gaps is the legacy
	// in-memory provider; a nil result there means a successful empty snapshot.
	ReadGaps  GapStateProvider
	Readiness ReadinessProvider
	RawReplay RawReplayFunc
	Now       func() time.Time
}

type ServiceMetrics struct {
	ObservedTrades            int64 `json:"observed_trades"`
	IgnoredTrades             int64 `json:"ignored_trades"`
	DuplicateTrades           int64 `json:"duplicate_trades"`
	ClosedWSMinutes           int64 `json:"closed_ws_minutes"`
	WSMinuteConflicts         int64 `json:"ws_minute_conflicts"`
	WSMinuteSaveFailures      int64 `json:"ws_minute_save_failures"`
	WSMinuteSaveRetries       int64 `json:"ws_minute_save_retries"`
	MinuteSealsSaved          int64 `json:"minute_seals_saved"`
	MinuteSealSaveFailures    int64 `json:"minute_seal_save_failures"`
	ObservedZeroMarkets       int64 `json:"observed_zero_markets"`
	UnknownMarkets            int64 `json:"unknown_markets"`
	RawReplayCandidates       int64 `json:"raw_replay_candidates"`
	RawReplaySuppressed       int64 `json:"raw_replay_suppressed"`
	RawReplayCompleted        int64 `json:"raw_replay_completed"`
	RawReplayFailures         int64 `json:"raw_replay_failures"`
	RawReplayExhausted        int64 `json:"raw_replay_exhausted"`
	RawReplayFramesRead       int64 `json:"raw_replay_frames_read"`
	RawReplayTradesSubmitted  int64 `json:"raw_replay_trades_submitted"`
	PendingWSCollisions       int64 `json:"pending_ws_row_collisions"`
	PreObservationTargets     int64 `json:"pre_observation_targets_skipped"`
	JobsScheduled             int64 `json:"jobs_scheduled"`
	QuietMarketsSampled       int64 `json:"quiet_markets_sampled"`
	QuietMarketsSuppressed    int64 `json:"quiet_markets_suppressed"`
	JobsCompleted             int64 `json:"jobs_completed"`
	JobsDroppedStale          int64 `json:"jobs_dropped_stale"`
	RequestsStarted           int64 `json:"requests_started"`
	RequestsSucceeded         int64 `json:"requests_succeeded"`
	OfficialCandlesSaved      int64 `json:"official_candles_saved"`
	OfficialCandlesIdempotent int64 `json:"official_candles_idempotent"`
	OfficialCandlesMissing    int64 `json:"official_candles_missing"`
	ImmutableCandleConflicts  int64 `json:"immutable_candle_conflicts"`
	ReconciledMinutes         int64 `json:"reconciled_minutes"`
	UnconfirmedAbsences       int64 `json:"unconfirmed_absences"`
	RetriesScheduled          int64 `json:"retries_scheduled"`
	RetriesSuppressed         int64 `json:"retries_suppressed_capacity"`
	RetriesExecuted           int64 `json:"retries_executed"`
	FinalizationsSucceeded    int64 `json:"finalizations_succeeded"`
	FinalizationFailures      int64 `json:"finalization_failures"`
	FinalizationRetries       int64 `json:"finalization_retries"`
	FinalizationsDropped      int64 `json:"finalizations_dropped"`
	RequestErrors             int64 `json:"request_errors"`
	ParseErrors               int64 `json:"parse_errors"`
	StorageErrors             int64 `json:"storage_errors"`
}

type ServiceSnapshot struct {
	Running                bool                  `json:"running"`
	ClockReady             bool                  `json:"clock_ready"`
	EstimatedClockSkewMS   *float64              `json:"estimated_clock_skew_ms"`
	OpenWSMinutes          int                   `json:"open_ws_minutes"`
	PendingClosedWSMinutes int                   `json:"pending_closed_ws_minutes"`
	PendingFinalizations   int                   `json:"pending_finalizations"`
	QueuedJobs             int                   `json:"queued_jobs"`
	ActiveWorkerCount      int                   `json:"active_worker_count"`
	LastScheduledMinute    *int64                `json:"last_scheduled_target_ms"`
	LatestSummary          Summary               `json:"latest_summary"`
	LatestMinuteSeals      map[string]MinuteSeal `json:"latest_minute_seals"`
	LastError              string                `json:"last_error"`
	Metrics                ServiceMetrics        `json:"metrics"`
}

type pendingMinute struct {
	row      WSTradeMinute
	attempts int
}

type finalizationKey struct {
	exchange domain.Exchange
	minute   domain.UnixSecond
}

type minuteSealRevision struct {
	seal        MinuteSeal
	previous    MinuteSeal
	hasPrevious bool
}

type pendingFinalization struct {
	checkedAt           time.Time
	firstQueued         time.Time
	attempts            int
	sourceAttempt       int
	expectedMarketCount int64
	queueEntry          *finalizationQueueEntry
}

const (
	closedVolumeRetainedMinutes = 3
	sealAuditRetainedMinutes    = 3
	rawReplayDeferredCapacity   = 32
	rawReplayMaxAttempts        = 3
	rawReplayRetryBaseDelay     = time.Second
	finalizationRetryDelay      = time.Second
	finalizationMaxRetryDelay   = 30 * time.Second
	finalizationMaxAge          = 5 * time.Minute
	finalizationMaxAttempts     = 8
	finalizationMaxPerWake      = 4
	finalizationMaxPending      = 16
)

type rawReplayGeneration struct {
	candidate RawReplayCandidate
	failures  int
	retryAt   time.Time
}

type rawReplayPending struct {
	generations map[string]rawReplayGeneration
}

type rawReplayDeferred struct {
	minute      domain.UnixSecond
	generations map[string]rawReplayGeneration
}

type MinuteService struct {
	store     ReconciliationStore
	client    JSONClient
	config    ServiceConfig
	scheduler *Scheduler
	runOnceMu sync.Mutex
	// Protect gap snapshot capture through seal publication from older workers.
	// Candle HTTP and accepted WebSocket trades never hold this mutex.
	sealEvidenceMu sync.Mutex

	mu                 sync.Mutex
	started            bool
	running            bool
	cancel             context.CancelFunc
	wait               sync.WaitGroup
	signals            map[domain.Exchange]chan struct{}
	pending            map[string]pendingMinute
	pendingOrder       []string
	closedVolume       map[volumeIdentity]float64
	finalizations      map[finalizationKey]*pendingFinalization
	finalizationQueues map[domain.Exchange]*finalizationQueue
	lastScheduled      *int64
	latest             Summary
	latestSeals        map[domain.Exchange]MinuteSeal
	recentSeals        map[finalizationKey]MinuteSeal
	recoveryQueue      chan RawReplayCandidate
	recoveryPending    map[string]rawReplayPending
	recoveryDeferred   map[string]rawReplayDeferred
	recoverySucceeded  map[string]domain.UnixSecond
	recoveryExhausted  map[string]domain.UnixSecond
	recoveryRetryDelay time.Duration
	metrics            ServiceMetrics
	lastError          string
}

func NewMinuteService(store ReconciliationStore, client JSONClient, config ServiceConfig) (*MinuteService, error) {
	if store == nil || client == nil || config.Markets == nil {
		return nil, errors.New("reconciliation store, REST client, and markets provider are required")
	}
	if config.Tick <= 0 {
		config.Tick = 250 * time.Millisecond
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	signals := make(map[domain.Exchange]chan struct{}, len(domain.DomesticExchanges))
	finalizationQueues := make(map[domain.Exchange]*finalizationQueue, len(domain.DomesticExchanges))
	for _, exchange := range domain.DomesticExchanges {
		signals[exchange] = make(chan struct{}, 1)
		queue := make(finalizationQueue, 0)
		finalizationQueues[exchange] = &queue
	}
	return &MinuteService{
		store: store, client: client, config: config,
		scheduler: NewScheduler(config.MinimumClockSamples, config.Readiness != nil),
		signals:   signals, pending: make(map[string]pendingMinute),
		closedVolume:       make(map[volumeIdentity]float64),
		finalizations:      make(map[finalizationKey]*pendingFinalization),
		finalizationQueues: finalizationQueues,
		latest:             Summary{Status: StatusCandleUnavailable, Exchanges: map[string]ExchangeSummary{}},
		latestSeals:        make(map[domain.Exchange]MinuteSeal, len(domain.DomesticExchanges)),
		recentSeals:        make(map[finalizationKey]MinuteSeal, len(domain.DomesticExchanges)*sealAuditRetainedMinutes),
		recoveryQueue:      make(chan RawReplayCandidate, RawReplayQueueCapacity),
		recoveryPending:    make(map[string]rawReplayPending, RawReplayQueueCapacity+1),
		recoveryDeferred:   make(map[string]rawReplayDeferred, len(domain.DomesticExchanges)*sealAuditRetainedMinutes),
		recoverySucceeded:  make(map[string]domain.UnixSecond, len(domain.DomesticExchanges)*sealAuditRetainedMinutes),
		recoveryExhausted:  make(map[string]domain.UnixSecond, len(domain.DomesticExchanges)*sealAuditRetainedMinutes),
		recoveryRetryDelay: rawReplayRetryBaseDelay,
	}, nil
}

func (s *MinuteService) Start(parent context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel, s.started, s.running = cancel, true, true
	s.mu.Unlock()
	if summary, err := s.store.LatestReconciliationSummary(ctx); err == nil {
		s.mu.Lock()
		s.latest = summary
		for name, seal := range summary.MinuteSeals {
			s.latestSeals[domain.Exchange(name)] = seal
			s.recentSeals[finalizationKey{exchange: seal.Exchange, minute: seal.MinuteStart}] = seal
			s.metrics.ObservedZeroMarkets += seal.ObservedZeroCount
			s.metrics.UnknownMarkets += seal.UnknownMarketCount
		}
		s.mu.Unlock()
	}
	for _, exchange := range domain.DomesticExchanges {
		s.wait.Add(1)
		go s.worker(ctx, exchange)
	}
	if s.config.RawReplay != nil {
		s.wait.Add(1)
		go s.rawReplayWorker(ctx)
	}
	s.wait.Add(1)
	go s.run(ctx)
	return nil
}

func (s *MinuteService) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	cancel := s.cancel
	s.mu.Unlock()
	cancel()
	done := make(chan struct{})
	go func() { s.wait.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := s.persistPending(ctx); err != nil {
		return err
	}
	for _, exchange := range domain.DomesticExchanges {
		s.flushPendingFinalizations(ctx, exchange, true)
	}
	s.mu.Lock()
	s.started = false
	s.running = false
	s.mu.Unlock()
	return nil
}

func (s *MinuteService) ObserveAccepted(trade aggregate.AcceptedTrade) (bool, error) {
	accepted, err := s.scheduler.ObserveAccepted(trade)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		return false, err
	}
	if accepted {
		s.metrics.ObservedTrades++
	} else {
		s.metrics.IgnoredTrades++
	}
	return accepted, nil
}

func (s *MinuteService) RunOnce(ctx context.Context) error {
	s.runOnceMu.Lock()
	defer s.runOnceMu.Unlock()
	if !s.scheduler.ClockReady() {
		return nil
	}
	s.sealEvidenceMu.Lock()
	defer s.sealEvidenceMu.Unlock()
	nowMS := s.scheduler.EstimatedServerNowMS(s.config.Now().UnixMilli())
	s.refreshReadiness(nowMS)
	gaps, err := s.gaps(ctx)
	if err != nil {
		return err
	}
	if err := s.flushClosed(ctx, nowMS, gaps); err != nil {
		return err
	}
	if nowMS%MinuteMS < CloseGraceMS {
		return nil
	}
	target := nowMS/MinuteMS*MinuteMS - MinuteMS
	s.mu.Lock()
	needs := target > 0 && (s.lastScheduled == nil || target > *s.lastScheduled)
	pending := s.hasPendingMinuteLocked(target / 1000)
	s.mu.Unlock()
	if !needs || pending {
		return nil
	}
	markets := s.config.Markets()
	s.mu.Lock()
	s.pruneClosedVolumesLocked(domain.UnixSecond(target / 1000))
	volumes := copyVolumes(s.closedVolume)
	s.mu.Unlock()
	if err := s.auditRecentSealsLocked(ctx, target, markets, gaps, volumes); err != nil {
		return err
	}
	result := s.scheduler.ScheduleTarget(target, markets, gaps, volumes)
	revisions := make([]minuteSealRevision, 0, len(result.Seals))
	sealedAt := s.config.Now().UTC()
	for _, seal := range result.Seals {
		seal.SealedAt = sealedAt
		seal.UpdatedAt = sealedAt
		revisions = append(revisions, minuteSealRevision{seal: seal})
	}
	if err := s.persistMinuteSealRevisions(ctx, revisions); err != nil {
		return err
	}
	for _, candidate := range result.RecoveryCandidates {
		s.enqueueRawReplay(candidate)
	}
	s.pruneRecentSeals(domain.UnixSecond(target / 1000))
	s.mu.Lock()
	if result.SkippedAll {
		s.metrics.PreObservationTargets++
	}
	s.metrics.JobsScheduled += int64(len(result.Jobs))
	s.metrics.QuietMarketsSampled += int64(result.SampledQuietMarkets)
	s.metrics.QuietMarketsSuppressed += int64(result.SuppressedQuietMarkets)
	s.lastScheduled = int64PointerService(target)
	s.mu.Unlock()
	for _, job := range result.Jobs {
		s.signal(job.Exchange)
	}
	return nil
}

func (s *MinuteService) auditRecentSeals(ctx context.Context, targetStartMS int64, markets []domain.MarketPair, gaps []coverage.Gap, volumes map[volumeIdentity]float64) error {
	s.sealEvidenceMu.Lock()
	defer s.sealEvidenceMu.Unlock()
	return s.auditRecentSealsLocked(ctx, targetStartMS, markets, gaps, volumes)
}

// Callers must hold sealEvidenceMu before capturing their gaps and volumes.
func (s *MinuteService) auditRecentSealsLocked(ctx context.Context, targetStartMS int64, markets []domain.MarketPair, gaps []coverage.Gap, volumes map[volumeIdentity]float64) error {
	revisions := make([]minuteSealRevision, 0, len(domain.DomesticExchanges)*(sealAuditRetainedMinutes-1))
	auditedSeals := make([]MinuteSeal, 0, cap(revisions))
	candidates := make([]RawReplayCandidate, 0)
	for age := int64(1); age < sealAuditRetainedMinutes; age++ {
		auditTarget := targetStartMS - age*MinuteMS
		if auditTarget <= 0 {
			continue
		}
		seals, previewCandidates := s.scheduler.PreviewTarget(auditTarget, markets, gaps, volumes)
		candidates = append(candidates, previewCandidates...)
		for _, seal := range seals {
			key := finalizationKey{exchange: seal.Exchange, minute: seal.MinuteStart}
			s.mu.Lock()
			previous, exists := s.recentSeals[key]
			s.mu.Unlock()
			if !exists || !sameMinuteSealRegistry(previous, seal) {
				continue
			}
			// A retry may already have persisted this gap for the exchange.
			// Its unchanged seal must not suppress other affected markets.
			auditedSeals = append(auditedSeals, seal)
			if sameMinuteSealEvidence(previous, seal) {
				continue
			}
			seal.SealedAt = previous.SealedAt
			seal.UpdatedAt = s.config.Now().UTC()
			revisions = append(revisions, minuteSealRevision{seal: seal, previous: previous, hasPrevious: true})
		}
	}
	if err := s.persistMinuteSealRevisions(ctx, revisions); err != nil {
		return err
	}
	// A gap discovered after the initial close can affect quiet, unsampled
	// markets as well as partial WS minutes. Request their smallest exact
	// quote candle after the historical registry and seal have been rechecked.
	skewMS := 0.0
	if skew := s.scheduler.EstimatedClockSkewMS(); skew != nil {
		skewMS = *skew
	}
	for _, seal := range auditedSeals {
		if seal.GapSeconds <= 0 || (seal.Exchange != domain.ExchangeUpbit && seal.Exchange != domain.ExchangeBithumb && seal.Exchange != domain.ExchangeCoinone) {
			continue
		}
		for _, market := range markets {
			if market.Exchange != seal.Exchange || market.Scope != domain.ScopeDomestic || market.Quote != "KRW" {
				continue
			}
			gap := KnownGapSeconds(market.Exchange, market.Symbol, seal.MinuteStart, gaps, skewMS)
			if gap <= 0 {
				continue
			}
			job := CandleJob{
				Exchange: market.Exchange, Market: market, TargetStartMS: int64(seal.MinuteStart) * 1000,
				DueServerMS:         int64(seal.MinuteStart)*1000 + MinuteMS + CloseGraceMS,
				ExpectedMarketCount: seal.ScheduledMarketCount, KnownGapSeconds: gap,
				ObservedVolumeKRW: volumes[volumeKey(market.Exchange, market.Symbol, seal.MinuteStart)],
			}
			if s.scheduler.ScheduleGapRepair(job) {
				s.mu.Lock()
				s.metrics.JobsScheduled++
				s.mu.Unlock()
				s.signal(market.Exchange)
			}
		}
	}
	for _, candidate := range candidates {
		s.enqueueRawReplay(candidate)
	}
	return nil
}

func sameMinuteSealRegistry(left, right MinuteSeal) bool {
	return left.RegistryMarketCount == right.RegistryMarketCount &&
		left.RegistryFingerprint == right.RegistryFingerprint
}

func (s *MinuteService) persistMinuteSealRevisions(ctx context.Context, revisions []minuteSealRevision) error {
	if len(revisions) == 0 {
		return nil
	}
	seals := make([]MinuteSeal, len(revisions))
	for index := range revisions {
		previous, next := revisions[index].previous, revisions[index].seal
		if revisions[index].hasPrevious && previous.GapSeconds > 0 && next.Status == SealComplete {
			err := fmt.Errorf("minute %s/%d lost known gap evidence; automatic confirmation is forbidden", next.Exchange, next.MinuteStart)
			s.recordError(err)
			return err
		}
		seals[index] = revisions[index].seal
	}
	if err := s.store.SaveMinuteSeals(ctx, seals); err != nil {
		s.mu.Lock()
		s.metrics.MinuteSealSaveFailures += int64(len(seals))
		s.metrics.StorageErrors++
		s.mu.Unlock()
		s.recordError(err)
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, revision := range revisions {
		seal := revision.seal
		key := finalizationKey{exchange: seal.Exchange, minute: seal.MinuteStart}
		s.recentSeals[key] = seal
		latest, exists := s.latestSeals[seal.Exchange]
		if !exists || seal.MinuteStart >= latest.MinuteStart {
			s.latestSeals[seal.Exchange] = seal
		}
		s.metrics.MinuteSealsSaved++
		if revision.hasPrevious {
			s.metrics.ObservedZeroMarkets += seal.ObservedZeroCount - revision.previous.ObservedZeroCount
			s.metrics.UnknownMarkets += seal.UnknownMarketCount - revision.previous.UnknownMarketCount
		} else {
			s.metrics.ObservedZeroMarkets += seal.ObservedZeroCount
			s.metrics.UnknownMarkets += seal.UnknownMarketCount
		}
	}
	return nil
}

func sameMinuteSealEvidence(left, right MinuteSeal) bool {
	return left.Exchange == right.Exchange && left.MinuteStart == right.MinuteStart &&
		left.Status == right.Status && left.CoverageSeconds == right.CoverageSeconds &&
		left.GapSeconds == right.GapSeconds && left.RegistryMarketCount == right.RegistryMarketCount &&
		left.ObservedMarketCount == right.ObservedMarketCount && left.ObservedZeroCount == right.ObservedZeroCount &&
		left.UnknownMarketCount == right.UnknownMarketCount && left.GapMarketCount == right.GapMarketCount &&
		left.ScheduledMarketCount == right.ScheduledMarketCount && left.RegistryFingerprint == right.RegistryFingerprint
}

func (s *MinuteService) pruneRecentSeals(newest domain.UnixSecond) {
	cutoff := newest - domain.UnixSecond((sealAuditRetainedMinutes-1)*MinuteSeconds)
	s.mu.Lock()
	for key := range s.recentSeals {
		if key.minute < cutoff {
			delete(s.recentSeals, key)
		}
	}
	for generation, minute := range s.recoverySucceeded {
		if minute < cutoff {
			delete(s.recoverySucceeded, generation)
		}
	}
	for generation, minute := range s.recoveryExhausted {
		if minute < cutoff {
			delete(s.recoveryExhausted, generation)
		}
	}
	for slot, deferred := range s.recoveryDeferred {
		if deferred.minute < cutoff {
			if _, pending := s.recoveryPending[slot]; !pending {
				delete(s.recoveryDeferred, slot)
			}
		}
	}
	s.mu.Unlock()
}

func (s *MinuteService) enqueueRawReplay(candidate RawReplayCandidate) {
	if s.config.RawReplay == nil {
		return
	}
	if err := candidate.Normalize(); err != nil {
		s.recordError(err)
		return
	}
	slot := candidate.SlotKey()
	generation := candidate.GenerationKey()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, succeeded := s.recoverySucceeded[generation]; succeeded {
		return
	}
	if _, exhausted := s.recoveryExhausted[generation]; exhausted {
		return
	}
	if pending, exists := s.recoveryPending[slot]; exists {
		if _, duplicate := pending.generations[generation]; duplicate {
			return
		}
		if deferred, waiting := s.recoveryDeferred[slot]; waiting {
			if _, duplicate := deferred.generations[generation]; duplicate {
				return
			}
		}
		if !s.deferRawReplayGenerationLocked(slot, generation, rawReplayGeneration{candidate: candidate}) {
			s.metrics.RawReplaySuppressed++
		}
		return
	}
	if deferred, exists := s.recoveryDeferred[slot]; exists {
		if existing, sameGeneration := deferred.generations[generation]; sameGeneration {
			now := time.Now()
			if rawReplayGenerationReady(existing, now) && !s.drainRawReplaySlotLocked(slot, now) {
				s.metrics.RawReplaySuppressed++
			}
			return
		}
	}
	if !s.deferRawReplayGenerationLocked(slot, generation, rawReplayGeneration{candidate: candidate}) {
		s.metrics.RawReplaySuppressed++
		return
	}
	if !s.drainRawReplaySlotLocked(slot, time.Now()) {
		s.metrics.RawReplaySuppressed++
	}
}

func (s *MinuteService) rawReplayWorker(ctx context.Context) {
	defer s.wait.Done()
	retryTimer := time.NewTimer(time.Hour)
	stopAndDrainTimer(retryTimer)
	defer retryTimer.Stop()
	for {
		stopAndDrainTimer(retryTimer)
		s.mu.Lock()
		retryDelay, retryScheduled := s.nextRawReplayRetryLocked(time.Now())
		s.mu.Unlock()
		var retry <-chan time.Time
		if retryScheduled {
			retryTimer.Reset(retryDelay)
			retry = retryTimer.C
		}
		select {
		case <-ctx.Done():
			return
		case <-retry:
			s.mu.Lock()
			s.drainRawReplayDeferredLocked(time.Now())
			s.mu.Unlock()
		case candidate := <-s.recoveryQueue:
			replayCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			result, err := s.config.RawReplay(replayCtx, candidate)
			if err == nil {
				err = replayCtx.Err()
			}
			cancel()
			slot := candidate.SlotKey()
			s.mu.Lock()
			pending, tracked := s.recoveryPending[slot]
			delete(s.recoveryPending, slot)
			if !tracked {
				pending.generations = map[string]rawReplayGeneration{
					candidate.GenerationKey(): {candidate: candidate},
				}
			}
			if err != nil {
				s.metrics.RawReplayFailures++
				for generation, failed := range pending.generations {
					failed.failures++
					if failed.failures >= rawReplayMaxAttempts {
						if _, exhausted := s.recoveryExhausted[generation]; !exhausted {
							s.recoveryExhausted[generation] = failed.candidate.MinuteStart
							s.metrics.RawReplayExhausted++
						}
						continue
					}
					failed.retryAt = time.Now().Add(s.rawReplayRetryDelayForFailure(failed.failures))
					if !s.deferRawReplayGenerationLocked(slot, generation, failed) {
						s.metrics.RawReplaySuppressed++
					}
				}
			} else {
				for generation, succeeded := range pending.generations {
					s.recoverySucceeded[generation] = succeeded.candidate.MinuteStart
				}
				s.metrics.RawReplayCompleted++
				s.metrics.RawReplayFramesRead += int64(result.FramesRead)
				s.metrics.RawReplayTradesSubmitted += int64(result.TradesSubmitted)
			}
			s.drainRawReplayDeferredLocked(time.Now())
			s.mu.Unlock()
			if err != nil {
				s.recordError(err)
			}
		}
	}
}

func (s *MinuteService) deferRawReplayGenerationLocked(slot, generation string, work rawReplayGeneration) bool {
	deferred, exists := s.recoveryDeferred[slot]
	if !exists && len(s.recoveryDeferred) >= rawReplayDeferredCapacity {
		oldestSlot := ""
		oldestMinute := work.candidate.MinuteStart
		for key, entry := range s.recoveryDeferred {
			if _, pending := s.recoveryPending[key]; pending {
				continue
			}
			if oldestSlot == "" || entry.minute < oldestMinute {
				oldestSlot = key
				oldestMinute = entry.minute
			}
		}
		if oldestSlot != "" && oldestMinute < work.candidate.MinuteStart {
			delete(s.recoveryDeferred, oldestSlot)
		} else {
			return false
		}
	}
	if !exists {
		deferred = rawReplayDeferred{
			minute: work.candidate.MinuteStart, generations: make(map[string]rawReplayGeneration),
		}
	}
	if previous, duplicate := deferred.generations[generation]; duplicate {
		if previous.failures > work.failures || (previous.failures == work.failures && previous.retryAt.After(work.retryAt)) {
			work = previous
		}
	}
	deferred.generations[generation] = work
	s.recoveryDeferred[slot] = deferred
	return true
}

func (s *MinuteService) drainRawReplayDeferredLocked(now time.Time) {
	slots := make([]string, 0, len(s.recoveryDeferred))
	for slot := range s.recoveryDeferred {
		slots = append(slots, slot)
	}
	sort.Slice(slots, func(i, j int) bool {
		left, right := s.recoveryDeferred[slots[i]].minute, s.recoveryDeferred[slots[j]].minute
		if left != right {
			return left < right
		}
		return slots[i] < slots[j]
	})
	for _, slot := range slots {
		if _, pending := s.recoveryPending[slot]; pending {
			continue
		}
		deferred := s.recoveryDeferred[slot]
		for generation := range deferred.generations {
			if _, succeeded := s.recoverySucceeded[generation]; succeeded {
				delete(deferred.generations, generation)
				continue
			}
			if _, exhausted := s.recoveryExhausted[generation]; exhausted {
				delete(deferred.generations, generation)
			}
		}
		if len(deferred.generations) == 0 {
			delete(s.recoveryDeferred, slot)
			continue
		}
		s.recoveryDeferred[slot] = deferred
		if rawReplaySlotReady(deferred, now) && !s.drainRawReplaySlotLocked(slot, now) {
			return
		}
	}
}

func (s *MinuteService) drainRawReplaySlotLocked(slot string, now time.Time) bool {
	if _, pending := s.recoveryPending[slot]; pending {
		return false
	}
	deferred, exists := s.recoveryDeferred[slot]
	if !exists {
		return false
	}
	keys := make([]string, 0, len(deferred.generations))
	for generation, work := range deferred.generations {
		if _, succeeded := s.recoverySucceeded[generation]; succeeded {
			delete(deferred.generations, generation)
			continue
		}
		if _, exhausted := s.recoveryExhausted[generation]; exhausted {
			delete(deferred.generations, generation)
			continue
		}
		if rawReplayGenerationReady(work, now) {
			keys = append(keys, generation)
		}
	}
	if len(deferred.generations) == 0 {
		delete(s.recoveryDeferred, slot)
		return false
	}
	if len(keys) == 0 {
		s.recoveryDeferred[slot] = deferred
		return false
	}
	sort.Strings(keys)
	pending := rawReplayPending{generations: make(map[string]rawReplayGeneration, len(keys))}
	candidate := deferred.generations[keys[0]].candidate
	for index, generation := range keys {
		work := deferred.generations[generation]
		pending.generations[generation] = work
		if index > 0 {
			candidate = mergeRawReplayCandidates(candidate, work.candidate)
		}
	}
	select {
	case s.recoveryQueue <- candidate:
		s.recoveryPending[slot] = pending
		for _, generation := range keys {
			delete(deferred.generations, generation)
		}
		if len(deferred.generations) == 0 {
			delete(s.recoveryDeferred, slot)
		} else {
			s.recoveryDeferred[slot] = deferred
		}
		s.metrics.RawReplayCandidates++
		return true
	default:
		s.recoveryDeferred[slot] = deferred
		return false
	}
}

func (s *MinuteService) nextRawReplayRetryLocked(now time.Time) (time.Duration, bool) {
	if len(s.recoveryQueue) > 0 {
		return 0, false
	}
	var earliest time.Time
	for slot, deferred := range s.recoveryDeferred {
		if _, pending := s.recoveryPending[slot]; pending {
			continue
		}
		for generation, work := range deferred.generations {
			if _, succeeded := s.recoverySucceeded[generation]; succeeded {
				delete(deferred.generations, generation)
				continue
			}
			if _, exhausted := s.recoveryExhausted[generation]; exhausted {
				delete(deferred.generations, generation)
				continue
			}
			if rawReplayGenerationReady(work, now) {
				return 0, true
			}
			if earliest.IsZero() || work.retryAt.Before(earliest) {
				earliest = work.retryAt
			}
		}
		if len(deferred.generations) == 0 {
			delete(s.recoveryDeferred, slot)
		}
	}
	if earliest.IsZero() {
		return 0, false
	}
	if delay := earliest.Sub(now); delay > 0 {
		return delay, true
	}
	return 0, true
}

func (s *MinuteService) rawReplayRetryDelayForFailure(failures int) time.Duration {
	delay := s.recoveryRetryDelay
	if delay <= 0 {
		delay = rawReplayRetryBaseDelay
	}
	for attempt := 1; attempt < failures; attempt++ {
		delay *= 2
	}
	return delay
}

func rawReplayGenerationReady(work rawReplayGeneration, now time.Time) bool {
	return work.retryAt.IsZero() || !now.Before(work.retryAt)
}

func rawReplaySlotReady(deferred rawReplayDeferred, now time.Time) bool {
	for _, work := range deferred.generations {
		if rawReplayGenerationReady(work, now) {
			return true
		}
	}
	return false
}

func (s *MinuteService) run(ctx context.Context) {
	defer s.wait.Done()
	ticker := time.NewTicker(s.config.Tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.RunOnce(ctx); err != nil {
				s.recordError(err)
			}
		}
	}
}

func (s *MinuteService) refreshReadiness(serverNowMS int64) {
	if s.config.Readiness == nil {
		return
	}
	ready := make([]domain.Exchange, 0)
	for exchange, value := range s.config.Readiness() {
		if value {
			ready = append(ready, exchange)
		}
	}
	s.scheduler.MarkReady(ready, serverNowMS)
}

func (s *MinuteService) gaps(ctx context.Context) ([]coverage.Gap, error) {
	if s.config.ReadGaps != nil {
		gaps, err := s.config.ReadGaps(ctx)
		if err != nil {
			err = fmt.Errorf("read minute coverage gaps: %w", err)
			s.recordError(err)
			return nil, err
		}
		return gaps, nil
	}
	if s.config.Gaps == nil {
		return nil, nil
	}
	return s.config.Gaps(), nil
}

func (s *MinuteService) flushClosed(ctx context.Context, serverNowMS int64, gaps []coverage.Gap) error {
	rows, err := s.scheduler.DrainClosed(serverNowMS, gaps)
	if err != nil {
		return err
	}
	s.mu.Lock()
	for _, row := range rows {
		key := minuteIdentity(row.Exchange, row.Market, row.MinuteStart)
		if _, exists := s.pending[key]; exists {
			s.metrics.PendingWSCollisions++
			continue
		}
		s.pending[key] = pendingMinute{row: row}
		s.pendingOrder = append(s.pendingOrder, key)
	}
	s.mu.Unlock()
	return s.persistPending(ctx)
}

func (s *MinuteService) persistPending(ctx context.Context) error {
	for {
		s.mu.Lock()
		if len(s.pendingOrder) == 0 {
			s.mu.Unlock()
			return nil
		}
		key := s.pendingOrder[0]
		entry, exists := s.pending[key]
		if !exists {
			s.pendingOrder = s.pendingOrder[1:]
			s.mu.Unlock()
			continue
		}
		if entry.attempts > 0 {
			s.metrics.WSMinuteSaveRetries++
		}
		entry.attempts++
		s.pending[key] = entry
		s.mu.Unlock()
		_, err := s.store.SaveWSMinute(ctx, entry.row)
		if err != nil {
			var conflict *ImmutableMinuteConflict
			s.mu.Lock()
			if errors.As(err, &conflict) {
				s.metrics.WSMinuteConflicts++
				delete(s.pending, key)
				s.pendingOrder = s.pendingOrder[1:]
			} else {
				s.metrics.WSMinuteSaveFailures++
				s.metrics.StorageErrors++
				s.pendingOrder = append(s.pendingOrder[1:], key)
			}
			s.mu.Unlock()
			s.recordError(err)
			if !errors.As(err, &conflict) {
				return nil
			}
			continue
		}
		s.mu.Lock()
		delete(s.pending, key)
		s.pendingOrder = s.pendingOrder[1:]
		s.metrics.ClosedWSMinutes++
		value := 0.0
		if entry.row.KRWVolume != nil {
			value = *entry.row.KRWVolume
		}
		s.closedVolume[volumeKey(entry.row.Exchange, entry.row.Market, entry.row.MinuteStart)] = value
		s.pruneClosedVolumesLocked(entry.row.MinuteStart)
		s.mu.Unlock()
	}
}

func (s *MinuteService) worker(ctx context.Context, exchange domain.Exchange) {
	defer s.wait.Done()
	timer := time.NewTimer(s.config.Tick)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for {
		job, dropped := s.scheduler.Next(exchange, s.scheduler.EstimatedServerNowMS(s.config.Now().UnixMilli()))
		if len(dropped) > 0 {
			s.mu.Lock()
			s.metrics.JobsDroppedStale += int64(len(dropped))
			s.mu.Unlock()
		}
		if job != nil {
			s.processJob(ctx, *job)
			s.finalizeJobBatch(ctx, *job)
			continue
		}
		s.flushPendingFinalizations(ctx, exchange, false)
		timer.Reset(s.config.Tick)
		select {
		case <-ctx.Done():
			stopAndDrainTimer(timer)
			return
		case <-s.signals[exchange]:
			stopAndDrainTimer(timer)
		case <-timer.C:
		}
	}
}

func stopAndDrainTimer(timer *time.Timer) {
	if timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

func (s *MinuteService) processJob(ctx context.Context, job CandleJob) {
	defer func() { s.mu.Lock(); s.metrics.JobsCompleted++; s.mu.Unlock() }()
	if err := s.refreshJobCoverage(ctx, &job); err != nil {
		s.scheduleRetry(job)
		return
	}
	if job.Attempt > 0 {
		s.mu.Lock()
		s.metrics.RetriesExecuted++
		s.mu.Unlock()
	}
	request, err := official.BuildCandleRequest(job.Exchange, job.Market.Symbol, job.TargetStartMS+MinuteMS+1)
	if err != nil {
		s.requestFailed(ctx, job, err, true)
		return
	}
	query, err := requestParams(request.Params)
	if err != nil {
		s.requestFailed(ctx, job, err, true)
		return
	}
	group := "public"
	if job.Exchange == domain.ExchangeUpbit {
		group = "candle"
	}
	priority := job.requestPriority()
	s.mu.Lock()
	s.metrics.RequestsStarted++
	s.mu.Unlock()
	raw, err := restbudget.FetchJSONBytes(ctx, s.client, restbudget.Request{Exchange: string(job.Exchange), Group: group, URL: request.URL, Query: query, Priority: priority, Cost: 1}, 1<<20)
	if err != nil {
		s.requestFailed(ctx, job, err, false)
		return
	}
	s.mu.Lock()
	s.metrics.RequestsSucceeded++
	s.mu.Unlock()
	candle, err := official.ParseClosedCandle(job.Exchange, job.Market.Symbol, raw, job.TargetStartMS+MinuteMS+1)
	if err != nil {
		s.requestFailed(ctx, job, err, true)
		return
	}
	if candle == nil {
		s.missingCandle(ctx, job)
		return
	}
	stored, err := CandleFromOfficial(*candle, job.Market.Base, job.Market.Quote, job.Attempt, time.UnixMilli(s.scheduler.EstimatedServerNowMS(s.config.Now().UnixMilli())))
	if err != nil {
		s.requestFailed(ctx, job, err, true)
		return
	}
	inserted, err := s.store.SaveOfficialCandle(ctx, stored)
	if err != nil {
		var conflict *ImmutableMinuteConflict
		s.mu.Lock()
		if errors.As(err, &conflict) {
			s.metrics.ImmutableCandleConflicts++
		} else {
			s.metrics.StorageErrors++
		}
		s.mu.Unlock()
		s.recordError(err)
		if !errors.As(err, &conflict) {
			s.scheduleRetry(job)
		}
		return
	}
	s.mu.Lock()
	if inserted {
		s.metrics.OfficialCandlesSaved++
	} else {
		s.metrics.OfficialCandlesIdempotent++
	}
	s.mu.Unlock()
	result, err := s.reconcile(ctx, job)
	if err != nil {
		s.scheduleRetry(job)
		return
	}
	if result.Status == StatusCandleUnavailable || result.Status == StatusMismatch || result.Status == StatusWSMissing {
		s.scheduleRetry(job)
	}
}

func (s *MinuteService) refreshJobCoverage(ctx context.Context, job *CandleJob) error {
	if job == nil {
		return nil
	}
	s.sealEvidenceMu.Lock()
	defer s.sealEvidenceMu.Unlock()
	return s.refreshJobCoverageLocked(ctx, job)
}

func (s *MinuteService) refreshJobCoverageLocked(ctx context.Context, job *CandleJob) error {
	minute := domain.UnixSecond(job.TargetStartMS / 1000)
	skew := s.scheduler.EstimatedClockSkewMS()
	skewMS := 0.0
	if skew != nil {
		skewMS = *skew
	}
	gaps, err := s.gaps(ctx)
	if err != nil {
		return err
	}
	knownGapSeconds := KnownGapSeconds(job.Exchange, job.Market.Symbol, minute, gaps, skewMS)
	s.mu.Lock()
	volumes := copyVolumes(s.closedVolume)
	s.mu.Unlock()
	seals, candidates := s.scheduler.PreviewTarget(job.TargetStartMS, s.config.Markets(), gaps, volumes)
	for _, seal := range seals {
		if seal.Exchange != job.Exchange {
			continue
		}
		key := finalizationKey{exchange: seal.Exchange, minute: seal.MinuteStart}
		s.mu.Lock()
		previous, exists := s.recentSeals[key]
		s.mu.Unlock()
		if !exists || sameMinuteSealEvidence(previous, seal) || !sameMinuteSealRegistry(previous, seal) {
			break
		}
		seal.SealedAt = previous.SealedAt
		seal.UpdatedAt = s.config.Now().UTC()
		if err := s.persistMinuteSealRevisions(ctx, []minuteSealRevision{{seal: seal, previous: previous, hasPrevious: true}}); err != nil {
			return err
		}
		break
	}
	// Success identity, pending serialization, and deferred queue recovery are
	// decided from the exact evidence generation in enqueueRawReplay. Attempt
	// zero must participate: its first admission may have met a full queue, its
	// replay may have failed, or a newer gap snapshot may now exist.
	for _, candidate := range candidates {
		if candidate.Exchange == job.Exchange {
			s.enqueueRawReplay(candidate)
		}
	}
	job.KnownGapSeconds = knownGapSeconds
	return nil
}

func (s *MinuteService) requestFailed(ctx context.Context, job CandleJob, err error, parse bool) {
	s.mu.Lock()
	if parse {
		s.metrics.ParseErrors++
	} else {
		s.metrics.RequestErrors++
	}
	s.mu.Unlock()
	s.recordError(err)
	_, _ = s.reconcileIfWS(ctx, job)
	s.scheduleRetry(job)
}

func (s *MinuteService) missingCandle(ctx context.Context, job CandleJob) {
	s.mu.Lock()
	s.metrics.OfficialCandlesMissing++
	s.mu.Unlock()
	result, ok := s.reconcileIfWS(ctx, job)
	if !ok {
		s.mu.Lock()
		s.metrics.UnconfirmedAbsences++
		s.mu.Unlock()
		if job.KnownGapSeconds > 0 {
			s.scheduleRetry(job)
		}
		return
	}
	if result.Status != StatusCandleBaseOnly {
		s.scheduleRetry(job)
	}
}

func (s *MinuteService) reconcileIfWS(ctx context.Context, job CandleJob) (Result, bool) {
	ws, err := s.store.GetWSMinute(ctx, job.Exchange, job.Market.Symbol, domain.UnixSecond(job.TargetStartMS/1000))
	if err != nil {
		s.storageError(err)
		return Result{}, false
	}
	if ws == nil {
		return Result{}, false
	}
	result, err := s.reconcile(ctx, job)
	return result, err == nil
}

func (s *MinuteService) reconcile(ctx context.Context, job CandleJob) (Result, error) {
	// The candle HTTP request can outlive the final audit. Re-read the gap
	// while holding the same publication boundary before storing its decision.
	s.sealEvidenceMu.Lock()
	defer s.sealEvidenceMu.Unlock()
	if err := s.refreshJobCoverageLocked(ctx, &job); err != nil {
		return Result{}, err
	}
	minute := domain.UnixSecond(job.TargetStartMS / 1000)
	checkedAt := time.UnixMilli(s.scheduler.EstimatedServerNowMS(s.config.Now().UnixMilli()))
	result, err := s.store.ReconcileMinute(ctx, job.Exchange, job.Market.Symbol, minute, job.KnownGapSeconds, checkedAt)
	if err != nil {
		s.storageError(err)
		return Result{}, err
	}
	s.mu.Lock()
	s.metrics.ReconciledMinutes++
	s.scheduleFinalizationLocked(job, minute, checkedAt)
	s.mu.Unlock()
	return result, nil
}

func (s *MinuteService) scheduleFinalizationLocked(job CandleJob, minute domain.UnixSecond, checkedAt time.Time) {
	key := finalizationKey{exchange: job.Exchange, minute: minute}
	now := s.config.Now()
	if pending, exists := s.finalizations[key]; exists {
		pending.checkedAt = checkedAt
		if job.ExpectedMarketCount > pending.expectedMarketCount {
			pending.expectedMarketCount = job.ExpectedMarketCount
		}
		if job.Attempt > pending.sourceAttempt {
			pending.firstQueued = now
			pending.attempts = 0
			pending.sourceAttempt = job.Attempt
		}
		s.finalizationQueues[job.Exchange].update(pending.queueEntry, now)
		return
	}

	queue := s.finalizationQueues[job.Exchange]
	if queue == nil {
		value := make(finalizationQueue, 0)
		queue = &value
		s.finalizationQueues[job.Exchange] = queue
	}
	if queue.Len() >= finalizationMaxPending {
		s.dropOldestFinalizationLocked(job.Exchange)
	}
	entry := &finalizationQueueEntry{key: key, nextAttempt: now, index: -1}
	pending := &pendingFinalization{
		checkedAt:           checkedAt,
		firstQueued:         now,
		sourceAttempt:       job.Attempt,
		expectedMarketCount: job.ExpectedMarketCount,
		queueEntry:          entry,
	}
	s.finalizations[key] = pending
	queue.push(entry)
}

func (s *MinuteService) dropOldestFinalizationLocked(exchange domain.Exchange) {
	queue := s.finalizationQueues[exchange]
	if queue == nil || queue.Len() == 0 {
		return
	}
	oldest := (*queue)[0]
	for _, entry := range *queue {
		if entry.key.minute < oldest.key.minute {
			oldest = entry
		}
	}
	s.removeFinalizationLocked(oldest.key, true)
}

func (s *MinuteService) removeFinalizationLocked(key finalizationKey, dropped bool) {
	pending, exists := s.finalizations[key]
	if !exists {
		return
	}
	if queue := s.finalizationQueues[key.exchange]; queue != nil {
		queue.remove(pending.queueEntry)
	}
	delete(s.finalizations, key)
	if dropped {
		s.metrics.FinalizationsDropped++
	}
}

func (s *MinuteService) finalizeJobBatch(ctx context.Context, job CandleJob) {
	if s.scheduler.HasQueuedBatch(job.Exchange, job.TargetStartMS, job.Attempt) {
		return
	}
	s.flushFinalization(ctx, finalizationKey{exchange: job.Exchange, minute: domain.UnixSecond(job.TargetStartMS / 1000)}, true, false)
}

func (s *MinuteService) flushPendingFinalizations(ctx context.Context, exchange domain.Exchange, force bool) {
	limit := finalizationMaxPerWake
	if force {
		limit = finalizationMaxPending
	}
	for processed := 0; processed < limit; processed++ {
		now := s.config.Now()
		s.mu.Lock()
		queue := s.finalizationQueues[exchange]
		if queue == nil || queue.Len() == 0 || (!force && (*queue)[0].nextAttempt.After(now)) {
			s.mu.Unlock()
			return
		}
		key := (*queue)[0].key
		s.mu.Unlock()
		s.flushFinalization(ctx, key, force, force)
	}
}

func (s *MinuteService) flushFinalization(ctx context.Context, key finalizationKey, ignoreDue, finalAttempt bool) {
	now := s.config.Now()
	s.mu.Lock()
	pending, exists := s.finalizations[key]
	if !exists || (!ignoreDue && now.Before(pending.queueEntry.nextAttempt)) {
		s.mu.Unlock()
		return
	}
	if pending.attempts >= finalizationMaxAttempts || now.Sub(pending.firstQueued) > finalizationMaxAge {
		s.removeFinalizationLocked(key, true)
		s.mu.Unlock()
		return
	}
	pending.attempts++
	attempt := pending.attempts
	checkedAt := pending.checkedAt
	expectedMarketCount := pending.expectedMarketCount
	s.mu.Unlock()

	summary, err := s.store.FinalizeExchangeMinute(ctx, key.exchange, key.minute, expectedMarketCount, checkedAt)
	if err != nil {
		s.storageError(err)
		s.mu.Lock()
		if current, ok := s.finalizations[key]; ok && current == pending && current.checkedAt.Equal(checkedAt) {
			s.metrics.FinalizationFailures++
			if finalAttempt || attempt >= finalizationMaxAttempts || now.Sub(current.firstQueued) >= finalizationMaxAge {
				s.removeFinalizationLocked(key, true)
			} else {
				s.metrics.FinalizationRetries++
				s.finalizationQueues[key.exchange].update(current.queueEntry, now.Add(finalizationRetryDelayForAttempt(attempt)))
			}
		}
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	if current, ok := s.finalizations[key]; ok && current == pending && current.checkedAt.Equal(checkedAt) {
		s.removeFinalizationLocked(key, false)
		s.metrics.FinalizationsSucceeded++
		s.latest = summary
	}
	s.mu.Unlock()
}

func finalizationRetryDelayForAttempt(attempt int) time.Duration {
	if attempt <= 1 {
		return finalizationRetryDelay
	}
	delay := finalizationRetryDelay << min(attempt-1, 30)
	if delay > finalizationMaxRetryDelay {
		return finalizationMaxRetryDelay
	}
	return delay
}

func (s *MinuteService) scheduleRetry(job CandleJob) {
	if s.scheduler.ScheduleRetry(job) {
		s.mu.Lock()
		s.metrics.RetriesScheduled++
		s.mu.Unlock()
		s.signal(job.Exchange)
	} else if job.Attempt < len(RetryOffsetsMS) {
		s.mu.Lock()
		s.metrics.RetriesSuppressed++
		s.mu.Unlock()
	}
}

func (s *MinuteService) signal(exchange domain.Exchange) {
	select {
	case s.signals[exchange] <- struct{}{}:
	default:
	}
}
func (s *MinuteService) storageError(err error) {
	s.mu.Lock()
	s.metrics.StorageErrors++
	s.mu.Unlock()
	s.recordError(err)
}
func (s *MinuteService) recordError(err error) {
	s.mu.Lock()
	s.lastError = fmt.Sprintf("%T: %v", err, err)
	if len(s.lastError) > 500 {
		s.lastError = s.lastError[:500]
	}
	s.mu.Unlock()
}

func (s *MinuteService) Snapshot() ServiceSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	queued := 0
	for _, exchange := range domain.DomesticExchanges {
		queued += s.scheduler.QueueLen(exchange)
	}
	last := cloneInt64(s.lastScheduled)
	seals := make(map[string]MinuteSeal, len(s.latestSeals))
	for exchange, seal := range s.latestSeals {
		seals[string(exchange)] = seal
	}
	return ServiceSnapshot{Running: s.running, ClockReady: s.scheduler.ClockReady(), EstimatedClockSkewMS: s.scheduler.EstimatedClockSkewMS(), OpenWSMinutes: s.scheduler.OpenMinuteCount(), PendingClosedWSMinutes: len(s.pending), PendingFinalizations: len(s.finalizations), QueuedJobs: queued, ActiveWorkerCount: len(domain.DomesticExchanges), LastScheduledMinute: last, LatestSummary: s.latest, LatestMinuteSeals: seals, LastError: s.lastError, Metrics: s.metrics}
}

func requestParams(values map[string]any) (url.Values, error) {
	result := make(url.Values, len(values))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		switch value := values[key].(type) {
		case string:
			result.Set(key, value)
		case int:
			result.Set(key, strconv.Itoa(value))
		case int64:
			result.Set(key, strconv.FormatInt(value, 10))
		case float64:
			result.Set(key, strconv.FormatFloat(value, 'f', -1, 64))
		default:
			return nil, fmt.Errorf("unsupported candle query parameter %s type %T", key, value)
		}
	}
	return result, nil
}
func minuteIdentity(exchange domain.Exchange, market string, minute domain.UnixSecond) string {
	return string(exchange) + "\x00" + market + "\x00" + integer(int64(minute))
}
func (s *MinuteService) hasPendingMinuteLocked(minute int64) bool {
	for _, entry := range s.pending {
		if int64(entry.row.MinuteStart) == minute {
			return true
		}
	}
	return false
}
func (s *MinuteService) pruneClosedVolumesLocked(newest domain.UnixSecond) {
	cutoff := newest - domain.UnixSecond((closedVolumeRetainedMinutes-1)*MinuteSeconds)
	for key := range s.closedVolume {
		if key.minute < cutoff {
			delete(s.closedVolume, key)
		}
	}
}
func copyVolumes(source map[volumeIdentity]float64) map[volumeIdentity]float64 {
	result := make(map[volumeIdentity]float64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
func int64PointerService(value int64) *int64 { return &value }
func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
