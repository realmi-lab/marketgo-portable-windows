CREATE TABLE IF NOT EXISTS official_candle_10m_market_coverage (
    range_start BIGINT NOT NULL,
    range_end BIGINT NOT NULL,
    exchange_name TEXT NOT NULL,
    market TEXT NOT NULL,
    row_count BIGINT NOT NULL,
    verified_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (range_start, range_end, exchange_name, market),
    CHECK (range_start > 0 AND range_end > range_start),
    CHECK (range_start % 600 = 0 AND range_end % 600 = 0),
    CHECK (row_count = (range_end - range_start) / 600)
);
