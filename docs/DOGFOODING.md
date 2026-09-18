# ForgeAI Dogfooding Runbook

This runbook is the canonical self-use scenario for #26 and the W12 release gate.

## Why

ForgeAI must prove that a developer can use it for a real information problem, notice failures, improve the system, evaluate the change, and consume the chosen result through the Runtime API.

Synthetic examples remain useful for deterministic tests, but they are not sufficient evidence of usability.

## Safety boundary before the Security & Privacy gate

Until #21 is closed:

- use only TechVit-owned data;
- use public, sanitized, or intentionally non-confidential documents;
- do not ingest customer documents, credentials, contracts, private personal information, or production secrets;
- record which provider receives embeddings/RAG prompts when an external provider is used.

## Recommended first TechVit corpus

Keep the first run small enough to understand manually. Use approximately 10–30 documents selected from material such as:

- public README/product documentation from TechVit projects;
- sanitized service descriptions;
- public architecture/operation notes;
- intentionally prepared FAQ/decision notes that contain no client or personal secrets.

Do not dump every repository into ForgeAI. The purpose of the first run is to learn where retrieval and answering fail.

## Canonical run

### A. Fresh setup

1. Start from a clean data directory.
2. Run `forgeai init`.
3. Run `forgeai doctor`.
4. Explicitly configure the chosen LLM and embedding provider.
5. Record setup friction and elapsed wall-clock time.

### B. Ingest

Create a `techvit-dogfood` knowledge base and ingest the chosen corpus.

```bash
forgeai ingest -kb techvit-dogfood ./dogfood/docs
```

Inspect document/chunk counts and record any parsing/ingestion failure.

### C. Ask real questions first

Before writing a Golden Dataset, ask at least 10 questions you genuinely want ForgeAI to answer.

Examples:

- What is the current responsibility boundary of this project?
- Which features are already implemented and which are only planned?
- What is the supported deployment path?
- What data is sent to an external LLM?
- What are the known limitations?
- Which document supports this answer?

Record:
- expected answer/source;
- actual answer;
- whether the right source was retrieved;
- whether citations actually support the claim;
- latency/cost;
- failure type.

Do **not** edit the questions to make ForgeAI look better.

### D. Convert reality into a Golden Dataset

Turn the real questions and expected source filenames into a versioned dataset. Add expected answers only where there is a stable, defensible answer.

Run retrieval evaluation and optionally Judge evaluation.

### E. Improve one observed failure

Choose at least one genuine failure. Examples:

- wrong chunk ranked above the correct one;
- useful chunk omitted by top_k;
- answer overstates the evidence;
- prompt produces weak citations;
- rerank makes results worse.

Change one understandable variable (prompt, top_k, rerank, corpus structure, etc.), run a second evaluation, and use Before/After comparison.

A metric increase alone is not sufficient: inspect the changed cases.

### F. Deploy the chosen configuration

Create a W10 Deployment from the configuration you actually choose and issue a runtime token. Follow `docs/RUNTIME_API.md`.

Verify:

1. runtime search works;
2. runtime chat streams citations;
3. changing the management-side active prompt does not mutate the existing Deployment;
4. restart ForgeAI and confirm the Deployment/token still work;
5. revoke the token and confirm runtime access stops.

### G. File what hurt

Every meaningful friction should result in one of:
- documentation correction;
- bug fix;
- focused GitHub issue;
- explicit accepted limitation.

Avoid adding speculative features that were not discovered through the run.

## Dogfooding report template

Commit the result to `docs/dogfooding/YYYY-MM-DD-techvit.md`.

```markdown
# TechVit Dogfood — YYYY-MM-DD

## Environment
- ForgeAI commit/version:
- OS/arch:
- provider/model:
- embedding model:
- privacy mode / egress notes:

## Corpus
- documents:
- why these documents:
- sensitive-data review:

## Setup
- elapsed time:
- friction:

## Questions
| # | Question | Expected source | Result | Failure / observation |
|---|---|---|---|---|

## Evaluation A
- config:
- metrics:
- important failed cases:

## Change
- what changed:
- why:

## Evaluation B
- config:
- metrics:
- improved:
- regressed:

## Deployment
- slug:
- runtime search:
- runtime chat:
- restart persistence:
- token revocation:

## Issues discovered
- links:

## Decision
- what is ready:
- what blocks release:
```

## Exit criteria

Dogfooding passes only when the complete path is usable without undocumented knowledge and at least one real failure has been turned into an evidence-backed improvement.
