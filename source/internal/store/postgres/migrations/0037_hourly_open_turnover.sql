CREATE TABLE IF NOT EXISTS hourly_open_turnover (
 exchange_name text NOT NULL, market text NOT NULL, hour_start bigint NOT NULL CHECK(hour_start>0 AND hour_start%3600=0),
 quote text NOT NULL, open_price numeric NOT NULL CHECK(open_price>0 AND open_price< 'Infinity'::numeric),
 quote_turnover numeric CHECK(quote_turnover>=0 AND quote_turnover<'Infinity'::numeric),
 source text NOT NULL, received_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(exchange_name,market,hour_start)
);
CREATE INDEX IF NOT EXISTS hourly_open_turnover_time ON hourly_open_turnover(hour_start);
CREATE TABLE IF NOT EXISTS hourly_open_turnover_jobs (
 run_id text NOT NULL, exchange_name text NOT NULL, market text NOT NULL, base text NOT NULL, quote text NOT NULL,
 start_sec bigint NOT NULL, end_sec bigint NOT NULL, status text NOT NULL DEFAULT 'PENDING',
 attempts integer NOT NULL DEFAULT 0, row_count integer, error text, next_attempt timestamptz NOT NULL DEFAULT now(),
 checked_at timestamptz, response_sha256 text,
 PRIMARY KEY(run_id,exchange_name,market,start_sec),
 CHECK(start_sec%3600=0 AND end_sec%3600=0 AND end_sec>start_sec AND end_sec-start_sec<=720000),
 CHECK(status IN ('PENDING','FETCHED','EMPTY','RETRY','FAILED'))
);
CREATE TABLE IF NOT EXISTS hourly_open_turnover_runs (
 run_id text PRIMARY KEY, start_sec bigint NOT NULL, end_sec bigint NOT NULL,
 cohort_at timestamptz NOT NULL DEFAULT now(), historical_universe_verified boolean NOT NULL DEFAULT false
);
CREATE TABLE IF NOT EXISTS hourly_open_turnover_cooldowns (
 exchange_name text PRIMARY KEY, retry_at timestamptz NOT NULL
);
