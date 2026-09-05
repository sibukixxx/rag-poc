# ForgeAI

> ⚠️ **Alpha Release** — v0.1 is under active development. API and configuration may change. Not recommended for production use without caution.

**A self-hosted RAG / AI application platform in a single Go binary.**

ForgeAI provides end-to-end knowledge management: ingest documents, run semantic search, evaluate retrieval quality with a golden dataset, and deploy chat APIs. Built with Go + SQLite, it runs on your infrastructure with no external dependencies.

**Key features:**
- 📄 Support for PDF, TXT, MD, HTML, CSV, JSON files
- 🔍 Hybrid search with semantic embeddings (OpenAI-compatible models)
- 🎯 Golden Dataset evaluation — measure retrieval + LLM generation quality
- 📊 Cost & latency tracking — see token usage and API costs per request
- 🚀 Deploy chat APIs — secure, rate-limited endpoints for your applications
- 🔐 Encrypted secret storage — AES-GCM with per-secret authentication

## Documentation

- [docs/V0.1_SPEC.md](docs/V0.1_SPEC.md) — Complete v0.1 specification (scope, API, schema, acceptance criteria)
- [docs/ROADMAP.md](docs/ROADMAP.md) — 12-week development roadmap
- [docs/DESIGN_REVIEW.md](docs/DESIGN_REVIEW.md) — Design decisions, trade-offs, risk assessment
- [docs/deploy-cloudflare.md](docs/deploy-cloudflare.md) — Free deployment guide (Cloudflare Tunnel + Workers)

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

The UI provides four main features:

**Chat** — Select an LLM alias (cheap / normal / judge) and chat interactively. Each reply shows token counts and API costs, recorded as Traces in SQLite. Optionally select a Knowledge Base to enable Hybrid Search retrieval with inline citations (`[1]`, `[2]`, etc.).

**Knowledge** — Create knowledge bases and upload files (PDF, TXT, MD, HTML, CSV, JSON). Documents are automatically chunked, normalized, and embedded. Identical re-uploads reuse cached embeddings (zero API cost). A **Search** sub-tab runs Hybrid Search (semantic + keyword, merged by RRF) with optional LLM reranking.

**Prompts** — Edit the RAG chat's system prompt without code changes. Write a version, diff it against the previous one, and activate it — the very next chat call uses it, no redeploy needed.

**Traces** — View every chat, search, and ingest call with detailed spans (type, latency, tokens, cost, status). Debug prompt and config changes by comparing traces side-by-side.

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
internal/usecase   ← Business logic (chat, ingest)
  ↓
internal/domain    ← Interfaces only (no external deps)
  ↓
internal/adapter   ← Implementations
  ├─ sqlite        ← Database & embedded migrations
  ├─ crypto        ← AES-GCM secret storage
  ├─ openaicompat  ← LLM & embedding client
  └─ extractor     ← PDF, HTML, text parsing
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
- 🚀 認証・レート制限付きチャット API のデプロイ
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
