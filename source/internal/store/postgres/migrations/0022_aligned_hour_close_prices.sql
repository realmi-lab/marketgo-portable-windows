ALTER TABLE official_aligned_1h_markets
    ADD COLUMN IF NOT EXISTS close_price_krw DOUBLE PRECISION;
