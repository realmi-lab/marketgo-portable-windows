package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
	"market-share-go/internal/store/postgres"
)

const (
	// Stay below the five-minute observation-time dedupe snapshot horizon.
	// Only time increases; trade/event capacity and overflow rejection do not.
	deploymentCaptureMaxAge          = 270 * time.Second
	deploymentCaptureMaxTrades       = 100_000
	deploymentCaptureMaxObservations = 4096
	deploymentCaptureDetachTimeout   = time.Second
)

type deploymentHydrationStore interface {
	StreamHydrationCheckpoint(context.Context, domain.UnixSecond, domain.UnixSecond, func(aggregate.Bucket) error) (postgres.State, error)
}

type handoffOwnerRequest struct {
	ctx                         context.Context
	tradeTarget, coverageTarget uint64
	run                         func(*pendingFlush) error
	response                    chan error
}

type capturedDeploymentTrade struct {
	event    domain.TradeEvent
	rate     float64
	resolved bool
	at       domain.UnixSecond
}

type capturedDeploymentObservation struct {
	at, invalidFrom int64
	complete        bool
	context         coverage.GapContext
}

type runtimeDeploymentCapture struct {
	generation                  uint64
	started                     time.Time
	trades                      []capturedDeploymentTrade
	observations                []capturedDeploymentObservation
	err                         error
	snapshotObservationBoundary int
}

type deploymentCaptureStatus struct {
	generation uint64
	started    time.Time
	age        time.Duration
	healthy    bool
	err        error
}

type preparedRuntimeCheckpoint struct {
	aggregator      *aggregate.Aggregator
	store           deploymentHydrationStore
	capture         *runtimeDeploymentCapture
	preparedAt      time.Time
	prepareDuration time.Duration
	rows            int64
	state           postgres.State
}

type DeploymentCheckpointResult struct {
	Protocol               int   `json:"protocol"`
	Rows                   int64 `json:"rows"`
	ChangedRows            int64 `json:"changed_rows"`
	AddedRows              int64 `json:"added_rows"`
	ReplayedTrades         int64 `json:"replayed_trades"`
	AlreadyPersistedTrades int64 `json:"already_persisted_trades"`
	PrepareMS              int64 `json:"prepare_ms"`
	RefreshMS              int64 `json:"refresh_ms"`
	AsOf                   int64 `json:"as_of"`
}

func (r *Runtime) handoffOwnerTurn(ctx context.Context, run func(*pendingFlush) error) error {
	// A submitter holds these mutexes while waiting for bounded queue space.
	// Never wait behind a blocked submitter outside the handoff deadline.
	if !r.tradeSubmitMu.TryLock() {
		return errors.New("deployment trade barrier is busy")
	}
	tradeTarget := r.nextTradeSequence
	r.tradeSubmitMu.Unlock()
	if !r.coverageSubmitMu.TryLock() {
		return errors.New("deployment coverage barrier is busy")
	}
	coverageTarget := r.nextCoverageSequence
	r.coverageSubmitMu.Unlock()
	req := handoffOwnerRequest{ctx: ctx, tradeTarget: tradeTarget, coverageTarget: coverageTarget, run: run, response: make(chan error, 1)}
	select {
	case r.handoffRequests <- req:
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return ErrStopped
	}
	// Once admitted, wait for the owner to release every supplied checkpoint
	// reference before returning. The request's context still bounds its work.
	select {
	case err := <-req.response:
		return err
	case <-r.done:
		return ErrStopped
	}
}

func (r *Runtime) captureDeploymentTrade(event domain.TradeEvent, rate *float64, now domain.UnixSecond) {
	c := r.deploymentCapture
	if c == nil || c.err != nil {
		return
	}
	if time.Since(c.started) > deploymentCaptureMaxAge || len(c.trades) >= deploymentCaptureMaxTrades {
		c.err = errors.New("deployment exact replay capture exceeded its bound")
		c.trades = nil
		c.observations = nil
		return
	}
	event.QuoteAmount = clonePointer(event.QuoteAmount)
	row := capturedDeploymentTrade{event: event, at: now}
	if rate != nil {
		row.rate = *rate
		row.resolved = true
	}
	c.trades = append(c.trades, row)
}

