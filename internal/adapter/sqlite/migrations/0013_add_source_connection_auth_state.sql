-- migration: 0013_add_source_connection_auth_state
-- purpose: Web source control plane (#31). Track each connection's
--          credential state and last error, and hold short-lived OAuth
--          authorization states (hashed) between redirect and callback.
-- safety: additive. New columns have defaults, so existing rows and
--         older code keep working; the new table is independent.
-- rollback: forward-only. To withdraw, add a new migration.

ALTER TABLE source_connections ADD COLUMN auth_state TEXT NOT NULL DEFAULT 'not_required';
ALTER TABLE source_connections ADD COLUMN last_error TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS oauth_states (
    state_hash    TEXT PRIMARY KEY,
    connection_id TEXT NOT NULL REFERENCES source_connections(id) ON DELETE CASCADE,
    provider      TEXT NOT NULL,
    code_verifier TEXT NOT NULL DEFAULT '',
    expires_at    TEXT NOT NULL,
    created_at    TEXT NOT NULL
);
