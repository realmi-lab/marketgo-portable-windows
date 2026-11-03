package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
	"market-share-go/internal/reconcile"
)

const (
	analysisObservationStepSec  = int64(5)
	analysisReadCacheTTL        = 15 * time.Second
	analysisReadCacheMaxEntries = 8
)

var rollingFlowHorizons = [...]int64{60, 300, 600, 1200, 1800, 3600}
var closedMinuteDurations = [...]int64{600, 1800, 3600}

type readIntervalKey struct {
	start int64
	end   int64
}

type analysisReadPlan struct {
	intervals           []aggregate.IntervalRequest
	coverage            []coverageInterval
	intervalIndex       map[readIntervalKey]int
	coverageIndex       map[readIntervalKey]int
	globalCoverageIndex map[readIntervalKey]int
}

type analysisReadView struct {
	version                 uint64
	intervals               []aggregate.IntervalSnapshotResult
	coverage                []coverage.Result
	intervalIndex           map[readIntervalKey]int
	coverageIndex           map[readIntervalKey]int
	globalCoverageIndex     map[readIntervalKey]int
	coverageState           coverage.State
	minuteSupplements       []reconcile.MinuteSupplement
	supplementStatus        string
	supplementConflictCount int
}

func newAnalysisReadPlan(now, _ int64) *analysisReadPlan {
	plan := &analysisReadPlan{
		intervalIndex:       make(map[readIntervalKey]int),
		coverageIndex:       make(map[readIntervalKey]int),
		globalCoverageIndex: make(map[readIntervalKey]int),
	}
	// One immutable superset serves every supported dashboard window for the
	// same observation. This avoids rescanning the aggregator once per pinned
	// window/scope while retaining result-key separation above this read layer.
	for _, window := range aggregate.DefaultWindows {
		plan.addInterval(now-window, now)
		plan.addCoverage(now-window+1, now)
		plan.addGlobalCoverage(now-window+1, now)
		if window != marketanalysis.RollingWindowSec {
			plan.addInterval(now-2*window, now-window)
			plan.addCoverage(now-2*window+1, now-window)
		}
	}

	rollingStart := now - marketanalysis.RollingWindowSec
	plan.addInterval(rollingStart, now)
	plan.addCoverage(rollingStart+1, now)
	for _, horizon := range rollingFlowHorizons {
		plan.addInterval(now-horizon, now)
		plan.addInterval(rollingStart-horizon, rollingStart)
		plan.addCoverage(now-horizon-marketanalysis.RollingWindowSec+1, now-horizon)
		plan.addCoverage(now-horizon+1, now)
		plan.addCoverage(rollingStart-horizon+1, rollingStart)
	}

	closedEnd := (now+1)/60*60 - 1
	if closedEnd > 0 {
		for _, duration := range closedMinuteDurations {
			currentStart := closedEnd - duration + 1
			previousStart, previousEnd := currentStart-duration, currentStart-1
			plan.addInterval(previousStart-1, previousEnd)
			plan.addInterval(currentStart-1, closedEnd)
			plan.addCoverage(previousStart, previousEnd)
			plan.addCoverage(currentStart, closedEnd)
		}
	}
	return plan
}

type analysisReadKey struct {
	endSecond  int64
	trendStart int64
	trendEnd   int64
	trendReady bool
}

type analysisReadSnapshot struct {
	endSecond   int64
	view        analysisReadView
	operational *analysisOperationalSnapshot
	windows     map[string]map[int64]marketanalysis.WindowState
	rollingBase marketanalysis.RollingMonitor
	rolling     marketanalysis.RollingMonitor
	// changes holds the cross-window changes per market scope. They are
	// computed on first use and shared by every reader of this snapshot.
	changes map[string]*lazyWindowChanges
	// coinShares keeps the coin-share lists asked of this snapshot.
	coinShares *coinSharesMemo
}

type analysisReadCacheEntry struct {
	created time.Time
	value   analysisReadSnapshot
}

