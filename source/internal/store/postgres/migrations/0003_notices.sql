CREATE TABLE IF NOT EXISTS exchange_notices (
    exchange_name TEXT NOT NULL,
    notice_id TEXT NOT NULL,
    title TEXT NOT NULL,
    url TEXT NOT NULL,
    category TEXT NOT NULL,
    published_ts_ms BIGINT NOT NULL,
    detected_ts_ms BIGINT NOT NULL,
    timestamp_precision TEXT NOT NULL,
    assets_json TEXT NOT NULL DEFAULT '[]',
    source_type TEXT NOT NULL,
    updated_ts_ms BIGINT NOT NULL DEFAULT 0,
    sort_ts_ms BIGINT NOT NULL DEFAULT 0,
    body_text TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    schedules_json TEXT NOT NULL DEFAULT '[]',
    risk_tags_json TEXT NOT NULL DEFAULT '[]',
    image_urls_json TEXT NOT NULL DEFAULT '[]',
    image_text TEXT NOT NULL DEFAULT '',
    image_analysis_json TEXT NOT NULL DEFAULT '[]',
    content_source TEXT NOT NULL DEFAULT 'LIST_ONLY',
    analysis_status TEXT NOT NULL DEFAULT 'LIST_ONLY',
    analysis_confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
    content_hash TEXT NOT NULL DEFAULT '',
    detail_checked_ts_ms BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (exchange_name, notice_id)
);

CREATE INDEX IF NOT EXISTS exchange_notices_sort_idx
    ON exchange_notices(sort_ts_ms DESC);

CREATE INDEX IF NOT EXISTS exchange_notices_source_sort_idx
    ON exchange_notices(exchange_name, sort_ts_ms);

CREATE TABLE IF NOT EXISTS notice_source_state (
    exchange_name TEXT PRIMARY KEY,
    last_history_scan_ts DOUBLE PRECISION NOT NULL,
    history_status TEXT NOT NULL,
    history_note TEXT NOT NULL,
    oldest_fetched_ts_ms BIGINT NOT NULL,
    pages_scanned BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS notice_analysis_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS notice_source_errors (
    id BIGSERIAL PRIMARY KEY,
    exchange_name TEXT NOT NULL,
    error_kind TEXT NOT NULL,
    message TEXT NOT NULL,
    started_ts DOUBLE PRECISION NOT NULL,
    last_seen_ts DOUBLE PRECISION NOT NULL,
    resolved_ts DOUBLE PRECISION NOT NULL DEFAULT 0,
    occurrence_count BIGINT NOT NULL DEFAULT 1,
    blocked_until_ts DOUBLE PRECISION NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS notice_source_errors_time_idx
    ON notice_source_errors(started_ts DESC);
