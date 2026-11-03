CREATE TABLE IF NOT EXISTS official_share_event_flow_evidence (
    event_id TEXT NOT NULL REFERENCES official_share_events(event_id) ON DELETE CASCADE,
    snapshot_kind TEXT NOT NULL,
    official_as_of_ms BIGINT NOT NULL,
    flow_as_of_ms BIGINT NOT NULL,
    lag_ms BIGINT NOT NULL,
    payload_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(event_id, snapshot_kind),
    CHECK (snapshot_kind IN ('CONFIRMED', 'EXTREME', 'ENDED')),
    CHECK (official_as_of_ms > 0),
    CHECK (flow_as_of_ms > 0),
    CHECK (lag_ms >= 0)
);

CREATE INDEX IF NOT EXISTS official_share_event_flow_evidence_time_idx
    ON official_share_event_flow_evidence(flow_as_of_ms DESC);
