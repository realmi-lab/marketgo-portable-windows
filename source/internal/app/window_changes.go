package app

import (
	"context"
	"time"

	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
)

// lazyWindowChanges computes one snapshot's cross-window changes once. Callers
// take turns; one that gives up before the first result leaves the work to the
// next caller instead of storing an empty result.
type lazyWindowChanges struct {
	turn  chan struct{} // holds one token while a caller reads or computes
	done  bool
	value marketanalysis.WindowChanges
}

func newLazyWindowChanges() *lazyWindowChanges {
	return &lazyWindowChanges{turn: make(chan struct{}, 1)}
}

func (l *lazyWindowChanges) get(
	ctx context.Context,
	compute func() (marketanalysis.WindowChanges, error),
) (marketanalysis.WindowChanges, error) {
	select {
	case l.turn <- struct{}{}:
	case <-ctx.Done():
		return marketanalysis.WindowChanges{}, ctx.Err()
	}
	defer func() { <-l.turn }()
	if !l.done {
		value, err := compute()
		if err != nil {
			return marketanalysis.WindowChanges{}, err
		}
		l.value, l.done = value, true
	}
	return l.value, nil
}

// WindowChanges compares every rolling window with the window just before it.
// 24 hours is left out because it has no earlier window to compare. All
// windows come from one shared read and are compared exactly as each
// window's own analysis compares them.
func (s *Service) WindowChanges(ctx context.Context, marketScope string) (marketanalysis.WindowChanges, error) {
	if marketScope != "KRW" && marketScope != "ALL_DOMESTIC_CONVERTED" {
		return marketanalysis.WindowChanges{}, ErrUnsupportedMarketScope
	}
	if s.runtime == nil || s.registry == nil {
		return marketanalysis.WindowChanges{}, ErrStopped
	}
	snapshot, err := s.currentAnalysisRead(ctx)
	if err != nil {
		return marketanalysis.WindowChanges{}, err
	}
	// The comparison runs in one of the analyses' slots, so it counts against
	// the same limit on concurrent work.
	s.analysisMemoOnce.Do(func() { s.analysisMemo = newAnalysisMemo() })
	return snapshot.changes[marketScope].get(ctx, func() (marketanalysis.WindowChanges, error) {
		var changes marketanalysis.WindowChanges
		err := s.analysisMemo.runInSlot(ctx, func() { changes = compareEveryWindow(snapshot, marketScope) })
		return changes, err
	})
}

// currentAnalysisRead is the shared read of the last closed observation. It
// uses the same official trend interval as calculateAnalysis, so the two
// share one cached read.
func (s *Service) currentAnalysisRead(ctx context.Context) (analysisReadSnapshot, error) {
	trendStart, trendEnd, trendReady := officialTickerTrendInterval(s.officialTickerTrendState())
	return s.sharedAnalysisRead(ctx, time.Now().Unix()-1, trendStart, trendEnd, trendReady)
}

func compareEveryWindow(snapshot analysisReadSnapshot, marketScope string) marketanalysis.WindowChanges {
	result := marketanalysis.WindowChanges{
		MarketScope: marketScope, AsOfSecond: snapshot.endSecond, Windows: []marketanalysis.WindowChange{},
	}
	for _, window := range aggregate.DefaultWindows {
		if window == marketanalysis.RollingWindowSec {
			continue
		}
		currentRows, previousRows, comparison := analysisWindowRows(snapshot, window, marketScope)
		attribution := marketanalysis.CompareWindows(
			filterAnalysisRowsByExchanges(currentRows, comparison.Included),
			filterAnalysisRowsByExchanges(previousRows, comparison.Included),
			comparison,
		)
		result.Windows = append(result.Windows, marketanalysis.SummarizeWindowChange(window, attribution))
	}
	return result
}
