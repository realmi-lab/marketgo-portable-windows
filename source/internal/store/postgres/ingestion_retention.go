package postgres

import (
	"context"
	"errors"
	"time"
)

const IngestionArchiveMaxBatch = 1000

const IngestionArchiveDrainBudget = 200 * time.Millisecond
const ingestionArchiveDrainMaxBatches = 3

type IngestionArchiveDrainResult struct {
	IngestionArchiveResult
	Batches         int
	BudgetExhausted bool
	Elapsed         time.Duration
}

// DrainArchives runs bounded transactions at one frozen retention cutoff. Each
// batch releases the writer mutex, giving foreground appends a chance to run.
// Only the first transaction may advance the durable cutoff.
func (w *IngestionWriter) DrainArchives(parent context.Context, now time.Time) (result IngestionArchiveDrainResult, resultErr error) {
	started := time.Now()
	defer func() { result.Elapsed = time.Since(started) }()
	ctx, cancel := context.WithTimeout(parent, IngestionArchiveDrainBudget)
	defer cancel()
	var fixed int64
	for result.Batches < ingestionArchiveDrainMaxBatches {
		if err := parent.Err(); err != nil {
			return result, err
		}
		// Do not start another SQL transaction with only cleanup time left.
		if deadline, _ := ctx.Deadline(); time.Until(deadline) < 25*time.Millisecond {
			result.BudgetExhausted = true
			return result, nil
		}
		batch, err := w.archiveBatch(ctx, now, fixed)
		if err != nil {
			return result, err
		}
		result.Batches++
		result.ArchivedFrames += batch.ArchivedFrames
		result.ArchivedEnvelopeStorageBytes += batch.ArchivedEnvelopeStorageBytes
		result.CutoffNS = batch.CutoffNS
		fixed = batch.CutoffNS
		if batch.ArchivedFrames < IngestionArchiveMaxBatch {
			return result, nil
		}
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			result.BudgetExhausted = true
			return result, parent.Err()
		case <-timer.C:
		}
	}
	result.BudgetExhausted = true
	return result, nil
}

type IngestionArchiveResult struct {
	ArchivedFrames               int64
	ArchivedEnvelopeStorageBytes int64
	CutoffNS                     int64
}

// Archive replaces only acknowledged, expired frame envelopes with explicit
// tombstones. Raw bytes AND parsed replay inputs are removed together. Identity
// hashes survive for retries; non-frame bootstrap/coverage evidence survives.
func (w *IngestionWriter) Archive(parent context.Context, now time.Time) (archiveResult IngestionArchiveResult, resultErr error) {
	return w.archiveBatch(parent, now, 0)
}

