package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"market-share-go/internal/aggregate"
	marketanalysis "market-share-go/internal/analysis"
	"market-share-go/internal/domain"
	officialdata "market-share-go/services/collector/official"
)

// CoinShares lists Bithumb's share of the most traded coins over the official
// 24 hours and over one realtime window. The window comes from the shared
// analysis read, so it matches that window's own analysis. Each read builds
// one full-length list per scope and window, however often it is asked for.
func (s *Service) CoinShares(ctx context.Context, window int64, marketScope string, limit int) (marketanalysis.CoinShares, error) {
	if !aggregate.SupportsWindow(window) {
		return marketanalysis.CoinShares{}, ErrUnsupportedWindow
	}
	if marketScope != "KRW" && marketScope != "ALL_DOMESTIC_CONVERTED" {
		return marketanalysis.CoinShares{}, ErrUnsupportedMarketScope
	}
	if s.runtime == nil || s.registry == nil {
		return marketanalysis.CoinShares{}, ErrStopped
	}
	snapshot, err := s.currentAnalysisRead(ctx)
	if err != nil {
		return marketanalysis.CoinShares{}, err
	}
	key := coinSharesKey{scope: marketScope, window: window}
	list := snapshot.coinShares.get(key, func() marketanalysis.CoinShares {
		input := marketanalysis.CoinSharesInput{
			MarketScope: marketScope, WindowSec: window, AsOfSecond: snapshot.endSecond,
			Window: snapshot.windows[marketScope][window], Rows: analysisCurrentRows(snapshot, window, marketScope),
			Official24hStatus: marketanalysis.CoinShareUnavailable, Limit: marketanalysis.MaxCoinShareCount,
		}
		if s.officialDomestic != nil {
			ticker := s.officialDomestic.Snapshot()
			if ticker.ReadyExchangeCount > 0 {
				input.Official24hStatus = ticker.Status
			}
			if rows, ok := officialTickerCoinRows(ticker); ok {
				input.OfficialRows, input.Official24hAsOf = rows, oldestTickerAsOf(ticker)
			}
		}
		if input.OfficialRows == nil {
			input.Realtime24hRows = analysisCurrentRows(snapshot, marketanalysis.RollingWindowSec, marketScope)
		}
		return marketanalysis.BuildCoinShares(input)
	})
	return list.FirstCoins(limit), nil
}

// LiveCoinShares is the WebSocket's newest frame of Bithumb's share of the
// most traded coins in the last minute. The WebSocket asks several times a
// second; each call moves on by at most one second, so every second is sent
// once and in order, even after a slow read, and never ahead of the applied
// journal. A stream that has fallen far behind starts again from the newest
// second. After a failure the same second is tried again a second later, so
// a failing read is not repeated at the asking rate. Caller cancellation is
// not shared with the next subscriber or retained as a read failure.
func (s *Service) LiveCoinShares(ctx context.Context) (marketanalysis.LiveCoinShares, error) {
	if err := ctx.Err(); err != nil {
		return marketanalysis.LiveCoinShares{}, err
	}
	s.liveCoins.mu.Lock()
	defer s.liveCoins.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return marketanalysis.LiveCoinShares{}, err
	}
	if s.runtime == nil || s.registry == nil {
		return marketanalysis.LiveCoinShares{}, ErrStopped
	}
	now := time.Now()
	if s.liveCoins.err != nil && now.Before(s.liveCoins.retryAt) {
		return marketanalysis.LiveCoinShares{}, s.liveCoins.err
	}
	frame, err := s.nextLiveCoinFrame(ctx, now)
	if err := ctx.Err(); err != nil {
		return marketanalysis.LiveCoinShares{}, err
	}
	s.liveCoins.err, s.liveCoins.retryAt = err, now.Add(time.Second)
	if err != nil {
		return marketanalysis.LiveCoinShares{}, err
	}
	s.liveCoins.frame = frame
	return frame, nil
}

// errNoLiveCoinSecond: no second has been applied yet, so there is nothing
// to send (a calculator that has just started).
var errNoLiveCoinSecond = errors.New("no applied second to read yet")

