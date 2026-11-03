package official

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"market-share-go/internal/domain"
	"market-share-go/services/collector/restbudget"
)

const (
	AlignedHourMS                   = int64(time.Hour / time.Millisecond)
	Aligned24hBarCount              = 24
	Aligned24hWindowMS              = AlignedHourMS * Aligned24hBarCount
	Aligned24hSource                = "OFFICIAL_DOMESTIC_ALIGNED_1H_CANDLES_24H"
	DefaultAlignedCandleRefresh     = time.Hour
	alignedCandleCloseGrace         = 90 * time.Second
	alignedCandleRetryDelay         = 5 * time.Minute
	alignedCandleRefreshTimeout     = 30 * time.Minute
	alignedCandleSnapshotStaleAfter = time.Hour + 15*time.Minute
	alignedCandleResponseLimit      = int64(4 << 20)
	// A closed 24-hour window can contain at most 24 one-hour candles. One
	// extra row tolerates an API that includes its boundary candle without
	// downloading the former 200-row history for every active market.
	alignedCandleBootstrapRequestCount = Aligned24hBarCount + 1
	alignedCandleMaximumRequestCount   = 200
	alignedCandleWorkersPerExchange    = 4
	alignedCandleFullRefreshInterval   = 24 * time.Hour
	alignedCandleUpdateBuffer          = 1
)

var AlignedShareExchanges = [...]domain.Exchange{
	domain.ExchangeUpbit,
	domain.ExchangeBithumb,
	domain.ExchangeCoinone,
}

var AlignedShareExcludedExchanges = map[domain.Exchange]string{
	domain.ExchangeKorbit: "OFFICIAL_CANDLE_QUOTE_VOLUME_UNAVAILABLE",
	domain.ExchangeGopax:  "OFFICIAL_CANDLE_QUOTE_VOLUME_UNAVAILABLE",
}

var alignedHourCandleURLs = map[domain.Exchange]string{
	domain.ExchangeUpbit:   "https://api.upbit.com/v1/candles/minutes/60",
	domain.ExchangeBithumb: "https://api.bithumb.com/v1/candles/minutes/60",
	domain.ExchangeCoinone: "https://api.coinone.co.kr/public/v2/chart/{quote}/{base}",
}

type AlignedCandleService struct {
	client                  TickerJSONClient
	expectedMarketsProvider func(domain.Exchange) []domain.MarketPair

	refreshMu sync.Mutex
	cacheMu   sync.Mutex
	cache     map[alignedMarketCacheKey]alignedMarketWindow
	mu        sync.RWMutex
	snapshot  DomesticTickerSummary
	cancel    context.CancelFunc
	wait      sync.WaitGroup
	running   bool

	updateMu         sync.Mutex
	updates          chan DomesticTickerSummary
	updatesPublished uint64
	updatesCoalesced uint64
}

func NewAlignedCandleService(client TickerJSONClient) *AlignedCandleService {
	return &AlignedCandleService{
		client:   client,
		snapshot: emptyAlignedCandleSummary(),
		updates:  make(chan DomesticTickerSummary, alignedCandleUpdateBuffer),
		cache:    make(map[alignedMarketCacheKey]alignedMarketWindow),
	}
}

type alignedMarketCacheKey struct {
	exchange domain.Exchange
	market   string
}

// alignedMarketWindow retains only the non-overlapping hourly values needed
// to advance one market's rolling 24-hour total. The cache is an optimization:
// every value still comes from the same official candle response and can be
// discarded and rebuilt without changing the published calculation.
type alignedMarketWindow struct {
	windowEndMS      int64
	fullRefreshEndMS int64
	values           map[int64]float64
	closePriceKRW    *float64
}

type alignedMarketResult struct {
	volumeKRW     float64
	closePriceKRW *float64
}

func (s *AlignedCandleService) SetExpectedMarketsProvider(provider func(domain.Exchange) []domain.MarketPair) {
	s.mu.Lock()
	s.expectedMarketsProvider = provider
	s.mu.Unlock()
}

func (s *AlignedCandleService) Start(parent context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel, s.running = cancel, true
	s.wait.Add(1)
	s.mu.Unlock()
	go s.run(ctx)
}

