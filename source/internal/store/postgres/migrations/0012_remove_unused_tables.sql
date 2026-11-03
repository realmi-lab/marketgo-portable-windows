-- These tables have no runtime readers. The ten-minute ticker rollup became
-- redundant when one-minute official ticker history moved to indefinite
-- retention; notice_analysis_metadata was never used by the Go runtime.
DROP TABLE IF EXISTS official_ticker_10m_markets;
DROP TABLE IF EXISTS official_ticker_10m_samples;
DROP TABLE IF EXISTS notice_analysis_metadata;
