package app

import (
	"context"

	officialdata "market-share-go/contracts/collection/official"
	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/notice"
)

// IndexFilterAnalysis returns only the inputs used by the index filter
// projection: ordered assets, contextual notice labels and their observation
// boundary. It shares the analysis read without computing books or studies.
func (s *Service) IndexFilterAnalysis(ctx context.Context, window int64, scope string) (marketanalysis.Analysis, error) {
	if s.intelligenceEvidence != nil {
		return s.Analysis(ctx, window, scope, 0)
	}
	if !aggregate.SupportsWindow(window) {
		return marketanalysis.Analysis{}, ErrUnsupportedWindow
	}
	if scope != "KRW" && scope != "ALL_DOMESTIC_CONVERTED" {
		return marketanalysis.Analysis{}, ErrUnsupportedMarketScope
	}
	if s.runtime == nil || s.registry == nil {
		return marketanalysis.Analysis{}, ErrStopped
	}
	s.analysisMemoOnce.Do(func() { s.analysisMemo = newAnalysisMemo() })
	key := analysisKey{window: window, scope: scope, indexFilters: true}
	return s.analysisMemo.get(ctx, key, func(calculationCtx context.Context) (marketanalysis.Analysis, error) {
		return s.calculateIndexFilterAnalysis(calculationCtx, window, scope)
	})
}

func (s *Service) calculateIndexFilterAnalysis(ctx context.Context, window int64, scope string) (marketanalysis.Analysis, error) {
	snapshot, err := s.currentAnalysisRead(ctx)
	if err != nil {
		return marketanalysis.Analysis{}, err
	}
	var ticker officialdata.DomesticTickerSummary
	if scope == "KRW" && window == marketanalysis.RollingWindowSec && s.officialDomestic != nil {
		ticker = s.officialDomestic.Snapshot()
	}
	notices, err := s.NoticeSnapshot(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Warn("index filter notice snapshot unavailable", "error", err)
		}
		notices = notice.Snapshot{}
	}
	return buildIndexFilterAnalysis(ctx, snapshot, window, scope, ticker, notices)
}

func buildIndexFilterAnalysis(ctx context.Context, snapshot analysisReadSnapshot, window int64, scope string, ticker officialdata.DomesticTickerSummary, notices notice.Snapshot) (marketanalysis.Analysis, error) {
	if err := ctx.Err(); err != nil {
		return marketanalysis.Analysis{}, err
	}
	rows := analysisCurrentRows(snapshot, window, scope)
	windows := snapshot.windows[scope]
	if scope == "KRW" && window == marketanalysis.RollingWindowSec {
		if officialWindow, officialRows, ok := officialTicker24hProjection(ticker); ok {
			windows = cloneAnalysisWindows(windows)
			windows[window] = officialWindow
			rows = replaceDomesticAnalysisRows(rows, officialRows)
		}
	}
	// Reuse canonicalization and asset ranking. Registry-only enumeration would
	// lose row-only assets and change the competitor-volume ordering.
	result, err := marketanalysis.BuildUnfinalizedContext(ctx, marketanalysis.BuildInput{
		Window: window, Rows: rows, DomesticWindows: windows,
		Markets: filterAnalysisMarkets(snapshot.operational.universe.Markets(), scope),
	})
	if err != nil {
		return marketanalysis.Analysis{}, err
	}
	result.GeneratedAt = float64(snapshot.endSecond + 1)
	filtered := filterMarketReactionSnapshot(notices, int64(result.GeneratedAt*1000), window)
	result.Notices, err = marketanalysis.ContextualizeNoticesWithProviders(result, filtered, result.GeneratedAt, marketanalysis.ReactionProviders{})
	if err != nil {
		return marketanalysis.Analysis{}, err
	}
	if err := ctx.Err(); err != nil {
		return marketanalysis.Analysis{}, err
	}
	return result, nil
}
