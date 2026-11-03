package orderbook

import (
	"sort"
	"strings"
	"time"

	"market-share-go/internal/domain"
)

const AnalysisDepth = 10

type BestPrices struct {
	Bid        float64
	Ask        float64
	ReceivedTS domain.UnixMilli
}

type bookState struct {
	pair             domain.MarketPair
	bids             map[float64]domain.OrderBookLevel
	asks             map[float64]domain.OrderBookLevel
	sortedBids       []domain.OrderBookLevel
	sortedAsks       []domain.OrderBookLevel
	initialized      bool
	sequence         *int64
	sourceDepth      *int
	firstReceivedTS  domain.UnixMilli
	lastReceivedTS   domain.UnixMilli
	lastEventTS      domain.UnixMilli
	updateCount      int64
	validUpdateCount int64
	sequenceGapCount int64
	crossedCount     int64
	bestChangeCount  int64
	updateRateHz     float64
	previousBestBid  float64
	previousBestAsk  float64
	hasPreviousBest  bool
}

// bookKey keeps the hot order-book lookup allocation-free. The previous
// exchange+separator+market string key allocated a new composite string for
// every snapshot even though the two canonical identity components already
// have stable string headers.
type bookKey struct {
	exchange domain.Exchange
	market   string
}

// Store has one owner: the application runtime owner goroutine. Callers must
// not use it concurrently. Keeping synchronization outside makes state
// transitions easy to inspect and test.
type Store struct {
	staleAfter time.Duration
	books      map[bookKey]*bookState

	Version        int64
	ReceivedEvents int64
	SequenceGaps   int64
}

func NewStore(staleAfter time.Duration) *Store {
	if staleAfter < time.Second {
		staleAfter = time.Second
	}
	return &Store{
		staleAfter: staleAfter,
		books:      make(map[bookKey]*bookState),
	}
}

func (s *Store) StaleAfterSec() float64 { return s.staleAfter.Seconds() }

func (s *Store) Ingest(event domain.OrderBookEvent) bool {
	s.ReceivedEvents++
	if err := event.Validate(); err != nil {
		return false
	}

	state := s.stateFor(event)
	if !s.acceptSequence(state, event) {
		return false
	}
	if isOldDelta(state, event) {
		return true
	}

	if event.Action == domain.BookActionSnapshot {
		// Full snapshots replace the book at high frequency. Reuse the existing
		// maps instead of allocating two maps per market per coalescer tick.
		clear(state.bids)
		clear(state.asks)
		state.initialized = true
	}
	applyLevels(state.bids, event.Bids)
	applyLevels(state.asks, event.Asks)
	if event.SourceDepth != nil {
		assignInt(&state.sourceDepth, event.SourceDepth)
	}
	if event.Sequence != nil {
		assignInt64(&state.sequence, event.Sequence)
	}
	state.sortAndTrim()
	state.recordUpdate(event)
	s.Version++
	return true
}

func (s *Store) stateFor(event domain.OrderBookEvent) *bookState {
	key := eventKey(event.Exchange, event.Market)
	if state := s.books[key]; state != nil {
		return state
	}
	state := &bookState{
		pair: domain.MarketPair{
			Exchange: event.Exchange,
			Symbol:   event.Market,
			Base:     event.Base,
			Quote:    event.Quote,
			Scope:    event.Scope,
		},
		bids: make(map[float64]domain.OrderBookLevel),
		asks: make(map[float64]domain.OrderBookLevel),
	}
	s.books[key] = state
	return state
}

func (s *Store) acceptSequence(state *bookState, event domain.OrderBookEvent) bool {
	if event.Action != domain.BookActionDelta {
		return true
	}
	if !state.initialized {
		s.recordSequenceGap(state, false)
		return false
	}
	if event.PreviousSequence == nil || state.sequence == nil || *event.PreviousSequence == *state.sequence {
		return true
	}
	s.recordSequenceGap(state, true)
	return false
}

func (s *Store) recordSequenceGap(state *bookState, clear bool) {
	if clear {
		state.initialized = false
		state.bids = make(map[float64]domain.OrderBookLevel)
		state.asks = make(map[float64]domain.OrderBookLevel)
		state.sortedBids = nil
		state.sortedAsks = nil
		s.Version++
	}
	state.sequenceGapCount++
	s.SequenceGaps++
}

func isOldDelta(state *bookState, event domain.OrderBookEvent) bool {
	return event.Action == domain.BookActionDelta &&
		event.PreviousSequence == nil &&
		event.Sequence != nil &&
		state.sequence != nil &&
		*event.Sequence <= *state.sequence
}

func applyLevels(target map[float64]domain.OrderBookLevel, levels []domain.OrderBookLevel) {
	for _, level := range levels {
		previous, found := target[level.Price]
		if found && previous.UpdateID != nil && level.UpdateID != nil && *level.UpdateID <= *previous.UpdateID {
			continue
		}
		if level.Quantity <= 0 {
			delete(target, level.Price)
		} else {
			target[level.Price] = level
		}
	}
}

func (state *bookState) sortAndTrim() {
	state.sortedBids = sortedLevelsInto(state.sortedBids, state.bids, true)
	state.sortedAsks = sortedLevelsInto(state.sortedAsks, state.asks, false)
	if state.sourceDepth == nil || *state.sourceDepth <= 0 {
		return
	}
	state.sortedBids = trimAndClearTail(state.sortedBids, *state.sourceDepth)
	state.sortedAsks = trimAndClearTail(state.sortedAsks, *state.sourceDepth)
	retainLevelMap(state.bids, state.sortedBids)
	retainLevelMap(state.asks, state.sortedAsks)
}

