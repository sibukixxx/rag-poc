-- migration: 0012_create_ingestion_jobs
-- purpose: resumable bulk ingestion of filesystem corpora (#30). One job row
--          per run and one item row per discovered file, so a restart
--          resumes without re-processing completed files.
-- safety: additive; creates new tables and indexes, touches no existing data.
--         Rows are deleted with their source connection (and therefore with
--         their knowledge base) through ON DELETE CASCADE.
-- rollback: forward-only. To withdraw, add a new migration that drops them.

CREATE TABLE IF NOT EXISTS ingestion_jobs (
    id             TEXT PRIMARY KEY,
    connection_id  TEXT NOT NULL REFERENCES source_connections(id) ON DELETE CASCADE,
    status         TEXT NOT NULL,
    discovery_done INTEGER NOT NULL DEFAULT 0,
    scan_json      TEXT NOT NULL DEFAULT '{}',
    deleted_count  INTEGER NOT NULL DEFAULT 0,
    last_error     TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    started_at     TEXT,
    finished_at    TEXT
);

CREATE INDEX IF NOT EXISTS idx_ingestion_jobs_connection ON ingestion_jobs(connection_id, created_at);
CREATE INDEX IF NOT EXISTS idx_ingestion_jobs_status ON ingestion_jobs(status);

CREATE TABLE IF NOT EXISTS ingestion_job_items (
    job_id     TEXT NOT NULL REFERENCES ingestion_jobs(id) ON DELETE CASCADE,
    path       TEXT NOT NULL,
    size       INTEGER NOT NULL DEFAULT 0,
    mod_time   INTEGER NOT NULL DEFAULT 0,
    status     TEXT NOT NULL,
    attempts   INTEGER NOT NULL DEFAULT 0,
    error      TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (job_id, path)
);

CREATE INDEX IF NOT EXISTS idx_ingestion_job_items_status ON ingestion_job_items(job_id, status);
