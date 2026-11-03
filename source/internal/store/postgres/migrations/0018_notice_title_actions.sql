ALTER TABLE exchange_notices
    ADD COLUMN IF NOT EXISTS title_actions_json TEXT NOT NULL DEFAULT '[]';
