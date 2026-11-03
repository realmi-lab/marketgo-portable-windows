CREATE TABLE IF NOT EXISTS official_ticker_1m_samples (
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

CREATE INDEX IF NOT EXISTS idx_official_ticker_1m_samples_observed
    ON official_ticker_1m_samples(observed_at_ms DESC);

CREATE TABLE IF NOT EXISTS official_ticker_1m_markets (
    bucket_ms BIGINT NOT NULL REFERENCES official_ticker_1m_samples(bucket_ms) ON DELETE CASCADE,
    exchange_name TEXT NOT NULL,
    asset TEXT NOT NULL,
    volume_krw DOUBLE PRECISION NOT NULL,
    PRIMARY KEY(bucket_ms, exchange_name, asset)
);

CREATE INDEX IF NOT EXISTS idx_official_ticker_1m_markets_asset_time
    ON official_ticker_1m_markets(asset, bucket_ms DESC);

-- Existing official samples provide an immediate one-minute share and
-- exchange trend. They predate per-asset retention, so the zero row count
-- intentionally prevents fabricated historical coin attribution.
INSERT INTO official_ticker_1m_samples (
    bucket_ms, observed_at_ms, status, source, collection_span_ms,
    total_volume_krw, bithumb_volume_krw, bithumb_share_pct,
    exchanges_json, asset_row_count
)
SELECT DISTINCT ON ((as_of_ms / 60000) * 60000)
    (as_of_ms / 60000) * 60000,
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
  AND as_of_ms >= (EXTRACT(EPOCH FROM now()) * 1000)::BIGINT - 604800000
ORDER BY (as_of_ms / 60000) * 60000, as_of_ms ASC
ON CONFLICT(bucket_ms) DO NOTHING;
