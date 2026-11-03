-- Official REST history, separate from observed WS candles and rolling tickers.
CREATE TABLE official_interval_candles (
 exchange_name TEXT NOT NULL,
 market TEXT NOT NULL,
 base TEXT NOT NULL,
 quote TEXT NOT NULL CHECK (quote='KRW'),
 interval_sec BIGINT NOT NULL CHECK (interval_sec IN (600,900,1800)),
 start_sec BIGINT NOT NULL CHECK (start_sec>0 AND start_sec%interval_sec=0),
 base_volume DOUBLE PRECISION NOT NULL CHECK (base_volume>=0 AND base_volume<'Infinity'::float8),
 quote_volume DOUBLE PRECISION CHECK (quote_volume>=0 AND quote_volume<'Infinity'::float8),
 source TEXT NOT NULL,
 collected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(exchange_name,market,interval_sec,start_sec)
);
CREATE INDEX official_interval_candles_time ON official_interval_candles(start_sec,exchange_name,market);
