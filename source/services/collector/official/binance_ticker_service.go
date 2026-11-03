package official

import (
	"context"
	"errors"
	"fmt"
	contract "market-share-go/contracts/collection/official"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"market-share-go/internal/domain"
	"market-share-go/services/collector/restbudget"
)

const (
	BinanceTicker24hURL         = "https://data-api.binance.vision/api/v3/ticker/24hr"
	DefaultBinanceTickerRefresh = 60 * time.Second
	BinanceTickerWindowSec      = int64(24 * time.Hour / time.Second)
	BinanceTickerQuoteAsset     = "USDT"

	binanceTickerSource          = "BINANCE_SPOT_OFFICIAL_24H_TICKER_USDT"
	binanceTickerRequestTimeout  = 5 * time.Second
	binanceTickerMaxAge          = 2 * time.Minute
	binanceTickerFutureSkew      = 30 * time.Second
	binanceTickerWindowTolerance = 5 * time.Minute
)

type BinanceTicker24hAsset = contract.BinanceTicker24hAsset

// BinanceTicker24hSummary is an official rolling 24-hour reference. It must
// not be interpreted as a trade-event rolling window or mixed into realtime
// global-volume fields.
type BinanceTicker24hSummary = contract.BinanceTicker24hSummary

type BinanceTickerService struct {
	client   TickerJSONClient
	interval time.Duration
	now      func() time.Time

	refreshMu sync.Mutex
	mu        sync.RWMutex
	provider  func() []domain.MarketPair
	snapshot  BinanceTicker24hSummary
	collected time.Time
	cancel    context.CancelFunc
	wait      sync.WaitGroup
	running   bool
}

func NewBinanceTickerService(client TickerJSONClient, interval time.Duration) *BinanceTickerService {
	if interval <= 0 {
		interval = DefaultBinanceTickerRefresh
	}
	return &BinanceTickerService{
		client:   client,
		interval: interval,
		now:      time.Now,
		snapshot: emptyBinanceTicker24hSummary(),
	}
}

func (s *BinanceTickerService) SetExpectedMarketsProvider(provider func() []domain.MarketPair) {
	s.mu.Lock()
	s.provider = provider
	s.mu.Unlock()
}

func (s *BinanceTickerService) Start(parent context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.running = true
	s.wait.Add(1)
	s.mu.Unlock()
	go s.run(ctx)
}