func (s *AlignedCandleService) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	cancel := s.cancel
	s.running = false
	s.mu.Unlock()
	cancel()
	s.wait.Wait()
}

func (s *AlignedCandleService) Snapshot() DomesticTickerSummary {
	return s.snapshotAt(time.Now())
}

func (s *AlignedCandleService) snapshotAt(now time.Time) DomesticTickerSummary {
	s.mu.RLock()
	result := cloneTickerSummary(s.snapshot)
	s.mu.RUnlock()
	if result.WindowEndMS <= 0 || now.UnixMilli()-result.WindowEndMS <= alignedCandleSnapshotStaleAfter.Milliseconds() {
		return result
	}
	if result.Status == "LIVE" {
		result.Status = "STALE"
	}
	for exchange, row := range result.Exchanges {
		if row.Status == "LIVE" {
			row.Status = "STALE"
			result.Exchanges[exchange] = row
		}
	}
	return result
}

func (s *AlignedCandleService) Updates() <-chan DomesticTickerSummary { return s.updates }

func (s *AlignedCandleService) UpdateStats() TickerUpdateStats {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	return TickerUpdateStats{
		Published: s.updatesPublished,
		Coalesced: s.updatesCoalesced,
		Pending:   len(s.updates),
	}
}

func (s *AlignedCandleService) Refresh(ctx context.Context) DomesticTickerSummary {
	return s.refreshAndPublish(ctx, ctx)
}

func (s *AlignedCandleService) refreshAndPublish(fetchCtx, publishCtx context.Context) DomesticTickerSummary {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	snapshot := s.refresh(fetchCtx, time.Now())
	s.publishUpdate(publishCtx, snapshot)
	return cloneTickerSummary(snapshot)
}

func (s *AlignedCandleService) refresh(ctx context.Context, now time.Time) DomesticTickerSummary {
	started := time.Now()
	windowEnd := now.UTC().Truncate(time.Hour)
	windowStart := windowEnd.Add(-24 * time.Hour)
	type result struct {
		exchange domain.Exchange
		row      TickerSnapshot
		err      error
	}
	results := make(chan result, len(AlignedShareExchanges))
	var wait sync.WaitGroup
	for _, exchange := range AlignedShareExchanges {
		wait.Add(1)
		go func(name domain.Exchange) {
			defer wait.Done()
			row, err := s.fetchExchange(ctx, name, windowStart, windowEnd)
			results <- result{exchange: name, row: row, err: err}
		}(exchange)
	}
	wait.Wait()
	close(results)
	rows := make(map[domain.Exchange]TickerSnapshot, len(AlignedShareExchanges))
	for item := range results {
		if item.err != nil {
			message := item.err.Error()
			item.row.Status = "UNAVAILABLE"
			item.row.LastError = &message
		}
		rows[item.exchange] = item.row
	}
	completed := time.Now()
	snapshot := aggregateAlignedCandles(rows, windowStart, windowEnd, started, completed)
	s.mu.Lock()
	s.snapshot = cloneTickerSummary(snapshot)
	s.mu.Unlock()
	return snapshot
}