func (r *Runtime) captureDeploymentObservation(at, from int64, complete bool, gapContext coverage.GapContext) {
	c := r.deploymentCapture
	if c == nil || c.err != nil {
		return
	}
	if time.Since(c.started) > deploymentCaptureMaxAge || len(c.observations) >= deploymentCaptureMaxObservations {
		c.err = errors.New("deployment coverage replay capture exceeded its bound")
		c.trades = nil
		c.observations = nil
		return
	}
	// Clone nested context fields through the existing coverage deep-copy path.
	copyState := cloneCoverageState(&coverage.State{Gaps: []coverage.Gap{{Context: gapContext}}})
	c.observations = append(c.observations, capturedDeploymentObservation{at: at, invalidFrom: from, complete: complete, context: copyState.Gaps[0].Context})
}

// prepareRuntimeCheckpoint performs the expensive rebuild while the old
// producer still owns the database. The private aggregate receives no live
// events until its final full canonical refresh has completed.
func (r *Runtime) prepareRuntimeCheckpoint(ctx context.Context, store deploymentHydrationStore) (*preparedRuntimeCheckpoint, error) {
	if store == nil {
		return nil, errors.New("consistent checkpoint store is required")
	}
	capture := &runtimeDeploymentCapture{}
	if err := r.handoffOwnerTurn(ctx, func(*pendingFlush) error {
		r.installDeploymentCapture(capture)
		return nil
	}); err != nil {
		return nil, err
	}
	p, err := buildRuntimeCheckpoint(ctx, store, capture)
	if err == nil {
		return p, nil
	}
	if detachErr := r.detachDeploymentCapture(ctx, capture); detachErr != nil {
		err = errors.Join(err, fmt.Errorf("detach failed deployment capture: %w", detachErr))
	}
	return nil, err
}

// installDeploymentCapture publishes a new monotonically identified capture.
// It must run on the runtime owner so status and demotion can attest that the
// prepared checkpoint still belongs to the exact live capture generation.
func (r *Runtime) installDeploymentCapture(capture *runtimeDeploymentCapture) {
	r.deploymentCaptureGeneration++
	if r.deploymentCaptureGeneration == 0 {
		r.deploymentCaptureGeneration++
	}
	capture.generation = r.deploymentCaptureGeneration
	capture.started = time.Now()
	r.deploymentCapture = capture
}

// detachDeploymentCapture removes only the failed preparation generation. The
// preparation context is commonly already cancelled, so cleanup gets its own
// short bound without extending the checkpoint's success deadline. A newer
// capture is never cleared by an older failure.
func (r *Runtime) detachDeploymentCapture(ctx context.Context, capture *runtimeDeploymentCapture) error {
	if capture == nil {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deploymentCaptureDetachTimeout)
	defer cancel()
	return r.handoffOwnerTurn(cleanupCtx, func(*pendingFlush) error {
		if r.deploymentCapture == capture {
			r.deploymentCapture = nil
		}
		return nil
	})
}

func (r *Runtime) detachExactDeploymentCapture(ctx context.Context, capture *runtimeDeploymentCapture) error {
	if capture == nil {
		return errors.New("deployment capture is absent")
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deploymentCaptureDetachTimeout)
	defer cancel()
	return r.handoffOwnerTurn(cleanupCtx, func(*pendingFlush) error {
		if r.deploymentCapture != capture {
			return errors.New("deployment capture generation changed before cleanup")
		}
		r.deploymentCapture = nil
		return nil
	})
}

// deploymentCaptureReusable reads the mutable capture error on the runtime
// owner. Callers must not treat a cached private checkpoint as prepared after
// its capture was replaced or exceeded a bound.
func (r *Runtime) deploymentCaptureReusable(ctx context.Context, capture *runtimeDeploymentCapture) (bool, error) {
	if capture == nil {
		return false, nil
	}
	var reusable bool
	err := r.handoffOwnerTurn(ctx, func(*pendingFlush) error {
		reusable = r.deploymentCapture == capture && capture.err == nil
		return nil
	})
	return reusable, err
}

