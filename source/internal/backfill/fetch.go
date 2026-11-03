package backfill

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"market-share-go/internal/domain"
	"market-share-go/services/collector/restbudget"
)

const (
	upbitTradesURL     = "https://api.upbit.com/v1/trades/ticks"
	bithumbTradesURL   = "https://api.bithumb.com/v1/trades/ticks"
	gopaxTradesURL     = "https://api.gopax.co.kr/trading-pairs/{market}/trades"
	coinoneCandlesURL  = "https://api.coinone.co.kr/public/v2/chart/{quote}/{base}"
	korbitCandlesURL   = "https://api.korbit.co.kr/v2/candles"
	coinoneHourSeconds = int64(3600)
	coinoneHourMethod  = "HOURLY_CANDLE_QUOTE_VOLUME_ESTIMATE"
)

type JSONClient interface {
	GetJSON(context.Context, restbudget.Request, any) error
}

type Fetcher struct {
	Client       JSONClient
	Now          func() time.Time
	requestCount int64
}

func (f Fetcher) FetchExchange(ctx context.Context, gap ObservationGap, exchange domain.Exchange, markets []domain.MarketPair) (FetchResult, error) {
	if f.Client == nil {
		return FetchResult{}, errors.New("backfill REST client is required")
	}
	if err := gap.Validate(); err != nil {
		return FetchResult{}, err
	}
	if f.Now == nil {
		f.Now = time.Now
	}
	result := FetchResult{Gap: gap, Exchange: exchange, Markets: filterMarkets(markets, exchange), Histories: map[string][]HistoricalTrade{}, Candles: map[string][]HistoricalCandle{}, CandleCoverage: map[string][2]domain.UnixSecond{}, Failures: map[string]string{}, EffectiveRequestsPerSecond: RequestsPerSecond[exchange]}
	if exchange == domain.ExchangeCoinone {
		result.Source = CandleSource
		result.SourceGranularitySeconds = coinoneHourSeconds
		result.NotionalMethod = coinoneHourMethod
	} else if exchange == domain.ExchangeKorbit {
		result.Source = CandleSource
		result.SourceGranularitySeconds = 60
		result.NotionalMethod = "BASE_VOLUME_ONLY"
	} else {
		result.Source = RawTradeSource
		result.SourceGranularitySeconds = 1
		result.NotionalMethod = "EXACT_TRADE"
	}
	for _, market := range result.Markets {
		before := f.requestCount
		if result.Source == RawTradeSource {
			history, err := f.fetchTrades(ctx, market, gap)
			if err != nil {
				result.Failures[market.Symbol] = err.Error()
			} else {
				result.Histories[market.Symbol] = history
			}
		} else {
			candles, coverage, err := f.fetchCandles(ctx, market, gap)
			if err != nil {
				result.Failures[market.Symbol] = err.Error()
			} else if coverage == nil {
				result.Failures[market.Symbol] = "gap has no usable official candle"
			} else {
				result.Candles[market.Symbol] = candles
				result.CandleCoverage[market.Symbol] = *coverage
			}
		}
		result.RequestCount += f.requestCount - before
	}
	return result, nil
}

func (f *Fetcher) request(ctx context.Context, exchange domain.Exchange, endpoint string, query url.Values) (any, error) {
	var payload any
	group := "public"
	if exchange == domain.ExchangeUpbit {
		group = "trades"
	}
	err := f.Client.GetJSON(ctx, restbudget.Request{Exchange: string(exchange), Group: group, URL: endpoint, Query: query, Priority: restbudget.PriorityRoundRobin, Cost: 1}, &payload)
	f.requestCount++
	return payload, err
}

func (f *Fetcher) fetchTrades(ctx context.Context, market domain.MarketPair, gap ObservationGap) ([]HistoricalTrade, error) {
	switch market.Exchange {
	case domain.ExchangeUpbit:
		return f.fetchUpbit(ctx, market, gap)
	case domain.ExchangeBithumb:
		return f.fetchBithumb(ctx, market, gap)
	case domain.ExchangeGopax:
		return f.fetchGopax(ctx, market, gap)
	default:
		return nil, fmt.Errorf("unsupported raw-trade backfill exchange: %s", market.Exchange)
	}
}

