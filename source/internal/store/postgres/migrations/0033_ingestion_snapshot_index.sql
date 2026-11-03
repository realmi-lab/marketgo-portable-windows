-- 0032 belongs to a retained production migration outside this checkout.
-- On a populated journal, prebuild this exact index CONCURRENTLY outside the
-- migration transaction. An interrupted/incorrect index must never be accepted.
DO $$
DECLARE
    actual_index oid := to_regclass('ingestion_events_snapshot_kind_cursor_idx');
    expected_index oid;
BEGIN
    IF actual_index IS NULL THEN
        -- Freeze emptiness before the fast non-concurrent build. An active
        -- collector must fail this path immediately, never queue a long lock.
        LOCK TABLE ingestion_events IN SHARE MODE NOWAIT;
        IF EXISTS (SELECT 1 FROM ingestion_events LIMIT 1) THEN
            RAISE EXCEPTION 'nonempty ingestion_events requires a valid concurrent prebuild of ingestion_events_snapshot_kind_cursor_idx';
        END IF;
        CREATE INDEX ingestion_events_snapshot_kind_cursor_idx
            ON ingestion_events (kind, cursor DESC)
            WHERE kind IN ('registry','health','official');
        actual_index := to_regclass('ingestion_events_snapshot_kind_cursor_idx');
    END IF;

    -- Let PostgreSQL normalize the expected definition, rather than accepting
    -- merely the name or relying on textual whitespace of a supplied index.
    CREATE TEMP TABLE snapshot_index_contract (kind text, payload jsonb, cursor bigint) ON COMMIT DROP;
    CREATE INDEX snapshot_index_contract_expected
        ON snapshot_index_contract (kind, cursor DESC)
        WHERE kind IN ('registry','health','official');
    expected_index := to_regclass('pg_temp.snapshot_index_contract_expected');
    IF NOT EXISTS (
        SELECT 1 FROM pg_index a JOIN pg_class ac ON ac.oid=a.indexrelid
        CROSS JOIN pg_index e JOIN pg_class ec ON ec.oid=e.indexrelid
        WHERE a.indexrelid=actual_index AND e.indexrelid=expected_index
          AND a.indrelid='ingestion_events'::regclass AND a.indisvalid AND a.indisready
          AND NOT a.indisunique AND NOT a.indisexclusion AND a.indnatts=2 AND a.indnkeyatts=2
          AND ac.relam=ec.relam AND a.indoption=e.indoption
          AND a.indclass=e.indclass AND a.indcollation=e.indcollation
          AND pg_get_indexdef(a.indexrelid,1,true)=pg_get_indexdef(e.indexrelid,1,true)
          AND pg_get_indexdef(a.indexrelid,2,true)=pg_get_indexdef(e.indexrelid,2,true)
          AND pg_get_expr(a.indpred,a.indrelid)=pg_get_expr(e.indpred,e.indrelid)
    ) THEN
        RAISE EXCEPTION 'ingestion_events_snapshot_kind_cursor_idx must be valid, ready and match the exact snapshot index definition';
    END IF;
END $$;
