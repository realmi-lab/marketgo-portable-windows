package app

import (
	"context"
	"fmt"
	"market-share-go/internal/config"
	"market-share-go/internal/indexday"
	"strings"
	"time"
)

func (s *Service) IndexDay(ctx context.Context, day string) (indexday.Response, error) {
	if strings.HasPrefix(day, "ticker:") {
		return s.currentTickerIndex(strings.TrimPrefix(day, "ticker:"))
	}
	if s != nil && s.storageMode == config.StorageSQLiteMemory && s.registry != nil {
		return memoryIndexDay(day, s.registry.Universe(), s.officialAlignedDomestic.CachedHourlyCandles(), time.Now())
	}
	if s == nil || s.postgresRepository == nil {
		return indexday.Response{}, fmt.Errorf("stored hourly candles are unavailable")
	}
	return s.postgresRepository.IndexDay(ctx, day)
}