func (f *Fetcher) fetchUpbit(ctx context.Context, market domain.MarketPair, gap ObservationGap) ([]HistoricalTrade, error) {
	seen := map[string]struct{}{}
	result := make([]HistoricalTrade, 0)
	for _, day := range splitDays(gap.Start, gap.End, time.UTC) {
		cursor := ""
		for {
			exclusive := day[1] + 1
			query := url.Values{"market": {market.Symbol}, "count": {"500"}, "to": {time.Unix(int64(exclusive), 0).UTC().Format("15:04:05")}}
			days, err := daysAgo(exclusive, time.UTC, f.Now())
			if err != nil {
				return nil, err
			}
			if days > 0 {
				query.Set("days_ago", strconv.Itoa(days))
			}
			if cursor != "" {
				query.Set("cursor", cursor)
			}
			payload, err := f.request(ctx, market.Exchange, upbitTradesURL, query)
			if err != nil {
				return nil, err
			}
			rows, err := objectRows(payload, "Upbit trade")
			if err != nil {
				return nil, err
			}
			if len(rows) == 0 {
				break
			}
			page := make([]HistoricalTrade, 0, len(rows))
			for _, row := range rows {
				trade, err := parseRawTrade(market, row, "upbit")
				if err != nil {
					return nil, err
				}
				page = append(page, trade)
				if _, exists := seen[trade.SourceIdentity]; !exists {
					seen[trade.SourceIdentity] = struct{}{}
					if trade.EventSecond >= day[0] && trade.EventSecond <= day[1] {
						result = append(result, trade)
					}
				}
			}
			oldest := oldestTradeSecond(page)
			if len(page) < 500 || oldest < day[0] {
				break
			}
			next := page[len(page)-1].SourceIdentity
			if next == cursor {
				return nil, errors.New("Upbit pagination cursor did not advance")
			}
			cursor = next
		}
	}
	return result, nil
}

func (f *Fetcher) fetchBithumb(ctx context.Context, market domain.MarketPair, gap ObservationGap) ([]HistoricalTrade, error) {
	location := time.FixedZone("KST", 9*60*60)
	result := make([]HistoricalTrade, 0)
	for _, day := range splitDays(gap.Start, gap.End, location) {
		var cursor *int64
		for {
			exclusive := day[1] + 1
			query := url.Values{"market": {market.Symbol}, "count": {"500"}, "to": {time.Unix(int64(exclusive), 0).In(location).Format("15:04:05")}}
			days, err := daysAgo(exclusive, location, f.Now())
			if err != nil {
				return nil, err
			}
			if days > 0 {
				query.Set("daysAgo", strconv.Itoa(days))
			}
			if cursor != nil {
				query.Set("cursor", strconv.FormatInt(*cursor, 10))
			}
			payload, err := f.request(ctx, market.Exchange, bithumbTradesURL, query)
			if err != nil {
				return nil, err
			}
			rows, err := objectRows(payload, "Bithumb trade")
			if err != nil {
				return nil, err
			}
			if len(rows) == 0 {
				break
			}
			page := make([]HistoricalTrade, 0, len(rows))
			identities := make([]int64, 0, len(rows))
			for _, row := range rows {
				trade, err := parseRawTrade(market, row, "bithumb")
				if err != nil {
					return nil, err
				}
				identity, err := strconv.ParseInt(trade.SourceIdentity, 10, 64)
				if err != nil {
					return nil, errors.New("Bithumb sequential_id is not an integer")
				}
				if cursor != nil && identity >= *cursor {
					return nil, errors.New("Bithumb pagination returned a non-older trade id")
				}
				identities = append(identities, identity)
				page = append(page, trade)
				if trade.EventSecond >= day[0] && trade.EventSecond <= day[1] {
					result = append(result, trade)
				}
			}
			oldest := oldestTradeSecond(page)
			if len(page) < 500 || oldest < day[0] {
				break
			}
			next := identities[len(identities)-1]
			if cursor != nil && next == *cursor {
				return nil, errors.New("Bithumb pagination cursor did not advance")
			}
			cursor = &next
		}
	}
	return result, nil
}

