package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	WebSocketRawRetention              = 4 * 24 * time.Hour
	WebSocketArchiveMaxBatch           = 10_000
	webSocketArchiveMaxClockSkew       = 5 * time.Second
	webSocketArchiveCutoffStep         = time.Second
	websocketArchiveLockID       int64 = 0x4d53475753524554
)

type WebSocketArchiveResult struct {
	CutoffNS              int64 `json:"cutoff_ns"`
	ArchivedFrames        int64 `json:"archived_frames"`
	DeletedFrames         int64 `json:"deleted_frames"`
	OldestRawReceivedAtNS int64 `json:"oldest_raw_received_at_ns"`
	Busy                  bool  `json:"busy"`
}

// A SHADOW writer intentionally acknowledges journal writes without running SQL.
// Check the actual schema before it can be declared ready, without repairing
// schema or reading payloads. Missing/locked tables and incomplete application
// role privileges fail startup instead of being discovered during promotion
// after the old writer stops.
func checkWebSocketRetentionSchema(parent context.Context, pool *pgxpool.Pool) error {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	_, err := pool.Exec(ctx, `SELECT
		f.exchange_name, f.connection_id, f.frame_sequence, f.message_type,
		f.received_at_ns, f.payload_encoding, f.payload_uncompressed_bytes,
		f.payload, f.frame_class, f.market,
		c.connection_id, c.sequence_block, c.archived_sequences @> f.frame_sequence,
		c.archived_frame_count, c.updated_at,
		s.resolution_sec, s.bucket_start, s.exchange_name, s.market, s.frame_class,
		s.message_type, s.frame_count, s.uncompressed_bytes, s.stored_bytes,
		s.gzip_frame_count, s.first_received_at_ns, s.last_received_at_ns, s.aggregated_at,
		r.singleton, r.cutoff_ns, r.updated_at
		FROM websocket_frames f, websocket_frame_archive_connections c,
			websocket_frame_statistics s, websocket_retention_state r
		LIMIT 0`)
	if err != nil {
		return fmt.Errorf("WebSocket retention schema unavailable (requires migration 0026): %w", err)
	}
	var privilegesReady bool
	err = pool.QueryRow(ctx, `SELECT COALESCE(bool_and(
		has_table_privilege(current_user, to_regclass(required.table_name), required.privilege_name)
	), false)
	FROM (VALUES
		('websocket_frames', 'SELECT'),
		('websocket_frames', 'INSERT'),
		('websocket_frames', 'UPDATE'),
		('websocket_frames', 'DELETE'),
		('websocket_frame_archive_connections', 'SELECT'),
		('websocket_frame_archive_connections', 'INSERT'),
		('websocket_frame_archive_connections', 'UPDATE'),
		('websocket_frame_statistics', 'SELECT'),
		('websocket_frame_statistics', 'INSERT'),
		('websocket_frame_statistics', 'UPDATE'),
		('websocket_retention_state', 'SELECT'),
		('websocket_retention_state', 'UPDATE')
	) AS required(table_name, privilege_name)`).Scan(&privilegesReady)
	if err != nil {
		return fmt.Errorf("check WebSocket retention application privileges: %w", err)
	}
	if !privilegesReady {
		return errors.New("WebSocket retention application role lacks required SELECT/INSERT/UPDATE/DELETE privileges")
	}
	var indexAndStateReady bool
	err = pool.QueryRow(ctx, `SELECT
		EXISTS (
			SELECT 1
			FROM pg_class table_relation
			JOIN pg_attribute received_column
			  ON received_column.attrelid = table_relation.oid
			 AND received_column.attname = 'received_at_ns'
			 AND NOT received_column.attisdropped
			JOIN pg_index index_metadata ON index_metadata.indrelid = table_relation.oid
			JOIN pg_class index_relation
			  ON index_relation.oid = index_metadata.indexrelid
			 AND index_relation.relnamespace = table_relation.relnamespace
			JOIN pg_am access_method ON access_method.oid = index_relation.relam
			JOIN pg_opclass operator_class ON operator_class.oid = index_metadata.indclass[0]
			WHERE table_relation.oid = 'websocket_frames'::regclass
			  AND index_relation.relname = 'websocket_frames_retention_idx'
			  AND index_relation.relkind = 'i'
			  AND access_method.amname = 'btree'
			  AND index_metadata.indisvalid
			  AND index_metadata.indisready
			  AND index_metadata.indislive
			  AND NOT index_metadata.indisunique
			  AND NOT index_metadata.indisprimary
			  AND NOT index_metadata.indisexclusion
			  AND index_metadata.indpred IS NULL
			  AND index_metadata.indexprs IS NULL
			  AND index_metadata.indnkeyatts = 1
			  AND index_metadata.indnatts = 1
			  AND index_metadata.indkey[0] = received_column.attnum
			  AND index_metadata.indcollation[0] = 0
			  AND index_metadata.indoption[0] = 0
			  AND operator_class.opcname = 'int8_ops'
			  AND operator_class.opcnamespace = 'pg_catalog'::regnamespace
		)
		AND (SELECT count(*) = 1 FROM websocket_retention_state WHERE singleton)`).Scan(&indexAndStateReady)
	if err != nil {
		return fmt.Errorf("check WebSocket retention index and cutoff state: %w", err)
	}
	if !indexAndStateReady {
		return errors.New("WebSocket retention requires one durable cutoff row and the exact ready migration 0026 B-tree")
	}
	return nil
}

