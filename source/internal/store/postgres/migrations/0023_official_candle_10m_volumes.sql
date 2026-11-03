CREATE TABLE IF NOT EXISTS official_candle_10m_volumes (
    bucket_start BIGINT NOT NULL,
    exchange_name TEXT NOT NULL,
    market TEXT NOT NULL,
    base TEXT NOT NULL,
    quote TEXT NOT NULL DEFAULT 'KRW',
    base_volume DOUBLE PRECISION NOT NULL,
    quote_volume_krw DOUBLE PRECISION,
    has_trades BOOLEAN NOT NULL,
    notional_method TEXT NOT NULL,
    source_interval_sec BIGINT NOT NULL,
    source TEXT NOT NULL,
    collected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (bucket_start, exchange_name, market),
    CHECK (bucket_start > 0 AND bucket_start % 600 = 0),
    CHECK (btrim(exchange_name) <> '' AND btrim(market) <> '' AND btrim(base) <> ''),
    CHECK (quote = 'KRW'),
    CHECK (base_volume >= 0 AND base_volume < 'Infinity'::DOUBLE PRECISION),
    CHECK (quote_volume_krw IS NULL OR
           (quote_volume_krw >= 0 AND quote_volume_krw < 'Infinity'::DOUBLE PRECISION)),
    CHECK ((has_trades AND (base_volume > 0 OR COALESCE(quote_volume_krw, 0) > 0)) OR
           (NOT has_trades AND base_volume = 0 AND COALESCE(quote_volume_krw, 0) = 0)),
    CHECK (notional_method IN ('EXACT_QUOTE_VOLUME', 'BASE_VOLUME_ONLY')),
    CHECK (source_interval_sec IN (300, 600))
);

CREATE INDEX IF NOT EXISTS official_candle_10m_exchange_bucket_idx
    ON official_candle_10m_volumes(exchange_name, bucket_start);
CREATE INDEX IF NOT EXISTS official_candle_10m_base_bucket_idx
    ON official_candle_10m_volumes(base, bucket_start);

CREATE TABLE IF NOT EXISTS official_candle_10m_backfill_runs (
    run_id BIGSERIAL PRIMARY KEY,
    range_start BIGINT NOT NULL,
    range_end BIGINT NOT NULL,
    rate_fraction DOUBLE PRECISION NOT NULL,
    status TEXT NOT NULL,
    requested_market_count BIGINT NOT NULL DEFAULT 0,
    completed_market_count BIGINT NOT NULL DEFAULT 0,
    failed_market_count BIGINT NOT NULL DEFAULT 0,
    request_count BIGINT NOT NULL DEFAULT 0,
    row_count BIGINT NOT NULL DEFAULT 0,
    detail_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    CHECK (range_start > 0 AND range_end > range_start),
    CHECK (range_start % 600 = 0 AND range_end % 600 = 0),
    CHECK (rate_fraction > 0 AND rate_fraction <= 0.8),
    CHECK (status IN ('RUNNING', 'COMPLETE', 'PARTIAL', 'FAILED'))
);
