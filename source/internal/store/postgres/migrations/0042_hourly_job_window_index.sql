-- Date-selected index reads must not scan every historical collection job.
-- This optional access path changes neither eligible evidence nor stored rows.
-- On a populated table prepare the exact index with CREATE INDEX CONCURRENTLY
-- outside this transaction, then validate and record only this migration.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '20s';

DO $migration$
DECLARE
    target regclass := 'hourly_open_turnover_jobs'::regclass;
    actual_index oid := to_regclass('hourly_open_turnover_jobs_verified_window_idx');
    expected_index oid;
BEGIN
    IF actual_index IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM pg_index WHERE indexrelid=actual_index AND indrelid=target
    ) THEN
        RAISE EXCEPTION '0042 index name belongs to another relation';
    END IF;
    IF actual_index IS NULL THEN
        LOCK TABLE hourly_open_turnover_jobs IN ACCESS EXCLUSIVE MODE NOWAIT;
        IF EXISTS (SELECT 1 FROM hourly_open_turnover_jobs LIMIT 1) THEN
            RAISE EXCEPTION 'nonempty hourly_open_turnover_jobs requires concurrent 0042 index preparation';
        END IF;
        CREATE INDEX hourly_open_turnover_jobs_verified_window_idx
        ON hourly_open_turnover_jobs (end_sec,start_sec)
        WHERE exchange_name IN ('bithumb','upbit')
          AND ((status='EMPTY' AND row_count=0)
            OR (status='FETCHED' AND row_count BETWEEN 1 AND (end_sec-start_sec)/3600))
          AND checked_at IS NOT NULL AND length(response_sha256)=64
          AND end_sec-start_sec BETWEEN 3600 AND 720000;
        actual_index := to_regclass('hourly_open_turnover_jobs_verified_window_idx');
    END IF;

    CREATE TEMP TABLE hourly_job_index_contract (LIKE hourly_open_turnover_jobs) ON COMMIT DROP;
    CREATE INDEX hourly_job_window_expected ON hourly_job_index_contract (end_sec,start_sec)
    WHERE exchange_name IN ('bithumb','upbit')
      AND ((status='EMPTY' AND row_count=0)
        OR (status='FETCHED' AND row_count BETWEEN 1 AND (end_sec-start_sec)/3600))
      AND checked_at IS NOT NULL AND length(response_sha256)=64
      AND end_sec-start_sec BETWEEN 3600 AND 720000;
    expected_index := to_regclass('pg_temp.hourly_job_window_expected');
    IF NOT EXISTS (
        SELECT 1 FROM pg_index a JOIN pg_class ac ON ac.oid=a.indexrelid
        CROSS JOIN pg_index e JOIN pg_class ec ON ec.oid=e.indexrelid
        WHERE a.indexrelid=actual_index AND e.indexrelid=expected_index
          AND a.indrelid=target AND a.indisvalid AND a.indisready
          AND NOT a.indisunique AND NOT a.indisexclusion
          AND ac.relam=ec.relam AND ac.reloptions IS NOT DISTINCT FROM ec.reloptions
          AND a.indnatts=e.indnatts AND a.indnkeyatts=e.indnkeyatts
          AND a.indkey=e.indkey AND a.indclass=e.indclass
          AND a.indcollation=e.indcollation AND a.indoption=e.indoption
          AND pg_get_expr(a.indpred,a.indrelid)=pg_get_expr(e.indpred,e.indrelid)
    ) THEN
        RAISE EXCEPTION 'hourly job window index must match exact valid and ready 0042 definition';
    END IF;
END $migration$;
