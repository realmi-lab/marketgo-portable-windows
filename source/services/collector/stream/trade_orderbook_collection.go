package collector

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	contract "market-share-go/contracts/collection/stream"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"

	"market-share-go/internal/domain"
	"market-share-go/services/collector/exchange"
)

const (
	readTimeout      = 90 * time.Second
	pingInterval     = 20 * time.Second
	maximumBackoff   = 30 * time.Second
	operationTimeout = 10 * time.Second
)

type TradeSink func(context.Context, domain.TradeEvent) error
type OrderBookSink func(context.Context, domain.OrderBookEvent) (bool, error)
type ObservationLossSink func(context.Context, ObservationLoss) error
type FrameSink func(context.Context, domain.WebSocketFrame) (domain.FrameReceipt, error)

// FrameEnvelopeSink durably commits the byte-exact frame and its single parse
// result together, including order books. A failed commit stops this connection.
type FrameEnvelopeSink func(context.Context, domain.WebSocketFrame, exchange.ParsedMessage, error) error

// FrameBatchSink commits an ordered batch before any frame is applied.
type FrameBatchSink func(context.Context, []FrameEnvelope) error
type FrameEnvelope = contract.FrameEnvelope
type FrameWaiter func(context.Context, domain.FrameReceipt) error

type ObservationLoss = contract.ObservationLoss

type Manager struct {
	remote            *remoteSnapshot
	ctx               context.Context
	dialer            Dialer
	onTrade           TradeSink
	onBook            OrderBookSink
	onObservationLoss ObservationLossSink
	onFrame           FrameSink
	onEnvelope        FrameEnvelopeSink
	onBatch           FrameBatchSink
	frameBudget       *semaphore.Weighted
	waitFrame         FrameWaiter
	enabled           map[domain.Exchange]bool
	staleAfter        time.Duration
	logger            *slog.Logger

	lifecycleMu sync.Mutex
	mu          sync.Mutex
	runners     map[domain.Exchange][]*chunkRunner
	markets     map[domain.Exchange][]domain.MarketPair
}

type Options struct {
	StaleAfter        time.Duration
	ObservationLoss   ObservationLossSink
	FrameSink         FrameSink
	FrameEnvelopeSink FrameEnvelopeSink
	FrameBatchSink    FrameBatchSink
	FrameWaiter       FrameWaiter
	Logger            *slog.Logger
}

type chunkRunner struct {
	cancel context.CancelFunc
	done   chan struct{}
	state  *chunkState
}

func NewManager(
	ctx context.Context,
	dialer Dialer,
	onTrade TradeSink,
	onBook OrderBookSink,
	enabled []domain.Exchange,
	options ...Options,
) *Manager {
	enabledSet := make(map[domain.Exchange]bool, len(enabled))
	for _, name := range enabled {
		enabledSet[name] = true
	}
	settings := Options{StaleAfter: 30 * time.Second, Logger: slog.Default()}
	if len(options) > 0 {
		settings.ObservationLoss = options[0].ObservationLoss
		settings.FrameSink = options[0].FrameSink
		settings.FrameEnvelopeSink = options[0].FrameEnvelopeSink
		settings.FrameBatchSink = options[0].FrameBatchSink
		settings.FrameWaiter = options[0].FrameWaiter
		if options[0].Logger != nil {
			settings.Logger = options[0].Logger
		}
		if options[0].StaleAfter >= 5*time.Second {
			settings.StaleAfter = options[0].StaleAfter
		}
	}
	return &Manager{
		ctx: ctx, dialer: dialer, onTrade: onTrade, onBook: onBook,
		onObservationLoss: settings.ObservationLoss,
		onFrame:           settings.FrameSink,
		onEnvelope:        settings.FrameEnvelopeSink,
		onBatch:           settings.FrameBatchSink,
		frameBudget:       semaphore.NewWeighted(frameBatchMemoryBudget),
		waitFrame:         settings.FrameWaiter,
		enabled:           enabledSet,
		staleAfter:        settings.StaleAfter,
		logger:            settings.Logger,
		runners:           make(map[domain.Exchange][]*chunkRunner),
		markets:           make(map[domain.Exchange][]domain.MarketPair),
	}
}