// ArchiveExpiredWebSocketFrames persists three statistical resolutions before
// deleting at most limit payloads, in the same transaction. Financial buckets,
// minute OHLC/side amounts and coverage evidence are already separate durable
// statistics and are never deleted or reinterpreted by this operation.
func (r *Repository) ArchiveExpiredWebSocketFrames(parent context.Context, now time.Time, limit int) (WebSocketArchiveResult, error) {
	if r == nil || r.pool == nil {
		return WebSocketArchiveResult{}, errors.New("WebSocket retention repository is unavailable")
	}
	if now.IsZero() || now.Add(-WebSocketRawRetention).UnixNano() <= 0 || limit < 1 || limit > WebSocketArchiveMaxBatch {
		return WebSocketArchiveResult{}, errors.New("invalid WebSocket retention time or batch limit")
	}
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	result := WebSocketArchiveResult{}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return WebSocketArchiveResult{}, fmt.Errorf("begin WebSocket archival: %w", err)
	}
	defer rollbackWebSocketTransaction(tx)
	var acquired bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, websocketArchiveLockID).Scan(&acquired); err != nil {
		return WebSocketArchiveResult{}, fmt.Errorf("lock WebSocket archival: %w", err)
	}
	if !acquired {
		result.Busy = true
		return result, nil
	}
	var nextCutoffNS int64
	if err := tx.QueryRow(ctx, `SELECT cutoff_ns
		FROM websocket_retention_state WHERE singleton FOR UPDATE`).Scan(&result.CutoffNS); err != nil {
		return WebSocketArchiveResult{}, fmt.Errorf("lock WebSocket retention cutoff: %w", err)
	}
	var databaseNow time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		return WebSocketArchiveResult{}, fmt.Errorf("read WebSocket retention database clock: %w", err)
	}
	if difference := now.Sub(databaseNow); difference > webSocketArchiveMaxClockSkew || difference < -webSocketArchiveMaxClockSkew {
		return WebSocketArchiveResult{}, errors.New("WebSocket retention caller and database clocks differ; keeping raw data")
	}
	targetCutoffNS := min(now.Add(-WebSocketRawRetention).UnixNano(), databaseNow.Add(-WebSocketRawRetention).UnixNano())
	nextCutoffNS, err = nextWebSocketRetentionCutoff(result.CutoffNS, targetCutoffNS)
	if err != nil {
		return WebSocketArchiveResult{}, err
	}
	// Plan a bounded set before acquiring connection locks. Both append and
	// archive take connection locks before touching frame rows, avoiding a
	// lock-order cycle with a retry waiting for the raw primary key.
	rows, err := tx.Query(ctx, `
		SELECT connection_id, frame_sequence
		FROM websocket_frames WHERE received_at_ns < $1
		ORDER BY received_at_ns LIMIT $2`, result.CutoffNS, limit)
	if err != nil {
		return WebSocketArchiveResult{}, fmt.Errorf("select expired WebSocket frames: %w", err)
	}
	var connections []string
	var sequences []int64
	for rows.Next() {
		var connection string
		var sequence int64
		if err := rows.Scan(&connection, &sequence); err != nil {
			rows.Close()
			return WebSocketArchiveResult{}, err
		}
		connections, sequences = append(connections, connection), append(sequences, sequence)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return WebSocketArchiveResult{}, err
	}
	if len(connections) > 0 {
		if err := lockWebSocketArchiveConnections(ctx, tx, connections, sequences, true); err != nil {
			return WebSocketArchiveResult{}, err
		}
		// Lock the exact planned rows. Retried legacy writes that are already
		// archived are deleted without incrementing their statistics again.
		var newCount, savedGroups, expectedGroups int64
		if err := tx.QueryRow(ctx, `
			WITH selected AS MATERIALIZED (
				SELECT f.exchange_name, f.market, f.frame_class, f.message_type,
					f.received_at_ns, f.payload_encoding, f.payload_uncompressed_bytes,
					octet_length(f.payload) AS stored_bytes,
					c.archived_sequences @> f.frame_sequence AS already_archived
				FROM websocket_frames f
				JOIN unnest($1::text[], $2::bigint[]) ids(connection_id, frame_sequence)
				  USING (connection_id, frame_sequence)
				JOIN websocket_frame_archive_connections c
				  ON c.connection_id = f.connection_id AND c.sequence_block = (f.frame_sequence - 1) / 4096
				WHERE f.received_at_ns < $3
				FOR UPDATE OF f
			), rollups AS MATERIALIZED (
				SELECT resolution_sec, (received_at_ns / 1000000000 / resolution_sec) * resolution_sec AS bucket_start,
					exchange_name, market, frame_class, message_type, count(*) AS frame_count,
					sum(payload_uncompressed_bytes) AS uncompressed_bytes, sum(stored_bytes) AS stored_bytes,
					count(*) FILTER (WHERE payload_encoding = 1) AS gzip_frame_count,
					min(received_at_ns) AS first_received_at_ns, max(received_at_ns) AS last_received_at_ns
				FROM selected CROSS JOIN (VALUES (60), (3600), (86400)) periods(resolution_sec)
				WHERE NOT already_archived
				GROUP BY 1, 2, 3, 4, 5, 6
			), statistics AS (
				INSERT INTO websocket_frame_statistics (
					resolution_sec, bucket_start, exchange_name, market, frame_class, message_type,
					frame_count, uncompressed_bytes, stored_bytes, gzip_frame_count,
					first_received_at_ns, last_received_at_ns
				)
				SELECT * FROM rollups
				ON CONFLICT (resolution_sec, bucket_start, exchange_name, market, frame_class, message_type)
				DO UPDATE SET
					frame_count = websocket_frame_statistics.frame_count + EXCLUDED.frame_count,
					uncompressed_bytes = websocket_frame_statistics.uncompressed_bytes + EXCLUDED.uncompressed_bytes,
					stored_bytes = websocket_frame_statistics.stored_bytes + EXCLUDED.stored_bytes,
					gzip_frame_count = websocket_frame_statistics.gzip_frame_count + EXCLUDED.gzip_frame_count,
					first_received_at_ns = LEAST(websocket_frame_statistics.first_received_at_ns, EXCLUDED.first_received_at_ns),
					last_received_at_ns = GREATEST(websocket_frame_statistics.last_received_at_ns, EXCLUDED.last_received_at_ns),
					aggregated_at = now()
				RETURNING 1
			)
			SELECT count(*) FILTER (WHERE NOT already_archived), count(*),
				(SELECT count(*) FROM statistics), (SELECT count(*) FROM rollups) FROM selected`,
			connections, sequences, result.CutoffNS).Scan(&newCount, &result.DeletedFrames, &savedGroups, &expectedGroups); err != nil {
			return WebSocketArchiveResult{}, fmt.Errorf("save WebSocket statistics: %w", err)
		}
		if result.DeletedFrames != int64(len(connections)) {
			return WebSocketArchiveResult{}, errors.New("WebSocket archive selection changed; keeping raw data")
		}
		if savedGroups != expectedGroups {
			return WebSocketArchiveResult{}, errors.New("WebSocket statistics write count mismatch; keeping raw data")
		}
		result.ArchivedFrames = newCount
		var savedConnections, expectedConnections int64
		if err := tx.QueryRow(ctx, `
			WITH grouped AS (
				SELECT connection_id, sequence_block, range_agg(int8range(frame_sequence,
					CASE WHEN frame_sequence = 9223372036854775807 THEN NULL ELSE frame_sequence + 1 END)) AS sequences,
					count(*) AS frames
				FROM unnest($1::text[], $2::bigint[]) ids(connection_id, frame_sequence)
				JOIN websocket_frame_archive_connections c USING (connection_id)
				WHERE c.sequence_block = (frame_sequence - 1) / 4096
				  AND NOT (c.archived_sequences @> frame_sequence)
				GROUP BY connection_id, sequence_block
			), updated AS (
				UPDATE websocket_frame_archive_connections c
				SET archived_sequences = c.archived_sequences + g.sequences,
					archived_frame_count = c.archived_frame_count + g.frames, updated_at = now()
				FROM grouped g WHERE c.connection_id = g.connection_id AND c.sequence_block = g.sequence_block RETURNING 1
			)
			SELECT (SELECT count(*) FROM updated), (SELECT count(*) FROM grouped)`, connections, sequences).Scan(&savedConnections, &expectedConnections); err != nil {
			return WebSocketArchiveResult{}, fmt.Errorf("save WebSocket archive identities: %w", err)
		}
		if savedConnections != expectedConnections {
			return WebSocketArchiveResult{}, errors.New("WebSocket archive identity write count mismatch; keeping raw data")
		}
		// This separate statement runs only after every statistics/identity write
		// succeeded. Any delete, cancellation or commit failure rolls back both.
		tag, err := tx.Exec(ctx, `
			DELETE FROM websocket_frames f
			USING unnest($1::text[], $2::bigint[]) ids(connection_id, frame_sequence)
			WHERE f.connection_id = ids.connection_id AND f.frame_sequence = ids.frame_sequence
			  AND f.received_at_ns < $3`, connections, sequences, result.CutoffNS)
		if err != nil {
			return WebSocketArchiveResult{}, fmt.Errorf("delete archived WebSocket frames: %w", err)
		}
		if tag.RowsAffected() != result.DeletedFrames {
			return WebSocketArchiveResult{}, errors.New("WebSocket archive delete count mismatch; rolling back")
		}
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(min(received_at_ns), 0) FROM websocket_frames`).Scan(&result.OldestRawReceivedAtNS); err != nil {
		return WebSocketArchiveResult{}, fmt.Errorf("check WebSocket retention backlog: %w", err)
	}
	tag, err := tx.Exec(ctx, `UPDATE websocket_retention_state
		SET cutoff_ns = $1, updated_at = clock_timestamp() WHERE singleton`, nextCutoffNS)
	if err != nil {
		return WebSocketArchiveResult{}, fmt.Errorf("advance WebSocket retention cutoff: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return WebSocketArchiveResult{}, errors.New("WebSocket retention cutoff row changed; keeping raw data")
	}
	if err := tx.Commit(ctx); err != nil {
		return WebSocketArchiveResult{}, fmt.Errorf("commit WebSocket archival: %w", err)
	}
	return result, nil
}

func nextWebSocketRetentionCutoff(storedCutoffNS, targetCutoffNS int64) (int64, error) {
	if storedCutoffNS <= 0 || targetCutoffNS < storedCutoffNS {
		return 0, errors.New("WebSocket retention clock moved behind its durable cutoff; keeping raw data")
	}
	return min(targetCutoffNS, storedCutoffNS+int64(webSocketArchiveCutoffStep)), nil
}

// Each retry row covers at most 4,096 sequence values, as fixed by migration
// 0026. Mixed trade/order-book streams contain real holes; a single multirange
// per long-lived connection would grow unbounded and be detoasted per frame.
func lockWebSocketArchiveConnections(ctx context.Context, tx pgx.Tx, connections []string, sequences []int64, exclusive bool) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO websocket_frame_archive_connections(connection_id, sequence_block)
		SELECT DISTINCT connection_id, (frame_sequence - 1) / 4096 AS sequence_block
		FROM unnest($1::text[], $2::bigint[]) ids(connection_id, frame_sequence)
		ORDER BY connection_id, sequence_block ON CONFLICT DO NOTHING`, connections, sequences); err != nil {
		return fmt.Errorf("ensure WebSocket archive connections: %w", err)
	}
	lock := " FOR KEY SHARE OF c"
	if exclusive {
		lock = " FOR UPDATE OF c"
	}
	if _, err := tx.Exec(ctx, `
		WITH requested AS (
			SELECT DISTINCT connection_id, (frame_sequence - 1) / 4096 AS sequence_block
			FROM unnest($1::text[], $2::bigint[]) ids(connection_id, frame_sequence)
		)
		SELECT c.connection_id FROM websocket_frame_archive_connections c
		JOIN requested USING (connection_id, sequence_block)
		ORDER BY c.connection_id, c.sequence_block`+lock, connections, sequences); err != nil {
		return fmt.Errorf("lock WebSocket archive connections: %w", err)
	}
	return nil
}

func rollbackWebSocketTransaction(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}
