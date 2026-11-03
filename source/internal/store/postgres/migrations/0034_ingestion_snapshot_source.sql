-- Additive metadata cache only. Never backfill under this DDL transaction.
SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '20s';
DO $migration$
DECLARE
    table_owner oid;
    table_namespace oid;
    function_oid oid;
    trigger_oid oid;
    expected_trigger oid;
    source_body text := $body$BEGIN
    IF NEW.kind IN ('registry','health','official') THEN
        NEW.snapshot_source := COALESCE(NEW.payload->>'Source','');
    ELSE
        NEW.snapshot_source := NULL;
    END IF;
    RETURN NEW;
END$body$;
BEGIN
    -- Column and correcting trigger become visible atomically, including to
    -- existing collectors that omit the cache column from their INSERT.
    LOCK TABLE ingestion_events IN ACCESS EXCLUSIVE MODE;
    SELECT relowner,relnamespace INTO table_owner,table_namespace FROM pg_class WHERE oid='ingestion_events'::regclass;
    IF NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='ingestion_events'::regclass AND attname='snapshot_source' AND NOT attisdropped) THEN
        ALTER TABLE ingestion_events ADD COLUMN snapshot_source text;
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_attribute a WHERE a.attrelid='ingestion_events'::regclass
          AND a.attname='snapshot_source' AND NOT a.attisdropped
          AND a.atttypid='text'::regtype AND a.atttypmod=-1
          AND NOT a.attnotnull AND NOT a.atthasdef AND a.attgenerated='' AND a.attidentity=''
          AND a.attcollation=(SELECT typcollation FROM pg_type WHERE oid='text'::regtype)
    ) THEN RAISE EXCEPTION 'snapshot_source must be an ordinary nullable text column with default text collation and no default'; END IF;

    function_oid := to_regprocedure('ingestion_snapshot_source_cache()');
    IF function_oid IS NULL THEN
        EXECUTE format('CREATE FUNCTION ingestion_snapshot_source_cache() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS %L',source_body);
        function_oid := to_regprocedure('ingestion_snapshot_source_cache()');
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang
        WHERE p.oid=function_oid AND p.proowner=table_owner AND p.pronamespace=table_namespace AND l.lanname='plpgsql'
          AND p.prorettype='trigger'::regtype AND p.pronargs=0 AND p.prokind='f'
          AND NOT p.prosecdef AND NOT p.proisstrict AND NOT p.proleakproof
          AND p.provolatile='v' AND p.proparallel='u' AND p.proconfig IS NULL AND p.prosrc=source_body
    ) THEN RAISE EXCEPTION 'ingestion_snapshot_source_cache function does not match the exact invoker contract'; END IF;

    SELECT oid INTO trigger_oid FROM pg_trigger WHERE tgrelid='ingestion_events'::regclass AND tgname='ingestion_snapshot_source_cache';
    IF trigger_oid IS NULL THEN
        CREATE TRIGGER ingestion_snapshot_source_cache
        BEFORE INSERT OR UPDATE OF kind,payload,snapshot_source ON ingestion_events
        FOR EACH ROW WHEN (NEW.kind IN ('registry','health','official') OR NEW.snapshot_source IS NOT NULL)
        EXECUTE FUNCTION ingestion_snapshot_source_cache();
        SELECT oid INTO trigger_oid FROM pg_trigger WHERE tgrelid='ingestion_events'::regclass AND tgname='ingestion_snapshot_source_cache';
    END IF;
    -- PostgreSQL normalizes the expected trigger including UPDATE columns and
    -- WHEN expression. LIKE is schema-only: no original journal data is copied.
    CREATE TEMP TABLE snapshot_source_contract (LIKE ingestion_events) ON COMMIT DROP;
    CREATE TRIGGER ingestion_snapshot_source_cache
    BEFORE INSERT OR UPDATE OF kind,payload,snapshot_source ON snapshot_source_contract
    FOR EACH ROW WHEN (NEW.kind IN ('registry','health','official') OR NEW.snapshot_source IS NOT NULL)
    EXECUTE FUNCTION ingestion_snapshot_source_cache();
    SELECT oid INTO expected_trigger FROM pg_trigger WHERE tgrelid='pg_temp.snapshot_source_contract'::regclass AND tgname='ingestion_snapshot_source_cache';
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger a WHERE a.oid=trigger_oid AND a.tgenabled='O'
          AND NOT a.tgisinternal AND NOT a.tgdeferrable AND NOT a.tginitdeferred
          AND a.tgfoid=function_oid AND a.tgconstraint=0
          AND regexp_replace(pg_get_triggerdef(a.oid),' ON [^ ]+ ',' ON <table> ')
              =regexp_replace(pg_get_triggerdef(expected_trigger),' ON [^ ]+ ',' ON <table> ')
    ) THEN RAISE EXCEPTION 'ingestion_snapshot_source_cache trigger must be enabled and match the exact metadata correction contract'; END IF;
END $migration$;
