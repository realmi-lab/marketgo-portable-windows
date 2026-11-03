-- The full realtime-flow JSON remains the immutable diagnostic/evidence row.
-- These compact tables are the hot analytical projection used by the rolling
-- 24-hour summary.  They preserve the exact ten-second window and attribution;
-- primary-window impact tables are intentionally not reused because their
-- window may be 30 or 60 seconds.
CREATE TABLE IF NOT EXISTS realtime_flow_10s_samples (
    as_of_ms BIGINT PRIMARY KEY
        REFERENCES realtime_flow_observations(as_of_ms) ON DELETE CASCADE,
    contract_version TEXT NOT NULL,
    status TEXT NOT NULL,
    frame_complete BOOLEAN NOT NULL,
    frame_status TEXT NOT NULL,
    window_available BOOLEAN NOT NULL,
    window_status TEXT NOT NULL,
    current_share_pct DOUBLE PRECISION,
    previous_share_pct DOUBLE PRECISION,
    share_delta_pp DOUBLE PRECISION,
    attribution_available BOOLEAN NOT NULL,
    residual_contribution_pp DOUBLE PRECISION,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS realtime_flow_10s_asset_impacts (
    as_of_ms BIGINT NOT NULL
        REFERENCES realtime_flow_10s_samples(as_of_ms) ON DELETE CASCADE,
    asset TEXT NOT NULL,
    rank_no INTEGER NOT NULL,
    contribution_pp DOUBLE PRECISION NOT NULL,
    bithumb_volume_change_krw DOUBLE PRECISION NOT NULL,
    competitor_volume_change_krw DOUBLE PRECISION NOT NULL,
    PRIMARY KEY(as_of_ms, rank_no)
);

CREATE INDEX IF NOT EXISTS realtime_flow_10s_asset_impacts_asset_time_idx
    ON realtime_flow_10s_asset_impacts(asset, as_of_ms DESC);

CREATE TABLE IF NOT EXISTS realtime_flow_10s_exchange_impacts (
    as_of_ms BIGINT NOT NULL
        REFERENCES realtime_flow_10s_samples(as_of_ms) ON DELETE CASCADE,
    exchange_name TEXT NOT NULL,
    rank_no INTEGER NOT NULL,
    contribution_pp DOUBLE PRECISION NOT NULL,
    PRIMARY KEY(as_of_ms, rank_no)
);

CREATE INDEX IF NOT EXISTS realtime_flow_10s_exchange_impacts_exchange_time_idx
    ON realtime_flow_10s_exchange_impacts(exchange_name, as_of_ms DESC);

-- Backfill the compact projection once from the retained evidence.  Future
-- writes maintain both representations in the same transaction.
INSERT INTO realtime_flow_10s_samples (
    as_of_ms, contract_version, status, frame_complete, frame_status,
    window_available, window_status, current_share_pct, previous_share_pct,
    share_delta_pp, attribution_available, residual_contribution_pp
)
SELECT
    observation.as_of_ms,
    observation.contract_version,
    observation.status,
    COALESCE((observation.payload_json #>> '{data_quality,complete_frame}')::BOOLEAN, FALSE),
    COALESCE(observation.payload_json #>> '{data_quality,frame_status}', ''),
    ten_second.window_json IS NOT NULL,
    COALESCE(ten_second.window_json->>'status', ''),
    NULLIF(ten_second.window_json->>'current_share_pct', '')::DOUBLE PRECISION,
    NULLIF(ten_second.window_json->>'previous_share_pct', '')::DOUBLE PRECISION,
    NULLIF(ten_second.window_json->>'share_delta_pp', '')::DOUBLE PRECISION,
    COALESCE(jsonb_typeof(observation.payload_json->'ten_second_attribution') = 'object', FALSE),
    NULLIF(observation.payload_json #>> '{ten_second_attribution,residual_contribution_pp}', '')::DOUBLE PRECISION
FROM realtime_flow_observations observation
LEFT JOIN LATERAL (
    SELECT item AS window_json
    FROM jsonb_array_elements(
        CASE
            WHEN jsonb_typeof(observation.payload_json->'windows') = 'array'
                THEN observation.payload_json->'windows'
            ELSE '[]'::JSONB
        END
    ) item
    WHERE item->>'duration_sec' = '10'
    LIMIT 1
) ten_second ON TRUE
WHERE observation.contract_version = 'REALTIME_FLOW_V2'
ON CONFLICT(as_of_ms) DO UPDATE SET
    contract_version=EXCLUDED.contract_version,
    status=EXCLUDED.status,
    frame_complete=EXCLUDED.frame_complete,
    frame_status=EXCLUDED.frame_status,
    window_available=EXCLUDED.window_available,
    window_status=EXCLUDED.window_status,
    current_share_pct=EXCLUDED.current_share_pct,
    previous_share_pct=EXCLUDED.previous_share_pct,
    share_delta_pp=EXCLUDED.share_delta_pp,
    attribution_available=EXCLUDED.attribution_available,
    residual_contribution_pp=EXCLUDED.residual_contribution_pp;

INSERT INTO realtime_flow_10s_asset_impacts (
    as_of_ms, asset, rank_no, contribution_pp,
    bithumb_volume_change_krw, competitor_volume_change_krw
)
SELECT
    observation.as_of_ms,
    driver.value->>'asset',
    driver.ordinality::INTEGER,
    COALESCE(NULLIF(driver.value->>'contribution_pp', '')::DOUBLE PRECISION, 0),
    COALESCE(NULLIF(driver.value->>'bithumb_volume_change_krw', '')::DOUBLE PRECISION, 0),
    COALESCE(NULLIF(driver.value->>'competitor_volume_change_krw', '')::DOUBLE PRECISION, 0)
FROM realtime_flow_observations observation
JOIN realtime_flow_10s_samples sample ON sample.as_of_ms=observation.as_of_ms
CROSS JOIN LATERAL jsonb_array_elements(
    CASE
        WHEN jsonb_typeof(observation.payload_json #> '{ten_second_attribution,drivers}') = 'array'
            THEN observation.payload_json #> '{ten_second_attribution,drivers}'
        ELSE '[]'::JSONB
    END
) WITH ORDINALITY driver(value, ordinality)
WHERE COALESCE(driver.value->>'asset', '') <> ''
ON CONFLICT(as_of_ms, rank_no) DO UPDATE SET
    asset=EXCLUDED.asset,
    contribution_pp=EXCLUDED.contribution_pp,
    bithumb_volume_change_krw=EXCLUDED.bithumb_volume_change_krw,
    competitor_volume_change_krw=EXCLUDED.competitor_volume_change_krw;

INSERT INTO realtime_flow_10s_exchange_impacts (
    as_of_ms, exchange_name, rank_no, contribution_pp
)
SELECT
    observation.as_of_ms,
    driver.value->>'exchange',
    driver.ordinality::INTEGER,
    COALESCE(NULLIF(driver.value->>'contribution_pp', '')::DOUBLE PRECISION, 0)
FROM realtime_flow_observations observation
JOIN realtime_flow_10s_samples sample ON sample.as_of_ms=observation.as_of_ms
CROSS JOIN LATERAL jsonb_array_elements(
    CASE
        WHEN jsonb_typeof(observation.payload_json #> '{ten_second_attribution,exchange_drivers}') = 'array'
            THEN observation.payload_json #> '{ten_second_attribution,exchange_drivers}'
        ELSE '[]'::JSONB
    END
) WITH ORDINALITY driver(value, ordinality)
WHERE COALESCE(driver.value->>'exchange', '') <> ''
ON CONFLICT(as_of_ms, rank_no) DO UPDATE SET
    exchange_name=EXCLUDED.exchange_name,
    contribution_pp=EXCLUDED.contribution_pp;
