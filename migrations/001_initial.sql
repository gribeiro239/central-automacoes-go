CREATE TABLE IF NOT EXISTS schema_migrations (
    version integer PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE IF NOT EXISTS automations (
    id text PRIMARY KEY,
    document jsonb NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS runs (
    id text PRIMARY KEY,
    automation_id text NOT NULL REFERENCES automations(id),
    snapshot jsonb NOT NULL,
    origin text NOT NULL CHECK (origin IN ('manual')),
    status text NOT NULL CHECK (status IN ('pending','running','succeeded','failed')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    available_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    finished_at timestamptz,
    result jsonb,
    lease_token text,
    lease_until timestamptz,
    CHECK ((status = 'running') = (lease_token IS NOT NULL AND lease_until IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS runs_pending_idx ON runs (available_at,created_at,id) WHERE status='pending';
CREATE INDEX IF NOT EXISTS runs_expired_idx ON runs (lease_until) WHERE status='running';
CREATE INDEX IF NOT EXISTS runs_history_idx ON runs (created_at DESC,id DESC);

CREATE TABLE IF NOT EXISTS run_attempts (
    id text PRIMARY KEY,
    run_id text NOT NULL REFERENCES runs(id),
    number integer NOT NULL CHECK (number > 0),
    worker_id text NOT NULL,
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    result jsonb,
    UNIQUE (run_id,number)
);

INSERT INTO schema_migrations(version) VALUES (1) ON CONFLICT DO NOTHING;