func (s *AlignedCandleService) fetchExchange(
	ctx context.Context,
	exchange domain.Exchange,
	windowStart time.Time,
	windowEnd time.Time,
) (TickerSnapshot, error) {
	markets, err := s.expectedMarkets(exchange)
	row := TickerSnapshot{
		Exchange: exchange, Status: "UNAVAILABLE",
		Source: alignedCandleSource(exchange), AsOf: float64(windowEnd.Unix()),
		WindowStartMS: windowStart.UnixMilli(), WindowEndMS: windowEnd.UnixMilli(),
		BarIntervalSec: int64(time.Hour / time.Second), RequiredBarCount: Aligned24hBarCount,
		Assets: map[string]float64{}, ClosePricesKRW: map[string]float64{},
		Markets: map[string]struct{}{}, MarketTimestampsMS: map[string]int64{},
	}
	if err != nil {
		return row, err
	}
	s.pruneAlignedMarketCache(exchange, markets)
	row.MarketCount = len(markets)
	volumes := make([]float64, len(markets))
	closePrices := make([]*float64, len(markets))
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
				market := markets[index]
				marketResult, fetchErr := s.fetchMarketResult(workerCtx, market, windowStart.UnixMilli(), windowEnd.UnixMilli())
				if fetchErr != nil {
					errorOnce.Do(func() {
						firstErr = fmt.Errorf("%s %s aligned candles: %w", exchange, market.Symbol, fetchErr)
						cancel()
					})
					return
				}
				if !finiteNonnegative(marketResult.volumeKRW) {
					errorOnce.Do(func() {
						firstErr = fmt.Errorf("%s %s aligned candle total is invalid", exchange, market.Symbol)
						cancel()
					})
					return
				}
				volumes[index] = marketResult.volumeKRW
				closePrices[index] = copyTickerFloat(marketResult.closePriceKRW)
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
		return row, firstErr
	}
	if err := ctx.Err(); err != nil {
		return row, err
	}

	// Sum in registry order rather than completion order so bounded parallel
	// fetching cannot change the last floating-point bits of the result.
	total := 0.0
	for index, market := range markets {
		volume := volumes[index]
		total += volume
		if math.IsInf(total, 0) {
			return row, fmt.Errorf("%s aligned candle total overflow", exchange)
		}
		asset := strings.ToUpper(strings.TrimSpace(market.Base))
		row.Assets[asset] += volume
		if closePrice := closePrices[index]; closePrice != nil {
			row.ClosePricesKRW[asset] = *closePrice
		}
		marketID := CanonicalKRWMarketID(asset)
		row.Markets[marketID] = struct{}{}
		row.MarketTimestampsMS[marketID] = windowEnd.UnixMilli()
	}
	row.Status = "LIVE"
	row.VolumeKRW = floatPointer(total)
	windowEndMS := windowEnd.UnixMilli()
	row.ExchangeTimestampMS = &windowEndMS
	row.ReceivedAt = time.Now()
	return row, nil
}

func (s *AlignedCandleService) expectedMarkets(exchange domain.Exchange) ([]domain.MarketPair, error) {
	s.mu.RLock()
	provider := s.expectedMarketsProvider
	s.mu.RUnlock()
	if provider == nil {
		return nil, errors.New("aligned candle market registry is not configured")
	}
	provided := provider(exchange)
	markets := make([]domain.MarketPair, 0, len(provided))
	seen := make(map[string]struct{}, len(provided))
	for _, market := range provided {
		if market.Exchange != exchange || market.Scope != domain.ScopeDomestic || !strings.EqualFold(market.Quote, "KRW") {
			continue
		}
		key := market.Key()
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("%s aligned candle registry contains duplicate market %s", exchange, market.Symbol)
		}
		seen[key] = struct{}{}
		markets = append(markets, market)
	}
	if len(markets) == 0 {
		return nil, fmt.Errorf("%s aligned candle registry has no active KRW markets", exchange)
	}
	sort.Slice(markets, func(i, j int) bool { return markets[i].Symbol < markets[j].Symbol })
	return markets, nil
}

func (s *AlignedCandleService) fetchMarket(
	ctx context.Context,
	market domain.MarketPair,
	windowStartMS int64,
	windowEndMS int64,
) (float64, error) {
	result, err := s.fetchMarketResult(ctx, market, windowStartMS, windowEndMS)
	return result.volumeKRW, err
}