type analysisReadCall struct {
	done      chan struct{}
	value     analysisReadSnapshot
	err       error
	cancel    context.CancelFunc
	ctx       context.Context
	consumers int
}

// analysisReadMemo coalesces interval/coverage reads independently of result
// options. Cached endSecond travels with the view, preserving one closed-second
// boundary for every consumer. Canceled work retains admission until it exits.
type analysisReadMemo struct {
	mu         sync.Mutex
	cache      map[analysisReadKey]analysisReadCacheEntry
	inflight   map[analysisReadKey]*analysisReadCall
	generation uint64
	slots      chan struct{}
	pending    int
}

func newAnalysisReadMemo() *analysisReadMemo {
	return &analysisReadMemo{
		cache:    make(map[analysisReadKey]analysisReadCacheEntry),
		inflight: make(map[analysisReadKey]*analysisReadCall),
		slots:    make(chan struct{}, analysisMaxRunning),
	}
}

func (m *analysisReadMemo) get(
	ctx context.Context,
	key analysisReadKey,
	calculate func(context.Context) (analysisReadSnapshot, error),
) (analysisReadSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return analysisReadSnapshot{}, err
	}
	now := time.Now()
	m.mu.Lock()
	m.removeExpired(now)
	if cached, found := m.cache[key]; found {
		m.mu.Unlock()
		return cached.value, nil
	}
	if call, found := m.inflight[key]; found {
		if err := call.ctx.Err(); err != nil {
			m.mu.Unlock()
			return analysisReadSnapshot{}, err
		}
		call.consumers++
		m.mu.Unlock()
		return m.wait(ctx, call)
	}
	if m.pending >= analysisMaxPending {
		m.mu.Unlock()
		return analysisReadSnapshot{}, errAnalysisBusy
	}
	calculationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	call := &analysisReadCall{done: make(chan struct{}), ctx: calculationCtx, cancel: cancel, consumers: 1}
	m.pending++
	generation := m.generation
	m.inflight[key] = call
	m.mu.Unlock()

	go func() {
		defer cancel()
		select {
		case m.slots <- struct{}{}:
			if call.err = calculationCtx.Err(); call.err == nil {
				call.value, call.err = calculate(calculationCtx)
			}
			<-m.slots
		case <-calculationCtx.Done():
			call.err = calculationCtx.Err()
		}
		m.mu.Lock()
		m.pending--
		if call.err == nil {
			call.err = calculationCtx.Err()
		}
		if generation != m.generation {
			call.value = analysisReadSnapshot{}
			call.err = errDeploymentDerivedStateChanged
		}
		if m.inflight[key] == call {
			delete(m.inflight, key)
		}
		if call.err == nil {
			m.makeRoom()
			m.cache[key] = analysisReadCacheEntry{created: time.Now(), value: call.value}
		}
		close(call.done)
		m.mu.Unlock()
	}()
	return m.wait(ctx, call)
}

func (m *analysisReadMemo) wait(ctx context.Context, call *analysisReadCall) (analysisReadSnapshot, error) {
	defer func() {
		m.mu.Lock()
		call.consumers--
		if call.consumers == 0 {
			call.cancel()
		}
		m.mu.Unlock()
	}()
	return waitForAnalysisRead(ctx, call)
}

func (m *analysisReadMemo) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.generation++
	clear(m.cache)
	for _, call := range m.inflight {
		call.cancel()
	}
	m.inflight = make(map[analysisReadKey]*analysisReadCall)
}

func waitForAnalysisRead(ctx context.Context, call *analysisReadCall) (analysisReadSnapshot, error) {
	select {
	case <-ctx.Done():
		return analysisReadSnapshot{}, ctx.Err()
	case <-call.done:
		return call.value, call.err
	}
}

func (m *analysisReadMemo) removeExpired(now time.Time) {
	for key, entry := range m.cache {
		if now.Sub(entry.created) >= analysisReadCacheTTL {
			delete(m.cache, key)
		}
	}
}

