package official

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"market-share-go/internal/domain"
)

// HistoricalAlignedSnapshots reconstructs every rolling-24h endpoint between
// firstWindowEnd and lastWindowEnd, inclusive. Bounded one-minute pages are
// validated and rolled into UTC hours once per market, then reused by endpoints.
func (s *AlignedCandleService) HistoricalAlignedSnapshots(
	ctx context.Context,
	firstWindowEnd time.Time,
	lastWindowEnd time.Time,
) ([]DomesticTickerSummary, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("aligned candle service is not configured")
	}
	firstWindowEnd = firstWindowEnd.UTC()
	lastWindowEnd = lastWindowEnd.UTC()
	if firstWindowEnd.IsZero() || lastWindowEnd.Before(firstWindowEnd) ||
		!firstWindowEnd.Equal(firstWindowEnd.Truncate(time.Hour)) ||
		!lastWindowEnd.Equal(lastWindowEnd.Truncate(time.Hour)) {
		return nil, errors.New("historical aligned candle endpoints must be ordered UTC hour boundaries")
	}
	endpointHours := int(lastWindowEnd.Sub(firstWindowEnd) / time.Hour)
	requestCount := endpointHours + Aligned24hBarCount + 1
	if requestCount > alignedCandleMaximumRequestCount {
		return nil, fmt.Errorf(
			"historical aligned candle range needs %d rows per market; maximum is %d",
			requestCount, alignedCandleMaximumRequestCount,
		)
	}

	started := time.Now()
	type result struct {
		exchange domain.Exchange
		rows     map[int64]TickerSnapshot
		err      error
	}
	results := make(chan result, len(AlignedShareExchanges))
	var wait sync.WaitGroup
	for _, exchange := range AlignedShareExchanges {
		wait.Add(1)
		go func(name domain.Exchange) {
			defer wait.Done()
			rows, err := s.fetchHistoricalAlignedExchange(
				ctx, name, firstWindowEnd, lastWindowEnd, requestCount,
			)
			results <- result{exchange: name, rows: rows, err: err}
		}(exchange)
	}
	wait.Wait()
	close(results)

	byExchange := make(map[domain.Exchange]map[int64]TickerSnapshot, len(AlignedShareExchanges))
	for item := range results {
		if item.err != nil {
			return nil, item.err
		}
		byExchange[item.exchange] = item.rows
	}
	completed := time.Now()
	snapshots := make([]DomesticTickerSummary, 0, endpointHours+1)
	for windowEnd := firstWindowEnd; !windowEnd.After(lastWindowEnd); windowEnd = windowEnd.Add(time.Hour) {
		rows := make(map[domain.Exchange]TickerSnapshot, len(AlignedShareExchanges))
		for _, exchange := range AlignedShareExchanges {
			row, found := byExchange[exchange][windowEnd.UnixMilli()]
			if !found {
				return nil, fmt.Errorf("%s historical aligned endpoint %s is missing", exchange, windowEnd.Format(time.RFC3339))
			}
			rows[exchange] = row
		}
		snapshot := aggregateAlignedCandles(rows, windowEnd.Add(-24*time.Hour), windowEnd, started, completed)
		if snapshot.Status != "LIVE" {
			return nil, fmt.Errorf("historical aligned endpoint %s is not LIVE", windowEnd.Format(time.RFC3339))
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func (s *AlignedCandleService) fetchHistoricalAlignedExchange(
	ctx context.Context,
	exchange domain.Exchange,
	firstWindowEnd time.Time,
	lastWindowEnd time.Time,
	requestCount int,
) (map[int64]TickerSnapshot, error) {
	markets, err := s.expectedMarkets(exchange)
	if err != nil {
		return nil, err
	}
	marketCandles := make([]map[int64]MinuteCandle, len(markets))
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	workerCount := min(alignedCandleWorkersPerExchange, len(markets))
	var workers sync.WaitGroup
	var firstErr error
	var errorOnce sync.Once
	for worker := 0; worker < workerCount; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				values, fetchErr := s.fetchHistoricalAlignedMarket(
					workerCtx, markets[index], firstWindowEnd, lastWindowEnd, requestCount,
				)
				if fetchErr != nil {
					errorOnce.Do(func() {
						firstErr = fmt.Errorf("%s %s historical aligned candles: %w", exchange, markets[index].Symbol, fetchErr)
						cancel()
					})
					return
				}
				marketCandles[index] = values
			}
		}()
	}
sendJobs:
	for index := range markets {
		select {
		case jobs <- index:
		case <-workerCtx.Done():
			break sendJobs
		}
	}
	close(jobs)
	workers.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	rows := make(map[int64]TickerSnapshot, int(lastWindowEnd.Sub(firstWindowEnd)/time.Hour)+1)
	for windowEnd := firstWindowEnd; !windowEnd.After(lastWindowEnd); windowEnd = windowEnd.Add(time.Hour) {
		windowEndMS := windowEnd.UnixMilli()
		row := TickerSnapshot{
			Exchange: exchange, Status: "LIVE", Source: historicalMinuteHourSource(exchange),
			MarketCount: len(markets), AsOf: float64(windowEnd.Unix()), ExchangeTimestampMS: &windowEndMS,
			Assets: map[string]float64{}, ClosePricesKRW: map[string]float64{},
			Markets: map[string]struct{}{}, MarketTimestampsMS: map[string]int64{},
			ReceivedAt: time.Now(), WindowStartMS: windowEnd.Add(-24 * time.Hour).UnixMilli(),
			WindowEndMS: windowEndMS, BarIntervalSec: int64(time.Hour / time.Second),
			RequiredBarCount: Aligned24hBarCount,
		}
		total := 0.0
		for index, market := range markets {
			volume := alignedHistoricalWindowTotal(marketCandles[index], row.WindowStartMS, row.WindowEndMS)
			if !finiteNonnegative(volume) {
				return nil, fmt.Errorf("%s %s historical aligned total is invalid", exchange, market.Symbol)
			}
			total += volume
			if math.IsInf(total, 0) {
				return nil, fmt.Errorf("%s historical aligned total overflow", exchange)
			}
			asset := strings.ToUpper(strings.TrimSpace(market.Base))
			row.Assets[asset] += volume
			if closeCandle, found := marketCandles[index][windowEndMS-AlignedHourMS]; found {
				row.ClosePricesKRW[asset] = closeCandle.Close
			}
			marketID := CanonicalKRWMarketID(asset)
			row.Markets[marketID] = struct{}{}
			row.MarketTimestampsMS[marketID] = windowEndMS
		}
		row.VolumeKRW = floatPointer(total)
		rows[windowEndMS] = row
	}
	return rows, nil
}

func (s *AlignedCandleService) fetchHistoricalAlignedMarket(
	ctx context.Context,
	market domain.MarketPair,
	firstWindowEnd time.Time,
	lastWindowEnd time.Time,
	requestCount int,
) (map[int64]MinuteCandle, error) {
	return s.fetchMinuteDerivedHours(ctx, market, firstWindowEnd.Add(-24*time.Hour).UnixMilli(), lastWindowEnd.UnixMilli())
}

func alignedHistoricalWindowTotal(values map[int64]MinuteCandle, startMS, endMS int64) float64 {
	total := 0.0
	for hourMS := startMS; hourMS < endMS; hourMS += AlignedHourMS {
		candle, found := values[hourMS]
		if !found || candle.QuoteVolumeKRW == nil {
			continue
		}
		total += *candle.QuoteVolumeKRW
	}
	return total
}
