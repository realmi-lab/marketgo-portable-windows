package collector

import (
	contract "market-share-go/contracts/collection/stream"
	"sort"
	"strings"
	"time"

	"market-share-go/internal/domain"
)

type ExchangeHealth = contract.ExchangeHealth

type marketUpdate struct {
	connected         bool
	subscriptionError bool
	trade             time.Time
	book              time.Time
}

// MarketActivity is the per-market and containing-chunk evidence used to
// distinguish a genuinely stale order book from a quiet market.
type MarketActivity = contract.MarketActivity

func (m *Manager) MarketActivitySnapshot(now time.Time) map[string]MarketActivity {
	if remote := m.remoteSnapshotAt(now); remote != nil {
		return remote.Activity
	}
	if now.IsZero() {
		now = time.Now()
	}
	m.mu.Lock()
	runners := make(map[domain.Exchange][]*chunkRunner, len(m.runners))
	for exchange, values := range m.runners {
		runners[exchange] = append([]*chunkRunner(nil), values...)
	}
	m.mu.Unlock()
	result := make(map[string]MarketActivity)
	for exchange, values := range runners {
		for _, runner := range values {
			state := runner.state.copy()
			for _, market := range state.MarketSymbols {
				result[string(exchange)+"\x00"+strings.ToUpper(strings.TrimSpace(market))] = MarketActivity{
					Connected: state.Connected, SubscriptionError: state.ActiveSubscriptionError,
					TradeAgeSec:          agePointer(state.LastTradeByMarket[market], now),
					OrderBookAgeSec:      agePointer(state.LastOrderBookByMarket[market], now),
					ChunkOrderBookAgeSec: agePointer(state.LastOrderBook, now),
					ChunkDataAgeSec:      agePointer(state.LastData, now),
				}
			}
		}
	}
	return result
}

func agePointer(value, now time.Time) *float64 {
	if value.IsZero() {
		return nil
	}
	age := now.Sub(value).Seconds()
	if age < 0 {
		age = 0
	}
	return &age
}

func (m *Manager) Health(now time.Time) map[domain.Exchange]ExchangeHealth {
	if remote := m.remoteSnapshotAt(now); remote != nil {
		return remote.Exchanges
	}
	if now.IsZero() {
		now = time.Now()
	}
	m.mu.Lock()
	runnersByExchange := make(map[domain.Exchange][]*chunkRunner, len(m.runners))
	marketsByExchange := make(map[domain.Exchange][]domain.MarketPair, len(m.markets))
	for name, runners := range m.runners {
		runnersByExchange[name] = append([]*chunkRunner(nil), runners...)
	}
	for name, markets := range m.markets {
		marketsByExchange[name] = append([]domain.MarketPair(nil), markets...)
	}
	m.mu.Unlock()

	result := make(map[domain.Exchange]ExchangeHealth)
	for _, name := range collectorExchanges() {
		states := make([]ChunkSnapshot, 0, len(runnersByExchange[name]))
		for _, runner := range runnersByExchange[name] {
			states = append(states, runner.state.copy())
		}
		result[name] = m.exchangeHealth(name, marketsByExchange[name], states, now)
	}
	return result
}

