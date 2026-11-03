package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"market-share-go/internal/domain"
	"market-share-go/internal/ingestlog"
)

// A distinct lease keeps collector lifetime independent of the compute writer.
const collectionLeaseKey int64 = 0x4d4732434f4c4c

type IngestionWriter struct {
	mu         ingestionAdmission
	conn       *pgxpool.Conn
	repository *Repository
	closed     bool
	identity   collectionSessionIdentity
	summaryEnd time.Time
	compact    bool
}

// OpenIngestionWriter performs no DDL and refuses an unprepared database.
// All appends use the same lease-owning session: losing it fences future writes.
func (r *Repository) OpenIngestionWriter(ctx context.Context) (*IngestionWriter, error) {
	conn, identity, err := r.acquireIngestionSession(ctx)
	if err != nil {
		return nil, classifyIngestionError(err)
	}
	w := &IngestionWriter{conn: conn, repository: r, identity: identity}
	if err := w.loadCompactSetting(ctx); err != nil {
		_ = w.Close(ctx)
		return nil, err
	}
	return w, nil
}

func (r *Repository) acquireIngestionSession(ctx context.Context) (*pgxpool.Conn, collectionSessionIdentity, error) {
	var identity collectionSessionIdentity
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, identity, err
	}
	failed := true
	defer func() {
		if failed {
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			_ = conn.Conn().Close(closeCtx)
			conn.Release()
		}
	}()
	// Record backend identity before acquiring ownership. PID alone can be reused.
	var head int64
	if err = conn.QueryRow(ctx, `SELECT committed_cursor, (SELECT backend_start FROM pg_stat_activity WHERE pid=pg_backend_pid()) FROM ingestion_stream_head WHERE singleton`).Scan(&head, &identity.started); err != nil {
		return nil, identity, fmt.Errorf("ingestion state or backend identity is unavailable: %w", err)
	}
	identity.pid = conn.Conn().PgConn().PID()
	if identity.pid == 0 || identity.started.IsZero() {
		return nil, identity, fmt.Errorf("%w: backend identity is unknown", errCollectionLeaseFenced)
	}
	var held bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, collectionLeaseKey).Scan(&held); err != nil {
		return nil, identity, err
	}
	if !held {
		return nil, identity, fmt.Errorf("%w: collection server already owns this database", errCollectionLeaseFenced)
	}
	failed = false
	return conn, identity, nil
}

func (w *IngestionWriter) Close(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.conn == nil {
		return nil
	}
	conn := w.conn
	w.conn = nil
	// Close instead of returning a lock-owning session to the pool.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	err := conn.Conn().Close(cleanup)
	conn.Release()
	return err
}

func ingestionHash(e ingestlog.Event) ([]byte, error) {
	// Canonical JSON makes a semantically identical retry insensitive to whitespace.
	var value any
	decoder := json.NewDecoder(bytes.NewReader(e.Payload))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(struct {
		Version      int
		ID, Kind     string
		ReceivedAtNS int64
		Payload      any
	}{e.Version, e.ID, e.Kind, e.ReceivedAtNS, value})
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(canonical)
	return hash[:], nil
}

