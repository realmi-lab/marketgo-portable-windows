package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	contract "market-share-go/contracts/collection/registry"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"market-share-go/internal/domain"
	"market-share-go/services/collector/exchange"
	"market-share-go/services/collector/restbudget"
)

var DefaultEndpoints = map[domain.Exchange]string{
	domain.ExchangeUpbit:   "https://api.upbit.com/v1/market/all?is_details=true",
	domain.ExchangeBithumb: "https://api.bithumb.com/v1/market/all?isDetails=true",
	domain.ExchangeCoinone: "https://api.coinone.co.kr/public/v2/markets/{quote}",
	domain.ExchangeKorbit:  "https://api.digitalx.miraeasset.com/v2/currencyPairs",
	domain.ExchangeGopax:   "https://api.gopax.co.kr/trading-pairs",
	domain.ExchangeBinance: "https://data-api.binance.vision/api/v3/exchangeInfo",
	domain.ExchangeBybit:   "https://api.bybit.com/v5/market/instruments-info",
	domain.ExchangeOKX:     "https://www.okx.com/api/v5/public/instruments",
}

type Snapshot = contract.Snapshot

type ExchangeSnapshot = contract.ExchangeSnapshot

type Registry struct {
	http          *http.Client
	official      *restbudget.Client
	endpoints     map[domain.Exchange]string
	coinoneQuotes []string

	refreshMu sync.Mutex
	mu        sync.Mutex
	markets   map[domain.Exchange][]domain.MarketPair
	errors    map[domain.Exchange]string
	version   uint64
	updatedAt time.Time
	universe  atomic.Pointer[domain.MarketUniverse]
}

func New(
	httpClient *http.Client,
	official *restbudget.Client,
	endpoints map[domain.Exchange]string,
	coinoneQuotes []string,
) (*Registry, error) {
	if httpClient == nil || official == nil {
		return nil, errors.New("HTTP and official REST clients are required")
	}
	if endpoints == nil {
		endpoints = DefaultEndpoints
	}
	endpointCopy := make(map[domain.Exchange]string, len(endpoints))
	for name, endpoint := range endpoints {
		if !name.Valid() || strings.TrimSpace(endpoint) == "" {
			return nil, fmt.Errorf("invalid registry endpoint for %q", name)
		}
		endpointCopy[name] = endpoint
	}
	empty, err := domain.NewMarketUniverse(nil)
	if err != nil {
		return nil, err
	}
	registry := &Registry{
		http: httpClient, official: official, endpoints: endpointCopy,
		coinoneQuotes: normalizedQuotes(coinoneQuotes),
		markets:       make(map[domain.Exchange][]domain.MarketPair),
		errors:        make(map[domain.Exchange]string),
	}
	registry.universe.Store(&empty)
	return registry, nil
}

func (r *Registry) Universe() domain.MarketUniverse {
	return *r.universe.Load()
}

// Restore seeds the in-memory registry from the last durable authoritative
// membership before the first official REST refresh. A failed refresh then
// preserves a usable subscription set instead of making the whole domestic
// runtime unavailable during an exchange maintenance window.
func (r *Registry) Restore(markets []domain.MarketPair) error {
	universe, err := domain.NewMarketUniverse(markets)
	if err != nil {
		return err
	}
	marketsByExchange := make(map[domain.Exchange][]domain.MarketPair)
	for _, market := range universe.Markets() {
		marketsByExchange[market.Exchange] = append(marketsByExchange[market.Exchange], market)
	}

	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.universe.Load().Markets()) > 0 {
		return errors.New("registry restore requires an empty registry")
	}
	r.markets = marketsByExchange
	r.universe.Store(&universe)
	return nil
}