// DefaultEnabled is the production collector policy. Global reference data is
// fetched from official REST snapshots and must never start a WebSocket
// collector through the default application path.
func DefaultEnabled() []domain.Exchange {
	return append([]domain.Exchange(nil), domain.DomesticExchanges[:]...)
}

func (m *Manager) ReplaceMarkets(exchangeName domain.Exchange, markets []domain.MarketPair) error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	if !exchangeName.Valid() {
		return fmt.Errorf("unsupported exchange %q", exchangeName)
	}
	normalized, err := normalizeMarkets(exchangeName, markets)
	if err != nil {
		return err
	}

	m.mu.Lock()
	old := m.runners[exchangeName]
	delete(m.runners, exchangeName)
	m.markets[exchangeName] = normalized
	m.mu.Unlock()
	stopRunners(old)

	if len(normalized) == 0 || !m.enabled[exchangeName] || m.ctx.Err() != nil {
		return nil
	}
	chunkSize := exchange.ChunkSizes[exchangeName]
	chunks := quoteAwareChunks(normalized, chunkSize)
	newRunners := make([]*chunkRunner, 0, len(chunks))
	for _, chunk := range chunks {
		runner := m.startChunk(exchangeName, chunk, len(newRunners)+1)
		newRunners = append(newRunners, runner)
	}
	m.mu.Lock()
	m.runners[exchangeName] = newRunners
	m.mu.Unlock()
	return nil
}

func (m *Manager) Stop() {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	all := make([]*chunkRunner, 0)
	for _, runners := range m.runners {
		all = append(all, runners...)
	}
	m.runners = make(map[domain.Exchange][]*chunkRunner)
	m.mu.Unlock()
	stopRunners(all)
}

func (m *Manager) Snapshots(exchangeName domain.Exchange) []ChunkSnapshot {
	m.mu.Lock()
	runners := append([]*chunkRunner(nil), m.runners[exchangeName]...)
	m.mu.Unlock()
	result := make([]ChunkSnapshot, 0, len(runners))
	for _, runner := range runners {
		result = append(result, runner.state.copy())
	}
	return result
}

func (m *Manager) startChunk(exchangeName domain.Exchange, markets []domain.MarketPair, chunk int) *chunkRunner {
	ctx, cancel := context.WithCancel(m.ctx)
	runner := &chunkRunner{
		cancel: cancel,
		done:   make(chan struct{}),
		state:  newChunkState(exchangeName, chunk, len(markets)),
	}
	runner.state.update(func(snapshot *ChunkSnapshot) {
		for _, market := range markets {
			snapshot.MarketSymbols = append(snapshot.MarketSymbols, market.Symbol)
		}
	})
	go func() {
		defer close(runner.done)
		m.runChunk(ctx, exchangeName, markets, chunk, runner.state)
	}()
	return runner
}

