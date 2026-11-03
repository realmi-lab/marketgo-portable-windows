-- Optional retained answers. This table never reconstructs an unrecorded past
-- snapshot, and stores no raw user question or generated explanation.
CREATE TABLE market_question_evidence_bundles (
    bundle_id TEXT PRIMARY KEY CHECK (bundle_id ~ '^bundle_[0-9a-f]{32}$'),
    stored_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    content_sha256 TEXT NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    bundle_payload BYTEA NOT NULL CHECK (octet_length(bundle_payload) BETWEEN 2 AND 2097152),
    CHECK (expires_at > stored_at)
);

CREATE INDEX market_question_evidence_bundles_expiry_idx
    ON market_question_evidence_bundles (expires_at);

CREATE FUNCTION reject_market_question_evidence_bundle_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'retained market question evidence is immutable';
END;
$$;

CREATE TRIGGER market_question_evidence_bundles_no_update
    BEFORE UPDATE ON market_question_evidence_bundles
    FOR EACH ROW EXECUTE FUNCTION reject_market_question_evidence_bundle_update();
