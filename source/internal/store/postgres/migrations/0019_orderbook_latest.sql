CREATE TABLE orderbook_latest (
    exchange_name text NOT NULL,
    market text NOT NULL,
    base text NOT NULL,
    quote text NOT NULL,
    event_at_ms bigint NOT NULL CHECK (event_at_ms > 0),
    received_at_ms bigint NOT NULL CHECK (received_at_ms > 0),
    sequence bigint,
    bids_json jsonb NOT NULL,
    asks_json jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (exchange_name, market),
    CHECK (btrim(exchange_name) <> ''),
    CHECK (btrim(market) <> '' AND btrim(base) <> '' AND btrim(quote) <> ''),
    CHECK (jsonb_typeof(bids_json) = 'array' AND jsonb_array_length(bids_json) BETWEEN 1 AND 10),
    CHECK (jsonb_typeof(asks_json) = 'array' AND jsonb_array_length(asks_json) BETWEEN 1 AND 10)
);

CREATE INDEX orderbook_latest_received_at_idx
    ON orderbook_latest (received_at_ms DESC);