func (m *Manager) runChunk(
	ctx context.Context,
	exchangeName domain.Exchange,
	markets []domain.MarketPair,
	chunk int,
	state *chunkState,
) {
	if !wait(ctx, time.Duration(chunk-1)*350*time.Millisecond+randomDuration(100*time.Millisecond)) {
		return
	}
	universe, err := domain.NewMarketUniverse(markets)
	if err != nil {
		state.update(func(snapshot *ChunkSnapshot) { snapshot.LastError = err.Error() })
		return
	}
	backoff := time.Second
	for ctx.Err() == nil {
		connected, resetBackoff, connectionErr := m.collectConnection(ctx, exchangeName, markets, universe, state)
		if ctx.Err() != nil {
			// A subscription replacement cancels a chunk, not the collector. Its
			// real transport gap must survive even between two health samples.
			if connected && m.ctx.Err() == nil {
				if err := m.reportObservationLoss(m.ctx, ObservationLoss{Exchange: exchangeName, Quote: homogeneousQuote(markets), DetectedAt: time.Now(), Reason: "WEBSOCKET_DISCONNECTED"}); err != nil {
					m.logger.Error("record replaced websocket observation gap", "exchange", exchangeName, "error", err)
				}
			}
			return
		}
		if connected {
			lossErr := m.reportObservationLoss(ctx, ObservationLoss{
				Exchange: exchangeName, Quote: homogeneousQuote(markets),
				DetectedAt: time.Now(), Reason: "WEBSOCKET_DISCONNECTED",
			})
			if lossErr != nil {
				connectionErr = errors.Join(connectionErr, fmt.Errorf("report websocket disconnect: %w", lossErr))
			}
		}
		if resetBackoff {
			backoff = time.Second
		}
		state.update(func(snapshot *ChunkSnapshot) {
			snapshot.Reconnects++
			if connectionErr != nil {
				snapshot.LastError = connectionErr.Error()
				snapshot.LastDisconnectError = connectionErr.Error()
				snapshot.LastDisconnectErrorAt = time.Now()
			}
		})
		if connectionErr != nil {
			m.logger.Warn("exchange websocket reconnecting", "exchange", exchangeName, "chunk", chunk, "error", connectionErr)
		}
		jitter := randomDuration(min(time.Second, backoff/4))
		if !wait(ctx, backoff+jitter) {
			return
		}
		backoff = min(maximumBackoff, backoff*2)
	}
}

