# ForgeAI

> ⚠️ **Alpha Release** — v0.1 is under active development. API and configuration may change. Not recommended for production use without caution.

**A local-first RAG Quality Engineering platform in a single Go binary.**

ForgeAI makes RAG quality measurable and reproducible. It ingests documents, runs the same retrieval pipeline used by RAG chat, evaluates it against a versioned Golden Dataset, compares baseline and candidate runs, detects query-level regressions, and emits stable technical evidence. Built with Go + SQLite, it runs on your infrastructure without mandatory cloud infrastructure.

ForgeAI measures and verifies RAG quality. It produces reproducible metrics, traces, evaluation results, and technical evidence. Business interpretation, remediation priority, consulting recommendations, estimates, pricing, and proposals are outside this repository.

**Key features:**
- 📄 Support for PDF, TXT, MD, HTML, CSV, JSON files
- 🔍 Hybrid search with semantic embeddings (OpenAI-compatible models)
- 🎯 Versioned Golden Dataset retrieval evaluation
- 📐 Recall@K, Precision@K, Hit Rate@K, MRR, and nDCG@K
- 🔬 Query-level evidence, failure cases, baseline/candidate comparison, and regression detection
- 📦 Stable schema v1 JSON artifacts for CI and downstream consumers
- 📊 Trace, latency, token, and cost evidence where observable (missing values remain unavailable, never fake zeroes)
- 🔐 Encrypted secret storage — AES-GCM with per-secret authentication

## Documentation

- [docs/V0.1_SPEC.md](docs/V0.1_SPEC.md) — Complete v0.1 specification (scope, API, schema, acceptance criteria)
- [docs/ROADMAP.md](docs/ROADMAP.md) — 12-week development roadmap
- [docs/DESIGN_REVIEW.md](docs/DESIGN_REVIEW.md) — Design decisions, trade-offs, risk assessment
- [docs/deploy-cloudflare.md](docs/deploy-cloudflare.md) — Free deployment guide (Cloudflare Tunnel + Workers)
- [docs/EVALUATION.md](docs/EVALUATION.md) — Golden Dataset, metrics, CLI/API, artifacts, CI, and privacy
- [docs/CURRENT_STATE.md](docs/CURRENT_STATE.md) — Code-backed implementation assessment and known debt

## Installation