func (m *Manager) exchangeHealth(
	name domain.Exchange,
	markets []domain.MarketPair,
	states []ChunkSnapshot,
	now time.Time,
) ExchangeHealth {
	enabled := m.enabled[name]
	orderBookEnabled := name != domain.ExchangeBinance
	health := ExchangeHealth{
		Enabled: enabled, OrderBookEnabled: orderBookEnabled,
		MarketCount: len(markets), ChunkCount: len(states),
		Transport: "WEBSOCKET_ONLY", Errors: []string{},
	}
	if !enabled {
		health.DisabledReason = "POLICY_DISABLED"
	}
	if !orderBookEnabled {
		health.OrderBookDisabledReason = "BINANCE_DEPTH_STREAM_DISABLED"
	}
	assets := make(map[string]struct{})
	krwMarkets := make(map[string]struct{})
	for _, market := range markets {
		assets[market.Base] = struct{}{}
		if strings.EqualFold(strings.TrimSpace(market.Quote), "KRW") {
			krwMarkets[strings.ToUpper(strings.TrimSpace(market.Symbol))] = struct{}{}
		}
	}
	health.AssetCount = len(assets)
	health.KRWMarketCount = len(krwMarkets)
	updates := make(map[string]marketUpdate)
	subscribedSymbols := make(map[string]struct{})
	krwSubscribedSymbols := make(map[string]struct{})
	allTradeReady := len(states) > 0
	allBooksReady := len(states) > 0
	allKRWRealtimeReady := true
	allKRWTradeReady := true
	allKRWTradeActivityFresh := true
	for _, state := range states {
		m.addChunkHealth(&health, state, now, orderBookEnabled)
		freshData := state.Connected && fresh(state.LastData, now, m.staleAfter) && !state.ActiveSubscriptionError
		if freshData {
			health.FreshChunks++
			health.FreshSubscriptionMarketCount += state.SubscribedMarkets
		}
		tradeChannelHealthy := state.Connected && !state.ActiveSubscriptionError && !state.ActiveTradeParseError
		tradeFresh := tradeChannelHealthy && fresh(state.LastTrade, now, m.staleAfter)
		// A successful subscription write or an old observed trade proves only
		// historical channel activity. Fresh order books, other data, and
		// ping/pong traffic cannot prove that the trade stream is still alive.
		// Require recent trade evidence to avoid confirming incomplete volume.
		allTradeReady = allTradeReady && tradeFresh
		bookReady := state.Connected && fresh(state.LastOrderBook, now, m.staleAfter) && !state.ActiveSubscriptionError
		allBooksReady = allBooksReady && bookReady
		krwSymbols, krwDataAt, krwTradeAt := krwEvidence(state, krwMarkets)
		if len(krwSymbols) > 0 {
			for _, symbol := range krwSymbols {
				krwSubscribedSymbols[symbol] = struct{}{}
			}
			health.KRWSubscribedMarketCount += len(krwSymbols)
			krwRealtimeReady, krwTradeReady, krwTradeActivityFresh := m.addKRWChunkHealth(
				&health, state, len(krwSymbols), krwDataAt, krwTradeAt, now,
			)
			allKRWRealtimeReady = allKRWRealtimeReady && krwRealtimeReady
			allKRWTradeReady = allKRWTradeReady && krwTradeReady
			allKRWTradeActivityFresh = allKRWTradeActivityFresh && krwTradeActivityFresh
		}
		for _, symbol := range state.MarketSymbols {
			subscribedSymbols[strings.ToUpper(strings.TrimSpace(symbol))] = struct{}{}
			updates[symbol] = marketUpdate{
				connected: state.Connected, subscriptionError: state.ActiveSubscriptionError,
				trade: state.LastTradeByMarket[symbol], book: state.LastOrderBookByMarket[symbol],
			}
		}
	}
	health.SubscribedMarketSymbols = make([]string, 0, len(subscribedSymbols))
	for symbol := range subscribedSymbols {
		health.SubscribedMarketSymbols = append(health.SubscribedMarketSymbols, symbol)
	}
	sort.Strings(health.SubscribedMarketSymbols)
	health.KRWSubscribedMarketSymbols = make([]string, 0, len(krwSubscribedSymbols))
	for symbol := range krwSubscribedSymbols {
		health.KRWSubscribedMarketSymbols = append(health.KRWSubscribedMarketSymbols, symbol)
	}
	sort.Strings(health.KRWSubscribedMarketSymbols)
	for _, update := range updates {
		tradeFresh := update.connected && !update.subscriptionError && fresh(update.trade, now, m.staleAfter)
		bookFresh := update.connected && !update.subscriptionError && fresh(update.book, now, m.staleAfter)
		if tradeFresh || bookFresh {
			health.FreshMarketCount++
		}
		if tradeFresh {
			health.FreshTradeMarketCount++
		}
		if bookFresh {
			health.FreshOrderBookMarketCount++
		}
		if !update.trade.IsZero() {
			health.ObservedTradeMarketCount++
		}
		if !update.book.IsZero() {
			health.ObservedOrderBookMarketCount++
		}
	}
	health.ConnectedMarketPercent = ratio(health.ConnectedMarketCount, health.MarketCount)
	health.FreshMarketPercent = ratio(health.FreshMarketCount, health.MarketCount)
	health.FreshSubscriptionPercent = ratio(health.FreshSubscriptionMarketCount, health.MarketCount)
	health.RealtimeReady = enabled && health.MarketCount > 0 && len(states) > 0 && health.FreshChunks == len(states)
	health.TradeStreamReady = enabled && health.MarketCount > 0 && allTradeReady
	krwSubscriptionComplete := health.KRWMarketCount > 0 &&
		health.KRWSubscribedMarketCount == health.KRWMarketCount &&
		len(health.KRWSubscribedMarketSymbols) == health.KRWMarketCount
	health.KRWRealtimeReady = enabled && krwSubscriptionComplete && health.KRWChunkCount > 0 &&
		health.KRWFreshChunks == health.KRWChunkCount && allKRWRealtimeReady
	health.KRWTradeStreamReady = enabled && krwSubscriptionComplete && health.KRWChunkCount > 0 &&
		allKRWTradeReady
	health.KRWTradeActivityFresh = enabled && krwSubscriptionComplete && health.KRWChunkCount > 0 &&
		allKRWTradeActivityFresh
	health.TradeActivityFresh = health.TradeStreamReady
	health.OrderBookStreamReady = orderBookEnabled && enabled && health.MarketCount > 0 && allBooksReady
	return health
}

