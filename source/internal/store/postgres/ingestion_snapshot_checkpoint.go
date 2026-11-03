package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"market-share-go/internal/ingestlog"
)

const snapshotCheckpointKey = "ingestion-snapshot-checkpoint-v1"

type snapshotCheckpointEntry struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Cursor int64  `json:"cursor"`
}

type snapshotCheckpoint struct {
	Version int                       `json:"version"`
	Through int64                     `json:"through"`
	Entries []snapshotCheckpointEntry `json:"entries"`
}

// The manifest is a derived index, never an alternate input or cursor. Updating
// it in the same transaction as calculation/FX prevents a crash or ambiguous
// commit from installing a newer source view. Old binaries leave it behind;
// a cursor mismatch then forces the complete legacy query on the next start.
// Only the newly applied batch touches journal rows, not historical payloads.
const advanceIngestionCheckpointSQL = `
WITH checkpoint AS (
 UPDATE computation_checkpoint SET applied_cursor=$1,fx_state=COALESCE($2,fx_state),committed_at=clock_timestamp() WHERE singleton
)
UPDATE aggregation_metadata m SET value=(
 SELECT jsonb_build_object('version',1,'through',$1::bigint,'entries',
   COALESCE(jsonb_agg(jsonb_build_object('kind',kind,'source',source,'cursor',cursor) ORDER BY kind,source),'[]'::jsonb))
 FROM (
  SELECT kind,source,max(cursor) AS cursor FROM (
   SELECT kind,source,cursor FROM jsonb_to_recordset(m.value::jsonb->'entries') AS saved(kind text,source text,cursor bigint)
   UNION ALL
   SELECT kind,COALESCE(snapshot_source,payload->>'Source',''),cursor FROM ingestion_events
   WHERE cursor>$3::bigint AND cursor<=$1 AND kind IN ('registry','health','official')
  ) inputs GROUP BY kind,source
 ) latest
)::text
WHERE m.key='ingestion-snapshot-checkpoint-v1'
 AND m.value::jsonb->>'version'='1' AND m.value::jsonb->>'through'=($3::bigint)::text`

// LoadIngestionSnapshots is the calculator startup path. Existing read-only
// consumers keep ReadIngestionSnapshots. No schema change or source backfill is
// required: a legacy scan seeds the small manifest once, then commits maintain it.
func (r *Repository) LoadIngestionSnapshots(ctx context.Context, through int64) ([]ingestlog.Event, error) {
	if through < 0 {
		return nil, errors.New("invalid snapshot cursor")
	}
	if events, ok, err := r.readSnapshotCheckpoint(ctx, through); err != nil || ok {
		return events, err
	}
	events, err := r.ReadIngestionSnapshots(ctx, through)
	if err != nil {
		return nil, err
	}
	cursors := make([]int64, 0, len(events))
	for _, e := range events {
		cursors = append(cursors, e.Cursor)
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var applied int64
	if err = tx.QueryRow(ctx, `SELECT applied_cursor FROM computation_checkpoint WHERE singleton FOR UPDATE`).Scan(&applied); err != nil {
		return nil, err
	}
	if applied != through {
		return nil, fmt.Errorf("snapshot checkpoint moved: stored=%d expected=%d", applied, through)
	}
	_, err = tx.Exec(ctx, `INSERT INTO aggregation_metadata(key,value)
 SELECT $1,jsonb_build_object('version',1,'through',$2::bigint,'entries',
 COALESCE(jsonb_agg(jsonb_build_object('kind',kind,'source',COALESCE(snapshot_source,payload->>'Source',''),'cursor',cursor) ORDER BY cursor),'[]'::jsonb))::text
 FROM ingestion_events WHERE cursor=ANY($3::bigint[])
 ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value`, snapshotCheckpointKey, through, cursors)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *Repository) readSnapshotCheckpoint(ctx context.Context, through int64) ([]ingestlog.Event, bool, error) {
	var raw string
	err := r.pool.QueryRow(ctx, `SELECT value FROM aggregation_metadata WHERE key=$1`, snapshotCheckpointKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var c snapshotCheckpoint
	if json.Unmarshal([]byte(raw), &c) != nil || c.Version != 1 || c.Through != through || c.Entries == nil {
		return nil, false, nil
	}
	byCursor := make(map[int64]snapshotCheckpointEntry, len(c.Entries))
	keys := make(map[[2]string]bool, len(c.Entries))
	cursors := make([]int64, 0, len(c.Entries))
	for _, e := range c.Entries {
		key := [2]string{e.Kind, e.Source}
		if e.Cursor <= 0 || e.Cursor > through || keys[key] || byCursor[e.Cursor].Cursor != 0 || (e.Kind != "registry" && e.Kind != "health" && e.Kind != "official") {
			return nil, false, nil
		}
		keys[key], byCursor[e.Cursor] = true, e
		cursors = append(cursors, e.Cursor)
	}
	rows, err := r.pool.Query(ctx, `SELECT cursor,event_id,schema_version,kind,received_at_ns,payload,COALESCE(snapshot_source,payload->>'Source','') FROM ingestion_events WHERE cursor=ANY($1::bigint[]) ORDER BY cursor`, cursors)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var events []ingestlog.Event
	for rows.Next() {
		var e ingestlog.Event
		var source string
		if err := rows.Scan(&e.Cursor, &e.ID, &e.Version, &e.Kind, &e.ReceivedAtNS, &e.Payload, &source); err != nil {
			return nil, false, err
		}
		if saved := byCursor[e.Cursor]; saved.Kind != e.Kind || saved.Source != source {
			return nil, false, nil
		}
		events = append(events, e)
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	rows.Close()
	if len(events) != len(c.Entries) {
		return nil, false, nil
	}
	if err = resolveCompactIngestion(ctx, r.pool, events, true); err != nil {
		return nil, false, err
	}
	return events, true, nil
}
