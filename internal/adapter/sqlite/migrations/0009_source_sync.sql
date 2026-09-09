-- Provider-neutral state for incremental external-source synchronization.
-- Credentials are never stored here; secret_name refers to the encrypted
-- ForgeAI secret store.

CREATE TABLE source_connections (
    id                TEXT PRIMARY KEY,
    knowledge_base_id TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    provider          TEXT NOT NULL,
    name              TEXT NOT NULL,
    config_json       TEXT NOT NULL DEFAULT '{}',
    secret_name       TEXT NOT NULL DEFAULT '',
    cursor            TEXT NOT NULL DEFAULT '',
    enabled           INTEGER NOT NULL DEFAULT 1,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

CREATE INDEX idx_source_connections_kb ON source_connections(knowledge_base_id);

CREATE TABLE source_items (
    id                TEXT PRIMARY KEY,
    connection_id     TEXT NOT NULL REFERENCES source_connections(id) ON DELETE CASCADE,
    external_id       TEXT NOT NULL,
    document_id       TEXT REFERENCES documents(id) ON DELETE SET NULL,
    title             TEXT NOT NULL DEFAULT '',
    source_url        TEXT NOT NULL DEFAULT '',
    metadata_json     TEXT NOT NULL DEFAULT '{}',
    visibility_json   TEXT NOT NULL DEFAULT '[]',
    content_hash      TEXT NOT NULL DEFAULT '',
    remote_updated_at TEXT,
    synced_at         TEXT NOT NULL,
    deleted_at        TEXT,
    UNIQUE(connection_id, external_id)
);

CREATE INDEX idx_source_items_document ON source_items(document_id);

CREATE TABLE source_sync_jobs (
    id                TEXT PRIMARY KEY,
    connection_id     TEXT NOT NULL REFERENCES source_connections(id) ON DELETE CASCADE,
    status            TEXT NOT NULL,
    cursor_before     TEXT NOT NULL DEFAULT '',
    cursor_after      TEXT NOT NULL DEFAULT '',
    created_count     INTEGER NOT NULL DEFAULT 0,
    updated_count     INTEGER NOT NULL DEFAULT 0,
    deleted_count     INTEGER NOT NULL DEFAULT 0,
    skipped_count     INTEGER NOT NULL DEFAULT 0,
    error             TEXT NOT NULL DEFAULT '',
    started_at        TEXT NOT NULL,
    finished_at       TEXT
);

CREATE INDEX idx_source_sync_jobs_connection ON source_sync_jobs(connection_id, started_at DESC);
