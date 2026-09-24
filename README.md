# ForgeAI

> ⚠️ **Alpha Release** — v0.1 is under active development. API and configuration may change. Not recommended for production use without caution.

**A self-hosted RAG / AI application platform in a single Go binary.**

ForgeAI provides end-to-end knowledge management: ingest documents, run semantic search, evaluate retrieval quality with a golden dataset, and deploy chat APIs. Built with Go + SQLite, it runs on your infrastructure with no external dependencies.

**Key features:**
- 📄 Support for PDF, TXT, MD, HTML, CSV, JSON files
- 🔍 Hybrid search with semantic embeddings (OpenAI-compatible models)
- 🎯 Golden Dataset evaluation — measure retrieval + LLM generation quality
- 📊 Cost & latency tracking — see token usage and API costs per request
- 📁 Folder / NAS sources — resumable bulk ingestion of whole directory trees, run by the server in the background
- 🚀 Deploy chat APIs — immutable Deployment snapshots + Bearer-token Runtime API
- 🔒 Private Mode and outbound sensitive-data policy — block unapproved destinations, block or redact known identifier formats
- 🧾 Security audit trail, data deletion/retention, single-tenant production profile
- 🔐 Encrypted secret storage — AES-GCM for provider keys and source OAuth grants

## Status

