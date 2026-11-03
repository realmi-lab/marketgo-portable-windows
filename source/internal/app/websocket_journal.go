package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"market-share-go/internal/domain"
)

const (
	websocketJournalBatchSize     = 512
	websocketJournalFlushInterval = 50 * time.Millisecond
	websocketJournalWriteTimeout  = 5 * time.Second
)

var errWebSocketJournalStopped = errors.New("WebSocket frame journal is stopped")

// WebSocketFrameBatchStore is the durable append boundary shared by the
// production journal and the capacity probe. Implementations must keep retry
// identity idempotent through (connection_id, frame_sequence).
type WebSocketFrameBatchStore interface {
	AppendWebSocketFrames(context.Context, []domain.WebSocketFrame) error
}

type websocketJournalEntry struct {
	sequence uint64
	frame    domain.WebSocketFrame
}

type WebSocketJournalMetrics struct {
	Submitted              uint64    `json:"submitted"`
	SubmittedBytes         uint64    `json:"submitted_bytes"`
	Stored                 uint64    `json:"stored"`
	Flushes                uint64    `json:"flushes"`
	AverageBatch           float64   `json:"average_batch_size"`
	Backpressure           uint64    `json:"backpressure"`
	FlushFailures          uint64    `json:"flush_failures"`
	QueueSize              int       `json:"queue_size"`
	QueueCapacity          int       `json:"queue_capacity"`
	LastSuccess            time.Time `json:"last_success"`
	LastError              string    `json:"last_error"`
	LastErrorAt            time.Time `json:"last_error_at"`
	CaughtUp               bool      `json:"caught_up"`
	PersistenceLive        bool      `json:"persistence_live"`
	AveragePayloadBytes    float64   `json:"average_payload_bytes"`
	AverageFramesPerSecond float64   `json:"average_frames_per_second"`
}

type websocketFrameJournal struct {
	store  WebSocketFrameBatchStore
	logger *slog.Logger
	queue  chan websocketJournalEntry

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	stop   chan struct{}

	acceptMu sync.RWMutex
	stopped  bool
	stopOnce sync.Once
	submitMu sync.Mutex

	submitted      atomic.Uint64
	submittedBytes atomic.Uint64
	committed      atomic.Uint64
	backpressure   atomic.Uint64

	progressMu sync.Mutex
	progress   chan struct{}
	terminal   error

	metricsMu sync.RWMutex
	metrics   WebSocketJournalMetrics
	startedAt time.Time
}

