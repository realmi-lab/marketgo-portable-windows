package app

import (
	"context"
	"sync"
	"time"

	"market-share-go/services/collector/registry"
)

const (
	registryHistoryCacheTTL        = 10 * time.Second
	registryHistoryCacheMaxEntries = 32
)

type registryHistoryEntry struct {
	created time.Time
	value   registry.History
}

type registryHistoryCall struct {
	done  chan struct{}
	value registry.History
	err   error
}

type registryHistoryMemo struct {
	mu       sync.Mutex
	cache    map[registry.HistoryFilter]registryHistoryEntry
	inflight map[registry.HistoryFilter]*registryHistoryCall
}

func newRegistryHistoryMemo() *registryHistoryMemo {
	return &registryHistoryMemo{
		cache:    make(map[registry.HistoryFilter]registryHistoryEntry),
		inflight: make(map[registry.HistoryFilter]*registryHistoryCall),
	}
}

func (m *registryHistoryMemo) get(
	ctx context.Context,
	filter registry.HistoryFilter,
	load func(context.Context) (registry.History, error),
) (registry.History, error) {
	now := time.Now()
	m.mu.Lock()
	m.removeExpired(now)
	if cached, ok := m.cache[filter]; ok {
		m.mu.Unlock()
		return cloneRegistryHistory(cached.value), nil
	}
	if call, ok := m.inflight[filter]; ok {
		m.mu.Unlock()
		return waitForRegistryHistory(ctx, call)
	}
	call := &registryHistoryCall{done: make(chan struct{})}
	m.inflight[filter] = call
	m.mu.Unlock()

	queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	go func() {
		defer cancel()
		call.value, call.err = load(queryCtx)
		m.mu.Lock()
		delete(m.inflight, filter)
		if call.err == nil {
			m.makeRoom()
			m.cache[filter] = registryHistoryEntry{created: time.Now(), value: cloneRegistryHistory(call.value)}
		}
		close(call.done)
		m.mu.Unlock()
	}()
	return waitForRegistryHistory(ctx, call)
}

func (m *registryHistoryMemo) removeExpired(now time.Time) {
	for key, entry := range m.cache {
		if now.Sub(entry.created) >= registryHistoryCacheTTL {
			delete(m.cache, key)
		}
	}
}

func (m *registryHistoryMemo) makeRoom() {
	if len(m.cache) < registryHistoryCacheMaxEntries {
		return
	}
	var oldestKey registry.HistoryFilter
	var oldestTime time.Time
	for key, entry := range m.cache {
		if oldestTime.IsZero() || entry.created.Before(oldestTime) {
			oldestKey, oldestTime = key, entry.created
		}
	}
	delete(m.cache, oldestKey)
}

func waitForRegistryHistory(ctx context.Context, call *registryHistoryCall) (registry.History, error) {
	select {
	case <-ctx.Done():
		return registry.History{}, ctx.Err()
	case <-call.done:
		return cloneRegistryHistory(call.value), call.err
	}
}

func cloneRegistryHistory(source registry.History) registry.History {
	result := registry.History{
		Refreshes:   append([]registry.RefreshObservation(nil), source.Refreshes...),
		Events:      append([]registry.LifecycleEvent(nil), source.Events...),
		GeneratedAt: source.GeneratedAt,
	}
	for index := range result.Refreshes {
		counts := source.Refreshes[index].QuoteCounts
		if counts == nil {
			continue
		}
		result.Refreshes[index].QuoteCounts = make(map[string]int, len(counts))
		for quote, count := range counts {
			result.Refreshes[index].QuoteCounts[quote] = count
		}
	}
	for index := range result.Events {
		result.Events[index].MakerFee = cloneFloat64(source.Events[index].MakerFee)
		result.Events[index].TakerFee = cloneFloat64(source.Events[index].TakerFee)
	}
	return result
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
