-- W1: minimal schema to bootstrap the server. Later weeks add
-- knowledge_bases/documents/chunks (W3), prompts (W6), datasets/evaluations
-- (W7-W9), deployments/traces (W2, W10) per docs/ROADMAP.md.

CREATE TABLE IF NOT EXISTS projects (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    slug       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Secret values are AES-GCM encrypted with FORGEAI_MASTER_KEY before being
-- written here; the DB never holds plaintext (docs/V0.1_SPEC.md F-9).
CREATE TABLE IF NOT EXISTS secrets (
    name          TEXT PRIMARY KEY,
    ciphertext    BLOB NOT NULL,
    nonce         BLOB NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