func krwEvidence(
	state ChunkSnapshot,
	krwMarkets map[string]struct{},
) (symbols []string, dataAt, tradeAt time.Time) {
	seen := make(map[string]struct{})
	for _, rawSymbol := range state.MarketSymbols {
		symbol := strings.ToUpper(strings.TrimSpace(rawSymbol))
		if _, ok := krwMarkets[symbol]; !ok {
			continue
		}
		if _, duplicate := seen[symbol]; duplicate {
			continue
		}
		seen[symbol] = struct{}{}
		symbols = append(symbols, symbol)
		marketTrade := state.LastTradeByMarket[rawSymbol]
		marketBook := state.LastOrderBookByMarket[rawSymbol]
		tradeAt = laterTime(tradeAt, marketTrade)
		dataAt = laterTime(dataAt, laterTime(marketTrade, marketBook))
	}
	return symbols, dataAt, tradeAt
}

func (m *Manager) addKRWChunkHealth(
	health *ExchangeHealth,
	state ChunkSnapshot,
	marketCount int,
	dataAt, tradeAt, now time.Time,
) (realtimeReady, tradeReady, tradeActivityFresh bool) {
	health.KRWChunkCount++
	if state.Connected {
		health.KRWConnectedChunks++
		health.KRWConnectedMarketCount += marketCount
	}
	realtimeReady = state.Connected && !state.ActiveSubscriptionError && fresh(dataAt, now, m.staleAfter)
	if realtimeReady {
		health.KRWFreshChunks++
	} else if state.Connected && !dataAt.IsZero() {
		health.KRWStaleChunks++
	}
	if tradeAt.IsZero() {
		health.KRWNeverReceivedTradeChunks++
	}
	if state.ActiveSubscriptionError {
		health.KRWActiveSubscriptionErrorChunks++
	}
	if state.ActiveTradeParseError {
		health.KRWActiveTradeParseErrorChunks++
	}
	health.KRWUnparsedTradeEvents += state.UnparsedTradeEvents
	health.KRWUnknownTradeSides += state.UnknownTradeSides
	// A connected KRW chunk with fresh market data proves that the exact
	// quote-scoped subscription is alive. Zero trades during a quiet interval
	// is a valid observation, not missing data. Subscription and parser errors
	// still fail closed, while activity freshness remains separately visible.
	tradeReady = realtimeReady && !state.ActiveTradeParseError
	tradeActivityFresh = tradeReady && fresh(tradeAt, now, m.staleAfter)
	updateKRWAges(health, dataAt, tradeAt)
	return realtimeReady, tradeReady, tradeActivityFresh
}

func updateKRWAges(health *ExchangeHealth, dataAt, tradeAt time.Time) {
	if !dataAt.IsZero() {
		timestamp := unixSeconds(dataAt)
		if health.KRWOldestChunkDataTS == 0 || timestamp < health.KRWOldestChunkDataTS {
			health.KRWOldestChunkDataTS = timestamp
		}
	}
	if !tradeAt.IsZero() {
		timestamp := unixSeconds(tradeAt)
		if health.KRWOldestChunkTradeTS == 0 || timestamp < health.KRWOldestChunkTradeTS {
			health.KRWOldestChunkTradeTS = timestamp
		}
	}
}

func laterTime(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}
	return left
}

