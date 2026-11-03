package app

import (
	"context"
	"errors"
	"sync"
	"time"

	marketanalysis "market-share-go/internal/analysis"
)

const (
	analysisCacheTTL        = 5 * time.Second
	analysisCacheMaxEntries = 32
	analysisMaxRunning      = 4
	analysisMaxPending      = 12 // running plus queued, including canceled work until it exits
)

var errAnalysisBusy = errors.New("analysis capacity exhausted")

type analysisKey struct {
	// Filter projections share admission, cancellation and reset fencing, but
	// must never satisfy a full-analysis request from the response cache.
	indexFilters          bool
	window                int64
	scope                 string
	notional              float64
	reactionPreWindowSec  int64
	reactionPostWindowSec int64
}

type analysisCacheEntry struct {
	created time.Time
	value   marketanalysis.Analysis
}

type analysisCall struct {
	done      chan struct{}
	value     marketanalysis.Analysis
	err       error
	cancel    context.CancelFunc
	ctx       context.Context
	consumers int
}

type analysisMemo struct {
	mu         sync.Mutex
	cache      map[analysisKey]analysisCacheEntry
	inflight   map[analysisKey]*analysisCall
	generation uint64
	slots      chan struct{}
	pending    int
}

func newAnalysisMemo() *analysisMemo {
	return &analysisMemo{
		cache:    make(map[analysisKey]analysisCacheEntry),
		inflight: make(map[analysisKey]*analysisCall),
		slots:    make(chan struct{}, analysisMaxRunning),
	}
}

func (m *analysisMemo) get(
	ctx context.Context,
	key analysisKey,
	calculate func(context.Context) (marketanalysis.Analysis, error),
) (marketanalysis.Analysis, error) {
	if err := ctx.Err(); err != nil {
		return marketanalysis.Analysis{}, err
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
			return marketanalysis.Analysis{}, err
		}
		call.consumers++
		m.mu.Unlock()
		return m.wait(ctx, call)
	}
	if m.pending >= analysisMaxPending {
		m.mu.Unlock()
		return marketanalysis.Analysis{}, errAnalysisBusy
	}
	calculationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	call := &analysisCall{done: make(chan struct{}), ctx: calculationCtx, cancel: cancel, consumers: 1}
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
			call.value = marketanalysis.Analysis{}
			call.err = errDeploymentDerivedStateChanged
		}
		if m.inflight[key] == call {
			delete(m.inflight, key)
		}
		if call.err == nil {
			m.makeRoom()
			// Queueing and calculation consume this request's freshness budget.
			// Starting a new TTL here would pin an already old observation for
			// another five seconds after a slow calculation completes.
			m.cache[key] = analysisCacheEntry{created: now, value: call.value}
		}
		close(call.done)
		m.mu.Unlock()
	}()
	return m.wait(ctx, call)
}

// runInSlot runs work in one of the running-analysis slots, or returns the
// context's error if it ends first.
func (m *analysisMemo) runInSlot(ctx context.Context, work func()) error {
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
		if err := ctx.Err(); err != nil {
			return err
		}
		work()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *analysisMemo) wait(ctx context.Context, call *analysisCall) (marketanalysis.Analysis, error) {
	defer func() {
		m.mu.Lock()
		call.consumers--
		if call.consumers == 0 {
			call.cancel()
		}
		m.mu.Unlock()
	}()
	return waitForAnalysis(ctx, call)
}

func (m *analysisMemo) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.generation++
	clear(m.cache)
	for _, call := range m.inflight {
		call.cancel()
	}
	m.inflight = make(map[analysisKey]*analysisCall)
}

func waitForAnalysis(ctx context.Context, call *analysisCall) (marketanalysis.Analysis, error) {
	select {
	case <-ctx.Done():
		return marketanalysis.Analysis{}, ctx.Err()
	case <-call.done:
		return call.value, call.err
	}
}

func (m *analysisMemo) removeExpired(now time.Time) {
	for key, entry := range m.cache {
		if now.Sub(entry.created) >= analysisCacheTTL {
			delete(m.cache, key)
		}
	}
}

func (m *analysisMemo) makeRoom() {
	if len(m.cache) < analysisCacheMaxEntries {
		return
	}
	var oldestKey analysisKey
	var oldestTime time.Time
	for key, entry := range m.cache {
		if oldestTime.IsZero() || entry.created.Before(oldestTime) {
			oldestKey, oldestTime = key, entry.created
		}
	}
	delete(m.cache, oldestKey)
}
