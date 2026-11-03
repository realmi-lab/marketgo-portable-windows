CREATE TABLE IF NOT EXISTS realtime_flow_observations (
    as_of_ms BIGINT PRIMARY KEY,
    contract_version TEXT NOT NULL,
    status TEXT NOT NULL,
    direction TEXT NOT NULL,
    reason TEXT NOT NULL,
    primary_window_sec BIGINT NOT NULL,
    share_delta_pp DOUBLE PRECISION,
    domestic_volume_1s_krw DOUBLE PRECISION NOT NULL,
    bithumb_volume_1s_krw DOUBLE PRECISION NOT NULL,
    bithumb_share_1s_pct DOUBLE PRECISION,
    detail_level TEXT NOT NULL,
    payload_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status IN ('WARMING', 'NORMAL', 'WATCH', 'ALERT', 'DEGRADED')),
    CHECK (
        status NOT IN ('WATCH', 'ALERT') OR
        (direction = 'UP' AND share_delta_pp > 0) OR
        (direction = 'DOWN' AND share_delta_pp < 0)
    )
);

CREATE INDEX IF NOT EXISTS realtime_flow_observations_time_idx
    ON realtime_flow_observations(as_of_ms DESC);

CREATE INDEX IF NOT EXISTS realtime_flow_observations_status_time_idx
    ON realtime_flow_observations(status, as_of_ms DESC);

CREATE TABLE IF NOT EXISTS realtime_flow_windows (
    as_of_ms BIGINT NOT NULL REFERENCES realtime_flow_observations(as_of_ms) ON DELETE CASCADE,
    duration_sec BIGINT NOT NULL,
    status TEXT NOT NULL,
    current_share_pct DOUBLE PRECISION,
    previous_share_pct DOUBLE PRECISION,
    share_delta_pp DOUBLE PRECISION,
    current_volume_krw DOUBLE PRECISION NOT NULL,
    previous_volume_krw DOUBLE PRECISION NOT NULL,
    threshold_pp DOUBLE PRECISION NOT NULL,
    PRIMARY KEY(as_of_ms, duration_sec)
);

CREATE TABLE IF NOT EXISTS realtime_flow_asset_impacts (
    as_of_ms BIGINT NOT NULL REFERENCES realtime_flow_observations(as_of_ms) ON DELETE CASCADE,
    asset TEXT NOT NULL,
    rank_no INTEGER NOT NULL,
    contribution_pp DOUBLE PRECISION NOT NULL,
    bithumb_volume_change_krw DOUBLE PRECISION NOT NULL,
    competitor_volume_change_krw DOUBLE PRECISION NOT NULL,
    previous_bithumb_volume_krw DOUBLE PRECISION NOT NULL,
    current_bithumb_volume_krw DOUBLE PRECISION NOT NULL,
    previous_competitor_volume_krw DOUBLE PRECISION NOT NULL,
    current_competitor_volume_krw DOUBLE PRECISION NOT NULL,
    bithumb_buy_pct DOUBLE PRECISION,
    top_competitor TEXT NOT NULL,
    top_competitor_volume_krw DOUBLE PRECISION NOT NULL,
    orderbook_json JSONB,
    PRIMARY KEY(as_of_ms, asset)
);

CREATE INDEX IF NOT EXISTS realtime_flow_asset_impacts_asset_time_idx
    ON realtime_flow_asset_impacts(asset, as_of_ms DESC);

CREATE TABLE IF NOT EXISTS realtime_flow_exchange_impacts (
    as_of_ms BIGINT NOT NULL REFERENCES realtime_flow_observations(as_of_ms) ON DELETE CASCADE,
    exchange_name TEXT NOT NULL,
    rank_no INTEGER NOT NULL,
    contribution_pp DOUBLE PRECISION NOT NULL,
    volume_change_krw DOUBLE PRECISION NOT NULL,
    previous_volume_krw DOUBLE PRECISION NOT NULL,
    current_volume_krw DOUBLE PRECISION NOT NULL,
    leading_asset TEXT NOT NULL,
    leading_asset_contribution_pp DOUBLE PRECISION NOT NULL,
    PRIMARY KEY(as_of_ms, exchange_name)
);

CREATE INDEX IF NOT EXISTS realtime_flow_exchange_impacts_exchange_time_idx
    ON realtime_flow_exchange_impacts(exchange_name, as_of_ms DESC);

CREATE TABLE IF NOT EXISTS realtime_flow_market_impacts (
    as_of_ms BIGINT NOT NULL REFERENCES realtime_flow_observations(as_of_ms) ON DELETE CASCADE,
    asset TEXT NOT NULL,
    exchange_name TEXT NOT NULL,
    asset_rank_no INTEGER NOT NULL,
    exchange_rank_no INTEGER NOT NULL,
    contribution_pp DOUBLE PRECISION NOT NULL,
    volume_change_krw DOUBLE PRECISION NOT NULL,
    previous_volume_krw DOUBLE PRECISION NOT NULL,
    current_volume_krw DOUBLE PRECISION NOT NULL,
    buy_pct DOUBLE PRECISION,
    orderbook_json JSONB,
    PRIMARY KEY(as_of_ms, asset, exchange_name)
);

CREATE INDEX IF NOT EXISTS realtime_flow_market_impacts_market_time_idx
    ON realtime_flow_market_impacts(asset, exchange_name, as_of_ms DESC);