| Area | State | Where |
|------|-------|-------|
| Ingest, Hybrid Search, cited RAG chat, Prompt Registry, Traces | ✅ | W1–W6 |
| Golden Dataset, retrieval metrics, LLM Judge, Before/After comparison | ✅ | W7–W9, [docs/EVALUATION.md](docs/EVALUATION.md) |
| Deployment snapshots + Runtime API (`/runtime/v1`) | ✅ | W10, [docs/RUNTIME_API.md](docs/RUNTIME_API.md) |
| Release packaging (GoReleaser, 4 targets, Docker) | ✅ | W11, [docs/RELEASE.md](docs/RELEASE.md) |
| Fail-closed v0.1 acceptance gate | ✅ script, ⏳ waiting on dogfooding | W12, [docs/V0.1_ACCEPTANCE.md](docs/V0.1_ACCEPTANCE.md) |
| Security & Privacy gate: Private Mode, deletion/retention, audit, outbound policy | ✅ | [#21](https://github.com/sibukixxx/rag-poc/issues/21), [docs/security/V0.1_SECURITY_EVIDENCE.md](docs/security/V0.1_SECURITY_EVIDENCE.md) |
| Folder / NAS bulk ingestion | ✅ | [#30](https://github.com/sibukixxx/rag-poc/issues/30), [docs/source-connectors.md](docs/source-connectors.md) |
| Web source control plane (OAuth, scopes, sync status) | ✅ framework, no OAuth connector bundled yet | [#31](https://github.com/sibukixxx/rag-poc/issues/31) |
| TechVit dogfooding report | ⏳ needs a real provider and real questions | [#26](https://github.com/sibukixxx/rag-poc/issues/26) |
| HTTP feed, Office files, Drive, M365, Atlassian, GitHub/Notion connectors | 📋 planned | [#29](https://github.com/sibukixxx/rag-poc/issues/29) |

`v0.1.0` is tagged only after `./scripts/v0.1-acceptance.sh` passes, which requires the dogfooding report.

Open work is tracked in [GitHub Issues](https://github.com/sibukixxx/rag-poc/issues) with `P0`/`P1`/`P2` and `area/*` labels; release-blocking items sit in the [v0.1 milestone](https://github.com/sibukixxx/rag-poc/milestone/1).

## Documentation

- [docs/USER_MANUAL_JA.md](docs/USER_MANUAL_JA.md) — 日本語の導入・画面操作・評価・運用マニュアル
- [docs/V0.1_SPEC.md](docs/V0.1_SPEC.md) — Complete v0.1 specification (scope, API, schema, acceptance criteria)
- [docs/ROADMAP.md](docs/ROADMAP.md) — 12-week development roadmap
- [docs/DESIGN_REVIEW.md](docs/DESIGN_REVIEW.md) — Design decisions, trade-offs, risk assessment
- [docs/EVALUATION.md](docs/EVALUATION.md) — Golden Dataset format, metric definitions, evaluation run semantics
- [docs/PRIVATE_MODE.md](docs/PRIVATE_MODE.md) — `local_only` / `external_allowed` provider egress policy and configuration
- [docs/deploy-cloudflare.md](docs/deploy-cloudflare.md) — Free deployment guide (Cloudflare Tunnel + Access)
- [docs/source-connectors.md](docs/source-connectors.md) — Source sync architecture, folder/NAS ingestion, OAuth control plane
- [docs/RUNTIME_API.md](docs/RUNTIME_API.md) — Deployments, runtime tokens, runtime search/chat
- [docs/RELEASE.md](docs/RELEASE.md) — Release packaging and clean-machine smoke test
- [docs/V0.1_ACCEPTANCE.md](docs/V0.1_ACCEPTANCE.md) — v0.1 acceptance gates
- [docs/DOGFOODING.md](docs/DOGFOODING.md) — TechVit dogfooding runbook
- [docs/security/](docs/security/) — Security evidence, data lifecycle, audit trail, sensitive-data policy
- [docs/demo-access.md](docs/demo-access.md) — 問い合わせ後に期限付き個別デモを発行する運用
- [SECURITY.md](SECURITY.md) — Security policy, implemented controls, and known limitations

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

Or store the key encrypted in the database. Name the secret in the provider config first, because a stored secret is only used when a provider references it:

```yaml
llm:
  providers:
    default:
      type: openai_compatible
      base_url: https://api.openai.com/v1
      api_key_secret: openai
```

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

The UI provides five main features:

**Chat** — Select an LLM alias (cheap / normal / judge) and chat interactively. Each reply shows token counts and API costs, recorded as Traces in SQLite. Optionally select a Knowledge Base to enable Hybrid Search retrieval with inline citations (`[1]`, `[2]`, etc.).

**Knowledge** — Create knowledge bases and upload files (PDF, TXT, MD, HTML, CSV, JSON). The **Data connections** sub-tab registers folders under `sources.filesystem.allowed_roots`, runs server-side sync jobs, and shows progress, failures, and authorization state. Documents are automatically chunked, normalized, and embedded. Identical re-uploads reuse cached embeddings (zero API cost). A **Search** sub-tab runs Hybrid Search (semantic + keyword, merged by RRF) with optional LLM reranking.

**Prompts** — Edit the RAG chat's system prompt without code changes. Write a version, diff it against the previous one, and activate it — the very next chat call uses it, no redeploy needed.

**Eval** — Create a Golden Dataset (human-verified test set) scoped to a knowledge base, import cases (JSON or CSV with `query` + `expected_filenames`), and run them through Hybrid Search. Each run reports Recall@K, Precision@K, MRR, and Hit Rate. Enable **LLM Judge** to grade each answer for Correctness / Groundedness / Relevance. Compare two finished runs as Before/After, analyzing quality/latency/cost with a winner rationale, and export as Markdown. See [examples/](examples/) for a 50-question sample.

**Traces** — View every chat, search, and ingest call with detailed spans (type, latency, tokens, cost, status). Debug prompt and config changes by comparing traces side-by-side.

### CLI

Everything above is also scriptable without the browser:

```bash
forgeai init                          # generate forgeai.yaml + master key
forgeai secret set <name>             # store a provider key (value read from stdin)
forgeai doctor                        # check DB, master key, privacy mode, provider destinations
forgeai ingest -kb <slug> <dir>       # ingest files directly under <dir> (KB created if missing)
forgeai eval import|run|list|compare  # Golden Dataset import, evaluation run, Before/After compare
forgeai demo-user create|list|revoke  # expiring demo accounts (docs/demo-access.md)
forgeai source add-fs|sync|job|pause|resume  # folder / NAS bulk ingestion
forgeai data delete-kb|delete-document|retention|compact  # customer-data deletion and retention
forgeai audit list                    # security audit trail
forgeai serve                         # start the HTTP server + embedded UI
```

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
internal/usecase   ← Business logic (chat, ingest, search, RAG, evaluate, judge, compare, source sync)
  ↓
internal/domain    ← Interfaces only (no external deps)
  ↓
internal/adapter   ← Implementations
  ├─ sqlite        ← Database & embedded migrations
  ├─ crypto        ← AES-GCM secret storage
  ├─ openaicompat  ← LLM & embedding client
  ├─ egress        ← Private Mode destination policy on the provider transport
  ├─ extractor     ← PDF, HTML, CSV, JSON, text parsing
  ├─ tokenizer     ← Token counting / chunking
  ├─ vecmem/vecenc ← Embedded brute-force vector index & encoding
  ├─ llmrerank     ← Optional LLM listwise reranker
  └─ source        ← Source connector registry
```

See [AGENTS.md](AGENTS.md) for detailed design decisions and conventions.

## v0.1 Roadmap

```
Upload Documents  →  Semantic Search  →  Golden Dataset Eval  →  Quality Analysis  →  Deploy API
       ↓                   ↓                      ↓                    ↓                  ↓
  PDF/TXT/MD/      Hybrid (BM25 +          50 benchmark          Side-by-side       /runtime/v1
  HTML/CSV/JSON    vectors, multi-lang)    questions             Before/After        chat endpoints
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

ForgeAI は、**Go 単一バイナリで動作する自ホスト型 RAG / AI アプリケーション プラットフォーム**です。

**主な機能：**
- 📄 PDF、TXT、MD、HTML、CSV、JSON ファイルをサポート
- 🔍 ハイブリッド検索（セマンティック + BM25）で日本語対応
- 🎯 Golden Dataset による検索・生成品質の自動評価
- 📊 トークン数と API コストの追跡
- 📁 フォルダ / NAS の一括取り込み（サーバー側で再開可能なジョブとして実行）
- 🧾 監査ログ、データ削除と保持期間、単一テナントの本番プロファイル
- 🛡️ 外部プロバイダへの送信時にメール・電話番号などの既知の形式を遮断またはマスク
- 🔒 Private Mode（`local_only`）— 承認外の LLM / 埋め込み先への送信を送信前に遮断
- 🔐 AES-GCM による秘密情報の暗号化保存
- 🚀 認証・レート制限付きチャット API のデプロイ

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
- **Eval** — ナレッジベースに紐づく Golden Dataset（人手で正解を検証した
  少量の評価用データ。eval set / human-labeled test set とも呼ばれる。
  学習データではなく品質を測る「ものさし」）を作成し、ケース
  （query + expected_filenames）を JSON/CSV でインポート。実際の検索と同じ
  Hybrid Search で評価を実行し、Recall@K / Precision@K / MRR / Hit Rate を表示。
  **LLM Judge** を有効にすると各質問に RAG で回答し、`judge` alias が
  Correctness / Groundedness / Relevance（0〜1）と理由を採点。run をクリックすると
  低スコアケースを理由付きで確認できる。完了した run を 2 つ **A / B** に選ぶと
  Before/After 表（品質・P95 レイテンシ・コスト・ケース単位の改善/悪化・Winner と根拠）が
  出て、Markdown でエクスポートできる（日本語50問のサンプルは [examples/](examples/) 参照）
- **Traces** — chat / RAG chat / search / ingest の全呼び出しを span 単位
  （種別・レイテンシ・トークン・コスト・状態）で確認可能

### デプロイ

```bash
docker compose up -d --build
```

Cloudflare Tunnel で無料公開可能（手順は [docs/deploy-cloudflare.md](docs/deploy-cloudflare.md)）。

### v0.1 完成条件

```
文書投入 → ハイブリッド検索 → Golden Dataset 評価 → Before/After 比較 → API デプロイ
```

詳しくは [docs/ROADMAP.md](docs/ROADMAP.md) をご覧ください。
