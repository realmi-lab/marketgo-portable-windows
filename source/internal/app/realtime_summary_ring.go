package app

import (
	"context"
	"sort"
	"sync"
	"time"

	marketanalysis "market-share-go/internal/analysis"
)

const (
	realtimeSummaryRingMaxStates = 9_001
	realtimeSummaryLoadTimeout   = 10 * time.Second
	realtimeSummaryCacheTTL      = 20 * time.Second
	realtimeSummaryCacheEntries  = 6
)

type realtimeSummaryLoadCall struct {
	done chan struct{}
	err  error
}

// realtimeSummaryRing has one application writer and immutable reader
// snapshots. PostgreSQL hydrates it once after startup; each successful
// ten-second persistence commit then appends the exact stored projection.
type realtimeSummaryRing struct {
	mu            sync.Mutex
	states        []marketanalysis.RealtimeFlowState
	loaded        bool
	coveredFrom   int64
	coveredTo     int64
	loading       *realtimeSummaryLoadCall
	memoryHits    uint64
	databaseLoads uint64
	generation    uint64
}

func newRealtimeSummaryRing() *realtimeSummaryRing {
	return &realtimeSummaryRing{states: []marketanalysis.RealtimeFlowState{}}
}

func (s *Service) initRealtimeSummaryState() {
	s.realtimeSummaryOnce.Do(func() {
		s.realtimeSummaryRing = newRealtimeSummaryRing()
		s.realtimePeriodSummaryMemo = newRealtimePeriodSummaryCache()
	})
}

func (r *realtimeSummaryRing) history(
	ctx context.Context,
	fromMS, toMS int64,
	load func(context.Context) ([]marketanalysis.RealtimeFlowState, error),
) ([]marketanalysis.RealtimeFlowState, error) {
	for {
		r.mu.Lock()
		if r.loaded && fromMS >= r.coveredFrom && toMS <= r.coveredTo {
			r.memoryHits++
			result := r.snapshotLocked(fromMS, toMS)
			r.mu.Unlock()
			return result, nil
		}
		if r.loading != nil {
			call := r.loading
			r.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-call.done:
				if call.err != nil {
					return nil, call.err
				}
				continue
			}
		}
		call := &realtimeSummaryLoadCall{done: make(chan struct{})}
		generation := r.generation
		r.loading = call
		r.databaseLoads++
		r.mu.Unlock()

		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), realtimeSummaryLoadTimeout)
		loaded, err := load(loadCtx)
		cancel()

		r.mu.Lock()
		if generation != r.generation {
			err = errDeploymentDerivedStateChanged
		}
		var result []marketanalysis.RealtimeFlowState
		if err == nil {
			r.mergeLocked(loaded)
			// An older requested day may be outside the bounded ring after
			// pruning. Return its complete query result before applying retention.
			result = r.snapshotLocked(fromMS, toMS)
			if !r.loaded || fromMS > r.coveredTo || toMS < r.coveredFrom {
				// Disjoint queries do not establish what lies between them.
				r.coveredFrom, r.coveredTo = fromMS, toMS
			} else {
				r.coveredFrom = min(r.coveredFrom, fromMS)
				r.coveredTo = max(r.coveredTo, toMS)
			}
			r.loaded = true
			r.pruneLocked()
		}
		call.err = err
		if r.loading == call {
			r.loading = nil
		}
		close(call.done)
		if err != nil {
			r.mu.Unlock()
			return nil, err
		}
		r.mu.Unlock()
		return result, nil
	}
}

func (r *realtimeSummaryRing) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.generation++
	r.states = nil
	r.loaded = false
	r.coveredFrom, r.coveredTo = 0, 0
	r.loading = nil
}

func (r *realtimeSummaryRing) metrics() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return map[string]any{
		"loaded": r.loaded, "state_count": len(r.states),
		"covered_from_ms": r.coveredFrom, "covered_to_ms": r.coveredTo,
		"memory_hits": r.memoryHits, "database_loads": r.databaseLoads,
	}
}

func (r *realtimeSummaryRing) observe(state marketanalysis.RealtimeFlowState) {
	if !realtimeSummaryAligned(state.AsOfMS) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	index := sort.Search(len(r.states), func(index int) bool {
		return r.states[index].AsOfMS >= state.AsOfMS
	})
	if index < len(r.states) && r.states[index].AsOfMS == state.AsOfMS {
		r.states[index] = state
	} else {
		r.states = append(r.states, marketanalysis.RealtimeFlowState{})
		copy(r.states[index+1:], r.states[index:])
		r.states[index] = state
	}
	if r.loaded && state.AsOfMS > r.coveredTo &&
		state.AsOfMS-r.coveredTo <= marketanalysis.RealtimeFlowSummarySampleSec*1_000 {
		r.coveredTo = state.AsOfMS
	}
	r.pruneLocked()
}

func (r *realtimeSummaryRing) snapshotLocked(fromMS, toMS int64) []marketanalysis.RealtimeFlowState {
	start := sort.Search(len(r.states), func(index int) bool {
		return r.states[index].AsOfMS > fromMS
	})
	end := sort.Search(len(r.states), func(index int) bool {
		return r.states[index].AsOfMS > toMS
	})
	return append([]marketanalysis.RealtimeFlowState(nil), r.states[start:end]...)
}

