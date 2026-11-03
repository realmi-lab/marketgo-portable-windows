CREATE TABLE IF NOT EXISTS official_aligned_1h_samples (
    bucket_ms BIGINT PRIMARY KEY,
    observed_at_ms BIGINT NOT NULL,
    status TEXT NOT NULL,
    source TEXT NOT NULL,
    collection_span_ms BIGINT NOT NULL,
    total_volume_krw DOUBLE PRECISION NOT NULL,
    bithumb_volume_krw DOUBLE PRECISION NOT NULL,
    bithumb_share_pct DOUBLE PRECISION NOT NULL,
    exchanges_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    asset_row_count INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_official_aligned_1h_samples_observed
    ON official_aligned_1h_samples(observed_at_ms DESC);

CREATE TABLE IF NOT EXISTS official_aligned_1h_markets (
    bucket_ms BIGINT NOT NULL REFERENCES official_aligned_1h_samples(bucket_ms) ON DELETE CASCADE,
    exchange_name TEXT NOT NULL,
    asset TEXT NOT NULL,
    volume_krw DOUBLE PRECISION NOT NULL,
    PRIMARY KEY(bucket_ms, exchange_name, asset)
);

CREATE INDEX IF NOT EXISTS idx_official_aligned_1h_markets_asset_time
    ON official_aligned_1h_markets(asset, bucket_ms DESC);

ALTER TABLE official_share_events
    ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'OFFICIAL_DOMESTIC_24H_TICKERS';

CREATE INDEX IF NOT EXISTS idx_official_share_events_source_updated
    ON official_share_events(source, updated_at_ms DESC);
