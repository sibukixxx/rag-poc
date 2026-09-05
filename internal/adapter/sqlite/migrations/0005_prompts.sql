-- W6: Prompt Registry. active_version points at the version the RAG
-- pipeline currently uses; switching it is how a prompt edit takes
-- effect without a code change (docs/ROADMAP.md W6).
CREATE TABLE IF NOT EXISTS prompts (
    id             TEXT PRIMARY KEY,
    name           TEXT NOT NULL UNIQUE,
    active_version INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS prompt_versions (
    id         TEXT PRIMARY KEY,
    prompt_id  TEXT NOT NULL REFERENCES prompts(id) ON DELETE CASCADE,
    version    INTEGER NOT NULL,
    content    TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE(prompt_id, version)
);