func (state *bookState) recordUpdate(event domain.OrderBookEvent) {
	if state.lastReceivedTS > 0 && event.ReceivedTS > state.lastReceivedTS {
		instantRate := 1000 / float64(event.ReceivedTS-state.lastReceivedTS)
		if state.updateRateHz <= 0 {
			state.updateRateHz = instantRate
		} else {
			state.updateRateHz = state.updateRateHz*0.9 + instantRate*0.1
		}
	}
	if state.firstReceivedTS == 0 {
		state.firstReceivedTS = event.ReceivedTS
	}
	state.lastReceivedTS = event.ReceivedTS
	state.lastEventTS = event.EventTS
	state.updateCount++

	if len(state.sortedBids) == 0 || len(state.sortedAsks) == 0 {
		return
	}
	bestBid := state.sortedBids[0].Price
	bestAsk := state.sortedAsks[0].Price
	if state.hasPreviousBest && (bestBid != state.previousBestBid || bestAsk != state.previousBestAsk) {
		state.bestChangeCount++
	}
	state.previousBestBid = bestBid
	state.previousBestAsk = bestAsk
	state.hasPreviousBest = true
	if bestBid < bestAsk {
		state.validUpdateCount++
	} else {
		state.crossedCount++
	}
}

func (s *Store) BestPrices(exchange domain.Exchange, market string) (BestPrices, bool) {
	state := s.books[eventKey(exchange, market)]
	if state == nil || !state.initialized || len(state.sortedBids) == 0 || len(state.sortedAsks) == 0 {
		return BestPrices{}, false
	}
	best := BestPrices{
		Bid:        state.sortedBids[0].Price,
		Ask:        state.sortedAsks[0].Price,
		ReceivedTS: state.lastReceivedTS,
	}
	return best, best.Bid < best.Ask
}

// TopSnapshot returns an ownership-safe copy of the calculated top ten bids
// and asks after all sequence-sensitive deltas have been applied.
func (s *Store) TopSnapshot(exchange domain.Exchange, market string) (domain.OrderBookTopSnapshot, bool) {
	state := s.books[eventKey(exchange, market)]
	if state == nil || !state.initialized || len(state.sortedBids) == 0 || len(state.sortedAsks) == 0 {
		return domain.OrderBookTopSnapshot{}, false
	}
	bids := trim(state.sortedBids, domain.PersistedOrderBookDepth)
	asks := trim(state.sortedAsks, domain.PersistedOrderBookDepth)
	snapshot := domain.OrderBookTopSnapshot{
		Exchange: state.pair.Exchange, Market: state.pair.Symbol, Base: state.pair.Base, Quote: state.pair.Quote,
		EventTS: state.lastEventTS, ReceivedTS: state.lastReceivedTS, Sequence: copyInt64(state.sequence),
		Bids: append([]domain.OrderBookLevel(nil), bids...), Asks: append([]domain.OrderBookLevel(nil), asks...),
	}
	return snapshot, snapshot.Validate() == nil
}

func (s *Store) ClearExchange(exchange domain.Exchange) int {
	removed := 0
	for key, state := range s.books {
		if state.pair.Exchange == exchange {
			delete(s.books, key)
			removed++
		}
	}
	if removed > 0 {
		s.Version++
	}
	return removed
}

func eventKey(exchange domain.Exchange, market string) bookKey {
	return bookKey{exchange: exchange, market: strings.ToUpper(strings.TrimSpace(market))}
}

func sortedLevels(source map[float64]domain.OrderBookLevel, descending bool) []domain.OrderBookLevel {
	return sortedLevelsInto(nil, source, descending)
}

func sortedLevelsInto(result []domain.OrderBookLevel, source map[float64]domain.OrderBookLevel, descending bool) []domain.OrderBookLevel {
	result = result[:0]
	for _, level := range source {
		result = append(result, level)
	}
	sort.Slice(result, func(i, j int) bool {
		if descending {
			return result[i].Price > result[j].Price
		}
		return result[i].Price < result[j].Price
	})
	return result
}

func trimAndClearTail(levels []domain.OrderBookLevel, depth int) []domain.OrderBookLevel {
	if len(levels) <= depth {
		return levels
	}
	for index := depth; index < len(levels); index++ {
		levels[index] = domain.OrderBookLevel{}
	}
	return levels[:depth]
}

func trim(levels []domain.OrderBookLevel, depth int) []domain.OrderBookLevel {
	if len(levels) <= depth {
		return levels
	}
	return levels[:depth]
}

func levelMap(levels []domain.OrderBookLevel) map[float64]domain.OrderBookLevel {
	result := make(map[float64]domain.OrderBookLevel, len(levels))
	for _, level := range levels {
		result[level.Price] = level
	}
	return result
}

func retainLevelMap(target map[float64]domain.OrderBookLevel, levels []domain.OrderBookLevel) {
	clear(target)
	for _, level := range levels {
		target[level.Price] = level
	}
}

func copyInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func copyInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func assignInt(target **int, source *int) {
	if *target == nil {
		*target = new(int)
	}
	**target = *source
}

func assignInt64(target **int64, source *int64) {
	if *target == nil {
		*target = new(int64)
	}
	**target = *source
}
