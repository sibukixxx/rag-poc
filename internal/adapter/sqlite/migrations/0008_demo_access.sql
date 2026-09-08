CREATE TABLE IF NOT EXISTS demo_users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    email         TEXT NOT NULL COLLATE NOCASE,
    company       TEXT NOT NULL,
    password_hash BLOB NOT NULL,
    expires_at    TEXT NOT NULL,
    disabled_at   TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_demo_users_email ON demo_users(email);

CREATE TABLE IF NOT EXISTS demo_sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES demo_users(id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_demo_sessions_user_id ON demo_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_demo_sessions_expires_at ON demo_sessions(expires_at);