func (m *Manager) addChunkHealth(health *ExchangeHealth, state ChunkSnapshot, now time.Time, orderBookEnabled bool) {
	health.SubscribedMarketCount += state.SubscribedMarkets
	if state.Connected {
		health.ConnectedChunks++
		health.ConnectedMarketCount += state.SubscribedMarkets
	}
	if state.LastData.IsZero() {
		health.NeverReceivedChunks++
	} else if state.Connected && !fresh(state.LastData, now, m.staleAfter) {
		health.StaleChunks++
	}
	if state.LastTrade.IsZero() {
		health.NeverReceivedTradeChunks++
	}
	if orderBookEnabled && state.LastOrderBook.IsZero() {
		health.NeverReceivedOrderBookChunks++
	}
	if state.ActiveSubscriptionError {
		health.ActiveSubscriptionErrorChunks++
	}
	if state.ActiveTradeParseError {
		health.ActiveTradeParseErrorChunks++
	}
	health.Messages += state.Messages
	health.Trades += state.Trades
	health.TradeMatches = saturatingCollectorAdd(health.TradeMatches, state.TradeMatches)
	health.UnknownTradeSides += state.UnknownTradeSides
	health.TradeMessages += state.TradeMessages
	health.TradeSnapshotsSkipped += state.TradeSnapshotsSkipped
	health.UnparsedTradeEvents += state.UnparsedTradeEvents
	health.UnparsedTradeMatches = saturatingCollectorAdd(health.UnparsedTradeMatches, state.UnparsedTradeMatches)
	health.OrderBooks += state.OrderBooks
	health.ParseErrors += state.ParseErrors
	health.SubscriptionErrors += state.SubscriptionErrors
	health.Reconnects += state.Reconnects
	if !state.LastDisconnectErrorAt.IsZero() {
		disconnectAt := unixSeconds(state.LastDisconnectErrorAt)
		if disconnectAt >= health.LastDisconnectErrorTS {
			health.LastDisconnectErrorTS = disconnectAt
			health.LastDisconnectError = state.LastDisconnectError
		}
	}
	disconnected := state.DisconnectedDuration
	if !state.LastDisconnect.IsZero() {
		disconnected += now.Sub(state.LastDisconnect)
	}
	health.DisconnectedSeconds += disconnected.Seconds()
	if state.LastError != "" {
		health.Errors = append(health.Errors, state.LastError)
	}
	updateAges(health, state, now)
}

func updateAges(health *ExchangeHealth, state ChunkSnapshot, now time.Time) {
	if !state.LastMessage.IsZero() {
		timestamp := unixSeconds(state.LastMessage)
		if health.LastMessageTS == 0 || timestamp > health.LastMessageTS {
			health.LastMessageTS = timestamp
		}
		if health.OldestChunkMessageTS == 0 || timestamp < health.OldestChunkMessageTS {
			health.OldestChunkMessageTS = timestamp
		}
		health.MaxMessageAgeSec = maximumAge(health.MaxMessageAgeSec, now.Sub(state.LastMessage).Seconds())
	}
	if !state.LastData.IsZero() {
		health.MaxDataAgeSec = maximumAge(health.MaxDataAgeSec, now.Sub(state.LastData).Seconds())
	}
	if !state.LastTrade.IsZero() {
		timestamp := unixSeconds(state.LastTrade)
		health.LastTradeTS = maxFloat(health.LastTradeTS, timestamp)
		if health.OldestChunkTradeTS == 0 || timestamp < health.OldestChunkTradeTS {
			health.OldestChunkTradeTS = timestamp
		}
		health.MaxTradeAgeSec = maximumAge(health.MaxTradeAgeSec, now.Sub(state.LastTrade).Seconds())
	}
	if !state.LastOrderBook.IsZero() {
		health.LastOrderBookTS = maxFloat(health.LastOrderBookTS, unixSeconds(state.LastOrderBook))
		health.MaxOrderBookAgeSec = maximumAge(health.MaxOrderBookAgeSec, now.Sub(state.LastOrderBook).Seconds())
	}
}

func fresh(value, now time.Time, maximum time.Duration) bool {
	return !value.IsZero() && now.Sub(value) <= maximum
}

func ratio(part, whole int) *float64 {
	if whole <= 0 {
		return nil
	}
	value := float64(part) / float64(whole) * 100
	return &value
}

func maximumAge(current *float64, candidate float64) *float64 {
	if candidate < 0 {
		candidate = 0
	}
	if current == nil || candidate > *current {
		value := candidate
		return &value
	}
	return current
}

func unixSeconds(value time.Time) float64 {
	return float64(value.UnixNano()) / float64(time.Second)
}

func maxFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}

func collectorExchanges() []domain.Exchange {
	result := make([]domain.Exchange, 0, len(domain.DomesticExchanges)+len(domain.GlobalExchanges))
	result = append(result, domain.DomesticExchanges[:]...)
	result = append(result, domain.GlobalExchanges[:]...)
	return result
}
