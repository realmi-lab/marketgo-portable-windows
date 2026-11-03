package app

import (
	"context"

	"market-share-go/services/collector/registry"
)

func (s *Service) RegistryHistory(
	ctx context.Context,
	filter registry.HistoryFilter,
) (registry.History, error) {
	if s.repository == nil {
		return registry.History{}, ErrStopped
	}
	s.registryHistoryMemoOnce.Do(func() { s.registryHistoryMemo = newRegistryHistoryMemo() })
	return s.registryHistoryMemo.get(ctx, filter, func(queryCtx context.Context) (registry.History, error) {
		return s.repository.MarketRegistryHistory(queryCtx, filter)
	})
}