func (m *Manager) collectConnection(
	ctx context.Context,
	exchangeName domain.Exchange,
	markets []domain.MarketPair,
	universe domain.MarketUniverse,
	state *chunkState,
) (connected bool, resetBackoff bool, resultErr error) {
	connectionCtx, cancelConnection := context.WithCancel(ctx)
	defer cancelConnection()
	dialCtx, cancelDial := context.WithTimeout(connectionCtx, operationTimeout)
	connection, err := m.dialer.Dial(dialCtx, exchange.WebSocketURLs[exchangeName])
	cancelDial()
	if err != nil {
		return false, false, fmt.Errorf("dial %s: %w", exchangeName, err)
	}
	defer connection.Close()
	connectionID, err := randomConnectionID()
	if err != nil {
		return false, false, fmt.Errorf("create %s connection identity: %w", exchangeName, err)
	}
	connectedAt := time.Time{}
	defer func() {
		if connected {
			resetBackoff = state.receivedDataSince(connectedAt) || time.Since(connectedAt) >= 30*time.Second
		}
		state.disconnected(time.Now())
	}()
	var bookCoalescer *orderBookCoalescer
	if m.onBook != nil && supportsOrderBookCoalescing(exchangeName) {
		bookCoalescer = newOrderBookCoalescer(
			connectionCtx, m.orderBookSinkWithHealth(state), defaultOrderBookCoalesceInterval, len(markets), cancelConnection,
		)
		defer func() {
			drainCtx, cancelDrain := context.WithTimeout(context.WithoutCancel(ctx), orderBookDrainTimeout)
			defer cancelDrain()
			if closeErr := bookCoalescer.Close(drainCtx); closeErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("flush %s order-book snapshots: %w", exchangeName, closeErr))
			}
		}()
	}
	if err := sendSubscriptions(connectionCtx, connection, exchangeName, markets); err != nil {
		return false, false, fmt.Errorf("subscribe %s: %w", exchangeName, err)
	}
	connectedAt = time.Now()
	state.connected(connectedAt)
	pingCtx, cancelPing := context.WithCancel(connectionCtx)
	pingErrors := make(chan error, 1)
	pingDone := make(chan struct{})
	go func() {
		defer close(pingDone)
		keepAlive(pingCtx, connection, exchangeName, pingErrors)
		// A failed heartbeat must interrupt a blocked read immediately.
		if pingCtx.Err() == nil {
			cancelConnection()
		}
	}()
	defer func() { cancelPing(); <-pingDone }()

	if m.onBatch != nil {
		return m.collectBatchedFrames(connectionCtx, connection, exchangeName, connectionID, universe, state, bookCoalescer, pingErrors)
	}
	if m.onFrame == nil && m.onEnvelope == nil {
		return m.collectConnectionInline(
			connectionCtx, connection, exchangeName, universe, state, bookCoalescer, pingErrors,
		)
	}
	if m.onEnvelope == nil && m.waitFrame == nil {
		return true, false, errors.New("frame persistence waiter is not configured")
	}

	type queuedConnectionFrame struct {
		payload    []byte
		receipt    domain.FrameReceipt
		receivedAt time.Time
		message    exchange.ParsedMessage
		parseErr   error
	}
	const connectionFrameQueueSize = 256
	frames := make(chan queuedConnectionFrame, connectionFrameQueueSize)
	processorErrors := make(chan error, 1)
	processorDone := make(chan struct{})
	var closeFramesOnce sync.Once
	closeFrames := func() { closeFramesOnce.Do(func() { close(frames) }) }
	defer func() { cancelConnection(); closeFrames(); <-processorDone }()
	go func() {
		defer close(processorDone)
		for frame := range frames {
			if m.onEnvelope == nil {
				if err := m.waitFrame(connectionCtx, frame.receipt); err != nil {
					processorErrors <- fmt.Errorf("persist %s websocket frame: %w", exchangeName, err)
					cancelConnection()
					return
				}
			}
			if err := m.handleParsedConnectionFrameWithBookCoalescer(
				connectionCtx, connection, exchangeName, universe, frame.payload, frame.receivedAt,
				frame.message, frame.parseErr, state, bookCoalescer,
			); err != nil {
				processorErrors <- err
				cancelConnection()
				return
			}
		}
		processorErrors <- nil
	}()

	var frameSequence uint64
	for {
		if coalescerErr := bookCoalescer.Err(); coalescerErr != nil {
			closeFrames()
			<-processorErrors
			return true, false, coalescerErr
		}
		select {
		case err := <-pingErrors:
			closeFrames()
			<-processorErrors
			return true, false, err
		case err := <-processorErrors:
			return true, false, err
		default:
		}
		readCtx, cancelRead := context.WithTimeout(connectionCtx, readTimeout)
		messageType, payload, err := connection.Read(readCtx)
		cancelRead()
		if err != nil {
			closeFrames()
			processorErr := <-processorErrors
			if processorErr != nil {
				return true, false, processorErr
			}
			if coalescerErr := bookCoalescer.Err(); coalescerErr != nil {
				return true, false, coalescerErr
			}
			return true, false, err
		}
		if messageType != MessageText && messageType != MessageBinary {
			continue
		}
		frameSequence++
		receivedAt := time.Now()
		parsed, parseErr := parseConnectionPayload(exchangeName, payload, universe, receivedAt)
		var receipt domain.FrameReceipt
		if m.onEnvelope != nil || len(parsed.OrderBooks) == 0 {
			frameClass, frameMarket := classifyJournalFrame(parsed, parseErr)
			raw := domain.WebSocketFrame{
				Exchange: exchangeName, ConnectionID: connectionID, Sequence: frameSequence,
				MessageType: int16(messageType), ReceivedAtNS: receivedAt.UnixNano(), Payload: payload,
				FrameClass: frameClass, Market: frameMarket,
			}
			if m.onEnvelope != nil {
				err = m.onEnvelope(connectionCtx, raw, parsed, parseErr)
			} else {
				receipt, err = m.onFrame(connectionCtx, raw)
			}
			if err != nil {
				closeFrames()
				<-processorErrors
				return true, false, fmt.Errorf("journal %s websocket frame: %w", exchangeName, err)
			}
		}
		select {
		case frames <- queuedConnectionFrame{
			payload: payload, receipt: receipt, receivedAt: receivedAt, message: parsed, parseErr: parseErr,
		}:
		case processorErr := <-processorErrors:
			return true, false, processorErr
		case <-connectionCtx.Done():
			select {
			case processorErr := <-processorErrors:
				return true, false, processorErr
			default:
				return true, false, connectionCtx.Err()
			}
		}
	}
}

