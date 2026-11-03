-- Requires 0029. Additive columns preserve old collection writers during rollout.
ALTER TABLE native_ws_candle_observations
 ADD COLUMN open_price NUMERIC,
 ADD COLUMN high_price NUMERIC,
 ADD COLUMN low_price NUMERIC,
 ADD COLUMN base_volume NUMERIC,
 ADD COLUMN quote_volume NUMERIC,
 ADD COLUMN volume_semantics TEXT NOT NULL DEFAULT 'UNKNOWN',
 ADD COLUMN source_date TEXT,
 ADD COLUMN source_time TEXT,
 ADD COLUMN normalization_error TEXT NOT NULL DEFAULT 'NOT_PROJECTED';

-- Compatibility projection: old binaries can continue writing payload_json.
-- Projection errors never discard the stored native evidence. A bounded Go
-- repair replays this trigger after a parser/schema fix. NULL is never zero.
CREATE FUNCTION normalize_native_market_observation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 BEGIN
  NEW.open_price := (NEW.payload_json->>'Open')::numeric;
  NEW.high_price := (NEW.payload_json->>'High')::numeric;
  NEW.low_price := (NEW.payload_json->>'Low')::numeric;
  NEW.base_volume := COALESCE(NEW.payload_json->>'BaseVolume',NEW.payload_json->>'TargetVolume')::numeric;
  NEW.quote_volume := (NEW.payload_json->>'QuoteVolume')::numeric;
  NEW.source_date := NEW.payload_json->>'SourceDate';
  NEW.source_time := NEW.payload_json->>'SourceTime';
  NEW.volume_semantics := CASE
   WHEN NEW.exchange_name='coinone' AND NEW.source_kind='websocket_coinone_chart' THEN 'DOCUMENTED_ROLLING_24H_UNVERIFIED'
   WHEN NEW.exchange_name='bithumb' AND NEW.source_kind='BITHUMB_LEGACY_WS_1H' THEN 'UNDOCUMENTED_1H_WINDOW'
   WHEN NEW.exchange_name='upbit' AND NEW.source_kind='websocket_upbit_candle' AND NEW.boundary_verified THEN 'INTERVAL'
   ELSE 'UNKNOWN' END;
  IF NEW.open_price IS NULL OR NEW.high_price IS NULL OR NEW.low_price IS NULL
   OR NEW.open_price<=0 OR NEW.high_price<greatest(NEW.open_price,NEW.close_price)
   OR NEW.low_price<=0 OR NEW.low_price>least(NEW.open_price,NEW.close_price)
   OR NEW.high_price>='Infinity'::numeric OR NEW.close_price>='Infinity'::numeric
   OR NEW.base_volume<0 OR NEW.base_volume>='Infinity'::numeric
   OR NEW.quote_volume<0 OR NEW.quote_volume>='Infinity'::numeric THEN
   RAISE EXCEPTION 'invalid native numeric evidence';
  END IF;
  NEW.normalization_error := '';
 EXCEPTION WHEN OTHERS THEN
  NEW.open_price := NULL; NEW.high_price := NULL; NEW.low_price := NULL;
  NEW.base_volume := NULL; NEW.quote_volume := NULL;
  NEW.volume_semantics := 'UNKNOWN'; NEW.normalization_error := SQLERRM;
  INSERT INTO native_normalization_attempts(exchange_name,market,interval_sec,source_kind,
   observation_slot,evidence_hash,error_text,payload_json)
  VALUES(NEW.exchange_name,NEW.market,NEW.interval_sec,NEW.source_kind,
   NEW.observation_slot,NEW.evidence_hash,NEW.normalization_error,NEW.payload_json);
 END;
 RETURN NEW;
END $$;
CREATE TRIGGER native_market_normalization BEFORE INSERT OR UPDATE OF payload_json
 ON native_ws_candle_observations FOR EACH ROW EXECUTE FUNCTION normalize_native_market_observation();
-- Do not rewrite all historical rows under a migration lock. The bounded repair
-- method handles NOT_PROJECTED rows and records every attempt after rollout.
CREATE INDEX native_market_asset_time ON native_ws_candle_observations
 (base,quote,interval_sec,start_sec,exchange_name,source_kind);
CREATE INDEX native_market_received_time ON native_ws_candle_observations
 (exchange_name,base,quote,received_at_ns);
CREATE INDEX native_market_source_clock ON native_ws_candle_observations
 (exchange_name,base,quote,source_date,source_time) WHERE source_date IS NOT NULL;
CREATE INDEX native_market_repair ON native_ws_candle_observations(received_at_ns)
 WHERE normalization_error<>'';
CREATE INDEX market_candles_asset_time ON market_candles(base,quote,interval_sec,start_sec,exchange_name);

CREATE TABLE native_normalization_attempts (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 exchange_name TEXT NOT NULL, market TEXT NOT NULL, interval_sec BIGINT NOT NULL,
 source_kind TEXT NOT NULL, observation_slot TEXT NOT NULL,
 evidence_hash BYTEA NOT NULL, attempted_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 error_text TEXT NOT NULL,
 payload_json JSONB -- failed source evidence survives later slot updates
);
CREATE INDEX native_normalization_attempt_lookup ON native_normalization_attempts
 (exchange_name,market,interval_sec,source_kind,observation_slot,attempted_at DESC);

-- This queue concerns native interval availability; it does not duplicate
-- canonical trade gaps, ingestion replay checkpoints or REST minute evidence.
CREATE TABLE native_history_repairs (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 exchange_name TEXT NOT NULL, market TEXT NOT NULL, base TEXT NOT NULL, quote TEXT NOT NULL,
 source_kind TEXT NOT NULL, interval_sec BIGINT NOT NULL CHECK(interval_sec IN (60,600,3600,86400)),
 start_sec BIGINT NOT NULL CHECK(start_sec>=0 AND start_sec%interval_sec=0),
 end_sec BIGINT NOT NULL CHECK(end_sec>start_sec AND end_sec%interval_sec=0),
 status TEXT NOT NULL DEFAULT 'PENDING' CHECK(status IN ('PENDING','RUNNING','RETRY','COMPLETE','UNAVAILABLE')),
 attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts>=0),
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 lease_until TIMESTAMPTZ, last_error TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 CHECK(exchange_name<>'bithumb'), -- legacy 1H lacks a verified native interval
 UNIQUE(exchange_name,market,source_kind,interval_sec,start_sec,end_sec)
);
CREATE INDEX native_history_repair_due ON native_history_repairs(next_attempt_at,id)
 WHERE status IN ('PENDING','RETRY','RUNNING');
CREATE TABLE native_history_repair_attempts (
 repair_id BIGINT NOT NULL REFERENCES native_history_repairs(id),
 attempt INTEGER NOT NULL, started_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 finished_at TIMESTAMPTZ, status TEXT NOT NULL, error_text TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(repair_id,attempt)
);