func (r *Runtime) preparedDeploymentCaptureStatus(ctx context.Context, capture *runtimeDeploymentCapture) (deploymentCaptureStatus, error) {
	var status deploymentCaptureStatus
	if capture == nil {
		return status, nil
	}
	err := r.handoffOwnerTurn(ctx, func(*pendingFlush) error {
		status.generation = capture.generation
		status.started = capture.started
		status.age = time.Since(capture.started)
		status.err = capture.err
		status.healthy = r.deploymentCapture == capture && capture.generation != 0 &&
			status.age >= 0 && status.age < deploymentCaptureMaxAge/2 && capture.err == nil
		return nil
	})
	return status, err
}

func (r *Runtime) currentDeploymentCaptureStatus(ctx context.Context) (deploymentCaptureStatus, error) {
	var status deploymentCaptureStatus
	err := r.handoffOwnerTurn(ctx, func(*pendingFlush) error {
		capture := r.deploymentCapture
		if capture == nil {
			return nil
		}
		status.generation = capture.generation
		status.started = capture.started
		status.age = time.Since(capture.started)
		status.err = capture.err
		status.healthy = capture.generation != 0 && status.age >= 0 &&
			status.age < deploymentCaptureMaxAge/2 && capture.err == nil
		return nil
	})
	return status, err
}

func (r *Runtime) discardCurrentDeploymentCapture(ctx context.Context) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deploymentCaptureDetachTimeout)
	defer cancel()
	return r.handoffOwnerTurn(cleanupCtx, func(*pendingFlush) error {
		r.deploymentCapture = nil
		return nil
	})
}

func (r *Runtime) discardPreparedRuntimeCheckpoint(ctx context.Context, p *preparedRuntimeCheckpoint) error {
	if p == nil {
		return nil
	}
	if err := r.detachDeploymentCapture(ctx, p.capture); err != nil {
		return err
	}
	if p.aggregator != nil {
		p.aggregator.Close()
		p.aggregator = nil
	}
	return nil
}

// discardFailedRuntimeCheckpoint always releases the unpublished private
// aggregate, even when the bounded owner cleanup cannot detach its capture.
// No Service pointer references this checkpoint, so retaining the aggregate
// would otherwise create an unrecoverable multi-million-row leak.
func (r *Runtime) discardFailedRuntimeCheckpoint(ctx context.Context, p *preparedRuntimeCheckpoint) error {
	if p == nil {
		return nil
	}
	err := r.detachDeploymentCapture(ctx, p.capture)
	if p.aggregator != nil {
		p.aggregator.Close()
		p.aggregator = nil
	}
	return err
}

func buildRuntimeCheckpoint(ctx context.Context, store deploymentHydrationStore, capture *runtimeDeploymentCapture) (*preparedRuntimeCheckpoint, error) {
	agg, err := aggregate.New(nil)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			agg.Close()
		}
	}()
	started := time.Now()
	now := domain.UnixSecond(started.Unix())
	// Initialize rolling cutoffs before loading historical rows. Those rows
	// never entered shorter windows and must not be subtracted from them later.
	agg.Advance(now)
	var rows int64
	state, err := store.StreamHydrationCheckpoint(ctx, now-domain.UnixSecond(hydrateRetention/time.Second), now-domain.UnixSecond(recentTradeIDRetention/time.Second), func(row aggregate.Bucket) error {
		rows++
		return agg.RestoreBucket(row, now)
	})
	if err != nil {
		return nil, err
	}
	if len(state.Buckets) != 0 {
		return nil, errors.New("checkpoint unexpectedly materialized buckets")
	}
	// Watermarks and IDs will be loaded anew with the final canonical rows;
	// importing this earlier generation now is deliberately unnecessary.
	agg.Advance(now)
	keep = true
	return &preparedRuntimeCheckpoint{aggregator: agg, store: store, capture: capture, preparedAt: time.Now(), prepareDuration: time.Since(started), rows: rows, state: state}, nil
}