func (w *IngestionWriter) Append(ctx context.Context, events []ingestlog.Event) (resultErr error) {
	if len(events) == 0 {
		return nil
	}
	if len(events) > ingestlog.MaxBatch {
		return errors.New("ingestion batch exceeds limit")
	}
	hashes := make([][]byte, len(events))
	ids := make([]string, len(events))
	needsProjection := make([]bool, len(events))
	for i, e := range events {
		if err := e.Validate(); err != nil {
			return err
		}
		var err error
		hashes[i], err = ingestionHash(e)
		if err != nil {
			return err
		}
		ids[i] = e.ID
		needsProjection[i] = ingestionNeedsProjection(e)
	}
	if err := w.admit(ctx, true); err != nil {
		return err
	}
	defer w.mu.Unlock()
	defer func() { resultErr = w.sessionErrorLocked(resultErr) }()
	if err := w.ensureSessionLocked(ctx); err != nil {
		return err
	}
	tx, err := w.conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	var head int64
	if err = tx.QueryRow(ctx, `SELECT committed_cursor FROM ingestion_stream_head WHERE singleton FOR UPDATE`).Scan(&head); err != nil {
		return err
	}
	// Resolve retry identities once per batch under the existing writer/head
	// lock. Include newly inserted IDs in this map for duplicates in one batch.
	known := make(map[string][]byte, len(events))
	rows, err := tx.Query(ctx, `SELECT event_id,content_sha256 FROM ingestion_events WHERE event_id=ANY($1::text[])`, ids)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var hash []byte
		if err := rows.Scan(&id, &hash); err != nil {
			rows.Close()
			return err
		}
		known[id] = hash
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	insertRows := make([][]any, 0, len(events))
	insertEvents := make([]ingestlog.Event, 0, len(events))
	projected := make([]ingestlog.Event, 0, len(events))
	for i, e := range events {
		if previous, exists := known[e.ID]; exists {
			if !bytes.Equal(previous, hashes[i]) {
				return fmt.Errorf("conflicting ingestion identity %q", e.ID)
			}
			continue
		}
		head++
		e.Cursor = head
		insertRows = append(insertRows, []any{head, e.ID, e.Version, e.Kind, e.ReceivedAtNS, json.RawMessage(e.Payload), hashes[i]})
		insertEvents = append(insertEvents, e)
		known[e.ID] = hashes[i]
		if needsProjection[i] {
			e.Cursor = head
			projected = append(projected, e)
		}
	}
	if len(insertRows) > 0 {
		if w.compact {
			if err := storeCompactIngestion(ctx, tx, insertEvents, insertRows); err != nil {
				return err
			}
		}
		if _, err = tx.CopyFrom(ctx, pgx.Identifier{"ingestion_events"}, []string{"cursor", "event_id", "schema_version", "kind", "received_at_ns", "payload", "content_sha256"}, pgx.CopyFromRows(insertRows)); err != nil {
			return err
		}
	}
	w.invalidateHourlySummary(projected)
	if err := projectIngestionBatch(ctx, tx, projected); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE ingestion_stream_head SET committed_cursor=$1 WHERE singleton`, head); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Empty trade lists cannot produce candle writes. Keep malformed typed inputs
// on the savepoint path so their projection errors remain durable evidence.
func ingestionNeedsProjection(e ingestlog.Event) bool {
	if e.Kind != "frame" {
		return e.Kind == "registry"
	}
	var frame struct {
		Message          struct{ Trades []domain.TradeEvent }
		TradeMatchCounts []uint64
	}
	return json.Unmarshal(e.Payload, &frame) != nil || len(frame.Message.Trades) != 0 || len(frame.TradeMatchCounts) != 0
}

func projectIngestionFrame(ctx context.Context, tx pgx.Tx, payload []byte) error {
	trades, err := ingestionFrameTrades(payload)
	if err != nil {
		return err
	}
	for start := 0; start < len(trades); start += 512 {
		end := min(start+512, len(trades))
		if err := ApplyMarketCandleTradesTx(ctx, tx, trades[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) ReadIngestion(ctx context.Context, after int64, limit int) ([]ingestlog.Event, error) {
	return r.readIngestion(ctx, after, limit, true)
}

// The calculator consumes parsed replay inputs; exchange wire bytes are only
// fetched by an explicit full/raw read. Legacy rows still use their old format.
func (r *Repository) ReadIngestionForCompute(ctx context.Context, after int64, limit int) ([]ingestlog.Event, error) {
	return r.readIngestion(ctx, after, limit, false)
}

func (r *Repository) readIngestion(ctx context.Context, after int64, limit int, includeRaw bool) ([]ingestlog.Event, error) {
	if after < 0 || limit < 1 || limit > ingestlog.MaxReadBatch {
		return nil, errors.New("invalid ingestion read bounds")
	}
	rows, err := r.pool.Query(ctx, `SELECT cursor,event_id,schema_version,kind,received_at_ns,payload,projection_error FROM ingestion_events WHERE cursor>$1 AND cursor<=(SELECT committed_cursor FROM ingestion_stream_head WHERE singleton) ORDER BY cursor LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ingestlog.Event, 0, limit)
	for rows.Next() {
		var e ingestlog.Event
		var payload []byte
		if err = rows.Scan(&e.Cursor, &e.ID, &e.Version, &e.Kind, &e.ReceivedAtNS, &payload, &e.ProjectionError); err != nil {
			return nil, err
		}
		// pgx copies JSONB bytes without an extra JSON unmarshal. Compact
		// resolution and the consumer still validate the complete input.
		e.Payload = payload
		result = append(result, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := resolveCompactIngestion(ctx, r.pool, result, includeRaw); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Repository) ReadIngestionCursor(ctx context.Context) (int64, error) {
	var cursor int64
	err := r.pool.QueryRow(ctx, `SELECT applied_cursor FROM computation_checkpoint WHERE singleton`).Scan(&cursor)
	return cursor, err
}

func (r *Repository) ReadIngestionFXCheckpoint(ctx context.Context) ([]byte, error) {
	var state []byte
	err := r.pool.QueryRow(ctx, `SELECT fx_state FROM computation_checkpoint WHERE singleton`).Scan(&state)
	return state, err
}

// ReadIngestionSnapshots returns source state at or before the exact computation
// checkpoint, never a newer registry/health/official view from the collector.
func (r *Repository) ReadIngestionSnapshots(ctx context.Context, through int64) ([]ingestlog.Event, error) {
	if through < 0 {
		return nil, errors.New("invalid snapshot cursor")
	}
	rows, err := r.pool.Query(ctx, `SELECT e.cursor,e.event_id,e.schema_version,e.kind,e.received_at_ns,e.payload FROM (
	 SELECT DISTINCT ON (kind,COALESCE(snapshot_source,payload->>'Source','')) cursor
	 FROM ingestion_events WHERE cursor<=$1 AND kind IN ('registry','health','official')
	 ORDER BY kind,COALESCE(snapshot_source,payload->>'Source',''),cursor DESC
	) AS snapshots JOIN ingestion_events e ON e.cursor=snapshots.cursor ORDER BY e.cursor`, through)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ingestlog.Event
	for rows.Next() {
		var e ingestlog.Event
		if err = rows.Scan(&e.Cursor, &e.ID, &e.Version, &e.Kind, &e.ReceivedAtNS, &e.Payload); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := resolveCompactIngestion(ctx, r.pool, result, true); err != nil {
		return nil, err
	}
	return result, nil
}

// Read retrieves journal events without exposing an append capability.
func (r *Repository) Read(ctx context.Context, after int64, limit int) ([]ingestlog.Event, error) {
	return r.ReadIngestion(ctx, after, limit)
}