### Prerequisites
- Go 1.25+ ([install](https://go.dev/doc/install))
- An OpenAI-compatible LLM provider (OpenAI, Ollama, LM Studio, etc.)
- Docker (optional, for containerized deployment)

### Build

```bash
make build
```

This produces a fully static binary at `dist/forgeai` (CGO_ENABLED=0, no external libc needed).

## Quick Start

### 1. Initialize

```bash
./dist/forgeai init
```

This generates:
- `forgeai.yaml` — configuration file
- `FORGEAI_MASTER_KEY` — **save this value securely**

### 2. Configure Your LLM

Set your API credentials. The easiest way is via environment:

```bash
export FORGEAI_MASTER_KEY=<value_from_init>
export FORGEAI_OPENAI_API_KEY=sk-...
```

Or use the interactive prompt to store securely in the database:

```bash
echo "sk-..." | ./dist/forgeai secret set openai
```

### 3. Verify Configuration

```bash
./dist/forgeai doctor
```

This checks your LLM connection, database setup, and master key.

### 4. Start the Server

```bash
./dist/forgeai serve
```

Visit http://localhost:8080 to access the interface.

### 5. Use ForgeAI

The UI currently provides five main features:

**Chat** — Select an LLM alias (cheap / normal / judge) and chat interactively. Each reply shows token counts and API costs, recorded as Traces in SQLite. Optionally select a Knowledge Base to enable Hybrid Search retrieval with inline citations (`[1]`, `[2]`, etc.).

**Knowledge** — Create knowledge bases and upload files (PDF, TXT, MD, HTML, CSV, JSON). Documents are automatically chunked, normalized, and embedded. Identical re-uploads reuse cached embeddings (zero API cost). A **Search** sub-tab runs Hybrid Search (semantic + keyword, merged by RRF) with optional LLM reranking.

**Prompts** — Edit the RAG chat's system prompt without code changes. Write a version, diff it against the previous one, and activate it — the very next chat call uses it, no redeploy needed.

**Traces** — View every chat, search, and ingest call with detailed spans (type, latency, tokens, cost, status). Debug prompt and config changes by comparing traces side-by-side.

**Evaluation** — See the RAG quality flow as a visual explanation, run a Golden Dataset, review MRR/Recall/Hit Rate cards, drill into failed queries and retrieved ranks, download JSON Evidence, and compare Baseline/Candidate artifacts for improved and regressed queries.

The navigation separates these capabilities with dedicated icons, colors, Japanese role names, and one-line purpose descriptions so a non-engineering customer can distinguish “add knowledge”, “ask”, “control behavior”, “inspect execution”, and “prove quality” at a glance.

### Evaluate retrieval

Run the live retrieval pipeline against a Golden Dataset:

```bash
./dist/forgeai eval run --dataset golden.json --output evaluation/
```

Compare a baseline and candidate, then apply thresholds chosen by the operator:

```bash
./dist/forgeai eval compare --baseline baseline.json --candidate candidate.json
./dist/forgeai eval check --baseline baseline.json --candidate candidate.json \
  --min-mrr-delta=-0.05 --max-regressed-ratio=0.20
```

The bundled synthetic fixtures provide an offline, deterministic metrics demo; see [docs/EVALUATION.md](docs/EVALUATION.md).

## Available today

- ingestion, chunking, embeddings, vector/FTS5 hybrid retrieval, RRF, optional reranking
- cited RAG chat, prompt registry, retrieval/answer traces
- versioned JSON Golden Datasets
- deterministic retrieval metrics and query-level failure evidence
- Evaluation Run artifacts, baseline/candidate comparison, regression detection
- `eval run`, `eval compare`, and user-configured `eval check` CLI commands
- synchronous evaluation run/compare API foundation
- customer-readable Evaluation dashboard with KPI cards, failed-query drill-down, and comparison view

## Planned / in development

- Golden Dataset editing/version management UI
- durable Evaluation Run storage and richer score/trace linkage
- phase-level latency and complete retrieval token/cost accounting
- deterministic citation checks and explicitly non-ground-truth LLM judge evaluation
- benchmark suite, multi-run trends, and advanced experiment management
- runtime deployment API

## Non-goals

ForgeAI does not contain TechVit proprietary assessment, root-cause consulting, automatic remediation, customer-specific architecture recommendations, improvement priorities, estimates, pricing, proposals, sales reports, CRM, lead management, autonomous optimization, a hosted multi-tenant SaaS, or mandatory cloud infrastructure. It reports facts and deltas; it does not decree that a specific chunk size, model, or architecture is correct.

## Development

### Building the UI

The React source is in `web/`. After UI changes, rebuild:

```bash
cd web && npm run build
```

The built output is committed, so `go build` works without Node.js.

### Testing

```bash
make test
make vet
```

## Deployment

### Local (Docker Compose)

For quick self-hosting:

```bash
cp .env.example .env
# Edit .env with your FORGEAI_MASTER_KEY and API credentials
docker compose up -d --build
```

This starts:
- **forgeai** — the application
- **cloudflared** — Cloudflare Tunnel for secure external access (free)

### Production (Cloudflare)

ForgeAI works well behind Cloudflare Access (free tier) with Cloudflare Tunnel for routing.

For step-by-step instructions, cost breakdown, and alternative deployment options, see [docs/deploy-cloudflare.md](docs/deploy-cloudflare.md).

**Note:** Cloudflare Workers doesn't support Go + SQLite, so Docker with Tunnel is the recommended approach.

## Architecture

ForgeAI uses clean layered architecture:

```
cmd/forgeai        ← Main entry point (bootstrap, CLI, API server)
  ↓
internal/app       ← Wiring, HTTP server setup
  ↓
internal/http      ← Chi router, API handlers, SSE
internal/usecase   ← Application logic (chat, ingest, evaluate)
  ↓
internal/domain    ← Interfaces only (no external deps)
  ↓
internal/adapter   ← Implementations
  ├─ sqlite        ← Database, FTS5 & embedded migrations
  ├─ crypto        ← AES-GCM secret storage
  ├─ openaicompat  ← LLM & embedding client
  └─ extractor     ← PDF, HTML, text parsing
```

See [AGENTS.md](AGENTS.md) for detailed design decisions and conventions.

## v0.1 Roadmap

```
Upload Documents  →  Hybrid Search  →  Golden Dataset Eval  →  Evidence / Compare  →  Verify
       ↓                   ↓                      ↓                    ↓                  ↓
  PDF/TXT/MD/      FTS5 + vectors,        versioned cases       query regressions    JSON / CI
  HTML/CSV/JSON    merged by RRF           and ground truth      and metric deltas    thresholds
```

See [docs/ROADMAP.md](docs/ROADMAP.md) for the 12-week development plan.

## License

Licensed under the Apache License 2.0. See [LICENSE](LICENSE) for details.

## Contributing

We welcome pull requests and issues! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

For security issues, see [SECURITY.md](SECURITY.md).

---

## Japanese Documentation (日本語ドキュメント)

日本語でのご説明は以下の通りです（補助的な役割です）。

### ForgeAI について

ForgeAI は、**Go 単一バイナリで動作するLocal-first RAG Quality Engineering Platform**です。RAG品質を感覚ではなく、再現可能な技術Evidenceとして測定・比較・検証します。原因診断、改善優先順位、見積、提案などのコンサルティング判断は対象外です。

**主な機能：**
- 📄 PDF、TXT、MD、HTML、CSV、JSON ファイルをサポート
- 🔍 ハイブリッド検索（セマンティック + BM25）で日本語対応
- 🎯 version付きGolden DatasetによるRetrieval評価
- 📐 Recall@K / Precision@K / Hit Rate@K / MRR / nDCG@K
- 🔬 Query別Evidence、失敗ケース、Baseline/Candidate比較、Regression検出
- 📦 downstreamやCIで利用できるschema v1 JSON Artifact
- 🔐 AES-GCM による秘密情報の暗号化保存

### クイックスタート

```bash
make build
./dist/forgeai init
export FORGEAI_MASTER_KEY=...（init の出力から）
export FORGEAI_OPENAI_API_KEY=sk-...
./dist/forgeai serve
```

ブラウザで http://localhost:8080 を開くと：

- **Chat** — LLM とチャット。トークン数とコストを表示。ナレッジベースを選ぶと
  Hybrid Search で検索した根拠を引用付き（`[1]`, `[2]`...）で回答
- **Knowledge** — PDF など文書をアップロード。チャンク分割・正規化・埋め込みは
  自動、同一内容の再アップロードは embedding を再生成しない。**Search** サブタブで
  ベクトル+キーワードのハイブリッド検索を単独実行可能
- **Prompts** — RAG チャットのシステムプロンプトをコード変更なしで編集・
  バージョン管理・切り替え（diff 表示付き）
- **Traces** — chat / RAG chat / search / ingest の全呼び出しを span 単位
  （種別・レイテンシ・トークン・コスト・状態）で確認可能

**品質評価**画面では、評価の流れを図で確認し、Golden Datasetの実行、KPI表示、失敗Queryの検索順位確認、Evidence JSONの保存、Baseline/Candidate比較ができます。内蔵サンプルを使えば事前準備なしでデモできます。Golden Datasetの画面編集とRunの永続管理は今後の対応です。

### デプロイ

```bash
docker compose up -d --build
```

Cloudflare Tunnel で無料公開可能（手順は [docs/deploy-cloudflare.md](docs/deploy-cloudflare.md)）。

### v0.1 完成条件

```
文書投入 → ハイブリッド検索 → Golden Dataset 評価 → Baseline/Candidate比較 → Regression検出 → JSON Evidence
```

詳しくは [docs/ROADMAP.md](docs/ROADMAP.md) をご覧ください。