func (m *analysisReadMemo) makeRoom() {
	if len(m.cache) < analysisReadCacheMaxEntries {
		return
	}
	var oldestKey analysisReadKey
	var oldestTime time.Time
	for key, entry := range m.cache {
		if oldestTime.IsZero() || entry.created.Before(oldestTime) {
			oldestKey, oldestTime = key, entry.created
		}
	}
	delete(m.cache, oldestKey)
}

func (s *Service) sharedAnalysisRead(ctx context.Context, requestedEnd, trendStart, trendEnd int64, trendReady bool) (analysisReadSnapshot, error) {
	s.analysisReadMemoOnce.Do(func() { s.analysisReadMemo = newAnalysisReadMemo() })
	endSecond := analysisObservationEnd(requestedEnd)
	key := analysisReadKey{endSecond: endSecond, trendStart: trendStart, trendEnd: trendEnd, trendReady: trendReady}
	return s.analysisReadMemo.get(ctx, key, func(readCtx context.Context) (analysisReadSnapshot, error) {
		plan := newAnalysisReadPlan(endSecond, 0)
		if trendReady {
			plan.addInterval(trendStart, trendEnd)
			plan.addCoverage(trendStart+1, trendEnd)
		}
		var supplements []reconcile.MinuteSupplement
		supplementStatus := "UNAVAILABLE"
		if s.repository != nil {
			firstSecond := endSecond - marketanalysis.RollingWindowSec + 1
			firstMinute := domain.UnixSecond(firstSecond - floorMod(firstSecond, reconcile.MinuteSeconds))
			// Exclude the still-open minute even if an early reconciliation row
			// was persisted. Read official evidence before the atomic live view.
			lastMinute := domain.UnixSecond((endSecond+1)/reconcile.MinuteSeconds*reconcile.MinuteSeconds - reconcile.MinuteSeconds)
			var supplementErr error
			supplements, supplementErr = s.repository.RecoveredMinuteSupplements(readCtx, firstMinute, lastMinute)
			if supplementErr != nil {
				if s.logger != nil {
					s.logger.Warn("official minute gap supplement unavailable", "error", supplementErr)
				}
			} else {
				supplementStatus = "READY"
			}
		}
		for _, row := range supplements {
			if validSupplementMinute(row, endSecond) {
				start := int64(row.MinuteStart)
				plan.addInterval(start-1, start+reconcile.MinuteSeconds-1)
			}
		}
		view, err := plan.read(readCtx, s.runtime)
		if err != nil {
			return analysisReadSnapshot{}, err
		}
		view.supplementStatus = supplementStatus
		view.minuteSupplements, view.supplementConflictCount = rebaseMinuteSupplements(view, supplements, endSecond)
		operational := s.captureAnalysisOperational(time.Now())
		rollingBase := buildRollingMonitor(view, endSecond)
		rolling := s.snapshotRollingMonitor(rollingBase)
		if rolling.DataQuality == nil {
			rolling.DataQuality = make(map[string]any)
		}
		rolling.DataQuality["aggregation_version"] = view.version
		rolling.DataQuality["as_of_kind"] = "LAST_CLOSED_SECOND_BOUNDARY"
		rolling.DataQuality["confirmation_semantics"] = "TRANSPORT_COVERAGE_ONLY"
		rolling.DataQuality["official_reconciled"] = false
		windowSupplements := minuteSupplementsByWindow(view, endSecond)
		return analysisReadSnapshot{
			endSecond:   endSecond,
			view:        view,
			operational: operational,
			windows: map[string]map[int64]marketanalysis.WindowState{
				"KRW":                    s.analysisWindowsWithSupplements(view, endSecond, "KRW", operational.health, windowSupplements),
				"ALL_DOMESTIC_CONVERTED": s.analysisWindowsWithSupplements(view, endSecond, "ALL_DOMESTIC_CONVERTED", operational.health, windowSupplements),
			},
			rollingBase: rollingBase,
			rolling:     rolling,
			changes:     map[string]*lazyWindowChanges{"KRW": newLazyWindowChanges(), "ALL_DOMESTIC_CONVERTED": newLazyWindowChanges()},
			coinShares:  newCoinSharesMemo(),
		}, nil
	})
}

