package collector

import (
	"sync"
	"time"

	"market-share-go/internal/domain"
)

type ChunkSnapshot struct {
	Exchange                domain.Exchange      `json:"exchange"`
	Chunk                   int                  `json:"chunk"`
	Connected               bool                 `json:"connected"`
	SubscribedMarkets       int                  `json:"subscribed_markets"`
	MarketSymbols           []string             `json:"market_symbols"`
	ConnectedSince          time.Time            `json:"connected_since"`
	LastDisconnect          time.Time            `json:"last_disconnect"`
	DisconnectedDuration    time.Duration        `json:"disconnected_duration"`
	LastMessage             time.Time            `json:"last_message"`
	LastData                time.Time            `json:"last_data"`
	LastTrade               time.Time            `json:"last_trade"`
	LastOrderBook           time.Time            `json:"last_orderbook"`
	Messages                uint64               `json:"messages"`
	Trades                  uint64               `json:"trades"`
	TradeMatches            uint64               `json:"-"`
	UnknownTradeSides       uint64               `json:"-"`
	TradeMessages           uint64               `json:"trade_messages"`
	TradeSnapshotsSkipped   uint64               `json:"trade_snapshots_skipped"`
	UnparsedTradeEvents     uint64               `json:"unparsed_trade_events"`
	UnparsedTradeMatches    uint64               `json:"-"`
	OrderBooks              uint64               `json:"orderbooks"`
	ParseErrors             uint64               `json:"parse_errors"`
	SubscriptionErrors      uint64               `json:"subscription_errors"`
	ActiveSubscriptionError bool                 `json:"active_subscription_error"`
	ActiveTradeParseError   bool                 `json:"active_trade_parse_error"`
	Reconnects              uint64               `json:"reconnects"`
	LastError               string               `json:"last_error"`
	LastDisconnectError     string               `json:"last_disconnect_error"`
	LastDisconnectErrorAt   time.Time            `json:"last_disconnect_error_at"`
	LastTradeByMarket       map[string]time.Time `json:"last_trade_by_market"`
	LastOrderBookByMarket   map[string]time.Time `json:"last_orderbook_by_market"`

	// tradeSubscriptionSent is connection-scoped evidence that the subscription
	// writes completed. It stays internal so the existing snapshot JSON contract
	// does not change.
	tradeSubscriptionSent     bool
	tradeSubscriptionObserved bool
}

type chunkState struct {
	mu       sync.RWMutex
	snapshot ChunkSnapshot
}

func newChunkState(exchange domain.Exchange, chunk, marketCount int) *chunkState {
	return &chunkState{snapshot: ChunkSnapshot{
		Exchange:              exchange,
		Chunk:                 chunk,
		SubscribedMarkets:     marketCount,
		LastDisconnect:        time.Now(),
		LastTradeByMarket:     make(map[string]time.Time),
		LastOrderBookByMarket: make(map[string]time.Time),
	}}
}

func (s *chunkState) update(change func(*ChunkSnapshot)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	change(&s.snapshot)
}

func (s *chunkState) copy() ChunkSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := s.snapshot
	result.MarketSymbols = append([]string(nil), s.snapshot.MarketSymbols...)
	result.LastTradeByMarket = copyTimes(s.snapshot.LastTradeByMarket)
	result.LastOrderBookByMarket = copyTimes(s.snapshot.LastOrderBookByMarket)
	return result
}

func (s *chunkState) activeSubscriptionError() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot.ActiveSubscriptionError
}

func (s *chunkState) receivedDataSince(start time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.snapshot.LastData.IsZero() && !s.snapshot.LastData.Before(start)
}

func copyTimes(source map[string]time.Time) map[string]time.Time {
	result := make(map[string]time.Time, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (s *chunkState) connected(now time.Time) {
	s.update(func(state *ChunkSnapshot) {
		if !state.LastDisconnect.IsZero() {
			state.DisconnectedDuration += now.Sub(state.LastDisconnect)
		}
		state.Connected = true
		state.ConnectedSince = now
		state.LastDisconnect = time.Time{}
		state.LastMessage = time.Time{}
		state.LastData = time.Time{}
		state.LastTrade = time.Time{}
		state.LastOrderBook = time.Time{}
		state.LastTradeByMarket = make(map[string]time.Time)
		state.LastOrderBookByMarket = make(map[string]time.Time)
		state.LastError = ""
		state.ActiveSubscriptionError = false
		state.ActiveTradeParseError = false
		// collectConnection calls connected only after sendSubscriptions succeeds.
		// Every collector subscription set includes its trade channel.
		state.tradeSubscriptionSent = state.SubscribedMarkets > 0
		state.tradeSubscriptionObserved = false
	})
}

func (s *chunkState) disconnected(now time.Time) {
	s.update(func(state *ChunkSnapshot) {
		if state.Connected {
			state.LastDisconnect = now
		}
		state.Connected = false
		state.ConnectedSince = time.Time{}
		state.tradeSubscriptionSent = false
		state.tradeSubscriptionObserved = false
	})
}
