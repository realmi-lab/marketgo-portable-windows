CREATE TABLE IF NOT EXISTS volume_buckets (
    second BIGINT NOT NULL,
    scope TEXT NOT NULL,
    exchange_name TEXT NOT NULL,
    market TEXT NOT NULL,
    base TEXT NOT NULL,
    quote TEXT NOT NULL,
    native_volume DOUBLE PRECISION NOT NULL,
    krw_volume DOUBLE PRECISION NOT NULL,
    trade_count BIGINT NOT NULL,
    unresolved_count BIGINT NOT NULL,
    buy_krw_volume DOUBLE PRECISION NOT NULL DEFAULT 0,
    sell_krw_volume DOUBLE PRECISION NOT NULL DEFAULT 0,
    base_volume DOUBLE PRECISION NOT NULL DEFAULT 0,
    source_granularity_sec BIGINT NOT NULL DEFAULT 1,
    source_kind TEXT NOT NULL DEFAULT 'WEBSOCKET_TRADE',
    notional_method TEXT NOT NULL DEFAULT 'EXACT_TRADE',
    PRIMARY KEY (second, exchange_name, market)
);

CREATE INDEX IF NOT EXISTS volume_buckets_second_idx
    ON volume_buckets(second);
CREATE INDEX IF NOT EXISTS volume_buckets_base_second_idx
    ON volume_buckets(base, second);
CREATE INDEX IF NOT EXISTS volume_buckets_scope_quote_second_idx
    ON volume_buckets(scope, quote, second);

CREATE TABLE IF NOT EXISTS trade_watermarks (
    exchange_name TEXT NOT NULL,
    market TEXT NOT NULL,
    trade_id BIGINT NOT NULL,
    PRIMARY KEY (exchange_name, market)
);

CREATE TABLE IF NOT EXISTS recent_trade_ids (
    exchange_name TEXT NOT NULL,
    market TEXT NOT NULL,
    trade_id TEXT NOT NULL,
    event_second BIGINT NOT NULL,
    PRIMARY KEY (exchange_name, market, trade_id)
);

CREATE TABLE IF NOT EXISTS observation_coverage (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    contiguous_start BIGINT NOT NULL,
    last_observed BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS observation_segments (
    start_second BIGINT PRIMARY KEY,
    end_second BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS observation_segments_end_idx
    ON observation_segments(end_second);

CREATE TABLE IF NOT EXISTS aggregation_metadata (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS observation_gap_events (
    gap_id BIGSERIAL PRIMARY KEY,
    start_second BIGINT NOT NULL,
    end_second BIGINT,
    status TEXT NOT NULL,
    affected_exchanges_json TEXT NOT NULL DEFAULT '[]',
    affected_markets_json TEXT NOT NULL DEFAULT '[]',
    observed_volume_krw DOUBLE PRECISION,
    observed_total_volume_krw DOUBLE PRECISION,
    observed_volume_at_risk_pct DOUBLE PRECISION,
    weight_window_sec BIGINT NOT NULL DEFAULT 10,
    reason_codes_json TEXT NOT NULL DEFAULT '[]',
    created_at DOUBLE PRECISION NOT NULL,
    closed_at DOUBLE PRECISION
);

CREATE TABLE IF NOT EXISTS historical_backfill_runs (
    gap_id BIGINT NOT NULL,
    exchange_name TEXT NOT NULL,
    quote TEXT NOT NULL,
    source TEXT NOT NULL,
    start_second BIGINT NOT NULL,
    end_second BIGINT NOT NULL,
    status TEXT NOT NULL,
    market_count BIGINT NOT NULL,
    successful_market_count BIGINT NOT NULL,
    fetched_trade_count BIGINT NOT NULL,
    bucket_count BIGINT NOT NULL,
    request_count BIGINT NOT NULL,
    completed_at DOUBLE PRECISION NOT NULL,
    detail_json TEXT NOT NULL,
    PRIMARY KEY (gap_id, exchange_name, quote, source)
);

CREATE INDEX IF NOT EXISTS historical_backfill_runs_status_idx
    ON historical_backfill_runs(status, completed_at DESC);
