package main

import (
	"context"
	"market-share-go/internal/analysis"
	"market-share-go/internal/indexday"

	"market-share-go/internal/transport/httpapi"
	"time"
)

// The standalone collector must expose the same current dashboard projections
// as the split calculator, without proxying them to an external deployment.
var (
	_ httpapi.WindowChangesProvider  = serviceAPI{}
	_ httpapi.CoinSharesProvider     = serviceAPI{}
	_ httpapi.ShareSeriesProvider    = serviceAPI{}
	_ httpapi.ClosedSecondsProvider  = serviceAPI{}
	_ httpapi.LiveCoinSharesProvider = serviceAPI{}
	_ httpapi.TickReferenceProvider  = serviceAPI{}
)

func (s serviceAPI) WindowChanges(ctx context.Context, scope string) (analysis.WindowChanges, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.service.WindowChanges(ctx, scope)
}
func (s serviceAPI) CoinShares(ctx context.Context, window int64, scope string, limit int) (analysis.CoinShares, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.service.CoinShares(ctx, window, scope, limit)
}
func (s serviceAPI) ShareSeries(ctx context.Context, bucketSec int64, scope string) (analysis.ShareSeries, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.service.ShareSeries(ctx, bucketSec, scope)
}
func (s serviceAPI) ClosedSeconds(ctx context.Context, window int64, scope string) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	seconds, err := s.service.ClosedSeconds(ctx, window, scope)
	if err != nil {
		return nil, err
	}
	return seconds, nil
}
func (s serviceAPI) LiveCoinShares(ctx context.Context) (analysis.LiveCoinShares, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.service.LiveCoinShares(ctx)
}
func (s serviceAPI) RealtimeTickReferences() analysis.TickReferences {
	return s.service.RealtimeTickReferences()
}

func (s serviceAPI) IndexDay(ctx context.Context, day string) (indexday.Response, error) {
	return s.service.IndexDay(ctx, day)
}
func (s serviceAPI) IndexFilterAnalysis(ctx context.Context, window int64, scope string) (analysis.Analysis, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.service.IndexFilterAnalysis(ctx, window, scope)
}

func (s serviceAPI) RealtimeIndex(ctx context.Context, window int64, filter indexday.RealtimeFilter) (indexday.RealtimeResponse, error) {
	return s.service.RealtimeIndex(ctx, window, filter)
}
