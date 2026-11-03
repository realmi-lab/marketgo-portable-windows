package official

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"market-share-go/internal/domain"
	"market-share-go/services/collector/restbudget"
)

const (
	// DefaultTickerRefresh is the successful-completion cadence target, not a
	// delay between request starts. The loop reserves fetch and scheduling time.
	DefaultTickerRefresh = 10 * time.Second
	tickerRefreshTimeout = 8 * time.Second
	// The default loop idles for one second after completion, leaving the full
	// eight-second fetch budget and one second for scheduling/publication. This
	// is headroom, not a guarantee against external timeouts or host stalls.
	tickerSchedulingReserve = time.Second
	// Updates is a latest-state stream rather than an event log. Keeping one
	// pending value bounds consumer lag; replacement is counted explicitly.
	tickerUpdateBuffer = 1
)

// TickerUpdateStats makes latest-state coalescing observable. Published counts
// refresh results accepted by the stream, including results that later became
// obsolete and were replaced. Coalesced counts those replacements.
type TickerUpdateStats struct {
	Published uint64 `json:"published"`
	Coalesced uint64 `json:"coalesced"`
	Pending   int    `json:"pending"`
}

type TickerJSONClient interface {
	GetJSON(context.Context, restbudget.Request, any) error
}

type TickerService struct {
	client                      TickerJSONClient
	interval                    time.Duration
	expectedMarketCountProvider func(domain.Exchange) int
	expectedMarketsProvider     func(domain.Exchange) []domain.MarketPair
	refreshMu                   sync.Mutex
	mu                          sync.RWMutex
	snapshot                    DomesticTickerSummary
	collectedAt                 time.Time
	updateMu                    sync.Mutex
	updates                     chan DomesticTickerSummary
	updatesPublished            uint64
	updatesCoalesced            uint64
	cancel                      context.CancelFunc
	wait                        sync.WaitGroup
	running                     bool
}

func NewTickerService(client TickerJSONClient, interval time.Duration) *TickerService {
	if interval <= 0 {
		interval = DefaultTickerRefresh
	}
	return &TickerService{
		client: client, interval: interval,
		snapshot: AggregateTickers(map[domain.Exchange]TickerSnapshot{}, time.Time{}, time.Time{}),
		updates:  make(chan DomesticTickerSummary, tickerUpdateBuffer),
	}
}

