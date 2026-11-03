-- Additive notice pipeline. No source is switched from the legacy collector.
-- Objects, observations, revisions and derivations have no retention deletion.
CREATE TABLE notice_v2_sources (
    exchange text PRIMARY KEY,
    enabled boolean NOT NULL DEFAULT false,
    collector_mode text NOT NULL DEFAULT 'LEGACY' CHECK (collector_mode IN ('LEGACY','V2')),
    read_mode text NOT NULL DEFAULT 'LEGACY' CHECK (read_mode IN ('LEGACY','SHADOW','V2')),
    source_generation bigint NOT NULL DEFAULT 1 CHECK (source_generation > 0),
    next_scan_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE notice_v2_objects (
    object_id uuid PRIMARY KEY,
    sha256 text NOT NULL CHECK (length(sha256)=64),
    byte_length bigint NOT NULL CHECK (byte_length BETWEEN 1 AND 25165824),
    media_type text NOT NULL,
    bytes bytea NOT NULL CHECK (octet_length(bytes)=byte_length),
    created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO notice_v2_sources(exchange) VALUES ('upbit'),('bithumb'),('coinone'),('korbit'),('gopax');
CREATE INDEX notice_v2_objects_digest ON notice_v2_objects(sha256,byte_length);
CREATE TABLE notice_v2_heads (
    exchange text NOT NULL REFERENCES notice_v2_sources(exchange) ON DELETE RESTRICT,
    notice_id text NOT NULL,
    current_revision_id uuid,
    current_fact_set_id uuid,
    current_summary_id uuid,
    evidence_generation bigint NOT NULL DEFAULT 0,
    projection_generation bigint NOT NULL DEFAULT 0,
    budget_day_kst date,
    spent_calls integer NOT NULL DEFAULT 0 CHECK(spent_calls>=0),
    reserved_calls integer NOT NULL DEFAULT 0 CHECK(reserved_calls>=0),
    PRIMARY KEY(exchange,notice_id)
);
CREATE TABLE notice_v2_revisions (
    revision_id uuid PRIMARY KEY,
    exchange text NOT NULL,
    notice_id text NOT NULL,
    manifest_digest text NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY(exchange,notice_id) REFERENCES notice_v2_heads(exchange,notice_id) ON DELETE RESTRICT,
    UNIQUE(exchange,notice_id,revision_id)
);
ALTER TABLE notice_v2_heads ADD FOREIGN KEY(exchange,notice_id,current_revision_id)
    REFERENCES notice_v2_revisions(exchange,notice_id,revision_id) ON DELETE RESTRICT;
CREATE INDEX notice_v2_revisions_history ON notice_v2_revisions(exchange,notice_id,created_at DESC);
CREATE TABLE notice_v2_jobs (
    job_id uuid PRIMARY KEY,
    work_key text NOT NULL UNIQUE,
    exchange text NOT NULL REFERENCES notice_v2_sources(exchange) ON DELETE RESTRICT,
    notice_id text NOT NULL DEFAULT '',
    stage text NOT NULL CHECK(stage IN ('LIST_PAGE','BODY','OCR','SEMANTIC_GENERATE','SEMANTIC_AUDIT')),
    input_revision_id uuid REFERENCES notice_v2_revisions(revision_id) ON DELETE RESTRICT,
    input_digest text NOT NULL,
    source_generation bigint NOT NULL,
    policy text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}',
    status text NOT NULL DEFAULT 'PENDING' CHECK(status IN ('PENDING','RUNNING','RETRY_WAIT','WAITING_BUDGET','WAITING_INPUT','SUCCEEDED','NEEDS_REVIEW','SUPERSEDED','CANCELLED')),
    reason text NOT NULL DEFAULT '',
    due_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    attempt_count integer NOT NULL DEFAULT 0 CHECK(attempt_count>=0),
    lease_token uuid,
    lease_until timestamptz,
    worker_id text,
    CHECK ((status='RUNNING') = (lease_token IS NOT NULL AND lease_until IS NOT NULL))
);
CREATE INDEX notice_v2_jobs_due ON notice_v2_jobs(stage,due_at,created_at) WHERE status IN ('PENDING','RETRY_WAIT','WAITING_BUDGET');
CREATE INDEX notice_v2_jobs_expired ON notice_v2_jobs(lease_until) WHERE status='RUNNING';
CREATE INDEX notice_v2_jobs_revision ON notice_v2_jobs(input_revision_id,created_at DESC);
CREATE INDEX notice_v2_jobs_notice ON notice_v2_jobs(exchange,notice_id,stage,status);
CREATE UNIQUE INDEX notice_v2_one_active_body ON notice_v2_jobs(exchange,notice_id)
    WHERE stage='BODY' AND status IN ('PENDING','RUNNING','RETRY_WAIT');
CREATE TABLE notice_v2_attempts (
    attempt_id uuid PRIMARY KEY,
    job_id uuid NOT NULL REFERENCES notice_v2_jobs(job_id) ON DELETE RESTRICT,
    attempt_no integer NOT NULL,
    lease_token uuid NOT NULL,
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    outcome text,
    UNIQUE(job_id,attempt_no)
);
CREATE TABLE notice_v2_receipts (
    idempotency_key text PRIMARY KEY,
    operation text NOT NULL,
    request_digest text NOT NULL,
    response jsonb NOT NULL,
    accepted_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE notice_v2_observations (
    observation_id uuid PRIMARY KEY,
    job_id uuid NOT NULL REFERENCES notice_v2_jobs(job_id) ON DELETE RESTRICT,
    object_id uuid NOT NULL REFERENCES notice_v2_objects(object_id) ON DELETE RESTRICT,
    source_generation bigint NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(job_id,object_id)
);
CREATE TABLE notice_v2_revision_media (
    revision_id uuid NOT NULL REFERENCES notice_v2_revisions(revision_id) ON DELETE RESTRICT,
    ordinal integer NOT NULL CHECK(ordinal>=0),
    original_url text NOT NULL,
    object_id uuid REFERENCES notice_v2_objects(object_id) ON DELETE RESTRICT,
    fetch_status text NOT NULL CHECK(fetch_status IN ('COMPLETE','PENDING','FAILED','POLICY_UNSUPPORTED')),
    error_code text NOT NULL DEFAULT '',
    PRIMARY KEY(revision_id,ordinal)
);
CREATE TABLE notice_v2_scans (
    scan_id uuid PRIMARY KEY REFERENCES notice_v2_jobs(job_id) ON DELETE RESTRICT,
    exchange text NOT NULL REFERENCES notice_v2_sources(exchange) ON DELETE RESTRICT,
    page_count integer NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'PARTIAL' CHECK(status IN ('PARTIAL','COMPLETE','FAILED')),
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE TABLE notice_v2_derivations (
    derivation_id uuid PRIMARY KEY,
    revision_id uuid NOT NULL REFERENCES notice_v2_revisions(revision_id) ON DELETE RESTRICT,
    kind text NOT NULL CHECK(kind IN ('CANDIDATE','AUDITED')),
    input_digest text NOT NULL,
    policy text NOT NULL,
    status text NOT NULL CHECK(status IN ('CANDIDATE','COMPLETE','PARTIAL','REJECTED')),
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(revision_id,derivation_id)
);
ALTER TABLE notice_v2_heads ADD FOREIGN KEY(current_revision_id,current_fact_set_id)
    REFERENCES notice_v2_derivations(revision_id,derivation_id) ON DELETE RESTRICT;
ALTER TABLE notice_v2_heads ADD FOREIGN KEY(current_revision_id,current_summary_id)
    REFERENCES notice_v2_derivations(revision_id,derivation_id) ON DELETE RESTRICT;
CREATE TABLE notice_v2_derivation_inputs (
    derivation_id uuid NOT NULL REFERENCES notice_v2_derivations(derivation_id) ON DELETE RESTRICT,
    input_derivation_id uuid NOT NULL REFERENCES notice_v2_derivations(derivation_id) ON DELETE RESTRICT,
    role text NOT NULL,
    PRIMARY KEY(derivation_id,input_derivation_id,role),
    CHECK(derivation_id<>input_derivation_id)
);
CREATE TABLE notice_v2_facts (
    fact_set_id uuid NOT NULL REFERENCES notice_v2_derivations(derivation_id) ON DELETE RESTRICT,
    ordinal integer NOT NULL CHECK(ordinal>=0),
    payload jsonb NOT NULL,
    PRIMARY KEY(fact_set_id,ordinal)
);
CREATE TABLE notice_v2_ocr (
    artifact_id uuid PRIMARY KEY,
    revision_id uuid NOT NULL REFERENCES notice_v2_revisions(revision_id) ON DELETE RESTRICT,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notice_v2_ocr_revision ON notice_v2_ocr(revision_id,created_at DESC);