func (s *AlignedCandleService) fetchMarketResult(
	ctx context.Context,
	market domain.MarketPair,
	windowStartMS int64,
	windowEndMS int64,
) (alignedMarketResult, error) {
	previous, cached := s.alignedMarketCache(market)
	if cached && previous.windowEndMS == windowEndMS {
		return alignedMarketResult{
			volumeKRW:     alignedWindowTotal(previous.values),
			closePriceKRW: copyTickerFloat(previous.closePriceKRW),
		}, nil
	}
	incremental := alignedWindowReusable(previous, cached, windowStartMS, windowEndMS)
	requestCount := alignedCandleBootstrapRequestCount
	if incremental && windowEndMS-previous.fullRefreshEndMS < alignedCandleFullRefreshInterval.Milliseconds() {
		missingHours := int((windowEndMS - previous.windowEndMS) / AlignedHourMS)
		requestCount = min(alignedCandleBootstrapRequestCount, missingHours+2)
	} else {
		incremental = false
	}
	request, err := buildAlignedHourRequest(market, windowEndMS, requestCount)
	if err != nil {
		return alignedMarketResult{}, err
	}
	group := "public"
	if market.Exchange == domain.ExchangeUpbit {
		group = "candle"
	}
	raw, err := restbudget.FetchJSONBytes(ctx, s.client, restbudget.Request{
		Exchange: string(market.Exchange), Group: group, URL: request.URL,
		Query: request.Query, Priority: restbudget.PriorityRoundRobin, Cost: 1,
	}, alignedCandleResponseLimit)
	if err != nil {
		return alignedMarketResult{}, err
	}
	candles, err := parseAlignedHourCandles(market.Exchange, market.Symbol, raw)
	if err != nil {
		return alignedMarketResult{}, err
	}
	values := make(map[int64]float64, Aligned24hBarCount)
	var closePriceKRW *float64
	fullRefreshEndMS := windowEndMS
	if incremental {
		fullRefreshEndMS = previous.fullRefreshEndMS
		for startMS, value := range previous.values {
			if startMS >= windowStartMS && startMS < windowEndMS {
				values[startMS] = value
			}
		}
	}
	for _, candle := range candles {
		if candle.StartMS < windowStartMS || candle.StartMS >= windowEndMS {
			continue
		}
		if candle.StartMS%AlignedHourMS != 0 {
			return alignedMarketResult{}, fmt.Errorf("candle start %d is not aligned to a UTC hour", candle.StartMS)
		}
		value, err := candle.QuoteVolume()
		if err != nil {
			return alignedMarketResult{}, err
		}
		values[candle.StartMS] = value
		if candle.StartMS == windowEndMS-AlignedHourMS {
			closePriceKRW = floatPointer(candle.Close)
		}
	}
	total := alignedWindowTotal(values)
	if math.IsInf(total, 0) {
		return alignedMarketResult{}, errors.New("aligned candle market total overflow")
	}
	s.storeAlignedMarketCache(market, alignedMarketWindow{
		windowEndMS: windowEndMS, fullRefreshEndMS: fullRefreshEndMS, values: values,
		closePriceKRW: copyTickerFloat(closePriceKRW),
	})
	return alignedMarketResult{volumeKRW: total, closePriceKRW: closePriceKRW}, nil
}

func alignedWindowReusable(previous alignedMarketWindow, found bool, windowStartMS, windowEndMS int64) bool {
	if !found || previous.windowEndMS <= 0 || previous.fullRefreshEndMS <= 0 ||
		previous.windowEndMS >= windowEndMS || previous.windowEndMS < windowStartMS ||
		previous.windowEndMS%AlignedHourMS != 0 || windowEndMS%AlignedHourMS != 0 {
		return false
	}
	return (windowEndMS-previous.windowEndMS)%AlignedHourMS == 0
}