// nextLiveCoinFrame is the frame for the second after the last one sent, or
// the last frame again while no new second is applied.
func (s *Service) nextLiveCoinFrame(ctx context.Context, now time.Time) (marketanalysis.LiveCoinShares, error) {
	newest, err := s.newestAppliedSecond(ctx, now)
	if err != nil {
		return marketanalysis.LiveCoinShares{}, err
	}
	second, due := nextLiveCoinSecond(s.liveCoins.frame.AsOfSecond, newest)
	switch {
	case due:
		return s.readLiveCoinShares(ctx, second)
	case s.liveCoins.frame.AsOfSecond == 0:
		return marketanalysis.LiveCoinShares{}, errNoLiveCoinSecond
	default:
		return s.liveCoins.frame, nil
	}
}

// liveCoinCatchUpSec is how far behind a live coin-share stream steps one
// second at a time; further behind, it starts again from the newest second.
const liveCoinCatchUpSec = 10

// nextLiveCoinSecond is the second to send after last, given the newest
// applied second; false while there is none.
func nextLiveCoinSecond(last, newest int64) (int64, bool) {
	switch {
	case newest <= last:
		return 0, false
	case last == 0 || newest-last > liveCoinCatchUpSec:
		return newest, true
	default:
		return last + 1, true
	}
}

// liveCoinStream is the last frame LiveCoinShares gave, and the last call's
// failure with when to try again.
type liveCoinStream struct {
	mu      sync.Mutex
	frame   marketanalysis.LiveCoinShares
	err     error
	retryAt time.Time
}

// readLiveCoinShares gives the coins of the shared analysis read's list, in
// its order, their shares of the minute ending at the closed second end. The
// list changes at most every five seconds, so each coin keeps its place.
func (s *Service) readLiveCoinShares(ctx context.Context, end int64) (marketanalysis.LiveCoinShares, error) {
	const scope, window = "KRW", marketanalysis.LiveCoinShareWindowSec
	list, err := s.CoinShares(ctx, window, scope, marketanalysis.LiveCoinShareCount)
	if err != nil {
		return marketanalysis.LiveCoinShares{}, err
	}
	read, err := s.runtime.atomicReadIntervals(ctx, []aggregate.IntervalRequest{{
		Start: domain.UnixSecond(end - window), End: domain.UnixSecond(end),
	}}, []coverageInterval{{start: end - window + 1, end: end}})
	if err != nil {
		return marketanalysis.LiveCoinShares{}, err
	}
	if len(read.intervals) != 1 || len(read.coverage) != 1 || !read.intervals[0].OK {
		return marketanalysis.LiveCoinShares{}, errors.New("live coin-share read is incomplete")
	}
	coverage := read.coverage[0]
	return marketanalysis.BuildLiveCoinShares(marketanalysis.LiveCoinSharesInput{
		Coins: list.Coins, Ranking: list.Ranking,
		Rows:      analysisRows(filterAnalysisValues(read.intervals[0].Values, scope)),
		WindowSec: window, AsOfSecond: end,
		WindowComplete: coverage.WindowComplete, CoverageSec: min(window, coverage.CoverageSec),
	}), nil
}

// oldestTickerAsOf is when the earliest of the summary's exchange tickers
// was received, so a late exchange's last values are not dated as new.
func oldestTickerAsOf(summary officialdata.DomesticTickerSummary) float64 {
	oldest := summary.AsOf
	for _, row := range summary.Exchanges {
		if row.AsOf > 0 && row.AsOf < oldest {
			oldest = row.AsOf
		}
	}
	return oldest
}

type coinSharesKey struct {
	scope  string
	window int64
}

// coinSharesMemo keeps one analysis read's coin-share lists, at most one per
// scope and window, each built in a few milliseconds. The lists are shared
// by every caller and must not be changed.
type coinSharesMemo struct {
	mu    sync.Mutex
	lists map[coinSharesKey]marketanalysis.CoinShares
}

func newCoinSharesMemo() *coinSharesMemo {
	return &coinSharesMemo{lists: make(map[coinSharesKey]marketanalysis.CoinShares)}
}

func (m *coinSharesMemo) get(key coinSharesKey, compute func() marketanalysis.CoinShares) marketanalysis.CoinShares {
	m.mu.Lock()
	defer m.mu.Unlock()
	list, found := m.lists[key]
	if !found {
		list = compute()
		m.lists[key] = list
	}
	return list
}
