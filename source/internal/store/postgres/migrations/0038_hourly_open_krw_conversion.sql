ALTER TABLE hourly_open_turnover
 ADD COLUMN IF NOT EXISTS fx_open_krw numeric,
 ADD COLUMN IF NOT EXISTS fx_market text,
 ADD COLUMN IF NOT EXISTS open_price_krw numeric,
 ADD COLUMN IF NOT EXISTS quote_turnover_krw numeric;
COMMENT ON COLUMN hourly_open_turnover.fx_open_krw IS 'Same exchange and exact hour BTC/KRW or USDT/KRW official opening price. KRW=1. No current FX fallback.';
COMMENT ON COLUMN hourly_open_turnover.quote_turnover_krw IS 'Original quote turnover multiplied by same-hour opening FX. BTC/USDT results are estimates, not exact trade-time KRW turnover.';
CREATE OR REPLACE VIEW hourly_open_turnover_with_conversion AS
SELECT h.*,
 CASE WHEN quote='KRW' AND fx_open_krw=1 THEN 'NATIVE_KRW'
      WHEN quote='KRW' THEN 'PENDING_NATIVE_KRW'
      WHEN quote IN ('BTC','USDT') AND fx_open_krw IS NOT NULL THEN 'SAME_EXCHANGE_HOURLY_OPEN_ESTIMATE'
      WHEN quote IN ('BTC','USDT') THEN 'MISSING_SAME_HOUR_FX_OPEN'
      ELSE 'UNSUPPORTED_QUOTE' END AS conversion_method,
 CASE WHEN quote_turnover IS NULL THEN 'SOURCE_TURNOVER_UNAVAILABLE'
      WHEN quote_turnover_krw IS NULL THEN 'CONVERSION_UNAVAILABLE'
      WHEN quote='KRW' THEN 'EXACT_QUOTE_TURNOVER'
      ELSE 'ESTIMATED_AT_HOURLY_OPEN' END AS turnover_krw_status
FROM hourly_open_turnover h;
