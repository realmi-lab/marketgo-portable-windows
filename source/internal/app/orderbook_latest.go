package app

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"market-share-go/internal/domain"
)

const (
	orderBookLatestFlushInterval = time.Second
	orderBookLatestWriteTimeout  = 5 * time.Second
)

var errOrderBookLatestStoreMissing = errors.New("calculated order-book latest store is not configured")

type orderBookLatestStore interface {
	UpsertOrderBookLatest(context.Context, []domain.OrderBookTopSnapshot) error
}

type orderBookLatestStop struct {
	ctx      context.Context
	response chan error
}

type OrderBookLatestMetrics struct {
	Submitted   uint64    `json:"submitted"`
	Replaced    uint64    `json:"replaced_before_flush"`
	Stored      uint64    `json:"stored"`
	Batches     uint64    `json:"batches"`
	Failures    uint64    `json:"failures"`
	Pending     int       `json:"pending"`
	LastSuccess time.Time `json:"last_success"`
	LastError   string    `json:"last_error"`
	LastErrorAt time.Time `json:"last_error_at"`
}

// orderBookLatestWriter is a write-behind latest-value map. Runtime only takes
// its short mutex; PostgreSQL latency never blocks order-book application or
// frontend publication. On failure, a newer snapshot supersedes an older retry.
type orderBookLatestWriter struct {
	store    orderBookLatestStore
	logger   *slog.Logger
	interval time.Duration
	stop     chan orderBookLatestStop
	done     chan struct{}

	mu      sync.Mutex
	stopped bool
	pending map[string]domain.OrderBookTopSnapshot
	metrics OrderBookLatestMetrics
}

func newOrderBookLatestWriter(store orderBookLatestStore, interval time.Duration, logger *slog.Logger) *orderBookLatestWriter {
	if interval <= 0 {
		interval = orderBookLatestFlushInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	writer := &orderBookLatestWriter{
		store: store, logger: logger, interval: interval,
		stop: make(chan orderBookLatestStop, 1), done: make(chan struct{}),
		pending: make(map[string]domain.OrderBookTopSnapshot),
	}
	go writer.run()
	return writer
}

func (w *orderBookLatestWriter) Submit(snapshot domain.OrderBookTopSnapshot) {
	if w == nil || snapshot.Validate() != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	key := snapshot.Key()
	if previous, exists := w.pending[key]; exists {
		w.metrics.Replaced++
		if previous.ReceivedTS > snapshot.ReceivedTS {
			return
		}
	}
	w.pending[key] = snapshot
	w.metrics.Submitted++
}

func (w *orderBookLatestWriter) Stop(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		select {
		case <-w.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	w.stopped = true
	w.mu.Unlock()
	request := orderBookLatestStop{ctx: ctx, response: make(chan error, 1)}
	select {
	case w.stop <- request:
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-request.response:
		<-w.done
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *orderBookLatestWriter) Metrics() OrderBookLatestMetrics {
	if w == nil {
		return OrderBookLatestMetrics{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	metrics := w.metrics
	metrics.Pending = len(w.pending)
	return metrics
}

func (w *orderBookLatestWriter) run() {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), orderBookLatestWriteTimeout)
			_ = w.flush(ctx)
			cancel()
		case request := <-w.stop:
			request.response <- w.flush(request.ctx)
			return
		}
	}
}

func (w *orderBookLatestWriter) flush(ctx context.Context) error {
	snapshots := w.takePending()
	if len(snapshots) == 0 {
		return nil
	}
	err := errOrderBookLatestStoreMissing
	if w.store != nil {
		err = w.store.UpsertOrderBookLatest(ctx, snapshots)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil {
		for _, snapshot := range snapshots {
			key := snapshot.Key()
			if current, exists := w.pending[key]; !exists || current.ReceivedTS < snapshot.ReceivedTS {
				w.pending[key] = snapshot
			}
		}
		w.metrics.Failures++
		w.metrics.LastError = err.Error()
		w.metrics.LastErrorAt = time.Now()
		w.logger.Warn("calculated order-book latest batch failed", "snapshots", len(snapshots), "error", err)
		return err
	}
	w.metrics.Stored += uint64(len(snapshots))
	w.metrics.Batches++
	w.metrics.LastSuccess = time.Now()
	w.metrics.LastError = ""
	return nil
}

func (w *orderBookLatestWriter) takePending() []domain.OrderBookTopSnapshot {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		return nil
	}
	snapshots := make([]domain.OrderBookTopSnapshot, 0, len(w.pending))
	for key, snapshot := range w.pending {
		snapshots = append(snapshots, snapshot)
		delete(w.pending, key)
	}
	sort.Slice(snapshots, func(left, right int) bool {
		return snapshots[left].Key() < snapshots[right].Key()
	})
	return snapshots
}
