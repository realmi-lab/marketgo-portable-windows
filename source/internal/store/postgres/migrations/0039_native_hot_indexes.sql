-- Same-slot native observations update received_at_ns on every accepted
-- successor. Ordinary indexes on that column prevent PostgreSQL HOT updates
-- and multiply WAL across every index. Keep receipt-range pruning through a
-- summarizing BRIN index and keep the sparse repair predicate on immutable keys.
--
-- A populated table must prepare and remove indexes outside this transactional
-- migration runner, one command at a time:
-- CREATE INDEX CONCURRENTLY native_market_received_brin
--   ON native_ws_candle_observations USING brin(received_at_ns)
--   WITH (pages_per_range=16);
-- CREATE INDEX CONCURRENTLY native_market_repair_hot
--   ON native_ws_candle_observations
--   (exchange_name,market,interval_sec,source_kind,observation_slot)
--   WHERE normalization_error<>'';
-- DROP INDEX CONCURRENTLY native_ws_candle_lookup;
-- DROP INDEX CONCURRENTLY native_market_received_time;
-- DROP INDEX CONCURRENTLY native_market_source_clock;
-- DROP INDEX CONCURRENTLY native_market_repair;
-- After concurrent preparation, execute this file in its own transaction while
-- holding the migration advisory lock, then record version 0039 with this
-- file's exact checksum. The full migration runner is also valid when all
-- preceding migrations are intentionally eligible to run.

SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '20s';

DO $migration$
DECLARE
    needs_change boolean;
    received_index oid;
    repair_index oid;
    expected_received oid;
    expected_repair oid;
    target_table regclass := 'native_ws_candle_observations'::regclass;
    candidate_name text;
    candidate_index oid;
