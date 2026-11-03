package app

import (
	"context"
	"fmt"

	"market-share-go/internal/tickertrend"
)

func (s *Service) OfficialAlignedHistory(ctx context.Context, fromMS, toMS int64) (tickertrend.History, error) {
	if s == nil || s.tickerTrend == nil {
		return tickertrend.History{}, fmt.Errorf("official aligned history is unavailable")
	}
	return s.tickerTrend.History(ctx, fromMS, toMS)
}

func (s *Service) OfficialAlignedContribution(ctx context.Context, toMS int64) (tickertrend.WindowComparison, error) {
	if s == nil || s.tickerTrend == nil {
		return tickertrend.WindowComparison{}, fmt.Errorf("official aligned contribution is unavailable")
	}
	return s.tickerTrend.CompareAlignedHour(ctx, toMS)
}
