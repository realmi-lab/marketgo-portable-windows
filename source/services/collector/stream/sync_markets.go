package collector

import (
	"fmt"
	"reflect"
	"sort"

	"market-share-go/internal/domain"
	"market-share-go/services/collector/exchange"
)

// SyncMarkets keeps unchanged socket chunks alive. Only removed/changed chunks
// are replaced, and newly listed symbols get their own paced subscriptions.
// Callers must supply a complete, validated last-known-good exchange membership.
func (m *Manager) SyncMarkets(name domain.Exchange, markets []domain.MarketPair) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if !name.Valid() {
		return fmt.Errorf("unsupported exchange %q", name)
	}
	normalized, err := normalizeMarkets(name, markets)
	if err != nil {
		return err
	}
	m.mu.Lock()
	previous := make(map[string]domain.MarketPair)
	for _, row := range m.markets[name] {
		previous[row.Symbol] = row
	}
	pending := make(map[string]domain.MarketPair)
	for _, row := range normalized {
		pending[row.Symbol] = row
	}
	var keep, stop []*chunkRunner
	for _, runner := range m.runners[name] {
		snapshot := runner.state.copy()
		unchanged := m.enabled[name] && m.ctx.Err() == nil
		for _, symbol := range snapshot.MarketSymbols {
			row, ok := pending[symbol]
			if !ok || !reflect.DeepEqual(row, previous[symbol]) {
				unchanged = false
				break
			}
		}
		if unchanged {
			keep = append(keep, runner)
			for _, symbol := range snapshot.MarketSymbols {
				delete(pending, symbol)
			}
		} else {
			stop = append(stop, runner)
		}
	}
	// Do not accumulate one connection per listing forever. Keep one spare
	// chunk per quote beyond a packed membership, consolidating the smallest
	// chunks only when this budget would be exceeded.
	quotes := make(map[string]bool)
	for _, row := range normalized {
		quotes[row.Quote] = true
	}
	budget := len(quoteAwareChunks(normalized, exchange.ChunkSizes[name])) + len(quotes)
	pendingRows := func() []domain.MarketPair {
		var rows []domain.MarketPair
		for _, row := range normalized {
			if _, ok := pending[row.Symbol]; ok {
				rows = append(rows, row)
			}
		}
		return rows
	}
	if len(keep)+len(quoteAwareChunks(pendingRows(), exchange.ChunkSizes[name])) > budget {
		sort.SliceStable(keep, func(i, j int) bool {
			return len(keep[i].state.copy().MarketSymbols) < len(keep[j].state.copy().MarketSymbols)
		})
		for len(keep) > 0 && len(keep)+len(quoteAwareChunks(pendingRows(), exchange.ChunkSizes[name])) > budget {
			runner := keep[0]
			keep = keep[1:]
			for _, symbol := range runner.state.copy().MarketSymbols {
				pending[symbol] = previous[symbol]
			}
			stop = append(stop, runner)
		}
	}
	m.markets[name] = normalized
	m.runners[name] = keep
	m.mu.Unlock()
	stopRunners(stop)
	if !m.enabled[name] || m.ctx.Err() != nil {
		return nil
	}
	used := make(map[int]bool)
	for _, runner := range keep {
		used[runner.state.copy().Chunk] = true
	}
	for _, chunk := range quoteAwareChunks(pendingRows(), exchange.ChunkSizes[name]) {
		id := 1
		for used[id] {
			id++
		}
		used[id] = true
		keep = append(keep, m.startChunk(name, chunk, id))
	}
	m.mu.Lock()
	m.runners[name] = keep
	m.mu.Unlock()
	return nil
}
