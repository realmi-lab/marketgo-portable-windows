package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Maintenance scans the primary key, not the increasingly sparse retention
// index. Its durable position wraps so late timestamps and repaired/locked rows
// are revisited. It neither acquires nor interrupts the collector's lease.
const ingestionMaintenanceLock int64 = 0x4d5347494d41494e
const ingestionMaintenanceScan = 2000
const ingestionMaintenanceFrameBatch = 200

// A health snapshot can contain the entire market activity map. Keep its
// decompression and TOAST deletion within a small transaction; cursor progress
// below resumes at the last archived row instead of skipping the remainder.
const ingestionMaintenanceLatestBatch = 4
const IngestionRecoveryRetention = 48 * time.Hour

type IngestionMaintenanceResult struct {
	After         int64 `json:"after"`
	Archived      int64 `json:"archived"`
	Bytes         int64 `json:"bytes"`
	TotalArchived int64 `json:"total_archived"`
	TotalBytes    int64 `json:"total_bytes"`
	CutoffNS      int64 `json:"cutoff_ns"`
	Busy          bool  `json:"busy"`
}

// MaintainIngestion removes processed envelopes older than 48 hours by BOTH
// receipt and commit time. Health/official keep a complete per-source
// anchor preceding the recovery horizon. Registry and loss history is retained.
// now must come from the caller's monotonic-clock projection; the database
// independently checks its wall clock. No DDL or cursor deletion occurs here.
func (r *Repository) MaintainIngestion(parent context.Context, now time.Time, snapshots bool) (result IngestionMaintenanceResult, err error) {
	return r.maintainIngestion(parent, now, snapshots, false, false)
}

// MaintainLatestSnapshots removes only superseded, successfully processed
// health/official payloads. The recent sweep prevents a large historical backlog
// from delaying cleanup of new snapshots; the full sweep revisits older rows.
func (r *Repository) MaintainLatestSnapshots(parent context.Context, now time.Time, recent bool) (IngestionMaintenanceResult, error) {
	return r.maintainIngestion(parent, now, true, true, recent)
}

