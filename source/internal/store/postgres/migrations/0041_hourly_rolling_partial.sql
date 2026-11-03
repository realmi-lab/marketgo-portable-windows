-- Keep available hourly sums even when a rolling window is incomplete or failed.
-- Raw hourly rows and the OK/requires-24-hours constraint are unchanged.
ALTER TABLE hourly_rolling_turnover_latest
 DROP CONSTRAINT IF EXISTS hourly_rolling_turnover_latest_check;
