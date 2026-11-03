-- Retention deletes search oldest rows by their time column. The existing
-- primary keys for these tables begin with exchange/market and cannot serve
-- that access pattern efficiently.
CREATE INDEX IF NOT EXISTS ws_trade_minutes_retention_idx
    ON ws_trade_minutes (minute_start);

CREATE INDEX IF NOT EXISTS official_minute_candles_retention_idx
    ON official_minute_candles (minute_start);

CREATE INDEX IF NOT EXISTS official_minute_candle_observations_retention_idx
    ON official_minute_candle_observations (minute_start);

-- Only completed events and resolved errors are eligible. Partial indexes keep
-- active operational rows out of the retention index as well as the delete.
CREATE INDEX IF NOT EXISTS official_share_events_retention_idx
    ON official_share_events (ended_at_ms)
    WHERE state = 'ENDED' AND ended_at_ms IS NOT NULL;

CREATE INDEX IF NOT EXISTS notice_source_errors_retention_idx
    ON notice_source_errors (resolved_ts)
    WHERE resolved_ts > 0;
