-- Independent collector -> compute log. Schema only; no original rows change.
CREATE TABLE ingestion_stream_head (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    committed_cursor BIGINT NOT NULL DEFAULT 0 CHECK (committed_cursor >= 0)
);
INSERT INTO ingestion_stream_head(singleton) VALUES (TRUE);

CREATE TABLE ingestion_events (
    cursor BIGINT PRIMARY KEY CHECK (cursor > 0),
    event_id TEXT NOT NULL UNIQUE,
    schema_version INTEGER NOT NULL CHECK (schema_version = 1),
    kind TEXT NOT NULL CHECK (kind IN ('frame','loss','registry','official','health')),
    received_at_ns BIGINT NOT NULL CHECK (received_at_ns > 0),
    payload JSONB NOT NULL,
    content_sha256 BYTEA NOT NULL,
    committed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ingestion_events_received_idx ON ingestion_events(received_at_ns);

CREATE TABLE computation_checkpoint (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    applied_cursor BIGINT NOT NULL DEFAULT 0 CHECK (applied_cursor >= 0),
	fx_state BYTEA NOT NULL DEFAULT ''::bytea,
    committed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
INSERT INTO computation_checkpoint(singleton) VALUES (TRUE);

-- Applied frame envelopes (raw plus parsed replay data) have the same 96-hour
-- raw retention horizon. Compact cursor/identity/hash tombstones remain.
ALTER TABLE ingestion_events ADD COLUMN archived_at TIMESTAMPTZ;
ALTER TABLE ingestion_events ADD COLUMN projection_error TEXT NOT NULL DEFAULT '';
CREATE INDEX ingestion_events_projection_error_idx ON ingestion_events(cursor)
    WHERE projection_error <> '' AND archived_at IS NULL;
CREATE INDEX ingestion_events_retention_idx ON ingestion_events(received_at_ns,cursor)
    WHERE kind = 'frame' AND archived_at IS NULL;
CREATE TABLE ingestion_retention_state (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    cutoff_ns BIGINT NOT NULL CHECK (cutoff_ns > 0)
);
INSERT INTO ingestion_retention_state(singleton,cutoff_ns)
VALUES(TRUE,(extract(epoch FROM clock_timestamp() - interval '96 hours') * 1000000000)::BIGINT);
CREATE TABLE ingestion_frame_statistics (
    start_sec BIGINT NOT NULL,
    interval_sec BIGINT NOT NULL CHECK (interval_sec IN (60,3600,86400)),
    exchange_name TEXT NOT NULL,
    market TEXT NOT NULL,
    frame_class TEXT NOT NULL,
    message_type TEXT NOT NULL,
    frame_count BIGINT NOT NULL CHECK (frame_count > 0),
    envelope_storage_bytes BIGINT NOT NULL CHECK (envelope_storage_bytes >= 0),
    first_received_at_ns BIGINT NOT NULL,
    last_received_at_ns BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(start_sec,interval_sec,exchange_name,market,frame_class,message_type)
);