func (r *Registry) Refresh(ctx context.Context, enabled []domain.Exchange) ([]domain.Exchange, error) {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()

	enabled = uniqueExchanges(enabled)
	r.mu.Lock()
	marketsByExchange := cloneMarketMap(r.markets)
	errorsByExchange := cloneRegistryErrors(r.errors)
	previousUniverse := *r.universe.Load()
	r.mu.Unlock()

	type result struct {
		exchange domain.Exchange
		markets  []domain.MarketPair
		err      error
	}
	results := make(chan result, len(enabled))
	var workers sync.WaitGroup
	for _, name := range enabled {
		if name == domain.ExchangeCoinone {
			continue
		}
		workers.Add(1)
		go func(exchangeName domain.Exchange) {
			defer workers.Done()
			markets, err := r.fetch(ctx, exchangeName)
			results <- result{exchange: exchangeName, markets: markets, err: err}
		}(name)
	}
	workers.Wait()
	close(results)

	for item := range results {
		applyRefresh(marketsByExchange, errorsByExchange, item.exchange, item.markets, item.err)
	}

	if containsExchange(enabled, domain.ExchangeCoinone) {
		markets, err := r.fetchCoinone(ctx, marketsByExchange)
		applyRefresh(marketsByExchange, errorsByExchange, domain.ExchangeCoinone, markets, err)
	}
	return r.publishRefresh(previousUniverse, marketsByExchange, errorsByExchange)
}

func applyRefresh(
	marketsByExchange map[domain.Exchange][]domain.MarketPair,
	errorsByExchange map[domain.Exchange]string,
	exchangeName domain.Exchange,
	markets []domain.MarketPair,
	fetchErr error,
) {
	if fetchErr == nil {
		fetchErr = validateMarketRefresh(marketsByExchange[exchangeName], markets)
	}
	if fetchErr != nil {
		errorsByExchange[exchangeName] = fetchErr.Error()
		return
	}
	delete(errorsByExchange, exchangeName)
	marketsByExchange[exchangeName] = cloneMarkets(markets)
}

func validateMarketRefresh(previous, current []domain.MarketPair) error {
	if len(current) == 0 {
		return errors.New("registry returned no active markets")
	}
	if len(previous) >= 10 && len(current)*2 < len(previous) {
		return fmt.Errorf("registry market set shrank suspiciously from %d to %d", len(previous), len(current))
	}
	previousQuotes := quoteCounts(previous)
	currentQuotes := quoteCounts(current)
	for quote, count := range previousQuotes {
		if count >= 10 && currentQuotes[quote]*2 < count {
			return fmt.Errorf("registry %s market set shrank suspiciously from %d to %d", quote, count, currentQuotes[quote])
		}
	}
	return nil
}

func quoteCounts(markets []domain.MarketPair) map[string]int {
	result := make(map[string]int)
	for _, market := range markets {
		result[market.Quote]++
	}
	return result
}

func (r *Registry) fetch(ctx context.Context, exchangeName domain.Exchange) ([]domain.MarketPair, error) {
	if exchangeName == domain.ExchangeBybit {
		return r.fetchBybit(ctx)
	}
	query := make(url.Values)
	if exchangeName == domain.ExchangeOKX {
		query.Set("instType", "SPOT")
	}
	payload, err := r.request(ctx, exchangeName, r.endpoints[exchangeName], query)
	if err != nil {
		return nil, err
	}
	universe, err := exchange.ParseRegistry(exchangeName, payload, "")
	if err != nil {
		return nil, err
	}
	return universe.Markets(), nil
}

