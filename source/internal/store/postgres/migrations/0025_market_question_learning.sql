CREATE TABLE IF NOT EXISTS market_question_attempts (
    request_id TEXT PRIMARY KEY,
    asked_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    question_text TEXT NOT NULL,
    question_sha256 TEXT NOT NULL,
    history_turn_count SMALLINT NOT NULL DEFAULT 0,
    catalog_version TEXT NOT NULL,
    planner_plan_json JSONB,
    planner_query_json JSONB,
    planner_error TEXT NOT NULL DEFAULT '',
    plan_validation_error TEXT NOT NULL DEFAULT '',
    deterministic_query_json JSONB,
    deterministic_error TEXT NOT NULL DEFAULT '',
    planner_parser_agree BOOLEAN,
    accepted_query_json JSONB,
    plan_source TEXT NOT NULL DEFAULT '',
    evidence_layer TEXT NOT NULL DEFAULT '',
    quality_status TEXT NOT NULL DEFAULT '',
    provider_source TEXT NOT NULL DEFAULT '',
    provider_model TEXT NOT NULL DEFAULT '',
    provider_fallback BOOLEAN NOT NULL DEFAULT FALSE,
    answer_guard_status TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL,
    error_kind TEXT NOT NULL DEFAULT '',
    latency_ms BIGINT NOT NULL,
    answer_policy_version TEXT NOT NULL,
    CHECK (request_id ~ '^[0-9a-f]{32}$'),
    CHECK (char_length(question_text) BETWEEN 2 AND 500),
    CHECK (question_sha256 ~ '^[0-9a-f]{64}$'),
    CHECK (history_turn_count BETWEEN 0 AND 15),
    CHECK (latency_ms >= 0),
    CHECK (expires_at > asked_at),
    CHECK (outcome IN ('COMPLETED', 'RESTRICTED', 'INVALID', 'FAILED'))
);

CREATE INDEX IF NOT EXISTS market_question_attempts_asked_at_idx
    ON market_question_attempts (asked_at DESC);
CREATE INDEX IF NOT EXISTS market_question_attempts_review_idx
    ON market_question_attempts (outcome, plan_source, answer_guard_status, asked_at DESC);
CREATE INDEX IF NOT EXISTS market_question_attempts_expiry_idx
    ON market_question_attempts (expires_at);

CREATE TABLE IF NOT EXISTS market_question_feedback (
    request_id TEXT PRIMARY KEY REFERENCES market_question_attempts(request_id) ON DELETE CASCADE,
    verdict TEXT NOT NULL,
    corrected_question TEXT,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (verdict IN ('CORRECT', 'WRONG_PERIOD', 'WRONG_TARGET', 'WRONG_METRIC', 'WRONG_ANALYSIS')),
    CHECK (
        (verdict = 'CORRECT' AND corrected_question IS NULL) OR
        (verdict <> 'CORRECT' AND corrected_question IS NOT NULL AND char_length(corrected_question) BETWEEN 2 AND 500)
    )
);

CREATE INDEX IF NOT EXISTS market_question_feedback_verdict_idx
    ON market_question_feedback (verdict, received_at DESC);
