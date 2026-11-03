CREATE TABLE IF NOT EXISTS minute_observation_seals (
    exchange_name TEXT NOT NULL,
    minute_start BIGINT NOT NULL CHECK (minute_start >= 0 AND minute_start % 60 = 0),
    status TEXT NOT NULL CHECK (status IN ('COMPLETE', 'PARTIAL', 'MISSING')),
    coverage_seconds BIGINT NOT NULL CHECK (coverage_seconds BETWEEN 0 AND 60),
    gap_seconds BIGINT NOT NULL CHECK (gap_seconds BETWEEN 0 AND 60),
    registry_market_count BIGINT NOT NULL CHECK (registry_market_count >= 0),
    observed_market_count BIGINT NOT NULL CHECK (observed_market_count >= 0),
    observed_zero_count BIGINT NOT NULL CHECK (observed_zero_count >= 0),
    unknown_market_count BIGINT NOT NULL CHECK (unknown_market_count >= 0),
    gap_market_count BIGINT NOT NULL CHECK (gap_market_count >= 0),
    scheduled_market_count BIGINT NOT NULL CHECK (scheduled_market_count >= 0),
    registry_fingerprint TEXT NOT NULL CHECK (length(registry_fingerprint) = 64),
    sealed_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (exchange_name, minute_start),
    CHECK (coverage_seconds + gap_seconds = 60),
    CHECK (observed_market_count <= registry_market_count),
    CHECK (observed_zero_count <= registry_market_count),
    CHECK (unknown_market_count <= registry_market_count),
    CHECK (gap_market_count <= registry_market_count),
    CHECK (scheduled_market_count <= registry_market_count),
    CHECK (
        (status = 'COMPLETE' AND coverage_seconds = 60 AND gap_seconds = 0
            AND observed_zero_count = registry_market_count - observed_market_count
            AND unknown_market_count = 0)
        OR
        (status = 'PARTIAL' AND coverage_seconds BETWEEN 0 AND 59
            AND gap_market_count > 0
            AND observed_zero_count + unknown_market_count = registry_market_count - observed_market_count
            AND unknown_market_count <= gap_market_count)
        OR
        (status = 'MISSING' AND coverage_seconds = 0 AND gap_seconds = 60
            AND gap_market_count = registry_market_count
            AND observed_zero_count + unknown_market_count = registry_market_count - observed_market_count
            AND unknown_market_count <= gap_market_count)
    )
);

CREATE INDEX IF NOT EXISTS minute_observation_seals_latest_idx
    ON minute_observation_seals (minute_start DESC, exchange_name);