func alignedWindowTotal(values map[int64]float64) float64 {
	starts := make([]int64, 0, len(values))
	for startMS := range values {
		starts = append(starts, startMS)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	total := 0.0
	for _, startMS := range starts {
		total += values[startMS]
	}
	return total
}

func (s *AlignedCandleService) alignedMarketCache(market domain.MarketPair) (alignedMarketWindow, bool) {
	key := alignedCacheKey(market)
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	value, found := s.cache[key]
	if !found {
		return alignedMarketWindow{}, false
	}
	copyValue := value
	copyValue.closePriceKRW = copyTickerFloat(value.closePriceKRW)
	copyValue.values = make(map[int64]float64, len(value.values))
	for startMS, volume := range value.values {
		copyValue.values[startMS] = volume
	}
	return copyValue, true
}

func (s *AlignedCandleService) storeAlignedMarketCache(market domain.MarketPair, value alignedMarketWindow) {
	key := alignedCacheKey(market)
	s.cacheMu.Lock()
	s.cache[key] = value
	s.cacheMu.Unlock()
}

func alignedCacheKey(market domain.MarketPair) alignedMarketCacheKey {
	return alignedMarketCacheKey{
		exchange: market.Exchange,
		market:   strings.ToUpper(strings.TrimSpace(market.Symbol)),
	}
}

func (s *AlignedCandleService) pruneAlignedMarketCache(exchange domain.Exchange, markets []domain.MarketPair) {
	active := make(map[alignedMarketCacheKey]struct{}, len(markets))
	for _, market := range markets {
		active[alignedCacheKey(market)] = struct{}{}
	}
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	for key := range s.cache {
		if key.exchange != exchange {
			continue
		}
		if _, found := active[key]; !found {
			delete(s.cache, key)
		}
	}
}

type alignedHourRequest struct {
	URL   string
	Query url.Values
}

func buildAlignedHourRequest(market domain.MarketPair, windowEndMS int64, requestCount int) (alignedHourRequest, error) {
	endpoint, ok := alignedHourCandleURLs[market.Exchange]
	if !ok {
		return alignedHourRequest{}, fmt.Errorf("%s does not provide exact KRW hourly candle turnover", market.Exchange)
	}
	if windowEndMS <= 0 || windowEndMS%AlignedHourMS != 0 {
		return alignedHourRequest{}, errors.New("aligned candle window end must be a positive UTC hour boundary")
	}
	if requestCount < 1 || requestCount > alignedCandleMaximumRequestCount {
		return alignedHourRequest{}, fmt.Errorf("aligned candle request count must be in [1,%d]", alignedCandleMaximumRequestCount)
	}
	base, quote, err := krwMarketParts(market.Symbol)
	if err != nil {
		return alignedHourRequest{}, err
	}
	query := make(url.Values)
	switch market.Exchange {
	case domain.ExchangeUpbit, domain.ExchangeBithumb:
		query.Set("market", quote+"-"+base)
		end := time.UnixMilli(windowEndMS)
		if market.Exchange == domain.ExchangeUpbit {
			query.Set("to", end.UTC().Format("2006-01-02T15:04:05Z"))
		} else {
			kst := time.FixedZone("KST", 9*60*60)
			query.Set("to", end.In(kst).Format("2006-01-02T15:04:05"))
		}
		query.Set("count", strconv.Itoa(requestCount))
	case domain.ExchangeCoinone:
		endpoint = strings.ReplaceAll(endpoint, "{quote}", quote)
		endpoint = strings.ReplaceAll(endpoint, "{base}", base)
		query.Set("interval", "1h")
		query.Set("timestamp", strconv.FormatInt(windowEndMS-AlignedHourMS, 10))
		query.Set("size", strconv.Itoa(requestCount))
	}
	return alignedHourRequest{URL: endpoint, Query: query}, nil
}

func parseAlignedHourCandles(exchange domain.Exchange, market string, payload []byte) ([]MinuteCandle, error) {
	root, err := decodeAny(payload)
	if err != nil {
		return nil, err
	}
	var candles []MinuteCandle
	switch exchange {
	case domain.ExchangeUpbit, domain.ExchangeBithumb:
		candles, err = parseUpbitStyleCandles(exchange, market, root, 60)
	case domain.ExchangeCoinone:
		candles, err = parseCoinoneCandles(market, root)
	default:
		return nil, fmt.Errorf("%s is excluded from aligned candle share", exchange)
	}
	if err != nil {
		return nil, err
	}
	for _, candle := range candles {
		if candle.StartMS%AlignedHourMS != 0 {
			return nil, fmt.Errorf("%s returned a candle outside the UTC hour boundary", exchange)
		}
		if candle.NotionalMethod != ExactQuoteNotional || candle.QuoteVolumeKRW == nil {
			return nil, fmt.Errorf("%s hourly candle lacks exact KRW quote volume", exchange)
		}
	}
	return deduplicateCandles(candles)
}

func aggregateAlignedCandles(
	rows map[domain.Exchange]TickerSnapshot,
	windowStart time.Time,
	windowEnd time.Time,
	started time.Time,
	completed time.Time,
) DomesticTickerSummary {
	result := emptyAlignedCandleSummary()
	result.AsOf = float64(windowEnd.Unix())
	result.StartedAt = float64(started.UnixNano()) / float64(time.Second)
	result.CollectionSpanMS = ceilPositiveMilliseconds(completed.Sub(started))
	result.WindowStartMS = windowStart.UnixMilli()
	result.WindowEndMS = windowEnd.UnixMilli()
	result.Exchanges = make(map[domain.Exchange]TickerSnapshot, len(AlignedShareExchanges))
	total := 0.0
	ready := 0
	for _, exchange := range AlignedShareExchanges {
		row := rows[exchange]
		row.SharePercent = nil
		result.Exchanges[exchange] = row
		if row.Status != "LIVE" || row.VolumeKRW == nil || !finiteNonnegative(*row.VolumeKRW) ||
			row.WindowStartMS != result.WindowStartMS || row.WindowEndMS != result.WindowEndMS {
			continue
		}
		ready++
		total += *row.VolumeKRW
	}
	result.ReadyExchangeCount = ready
	if ready != len(AlignedShareExchanges) || !finiteNonnegative(total) {
		return result
	}
	result.Status = "LIVE"
	result.TotalVolumeKRW = floatPointer(total)
	if total > 0 {
		for _, exchange := range AlignedShareExchanges {
			row := result.Exchanges[exchange]
			row.SharePercent = floatPointer(*row.VolumeKRW / total * 100)
			result.Exchanges[exchange] = row
		}
	}
	if bithumb := result.Exchanges[domain.ExchangeBithumb].VolumeKRW; bithumb != nil {
		result.BithumbVolumeKRW = floatPointer(*bithumb)
		if total > 0 {
			result.BithumbSharePercent = floatPointer(*bithumb / total * 100)
		}
	}
	return result
}

func emptyAlignedCandleSummary() DomesticTickerSummary {
	included := append([]domain.Exchange(nil), AlignedShareExchanges[:]...)
	excluded := make(map[domain.Exchange]string, len(AlignedShareExcludedExchanges))
	for exchange, reason := range AlignedShareExcludedExchanges {
		excluded[exchange] = reason
	}
	return DomesticTickerSummary{
		Status: "PARTIAL", Source: Aligned24hSource,
		ExpectedExchangeCount: len(AlignedShareExchanges),
		Exchanges:             map[domain.Exchange]TickerSnapshot{},
		BarIntervalSec:        int64(time.Hour / time.Second),
		RequiredBarCount:      Aligned24hBarCount,
		IncludedExchanges:     included,
		ExcludedExchanges:     excluded,
	}
}

func alignedCandleSource(exchange domain.Exchange) string {
	return strings.ToUpper(string(exchange)) + "_OFFICIAL_ALIGNED_1H_CANDLES_24H"
}

func (s *AlignedCandleService) publishUpdate(ctx context.Context, snapshot DomesticTickerSummary) bool {
	if ctx.Err() != nil {
		return false
	}
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	s.updatesPublished++
	snapshot.UpdateSequence = s.updatesPublished
	for {
		select {
		case <-s.updates:
			s.updatesCoalesced++
			snapshot.MissedUpdateCount++
		default:
			select {
			case s.updates <- cloneTickerSummary(snapshot):
				return true
			case <-ctx.Done():
				return false
			}
		}
	}
}

func (s *AlignedCandleService) run(ctx context.Context) {
	defer s.wait.Done()
	for {
		refreshCtx, cancel := context.WithTimeout(ctx, alignedCandleRefreshTimeout)
		s.refreshAndPublish(refreshCtx, ctx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		delay := s.nextRefreshDelay(time.Now())
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func (s *AlignedCandleService) nextRefreshDelay(now time.Time) time.Duration {
	s.mu.RLock()
	windowEndMS := s.snapshot.WindowEndMS
	status := s.snapshot.Status
	s.mu.RUnlock()
	currentEnd := now.UTC().Truncate(time.Hour)
	readyAt := currentEnd.Add(alignedCandleCloseGrace)
	if windowEndMS < currentEnd.UnixMilli() && !now.Before(readyAt) {
		return 0
	}
	next := currentEnd.Add(time.Hour).Add(alignedCandleCloseGrace)
	if status != "LIVE" {
		return min(alignedCandleRetryDelay, max(time.Duration(0), next.Sub(now)))
	}
	return max(time.Duration(0), next.Sub(now))
}
