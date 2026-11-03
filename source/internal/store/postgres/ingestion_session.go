package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"market-share-go/internal/ingestlog"
)

type collectionSessionIdentity struct {
	pid     uint32
	started time.Time
}

var errCollectionLeaseFenced = errors.New("collector lease ownership cannot be acquired")

// The caller holds mu. A dead lease session is closed before reacquiring from
// the same configured pool. Another collector owning the lease is a fatal fence.
func (w *IngestionWriter) ensureSessionLocked(ctx context.Context) error {
	if w.closed {
		return errors.New("ingestion writer is closed")
	}
	if w.conn != nil && !w.conn.Conn().IsClosed() {
		return nil
	}
	if w.repository == nil {
		return errors.New("ingestion writer has no configured repository")
	}
	// One budget covers local cleanup, verified remote release and reacquisition.
	reconnect, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if w.conn != nil {
		// IsClosed precedes asynchronous cancel/Terminate/socket cleanup in pgx.
		// Keep this handle until cleanup finishes; otherwise a new session can
		// conflict with the advisory lease still held by our old backend.
		select {
		case <-w.conn.Conn().PgConn().CleanupDone():
		case <-reconnect.Done():
			return classifyIngestionError(reconnect.Err())
		}
		if err := reconnect.Err(); err != nil {
			return classifyIngestionError(err)
		}
		if err := w.waitFormerCollectionLease(reconnect); err != nil {
			return classifyIngestionError(err)
		}
		w.conn.Release()
		w.conn = nil
	} else if w.identity.pid != 0 {
		// A previous acquisition may have obtained the lock but lost its
		// acknowledgement. Local Close/Release does not prove remote release.
		if err := w.waitFormerCollectionLease(reconnect); err != nil {
			return classifyIngestionError(err)
		}
	}
	conn, identity, err := w.repository.acquireIngestionSession(reconnect)
	if err != nil {
		// Identity is recorded before the lock query. Retain it even when the
		// response is lost, so the next retry can distinguish our orphaned
		// backend from a genuinely competing writer. Earlier failures cannot
		// have acquired the lock and must not overwrite the known identity.
		if identity.pid != 0 && !identity.started.IsZero() {
			w.identity = identity
		}
		return classifyIngestionError(err)
	}
	w.summaryEnd = time.Time{}
	w.conn = conn
	w.identity = identity
	return nil
}

// CleanupDone has no server acknowledgement. Only our exact former session may
// be waited on; a reused PID, invisible identity or any other holder fences us.
func (w *IngestionWriter) waitFormerCollectionLease(ctx context.Context) error {
	if w.identity.pid == 0 || w.identity.started.IsZero() {
		return fmt.Errorf("%w: former backend identity is unknown", errCollectionLeaseFenced)
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		rows, err := w.repository.pool.Query(ctx, `SELECT l.pid, a.backend_start FROM pg_locks l
LEFT JOIN pg_stat_activity a ON a.pid=l.pid
WHERE l.locktype='advisory' AND l.granted AND l.objsubid=1
AND l.database=(SELECT oid FROM pg_database WHERE datname=current_database())
AND l.classid=$1::oid AND l.objid=$2::oid`, uint32(collectionLeaseKey>>32), uint32(collectionLeaseKey&0xffffffff))
		if err != nil {
			return err
		}
		count := 0
		var invalid bool
		for rows.Next() {
			count++
			var pid *uint32
			var started *time.Time
			if err = rows.Scan(&pid, &started); err != nil {
				rows.Close()
				return err
			}
			if pid == nil || started == nil || *pid != w.identity.pid || !started.Equal(w.identity.started) {
				invalid = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if invalid || count > 1 {
			return fmt.Errorf("%w: external or unknown holder", errCollectionLeaseFenced)
		}
		if count == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func classifyIngestionError(err error) error {
	if err == nil {
		return nil
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "40001", "40P01", "55P03", "53300", "57014", "57P01", "57P02", "57P03":
			return ingestlog.Retryable(err)
		}
		if len(postgresError.Code) >= 2 && postgresError.Code[:2] == "08" {
			return ingestlog.Retryable(err)
		}
		return err
	}
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &networkError) || pgconn.SafeToRetry(err) {
		return ingestlog.Retryable(err)
	}
	return err
}

// Call before releasing mu so a failed session cannot be replaced underneath
// error classification. Explicit shutdown never becomes a retry request.
func (w *IngestionWriter) sessionErrorLocked(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, errCollectionLeaseFenced) {
		return err
	}
	if !w.closed && w.conn != nil && w.conn.Conn().IsClosed() {
		return ingestlog.Retryable(err)
	}
	return classifyIngestionError(err)
}