func (w *IngestionWriter) archiveBatch(parent context.Context, now time.Time, fixedCutoff int64) (archiveResult IngestionArchiveResult, resultErr error) {
	if now.IsZero() || now.Add(-WebSocketRawRetention).UnixNano() <= 0 {
		return IngestionArchiveResult{}, errors.New("invalid ingestion archival time")
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	for !w.mu.TryLock() {
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return IngestionArchiveResult{}, ctx.Err()
		case <-timer.C:
		}
	}
	defer w.mu.Unlock()
	defer func() { resultErr = w.sessionErrorLocked(resultErr) }()
	if err := w.ensureSessionLocked(ctx); err != nil {
		return IngestionArchiveResult{}, err
	}
	tx, err := w.conn.Begin(ctx)
	if err != nil {
		return IngestionArchiveResult{}, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	var cutoff int64
	if err = tx.QueryRow(ctx, `SELECT cutoff_ns FROM ingestion_retention_state WHERE singleton FOR UPDATE`).Scan(&cutoff); err != nil {
		return IngestionArchiveResult{}, err
	}
	var databaseNow time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		return IngestionArchiveResult{}, err
	}
	if skew := now.Sub(databaseNow); skew > webSocketArchiveMaxClockSkew || skew < -webSocketArchiveMaxClockSkew {
		return IngestionArchiveResult{}, errors.New("ingestion archival clock skew; retaining frames")
	}
	next, err := nextWebSocketRetentionCutoff(cutoff, databaseNow.Add(-WebSocketRawRetention).UnixNano())
	if err != nil {
		return IngestionArchiveResult{}, err
	}
	if fixedCutoff > 0 {
		if fixedCutoff > cutoff {
			return IngestionArchiveResult{}, errors.New("archive drain cutoff moved backwards")
		}
		next = cutoff
		cutoff = fixedCutoff
	}
	result := IngestionArchiveResult{CutoffNS: cutoff}
	if err = tx.QueryRow(ctx, ingestionArchiveSQL, cutoff, IngestionArchiveMaxBatch).Scan(&result.ArchivedFrames, &result.ArchivedEnvelopeStorageBytes); err != nil {
		return IngestionArchiveResult{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE ingestion_retention_state SET cutoff_ns=$1 WHERE singleton`, next)
	if err != nil {
		return IngestionArchiveResult{}, err
	}
	if tag.RowsAffected() != 1 {
		return IngestionArchiveResult{}, errors.New("ingestion retention cutoff disappeared")
	}
	if err = tx.Commit(ctx); err != nil {
		return IngestionArchiveResult{}, err
	}
	return result, nil
}

const ingestionArchiveSQL = `
WITH candidates AS MATERIALIZED (
 SELECT cursor,received_at_ns,pg_column_size(payload)::bigint+COALESCE((payload->>'CompactBytes')::bigint,0) AS envelope_bytes,
  COALESCE(payload#>>'{Frame,Exchange}','UNKNOWN') AS exchange_name,
  COALESCE(payload#>>'{Frame,Market}','') AS market,
  COALESCE(NULLIF(payload#>>'{Frame,FrameClass}',''),'UNKNOWN') AS frame_class,
  COALESCE(payload#>>'{Frame,MessageType}','UNKNOWN') AS message_type
 FROM ingestion_events
 WHERE kind='frame' AND archived_at IS NULL AND projection_error='' AND received_at_ns<$1
  AND cursor<=(SELECT applied_cursor FROM computation_checkpoint WHERE singleton)
 ORDER BY received_at_ns,cursor LIMIT $2 FOR UPDATE
), statistics AS (
 INSERT INTO ingestion_frame_statistics AS s
  (start_sec,interval_sec,exchange_name,market,frame_class,message_type,frame_count,
   envelope_storage_bytes,first_received_at_ns,last_received_at_ns)
 SELECT (received_at_ns/1000000000/interval_sec)*interval_sec,interval_sec,
  exchange_name,market,frame_class,message_type,count(*),sum(envelope_bytes),min(received_at_ns),max(received_at_ns)
 FROM candidates CROSS JOIN unnest(ARRAY[60,3600,86400]::bigint[]) AS duration(interval_sec)
 GROUP BY 1,2,3,4,5,6
 ON CONFLICT(start_sec,interval_sec,exchange_name,market,frame_class,message_type)
 DO UPDATE SET frame_count=s.frame_count+EXCLUDED.frame_count,
  envelope_storage_bytes=s.envelope_storage_bytes+EXCLUDED.envelope_storage_bytes,
  first_received_at_ns=LEAST(s.first_received_at_ns,EXCLUDED.first_received_at_ns),
  last_received_at_ns=GREATEST(s.last_received_at_ns,EXCLUDED.last_received_at_ns),updated_at=clock_timestamp()
 RETURNING 1
), archived AS (
 UPDATE ingestion_events SET payload='{"Archived":true}'::jsonb,archived_at=clock_timestamp()
 WHERE cursor IN (SELECT cursor FROM candidates)
 RETURNING cursor
)
SELECT (SELECT count(*) FROM archived),COALESCE((SELECT sum(envelope_bytes) FROM candidates),0)::bigint
`