func (s *BinanceTickerService) Stop() {
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

func (s *BinanceTickerService) Refresh(ctx context.Context) BinanceTicker24hSummary {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	return s.refresh(ctx)
}

func (s *BinanceTickerService) Snapshot() BinanceTicker24hSummary {
	return s.snapshotAt(s.now())
}

func (s *BinanceTickerService) snapshotAt(now time.Time) BinanceTicker24hSummary {
	s.mu.RLock()
	snapshot := cloneBinanceTicker24hSummary(s.snapshot)
	collected := s.collected
	s.mu.RUnlock()
	if len(snapshot.Assets) == 0 || snapshot.Status == "UNAVAILABLE" {
		return snapshot
	}
	if err := validateBinanceCollectionTime(collected, now); err != nil {
		snapshot.Status = "STALE"
		message := err.Error()
		snapshot.LastError = &message
	}
	return snapshot
}

func (s *BinanceTickerService) refresh(ctx context.Context) BinanceTicker24hSummary {
	now := s.now()
	expected, err := s.expectedMarkets()
	if err == nil {
		if s.client == nil {
			err = errors.New("official Binance ticker REST client is nil")
		} else {
			var raw []byte
			raw, err = restbudget.FetchJSONBytes(ctx, s.client, restbudget.Request{
				Exchange: string(domain.ExchangeBinance),
				Group:    "ticker",
				URL:      BinanceTicker24hURL,
				Query: url.Values{
					"type":         {"FULL"},
					"symbolStatus": {"TRADING"},
				},
				Priority: restbudget.PriorityTicker,
				Cost:     1,
			}, 16<<20)
			if err == nil {
				var fresh BinanceTicker24hSummary
				fresh, err = ParseBinanceTicker24h(raw, expected, now)
				if err == nil {
					s.store(fresh, now)
					return cloneBinanceTicker24hSummary(fresh)
				}
			}
		}
	}

	s.mu.RLock()
	failed := cloneBinanceTicker24hSummary(s.snapshot)
	s.mu.RUnlock()
	message := fmt.Sprintf("%T: %v", err, err)
	failed.LastError = &message
	if len(failed.Assets) > 0 {
		failed.Status = "STALE"
	} else {
		failed = emptyBinanceTicker24hSummary()
		failed.LastError = &message
	}
	s.store(failed, now)
	return cloneBinanceTicker24hSummary(failed)
}

func (s *BinanceTickerService) expectedMarkets() ([]domain.MarketPair, error) {
	s.mu.RLock()
	provider := s.provider
	s.mu.RUnlock()
	if provider == nil {
		return nil, errors.New("official Binance ticker expected-market provider is not configured")
	}
	return normalizeBinanceExpectedMarkets(provider())
}

func (s *BinanceTickerService) store(snapshot BinanceTicker24hSummary, collected time.Time) {
	s.mu.Lock()
	s.snapshot = cloneBinanceTicker24hSummary(snapshot)
	s.collected = collected
	s.mu.Unlock()
}

func (s *BinanceTickerService) run(ctx context.Context) {
	defer s.wait.Done()
	for {
		started := s.now()
		requestCtx, cancel := context.WithTimeout(ctx, binanceTickerRequestTimeout)
		s.Refresh(requestCtx)
		cancel()
		delay := s.interval - s.now().Sub(started)
		if delay < 0 {
			delay = 0
		}
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

func ParseBinanceTicker24h(payload []byte, expected []domain.MarketPair, receivedAt time.Time) (BinanceTicker24hSummary, error) {
	if receivedAt.IsZero() {
		return BinanceTicker24hSummary{}, errors.New("official Binance ticker receipt time is missing")
	}
	markets, err := normalizeBinanceExpectedMarkets(expected)
	if err != nil {
		return BinanceTicker24hSummary{}, err
	}
	root, err := decodeAny(payload)
	if err != nil {
		return BinanceTicker24hSummary{}, fmt.Errorf("decode Binance 24h ticker: %w", err)
	}
	values, ok := root.([]any)
	if !ok {
		return BinanceTicker24hSummary{}, errors.New("Binance 24h ticker response must be an array")
	}

	expectedBySymbol := make(map[string]domain.MarketPair, len(markets))
	for _, market := range markets {
		expectedBySymbol[strings.ToUpper(market.Symbol)] = market
	}
	seen := make(map[string]struct{}, len(values))
	accepted := make(map[string]BinanceTicker24hAsset, len(markets))
	problems := make([]string, 0)
	for _, value := range values {
		row, rowOK := value.(map[string]any)
		if !rowOK {
			continue
		}
		symbol := strings.ToUpper(text(row["symbol"]))
		if symbol == "" {
			continue
		}
		if _, duplicate := seen[symbol]; duplicate {
			return BinanceTicker24hSummary{}, fmt.Errorf("Binance 24h ticker has duplicate symbol %s", symbol)
		}
		seen[symbol] = struct{}{}
		market, wanted := expectedBySymbol[symbol]
		if !wanted {
			continue
		}
		quoteVolume, volumeErr := nonnegative(row["quoteVolume"], "quoteVolume")
		lastPrice, priceErr := parsePositiveFloat(row["lastPrice"], "lastPrice")
		bidPrice, bidPriceErr := parsePositiveFloat(row["bidPrice"], "bidPrice")
		bidQuantity, bidQuantityErr := nonnegative(row["bidQty"], "bidQty")
		askPrice, askPriceErr := parsePositiveFloat(row["askPrice"], "askPrice")
		askQuantity, askQuantityErr := nonnegative(row["askQty"], "askQty")
		openTime, openErr := positiveInt64(row["openTime"], "openTime")
		closeTime, closeErr := positiveInt64(row["closeTime"], "closeTime")
		if volumeErr != nil || priceErr != nil || bidPriceErr != nil || bidQuantityErr != nil ||
			askPriceErr != nil || askQuantityErr != nil || openErr != nil || closeErr != nil {
			problems = append(problems, symbol+": invalid volume, best quote, price, or timestamp")
			continue
		}
		if timestampErr := validateBinanceTickerTimes(receivedAt, openTime, closeTime); timestampErr != nil {
			problems = append(problems, symbol+": "+timestampErr.Error())
			continue
		}
		accepted[symbol] = BinanceTicker24hAsset{
			Asset:            market.Base,
			Market:           symbol,
			QuoteAsset:       BinanceTickerQuoteAsset,
			QuoteVolumeUSDT:  quoteVolume,
			LastPriceUSDT:    lastPrice,
			BestBidPriceUSDT: bidPrice,
			BestBidQuantity:  bidQuantity,
			BestAskPriceUSDT: askPrice,
			BestAskQuantity:  askQuantity,
			OpenTimeMS:       openTime,
			CloseTimeMS:      closeTime,
		}
	}

	assets := make([]BinanceTicker24hAsset, 0, len(accepted))
	missing := make([]string, 0)
	total := 0.0
	for _, market := range markets {
		symbol := strings.ToUpper(market.Symbol)
		asset, exists := accepted[symbol]
		if !exists {
			missing = append(missing, symbol)
			continue
		}
		total += asset.QuoteVolumeUSDT
		if !finiteNonnegative(total) {
			return BinanceTicker24hSummary{}, errors.New("Binance 24h ticker total quote volume is not finite")
		}
		assets = append(assets, asset)
	}
	slices.SortFunc(assets, func(left, right BinanceTicker24hAsset) int {
		if left.Asset != right.Asset {
			return strings.Compare(left.Asset, right.Asset)
		}
		return strings.Compare(left.Market, right.Market)
	})

	status := "LIVE"
	var lastError *string
	if len(assets) != len(markets) {
		status = "PARTIAL"
		details := make([]string, 0, 2)
		if len(missing) > 0 {
			details = append(details, "missing="+strings.Join(missing, ","))
		}
		if len(problems) > 0 {
			details = append(details, "invalid="+strings.Join(problems, ";"))
		}
		message := "Binance 24h ticker market set is partial: " + strings.Join(details, " ")
		lastError = &message
	}
	result := BinanceTicker24hSummary{
		Status:                  status,
		Source:                  binanceTickerSource,
		QuoteAsset:              BinanceTickerQuoteAsset,
		WindowSec:               BinanceTickerWindowSec,
		AsOf:                    float64(receivedAt.UnixNano()) / 1e9,
		ExpectedMarketCount:     len(markets),
		ReadyMarketCount:        len(assets),
		ObservedQuoteVolumeUSDT: total,
		ConversionStatus:        "UNRESOLVED",
		Assets:                  assets,
		LastError:               lastError,
	}
	if status == "LIVE" {
		result.TotalQuoteVolumeUSDT = floatPointer(total)
	}
	return result, nil
}

func normalizeBinanceExpectedMarkets(markets []domain.MarketPair) ([]domain.MarketPair, error) {
	if len(markets) == 0 {
		return nil, errors.New("official Binance ticker expected-market set is empty")
	}
	result := make([]domain.MarketPair, 0, len(markets))
	seenSymbols := make(map[string]struct{}, len(markets))
	seenAssets := make(map[string]struct{}, len(markets))
	for _, market := range markets {
		market.Symbol = strings.ToUpper(strings.TrimSpace(market.Symbol))
		market.Base = strings.ToUpper(strings.TrimSpace(market.Base))
		market.Quote = strings.ToUpper(strings.TrimSpace(market.Quote))
		if market.Exchange != domain.ExchangeBinance || market.Scope != domain.ScopeGlobal || market.Symbol == "" || market.Base == "" || market.Quote == "" {
			return nil, fmt.Errorf("invalid Binance Spot USDT expected market: %#v", market)
		}
		if market.Quote != BinanceTickerQuoteAsset {
			continue
		}
		if _, duplicate := seenSymbols[market.Symbol]; duplicate {
			return nil, fmt.Errorf("duplicate Binance expected symbol %s", market.Symbol)
		}
		if _, duplicate := seenAssets[market.Base]; duplicate {
			return nil, fmt.Errorf("duplicate Binance USDT base asset %s", market.Base)
		}
		seenSymbols[market.Symbol] = struct{}{}
		seenAssets[market.Base] = struct{}{}
		result = append(result, market)
	}
	if len(result) == 0 {
		return nil, errors.New("official Binance ticker expected-market set has no Spot USDT markets")
	}
	slices.SortFunc(result, func(left, right domain.MarketPair) int {
		return strings.Compare(left.Symbol, right.Symbol)
	})
	return result, nil
}

func positiveInt64(value any, field string) (int64, error) {
	parsed, err := strconv.ParseInt(text(value), 10, 64)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", field)
	}
	return parsed, nil
}

func parsePositiveFloat(value any, field string) (float64, error) {
	parsed, err := strconv.ParseFloat(text(value), 64)
	if err != nil || !finitePositive(parsed) {
		return 0, fmt.Errorf("%s must be a positive finite number", field)
	}
	return parsed, nil
}

func validateBinanceTickerTimes(receivedAt time.Time, openTimeMS, closeTimeMS int64) error {
	if receivedAt.IsZero() {
		return errors.New("receipt time is missing")
	}
	window := time.Duration(closeTimeMS-openTimeMS+1) * time.Millisecond
	if window < 24*time.Hour-binanceTickerWindowTolerance || window > 24*time.Hour+binanceTickerWindowTolerance {
		return fmt.Errorf("rolling window is %s, expected approximately 24h", window)
	}
	age := receivedAt.Sub(time.UnixMilli(closeTimeMS))
	if age > binanceTickerMaxAge {
		return fmt.Errorf("close time is stale by %s", age)
	}
	if age < -binanceTickerFutureSkew {
		return fmt.Errorf("close time is in the future by %s", -age)
	}
	return nil
}

func validateBinanceCollectionTime(collected, now time.Time) error {
	if collected.IsZero() || now.IsZero() {
		return errors.New("official Binance ticker collection time is missing")
	}
	age := now.Sub(collected)
	if age > binanceTickerMaxAge {
		return fmt.Errorf("official Binance ticker collection is stale by %s", age)
	}
	if age < -binanceTickerFutureSkew {
		return fmt.Errorf("official Binance ticker collection is in the future by %s", -age)
	}
	return nil
}

// BinanceTickerAssetFreshAt applies the same exchange timestamp bounds used
// while parsing a fresh ticker response. App-layer premium enrichment uses it
// again because a failed refresh may preserve an older row in a STALE summary.
func BinanceTickerAssetFreshAt(closeTimeMS int64, now time.Time) bool {
	if closeTimeMS <= 0 || now.IsZero() {
		return false
	}
	age := now.Sub(time.UnixMilli(closeTimeMS))
	return age <= binanceTickerMaxAge && age >= -binanceTickerFutureSkew
}

// ConvertBinanceTicker24hToEstimatedKRW converts a cloned snapshot atomically.
// USDT remains authoritative. Invalid rates, missing rate time, or any overflow
// leave every estimated KRW field nil, so consumers cannot mistake a failed
// conversion for either a real zero or exchange-reported KRW turnover.
func ConvertBinanceTicker24hToEstimatedKRW(summary BinanceTicker24hSummary, usdtKRW *float64, rateAsOf time.Time) BinanceTicker24hSummary {
	result := cloneBinanceTicker24hSummary(summary)
	result.EstimatedTotalVolumeKRW = nil
	result.USDTKRWRate = nil
	result.ConversionAsOf = nil
	result.ConversionStatus = "UNRESOLVED"
	for index := range result.Assets {
		result.Assets[index].EstimatedVolumeKRW = nil
		result.Assets[index].EstimatedLastPriceKRW = nil
		result.Assets[index].EstimatedBestBidKRW = nil
		result.Assets[index].EstimatedBestAskKRW = nil
	}
	if usdtKRW == nil || !finitePositive(*usdtKRW) || rateAsOf.IsZero() || len(result.Assets) == 0 ||
		!finitePositive(result.AsOf) || result.AsOf > float64(math.MaxInt64)/float64(time.Second) {
		return result
	}
	snapshotAsOf := time.Unix(0, int64(result.AsOf*float64(time.Second)))
	rateAge := snapshotAsOf.Sub(rateAsOf)
	// The conversion is explicitly estimated at ConversionAsOf, so a fresh FX
	// observation may legitimately be newer than the last 60-second ticker
	// refresh. Bound both directions by the same freshness window; the tighter
	// future-skew rule applies to exchange timestamps, not to a newer local FX
	// observation used for a labelled estimate.
	if rateAge > binanceTickerMaxAge || rateAge < -binanceTickerMaxAge {
		return result
	}
	var estimatedTotal *float64
	if result.TotalQuoteVolumeUSDT != nil {
		total := *result.TotalQuoteVolumeUSDT * *usdtKRW
		if !finiteNonnegative(total) {
			return result
		}
		estimatedTotal = floatPointer(total)
	}
	converted := make([]float64, len(result.Assets))
	convertedPrices := make([]float64, len(result.Assets))
	convertedBids := make([]float64, len(result.Assets))
	convertedAsks := make([]float64, len(result.Assets))
	for index, asset := range result.Assets {
		converted[index] = asset.QuoteVolumeUSDT * *usdtKRW
		convertedPrices[index] = asset.LastPriceUSDT * *usdtKRW
		convertedBids[index] = asset.BestBidPriceUSDT * *usdtKRW
		convertedAsks[index] = asset.BestAskPriceUSDT * *usdtKRW
		if !finiteNonnegative(converted[index]) || !finitePositive(convertedPrices[index]) ||
			!finitePositive(convertedBids[index]) || !finitePositive(convertedAsks[index]) {
			return result
		}
	}
	result.USDTKRWRate = floatPointer(*usdtKRW)
	result.ConversionAsOf = floatPointer(float64(rateAsOf.UnixNano()) / 1e9)
	result.EstimatedTotalVolumeKRW = estimatedTotal
	result.ConversionStatus = "RESOLVED"
	for index := range result.Assets {
		result.Assets[index].EstimatedVolumeKRW = floatPointer(converted[index])
		result.Assets[index].EstimatedLastPriceKRW = floatPointer(convertedPrices[index])
		result.Assets[index].EstimatedBestBidKRW = floatPointer(convertedBids[index])
		result.Assets[index].EstimatedBestAskKRW = floatPointer(convertedAsks[index])
	}
	return result
}

func emptyBinanceTicker24hSummary() BinanceTicker24hSummary {
	return BinanceTicker24hSummary{
		Status:           "UNAVAILABLE",
		Source:           binanceTickerSource,
		QuoteAsset:       BinanceTickerQuoteAsset,
		WindowSec:        BinanceTickerWindowSec,
		ConversionStatus: "UNRESOLVED",
		Assets:           []BinanceTicker24hAsset{},
	}
}

func cloneBinanceTicker24hSummary(value BinanceTicker24hSummary) BinanceTicker24hSummary {
	result := value
	result.TotalQuoteVolumeUSDT = copyTickerFloat(value.TotalQuoteVolumeUSDT)
	result.EstimatedTotalVolumeKRW = copyTickerFloat(value.EstimatedTotalVolumeKRW)
	result.USDTKRWRate = copyTickerFloat(value.USDTKRWRate)
	result.ConversionAsOf = copyTickerFloat(value.ConversionAsOf)
	if value.LastError != nil {
		message := *value.LastError
		result.LastError = &message
	}
	result.Assets = make([]BinanceTicker24hAsset, len(value.Assets))
	copy(result.Assets, value.Assets)
	for index := range result.Assets {
		result.Assets[index].EstimatedVolumeKRW = copyTickerFloat(value.Assets[index].EstimatedVolumeKRW)
		result.Assets[index].EstimatedLastPriceKRW = copyTickerFloat(value.Assets[index].EstimatedLastPriceKRW)
		result.Assets[index].EstimatedBestBidKRW = copyTickerFloat(value.Assets[index].EstimatedBestBidKRW)
		result.Assets[index].EstimatedBestAskKRW = copyTickerFloat(value.Assets[index].EstimatedBestAskKRW)
		result.Assets[index].PremiumBithumbMidKRW = copyTickerFloat(value.Assets[index].PremiumBithumbMidKRW)
		result.Assets[index].PremiumBPS = copyTickerFloat(value.Assets[index].PremiumBPS)
	}
	return result
}

func finitePositive(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0
}
