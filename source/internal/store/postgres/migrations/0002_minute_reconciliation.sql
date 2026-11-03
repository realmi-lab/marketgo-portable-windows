CREATE TABLE IF NOT EXISTS ws_trade_minutes (
    exchange_name TEXT NOT NULL,
    market TEXT NOT NULL,
    base TEXT NOT NULL,
    quote TEXT NOT NULL,
    minute_start BIGINT NOT NULL,
    open_price DOUBLE PRECISION,
    high_price DOUBLE PRECISION,
    low_price DOUBLE PRECISION,
    close_price DOUBLE PRECISION,
    base_volume DOUBLE PRECISION NOT NULL,
    quote_volume DOUBLE PRECISION NOT NULL,
    krw_volume DOUBLE PRECISION,
    trade_count BIGINT NOT NULL,
    buy_krw_volume DOUBLE PRECISION,
    sell_krw_volume DOUBLE PRECISION,
    first_trade_ts_ms BIGINT,
    last_trade_ts_ms BIGINT,
    unresolved_trade_count BIGINT NOT NULL,
    coverage_status TEXT NOT NULL,
    gap_seconds BIGINT NOT NULL CHECK (gap_seconds BETWEEN 0 AND 60),
    source_kind TEXT NOT NULL,
    detail_status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (exchange_name, market, minute_start)
);

CREATE TABLE IF NOT EXISTS official_minute_candles (
    exchange_name TEXT NOT NULL,
    market TEXT NOT NULL,
    base TEXT NOT NULL,
    quote TEXT NOT NULL,
    minute_start BIGINT NOT NULL,
    open_price DOUBLE PRECISION,
    high_price DOUBLE PRECISION,
    low_price DOUBLE PRECISION,
    close_price DOUBLE PRECISION,
    base_volume DOUBLE PRECISION NOT NULL,
    quote_volume DOUBLE PRECISION,
    quote_volume_exact BOOLEAN NOT NULL,
    source_kind TEXT NOT NULL,
    notional_method TEXT NOT NULL,
    fetched_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (exchange_name, market, minute_start)
);

CREATE TABLE IF NOT EXISTS official_minute_candle_observations (
    exchange_name TEXT NOT NULL,
    market TEXT NOT NULL,
    base TEXT NOT NULL,
    quote TEXT NOT NULL,
    minute_start BIGINT NOT NULL,
    observation_attempt INTEGER NOT NULL CHECK (observation_attempt >= 0),
    open_price DOUBLE PRECISION,
    high_price DOUBLE PRECISION,
    low_price DOUBLE PRECISION,
    close_price DOUBLE PRECISION,
    base_volume DOUBLE PRECISION NOT NULL,
    quote_volume DOUBLE PRECISION,
    quote_volume_exact BOOLEAN NOT NULL,
    source_kind TEXT NOT NULL,
    notional_method TEXT NOT NULL,
    fetched_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (exchange_name, market, minute_start, observation_attempt)
);

INSERT INTO official_minute_candle_observations (
    exchange_name, market, base, quote, minute_start, observation_attempt,
    open_price, high_price, low_price, close_price, base_volume, quote_volume,
    quote_volume_exact, source_kind, notional_method, fetched_at, created_at
)
SELECT exchange_name, market, base, quote, minute_start, 0,
       open_price, high_price, low_price, close_price, base_volume, quote_volume,
       quote_volume_exact, source_kind, notional_method, fetched_at, created_at
FROM official_minute_candles first_row
WHERE NOT EXISTS (
    SELECT 1 FROM official_minute_candle_observations observed
    WHERE observed.exchange_name = first_row.exchange_name
      AND observed.market = first_row.market
      AND observed.minute_start = first_row.minute_start
)
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS minute_reconciliations (
    exchange_name TEXT NOT NULL,
    market TEXT NOT NULL,
    base TEXT NOT NULL,
    quote TEXT NOT NULL,
    minute_start BIGINT NOT NULL,
    status TEXT NOT NULL,
    reason TEXT NOT NULL,
    ws_present BOOLEAN NOT NULL,
    official_present BOOLEAN NOT NULL,
    known_gap_seconds BIGINT NOT NULL,
    base_volume_matches BOOLEAN,
    quote_volume_matches BOOLEAN,
    ohlc_matches BOOLEAN,
    ws_base_volume DOUBLE PRECISION,
    ws_quote_volume DOUBLE PRECISION,
    ws_trade_count BIGINT,
    official_base_volume DOUBLE PRECISION,
    official_quote_volume DOUBLE PRECISION,
    base_volume_delta DOUBLE PRECISION,
    quote_volume_delta DOUBLE PRECISION,
    effective_base_volume DOUBLE PRECISION,
    effective_quote_volume DOUBLE PRECISION,
    effective_krw_volume DOUBLE PRECISION,
    effective_trade_count BIGINT,
    effective_buy_krw_volume DOUBLE PRECISION,
    effective_sell_krw_volume DOUBLE PRECISION,
    effective_source TEXT NOT NULL,
    trade_detail_status TEXT NOT NULL,
    checked_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (exchange_name, market, minute_start)
);

CREATE TABLE IF NOT EXISTS exchange_minute_reconciliations (
    exchange_name TEXT NOT NULL,
    minute_start BIGINT NOT NULL,
    status TEXT NOT NULL,
    market_count BIGINT NOT NULL,
    ws_market_count BIGINT NOT NULL,
    official_market_count BIGINT NOT NULL,
    verified_count BIGINT NOT NULL,
    recovered_count BIGINT NOT NULL,
    mismatch_count BIGINT NOT NULL,
    ws_missing_count BIGINT NOT NULL,
    base_only_count BIGINT NOT NULL,
    candle_unavailable_count BIGINT NOT NULL,
    unknown_detail_count BIGINT NOT NULL,
    ws_quote_volume DOUBLE PRECISION NOT NULL,
    official_quote_volume DOUBLE PRECISION,
    effective_quote_volume DOUBLE PRECISION,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (exchange_name, minute_start)
);

CREATE INDEX IF NOT EXISTS minute_reconciliations_latest_idx
    ON minute_reconciliations (minute_start DESC, exchange_name);
CREATE INDEX IF NOT EXISTS official_minute_observations_latest_idx
    ON official_minute_candle_observations
    (exchange_name, market, minute_start, observation_attempt DESC);
CREATE INDEX IF NOT EXISTS exchange_minute_reconciliations_latest_idx
    ON exchange_minute_reconciliations (minute_start DESC);
