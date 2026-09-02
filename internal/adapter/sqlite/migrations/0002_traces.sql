-- W2: telemetry tables. Every LLM call is recorded as a Span inside a
-- Trace (docs/DESIGN_REVIEW.md F-2), so token usage/cost/latency exist
-- before Workflow/Evaluation are built on top of them.

CREATE TABLE IF NOT EXISTS traces (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    started_at  TEXT NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    status      TEXT NOT NULL DEFAULT 'ok',
    cost_usd    REAL NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS spans (
    id            TEXT PRIMARY KEY,
    trace_id      TEXT NOT NULL REFERENCES traces(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL,
    name          TEXT NOT NULL,
    started_at    TEXT NOT NULL,
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    model         TEXT NOT NULL DEFAULT '',
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cost_usd      REAL NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT 'ok',
    error         TEXT NOT NULL DEFAULT '',
    input         TEXT NOT NULL DEFAULT '',
    output        TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_spans_trace_id ON spans(trace_id);
CREATE INDEX IF NOT EXISTS idx_traces_started_at ON traces(started_at);
