-- W8: LLM Judge evaluation. Cases gain an optional reference answer;
-- runs and per-case results gain answer-quality scores (Correctness /
-- Groundedness / Relevance, 0.0-1.0) plus the judge's reason, the model
-- and prompt version that produced them, and per-case cost/latency so W9
-- can compare runs on quality, cost, and P95 latency
-- (docs/V0.1_SPEC.md §8, docs/ROADMAP.md W8).
ALTER TABLE dataset_cases ADD COLUMN expected_answer TEXT NOT NULL DEFAULT '';

ALTER TABLE evaluation_runs ADD COLUMN judge        INTEGER NOT NULL DEFAULT 0;
ALTER TABLE evaluation_runs ADD COLUMN alias        TEXT    NOT NULL DEFAULT '';
ALTER TABLE evaluation_runs ADD COLUMN correctness  REAL    NOT NULL DEFAULT 0;
ALTER TABLE evaluation_runs ADD COLUMN groundedness REAL    NOT NULL DEFAULT 0;
ALTER TABLE evaluation_runs ADD COLUMN relevance    REAL    NOT NULL DEFAULT 0;
ALTER TABLE evaluation_runs ADD COLUMN cost_usd     REAL    NOT NULL DEFAULT 0;

ALTER TABLE evaluation_results ADD COLUMN answer               TEXT    NOT NULL DEFAULT '';
ALTER TABLE evaluation_results ADD COLUMN correctness          REAL    NOT NULL DEFAULT 0;
ALTER TABLE evaluation_results ADD COLUMN groundedness         REAL    NOT NULL DEFAULT 0;
ALTER TABLE evaluation_results ADD COLUMN relevance            REAL    NOT NULL DEFAULT 0;
ALTER TABLE evaluation_results ADD COLUMN judge_reason         TEXT    NOT NULL DEFAULT '';
ALTER TABLE evaluation_results ADD COLUMN judge_model          TEXT    NOT NULL DEFAULT '';
ALTER TABLE evaluation_results ADD COLUMN judge_prompt_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE evaluation_results ADD COLUMN cost_usd             REAL    NOT NULL DEFAULT 0;
ALTER TABLE evaluation_results ADD COLUMN duration_ms          INTEGER NOT NULL DEFAULT 0;
