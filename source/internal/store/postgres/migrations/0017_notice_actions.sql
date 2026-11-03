ALTER TABLE exchange_notices
    ADD COLUMN IF NOT EXISTS actions_json TEXT NOT NULL DEFAULT '[]';