func (m *Manager) collectConnectionInline(
	ctx context.Context,
	connection Connection,
	exchangeName domain.Exchange,
	universe domain.MarketUniverse,
	state *chunkState,
	bookCoalescer *orderBookCoalescer,
	pingErrors <-chan error,
) (connected bool, resetBackoff bool, resultErr error) {
	for {
		if coalescerErr := bookCoalescer.Err(); coalescerErr != nil {
			return true, false, coalescerErr
		}
		select {
		case err := <-pingErrors:
			return true, false, err
		default:
		}
		readCtx, cancelRead := context.WithTimeout(ctx, readTimeout)
		messageType, payload, err := connection.Read(readCtx)
		cancelRead()
		if err != nil {
			if coalescerErr := bookCoalescer.Err(); coalescerErr != nil {
				return true, false, coalescerErr
			}
			return true, false, err
		}
		if messageType != MessageText && messageType != MessageBinary {
			continue
		}
		if err := m.handleConnectionFrameWithBookCoalescer(
			ctx, connection, exchangeName, universe, payload, state, bookCoalescer,
		); err != nil {
			return true, false, err
		}
	}
}

func randomConnectionID() (string, error) {
	var value [16]byte
	if _, err := cryptorand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (m *Manager) handleConnectionFrame(
	ctx context.Context,
	connection Connection,
	exchangeName domain.Exchange,
	universe domain.MarketUniverse,
	payload []byte,
	state *chunkState,
) error {
	return m.handleConnectionFrameWithBookCoalescer(ctx, connection, exchangeName, universe, payload, state, nil)
}

func (m *Manager) handleConnectionFrameWithBookCoalescer(
	ctx context.Context,
	connection Connection,
	exchangeName domain.Exchange,
	universe domain.MarketUniverse,
	payload []byte,
	state *chunkState,
	bookCoalescer *orderBookCoalescer,
) error {
	handled, err := handleProtocolMessage(ctx, connection, exchangeName, payload)
	if err != nil {
		return err
	}
	if !handled {
		if err := m.handleDataWithBookCoalescer(ctx, exchangeName, universe, payload, state, bookCoalescer); err != nil {
			return err
		}
		if state.activeSubscriptionError() {
			return fmt.Errorf("%s websocket subscription was rejected", exchangeName)
		}
	}
	return nil
}

func (m *Manager) handleParsedConnectionFrameWithBookCoalescer(
	ctx context.Context,
	connection Connection,
	exchangeName domain.Exchange,
	universe domain.MarketUniverse,
	payload []byte,
	receivedAt time.Time,
	message exchange.ParsedMessage,
	parseErr error,
	state *chunkState,
	bookCoalescer *orderBookCoalescer,
) error {
	handled, err := handleProtocolMessage(ctx, connection, exchangeName, payload)
	if err != nil {
		return err
	}
	if handled {
		return nil
	}
	if err := m.handleParsedDataWithBookCoalescer(
		ctx, exchangeName, universe, receivedAt, message, parseErr, state, bookCoalescer,
	); err != nil {
		return err
	}
	if state.activeSubscriptionError() {
		return fmt.Errorf("%s websocket subscription was rejected", exchangeName)
	}
	return nil
}

func (m *Manager) handleData(
	ctx context.Context,
	exchangeName domain.Exchange,
	universe domain.MarketUniverse,
	payload []byte,
	state *chunkState,
) error {
	return m.handleDataWithBookCoalescer(ctx, exchangeName, universe, payload, state, nil)
}

func (m *Manager) handleDataWithBookCoalescer(
	ctx context.Context,
	exchangeName domain.Exchange,
	universe domain.MarketUniverse,
	payload []byte,
	state *chunkState,
	bookCoalescer *orderBookCoalescer,
) error {
	now := time.Now()
	message, err := parseConnectionPayload(exchangeName, payload, universe, now)
	return m.handleParsedDataWithBookCoalescer(
		ctx, exchangeName, universe, now, message, err, state, bookCoalescer,
	)
}

func (m *Manager) handleParsedDataWithBookCoalescer(
	ctx context.Context,
	exchangeName domain.Exchange,
	universe domain.MarketUniverse,
	now time.Time,
	message exchange.ParsedMessage,
	parseErr error,
	state *chunkState,
	bookCoalescer *orderBookCoalescer,
) error {
	state.update(func(snapshot *ChunkSnapshot) {
		snapshot.LastMessage = now
		snapshot.Messages++
	})

	if parseErr != nil {
		state.update(func(snapshot *ChunkSnapshot) {
			snapshot.ParseErrors++
			snapshot.ActiveTradeParseError = true
			snapshot.LastError = parseErr.Error()
		})
		if lossErr := m.reportObservationLoss(ctx, ObservationLoss{
			Exchange: exchangeName, Quote: homogeneousQuote(universe.Markets()),
			DetectedAt: now, Reason: "MESSAGE_PARSE_ERROR",
		}); lossErr != nil {
			return lossErr
		}
		return nil
	}
	if message.SubscriptionError != "" {
		state.update(func(snapshot *ChunkSnapshot) {
			snapshot.SubscriptionErrors++
			snapshot.ActiveSubscriptionError = true
			snapshot.LastError = message.SubscriptionError
		})
		return nil
	}
	kind := message.Kind
	trades, books := message.Trades, message.OrderBooks
	missing := max(0, message.ExpectedTradeEvents-len(trades))
	parsedMatches := uint64(0)
	for _, trade := range trades {
		parsedMatches = saturatingCollectorAdd(parsedMatches, trade.EffectiveMatchCount())
	}
	expectedMatches := message.ExpectedTradeMatches
	if expectedMatches == 0 && message.ExpectedTradeEvents > 0 {
		expectedMatches = uint64(message.ExpectedTradeEvents)
	}
	missingMatches := uint64(0)
	if expectedMatches > parsedMatches {
		missingMatches = expectedMatches - parsedMatches
	}
	state.update(func(snapshot *ChunkSnapshot) {
		if kind == exchange.MessageSnapshot {
			snapshot.TradeSnapshotsSkipped++
		}
		if kind == exchange.MessageTrade {
			snapshot.TradeMessages++
			snapshot.UnparsedTradeEvents += uint64(missing)
			snapshot.UnparsedTradeMatches = saturatingCollectorAdd(snapshot.UnparsedTradeMatches, missingMatches)
			snapshot.ActiveTradeParseError = missing > 0 || missingMatches > 0
		}
		if len(trades) > 0 || len(books) > 0 {
			snapshot.LastData = now
			if !snapshot.ActiveSubscriptionError {
				snapshot.LastError = ""
			}
		}
		if missing > 0 || missingMatches > 0 {
			snapshot.LastError = fmt.Sprintf("UNPARSED_TRADE_ROWS:%d MATCHES:%d", missing, missingMatches)
		}
	})
	if missing > 0 || missingMatches > 0 {
		if err := m.reportObservationLoss(ctx, ObservationLoss{
			Exchange: exchangeName, Quote: homogeneousQuote(universe.Markets()),
			DetectedAt: now, Reason: "TRADE_EVENT_PARSE_ERROR",
			EarliestEventTS: message.EarliestTradeTS, MissingTradeEvents: missing,
			MissingTradeMatches: missingMatches,
		}); err != nil {
			return err
		}
	}

	for _, trade := range trades {
		if m.onTrade == nil {
			return errors.New("trade sink is not configured")
		}
		if err := m.onTrade(ctx, trade); err != nil {
			return err
		}
		state.update(func(snapshot *ChunkSnapshot) {
			snapshot.Trades++
			snapshot.TradeMatches = saturatingCollectorAdd(snapshot.TradeMatches, trade.EffectiveMatchCount())
			snapshot.tradeSubscriptionObserved = true
			if trade.Side == domain.TradeSideUnknown {
				snapshot.UnknownTradeSides++
			}
			snapshot.LastTrade = now
			snapshot.LastTradeByMarket[trade.Market] = now
		})
	}
	for _, book := range books {
		if m.onBook == nil {
			return errors.New("order-book sink is not configured")
		}
		if bookCoalescer != nil && shouldCoalesceOrderBook(book) {
			if err := bookCoalescer.Enqueue(book); err != nil {
				return err
			}
			continue
		}
		accepted, err := m.onBook(ctx, book)
		if err != nil {
			return err
		}
		if !accepted {
			return fmt.Errorf("%s %s orderbook sequence gap", exchangeName, book.Market)
		}
		recordOrderBookObservation(state, book.Market, now)
	}
	return nil
}

func parseConnectionPayload(
	exchangeName domain.Exchange,
	payload []byte,
	universe domain.MarketUniverse,
	receivedAt time.Time,
) (exchange.ParsedMessage, error) {
	return exchange.ParseMessage(exchangeName, payload, universe, domain.UnixMilli(receivedAt.UnixMilli()))
}

func supportsOrderBookCoalescing(exchangeName domain.Exchange) bool {
	switch exchangeName {
	case domain.ExchangeUpbit, domain.ExchangeBithumb, domain.ExchangeCoinone, domain.ExchangeKorbit:
		return true
	default:
		return false
	}
}

func recordOrderBookObservation(state *chunkState, market string, observedAt time.Time) {
	state.update(func(snapshot *ChunkSnapshot) {
		snapshot.OrderBooks++
		snapshot.LastOrderBook = observedAt
		snapshot.LastOrderBookByMarket[market] = observedAt
	})
}

func (m *Manager) orderBookSinkWithHealth(state *chunkState) OrderBookSink {
	return func(ctx context.Context, event domain.OrderBookEvent) (bool, error) {
		accepted, err := m.onBook(ctx, event)
		if err != nil || !accepted {
			return accepted, err
		}
		observedAt := time.UnixMilli(int64(event.ReceivedTS))
		if event.ReceivedTS <= 0 {
			observedAt = time.Now()
		}
		recordOrderBookObservation(state, event.Market, observedAt)
		return true, nil
	}
}

func saturatingCollectorAdd(left, right uint64) uint64 {
	if ^uint64(0)-left < right {
		return ^uint64(0)
	}
	return left + right
}

func (m *Manager) reportObservationLoss(ctx context.Context, loss ObservationLoss) error {
	if m.onObservationLoss == nil {
		return nil
	}
	return m.onObservationLoss(ctx, loss)
}

func sendSubscriptions(ctx context.Context, connection Connection, exchangeName domain.Exchange, markets []domain.MarketPair) error {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	payloads, err := exchange.SubscriptionPayloads(exchangeName, markets)
	if err != nil {
		return err
	}
	for _, payload := range payloads {
		if err := connection.Write(ctx, MessageText, payload); err != nil {
			return err
		}
		if exchangeName == domain.ExchangeCoinone || exchangeName == domain.ExchangeGopax || exchangeName == domain.ExchangeBybit {
			if !wait(ctx, 5*time.Millisecond) {
				return ctx.Err()
			}
		}
	}
	return nil
}

func keepAlive(ctx context.Context, connection Connection, exchangeName domain.Exchange, errors chan<- error) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := sendPing(ctx, connection, exchangeName); err != nil {
				select {
				case errors <- err:
				default:
				}
				return
			}
		}
	}
}

