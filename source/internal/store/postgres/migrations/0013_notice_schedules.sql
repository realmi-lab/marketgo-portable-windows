CREATE TABLE IF NOT EXISTS exchange_notice_schedules (
    exchange_name TEXT NOT NULL,
    notice_id TEXT NOT NULL,
    schedule_no INTEGER NOT NULL,
    kind TEXT NOT NULL DEFAULT '',
    start_ts_ms BIGINT NOT NULL DEFAULT 0,
    end_ts_ms BIGINT NOT NULL DEFAULT 0,
    timezone TEXT NOT NULL DEFAULT '',
    precision_value TEXT NOT NULL DEFAULT '',
    evidence TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (exchange_name, notice_id, schedule_no),
    FOREIGN KEY (exchange_name, notice_id)
        REFERENCES exchange_notices(exchange_name, notice_id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS exchange_notice_schedules_start_idx
    ON exchange_notice_schedules(exchange_name, start_ts_ms);

CREATE INDEX IF NOT EXISTS exchange_notice_schedules_end_idx
    ON exchange_notice_schedules(exchange_name, end_ts_ms)
    WHERE end_ts_ms > 0;

INSERT INTO exchange_notice_schedules(
    exchange_name, notice_id, schedule_no, kind, start_ts_ms, end_ts_ms,
    timezone, precision_value, evidence
)
SELECT n.exchange_name, n.notice_id, (entry.ordinality - 1)::INTEGER,
       COALESCE(entry.value->>'kind', ''),
       COALESCE((entry.value->>'start_ts_ms')::BIGINT, 0),
       COALESCE((entry.value->>'end_ts_ms')::BIGINT, 0),
       COALESCE(entry.value->>'timezone', ''),
       COALESCE(entry.value->>'precision', ''),
       COALESCE(entry.value->>'evidence', '')
FROM exchange_notices AS n
CROSS JOIN LATERAL jsonb_array_elements(n.schedules_json::JSONB)
    WITH ORDINALITY AS entry(value, ordinality)
ON CONFLICT (exchange_name, notice_id, schedule_no) DO UPDATE SET
    kind=EXCLUDED.kind,
    start_ts_ms=EXCLUDED.start_ts_ms,
    end_ts_ms=EXCLUDED.end_ts_ms,
    timezone=EXCLUDED.timezone,
    precision_value=EXCLUDED.precision_value,
    evidence=EXCLUDED.evidence;
