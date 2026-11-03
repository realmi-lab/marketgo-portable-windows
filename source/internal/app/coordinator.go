package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"market-share-go/internal/domain"
	"market-share-go/internal/fx"
	"market-share-go/services/collector/registry"
)

type MarketCollector interface {
	ReplaceMarkets(domain.Exchange, []domain.MarketPair) error
}

type MarketRegistryStore interface {
	SaveMarketRegistryObservation(
		context.Context,
		registry.Snapshot,
		domain.MarketUniverse,
		[]domain.Exchange,
	) error
}

type RegistryCoordinator struct {
	registry  *registry.Registry
	collector MarketCollector
	store     MarketRegistryStore
	oracle    *fx.Oracle
	enabled   []domain.Exchange
	interval  time.Duration
	logger    *slog.Logger
	syncMu    sync.Mutex
	pending   map[domain.Exchange]struct{}
}

func NewRegistryCoordinator(
	registry *registry.Registry,
	collector MarketCollector,
	store MarketRegistryStore,
	oracle *fx.Oracle,
	enabled []domain.Exchange,
	interval time.Duration,
	logger *slog.Logger,
) *RegistryCoordinator {
	pending := make(map[domain.Exchange]struct{}, len(enabled))
	for _, exchange := range enabled {
		pending[exchange] = struct{}{}
	}
	return &RegistryCoordinator{
		registry: registry, collector: collector, store: store, oracle: oracle,
		enabled:  append([]domain.Exchange(nil), enabled...),
		interval: interval, logger: logger, pending: pending,
	}
}

func (c *RegistryCoordinator) Sync(ctx context.Context) error {
	c.syncMu.Lock()
	defer c.syncMu.Unlock()

	changed, err := c.registry.Refresh(ctx, c.enabled)
	if err != nil {
		return err
	}
	snapshot, universe := c.registry.View()
	c.oracle.Configure(universe)
	for _, name := range changed {
		c.pending[name] = struct{}{}
	}
	var failures []error
	for _, name := range c.pendingExchanges() {
		if err := c.collector.ReplaceMarkets(name, universe.ForExchange(name)); err != nil {
			failures = append(failures, fmt.Errorf("replace %s markets: %w", name, err))
			continue
		}
		delete(c.pending, name)
	}
	if err := c.store.SaveMarketRegistryObservation(ctx, snapshot, universe, c.enabled); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (c *RegistryCoordinator) pendingExchanges() []domain.Exchange {
	result := make([]domain.Exchange, 0, len(c.pending))
	for _, exchange := range c.enabled {
		if _, pending := c.pending[exchange]; pending {
			result = append(result, exchange)
		}
	}
	return result
}

func (c *RegistryCoordinator) Run(ctx context.Context) {
	if c.interval <= 0 {
		return
	}
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.Sync(ctx); err != nil && ctx.Err() == nil {
				c.logger.Error("market registry refresh failed", "error", err)
			}
		}
	}
}
