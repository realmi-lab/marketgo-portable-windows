package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// writerLeaseKey is deliberately stable across releases. PostgreSQL advisory
// locks are session scoped, so a dead process or connection cannot leave a
// permanent lock behind.
const writerLeaseKey int64 = 0x4d41524b4554474f // "MARKETGO"

var ErrWriterLeaseHeld = errors.New("canonical MarketGo writer lease is already held")

// WriterLease owns the one PostgreSQL session allowed to put a deployment
// instance into ACTIVE writer mode. Application writes still use their normal
// pools; the in-process deployment gate refuses them before this lease exists
// and before it is explicitly released during handoff.
type WriterLease struct {
	mu         sync.Mutex
	conn       *pgxpool.Conn
	owner      string
	released   bool
	releaseErr error
}

func (r *Repository) TryAcquireWriterLease(ctx context.Context, owner string) (*WriterLease, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("PostgreSQL repository is not open")
	}
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire writer lease session: %w", err)
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, writerLeaseKey).Scan(&acquired); err != nil {
		// An unread response does not prove the server never acquired the lock.
		// Never return an ambiguously lock-owning idle session to the pool.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		closeErr := conn.Conn().Close(cleanupCtx)
		cancel()
		conn.Release()
		return nil, errors.Join(fmt.Errorf("acquire writer lease: %w", err), closeErr)
	}
	if !acquired {
		conn.Release()
		return nil, ErrWriterLeaseHeld
	}
	return &WriterLease{conn: conn, owner: owner}, nil
}

func (l *WriterLease) Owner() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.owner
}

// Valid is a cheap fail-closed check on the reserved lease session. Normal
// handoff explicitly changes the deployment phase before Release, so another
// instance never sees the lock as free while this process still accepts writes.
func (l *WriterLease) Valid() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.released && l.conn != nil && !l.conn.Conn().IsClosed()
}

func (l *WriterLease) Release(ctx context.Context) (releaseErr error) {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		// A failed first release is still failed. Handoff callers may invoke
		// cleanup defensively more than once, but repetition is not new proof
		// that PostgreSQL acknowledged the unlock.
		return l.releaseErr
	}
	l.released = true
	defer func() { l.releaseErr = releaseErr }()
	conn := l.conn
	l.conn = nil
	if conn == nil {
		return nil
	}
	// One cleanup budget covers BOTH unlock and emergency connection close.
	// Reserve half for closing a session whose unlock result was ambiguous.
	cleanupCtx, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancelCleanup()
	unlockCtx, cancelUnlock := context.WithTimeout(cleanupCtx, 500*time.Millisecond)
	defer cancelUnlock()
	var unlocked bool
	if err := conn.QueryRow(unlockCtx, `SELECT pg_advisory_unlock($1)`, writerLeaseKey).Scan(&unlocked); err != nil {
		// Never return a possibly lock-owning session to the pool. Closing the
		// PostgreSQL session is fail-safe, but an ambiguous unlock remains an
		// error because local cleanup is not a server acknowledgement.
		closeErr := conn.Conn().Close(cleanupCtx)
		conn.Release()
		return errors.Join(fmt.Errorf("release writer lease: %w", err), closeLeaseSessionError(closeErr))
	}
	if !unlocked {
		closeErr := conn.Conn().Close(cleanupCtx)
		conn.Release()
		return errors.Join(errors.New("release writer lease: session did not own lock"), closeLeaseSessionError(closeErr))
	}
	conn.Release()
	return nil
}

func closeLeaseSessionError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close writer lease session: %w", err)
}