// refreshRuntimeCheckpoint scans ALL retained canonical rows after the old
// writer has released its lease. A delayed trade can change any retained
// second, so an arbitrary recent-tail query is not a substitute for this pass.
func refreshRuntimeCheckpoint(ctx context.Context, p *preparedRuntimeCheckpoint) (postgres.State, DeploymentCheckpointResult, error) {
	result := DeploymentCheckpointResult{Protocol: 2, PrepareMS: p.prepareDuration.Milliseconds()}
	started := time.Now()
	now := domain.UnixSecond(started.Unix())
	if p.aggregator == nil || time.Since(p.capture.started) > deploymentCaptureMaxAge {
		return postgres.State{}, result, errors.New("prepared checkpoint expired")
	}
	since := now - domain.UnixSecond(hydrateRetention/time.Second)
	p.aggregator.Advance(now)
	expected := p.aggregator.CheckpointRowCount(since)
	state, err := p.store.StreamHydrationCheckpoint(ctx, since, now-domain.UnixSecond(recentTradeIDRetention/time.Second), func(row aggregate.Bucket) error {
		added, changed, err := p.aggregator.ReplaceCheckpointBucket(row, now)
		if err != nil {
			return err
		}
		result.Rows++
		if added {
			result.AddedRows++
		}
		if changed {
			result.ChangedRows++
		}
		return nil
	})
	if err != nil {
		return postgres.State{}, result, err
	}
	if result.Rows != expected+result.AddedRows {
		return postgres.State{}, result, errors.New("canonical checkpoint contains deleted or duplicate retained rows")
	}
	if len(state.Buckets) != 0 || !state.CoverageLoaded {
		return postgres.State{}, result, errors.New("canonical checkpoint metadata is incomplete")
	}
	if err := p.aggregator.ResetCheckpointDedupe(state.Watermarks, state.RecentIDs); err != nil {
		return postgres.State{}, result, err
	}
	p.state = postgres.State{}
	result.RefreshMS = time.Since(started).Milliseconds()
	result.AsOf = int64(now)
	return state, result, nil
}

func replayDeploymentCapture(ctx context.Context, agg *aggregate.Aggregator, state postgres.State, capture *runtimeDeploymentCapture, result *DeploymentCheckpointResult) (*coverage.Tracker, error) {
	if capture == nil || capture.err != nil {
		if capture != nil {
			return nil, capture.err
		}
		return nil, errors.New("deployment capture is absent")
	}
	if time.Since(capture.started) > deploymentCaptureMaxAge {
		return nil, errors.New("deployment capture expired")
	}
	// Keep an exact membership set for captured identities, independent of the
	// bounded in-memory dedupe eviction policy. Observed time is NOT bucket time.
	wanted := make(map[recentKey]bool, len(capture.trades))
	for _, row := range capture.trades {
		wanted[recentKey{row.event.Exchange, row.event.Market, row.event.TradeID}] = false
	}
	for _, row := range state.RecentIDs {
		key := recentKey{row.Exchange, row.Market, row.TradeID}
		if _, ok := wanted[key]; ok {
			wanted[key] = true
		}
	}
	watermarks := make(map[watermarkKey]int64, len(state.Watermarks))
	for _, row := range state.Watermarks {
		if row.Exchange == domain.ExchangeKorbit {
			watermarks[watermarkKey{row.Exchange, row.Market}] = row.TradeID
		}
	}
	replayNow := domain.UnixSecond(time.Now().Unix())
	agg.Advance(replayNow)
	for index, row := range capture.trades {
		if index%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		key := recentKey{row.event.Exchange, row.event.Market, row.event.TradeID}
		persisted := wanted[key]
		if row.event.Exchange == domain.ExchangeKorbit {
			if id, err := strconv.ParseInt(row.event.TradeID, 10, 64); err == nil {
				if watermark, ok := watermarks[watermarkKey{row.event.Exchange, row.event.Market}]; ok && id <= watermark {
					persisted = true
				}
			}
		}
		if persisted {
			result.AlreadyPersistedTrades++
			continue
		}
		var rate *float64
		if row.resolved {
			rate = &row.rate
		}
		before := agg.Metrics()
		_, accepted := agg.IngestWithResult(row.event, rate, replayNow)
		if !accepted && agg.Metrics().InvalidEvents > before.InvalidEvents {
			return nil, fmt.Errorf("accepted deployment trade failed exact replay for %s", row.event.Exchange)
		}
		if accepted {
			result.ReplayedTrades++
		}
		wanted[key] = true
	}
	// The canonical prefix is authoritative. Only subsequent transport ticks
	// append coverage; real captured invalidations still remove affected seconds.
	frontier := state.Coverage.LastObserved
	if frontier < capture.started.Unix()-2 {
		return nil, errors.New("canonical coverage is behind the deployment capture")
	}
	tracker := coverage.ContinueCheckpoint(state.Coverage)
	for index, row := range capture.observations {
		if index < capture.snapshotObservationBoundary && row.at <= frontier {
			continue
		}
		if row.invalidFrom > 0 {
			if index < capture.snapshotObservationBoundary {
				row.invalidFrom = max(row.invalidFrom, frontier+1)
			}
			tracker.InvalidateFrom(row.invalidFrom, row.at, row.context)
		} else if row.at > frontier {
			tracker.Advance(row.at, row.complete, row.context)
		}
	}
	return tracker, nil
}

