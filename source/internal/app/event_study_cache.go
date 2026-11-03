package app

import (
	"context"
	"strings"
	"sync"
	"time"

	"market-share-go/internal/aggregate"
	"market-share-go/internal/coverage"
	"market-share-go/internal/domain"
)

const (
	eventStudyCacheTTL        = 15 * time.Second
	eventStudyCacheMaxEntries = 512
)

type eventStudyKey struct {
	start          int64
	end            int64
	observationEnd int64
	asset          string
	marketScope    string
}

type eventStudyCacheEntry struct {
	created time.Time
	value   eventStudyRead
}

type eventStudyCall struct {
	done      chan struct{}
	value     eventStudyRead
	err       error
	cancel    context.CancelFunc
	ctx       context.Context
	consumers int
}

type eventStudyMemo struct {
	mu         sync.Mutex
	cache      map[eventStudyKey]eventStudyCacheEntry
	inflight   map[eventStudyKey]*eventStudyCall
	generation uint64
	slots      chan struct{}
	pending    int
}

func newEventStudyMemo() *eventStudyMemo {
	return &eventStudyMemo{
		cache:    make(map[eventStudyKey]eventStudyCacheEntry),
		inflight: make(map[eventStudyKey]*eventStudyCall),
		slots:    make(chan struct{}, analysisMaxRunning),
	}
}

func (m *eventStudyMemo) get(
	ctx context.Context,
	key eventStudyKey,
	calculate func(context.Context) (eventStudyRead, error),
) (eventStudyRead, error) {
	if err := ctx.Err(); err != nil {
		return eventStudyRead{}, err
	}
	now := time.Now()
	m.mu.Lock()
	m.removeExpired(now)
	if cached, found := m.cache[key]; found {
		m.mu.Unlock()
		return cloneEventStudyRead(cached.value), nil
	}
	if call, found := m.inflight[key]; found {
		if err := call.ctx.Err(); err != nil {
			m.mu.Unlock()
			return eventStudyRead{}, err
		}
		call.consumers++
		m.mu.Unlock()
		return m.wait(ctx, call)
	}
	if m.pending >= analysisMaxPending {
		m.mu.Unlock()
		return eventStudyRead{}, errAnalysisBusy
	}
	calculationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	call := &eventStudyCall{done: make(chan struct{}), ctx: calculationCtx, cancel: cancel, consumers: 1}
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
			call.value = eventStudyRead{}
			call.err = errDeploymentDerivedStateChanged
		}
		if m.inflight[key] == call {
			delete(m.inflight, key)
		}
		if call.err == nil {
			m.makeRoom()
			m.cache[key] = eventStudyCacheEntry{created: time.Now(), value: cloneEventStudyRead(call.value)}
		}
		close(call.done)
		m.mu.Unlock()
	}()
	return m.wait(ctx, call)
}

func (m *eventStudyMemo) wait(ctx context.Context, call *eventStudyCall) (eventStudyRead, error) {
	defer func() {
		m.mu.Lock()
		call.consumers--
		if call.consumers == 0 {
			call.cancel()
		}
		m.mu.Unlock()
	}()
	return waitForEventStudy(ctx, call)
}

func (m *eventStudyMemo) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.generation++
	clear(m.cache)
	for _, call := range m.inflight {
		call.cancel()
	}
	m.inflight = make(map[eventStudyKey]*eventStudyCall)
}

func waitForEventStudy(ctx context.Context, call *eventStudyCall) (eventStudyRead, error) {
	select {
	case <-ctx.Done():
		return eventStudyRead{}, ctx.Err()
	case <-call.done:
		return cloneEventStudyRead(call.value), call.err
	}
}

func (m *eventStudyMemo) removeExpired(now time.Time) {
	for key, entry := range m.cache {
		if now.Sub(entry.created) >= eventStudyCacheTTL {
			delete(m.cache, key)
		}
	}
}

func (m *eventStudyMemo) makeRoom() {
	if len(m.cache) < eventStudyCacheMaxEntries {
		return
	}
	var oldestKey eventStudyKey
	var oldestTime time.Time
	for key, entry := range m.cache {
		if oldestTime.IsZero() || entry.created.Before(oldestTime) {
			oldestKey, oldestTime = key, entry.created
		}
	}
	delete(m.cache, oldestKey)
}

func cloneEventStudyRead(value eventStudyRead) eventStudyRead {
	value.buckets = append([]aggregate.Bucket(nil), value.buckets...)
	value.domesticCoverage = cloneCoverageResult(value.domesticCoverage)
	value.globalCoverage = cloneCoverageResult(value.globalCoverage)
	return value
}

func cloneCoverageResult(value coverage.Result) coverage.Result {
	value.RecoveredGapIDs = append([]int64(nil), value.RecoveredGapIDs...)
	return value
}

func (s *Service) sharedEventStudy(
	ctx context.Context,
	start domain.UnixSecond,
	end domain.UnixSecond,
	observationEnd int64,
	asset string,
	marketScope string,
) (eventStudyRead, error) {
	s.eventStudyMemoOnce.Do(func() { s.eventStudyMemo = newEventStudyMemo() })
	asset = strings.ToUpper(strings.TrimSpace(asset))
	marketScope = strings.ToUpper(strings.TrimSpace(marketScope))
	key := eventStudyKey{
		start: int64(start), end: int64(end), observationEnd: observationEnd,
		asset: asset, marketScope: marketScope,
	}
	return s.eventStudyMemo.get(ctx, key, func(loadCtx context.Context) (eventStudyRead, error) {
		return s.runtime.eventStudy(loadCtx, start, end, asset, marketScope)
	})
}