func sendPing(ctx context.Context, connection Connection, exchangeName domain.Exchange) error {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	switch exchangeName {
	case domain.ExchangeCoinone:
		return connection.Write(ctx, MessageText, []byte(`{"request_type":"PING"}`))
	case domain.ExchangeBybit:
		return connection.Write(ctx, MessageText, []byte(`{"op":"ping"}`))
	case domain.ExchangeOKX:
		return connection.Write(ctx, MessageText, []byte("ping"))
	case domain.ExchangeGopax:
		return nil
	default:
		return connection.Ping(ctx)
	}
}

func handleProtocolMessage(ctx context.Context, connection Connection, exchangeName domain.Exchange, payload []byte) (bool, error) {
	if exchangeName == domain.ExchangeGopax && bytes.Contains(payload, []byte("::ping::")) {
		ctx, cancel := context.WithTimeout(ctx, operationTimeout)
		defer cancel()
		return true, connection.Write(ctx, MessageText, bytes.ReplaceAll(payload, []byte("::ping::"), []byte("::pong::")))
	}
	if exchangeName == domain.ExchangeOKX && strings.EqualFold(strings.TrimSpace(string(payload)), "pong") {
		return true, nil
	}
	return false, nil
}

func normalizeMarkets(exchangeName domain.Exchange, markets []domain.MarketPair) ([]domain.MarketPair, error) {
	for _, market := range markets {
		if market.Exchange != exchangeName {
			return nil, fmt.Errorf("market %s belongs to %s, not %s", market.Symbol, market.Exchange, exchangeName)
		}
	}
	universe, err := domain.NewMarketUniverse(markets)
	if err != nil {
		return nil, fmt.Errorf("normalize %s markets: %w", exchangeName, err)
	}
	// MarketUniverse collapses byte-for-byte equivalent identities and rejects
	// conflicting ones. This is the final defensive boundary before chunking so
	// a bad registry caller cannot open two subscriptions for the same market.
	return universe.ForExchange(exchangeName), nil
}