func (r *Runtime) installRuntimeCheckpoint(ctx context.Context, p *preparedRuntimeCheckpoint, state postgres.State, result *DeploymentCheckpointResult, gate *deploymentWriterGate) error {
	return r.handoffOwnerTurn(ctx, func(pending *pendingFlush) error {
		if r.deploymentCapture != p.capture {
			return errors.New("deployment capture generation changed")
		}
		tracker, err := replayDeploymentCapture(ctx, p.aggregator, state, p.capture, result)
		if err != nil {
			return err
		}
		if err := p.aggregator.ContinueCheckpointVersion(r.aggregator.Metrics()); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		replacer, ok := r.store.(interface {
			replaceDeploymentAggregation(context.Context, []aggregate.Bucket, []aggregate.WatermarkRow, []aggregate.RecentIDRow, *coverage.State) error
		})
		if !ok {
			return errors.New("runtime store cannot atomically replace a deployment checkpoint")
		}
		now := domain.UnixSecond(time.Now().Unix())
		buckets, watermarks, recent := p.aggregator.DrainClosed(now)
		observation := tracker.State()
		// Capacity is checked and the fresh immutable write replaces the old
		// generation BEFORE changing the live pointer or clearing pending rows.
		if err := replacer.replaceDeploymentAggregation(ctx, buckets, watermarks, recent, &observation); err != nil {
			return err
		}
		// Old-only accepted trades may lie a few seconds in the future. Their
		// entire minute is absent from the candidate's separate accumulator.
		gate.setReconciliationFloor(checkpointReconciliationFloor(now, p.aggregator.LatestBucketSecond()))
		old := r.aggregator
		r.aggregator = p.aggregator
		p.aggregator = nil
		r.coverage = tracker
		r.shareSeriesCorrections.record(1, int64(now), int64(now), false)
		r.coverageDirty = false
		pending.clear()
		old.Close()
		return nil
	})
}

func checkpointReconciliationFloor(installed, latest domain.UnixSecond) int64 {
	return int64(max(installed, latest))/60*60 + 60
}

func (r *Runtime) markDeploymentSnapshot(ctx context.Context) error {
	return r.handoffOwnerTurn(ctx, func(*pendingFlush) error {
		if r.deploymentCapture == nil {
			return errors.New("deployment capture is absent")
		}
		r.deploymentCapture.snapshotObservationBoundary = len(r.deploymentCapture.observations)
		return nil
	})
}
