CREATE TABLE IF NOT EXISTS official_ticker_10m_samples (
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

CREATE INDEX IF NOT EXISTS idx_official_ticker_10m_samples_observed
    ON official_ticker_10m_samples(observed_at_ms DESC);

CREATE TABLE IF NOT EXISTS official_ticker_10m_markets (
    bucket_ms BIGINT NOT NULL REFERENCES official_ticker_10m_samples(bucket_ms) ON DELETE CASCADE,
    exchange_name TEXT NOT NULL,
    asset TEXT NOT NULL,
    volume_krw DOUBLE PRECISION NOT NULL,
    PRIMARY KEY(bucket_ms, exchange_name, asset)
);

CREATE INDEX IF NOT EXISTS idx_official_ticker_10m_markets_asset_time
    ON official_ticker_10m_markets(asset, bucket_ms DESC);

-- Preserve the already collected official share trend immediately. Historical
-- samples did not retain per-asset values, so asset_row_count remains zero and
-- the API will not fabricate historical coin attribution from them.
INSERT INTO official_ticker_10m_samples (
    bucket_ms, observed_at_ms, status, source, collection_span_ms,
    total_volume_krw, bithumb_volume_krw, bithumb_share_pct,
    exchanges_json, asset_row_count
)
SELECT DISTINCT ON ((as_of_ms / 600000) * 600000)
    (as_of_ms / 600000) * 600000,
    as_of_ms,
    status,
    source,
    collection_span_ms,
    total_volume_krw,
    bithumb_volume_krw,
    bithumb_share_pct,
    exchanges_json,
    0
FROM official_share_samples
WHERE status = 'LIVE'
ORDER BY (as_of_ms / 600000) * 600000, as_of_ms ASC
ON CONFLICT(bucket_ms) DO NOTHING;