func (f *Fetcher) fetchGopax(ctx context.Context, market domain.MarketPair, gap ObservationGap) ([]HistoricalTrade, error) {
	result := make([]HistoricalTrade, 0)
	seen := map[string]struct{}{}
	var fetch func(domain.UnixSecond, domain.UnixSecond) error
	fetch = func(start, end domain.UnixSecond) error {
		endpoint := strings.ReplaceAll(gopaxTradesURL, "{market}", market.Symbol)
		payload, err := f.request(ctx, market.Exchange, endpoint, url.Values{"limit": {"100"}, "before": {strconv.FormatInt(int64(end), 10)}})
		if err != nil {
			return err
		}
		rows, err := objectRows(payload, "GOPAX trade")
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		page := make([]HistoricalTrade, 0, len(rows))
		for _, row := range rows {
			trade, err := parseRawTrade(market, row, "gopax")
			if err != nil {
				return err
			}
			page = append(page, trade)
		}
		oldest := oldestTradeSecond(page)
		if len(page) == 100 && oldest >= start {
			if start >= end {
				return errors.New("GOPAX has more than one page of trades inside one second")
			}
			middle := start + (end-start)/2
			if err := fetch(middle+1, end); err != nil {
				return err
			}
			return fetch(start, middle)
		}
		for _, trade := range page {
			if trade.EventSecond >= start && trade.EventSecond <= end {
				if _, exists := seen[trade.SourceIdentity]; !exists {
					seen[trade.SourceIdentity] = struct{}{}
					result = append(result, trade)
				}
			}
		}
		return nil
	}
	if err := fetch(gap.Start, gap.End); err != nil {
		return nil, err
	}
	return result, nil
}

func (f *Fetcher) fetchCandles(ctx context.Context, market domain.MarketPair, gap ObservationGap) ([]HistoricalCandle, *[2]domain.UnixSecond, error) {
	if market.Exchange == domain.ExchangeCoinone {
		return f.fetchCoinone(ctx, market, gap)
	}
	if market.Exchange == domain.ExchangeKorbit {
		return f.fetchKorbit(ctx, market, gap)
	}
	return nil, nil, fmt.Errorf("unsupported candle backfill exchange: %s", market.Exchange)
}

func (f *Fetcher) fetchCoinone(ctx context.Context, market domain.MarketPair, gap ObservationGap) ([]HistoricalCandle, *[2]domain.UnixSecond, error) {
	start, end := IntersectingCandleRange(gap, coinoneHourSeconds)
	coverage := [2]domain.UnixSecond{start, end}
	expected := map[domain.UnixSecond]struct{}{}
	for second := start; second <= end; second += domain.UnixSecond(coinoneHourSeconds) {
		expected[second] = struct{}{}
	}
	collected := map[domain.UnixSecond]HistoricalCandle{}
	cursor := end + domain.UnixSecond(coinoneHourSeconds)
	endpoint := strings.ReplaceAll(strings.ReplaceAll(coinoneCandlesURL, "{quote}", "KRW"), "{base}", market.Base)
	for len(collected) < len(expected) {
		payload, err := f.request(ctx, market.Exchange, endpoint, url.Values{"interval": {"1h"}, "timestamp": {strconv.FormatInt(int64(cursor)*1000, 10)}, "size": {"500"}})
		if err != nil {
			return nil, nil, err
		}
		object, ok := payload.(map[string]any)
		if !ok || !strings.EqualFold(text(object["result"]), "success") {
			return nil, nil, errors.New("Coinone candle response is not successful")
		}
		rows, err := objectRows(object["chart"], "Coinone candle")
		if err != nil || len(rows) == 0 {
			return nil, nil, errors.New("Coinone returned no candle page for the requested range")
		}
		oldest := domain.UnixSecond(1 << 62)
		for _, row := range rows {
			candle, err := parseCoinoneCandle(market, row)
			if err != nil {
				return nil, nil, err
			}
			if candle.Start < oldest {
				oldest = candle.Start
			}
			if _, wanted := expected[candle.Start]; wanted {
				if previous, exists := collected[candle.Start]; exists && previous != candle {
					return nil, nil, errors.New("Coinone returned conflicting hourly candles")
				}
				collected[candle.Start] = candle
			}
		}
		if oldest <= start {
			break
		}
		if len(rows) < 500 {
			return nil, nil, errors.New("Coinone hourly candle page ended before the requested range")
		}
		next := oldest - domain.UnixSecond(coinoneHourSeconds)
		if next >= cursor {
			return nil, nil, errors.New("Coinone candle cursor did not move backward")
		}
		cursor = next
	}
	if len(collected) != len(expected) {
		return nil, nil, fmt.Errorf("Coinone omitted %d hourly candles", len(expected)-len(collected))
	}
	result := make([]HistoricalCandle, 0, len(collected))
	for second := start; second <= end; second += domain.UnixSecond(coinoneHourSeconds) {
		result = append(result, collected[second])
	}
	return result, &coverage, nil
}

