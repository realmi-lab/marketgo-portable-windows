-- Opt-in storage only. Existing journal rows, identities and hashes are unchanged.
-- Readers must be upgraded before aggregation_metadata enables compact-v1 writes.
CREATE TABLE ingestion_frame_blocks (
    day BIGINT NOT NULL CHECK (day >= 0),
    first_cursor BIGINT NOT NULL CHECK (first_cursor > 0),
    last_cursor BIGINT NOT NULL CHECK (last_cursor >= first_cursor),
    version INTEGER NOT NULL CHECK (version = 1),
    replay BYTEA NOT NULL,
    raw BYTEA NOT NULL,
    replay_hash BYTEA NOT NULL CHECK (octet_length(replay_hash) = 32),
    raw_hash BYTEA NOT NULL CHECK (octet_length(raw_hash) = 32),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (day, first_cursor)
) PARTITION BY RANGE (day);
ALTER TABLE ingestion_frame_blocks ALTER COLUMN replay SET STORAGE EXTERNAL;
ALTER TABLE ingestion_frame_blocks ALTER COLUMN raw SET STORAGE EXTERNAL;

-- A prepared week keeps daily DDL out of foreground appends. The default
-- partition preserves durability if the DB owner has not prepared a later day.
DO $prepare$
DECLARE today bigint := floor(extract(epoch FROM clock_timestamp())/86400)::bigint;
        day_number bigint;
BEGIN
    FOR day_number IN today-1..today+7 LOOP
        EXECUTE format('CREATE TABLE %I PARTITION OF ingestion_frame_blocks FOR VALUES FROM (%s) TO (%s)',
            'ingestion_frame_blocks_d' || day_number, day_number, day_number+1);
    END LOOP;
END $prepare$;
CREATE TABLE ingestion_frame_blocks_default PARTITION OF ingestion_frame_blocks DEFAULT;

-- Snapshot anchors and registry/loss evidence have different lifetimes; they
-- cannot pin an expired frame partition. No foreign keys force hot header scans.
CREATE TABLE ingestion_snapshot_blocks (LIKE ingestion_frame_blocks INCLUDING ALL);
CREATE INDEX ingestion_snapshot_blocks_cursor_idx ON ingestion_snapshot_blocks(first_cursor);
