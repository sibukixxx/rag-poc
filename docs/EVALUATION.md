# RAG quality evaluation

ForgeAI is an evidence producer. It measures what was retrieved, calculates deterministic metrics, compares runs, and detects regressions. It does not infer root causes, prescribe chunk sizes or models, prioritize remediation, estimate work, or produce consulting proposals.

## Golden Dataset

A dataset is a versioned JSON document with a stable `id`, explicit `version`, target `knowledge_base_id`, and cases. A case contains a query and either relevant chunk IDs or relevant document IDs. When both are present, chunk IDs are the more precise ground truth. Optional answers, tags, category, difficulty, and notes are descriptive and are not used by deterministic retrieval metrics.

See `examples/evaluation/golden.json`. Real customer data must not be committed as a fixture.

## Metric definitions

For every positive integer K:

- `recall_at_K`: unique relevant targets found in the first K results divided by all relevant targets.
- `precision_at_K`: unique relevant targets found in the first K results divided by K. Missing result slots therefore do not count as relevant.
- `hit_rate_at_K`: 1 when the first K contain any relevant target; otherwise 0. A run's value is the mean over comparable queries.
- `reciprocal_rank`: reciprocal of the rank of the first relevant result, or 0 when absent. The run mean is MRR.
- `ndcg_at_K`: binary-relevance DCG divided by the ideal DCG at K.

Duplicate result IDs count once. K larger than the returned result count is valid. Empty retrieval yields observed zeroes. A Golden case with no relevant IDs is `NOT_APPLICABLE`, not zero, and is excluded from aggregates. Retrieval failures are `ERROR` and are also excluded rather than silently converted to zero.

## Evaluation run

```bash
forgeai eval run --dataset golden.json --output evaluation/
```

The command uses the same hybrid retrieval pipeline as the server and captures dataset version, retrieval configuration, model identifier, ForgeAI/Go/OS versions, timestamps, query-level results, overall metrics, latency, errors, and unknown fields. `--top-k 1,3,5,10` and `--rerank` are configurable.

For deterministic CI and the bundled synthetic demo, query-keyed fixed results can replace provider-backed retrieval:

```bash
forgeai eval run --dataset examples/evaluation/golden.json \
  --results examples/evaluation/baseline-results.json --output baseline.json
forgeai eval run --dataset examples/evaluation/golden.json \
  --results examples/evaluation/candidate-results.json --output candidate.json
```

The fixed-results mode is explicitly identified by the artifact's embedding model (`fixture`). It is intended for metrics verification, not as evidence of live retrieval quality.

## Comparison and regression gates

```bash
forgeai eval compare --baseline baseline.json --candidate candidate.json
forgeai eval check --baseline baseline.json --candidate candidate.json \
  --min-mrr-delta=-0.05 --max-regressed-ratio=0.20
```

Comparison reports aggregate metric deltas plus `IMPROVED`, `REGRESSED`, `UNCHANGED`, or `NOT_COMPARABLE` for each query, using reciprocal rank as the initial query-level comparison signal. `check` exits 2 when a user-supplied threshold is exceeded. ForgeAI supplies no universal “correct” threshold.

## HTTP API

- `POST /api/v1/evaluations/run` accepts `{ "dataset": ..., "config": ... }` and returns a run artifact.
- `POST /api/v1/evaluations/compare` accepts `{ "baseline": ..., "candidate": ... }`.

Runs are synchronous in schema v1. File artifacts are the stable persistence/interchange boundary for P0; durable run management and UI are planned.

## Artifact compatibility

Artifacts contain `schema_version: "1.0"` and `tool: "forgeai"`. Within major version 1, fields may be added but existing field meaning will not change. Removing/renaming a field or changing its semantics requires a new major schema version. Consumers must ignore unknown fields and reject unsupported major versions.

The schema is intentionally independent of any private repository. Downstream systems may consume facts, metrics, evidence, traces, and regression results without ForgeAI knowing their business schema.

## Privacy and external processing

Dataset queries, document chunks, reranking candidates, prompts, and generated answers may contain sensitive data. SQLite files and exported artifacts remain local unless the operator copies them. Query text and document text are sent to the configured embedding provider; reranking candidates are sent to the configured LLM only when reranking is enabled. Deterministic fixture evaluation makes no provider call. Artifacts can include retrieved text, so review them before sharing.

Token/cost evidence and per-phase evaluation latency are currently recorded as `UNAVAILABLE`/unknown messages where the retrieval interfaces cannot observe them. ForgeAI does not encode missing measurements as zero.
