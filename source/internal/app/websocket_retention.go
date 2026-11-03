package app

import (
	"context"
	"errors"
	"time"

	"market-share-go/internal/store/postgres"
)

const (
	websocketRetentionInterval                = time.Second
	websocketRetentionMaximumForwardClockStep = 5 * time.Second
)

type websocketFrameArchiver interface {
	ArchiveExpiredWebSocketFrames(context.Context, time.Time, int) (postgres.WebSocketArchiveResult, error)
}

type gatedWebSocketArchiver struct {
	next websocketFrameArchiver
	gate *deploymentWriterGate
}

func (a gatedWebSocketArchiver) ArchiveExpiredWebSocketFrames(ctx context.Context, now time.Time, limit int) (postgres.WebSocketArchiveResult, error) {
	var result postgres.WebSocketArchiveResult
	err := a.gate.dispatchActiveOnly(ctx, bufferedDeploymentWrite{name: "websocket_archive", run: func(writeCtx context.Context) error {
		var err error
		result, err = a.next.ArchiveExpiredWebSocketFrames(writeCtx, now, limit)
		return err
	}})
	return result, err
}

type WebSocketRetentionMetrics struct {
	Enabled               bool      `json:"enabled"`
	Status                string    `json:"status"`
	RawRetentionSeconds   int64     `json:"raw_retention_seconds"`
	BatchLimit            int       `json:"batch_limit"`
	ArchivedFrames        uint64    `json:"archived_frames"`
	DeletedFrames         uint64    `json:"deleted_frames"`
	Failures              uint64    `json:"failures"`
	LastSuccess           time.Time `json:"last_success"`
	LastError             string    `json:"last_error"`
	LastErrorAt           time.Time `json:"last_error_at"`
	OldestRawReceivedAtNS int64     `json:"oldest_raw_received_at_ns"`
	RetentionLagSeconds   float64   `json:"retention_lag_seconds"`
	clockBaseline         time.Time
}

func (s *Service) monitorWebSocketRetention(ctx context.Context) {
	if s.websocketArchiver == nil {
		return
	}
	ticker := time.NewTicker(websocketRetentionInterval)
	defer ticker.Stop()
	for {
		s.archiveExpiredWebSocketFrames(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) archiveExpiredWebSocketFrames(parent context.Context, now time.Time) {
	if s.websocketArchiver == nil || now.IsZero() || parent.Err() != nil {
		return
	}
	metrics := s.webSocketRetentionMetrics(now)
	if metrics.clockBaseline.IsZero() {
		metrics.clockBaseline = now
	} else {
		wallElapsed := time.Duration(now.UnixNano() - metrics.clockBaseline.UnixNano())
		monotonicElapsed := now.Sub(metrics.clockBaseline)
		if websocketRetentionForwardClockJump(wallElapsed, monotonicElapsed) {
			metrics.Status = "ERROR"
			metrics.LastError = "WebSocket archival paused after an unsafe forward wall-clock step"
			metrics.LastErrorAt = now
			metrics.Failures++
			s.websocketRetentionState.Store(&metrics)
			if s.logger != nil {
				s.logger.Warn("WebSocket archival paused after an unsafe forward wall-clock step")
			}
			return
		}
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	result, err := s.websocketArchiver.ArchiveExpiredWebSocketFrames(ctx, now, postgres.WebSocketArchiveMaxBatch)
	cancel()
	if errors.Is(err, ErrDeploymentNotActive) {
		metrics.Status = "STANDBY"
	} else if err != nil {
		if parent.Err() != nil {
			return
		}
		metrics.Status, metrics.LastError, metrics.LastErrorAt = "ERROR", err.Error(), now
		metrics.Failures++
		if s.logger != nil {
			s.logger.Warn("WebSocket archival not confirmed", "error", err)
		}
	} else if result.Busy {
		metrics.Status = "BUSY"
	} else {
		metrics.Status, metrics.LastSuccess, metrics.LastError = "OK", now, ""
		metrics.OldestRawReceivedAtNS = result.OldestRawReceivedAtNS
		metrics.ArchivedFrames += uint64(result.ArchivedFrames)
		metrics.DeletedFrames += uint64(result.DeletedFrames)
	}
	s.websocketRetentionState.Store(&metrics)
}

func websocketRetentionForwardClockJump(wallElapsed, monotonicElapsed time.Duration) bool {
	return wallElapsed > monotonicElapsed && wallElapsed-monotonicElapsed > websocketRetentionMaximumForwardClockStep
}

func (s *Service) webSocketRetentionMetrics(now time.Time) WebSocketRetentionMetrics {
	metrics := WebSocketRetentionMetrics{
		Enabled: s.websocketArchiver != nil, Status: "DISABLED",
		RawRetentionSeconds: int64(postgres.WebSocketRawRetention / time.Second), BatchLimit: postgres.WebSocketArchiveMaxBatch,
	}
	if s.websocketArchiver != nil {
		metrics.Status = "STARTING"
	}
	if stored := s.websocketRetentionState.Load(); stored != nil {
		metrics = *stored
	}
	metrics.RetentionLagSeconds = 0
	if metrics.OldestRawReceivedAtNS > 0 {
		metrics.RetentionLagSeconds = max(0, now.Sub(time.Unix(0, metrics.OldestRawReceivedAtNS).Add(postgres.WebSocketRawRetention)).Seconds())
		if metrics.RetentionLagSeconds > 0 && metrics.Status == "OK" {
			metrics.Status = "CATCHING_UP"
		}
	}
	return metrics
}
