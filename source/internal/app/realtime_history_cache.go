package app

import (
	"context"
	"sync"
	"time"

	marketanalysis "market-share-go/internal/analysis"
)

const (
	realtimeHistoryCacheTTL        = 5 * time.Second
	realtimeHistoryCacheMaxEntries = 64
)

type realtimeHistoryEntry struct {
	created time.Time
	value   []marketanalysis.RealtimeFlowState
}

type realtimeHistoryCall struct {
	done  chan struct{}
	value []marketanalysis.RealtimeFlowState
	err   error
}

type realtimeHistoryMemo struct {
	mu       sync.Mutex
	cache    map[marketanalysis.RealtimeFlowHistoryFilter]realtimeHistoryEntry
	inflight map[marketanalysis.RealtimeFlowHistoryFilter]*realtimeHistoryCall
}

func newRealtimeHistoryMemo() *realtimeHistoryMemo {
	return &realtimeHistoryMemo{
		cache:    make(map[marketanalysis.RealtimeFlowHistoryFilter]realtimeHistoryEntry),
		inflight: make(map[marketanalysis.RealtimeFlowHistoryFilter]*realtimeHistoryCall),
	}
}

func (m *realtimeHistoryMemo) get(
	ctx context.Context,
	filter marketanalysis.RealtimeFlowHistoryFilter,
	load func(context.Context) ([]marketanalysis.RealtimeFlowState, error),
) ([]marketanalysis.RealtimeFlowState, error) {
	now := time.Now()
	m.mu.Lock()
	for key, entry := range m.cache {
		if now.Sub(entry.created) >= realtimeHistoryCacheTTL {
			delete(m.cache, key)
		}
	}
	if cached, ok := m.cache[filter]; ok {
		m.mu.Unlock()
		return cloneRealtimeHistory(cached.value), nil
	}
	if call, ok := m.inflight[filter]; ok {
		m.mu.Unlock()
		return waitForRealtimeHistory(ctx, call)
	}
	call := &realtimeHistoryCall{done: make(chan struct{})}
	m.inflight[filter] = call
	m.mu.Unlock()

	queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	go func() {
		defer cancel()
		call.value, call.err = load(queryCtx)
		m.mu.Lock()
		delete(m.inflight, filter)
		if call.err == nil {
			if len(m.cache) >= realtimeHistoryCacheMaxEntries {
				var oldestKey marketanalysis.RealtimeFlowHistoryFilter
				var oldestTime time.Time
				for key, entry := range m.cache {
					if oldestTime.IsZero() || entry.created.Before(oldestTime) {
						oldestKey, oldestTime = key, entry.created
					}
				}
				delete(m.cache, oldestKey)
			}
			m.cache[filter] = realtimeHistoryEntry{created: time.Now(), value: call.value}
		}
		close(call.done)
		m.mu.Unlock()
	}()
	return waitForRealtimeHistory(ctx, call)
}

func waitForRealtimeHistory(ctx context.Context, call *realtimeHistoryCall) ([]marketanalysis.RealtimeFlowState, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-call.done:
		return cloneRealtimeHistory(call.value), call.err
	}
}

func cloneRealtimeHistory(value []marketanalysis.RealtimeFlowState) []marketanalysis.RealtimeFlowState {
	return append([]marketanalysis.RealtimeFlowState(nil), value...)
}