BEGIN
    -- Every unqualified DROP below must resolve to an index owned by the native
    -- observation table. Fail before DDL if search_path exposes a same-named
    -- object from another relation or schema.
    FOREACH candidate_name IN ARRAY ARRAY[
        'native_ws_candle_lookup',
        'native_market_received_time',
        'native_market_source_clock',
        'native_market_repair',
        'native_market_received_brin',
        'native_market_repair_hot'
    ] LOOP
        candidate_index := to_regclass(candidate_name);
        IF candidate_index IS NOT NULL AND NOT EXISTS (
            SELECT 1
            FROM pg_index
            WHERE indexrelid=candidate_index
              AND indrelid=target_table
        ) THEN
            RAISE EXCEPTION '0039 index name % does not belong to native_ws_candle_observations', candidate_name;
        END IF;
    END LOOP;

    needs_change := to_regclass('native_market_received_brin') IS NULL
        OR to_regclass('native_market_repair_hot') IS NULL
        OR to_regclass('native_ws_candle_lookup') IS NOT NULL
        OR to_regclass('native_market_received_time') IS NOT NULL
        OR to_regclass('native_market_source_clock') IS NOT NULL
        OR to_regclass('native_market_repair') IS NOT NULL;

    IF needs_change THEN
        -- Only an empty first installation may do ordinary index DDL here.
        -- Freeze emptiness before changing the legacy definitions.
        LOCK TABLE native_ws_candle_observations IN ACCESS EXCLUSIVE MODE NOWAIT;
        IF EXISTS (SELECT 1 FROM native_ws_candle_observations LIMIT 1) THEN
            RAISE EXCEPTION 'nonempty native_ws_candle_observations requires concurrent 0039 index preparation and removal';
        END IF;
        DROP INDEX IF EXISTS native_ws_candle_lookup;
        DROP INDEX IF EXISTS native_market_received_time;
        DROP INDEX IF EXISTS native_market_source_clock;
        DROP INDEX IF EXISTS native_market_repair;
        DROP INDEX IF EXISTS native_market_received_brin;
        DROP INDEX IF EXISTS native_market_repair_hot;
        CREATE INDEX native_market_received_brin
            ON native_ws_candle_observations USING brin(received_at_ns)
            WITH (pages_per_range=16);
        CREATE INDEX native_market_repair_hot
            ON native_ws_candle_observations
            (exchange_name,market,interval_sec,source_kind,observation_slot)
            WHERE normalization_error<>'';
    END IF;

    received_index := to_regclass('native_market_received_brin');
    repair_index := to_regclass('native_market_repair_hot');
    CREATE TEMP TABLE native_hot_index_contract (
        exchange_name text,
        market text,
        interval_sec bigint,
        source_kind text,
        observation_slot text,
        received_at_ns bigint,
        normalization_error text
    ) ON COMMIT DROP;
    CREATE INDEX native_received_expected ON native_hot_index_contract
        USING brin(received_at_ns) WITH (pages_per_range=16);
    CREATE INDEX native_repair_expected ON native_hot_index_contract
        (exchange_name,market,interval_sec,source_kind,observation_slot)
        WHERE normalization_error<>'';
    expected_received := to_regclass('pg_temp.native_received_expected');
    expected_repair := to_regclass('pg_temp.native_repair_expected');

    IF received_index IS NULL OR NOT EXISTS (
        SELECT 1
        FROM pg_index actual
        JOIN pg_class actual_class ON actual_class.oid=actual.indexrelid
        CROSS JOIN pg_index expected
        JOIN pg_class expected_class ON expected_class.oid=expected.indexrelid
        WHERE actual.indexrelid=received_index
          AND expected.indexrelid=expected_received
          AND actual.indrelid='native_ws_candle_observations'::regclass
          AND actual.indisvalid AND actual.indisready
          AND NOT actual.indisunique AND NOT actual.indisexclusion
          AND actual.indnatts=expected.indnatts
          AND actual.indnkeyatts=expected.indnkeyatts
          AND actual_class.relam=expected_class.relam
          AND actual_class.reloptions=expected_class.reloptions
          AND pg_get_indexdef(actual.indexrelid,1,true)=pg_get_indexdef(expected.indexrelid,1,true)
          AND pg_get_expr(actual.indpred,actual.indrelid) IS NULL
    ) THEN
        RAISE EXCEPTION 'native_market_received_brin must be valid, ready and match the exact 0039 definition';
    END IF;

    IF repair_index IS NULL OR NOT EXISTS (
        SELECT 1
        FROM pg_index actual
        JOIN pg_class actual_class ON actual_class.oid=actual.indexrelid
        CROSS JOIN pg_index expected
        JOIN pg_class expected_class ON expected_class.oid=expected.indexrelid
        WHERE actual.indexrelid=repair_index
          AND expected.indexrelid=expected_repair
          AND actual.indrelid='native_ws_candle_observations'::regclass
          AND actual.indisvalid AND actual.indisready
          AND NOT actual.indisunique AND NOT actual.indisexclusion
          AND actual.indnatts=expected.indnatts
          AND actual.indnkeyatts=expected.indnkeyatts
          AND actual_class.relam=expected_class.relam
          AND pg_get_indexdef(actual.indexrelid,1,true)=pg_get_indexdef(expected.indexrelid,1,true)
          AND pg_get_indexdef(actual.indexrelid,2,true)=pg_get_indexdef(expected.indexrelid,2,true)
          AND pg_get_indexdef(actual.indexrelid,3,true)=pg_get_indexdef(expected.indexrelid,3,true)
          AND pg_get_indexdef(actual.indexrelid,4,true)=pg_get_indexdef(expected.indexrelid,4,true)
          AND pg_get_indexdef(actual.indexrelid,5,true)=pg_get_indexdef(expected.indexrelid,5,true)
          AND pg_get_expr(actual.indpred,actual.indrelid)=pg_get_expr(expected.indpred,expected.indrelid)
    ) THEN
        RAISE EXCEPTION 'native_market_repair_hot must be valid, ready and match the exact 0039 definition';
    END IF;

    IF to_regclass('native_ws_candle_lookup') IS NOT NULL
        OR to_regclass('native_market_received_time') IS NOT NULL
        OR to_regclass('native_market_source_clock') IS NOT NULL
        OR to_regclass('native_market_repair') IS NOT NULL THEN
        RAISE EXCEPTION 'legacy native indexes must be removed before recording 0039';
    END IF;
END $migration$;
