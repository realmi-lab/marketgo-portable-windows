package registry

import (
	"market-share-go/internal/domain"
	"time"
)

// RestoreRemoteSnapshot replaces a read-only registry mirror without REST I/O.
func (r *Registry) RestoreRemoteSnapshot(snapshot Snapshot, markets []domain.MarketPair) error {
	universe, err := domain.NewMarketUniverse(markets)
	if err != nil {
		return err
	}
	grouped := make(map[domain.Exchange][]domain.MarketPair)
	for _, market := range universe.Markets() {
		grouped[market.Exchange] = append(grouped[market.Exchange], market)
	}
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.markets = grouped
	r.errors = make(map[domain.Exchange]string)
	for name, row := range snapshot.Exchanges {
		r.errors[name] = row.Error
	}
	r.version = snapshot.Version
	r.updatedAt = time.Unix(0, int64(snapshot.UpdatedAt*1e9))
	r.universe.Store(&universe)
	return nil
}