func newWebSocketFrameJournal(store WebSocketFrameBatchStore, queueSize int, logger *slog.Logger) *websocketFrameJournal {
	if queueSize < websocketJournalBatchSize {
		queueSize = websocketJournalBatchSize
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	journal := &websocketFrameJournal{
		store: store, logger: logger, queue: make(chan websocketJournalEntry, queueSize),
		ctx: ctx, cancel: cancel, done: make(chan struct{}), stop: make(chan struct{}),
		progress:  make(chan struct{}),
		startedAt: time.Now(),
	}
	go journal.run()
	return journal
}

func (j *websocketFrameJournal) Submit(ctx context.Context, frame domain.WebSocketFrame) (domain.FrameReceipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	j.submitMu.Lock()
	defer j.submitMu.Unlock()
	j.acceptMu.RLock()
	defer j.acceptMu.RUnlock()
	if j.stopped {
		return 0, errWebSocketJournalStopped
	}
	sequence := j.submitted.Load() + 1
	entry := websocketJournalEntry{sequence: sequence, frame: frame}
	select {
	case j.queue <- entry:
		j.submitted.Store(sequence)
		j.submittedBytes.Add(uint64(len(frame.Payload)))
		return domain.FrameReceipt(sequence), nil
	default:
		j.backpressure.Add(1)
	}
	select {
	case j.queue <- entry:
		j.submitted.Store(sequence)
		j.submittedBytes.Add(uint64(len(frame.Payload)))
		return domain.FrameReceipt(sequence), nil
	case <-j.stop:
		return 0, errWebSocketJournalStopped
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (j *websocketFrameJournal) Wait(ctx context.Context, receipt domain.FrameReceipt) error {
	target := uint64(receipt)
	if target == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if j.committed.Load() >= target {
			return nil
		}
		j.progressMu.Lock()
		if j.committed.Load() >= target {
			j.progressMu.Unlock()
			return nil
		}
		if j.terminal != nil {
			err := j.terminal
			j.progressMu.Unlock()
			return err
		}
		progress := j.progress
		j.progressMu.Unlock()
		select {
		case <-progress:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (j *websocketFrameJournal) Stop(ctx context.Context) error {
	j.stopOnce.Do(func() {
		close(j.stop)
		j.acceptMu.Lock()
		j.stopped = true
		close(j.queue)
		j.acceptMu.Unlock()
	})
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-j.done:
		return nil
	case <-ctx.Done():
		j.cancel()
		<-j.done
		return ctx.Err()
	}
}

func (j *websocketFrameJournal) Metrics() WebSocketJournalMetrics {
	j.metricsMu.RLock()
	metrics := j.metrics
	j.metricsMu.RUnlock()
	metrics.Submitted = j.submitted.Load()
	metrics.SubmittedBytes = j.submittedBytes.Load()
	metrics.Stored = j.committed.Load()
	metrics.Backpressure = j.backpressure.Load()
	metrics.QueueSize = len(j.queue)
	metrics.QueueCapacity = cap(j.queue)
	if metrics.Flushes > 0 {
		metrics.AverageBatch = float64(metrics.Stored) / float64(metrics.Flushes)
	}
	metrics.CaughtUp = metrics.Stored == metrics.Submitted
	metrics.PersistenceLive = metrics.LastError == "" && (metrics.Stored > 0 || metrics.Submitted == 0)
	if metrics.Submitted > 0 {
		metrics.AveragePayloadBytes = float64(metrics.SubmittedBytes) / float64(metrics.Submitted)
	}
	if elapsed := time.Since(j.startedAt).Seconds(); elapsed > 0 {
		metrics.AverageFramesPerSecond = float64(metrics.Submitted) / elapsed
	}
	return metrics
}

func (j *websocketFrameJournal) run() {
	defer close(j.done)
	defer j.failWaiters(errWebSocketJournalStopped)
	for {
		first, ok := <-j.queue
		if !ok {
			return
		}
		batch := make([]websocketJournalEntry, 0, websocketJournalBatchSize)
		batch = append(batch, first)
		timer := time.NewTimer(websocketJournalFlushInterval)
		closed := false
	collect:
		for len(batch) < websocketJournalBatchSize {
			select {
			case entry, open := <-j.queue:
				if !open {
					closed = true
					break collect
				}
				batch = append(batch, entry)
			case <-timer.C:
				break collect
			case <-j.ctx.Done():
				timer.Stop()
				j.failWaiters(j.ctx.Err())
				return
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		if err := j.persistWithRetry(batch); err != nil {
			j.failWaiters(err)
			return
		}
		j.publishCommit(batch[len(batch)-1].sequence)
		if closed {
			return
		}
	}
}

func (j *websocketFrameJournal) persistWithRetry(entries []websocketJournalEntry) error {
	frames := make([]domain.WebSocketFrame, len(entries))
	for index := range entries {
		frames[index] = entries[index].frame
	}
	backoff := 25 * time.Millisecond
	for {
		writeCtx, cancel := context.WithTimeout(j.ctx, websocketJournalWriteTimeout)
		err := j.store.AppendWebSocketFrames(writeCtx, frames)
		cancel()
		if err == nil {
			return nil
		}
		j.metricsMu.Lock()
		j.metrics.FlushFailures++
		j.metrics.LastError = err.Error()
		j.metrics.LastErrorAt = time.Now()
		j.metricsMu.Unlock()
		j.logger.Warn("WebSocket frame journal batch failed; retrying", "frames", len(frames), "error", err)
		timer := time.NewTimer(backoff)
		select {
		case <-timer.C:
		case <-j.ctx.Done():
			timer.Stop()
			return j.ctx.Err()
		}
		backoff = min(time.Second, backoff*2)
	}
}

func (j *websocketFrameJournal) publishCommit(sequence uint64) {
	j.committed.Store(sequence)
	j.metricsMu.Lock()
	j.metrics.Flushes++
	j.metrics.LastSuccess = time.Now()
	j.metrics.LastError = ""
	j.metricsMu.Unlock()
	j.progressMu.Lock()
	close(j.progress)
	j.progress = make(chan struct{})
	j.progressMu.Unlock()
}

func (j *websocketFrameJournal) failWaiters(err error) {
	if err == nil {
		err = errWebSocketJournalStopped
	}
	j.progressMu.Lock()
	if j.terminal == nil {
		j.terminal = err
		close(j.progress)
	}
	j.progressMu.Unlock()
}