func (r *realtimeSummaryRing) mergeLocked(incoming []marketanalysis.RealtimeFlowState) {
	byTime := make(map[int64]marketanalysis.RealtimeFlowState, len(r.states)+len(incoming))
	for _, state := range r.states {
		byTime[state.AsOfMS] = state
	}
	for _, state := range incoming {
		if !realtimeSummaryAligned(state.AsOfMS) {
			continue
		}
		byTime[state.AsOfMS] = state
	}
	times := make([]int64, 0, len(byTime))
	for asOfMS := range byTime {
		times = append(times, asOfMS)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	r.states = make([]marketanalysis.RealtimeFlowState, 0, len(times))
	for _, asOfMS := range times {
		r.states = append(r.states, byTime[asOfMS])
	}
}

func realtimeSummaryAligned(asOfMS int64) bool {
	return asOfMS > 0 && asOfMS%(marketanalysis.RealtimeFlowSummarySampleSec*1_000) == 0
}

func (r *realtimeSummaryRing) pruneLocked() {
	if len(r.states) <= realtimeSummaryRingMaxStates {
		return
	}
	drop := len(r.states) - realtimeSummaryRingMaxStates
	retained := append([]marketanalysis.RealtimeFlowState(nil), r.states[drop:]...)
	r.states = retained
	oldestCovered := r.states[0].AsOfMS - marketanalysis.RealtimeFlowSummarySampleSec*1_000
	if r.loaded && oldestCovered > r.coveredFrom {
		r.coveredFrom = oldestCovered
		if r.coveredFrom >= r.coveredTo {
			r.loaded = false
		}
	}
}

type realtimePeriodSummaryCacheItem struct {
	value     marketanalysis.RealtimeFlowPeriodSummary
	expiresAt time.Time
}

type realtimePeriodSummaryCall struct {
	done  chan struct{}
	value marketanalysis.RealtimeFlowPeriodSummary
	err   error
}

type realtimePeriodSummaryCache struct {
	mu           sync.Mutex
	entries      map[int64]realtimePeriodSummaryCacheItem
	inFlight     map[int64]*realtimePeriodSummaryCall
	order        []int64
	hits         uint64
	misses       uint64
	inflightHits uint64
	generation   uint64
}

func newRealtimePeriodSummaryCache() *realtimePeriodSummaryCache {
	return &realtimePeriodSummaryCache{
		entries:  make(map[int64]realtimePeriodSummaryCacheItem),
		inFlight: make(map[int64]*realtimePeriodSummaryCall),
	}
}

func (c *realtimePeriodSummaryCache) get(
	ctx context.Context,
	toMS int64,
	load func(context.Context) (marketanalysis.RealtimeFlowPeriodSummary, error),
) (marketanalysis.RealtimeFlowPeriodSummary, error) {
	now := time.Now()
	c.mu.Lock()
	if item, ok := c.entries[toMS]; ok && now.Before(item.expiresAt) {
		c.hits++
		c.touchLocked(toMS)
		c.mu.Unlock()
		return item.value, nil
	}
	c.removeLocked(toMS)
	if call := c.inFlight[toMS]; call != nil {
		c.inflightHits++
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return marketanalysis.RealtimeFlowPeriodSummary{}, ctx.Err()
		case <-call.done:
			return call.value, call.err
		}
	}
	call := &realtimePeriodSummaryCall{done: make(chan struct{})}
	generation := c.generation
	c.misses++
	c.inFlight[toMS] = call
	c.mu.Unlock()

	call.value, call.err = load(ctx)

	c.mu.Lock()
	if generation != c.generation {
		call.value = marketanalysis.RealtimeFlowPeriodSummary{}
		call.err = errDeploymentDerivedStateChanged
	}
	current := c.inFlight[toMS] == call
	if current {
		delete(c.inFlight, toMS)
	}
	// A persistence commit can detach this load while it runs. Its original
	// readers may finish, but it must not replace a post-commit cached result.
	if current && call.err == nil {
		c.removeLocked(toMS)
		c.entries[toMS] = realtimePeriodSummaryCacheItem{
			value: call.value, expiresAt: time.Now().Add(realtimeSummaryCacheTTL),
		}
		c.order = append(c.order, toMS)
		for len(c.order) > realtimeSummaryCacheEntries {
			c.evictOldestLocked()
		}
	}
	close(call.done)
	c.mu.Unlock()
	return call.value, call.err
}

func (c *realtimePeriodSummaryCache) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	clear(c.entries)
	c.order = nil
	c.inFlight = make(map[int64]*realtimePeriodSummaryCall)
}

func (c *realtimePeriodSummaryCache) metrics() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return map[string]any{
		"entries": len(c.entries), "hits": c.hits, "misses": c.misses,
		"inflight_hits": c.inflightHits,
	}
}

func (c *realtimePeriodSummaryCache) invalidateFrom(asOfMS int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Later readers must start from the committed state rather than join a
	// calculation that began before it. Older, unaffected periods stay shared.
	for toMS := range c.inFlight {
		if toMS >= asOfMS {
			delete(c.inFlight, toMS)
		}
	}
	retained := c.order[:0]
	for _, toMS := range c.order {
		if toMS >= asOfMS {
			delete(c.entries, toMS)
			continue
		}
		retained = append(retained, toMS)
	}
	c.order = retained
}

func (c *realtimePeriodSummaryCache) touchLocked(toMS int64) {
	for index, key := range c.order {
		if key != toMS {
			continue
		}
		copy(c.order[index:], c.order[index+1:])
		c.order[len(c.order)-1] = toMS
		return
	}
}

func (c *realtimePeriodSummaryCache) removeLocked(toMS int64) {
	delete(c.entries, toMS)
	for index, key := range c.order {
		if key != toMS {
			continue
		}
		copy(c.order[index:], c.order[index+1:])
		c.order = c.order[:len(c.order)-1]
		return
	}
}

func (c *realtimePeriodSummaryCache) evictOldestLocked() {
	if len(c.order) == 0 {
		return
	}
	oldest := c.order[0]
	c.order = c.order[1:]
	delete(c.entries, oldest)
}
