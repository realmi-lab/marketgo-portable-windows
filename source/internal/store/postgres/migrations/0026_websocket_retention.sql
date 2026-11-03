-- Additive only: applying this migration never deletes existing evidence.
-- Legacy frames have no parser classification; do not infer successful trades.
--
-- Building a normal B-tree on a populated journal would hold a SHARE lock for
-- the whole build and stop the live RowExclusive append path. A populated
-- database must therefore prepare this exact index with CREATE INDEX
-- CONCURRENTLY before entering the transaction-only migration runner. An empty
-- database may build it here after taking a non-waiting SHARE lock and checking
-- emptiness again. Wrong, incomplete, or invalid same-name indexes fail closed.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '25s';

DO $websocket_retention_index$
DECLARE
    journal_has_rows boolean;
    named_index_exists boolean;
    retention_index_ready boolean;
BEGIN
    SELECT EXISTS (
        SELECT 1
        FROM pg_class index_relation
        JOIN pg_class table_relation
          ON table_relation.oid = 'websocket_frames'::regclass
         AND index_relation.relnamespace = table_relation.relnamespace
        WHERE index_relation.relkind = 'i'
          AND index_relation.relname = 'websocket_frames_retention_idx'
    ) INTO named_index_exists;

    SELECT EXISTS (
        SELECT 1
        FROM pg_class table_relation
        JOIN pg_attribute received_column
          ON received_column.attrelid = table_relation.oid
         AND received_column.attname = 'received_at_ns'
         AND NOT received_column.attisdropped
        JOIN pg_index index_metadata
          ON index_metadata.indrelid = table_relation.oid
        JOIN pg_class index_relation
          ON index_relation.oid = index_metadata.indexrelid
         AND index_relation.relnamespace = table_relation.relnamespace
        JOIN pg_am access_method
          ON access_method.oid = index_relation.relam
        JOIN pg_opclass operator_class
          ON operator_class.oid = index_metadata.indclass[0]
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
    ) INTO retention_index_ready;

    IF named_index_exists AND NOT retention_index_ready THEN
        RAISE EXCEPTION USING MESSAGE =
            'migration 0026 found an invalid or incompatible websocket_frames_retention_idx; keep websocket_frames unchanged and repair the index before retrying';
    END IF;

    IF retention_index_ready THEN
        RETURN;
    END IF;

    SELECT EXISTS (SELECT 1 FROM websocket_frames LIMIT 1)
      INTO journal_has_rows;
    IF journal_has_rows THEN
        RAISE EXCEPTION USING MESSAGE =
            'migration 0026 requires CREATE INDEX CONCURRENTLY websocket_frames_retention_idx ON websocket_frames (received_at_ns) before migrating a nonempty journal';
    END IF;

    -- Do not wait behind, or race with, a writer that starts after the first
    -- empty check. The migration runner rolls back immediately on contention.
    LOCK TABLE websocket_frames IN SHARE MODE NOWAIT;
    SELECT EXISTS (SELECT 1 FROM websocket_frames LIMIT 1)
      INTO journal_has_rows;
    IF journal_has_rows THEN
        RAISE EXCEPTION USING MESSAGE =
            'migration 0026 journal became nonempty; prepare websocket_frames_retention_idx concurrently and retry';
    END IF;
    CREATE INDEX websocket_frames_retention_idx
        ON websocket_frames (received_at_ns);
END
$websocket_retention_index$;

ALTER TABLE websocket_frames
    ADD COLUMN frame_class text NOT NULL DEFAULT 'UNKNOWN',
    ADD COLUMN market text NOT NULL DEFAULT '',
    ADD CONSTRAINT websocket_frames_frame_class_check
        CHECK (frame_class IN ('UNKNOWN', 'TRADE', 'PARTIAL_TRADE', 'PARSE_ERROR',
            'SUBSCRIPTION_ERROR', 'SNAPSHOT', 'OTHER')) NOT VALID,
    ADD CONSTRAINT websocket_frames_market_length_check
        CHECK (octet_length(market) <= 128) NOT VALID;

CREATE TABLE websocket_frame_statistics (
    resolution_sec integer NOT NULL CHECK (resolution_sec IN (60, 3600, 86400)),
    bucket_start bigint NOT NULL CHECK (bucket_start >= 0),
    exchange_name text NOT NULL,
    market text NOT NULL,
    frame_class text NOT NULL,
    message_type smallint NOT NULL,
    frame_count bigint NOT NULL CHECK (frame_count > 0),
    uncompressed_bytes bigint NOT NULL CHECK (uncompressed_bytes >= 0),
    stored_bytes bigint NOT NULL CHECK (stored_bytes >= 0),
    gzip_frame_count bigint NOT NULL CHECK (gzip_frame_count BETWEEN 0 AND frame_count),
    first_received_at_ns bigint NOT NULL CHECK (first_received_at_ns > 0),
    last_received_at_ns bigint NOT NULL CHECK (last_received_at_ns >= first_received_at_ns),
    aggregated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (resolution_sec, bucket_start, exchange_name, market, frame_class, message_type),
    CHECK (bucket_start % resolution_sec = 0),
    CHECK (octet_length(market) <= 128),
    CHECK (frame_class IN ('UNKNOWN', 'TRADE', 'PARTIAL_TRADE', 'PARSE_ERROR',
        'SUBSCRIPTION_ERROR', 'SNAPSHOT', 'OTHER'))
);

-- The deletion boundary is durable and monotonic. Each successful archival
-- transaction uses the stored boundary, then advances it by at most one second
-- toward the current database cutoff. A process or database wall-clock jump
-- therefore cannot turn one restart into a multi-hour/day deletion jump.
CREATE TABLE websocket_retention_state (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    cutoff_ns bigint NOT NULL CHECK (cutoff_ns > 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO websocket_retention_state (singleton, cutoff_ns)
VALUES (
    true,
    floor(extract(epoch FROM clock_timestamp() - interval '96 hours') * 1000000000)::bigint
);

-- Compact retry identities survive payload deletion. Ranges preserve real
-- sequence holes and clock reversals instead of treating max(sequence) as proof.
-- Bound each multirange to 4,096 sequence values. Mixed trade/book streams have
-- gaps, so one multirange per connection would grow indefinitely and eventually
-- time out both archival and live append. Lock (connection_id, sequence_block)
-- in that order. The final block includes bigint's maximum without overflow.
CREATE TABLE websocket_frame_archive_connections (
    connection_id text NOT NULL,
    sequence_block bigint NOT NULL CHECK (sequence_block BETWEEN 0 AND 2251799813685247),
    archived_sequences int8multirange NOT NULL DEFAULT '{}',
    archived_frame_count bigint NOT NULL DEFAULT 0 CHECK (archived_frame_count BETWEEN 0 AND 4096),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (connection_id, sequence_block),
    CHECK (archived_sequences <@ int8multirange(int8range(
        sequence_block * 4096 + 1,
        CASE WHEN sequence_block = 2251799813685247 THEN NULL
             ELSE (sequence_block + 1) * 4096 + 1 END
    )))
);