func analysisObservationEnd(requestedEnd int64) int64 {
	if requestedEnd <= 0 {
		return requestedEnd
	}
	// Align the boundary (end+1), not the inclusive final bucket. Therefore an
	// exact minute boundary keeps bucket :59 and immediately exposes the minute
	// that just closed instead of delaying it until the next five-second frame.
	remainder := requestedEnd % analysisObservationStepSec
	if remainder == analysisObservationStepSec-1 {
		return requestedEnd
	}
	return requestedEnd - remainder - 1
}

func (p *analysisReadPlan) addInterval(start, end int64) {
	key := readIntervalKey{start: start, end: end}
	if _, exists := p.intervalIndex[key]; exists {
		return
	}
	p.intervalIndex[key] = len(p.intervals)
	p.intervals = append(p.intervals, aggregate.IntervalRequest{
		Start: domain.UnixSecond(start),
		End:   domain.UnixSecond(end),
	})
}

func (p *analysisReadPlan) addCoverage(start, end int64) {
	key := readIntervalKey{start: start, end: end}
	if _, exists := p.coverageIndex[key]; exists {
		return
	}
	p.coverageIndex[key] = len(p.coverage)
	p.coverage = append(p.coverage, coverageInterval{start: start, end: end})
}

func (p *analysisReadPlan) addGlobalCoverage(start, end int64) {
	key := readIntervalKey{start: start, end: end}
	if _, exists := p.globalCoverageIndex[key]; exists {
		return
	}
	p.globalCoverageIndex[key] = len(p.coverage)
	p.coverage = append(p.coverage, coverageInterval{start: start, end: end, global: true})
}

func (p *analysisReadPlan) read(ctx context.Context, runtime *Runtime) (analysisReadView, error) {
	result, err := runtime.atomicRead(ctx, p.intervals, p.coverage)
	if err != nil {
		return analysisReadView{}, err
	}
	for index, interval := range result.intervals {
		if !interval.OK {
			return analysisReadView{}, fmt.Errorf("atomic analysis interval %d is invalid", index)
		}
	}
	return analysisReadView{
		version: result.version, intervals: result.intervals, coverage: result.coverage,
		intervalIndex: p.intervalIndex, coverageIndex: p.coverageIndex,
		globalCoverageIndex: p.globalCoverageIndex, coverageState: result.coverageState,
	}, nil
}

func floorMod(value, divisor int64) int64 {
	remainder := value % divisor
	if remainder < 0 {
		return remainder + divisor
	}
	return remainder
}

func (v analysisReadView) globalCoverageFor(start, end int64) coverage.Result {
	index, ok := v.globalCoverageIndex[readIntervalKey{start: start, end: end}]
	if !ok {
		panic(fmt.Sprintf("global analysis coverage [%d,%d] was not planned", start, end))
	}
	return v.coverage[index]
}

func (v analysisReadView) interval(start, end int64) map[aggregate.VolumeKey]aggregate.Totals {
	index, ok := v.intervalIndex[readIntervalKey{start: start, end: end}]
	if !ok {
		panic(fmt.Sprintf("analysis interval (%d,%d] was not planned", start, end))
	}
	return v.intervals[index].Values
}

func (v analysisReadView) coverageFor(start, end int64) coverage.Result {
	index, ok := v.coverageIndex[readIntervalKey{start: start, end: end}]
	if !ok {
		panic(fmt.Sprintf("analysis coverage [%d,%d] was not planned", start, end))
	}
	return v.coverage[index]
}
