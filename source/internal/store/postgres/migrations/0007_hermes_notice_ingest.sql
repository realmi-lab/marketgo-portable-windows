ALTER TABLE exchange_notices
    ADD COLUMN IF NOT EXISTS original_language TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS original_title TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS original_body_text TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS translated_title_ko TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS translated_body_text_ko TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS translation_status TEXT NOT NULL DEFAULT 'NOT_REQUIRED',
    ADD COLUMN IF NOT EXISTS importance TEXT NOT NULL DEFAULT 'NORMAL',
    ADD COLUMN IF NOT EXISTS analysis_provider TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS analysis_model TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS reasoning_effort TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS escalation_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS analysis_policy_version TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS hermes_notice_revisions (
    id BIGSERIAL PRIMARY KEY,
    exchange_name TEXT NOT NULL,
    notice_id TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    original_language TEXT NOT NULL,
    original_title TEXT NOT NULL,
    original_body_text TEXT NOT NULL,
    image_urls_json TEXT NOT NULL DEFAULT '[]',
    source_payload JSONB NOT NULL,
    fetched_ts_ms BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (exchange_name, notice_id, content_hash)
);

CREATE INDEX IF NOT EXISTS hermes_notice_revisions_notice_idx
    ON hermes_notice_revisions(exchange_name, notice_id, id DESC);

CREATE TABLE IF NOT EXISTS hermes_notice_analysis_runs (
    id BIGSERIAL PRIMARY KEY,
    batch_id TEXT NOT NULL,
    exchange_name TEXT NOT NULL,
    notice_id TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    status TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    reasoning_effort TEXT NOT NULL DEFAULT '',
    escalation_reason TEXT NOT NULL DEFAULT '',
    policy_version TEXT NOT NULL DEFAULT '',
    result_payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (batch_id, exchange_name, notice_id)
);

CREATE INDEX IF NOT EXISTS hermes_notice_analysis_runs_notice_idx
    ON hermes_notice_analysis_runs(exchange_name, notice_id, created_at DESC);
