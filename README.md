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

## Quick Start

### Prerequisites
- Go 1.25+
- An OpenAI-compatible LLM API (OpenAI, Ollama, etc.)
- A Linux/macOS/Windows machine

### 1. Build

```sh
make build
```

The binary `dist/forgeai` is fully static (CGO_ENABLED=0) and runs everywhere.

### 2. Initialize

```sh
./dist/forgeai init
```

This generates:
- `forgeai.yaml` — configuration file
- A random `FORGEAI_MASTER_KEY` — **save this securely**

### 3. Configure LLM

Set your OpenAI API key (or compatible endpoint):

```sh
export FORGEAI_MASTER_KEY=your_key_from_init
export FORGEAI_OPENAI_API_KEY=sk-...
```

Or use the interactive prompt:

```sh
echo "sk-..." | ./dist/forgeai secret set openai
```

### 4. Start the Server

```sh
./dist/forgeai doctor     # verify configuration
./dist/forgeai serve      # starts on http://localhost:8080
```

### 5. Open the UI

Visit http://localhost:8080 and you'll see two tabs:

**Chat**
- Select a model alias (cheap / normal / judge)
- Chat with the LLM and see token counts & costs
- All conversations are stored with trace data in SQLite

**Knowledge**
- Create a knowledge base
- Upload files (PDF, TXT, MD, HTML, CSV, JSON)
- ForgeAI chunks, normalizes, and embeds documents
- Identical re-uploads skip the embedding API cost
- Query your knowledge via the chat interface

## Development

The React UI source is in `web/`. Rebuild after changes:

```sh
cd web && npm run build
```

The built output (`web/dist`) is committed, so a plain `go build` works without Node installed.

## Deployment

### Local (Docker)

```sh
cp .env.example .env
# Edit .env with your FORGEAI_MASTER_KEY and FORGEAI_OPENAI_API_KEY
docker compose up -d --build
```

The `docker-compose.yml` includes:
- **forgeai** service — the Go binary
- **cloudflared** tunnel — routes external traffic securely

### Production (Cloudflare)

ForgeAI works great behind Cloudflare Access (free tier). See [docs/deploy-cloudflare.md](docs/deploy-cloudflare.md) for step-by-step instructions and cost breakdown.

**Note:** Workers doesn't support Go + SQLite, so use the Docker approach with Cloudflare Tunnel (free) instead.

## v0.1 Roadmap

```
Document Upload  →  Hybrid Search  →  Golden Dataset Eval  →  Before/After Comparison  →  API Deployment
        ↓               ↓                      ↓                      ↓                           ↓
   PDF/TXT/MD/   Japanese-aware        50 eval questions      Quality/Cost/Latency      /runtime/v1/chat
   HTML/CSV/JSON  semantic + BM25      (retrieval + LLM)      metrics (side-by-side)    endpoints
```

See [docs/ROADMAP.md](docs/ROADMAP.md) for weekly milestones.

## License

Licensed under the Apache License 2.0. See [LICENSE](LICENSE) for details.

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

- **Chat** — LLM とチャット。トークン数とコストを表示
- **Knowledge** — PDF など文書をアップロード。意味検索で質問に答える

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