func (r *Repository) maintainIngestion(parent context.Context, now time.Time, snapshots, latest, recent bool) (result IngestionMaintenanceResult, err error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		_ = tx.Rollback(cleanup)
	}()
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='100ms'; SET LOCAL statement_timeout='1500ms'`); err != nil {
		return result, err
	}
	var locked bool
	if err = tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, ingestionMaintenanceLock).Scan(&locked); err != nil {
		return result, err
	}
	if !locked {
		result.Busy = true
		return result, nil
	}
	var databaseNow time.Time
	var applied int64
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp(),applied_cursor FROM computation_checkpoint WHERE singleton`).Scan(&databaseNow, &applied); err != nil {
		return result, err
	}
	if now.IsZero() || now.Sub(databaseNow) > webSocketArchiveMaxClockSkew || databaseNow.Sub(now) > webSocketArchiveMaxClockSkew {
		return result, errors.New("ingestion maintenance clock skew; retaining inputs")
	}
	cutoff := now.Add(-IngestionRecoveryRetention)
	if databaseNow.Before(now) {
		cutoff = databaseNow.Add(-IngestionRecoveryRetention)
	}
	if cutoff.UnixNano() <= 0 {
		return result, errors.New("invalid ingestion maintenance cutoff")
	}
	key := "ingestion-maintenance-frames-v1"
	limit := ingestionMaintenanceFrameBatch
	query := ingestionMaintenanceFramesSQL
	if snapshots {
		key = "ingestion-maintenance-snapshots-v1"
		limit = 32
		query = ingestionMaintenanceSnapshotsSQL
	}
	if latest {
		key = "ingestion-maintenance-latest-snapshots-v1"
		limit = ingestionMaintenanceLatestBatch
		query = ingestionMaintenanceLatestSnapshotsSQL
		if recent {
			key += "-recent"
		}
	}
	var saved string
	if err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT value FROM aggregation_metadata WHERE key=$1),'{}')`, key).Scan(&saved); err != nil {
		return result, err
	}
	if err = json.Unmarshal([]byte(saved), &result); err != nil {
		return result, err
	}
	if result.After < 0 || result.CutoffNS > cutoff.UnixNano() {
		return result, errors.New("ingestion maintenance clock or progress moved backwards")
	}
	if result.After >= applied {
		result.After = 0
	}
	if recent {
		result.After = max(result.After, applied-100000)
	}
	// Seek across deleted cursor ranges instead of spending one maintenance tick
	// per 10,000 absent IDs. Keep the scan window and all eligibility checks.
	var next int64
	if err = tx.QueryRow(ctx, `SELECT COALESCE((SELECT cursor FROM ingestion_events
 WHERE cursor>$1 AND cursor<=$2 ORDER BY cursor LIMIT 1),0)`, result.After, applied).Scan(&next); err != nil {
		return result, err
	}
	if next > 0 {
		result.After = next - 1
	} else {
		result.After = applied
	}
	end := min(applied, result.After+ingestionMaintenanceScan)
	var last int64
	args := []any{result.After, end, cutoff.UnixNano(), cutoff, limit, applied}
	if err = tx.QueryRow(ctx, query, args...).Scan(&result.Archived, &result.Bytes, &last); err != nil {
		return IngestionMaintenanceResult{}, err
	}
	result.After = end
	if result.Archived >= int64(limit) {
		result.After = last
	}
	if recent && result.After == applied {
		// Wrap even when the head advances before the next tick. Otherwise an
		// anchor passed just before its successor committed is never revisited.
		result.After = 0
	}
	result.CutoffNS = cutoff.UnixNano()
	result.TotalArchived += result.Archived
	result.TotalBytes += result.Bytes
	encoded, err := json.Marshal(result)
	if err != nil {
		return IngestionMaintenanceResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO aggregation_metadata(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, key, string(encoded)); err != nil {
		return IngestionMaintenanceResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return IngestionMaintenanceResult{}, err
	}
	return result, nil
}

const ingestionMaintenanceFramesSQL = `
WITH candidates AS MATERIALIZED (
 SELECT cursor,received_at_ns,pg_column_size(payload)::bigint+COALESCE((payload->>'CompactBytes')::bigint,0) AS envelope_bytes,
  COALESCE(payload#>>'{Frame,Exchange}','UNKNOWN') AS exchange_name,
  COALESCE(payload#>>'{Frame,Market}','') AS market,
  COALESCE(NULLIF(payload#>>'{Frame,FrameClass}',''),'UNKNOWN') AS frame_class,
  COALESCE(payload#>>'{Frame,MessageType}','UNKNOWN') AS message_type
 FROM ingestion_events
 WHERE cursor>$1 AND cursor<=$2 AND cursor<=$6
  AND kind='frame' AND archived_at IS NULL AND projection_error=''
  AND received_at_ns<$3 AND committed_at<$4
 ORDER BY cursor LIMIT $5 FOR UPDATE SKIP LOCKED
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
 WHERE cursor IN (SELECT cursor FROM candidates) RETURNING cursor
)
SELECT (SELECT count(*) FROM archived),COALESCE((SELECT sum(envelope_bytes) FROM candidates),0)::bigint,
 COALESCE((SELECT max(cursor) FROM candidates),0)::bigint
`

const ingestionMaintenanceSnapshotsSQL = `
WITH candidates AS MATERIALIZED (
 SELECT e.cursor,pg_column_size(e.payload)::bigint+COALESCE((e.payload->>'CompactBytes')::bigint,0) AS envelope_bytes,
  COALESCE(e.snapshot_source,e.payload->>'Source','') AS source
 FROM ingestion_events e
 WHERE e.cursor>$1 AND e.cursor<=$2 AND e.cursor<=$6
  AND e.kind IN ('health','official') AND e.archived_at IS NULL AND e.projection_error=''
  AND e.received_at_ns<$3 AND e.committed_at<$4
  AND COALESCE((
   SELECT s.received_at_ns<$3 AND s.committed_at<$4 FROM ingestion_events s
   WHERE s.kind=e.kind AND s.cursor>e.cursor AND s.cursor<=$6
    AND COALESCE(s.snapshot_source,s.payload->>'Source','')=COALESCE(e.snapshot_source,e.payload->>'Source','')
    AND s.archived_at IS NULL AND s.projection_error=''
   ORDER BY s.cursor ASC LIMIT 1
  ),false)
 ORDER BY e.cursor LIMIT $5 FOR UPDATE OF e SKIP LOCKED
), archived AS (
 UPDATE ingestion_events e SET payload=jsonb_build_object('Archived',true,'Source',c.source),archived_at=clock_timestamp()
 FROM candidates c WHERE e.cursor=c.cursor RETURNING e.cursor
)
SELECT (SELECT count(*) FROM archived),COALESCE((SELECT sum(envelope_bytes) FROM candidates),0)::bigint,
 COALESCE((SELECT max(cursor) FROM candidates),0)::bigint
`

// The successor search is bounded. Unknown/rare sources without a proven
// successor in this window are retained, never guessed to be obsolete. The
// lookahead and processed-head ranges are each one scan window. Head anchors
// let a previously locked row retire even after its nearby successors have
// been archived. No query scans the former 102,000-position successor range.
// Receipt timestamps are intentionally irrelevant: cursor order is replay order.
const ingestionMaintenanceLatestSnapshotsSQL = `
WITH eligible AS MATERIALIZED (
 SELECT cursor FROM ingestion_events
 WHERE cursor>$1 AND cursor<=$2 AND cursor<=$6
  AND kind IN ('health','official') AND archived_at IS NULL AND projection_error=''
 LIMIT 1
), successor_rows AS MATERIALIZED (
 SELECT s.kind,COALESCE(s.snapshot_source,s.payload->>'Source','') AS source,s.cursor
 FROM ingestion_events s
 WHERE s.kind IN ('health','official') AND s.cursor>$1 AND s.cursor<=LEAST($6,$2+2000)
  AND s.archived_at IS NULL AND s.projection_error=''
  AND EXISTS (SELECT 1 FROM eligible)
 UNION ALL
 SELECT s.kind,COALESCE(s.snapshot_source,s.payload->>'Source','') AS source,s.cursor
 FROM ingestion_events s
 WHERE s.kind IN ('health','official') AND s.cursor>GREATEST($2+2000,$6-2000) AND s.cursor<=$6
  AND s.archived_at IS NULL AND s.projection_error=''
  AND EXISTS (SELECT 1 FROM eligible)
), successors AS MATERIALIZED (
 SELECT kind,source,max(cursor) AS cursor FROM successor_rows GROUP BY kind,source
), candidates AS MATERIALIZED (
 SELECT e.cursor,pg_column_size(e.payload)::bigint+COALESCE((e.payload->>'CompactBytes')::bigint,0) AS envelope_bytes,
  COALESCE(e.snapshot_source,e.payload->>'Source','') AS source
 FROM ingestion_events e
 JOIN successors s ON s.kind=e.kind
  AND s.source=COALESCE(e.snapshot_source,e.payload->>'Source','') AND s.cursor>e.cursor
 WHERE e.cursor>$1 AND e.cursor<=$2 AND e.cursor<=$6
  AND e.kind IN ('health','official') AND e.archived_at IS NULL AND e.projection_error=''
  AND $3::bigint>0 AND $4::timestamptz IS NOT NULL
 ORDER BY e.cursor LIMIT $5 FOR UPDATE OF e SKIP LOCKED
), archived AS (
 UPDATE ingestion_events e SET payload=jsonb_build_object('Archived',true,'Source',c.source),archived_at=clock_timestamp()
 FROM candidates c WHERE e.cursor=c.cursor RETURNING e.cursor
)
SELECT (SELECT count(*) FROM archived),COALESCE((SELECT sum(envelope_bytes) FROM candidates),0)::bigint,
 COALESCE((SELECT max(cursor) FROM candidates),0)::bigint
`
