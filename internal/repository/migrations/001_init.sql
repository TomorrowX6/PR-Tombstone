CREATE TABLE IF NOT EXISTS repositories (
    id BIGSERIAL PRIMARY KEY,
    github_id BIGINT NOT NULL,
    installation_id BIGINT NOT NULL,
    owner TEXT NOT NULL,
    name TEXT NOT NULL,
    private BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (owner, name),
    UNIQUE (github_id)
);

CREATE TABLE IF NOT EXISTS installations (
    id BIGSERIAL PRIMARY KEY,
    github_id BIGINT NOT NULL UNIQUE,
    account_login TEXT NOT NULL DEFAULT '',
    account_type TEXT NOT NULL DEFAULT '',
    suspended_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
    delivery_id TEXT PRIMARY KEY,
    event_name TEXT NOT NULL,
    payload JSONB,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS analysis_jobs (
    id BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    installation_id BIGINT NOT NULL,
    pr_number INT NOT NULL,
    kind TEXT NOT NULL DEFAULT 'tombstone',
    status TEXT NOT NULL DEFAULT 'pending',
    attempts INT NOT NULL DEFAULT 0,
    last_error TEXT,
    run_after TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS analysis_jobs_ready_idx ON analysis_jobs(status, run_after, id);

CREATE TABLE IF NOT EXISTS pull_requests (
    id BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    number INT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    closed_at TIMESTAMPTZ,
    merged BOOLEAN NOT NULL DEFAULT FALSE,
    snapshot JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (repository_id, number)
);

CREATE TABLE IF NOT EXISTS pr_files (
    repository_id BIGINT NOT NULL,
    pr_number INT NOT NULL,
    filename TEXT NOT NULL,
    status TEXT NOT NULL,
    additions INT NOT NULL DEFAULT 0,
    deletions INT NOT NULL DEFAULT 0,
    changes INT NOT NULL DEFAULT 0,
    patch TEXT,
    PRIMARY KEY (repository_id, pr_number, filename)
);

CREATE TABLE IF NOT EXISTS pr_commits (
    repository_id BIGINT NOT NULL,
    pr_number INT NOT NULL,
    sha TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (repository_id, pr_number, sha)
);

CREATE TABLE IF NOT EXISTS evidence_items (
    id TEXT NOT NULL,
    repository_id BIGINT NOT NULL,
    pr_number INT NOT NULL,
    type TEXT NOT NULL,
    author TEXT NOT NULL DEFAULT '',
    author_association TEXT NOT NULL DEFAULT '',
    path TEXT,
    line INT,
    body TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ,
    rank_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    PRIMARY KEY (repository_id, pr_number, id)
);
CREATE INDEX IF NOT EXISTS evidence_fts_idx ON evidence_items USING GIN (to_tsvector('english', body));

CREATE TABLE IF NOT EXISTS tombstones (
    id BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    pr_number INT NOT NULL,
    state TEXT NOT NULL DEFAULT 'ACTIVE',
    summary TEXT NOT NULL DEFAULT '',
    result JSONB NOT NULL,
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
    model_version TEXT NOT NULL DEFAULT '',
    schema_version TEXT NOT NULL DEFAULT '1.0',
    generated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (repository_id, pr_number)
);

CREATE TABLE IF NOT EXISTS similarity_matches (
    id BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    new_pr_number INT NOT NULL,
    old_pr_number INT NOT NULL,
    score DOUBLE PRECISION NOT NULL,
    relationship TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (repository_id, new_pr_number, old_pr_number)
);

DO $$
BEGIN
    CREATE EXTENSION IF NOT EXISTS vector;
EXCEPTION WHEN undefined_file THEN
    RAISE NOTICE 'pgvector is not available; semantic search remains disabled';
END $$;

CREATE TABLE IF NOT EXISTS tombstone_embeddings (
    tombstone_id BIGINT PRIMARY KEY REFERENCES tombstones(id) ON DELETE CASCADE,
    title_embedding vector(64),
    description_embedding vector(64),
    discussion_embedding vector(64),
    approach_embedding vector(64),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS tombstone_embeddings_approach_idx
    ON tombstone_embeddings USING hnsw (approach_embedding vector_cosine_ops);

CREATE TABLE IF NOT EXISTS decision_relations (
    id BIGSERIAL PRIMARY KEY,
    repository_id BIGINT NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    source_type TEXT NOT NULL,
    source_key TEXT NOT NULL,
    relation TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_key TEXT NOT NULL,
    evidence_id TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (repository_id, source_type, source_key, relation, target_type, target_key)
);
CREATE INDEX IF NOT EXISTS decision_relations_repo_idx ON decision_relations(repository_id);

CREATE TABLE IF NOT EXISTS repository_settings (
    repository_id BIGINT PRIMARY KEY REFERENCES repositories(id) ON DELETE CASCADE,
    notify_mode TEXT NOT NULL DEFAULT 'dashboard',
    retention_days INT NOT NULL DEFAULT 30,
    contents_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
