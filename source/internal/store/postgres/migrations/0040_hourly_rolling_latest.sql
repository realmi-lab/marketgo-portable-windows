-- Derived latest values only. Never modify the raw hourly archive.
CREATE TABLE hourly_rolling_turnover_latest (
 exchange_name text NOT NULL,
 market text NOT NULL,
 base text NOT NULL,
 quote text NOT NULL,
 window_end bigint NOT NULL CHECK(window_end>0 AND window_end%3600=0),
 quote_turnover numeric NOT NULL CHECK(quote_turnover>=0 AND quote_turnover<'Infinity'::numeric),
 turnover_krw numeric NOT NULL CHECK(turnover_krw>=0 AND turnover_krw<'Infinity'::numeric),
 known_hours integer NOT NULL CHECK(known_hours BETWEEN 0 AND 24),
 status text NOT NULL CHECK(status IN ('OK','INCOMPLETE','SOURCE_UNAVAILABLE','FAILED','COLLECTING','INACTIVE')),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(exchange_name,market),
 CHECK(status='OK' OR (quote_turnover=0 AND turnover_krw=0)),
 CHECK(status<>'OK' OR known_hours=24)
);