func homogeneousQuote(markets []domain.MarketPair) string {
	quote := ""
	for _, market := range markets {
		current := strings.ToUpper(strings.TrimSpace(market.Quote))
		if current == "" || (quote != "" && current != quote) {
			return ""
		}
		quote = current
	}
	return quote
}

// quoteAwareChunks prevents activity in one quote currency from being used as
// health evidence for another. Domestic KRW markets are started first because
// they are the production market-share scope; the remaining quote groups keep
// a deterministic alphabetical order.
func quoteAwareChunks(markets []domain.MarketPair, chunkSize int) [][]domain.MarketPair {
	if chunkSize <= 0 || len(markets) == 0 {
		return nil
	}
	byQuote := make(map[string][]domain.MarketPair)
	for _, market := range markets {
		quote := strings.ToUpper(strings.TrimSpace(market.Quote))
		byQuote[quote] = append(byQuote[quote], market)
	}
	quotes := make([]string, 0, len(byQuote))
	for quote := range byQuote {
		quotes = append(quotes, quote)
	}
	sort.Slice(quotes, func(i, j int) bool {
		if quotes[i] == "KRW" {
			return quotes[j] != "KRW"
		}
		if quotes[j] == "KRW" {
			return false
		}
		return quotes[i] < quotes[j]
	})

	chunks := make([][]domain.MarketPair, 0, (len(markets)+chunkSize-1)/chunkSize)
	for _, quote := range quotes {
		chunks = append(chunks, balancedMarketChunks(byQuote[quote], chunkSize)...)
	}
	return chunks
}

// balancedMarketChunks avoids leaving one tiny tail connection containing
// only alphabetically late, low-activity markets. Every connection still has
// at most chunkSize subscriptions, but round-robin distribution gives each
// connection a representative part of the quote group. This lets the strict
// per-connection trade health check obtain real evidence without weakening it.
func balancedMarketChunks(markets []domain.MarketPair, chunkSize int) [][]domain.MarketPair {
	if chunkSize <= 0 || len(markets) == 0 {
		return nil
	}
	chunkCount := (len(markets) + chunkSize - 1) / chunkSize
	chunks := make([][]domain.MarketPair, chunkCount)
	capacity := (len(markets) + chunkCount - 1) / chunkCount
	for index, market := range markets {
		chunk := index % chunkCount
		if chunks[chunk] == nil {
			chunks[chunk] = make([]domain.MarketPair, 0, capacity)
		}
		chunks[chunk] = append(chunks[chunk], market)
	}
	return chunks
}

func stopRunners(runners []*chunkRunner) {
	for _, runner := range runners {
		runner.cancel()
	}
	for _, runner := range runners {
		<-runner.done
	}
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func randomDuration(limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(limit)))
}
