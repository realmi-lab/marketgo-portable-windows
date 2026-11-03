package app

import (
	"context"
	"sync"
)

// runtimeSubmissionGate serializes assignment, enqueue and publication of an
// admitted sequence. Waiting producers can leave on cancellation while handoff
// retains its nonblocking TryLock contract. The zero value is ready for use.
type runtimeSubmissionGate struct {
	once sync.Once
	held chan struct{}
}

func (g *runtimeSubmissionGate) channel() chan struct{} {
	g.once.Do(func() { g.held = make(chan struct{}, 1) })
	return g.held
}

func (g *runtimeSubmissionGate) lockContext(ctx context.Context, stopped <-chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case g.channel() <- struct{}{}:
		if err := ctx.Err(); err != nil {
			g.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-stopped:
		return ErrStopped
	}
}

func (g *runtimeSubmissionGate) Lock() { g.channel() <- struct{}{} }

func (g *runtimeSubmissionGate) TryLock() bool {
	select {
	case g.channel() <- struct{}{}:
		return true
	default:
		return false
	}
}

func (g *runtimeSubmissionGate) Unlock() {
	select {
	case <-g.channel():
	default:
		panic("unlock of unlocked runtime submission gate")
	}
}
