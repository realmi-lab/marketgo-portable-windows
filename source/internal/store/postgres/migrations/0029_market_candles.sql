-- Independent collector projection; no historical fill or source-table mutation.
CREATE TABLE market_candle_trade_ids (
    exchange_name TEXT NOT NULL,
    market TEXT NOT NULL,
    trade_id TEXT NOT NULL,
    evidence_hash BYTEA NOT NULL,
    event_ts_ms BIGINT NOT NULL,
    PRIMARY KEY (exchange_name, market, trade_id)
);

CREATE TABLE market_candles (
    exchange_name TEXT NOT NULL CHECK (exchange_name IN ('bithumb','upbit','coinone','korbit','gopax')),
    market TEXT NOT NULL,
    base TEXT NOT NULL,
    quote TEXT NOT NULL,
    interval_sec BIGINT NOT NULL CHECK (interval_sec IN (60,600,3600,86400)),
    start_sec BIGINT NOT NULL CHECK (start_sec >= 0 AND start_sec % interval_sec = 0),
    open_price DOUBLE PRECISION NOT NULL CHECK (open_price > 0 AND open_price < 'Infinity'::float8),
    high_price DOUBLE PRECISION NOT NULL CHECK (high_price >= open_price AND high_price >= close_price AND high_price < 'Infinity'::float8),
    low_price DOUBLE PRECISION NOT NULL CHECK (low_price > 0 AND low_price <= open_price AND low_price <= close_price),
    close_price DOUBLE PRECISION NOT NULL CHECK (close_price > 0 AND close_price < 'Infinity'::float8),
    base_volume DOUBLE PRECISION NOT NULL CHECK (base_volume > 0 AND base_volume < 'Infinity'::float8),
    quote_volume DOUBLE PRECISION NOT NULL CHECK (quote_volume > 0 AND quote_volume < 'Infinity'::float8),
    trade_count BIGINT NOT NULL CHECK (trade_count > 0),
    first_ts_ms BIGINT NOT NULL,
    first_trade_id TEXT COLLATE "C" NOT NULL,
    last_ts_ms BIGINT NOT NULL,
    last_trade_id TEXT COLLATE "C" NOT NULL,
    source_kind TEXT NOT NULL DEFAULT 'WEBSOCKET_TRADE',
    coverage_status TEXT NOT NULL DEFAULT 'OBSERVED_PARTIAL',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (exchange_name, market, interval_sec, start_sec)
);
CREATE INDEX market_candles_interval_lookup ON market_candles(exchange_name,interval_sec,start_sec);

-- Price remains in the canonical persisted candle row, avoiding a second writer.
-- A time-closed candle still has partial observation unless separately proven.
CREATE VIEW bithumb_usdt_closes AS
SELECT market, interval_sec, start_sec, start_sec + interval_sec AS end_sec,
       close_price AS close_price_krw, last_ts_ms, source_kind, coverage_status,
       start_sec + interval_sec <= extract(epoch FROM statement_timestamp()) AS time_closed
FROM market_candles
WHERE exchange_name = 'bithumb' AND base = 'USDT' AND quote = 'KRW';

-- Native websocket messages replace the same observation slot, never add volume.
-- Undocumented rolling boundaries remain NULL (not receive-time buckets).
CREATE TABLE native_ws_candle_observations (
    evidence_hash BYTEA NOT NULL,
    exchange_name TEXT NOT NULL,
    market TEXT NOT NULL,
    base TEXT NOT NULL,
    quote TEXT NOT NULL,
    interval_sec BIGINT NOT NULL CHECK (interval_sec > 0),
    start_sec BIGINT,
    end_sec BIGINT,
    close_price NUMERIC NOT NULL CHECK (close_price > 0),
    source_kind TEXT NOT NULL,
    observation_slot TEXT NOT NULL,
    source_order BIGINT NOT NULL,
    boundary_verified BOOLEAN NOT NULL,
    closed BOOLEAN NOT NULL,
    received_at_ns BIGINT NOT NULL CHECK (received_at_ns > 0),
    payload_json JSONB NOT NULL,
    CHECK ((start_sec IS NULL AND end_sec IS NULL AND NOT boundary_verified AND NOT closed)
        OR (start_sec IS NOT NULL AND end_sec IS NOT NULL AND end_sec = start_sec + interval_sec)),
    CHECK (NOT closed OR boundary_verified),
    PRIMARY KEY(exchange_name,market,interval_sec,source_kind,observation_slot)
);
CREATE INDEX native_ws_candle_lookup ON native_ws_candle_observations(exchange_name, market, interval_sec, received_at_ns);
CREATE VIEW bithumb_native_usdt_closes AS
SELECT interval_sec,start_sec,end_sec,close_price AS close_price_krw,
       source_kind,boundary_verified,closed,received_at_ns,payload_json
FROM native_ws_candle_observations
WHERE exchange_name='bithumb' AND base='USDT' AND quote='KRW';

-- User contract: no Bithumb daily candle or native 24H ticker. Only verified
-- hourly inputs can produce this separate rolling 24-hour summary.
ALTER TABLE market_candles ADD CHECK (exchange_name <> 'bithumb' OR interval_sec <> 86400);
ALTER TABLE native_ws_candle_observations ADD CHECK (exchange_name <> 'bithumb' OR interval_sec = 3600);
CREATE TABLE bithumb_hourly_24h (
    exchange_name TEXT NOT NULL CHECK (exchange_name='bithumb'),
    market TEXT NOT NULL,
    base TEXT NOT NULL,
    quote TEXT NOT NULL,
    start_sec BIGINT NOT NULL CHECK (start_sec % 3600 = 0),
    end_sec BIGINT NOT NULL CHECK (end_sec = start_sec + 86400),
    quote_volume NUMERIC,
    base_volume NUMERIC,
    close_price NUMERIC,
    observed_hours BIGINT NOT NULL DEFAULT 24 CHECK (observed_hours BETWEEN 0 AND 24),
    coverage_status TEXT NOT NULL DEFAULT 'VERIFIED',
    source_kind TEXT NOT NULL,
    payload_json JSONB NOT NULL,
    PRIMARY KEY (market,end_sec)
);