func (s *TickerService) Start(parent context.Context) {
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
func (s *TickerService) Stop() {
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
func (s *TickerService) Snapshot() DomesticTickerSummary {
	return s.snapshotAt(time.Now())
}

func (s *TickerService) snapshotAt(now time.Time) DomesticTickerSummary {
	s.mu.RLock()
	snapshot := cloneTickerSummary(s.snapshot)
	collectedAt := s.collectedAt
	s.mu.RUnlock()
	return revalidateTickerSummary(snapshot, collectedAt, now)
}

// Updates returns a single-consumer latest-state stream. If the consumer has
// not taken the pending value when another refresh completes, the obsolete
// pending value is replaced by the new one and UpdateStats().Coalesced is
// incremented. Consumers must select on their lifecycle context because the
// channel remains reusable across Stop/Start cycles and is not closed by Stop.
func (s *TickerService) Updates() <-chan DomesticTickerSummary { return s.updates }

// UpdateStats reports publication/coalescing counters and the currently
// pending queue length. Pending is an instantaneous observation because the
// single consumer receives without taking updateMu.
func (s *TickerService) UpdateStats() TickerUpdateStats {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	return TickerUpdateStats{
		Published: s.updatesPublished,
		Coalesced: s.updatesCoalesced,
		Pending:   len(s.updates),
	}
}

// SetExpectedMarketCountProvider preserves the legacy count-only completeness
// hook. New callers should use SetExpectedMarketsProvider so same-count market
// substitutions cannot pass validation.
func (s *TickerService) SetExpectedMarketCountProvider(provider func(domain.Exchange) int) {
	s.mu.Lock()
	s.expectedMarketCountProvider = provider
	s.mu.Unlock()
}

// SetExpectedMarketsProvider connects ticker validation to the exact active
// registry set. It supersedes the count-only compatibility provider when set.
// A configured provider returning an empty slice is still authoritative: a
// non-empty ticker response cannot be verified before registry discovery.
func (s *TickerService) SetExpectedMarketsProvider(provider func(domain.Exchange) []domain.MarketPair) {
	s.mu.Lock()
	s.expectedMarketsProvider = provider
	s.mu.Unlock()
}

func (s *TickerService) Refresh(ctx context.Context) DomesticTickerSummary {
	return s.refreshAndPublish(ctx, ctx)
}

func (s *TickerService) refreshAndPublish(fetchCtx, publishCtx context.Context) DomesticTickerSummary {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	snapshot := s.refresh(fetchCtx)
	s.publishUpdate(publishCtx, snapshot)
	return cloneTickerSummary(snapshot)
}

func (s *TickerService) refresh(ctx context.Context) DomesticTickerSummary {
	started := time.Now()
	s.mu.RLock()
	previous := cloneTickerSummary(s.snapshot).Exchanges
	s.mu.RUnlock()
	type result struct {
		exchange domain.Exchange
		snapshot TickerSnapshot
		err      error
	}
	results := make(chan result, len(domain.DomesticExchanges))
	var wait sync.WaitGroup
	for _, exchange := range domain.DomesticExchanges {
		wait.Add(1)
		go func(name domain.Exchange) {
			defer wait.Done()
			snapshot, err := s.fetch(ctx, name)
			results <- result{name, snapshot, err}
		}(exchange)
	}
	wait.Wait()
	close(results)
	exchanges := make(map[domain.Exchange]TickerSnapshot, len(domain.DomesticExchanges))
	for value := range results {
		if value.err == nil {
			expected, exact, expectationErr := s.expectedMarkets(value.exchange)
			if expectationErr != nil {
				value.err = expectationErr
			} else if exact {
				value.err = validateTickerMarketSet(&value.snapshot, expected)
			} else if validationErr := validateTickerMarketCount(previous[value.exchange], value.snapshot, s.expectedMarketCount(value.exchange)); validationErr != nil {
				value.err = validationErr
			}
		}
		if value.err == nil {
			exchanges[value.exchange] = value.snapshot
			continue
		}
		old := previous[value.exchange]
		old.Exchange = value.exchange
		message := fmt.Sprintf("%T: %v", value.err, value.err)
		old.LastError = &message
		if old.VolumeKRW != nil {
			old.Status = "STALE"
		} else {
			old.Status = "UNAVAILABLE"
		}
		exchanges[value.exchange] = old
	}
	completed := time.Now()
	snapshot := AggregateTickers(exchanges, started, completed)
	s.mu.Lock()
	s.snapshot = snapshot
	s.collectedAt = completed
	s.mu.Unlock()
	return snapshot
}

func (s *TickerService) publishUpdate(ctx context.Context, snapshot DomesticTickerSummary) bool {
	update := cloneTickerSummary(snapshot)
	s.updateMu.Lock()
	defer s.updateMu.Unlock()

	select {
	case <-ctx.Done():
		return false
	default:
	}
	update.UpdateSequence = s.updatesPublished + 1
	update.MissedUpdateCount = 0

	select {
	case s.updates <- update:
		s.updatesPublished++
		return true
	default:
	}

	// A receiver can race with the full-channel observation above. Use a
	// non-blocking drain, then send while updateMu excludes other publishers;
	// either the receiver or this branch has made exactly one slot available.
	select {
	case obsolete := <-s.updates:
		update.MissedUpdateCount = saturatingTickerAdd(obsolete.MissedUpdateCount, 1)
		s.updatesCoalesced++
	default:
	}
	s.updates <- update
	s.updatesPublished++
	return true
}

func saturatingTickerAdd(left, right uint64) uint64 {
	if ^uint64(0)-left < right {
		return ^uint64(0)
	}
	return left + right
}

func revalidateTickerSummary(snapshot DomesticTickerSummary, collectedAt, now time.Time) DomesticTickerSummary {
	result := cloneTickerSummary(snapshot)
	collectionErr := validateTickerCollectionAge(collectedAt, now)
	allLive := result.Status == "LIVE" && collectionErr == nil

	for _, exchange := range domain.DomesticExchanges {
		row, exists := result.Exchanges[exchange]
		if !exists {
			allLive = false
			continue
		}
		if row.Status == "LIVE" {
			err := validateTickerTimestamp(exchange, now, row.ExchangeTimestampMS)
			if err == nil {
				err = collectionErr
			}
			if err != nil {
				row.Status = "STALE"
				message := err.Error()
				row.LastError = &message
			}
		}
		if row.Status != "LIVE" {
			allLive = false
		}
		result.Exchanges[exchange] = row
	}

	complete := result.ReadyExchangeCount == len(domain.DomesticExchanges) &&
		result.TotalVolumeKRW != nil && finiteNonnegative(*result.TotalVolumeKRW)
	if !complete {
		result.Status = "PARTIAL"
	} else if allLive {
		result.Status = "LIVE"
	} else {
		result.Status = "STALE"
	}
	return result
}

func validateTickerCollectionAge(collectedAt, now time.Time) error {
	if collectedAt.IsZero() || now.IsZero() {
		return fmt.Errorf("official ticker collection time is missing")
	}
	age := now.Sub(collectedAt)
	if age > MaxTickerSnapshotAge {
		return fmt.Errorf("official ticker collection is stale by %s", age)
	}
	if age < -MaxTickerExchangeTimestampFutureSkew {
		return fmt.Errorf("official ticker collection is in the future by %s", -age)
	}
	return nil
}

func (s *TickerService) expectedMarketCount(exchange domain.Exchange) int {
	s.mu.RLock()
	provider := s.expectedMarketCountProvider
	s.mu.RUnlock()
	if provider == nil {
		return 0
	}
	return max(0, provider(exchange))
}

func (s *TickerService) expectedMarkets(exchange domain.Exchange) (map[string]struct{}, bool, error) {
	s.mu.RLock()
	provider := s.expectedMarketsProvider
	s.mu.RUnlock()
	if provider == nil {
		return nil, false, nil
	}

	markets := provider(exchange)
	result := make(map[string]struct{}, len(markets))
	for _, market := range markets {
		if market.Exchange != exchange || market.Scope != domain.ScopeDomestic || market.Quote != "KRW" {
			continue
		}
		marketID := CanonicalKRWMarketID(market.Base)
		if marketID == "" {
			return nil, true, fmt.Errorf("%s registry has an empty normalized market identity", exchange)
		}
		if _, duplicate := result[marketID]; duplicate {
			return nil, true, fmt.Errorf("%s registry has duplicate normalized market identity %s", exchange, marketID)
		}
		result[marketID] = struct{}{}
	}
	return result, true, nil
}

func validateTickerMarketSet(current *TickerSnapshot, expected map[string]struct{}) error {
	if current == nil {
		return fmt.Errorf("ticker snapshot is nil")
	}
	if current.MarketCount != len(current.Markets) {
		return fmt.Errorf("%s ticker market identity count mismatch: rows=%d identities=%d", current.Exchange, current.MarketCount, len(current.Markets))
	}
	missing := marketSetDifference(expected, current.Markets)
	extra := marketSetDifference(current.Markets, expected)
	if len(missing) > 0 {
		return fmt.Errorf("%s ticker market set mismatch: missing=%v extra=%v", current.Exchange, missing, extra)
	}

	// Some exchange all-ticker endpoints retain rows for suspended or recently
	// delisted markets after the active registry has removed them. Those rows
	// must not pollute the official denominator, but they also must not make an
	// otherwise complete active-market snapshot unavailable. Rebuild every
	// derived field from the authoritative active registry set. Missing active
	// markets remain a hard error above.
	filteredAssets := make(map[string]float64, len(expected))
	filteredMarkets := make(map[string]struct{}, len(expected))
	filteredTimestamps := make(map[string]int64, len(expected))
	total := 0.0

	var latest int64
	for marketID := range expected {
		asset := strings.TrimPrefix(marketID, "KRW-")
		volume, exists := current.Assets[asset]
		if !exists || !finiteNonnegative(volume) {
			return fmt.Errorf("%s ticker active market %s has no valid volume", current.Exchange, marketID)
		}
		total += volume
		if !finiteNonnegative(total) {
			return fmt.Errorf("%s ticker active market total is not finite", current.Exchange)
		}
		filteredAssets[asset] = volume
		filteredMarkets[marketID] = struct{}{}
		if timestamp := current.MarketTimestampsMS[marketID]; timestamp > 0 {
			filteredTimestamps[marketID] = timestamp
			latest = max(latest, timestamp)
		}
	}
	var timestamp *int64
	if latest > 0 {
		timestamp = &latest
	}
	if err := validateTickerTimestamp(current.Exchange, current.ReceivedAt, timestamp); err != nil {
		return err
	}
	current.Assets = filteredAssets
	current.Markets = filteredMarkets
	current.MarketTimestampsMS = filteredTimestamps
	current.MarketCount = len(filteredMarkets)
	current.VolumeKRW = floatPointer(total)
	current.ExchangeTimestampMS = timestamp
	return nil
}

func marketSetDifference(left, right map[string]struct{}) []string {
	result := make([]string, 0)
	for marketID := range left {
		if _, exists := right[marketID]; !exists {
			result = append(result, marketID)
		}
	}
	slices.Sort(result)
	return result
}

func validateTickerMarketCount(previous, current TickerSnapshot, expected int) error {
	if expected > 0 && current.MarketCount < expected {
		return fmt.Errorf("%s ticker market set is partial: got %d, expected at least %d", current.Exchange, current.MarketCount, expected)
	}
	if previous.MarketCount >= 10 && current.MarketCount*2 < previous.MarketCount {
		return fmt.Errorf("%s ticker market set shrank suspiciously from %d to %d", current.Exchange, previous.MarketCount, current.MarketCount)
	}
	return nil
}

func (s *TickerService) fetch(ctx context.Context, exchange domain.Exchange) (TickerSnapshot, error) {
	if s.client == nil {
		return TickerSnapshot{}, fmt.Errorf("official ticker REST client is nil")
	}
	group := "public"
	if exchange == domain.ExchangeUpbit {
		group = "ticker"
	}
	raw, err := restbudget.FetchJSONBytes(ctx, s.client, restbudget.Request{Exchange: string(exchange), Group: group, URL: TickerURLs[exchange], Priority: restbudget.PriorityTicker, Cost: 1}, 16<<20)
	if err != nil {
		return TickerSnapshot{}, err
	}
	return ParseTicker(exchange, raw, time.Now())
}
func (s *TickerService) run(ctx context.Context) {
	defer s.wait.Done()
	// Short explicit intervals scale the fetch reserve for bounded local/test
	// callers. Production uses 10s = 8s fetch + 1s scheduler reserve + 1s idle.
	fetchTimeout := min(tickerRefreshTimeout, s.interval-s.interval/5)
	idle := s.interval - fetchTimeout - min(tickerSchedulingReserve, s.interval/10)
	for ctx.Err() == nil {
		refreshCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
		s.refreshAndPublish(refreshCtx, ctx)
		cancel()
		// Waiting after completion prevents both overlap and catch-up bursts.
		// AsOf remains the actual response completion time in refresh(); never
		// round it or backdate it to make an interval appear to meet the target.
		timer := time.NewTimer(idle)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func cloneTickerSummary(value DomesticTickerSummary) DomesticTickerSummary {
	result := value
	result.IncludedExchanges = append([]domain.Exchange(nil), value.IncludedExchanges...)
	result.ExcludedExchanges = make(map[domain.Exchange]string, len(value.ExcludedExchanges))
	for exchange, reason := range value.ExcludedExchanges {
		result.ExcludedExchanges[exchange] = reason
	}
	result.Exchanges = make(map[domain.Exchange]TickerSnapshot, len(value.Exchanges))
	for key, row := range value.Exchanges {
		row.VolumeKRW = copyTickerFloat(row.VolumeKRW)
		row.SharePercent = copyTickerFloat(row.SharePercent)
		row.ExchangeTimestampMS = copyTickerInt(row.ExchangeTimestampMS)
		if row.LastError != nil {
			message := *row.LastError
			row.LastError = &message
		}
		row.Assets = make(map[string]float64, len(value.Exchanges[key].Assets))
		for asset, volume := range value.Exchanges[key].Assets {
			row.Assets[asset] = volume
		}
		row.ClosePricesKRW = make(map[string]float64, len(value.Exchanges[key].ClosePricesKRW))
		for asset, closePrice := range value.Exchanges[key].ClosePricesKRW {
			row.ClosePricesKRW[asset] = closePrice
		}
		row.Markets = make(map[string]struct{}, len(value.Exchanges[key].Markets))
		for marketID := range value.Exchanges[key].Markets {
			row.Markets[marketID] = struct{}{}
		}
		row.MarketTimestampsMS = make(map[string]int64, len(value.Exchanges[key].MarketTimestampsMS))
		for marketID, timestamp := range value.Exchanges[key].MarketTimestampsMS {
			row.MarketTimestampsMS[marketID] = timestamp
		}
		result.Exchanges[key] = row
	}
	result.TotalVolumeKRW = copyTickerFloat(value.TotalVolumeKRW)
	result.BithumbVolumeKRW = copyTickerFloat(value.BithumbVolumeKRW)
	result.BithumbSharePercent = copyTickerFloat(value.BithumbSharePercent)
	return result
}
func copyTickerFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func copyTickerInt(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
