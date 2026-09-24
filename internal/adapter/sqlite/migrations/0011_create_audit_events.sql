-- migration: 0011_create_audit_events
-- purpose: security audit trail (#24). One row per security-relevant action.
--          Rows hold IDs, fingerprints, and operation metadata only; never
--          secrets, prompts, or document text.
-- safety: additive; creates a new table and index, touches no existing data.
-- rollback: forward-only. To withdraw, add a new migration that drops the table.

CREATE TABLE IF NOT EXISTS audit_events (
    id            TEXT PRIMARY KEY,
    occurred_at   TEXT NOT NULL,
    action        TEXT NOT NULL,
    outcome       TEXT NOT NULL,
    actor         TEXT NOT NULL,
    target        TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_audit_events_occurred_at ON audit_events(occurred_at);
