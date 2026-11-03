package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sort"
	"sync"
	"time"

	"market-share-go/internal/domain"
)

const WebSocketJournalProbeContractVersion = "WEBSOCKET_JOURNAL_PROBE_V1"

// WebSocketJournalProbeResult records the production batcher's observable
// queue and write behavior. PostgreSQL relation/WAL and compression evidence
// is collected by the command after this owner has committed every frame.
type WebSocketJournalProbeResult struct {
	ContractVersion string                  `json:"contract_version"`
	Metrics         WebSocketJournalMetrics `json:"metrics"`
	SubmitSeconds   float64                 `json:"submit_seconds"`
	CommitSeconds   float64                 `json:"commit_seconds"`
	FramesPerSecond float64                 `json:"frames_per_second"`
	BatchP50MS      float64                 `json:"batch_p50_ms"`
	BatchP95MS      float64                 `json:"batch_p95_ms"`
	BatchP99MS      float64                 `json:"batch_p99_ms"`
	BatchMaxMS      float64                 `json:"batch_max_ms"`
}

type measuredWebSocketFrameBatchStore struct {
	next WebSocketFrameBatchStore
	mu   sync.Mutex
	took []time.Duration
}

func (s *measuredWebSocketFrameBatchStore) AppendWebSocketFrames(ctx context.Context, frames []domain.WebSocketFrame) error {
	started := time.Now()
	err := s.next.AppendWebSocketFrames(ctx, frames)
	s.mu.Lock()
	s.took = append(s.took, time.Since(started))
	s.mu.Unlock()
	return err
}

func (s *measuredWebSocketFrameBatchStore) durations() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.took...)
}

// ProbeWebSocketFrameJournal submits an already-materialized deterministic
// fixture through the same bounded queue, batching, retry, and commit barrier
// used by production. It does not generate frames or alter retention policy.
func ProbeWebSocketFrameJournal(
	ctx context.Context,
	store WebSocketFrameBatchStore,
	frames []domain.WebSocketFrame,
	queueSize int,
) (WebSocketJournalProbeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if store == nil {
		return WebSocketJournalProbeResult{}, errors.New("WebSocket journal probe store is required")
	}
	if len(frames) == 0 {
		return WebSocketJournalProbeResult{}, errors.New("WebSocket journal probe requires at least one frame")
	}
	measured := &measuredWebSocketFrameBatchStore{next: store}
	journal := newWebSocketFrameJournal(
		measured,
		queueSize,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	started := time.Now()
	var last domain.FrameReceipt
	for _, frame := range frames {
		receipt, err := journal.Submit(ctx, frame)
		if err != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = journal.Stop(stopCtx)
			cancel()
			return WebSocketJournalProbeResult{}, err
		}
		last = receipt
	}
	submitDuration := time.Since(started)
	if err := journal.Wait(ctx, last); err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = journal.Stop(stopCtx)
		cancel()
		return WebSocketJournalProbeResult{}, err
	}
	commitDuration := time.Since(started)
	if err := journal.Stop(ctx); err != nil {
		return WebSocketJournalProbeResult{}, err
	}
	metrics := journal.Metrics()
	result := WebSocketJournalProbeResult{
		ContractVersion: WebSocketJournalProbeContractVersion,
		Metrics:         metrics,
		SubmitSeconds:   submitDuration.Seconds(),
		CommitSeconds:   commitDuration.Seconds(),
	}
	if commitDuration > 0 {
		result.FramesPerSecond = float64(metrics.Stored) / commitDuration.Seconds()
	}
	durations := measured.durations()
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	result.BatchP50MS = durationQuantileMS(durations, 0.50)
	result.BatchP95MS = durationQuantileMS(durations, 0.95)
	result.BatchP99MS = durationQuantileMS(durations, 0.99)
	result.BatchMaxMS = durationQuantileMS(durations, 1)
	return result, nil
}

func durationQuantileMS(values []time.Duration, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	if quantile <= 0 {
		return float64(values[0]) / float64(time.Millisecond)
	}
	if quantile >= 1 {
		return float64(values[len(values)-1]) / float64(time.Millisecond)
	}
	index := int(float64(len(values)-1)*quantile + 0.5)
	return float64(values[index]) / float64(time.Millisecond)
}
