-- W7: Golden Dataset + Retrieval evaluation. A dataset is scoped to one
-- knowledge base; its cases are matched against that KB's documents by
-- filename rather than document ID, since re-ingesting a file creates a
-- new document row (docs/ROADMAP.md W7).
CREATE TABLE IF NOT EXISTS datasets (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL UNIQUE,
    knowledge_base_id TEXT NOT NULL REFERENCES knowledge_bases(id) ON DELETE CASCADE,
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS dataset_cases (
    id                  TEXT PRIMARY KEY,
    dataset_id          TEXT NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    query               TEXT NOT NULL,
    -- JSON array of filenames expected among the retrieval results.
    expected_filenames  TEXT NOT NULL,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_dataset_cases_dataset ON dataset_cases(dataset_id);

-- evaluation_runs holds retrieval-only aggregate metrics in W7; W8 adds
-- judge (answer quality) columns on top rather than a new table.
CREATE TABLE IF NOT EXISTS evaluation_runs (
    id              TEXT PRIMARY KEY,
    dataset_id      TEXT NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    status          TEXT NOT NULL, -- pending | running | done | failed
    error           TEXT NOT NULL DEFAULT '',
    top_k           INTEGER NOT NULL,
    rerank          INTEGER NOT NULL DEFAULT 0,
    recall_at_k     REAL NOT NULL DEFAULT 0,
    precision_at_k  REAL NOT NULL DEFAULT 0,
    mrr             REAL NOT NULL DEFAULT 0,
    hit_rate        REAL NOT NULL DEFAULT 0,
    started_at      TEXT NOT NULL,
    finished_at     TEXT
);
CREATE INDEX IF NOT EXISTS idx_evaluation_runs_dataset ON evaluation_runs(dataset_id);

CREATE TABLE IF NOT EXISTS evaluation_results (
    id                   TEXT PRIMARY KEY,
    run_id               TEXT NOT NULL REFERENCES evaluation_runs(id) ON DELETE CASCADE,
    case_id              TEXT NOT NULL REFERENCES dataset_cases(id) ON DELETE CASCADE,
    -- JSON array of filenames actually retrieved for this case.
    retrieved_filenames  TEXT NOT NULL,
    recall_at_k          REAL NOT NULL DEFAULT 0,
    precision_at_k       REAL NOT NULL DEFAULT 0,
    reciprocal_rank      REAL NOT NULL DEFAULT 0,
    hit                  INTEGER NOT NULL DEFAULT 0,
    error                TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_evaluation_results_run ON evaluation_results(run_id);
