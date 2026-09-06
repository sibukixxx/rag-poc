# Repository assessment

Assessment of the code before the RAG Quality Engineering P0 implementation.

## Architecture

The product is a local-first Go 1.25 application. `cmd/forgeai` boots clean-layered domain/usecase/adapter packages, Chi HTTP handlers, SQLite migrations, and a React/Vite SPA embedded into one static Go binary. Ingestion extracts PDF/TXT/Markdown/HTML/CSV/JSON, normalizes and chunks text, creates OpenAI-compatible embeddings, and stores chunks plus embeddings locally. Retrieval embeds the query, scans vectors, searches SQLite FTS5, fuses both lists with RRF, and optionally applies an LLM listwise reranker. RAG chat builds a bounded cited context and streams an answer. Trace/span storage observes chat, ingest, retrieval, and reranking operations.

## Classification

### IMPLEMENTED

- Go single-binary / embedded React / SQLite / local-first architecture
- document extraction, normalization, token chunking, hash-based embedding reuse
- brute-force vector search, SQLite FTS5 trigram search with short-query fallback
- hybrid RRF retrieval and optional fail-soft LLM reranking
- RAG answer generation, citations, prompt version registry, traces
- API and UI for chat, knowledge, search, prompts, and traces
- focused Go unit tests for adapters and use cases

### PARTIAL

- latency: total traces and broad retrieval spans exist; evaluation did not have per-phase evidence
- tokens/cost: LLM traces exist, but embedding/search token usage is not exposed consistently
- CI: a workflow template exists under `docs/`, not an active `.github/workflows` workflow
- release/build: Makefile and Docker build exist; automated release workflow is absent
- vector configuration: only the embedded brute-force implementation is available
- UI: core RAG operations exist; Evaluation and Golden Dataset screens do not

### PLANNED (before this change)

- Golden Dataset domain/storage/import
- Evaluation Run and query-level evaluation
- Recall@K, Precision@K, Hit Rate@K, MRR, nDCG@K
- baseline/candidate comparison and regression detection
- evaluation CLI/API/UI and CI gate
- answer/citation evaluation and benchmark suite

### DEAD / UNUSED

- no dead evaluation implementation was found; evaluation references in README/spec/roadmap were aspirational rather than wired code
- `projects` is bootstrap-era schema not used by current RAG handlers

## Main risks and debt

- Documentation previously described Golden Dataset evaluation and deployed runtime APIs as available although they were not implemented.
- Retrieval `Result.Score` is the final RRF/reranker score; original vector, lexical, fusion, and reranker scores are not separately preserved.
- Search trace creation occurs after spans, and the evaluation runner cannot directly bind the trace ID to a query artifact.
- Evaluation persistence and UI require a later migration; P0 uses stable versioned JSON artifacts.
- Frontend has lint/build scripts but no typecheck or test script because it is JavaScript and no test runner is configured.

The P0 implementation adds deterministic evaluation without changing the retrieval behavior or introducing a dependency on TechVit private systems.
