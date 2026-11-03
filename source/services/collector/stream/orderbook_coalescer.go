package collector

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"market-share-go/internal/domain"
)

const (
	defaultOrderBookCoalesceInterval = 75 * time.Millisecond
	orderBookDrainTimeout            = 2 * time.Second
)

var errOrderBookCoalescerClosed = errors.New("order-book snapshot coalescer is closed")

type pendingOrderBook struct {
	event domain.OrderBookEvent
}

type orderBookCoalescerStop struct {
	ctx      context.Context
	response chan error
}

// orderBookCoalescer decouples full-snapshot WebSocket reads from the
// synchronous order-book sink. At most one parser-owned snapshot per subscribed
// market is retained, so a slow sink cannot create an unbounded second queue.
//
// Sequence-sensitive streams are deliberately excluded by
// shouldCoalesceOrderBook. They continue through the collector's synchronous
// path in exact arrival order.
type orderBookCoalescer struct {
	sink             OrderBookSink
	interval         time.Duration
	maxPending       int
	cancelConnection context.CancelFunc
	runCtx           context.Context
	cancelRun        context.CancelFunc

	mu           sync.Mutex
	pending      map[string]pendingOrderBook
	keysScratch  []string
	batchScratch []pendingOrderBook
	terminalErr  error
	closing      bool

	stop chan orderBookCoalescerStop
	done chan struct{}
}

func newOrderBookCoalescer(
	parent context.Context,
	sink OrderBookSink,
	interval time.Duration,
	maxPending int,
	cancelConnection context.CancelFunc,
) *orderBookCoalescer {
	if interval <= 0 {
		interval = defaultOrderBookCoalesceInterval
	}
	if maxPending < 1 {
		maxPending = 1
	}
	// Connection cancellation must stop reads immediately, but a graceful
	// collector shutdown still gets one bounded final snapshot flush before the
	// runtime is stopped. Close owns this detached context and always cancels it.
	runCtx, cancelRun := context.WithCancel(context.WithoutCancel(parent))
	coalescer := &orderBookCoalescer{
		sink:             sink,
		interval:         interval,
		maxPending:       maxPending,
		cancelConnection: cancelConnection,
		runCtx:           runCtx,
		cancelRun:        cancelRun,
		pending:          make(map[string]pendingOrderBook, maxPending),
		stop:             make(chan orderBookCoalescerStop, 1),
		done:             make(chan struct{}),
	}
	go coalescer.run()
	return coalescer
}

func shouldCoalesceOrderBook(event domain.OrderBookEvent) bool {
	if event.Action != domain.BookActionSnapshot {
		return false
	}
	switch event.Exchange {
	case domain.ExchangeUpbit, domain.ExchangeBithumb, domain.ExchangeCoinone, domain.ExchangeKorbit:
		return true
	default:
		// GOPAX sends sequence-sensitive snapshots and deltas. Global depth
		// streams may also be delta based. Neither category may pass here.
		return false
	}
}

func (c *orderBookCoalescer) Enqueue(event domain.OrderBookEvent) error {
	if c == nil {
		return errors.New("order-book snapshot coalescer is not configured")
	}
	if !shouldCoalesceOrderBook(event) {
		return fmt.Errorf("order-book event %s %s %s is not coalescible", event.Exchange, event.Market, event.Action)
	}
	if err := event.Validate(); err != nil {
		return fmt.Errorf("validate order-book snapshot: %w", err)
	}

	key := orderBookCoalescerKey(event)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return errOrderBookCoalescerClosed
	}
	if c.terminalErr != nil {
		return c.terminalErr
	}
	if _, exists := c.pending[key]; !exists && len(c.pending) >= c.maxPending {
		return fmt.Errorf("order-book snapshot coalescer capacity %d exceeded", c.maxPending)
	}
	// Enqueue is an internal ownership-transfer boundary: parsers allocate fresh
	// event slices for every frame and never mutate them after this call. Keeping
	// those slices avoids duplicating every order-book level on the hot path.
	c.pending[key] = pendingOrderBook{event: event}
	return nil
}

func (c *orderBookCoalescer) Err() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.terminalErr
}

func (c *orderBookCoalescer) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		select {
		case <-c.done:
			return c.Err()
		case <-ctx.Done():
			c.cancelRun()
			return ctx.Err()
		}
	}
	c.closing = true
	c.mu.Unlock()

	response := make(chan error, 1)
	// The channel is buffered and this is the sole Close caller in production,
	// so the stop request remains deliverable even if the caller's deadline
	// expires while a sink call is completing.
	c.stop <- orderBookCoalescerStop{ctx: ctx, response: response}
	select {
	case err := <-response:
		return err
	case <-ctx.Done():
		c.cancelRun()
		return ctx.Err()
	}
}

func (c *orderBookCoalescer) run() {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	defer c.cancelRun()
	defer close(c.done)
	for {
		select {
		case <-ticker.C:
			if c.Err() == nil {
				_ = c.flush(c.runCtx)
			}
		case request := <-c.stop:
			err := c.Err()
			if err == nil {
				err = c.flush(request.ctx)
			}
			c.clearPending()
			request.response <- err
			return
		}
	}
}

func (c *orderBookCoalescer) flush(ctx context.Context) error {
	batch := c.takePending()
	defer c.recycleBatch(batch)
	for _, pending := range batch {
		accepted, err := c.sink(ctx, pending.event)
		if err != nil {
			return c.fail(err)
		}
		if !accepted {
			return c.fail(fmt.Errorf("%s %s orderbook sequence gap", pending.event.Exchange, pending.event.Market))
		}
	}
	return nil
}

func (c *orderBookCoalescer) takePending() []pendingOrderBook {
	c.mu.Lock()
	keys := c.keysScratch[:0]
	c.keysScratch = nil
	if cap(keys) < len(c.pending) {
		keys = make([]string, 0, len(c.pending))
	}
	for key := range c.pending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := c.batchScratch[:0]
	c.batchScratch = nil
	if cap(result) < len(keys) {
		result = make([]pendingOrderBook, 0, len(keys))
	}
	for _, key := range keys {
		result = append(result, c.pending[key])
	}
	clear(c.pending)
	for index := range keys {
		keys[index] = ""
	}
	c.keysScratch = keys[:0]
	c.mu.Unlock()
	return result
}

func (c *orderBookCoalescer) recycleBatch(batch []pendingOrderBook) {
	for index := range batch {
		batch[index] = pendingOrderBook{}
	}
	c.mu.Lock()
	if cap(batch) > cap(c.batchScratch) {
		c.batchScratch = batch[:0]
	}
	c.mu.Unlock()
}

func (c *orderBookCoalescer) clearPending() {
	c.mu.Lock()
	clear(c.pending)
	c.mu.Unlock()
}

func (c *orderBookCoalescer) fail(err error) error {
	if err == nil {
		return nil
	}
	c.mu.Lock()
	if c.terminalErr == nil {
		c.terminalErr = err
	}
	result := c.terminalErr
	c.mu.Unlock()
	if c.cancelConnection != nil {
		c.cancelConnection()
	}
	return result
}

func orderBookCoalescerKey(event domain.OrderBookEvent) string {
	return string(event.Exchange) + "\x00" + strings.ToUpper(strings.TrimSpace(event.Market))
}
