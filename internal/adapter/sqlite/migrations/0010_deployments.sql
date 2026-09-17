CREATE TABLE deployments (
    id                TEXT PRIMARY KEY,
    slug              TEXT NOT NULL UNIQUE,
    knowledge_base_id TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE RESTRICT,
    prompt_name       TEXT NOT NULL,
    prompt_version    INTEGER NOT NULL CHECK (prompt_version > 0),
    prompt_content    TEXT NOT NULL,
    alias             TEXT NOT NULL,
    top_k             INTEGER NOT NULL CHECK (top_k > 0),
    rerank            INTEGER NOT NULL DEFAULT 0 CHECK (rerank IN (0, 1)),
    created_at        TEXT NOT NULL
);

CREATE INDEX idx_deployments_kb ON deployments(knowledge_base_id);

CREATE TABLE runtime_api_tokens (
    id            TEXT PRIMARY KEY,
    deployment_id TEXT NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    token_prefix  TEXT NOT NULL,
    token_hash    TEXT NOT NULL UNIQUE,
    created_at    TEXT NOT NULL,
    revoked_at    TEXT
);

CREATE INDEX idx_runtime_api_tokens_deployment ON runtime_api_tokens(deployment_id, created_at DESC);
