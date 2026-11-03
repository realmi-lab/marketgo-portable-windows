-- Persist one compact summary per exchange refresh and only lifecycle deltas.
-- Unchanged markets are not rewritten every ten minutes. The current table
-- retains inactive rows as tombstones so first discovery and re-addition stay
-- distinguishable without replaying the complete event history.
CREATE TABLE IF NOT EXISTS market_registry_refreshes (
    observed_at_ms BIGINT NOT NULL,
    exchange_name TEXT NOT NULL,
    status TEXT NOT NULL,
    authoritative BOOLEAN NOT NULL,
    registry_version BIGINT NOT NULL,
    previous_market_count INTEGER NOT NULL,
    observed_market_count INTEGER NOT NULL,
    active_market_count INTEGER NOT NULL,
    active_asset_count INTEGER NOT NULL,
    quote_counts_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    public_fee_market_count INTEGER NOT NULL,
    discovered_count INTEGER NOT NULL,
    added_count INTEGER NOT NULL,
    removed_count INTEGER NOT NULL,
    error_text TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (observed_at_ms, exchange_name),
    CHECK (observed_at_ms > 0),
    CHECK (btrim(exchange_name) <> ''),
    CHECK (status IN ('SUCCESS', 'ERROR')),
    CHECK ((status = 'SUCCESS') = authoritative),
    CHECK (
        (status = 'SUCCESS' AND error_text = '') OR
        (status = 'ERROR' AND btrim(error_text) <> '')
    ),
    CHECK (registry_version >= 0),
    CHECK (
        previous_market_count >= 0 AND
        observed_market_count >= 0 AND
        active_market_count >= 0 AND
        active_asset_count >= 0 AND
        public_fee_market_count >= 0 AND
        discovered_count >= 0 AND
        added_count >= 0 AND
        removed_count >= 0
    ),
    CHECK (active_asset_count <= active_market_count),
    CHECK (public_fee_market_count <= active_market_count),
    CHECK (jsonb_typeof(quote_counts_json) = 'object'),
    CHECK (
        (status = 'SUCCESS' AND active_market_count = observed_market_count) OR
        (status = 'ERROR' AND observed_market_count = 0 AND active_market_count = previous_market_count)
    ),
    CHECK (
        (status = 'SUCCESS' AND
            active_market_count::BIGINT = previous_market_count::BIGINT + added_count::BIGINT - removed_count::BIGINT AND
            discovered_count <= added_count AND
            removed_count <= previous_market_count) OR
        (status = 'ERROR' AND discovered_count = 0 AND added_count = 0 AND removed_count = 0)
    )
);

CREATE INDEX IF NOT EXISTS market_registry_refreshes_exchange_time_idx
    ON market_registry_refreshes(exchange_name, observed_at_ms DESC);

CREATE TABLE IF NOT EXISTS market_registry_current (
    exchange_name TEXT NOT NULL,
    market TEXT NOT NULL,
    base TEXT NOT NULL,
    quote TEXT NOT NULL,
    scope TEXT NOT NULL,
    maker_fee_rate DOUBLE PRECISION,
    taker_fee_rate DOUBLE PRECISION,
    fee_source TEXT NOT NULL DEFAULT '',
    active BOOLEAN NOT NULL,
    first_discovered_at_ms BIGINT NOT NULL,
    current_since_ms BIGINT,
    removed_at_ms BIGINT,
    last_changed_at_ms BIGINT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (exchange_name, market),
    FOREIGN KEY (first_discovered_at_ms, exchange_name)
        REFERENCES market_registry_refreshes(observed_at_ms, exchange_name),
    FOREIGN KEY (current_since_ms, exchange_name)
        REFERENCES market_registry_refreshes(observed_at_ms, exchange_name),
    FOREIGN KEY (removed_at_ms, exchange_name)
        REFERENCES market_registry_refreshes(observed_at_ms, exchange_name),
    FOREIGN KEY (last_changed_at_ms, exchange_name)
        REFERENCES market_registry_refreshes(observed_at_ms, exchange_name),
    CHECK (btrim(exchange_name) <> ''),
    CHECK (btrim(market) <> '' AND btrim(base) <> '' AND btrim(quote) <> ''),
    CHECK (scope IN ('domestic', 'global')),
    CHECK (maker_fee_rate IS NULL OR (maker_fee_rate >= 0 AND maker_fee_rate < 1)),
    CHECK (taker_fee_rate IS NULL OR (taker_fee_rate >= 0 AND taker_fee_rate < 1)),
    CHECK (first_discovered_at_ms > 0 AND last_changed_at_ms >= first_discovered_at_ms),
    CHECK (
        (active AND current_since_ms IS NOT NULL AND removed_at_ms IS NULL AND
            current_since_ms >= first_discovered_at_ms AND last_changed_at_ms >= current_since_ms) OR
        (NOT active AND current_since_ms IS NULL AND removed_at_ms IS NOT NULL AND
            removed_at_ms >= first_discovered_at_ms AND last_changed_at_ms >= removed_at_ms)
    )
);

CREATE INDEX IF NOT EXISTS market_registry_current_active_quote_idx
    ON market_registry_current(exchange_name, quote, base, market)
    WHERE active;

CREATE INDEX IF NOT EXISTS market_registry_current_active_base_idx
    ON market_registry_current(base, exchange_name, market)
    WHERE active;

CREATE TABLE IF NOT EXISTS market_registry_events (
    event_id BIGSERIAL PRIMARY KEY,
    observed_at_ms BIGINT NOT NULL,
    exchange_name TEXT NOT NULL,
    market TEXT NOT NULL,
    base TEXT NOT NULL,
    quote TEXT NOT NULL,
    scope TEXT NOT NULL,
    event_type TEXT NOT NULL,
    maker_fee_rate DOUBLE PRECISION,
    taker_fee_rate DOUBLE PRECISION,
    fee_source TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (observed_at_ms, exchange_name)
        REFERENCES market_registry_refreshes(observed_at_ms, exchange_name) ON DELETE CASCADE,
    UNIQUE (observed_at_ms, exchange_name, market, event_type),
    CHECK (observed_at_ms > 0),
    CHECK (btrim(exchange_name) <> ''),
    CHECK (btrim(market) <> '' AND btrim(base) <> '' AND btrim(quote) <> ''),
    CHECK (scope IN ('domestic', 'global')),
    CHECK (event_type IN ('DISCOVERED', 'ADDED', 'REMOVED')),
    CHECK (maker_fee_rate IS NULL OR (maker_fee_rate >= 0 AND maker_fee_rate < 1)),
    CHECK (taker_fee_rate IS NULL OR (taker_fee_rate >= 0 AND taker_fee_rate < 1))
);

CREATE INDEX IF NOT EXISTS market_registry_events_time_idx
    ON market_registry_events(observed_at_ms DESC, event_id DESC);

CREATE INDEX IF NOT EXISTS market_registry_events_market_time_idx
    ON market_registry_events(exchange_name, market, observed_at_ms DESC, event_id DESC);

CREATE INDEX IF NOT EXISTS market_registry_events_type_time_idx
    ON market_registry_events(event_type, observed_at_ms DESC, event_id DESC);