func (r *Registry) fetchBybit(ctx context.Context) ([]domain.MarketPair, error) {
	cursor := ""
	all := make([]domain.MarketPair, 0)
	for {
		query := url.Values{"category": {"spot"}, "limit": {"1000"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		payload, err := r.request(ctx, domain.ExchangeBybit, r.endpoints[domain.ExchangeBybit], query)
		if err != nil {
			return nil, err
		}
		universe, err := exchange.ParseRegistry(domain.ExchangeBybit, payload, "")
		if err != nil {
			return nil, err
		}
		all = append(all, universe.Markets()...)
		next := bybitCursor(payload)
		if next == "" || next == cursor {
			break
		}
		cursor = next
	}
	universe, err := domain.NewMarketUniverse(all)
	if err != nil {
		return nil, err
	}
	return universe.Markets(), nil
}

func (r *Registry) fetchCoinone(
	ctx context.Context,
	marketsByExchange map[domain.Exchange][]domain.MarketPair,
) ([]domain.MarketPair, error) {
	quotes := append([]string(nil), r.coinoneQuotes...)
	for exchangeName, pairs := range marketsByExchange {
		if !isDomestic(exchangeName) {
			continue
		}
		for _, pair := range pairs {
			quotes = append(quotes, pair.Quote)
		}
	}
	quotes = normalizedQuotes(append(quotes, "KRW"))
	markets := make([]domain.MarketPair, 0)
	var requestErrors []error
	for _, quote := range quotes {
		endpoint := strings.ReplaceAll(r.endpoints[domain.ExchangeCoinone], "{quote}", url.PathEscape(quote))
		payload, err := r.request(ctx, domain.ExchangeCoinone, endpoint, nil)
		if err != nil {
			requestErrors = append(requestErrors, fmt.Errorf("%s quote: %w", quote, err))
			continue
		}
		universe, err := exchange.ParseRegistry(domain.ExchangeCoinone, payload, quote)
		if err != nil {
			requestErrors = append(requestErrors, fmt.Errorf("%s quote: %w", quote, err))
			continue
		}
		markets = append(markets, universe.Markets()...)
	}
	if len(requestErrors) > 0 {
		return nil, fmt.Errorf("Coinone quote refresh was incomplete: %w", errors.Join(requestErrors...))
	}
	if len(markets) == 0 {
		return nil, errors.New("Coinone returned no active markets")
	}
	universe, err := domain.NewMarketUniverse(markets)
	if err != nil {
		return nil, err
	}
	return universe.Markets(), nil
}

func (r *Registry) request(ctx context.Context, exchangeName domain.Exchange, endpoint string, query url.Values) ([]byte, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if isDomestic(exchangeName) {
		var payload json.RawMessage
		group := "public"
		if exchangeName == domain.ExchangeUpbit {
			group = "market"
		}
		err := r.official.GetJSON(requestCtx, restbudget.Request{
			Exchange: string(exchangeName), Group: group, URL: endpoint,
			Query: query, Priority: restbudget.PriorityRegistry, Cost: 1,
		}, &payload)
		return payload, err
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	values := parsed.Query()
	for key, rows := range query {
		for _, value := range rows {
			values.Add(key, value)
		}
	}
	parsed.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := r.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 300))
		return nil, fmt.Errorf("registry HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(response.Body)
}

func (r *Registry) publishRefresh(
	previous domain.MarketUniverse,
	marketsByExchange map[domain.Exchange][]domain.MarketPair,
	errorsByExchange map[domain.Exchange]string,
) ([]domain.Exchange, error) {
	all := make([]domain.MarketPair, 0)
	for _, name := range allExchanges() {
		all = append(all, marketsByExchange[name]...)
	}
	universe, err := domain.NewMarketUniverse(all)
	if err != nil {
		return nil, err
	}
	changed := changedExchanges(previous, universe)

	r.mu.Lock()
	defer r.mu.Unlock()
	r.markets = marketsByExchange
	r.errors = errorsByExchange
	if len(changed) > 0 {
		r.version++
		r.universe.Store(&universe)
	}
	r.updatedAt = time.Now()
	return changed, nil
}

func cloneMarketMap(source map[domain.Exchange][]domain.MarketPair) map[domain.Exchange][]domain.MarketPair {
	result := make(map[domain.Exchange][]domain.MarketPair, len(source))
	for exchangeName, markets := range source {
		result[exchangeName] = cloneMarkets(markets)
	}
	return result
}

func cloneMarkets(source []domain.MarketPair) []domain.MarketPair {
	result := make([]domain.MarketPair, len(source))
	for index, market := range source {
		if market.MakerFee != nil {
			value := *market.MakerFee
			market.MakerFee = &value
		}
		if market.TakerFee != nil {
			value := *market.TakerFee
			market.TakerFee = &value
		}
		result[index] = market
	}
	return result
}

func cloneRegistryErrors(source map[domain.Exchange]string) map[domain.Exchange]string {
	result := make(map[domain.Exchange]string, len(source))
	for exchangeName, message := range source {
		result[exchangeName] = message
	}
	return result
}

func (r *Registry) Snapshot() Snapshot {
	snapshot, _ := r.View()
	return snapshot
}

// View returns health metadata and the exact immutable universe that produced
// it under one registry lock. Analysis callers must not pair a Snapshot from
// one refresh generation with market mappings from another.
func (r *Registry) View() (Snapshot, domain.MarketUniverse) {
	r.mu.Lock()
	defer r.mu.Unlock()
	universe := r.universe.Load()
	assets := make(map[string]struct{})
	result := Snapshot{
		Version: r.version, UpdatedAt: unixTimestamp(r.updatedAt),
		MarketCount: len(universe.Markets()),
		Exchanges:   make(map[domain.Exchange]ExchangeSnapshot),
	}
	for _, name := range allExchanges() {
		pairs := r.markets[name]
		quotes := make(map[string]int)
		exchangeAssets := make(map[string]struct{})
		fees := 0
		for _, pair := range pairs {
			assets[pair.Base] = struct{}{}
			exchangeAssets[pair.Base] = struct{}{}
			quotes[pair.Quote]++
			if pair.TakerFee != nil {
				fees++
			}
		}
		result.Exchanges[name] = ExchangeSnapshot{
			MarketCount: len(pairs), AssetCount: len(exchangeAssets), Quotes: quotes,
			PublicFeeMarketCount: fees, Error: r.errors[name],
		}
	}
	result.AssetCount = len(assets)
	return result, *universe
}

func unixTimestamp(value time.Time) float64 {
	if value.IsZero() {
		return 0
	}
	return float64(value.UnixNano()) / float64(time.Second)
}

func bybitCursor(payload []byte) string {
	var value struct {
		Result struct {
			Next string `json:"nextPageCursor"`
		} `json:"result"`
	}
	if json.NewDecoder(bytes.NewReader(payload)).Decode(&value) != nil {
		return ""
	}
	return value.Result.Next
}

func changedExchanges(previous, current domain.MarketUniverse) []domain.Exchange {
	changed := make([]domain.Exchange, 0)
	for _, name := range allExchanges() {
		if !reflect.DeepEqual(previous.ForExchange(name), current.ForExchange(name)) {
			changed = append(changed, name)
		}
	}
	return changed
}

func normalizedQuotes(quotes []string) []string {
	seen := make(map[string]struct{})
	for _, quote := range quotes {
		quote = strings.ToUpper(strings.TrimSpace(quote))
		if quote != "" {
			seen[quote] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for quote := range seen {
		result = append(result, quote)
	}
	sort.Strings(result)
	return result
}

func uniqueExchanges(values []domain.Exchange) []domain.Exchange {
	seen := make(map[domain.Exchange]struct{})
	result := make([]domain.Exchange, 0, len(values))
	for _, value := range values {
		if value.Valid() {
			if _, exists := seen[value]; !exists {
				seen[value] = struct{}{}
				result = append(result, value)
			}
		}
	}
	return result
}

func containsExchange(values []domain.Exchange, target domain.Exchange) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func allExchanges() []domain.Exchange {
	result := make([]domain.Exchange, 0, len(domain.DomesticExchanges)+len(domain.GlobalExchanges))
	result = append(result, domain.DomesticExchanges[:]...)
	result = append(result, domain.GlobalExchanges[:]...)
	return result
}

func isDomestic(name domain.Exchange) bool {
	for _, domestic := range domain.DomesticExchanges {
		if name == domestic {
			return true
		}
	}
	return false
}
