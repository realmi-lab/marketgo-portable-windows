package postgres

import (
	"context"
	"errors"
	"sync"
	"time"

	"market-share-go/internal/ingestlog"
)

var errIngestionAdmissionFull = errors.New("collector writer admission capacity exhausted")

// The zero-value gate serializes the existing lease-owning session. No SQL
// executes under its short state lock. Foreground waiters precede maintenance;
// cancellation removes admission without ever touching the session.
type ingestionAdmission struct {
	state      sync.Mutex
	held       bool
	foreground int
	waiting    int
	changed    chan struct{}
}

func (g *ingestionAdmission) signal() {
	if g.changed != nil {
		close(g.changed)
	}
	g.changed = make(chan struct{})
}
func (g *ingestionAdmission) LockContext(ctx context.Context, foreground bool) error {
	g.state.Lock()
	defer g.state.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if g.waiting >= 64 || !foreground && g.waiting >= 8 {
		return ingestlog.Retryable(errIngestionAdmissionFull)
	}
	g.waiting++
	if foreground {
		g.foreground++
	}
	defer func() {
		g.waiting--
		if foreground {
			g.foreground--
		}
		g.signal()
	}()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !g.held && (foreground || g.foreground == 0) {
			g.held = true
			return nil
		}
		if g.changed == nil {
			g.changed = make(chan struct{})
		}
		changed := g.changed
		g.state.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		g.state.Lock()
	}
}
func (g *ingestionAdmission) Lock() {
	// Close drains the current writer even if its caller has been canceled.
	for g.LockContext(context.Background(), true) != nil {
		time.Sleep(time.Millisecond)
	}
}
func (g *ingestionAdmission) TryLock() bool {
	g.state.Lock()
	defer g.state.Unlock()
	if g.held || g.foreground != 0 {
		return false
	}
	g.held = true
	return true
}
func (g *ingestionAdmission) Unlock() {
	g.state.Lock()
	defer g.state.Unlock()
	g.held = false
	g.signal()
}

func (w *IngestionWriter) admit(ctx context.Context, foreground bool) error {
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return classifyIngestionError(w.mu.LockContext(wait, foreground))
}