func (f *Fetcher) fetchKorbit(ctx context.Context, market domain.MarketPair, gap ObservationGap) ([]HistoricalCandle, *[2]domain.UnixSecond, error) {
	start, end := IntersectingCandleRange(gap, 60)
	coverage := [2]domain.UnixSecond{start, end}
	collected := map[domain.UnixSecond]HistoricalCandle{}
	for _, chunk := range CandleChunks(start, end, 60, 180) {
		payload, err := f.request(ctx, market.Exchange, korbitCandlesURL, url.Values{"symbol": {market.Symbol}, "interval": {"1"}, "start": {strconv.FormatInt(int64(chunk[0])*1000, 10)}, "end": {strconv.FormatInt((int64(chunk[1])+60)*1000, 10)}, "limit": {"200"}})
		if err != nil {
			return nil, nil, err
		}
		object, ok := payload.(map[string]any)
		if !ok || object["success"] != true {
			return nil, nil, errors.New("Korbit candle response is not successful")
		}
		rows, err := objectRows(object["data"], "Korbit candle")
		if err != nil {
			return nil, nil, err
		}
		if len(rows) > 180 {
			return nil, nil, errors.New("Korbit candle response exceeded its safe page size")
		}
		for _, row := range rows {
			candle, err := parseKorbitCandle(market, row)
			if err != nil {
				return nil, nil, err
			}
			if candle.Start < chunk[0] || candle.Start > chunk[1] {
				continue
			}
			if previous, exists := collected[candle.Start]; exists && previous != candle {
				return nil, nil, errors.New("Korbit returned conflicting minute candles")
			}
			collected[candle.Start] = candle
		}
	}
	starts := make([]int64, 0, len(collected))
	for second := range collected {
		starts = append(starts, int64(second))
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	result := make([]HistoricalCandle, 0, len(starts))
	for _, second := range starts {
		result = append(result, collected[domain.UnixSecond(second)])
	}
	return result, &coverage, nil
}

func parseRawTrade(market domain.MarketPair, row map[string]any, exchange string) (HistoricalTrade, error) {
	var timestamp any
	milliseconds := true
	priceKey, quantityKey, sideKey, idKey := "trade_price", "trade_volume", "ask_bid", "sequential_id"
	if exchange == "gopax" {
		timestamp = row["date"]
		milliseconds = false
		priceKey = "price"
		quantityKey = "amount"
		sideKey = "side"
		idKey = "id"
	} else {
		timestamp = row["timestamp"]
	}
	second, err := timestampSecond(timestamp, milliseconds)
	if err != nil {
		return HistoricalTrade{}, err
	}
	price, err := positiveFloat(row[priceKey], priceKey)
	if err != nil {
		return HistoricalTrade{}, err
	}
	quantity, err := positiveFloat(row[quantityKey], quantityKey)
	if err != nil {
		return HistoricalTrade{}, err
	}
	identity := strings.TrimSpace(text(row[idKey]))
	if identity == "" {
		return HistoricalTrade{}, errors.New("official trade response has no stable identity")
	}
	side := domain.TradeSideUnknown
	native := strings.ToUpper(strings.TrimSpace(text(row[sideKey])))
	if native == "BID" || native == "BUY" {
		side = domain.TradeSideBuy
	} else if native == "ASK" || native == "SELL" {
		side = domain.TradeSideSell
	}
	trade := HistoricalTrade{Exchange: market.Exchange, Market: market.Symbol, Base: market.Base, Quote: "KRW", EventSecond: second, Price: price, Quantity: quantity, Side: side, SourceIdentity: identity}
	return trade, trade.Validate()
}

func parseCoinoneCandle(market domain.MarketPair, row map[string]any) (HistoricalCandle, error) {
	start, err := timestampSecond(row["timestamp"], true)
	if err != nil {
		return HistoricalCandle{}, err
	}
	quote, err := nonnegativeFloat(row["quote_volume"], "quote_volume")
	if err != nil {
		return HistoricalCandle{}, err
	}
	base, err := nonnegativeFloat(row["target_volume"], "target_volume")
	if err != nil {
		return HistoricalCandle{}, err
	}
	return HistoricalCandle{Exchange: market.Exchange, Market: market.Symbol, Base: market.Base, Quote: "KRW", Start: start, DurationSeconds: coinoneHourSeconds, KRWVolume: quote, BaseVolume: base, NotionalMethod: coinoneHourMethod}, nil
}
func parseKorbitCandle(market domain.MarketPair, row map[string]any) (HistoricalCandle, error) {
	start, err := timestampSecond(row["timestamp"], true)
	if err != nil {
		return HistoricalCandle{}, err
	}
	base, err := nonnegativeFloat(row["volume"], "volume")
	if err != nil {
		return HistoricalCandle{}, err
	}
	if _, err := positiveFloat(row["close"], "close"); err != nil {
		return HistoricalCandle{}, err
	}
	return HistoricalCandle{Exchange: market.Exchange, Market: market.Symbol, Base: market.Base, Quote: "KRW", Start: start, DurationSeconds: 60, BaseVolume: base, NotionalMethod: "BASE_VOLUME_ONLY"}, nil
}

func filterMarkets(markets []domain.MarketPair, exchange domain.Exchange) []domain.MarketPair {
	byKey := map[string]domain.MarketPair{}
	for _, market := range markets {
		if market.Exchange == exchange && market.Scope == domain.ScopeDomestic && strings.EqualFold(market.Quote, "KRW") {
			byKey[market.Key()] = market
		}
	}
	result := make([]domain.MarketPair, 0, len(byKey))
	for _, market := range byKey {
		result = append(result, market)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key() < result[j].Key() })
	return result
}
func splitDays(start, end domain.UnixSecond, location *time.Location) [][2]domain.UnixSecond {
	result := make([][2]domain.UnixSecond, 0)
	for cursor := start; cursor <= end; {
		local := time.Unix(int64(cursor), 0).In(location)
		next := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, location)
		dayEnd := domain.UnixSecond(next.Unix() - 1)
		if dayEnd > end {
			dayEnd = end
		}
		result = append(result, [2]domain.UnixSecond{cursor, dayEnd})
		cursor = dayEnd + 1
	}
	return result
}
func daysAgo(second domain.UnixSecond, location *time.Location, now time.Time) (int, error) {
	current := dateOnly(now.In(location))
	target := dateOnly(time.Unix(int64(second), 0).In(location))
	days := int(current.Sub(target).Hours() / 24)
	if days < 0 || days > 7 {
		return 0, errors.New("Upbit/Bithumb raw trade history is limited to the last 7 days")
	}
	return days, nil
}
func dateOnly(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, value.Location())
}
func objectRows(payload any, label string) ([]map[string]any, error) {
	values, ok := payload.([]any)
	if !ok {
		if typed, typedOK := payload.([]map[string]any); typedOK {
			return typed, nil
		}
		return nil, fmt.Errorf("%s response is not a list", label)
	}
	rows := make([]map[string]any, 0, len(values))
	for _, value := range values {
		row, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s response has an invalid row", label)
		}
		rows = append(rows, row)
	}
	return rows, nil
}
func timestampSecond(value any, milliseconds bool) (domain.UnixSecond, error) {
	parsed, err := strconv.ParseInt(text(value), 10, 64)
	if err != nil {
		return 0, errors.New("historical timestamp is not an integer")
	}
	if milliseconds {
		parsed /= 1000
	}
	if parsed <= 0 {
		return 0, errors.New("historical timestamp must be positive")
	}
	return domain.UnixSecond(parsed), nil
}
func positiveFloat(value any, name string) (float64, error) {
	parsed, err := strconv.ParseFloat(text(value), 64)
	if err != nil || !positive(parsed) {
		return 0, fmt.Errorf("%s must be a positive finite number", name)
	}
	return parsed, nil
}
func nonnegativeFloat(value any, name string) (float64, error) {
	parsed, err := strconv.ParseFloat(text(value), 64)
	if err != nil || !nonnegative(parsed) {
		return 0, fmt.Errorf("%s must be a non-negative finite number", name)
	}
	return parsed, nil
}
func text(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case jsonNumber:
		return string(typed)
	default:
		return fmt.Sprint(value)
	}
}

type jsonNumber string

func oldestTradeSecond(rows []HistoricalTrade) domain.UnixSecond {
	oldest := rows[0].EventSecond
	for _, row := range rows[1:] {
		if row.EventSecond < oldest {
			oldest = row.EventSecond
		}
	}
	return oldest
}
