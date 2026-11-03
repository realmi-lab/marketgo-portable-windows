-- Keep the verified-empty evidence contract identical for date-selected and
-- rolling hourly readers while each reader retains its own time window.
SET LOCAL lock_timeout = '1s';
SET LOCAL statement_timeout = '20s';

CREATE VIEW hourly_open_turnover_verified_jobs AS
SELECT exchange_name,market,base,quote,start_sec,end_sec
FROM hourly_open_turnover_jobs
WHERE exchange_name IN ('bithumb','upbit')
  AND ((status='EMPTY' AND row_count=0)
    OR (status='FETCHED' AND row_count BETWEEN 1 AND (end_sec-start_sec)/3600))
  AND checked_at IS NOT NULL AND length(response_sha256)=64
  AND end_sec-start_sec BETWEEN 3600 AND 720000;

REVOKE ALL ON hourly_open_turnover_verified_jobs FROM PUBLIC;
DO $permissions$
BEGIN
    IF to_regrole('market_share_go_app') IS NOT NULL THEN
        GRANT SELECT ON hourly_open_turnover_verified_jobs TO market_share_go_app;
    END IF;
END $permissions$;
