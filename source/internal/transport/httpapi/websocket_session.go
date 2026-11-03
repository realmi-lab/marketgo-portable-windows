package httpapi

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const websocketTransportHeartbeatInterval = 5 * time.Second

var websocketSessionSequence atomic.Uint64

type websocketSessionKey struct{}

// Transport observations never replace calculation freshness or coverage.
type websocketSession struct {
	mu          sync.Mutex
	started     time.Time
	lastSent    time.Time
	sentFrames  int64
	sentBytes   int64
	lastWriteMS int64
	peerError   error
	writeError  error
}

func (s *websocketSession) recordWrite(start time.Time, size int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastWriteMS = time.Since(start).Milliseconds()
	if err != nil {
		s.writeError = err
		return
	}
	s.lastSent = time.Now()
	s.sentFrames++
	s.sentBytes += int64(size)
}
func (s *websocketSession) recordPeer(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peerError = err
}
func (s *websocketSession) logEnd(logger *slog.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reason := "handler_exit"
	err := s.peerError
	if err != nil {
		reason = "peer_closed_or_transport_lost"
	}
	if s.writeError != nil {
		reason = "write_failed"
		err = s.writeError
	}
	age := int64(-1)
	if !s.lastSent.IsZero() {
		age = time.Since(s.lastSent).Milliseconds()
	}
	logger.Info("websocket session ended", "reason", reason, "close_code", websocket.CloseStatus(err), "error", err,
		"duration_ms", time.Since(s.started).Milliseconds(), "sent_frames", s.sentFrames, "sent_bytes", s.sentBytes,
		"last_send_age_ms", age, "last_write_ms", s.lastWriteMS)
}
func observeWebSocketWrite(ctx context.Context, started time.Time, size int, err error) {
	if s, ok := ctx.Value(websocketSessionKey{}).(*websocketSession); ok {
		s.recordWrite(started, size, err)
	}
}
