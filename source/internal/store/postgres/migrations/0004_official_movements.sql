CREATE TABLE IF NOT EXISTS official_share_samples (
    as_of_ms BIGINT PRIMARY KEY,
    status TEXT NOT NULL,
    source TEXT NOT NULL,
    collection_span_ms BIGINT NOT NULL,
    total_volume_krw DOUBLE PRECISION NOT NULL,
    bithumb_volume_krw DOUBLE PRECISION NOT NULL,
    bithumb_share_pct DOUBLE PRECISION NOT NULL,
    exchanges_json JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_official_share_samples_time
    ON official_share_samples(as_of_ms DESC);

CREATE TABLE IF NOT EXISTS official_share_events (
    event_id TEXT PRIMARY KEY,
    direction TEXT NOT NULL,
    state TEXT NOT NULL,
    started_at_ms BIGINT NOT NULL,
    confirmed_at_ms BIGINT,
    updated_at_ms BIGINT NOT NULL,
    ended_at_ms BIGINT,
    baseline_share_pct DOUBLE PRECISION NOT NULL,
    current_share_pct DOUBLE PRECISION NOT NULL,
    extreme_share_pct DOUBLE PRECISION NOT NULL,
    change_pp DOUBLE PRECISION NOT NULL,
    extreme_change_pp DOUBLE PRECISION NOT NULL,
    threshold_pp DOUBLE PRECISION NOT NULL,
    quality_status TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_official_share_events_time
    ON official_share_events(updated_at_ms DESC);

CREATE TABLE IF NOT EXISTS official_share_event_drivers (
    event_id TEXT NOT NULL REFERENCES official_share_events(event_id) ON DELETE CASCADE,
    asset TEXT NOT NULL,
    rank_no INTEGER NOT NULL,
    contribution_pp DOUBLE PRECISION NOT NULL,
    bithumb_volume_change_krw DOUBLE PRECISION NOT NULL,
    competitor_volume_change_krw DOUBLE PRECISION NOT NULL,
    previous_bithumb_volume_krw DOUBLE PRECISION NOT NULL,
    current_bithumb_volume_krw DOUBLE PRECISION NOT NULL,
    previous_competitor_volume_krw DOUBLE PRECISION NOT NULL,
    current_competitor_volume_krw DOUBLE PRECISION NOT NULL,
    PRIMARY KEY(event_id, asset)
);

CREATE TABLE IF NOT EXISTS official_share_event_exchange_drivers (
    event_id TEXT NOT NULL REFERENCES official_share_events(event_id) ON DELETE CASCADE,
    exchange_name TEXT NOT NULL,
    rank_no INTEGER NOT NULL,
    contribution_pp DOUBLE PRECISION NOT NULL,
    volume_change_krw DOUBLE PRECISION NOT NULL,
    previous_volume_krw DOUBLE PRECISION NOT NULL,
    current_volume_krw DOUBLE PRECISION NOT NULL,
    leading_asset TEXT NOT NULL,
    leading_asset_contribution_pp DOUBLE PRECISION NOT NULL,
    PRIMARY KEY(event_id, exchange_name)
);

CREATE TABLE IF NOT EXISTS official_share_event_assets (
    event_id TEXT NOT NULL REFERENCES official_share_events(event_id) ON DELETE CASCADE,
    snapshot_kind TEXT NOT NULL,
    exchange_name TEXT NOT NULL,
    asset TEXT NOT NULL,
    volume_krw DOUBLE PRECISION NOT NULL,
    as_of_ms BIGINT NOT NULL,
    PRIMARY KEY(event_id, snapshot_kind, exchange_name, asset)
);

CREATE TABLE IF NOT EXISTS official_share_event_evidence (
    event_id TEXT NOT NULL REFERENCES official_share_events(event_id) ON DELETE CASCADE,
    exchange_name TEXT NOT NULL,
    notice_id TEXT NOT NULL,
    title TEXT NOT NULL,
    url TEXT NOT NULL,
    category TEXT NOT NULL,
    published_ts_ms BIGINT NOT NULL,
    detected_ts_ms BIGINT NOT NULL,
    anchor_ts_ms BIGINT NOT NULL,
    relation TEXT NOT NULL,
    matched_assets_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    PRIMARY KEY(event_id, exchange_name, notice_id, anchor_ts_ms)
);
