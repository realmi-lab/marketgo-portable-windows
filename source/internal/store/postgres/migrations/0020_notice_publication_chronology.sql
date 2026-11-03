ALTER TABLE exchange_notices
    ADD COLUMN IF NOT EXISTS revision_ts_ms BIGINT NOT NULL DEFAULT 0;

UPDATE exchange_notices
SET sort_ts_ms = CASE
        WHEN published_ts_ms > 0 THEN published_ts_ms
        WHEN detected_ts_ms > 0 THEN detected_ts_ms
        ELSE updated_ts_ms
    END,
    revision_ts_ms = CASE
        WHEN GREATEST(published_ts_ms, updated_ts_ms) > 0
            THEN GREATEST(published_ts_ms, updated_ts_ms)
        ELSE detected_ts_ms
    END
WHERE sort_ts_ms IS DISTINCT FROM CASE
        WHEN published_ts_ms > 0 THEN published_ts_ms
        WHEN detected_ts_ms > 0 THEN detected_ts_ms
        ELSE updated_ts_ms
    END
   OR revision_ts_ms IS DISTINCT FROM CASE
        WHEN GREATEST(published_ts_ms, updated_ts_ms) > 0
            THEN GREATEST(published_ts_ms, updated_ts_ms)
        ELSE detected_ts_ms
    END;

CREATE INDEX IF NOT EXISTS exchange_notices_revision_idx
    ON exchange_notices(revision_ts_ms DESC);

COMMENT ON COLUMN exchange_notices.sort_ts_ms IS
    'Publication chronology: official published time, then detection fallback.';
COMMENT ON COLUMN exchange_notices.revision_ts_ms IS
    'Incremental freshness: latest official published/updated time, then detection fallback.';
