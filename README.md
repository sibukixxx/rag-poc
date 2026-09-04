# ForgeAI

Self-hosted AI Application / RAG / Agent Platform — Single Binary Distribution.

Go バイナリ1個で、Knowledge 取り込み → RAG → Golden Dataset 評価 →
Before/After 比較 → Runtime API デプロイまでを提供する（v0.1 スコープ）。

## ドキュメント

- [docs/DESIGN_REVIEW.md](docs/DESIGN_REVIEW.md) — 構想の検証結果（採用方針・修正点・技術リスク）
- [docs/V0.1_SPEC.md](docs/V0.1_SPEC.md) — v0.1 確定仕様（スコープ / interface / スキーマ / API / 受け入れテスト）
- [docs/ROADMAP.md](docs/ROADMAP.md) — 週次ロードマップ（v0.1 = 12週）
- [docs/deploy-cloudflare.md](docs/deploy-cloudflare.md) — Cloudflare への最安デプロイ（Tunnel + Access で $0 / Containers は有料）

## Getting Started (W1-W3)

```sh
make build
./dist/forgeai init                        # generates forgeai.yaml + a master key
export FORGEAI_MASTER_KEY=...              # printed by init
export FORGEAI_OPENAI_API_KEY=sk-...       # or: echo "$KEY" | ./dist/forgeai secret set openai
./dist/forgeai doctor                      # checks config / db / master key / LLM aliases / embedding model
./dist/forgeai serve                       # http://localhost:8080
```

Open http://localhost:8080 for two tabs:

- **Chat** — pick an alias (cheap / normal / judge) and chat; each reply
  shows tokens and cost. Every call is recorded as a Trace+Span in SQLite.
- **Knowledge** — create a knowledge base and upload a PDF/TXT/MD/HTML/CSV/JSON
  file. It's loaded, NFKC-normalized, chunked (token-based, tiktoken), hashed,
  and embedded — re-uploading identical content reuses the existing embedding
  instead of re-calling the API.

The React source lives in `web/`; run `npm run build` there after UI changes
(the built `web/dist` is committed so `go build` alone still works without
Node installed).

## Deploy

```sh
cp .env.example .env      # FORGEAI_MASTER_KEY / FORGEAI_OPENAI_API_KEY / TUNNEL_TOKEN
docker compose up -d --build
```

`Dockerfile` で単一バイナリのコンテナを作り、`docker-compose.yml` の `cloudflared` サービスが
Cloudflare Tunnel 経由で公開する（Cloudflare 側は無料）。Workers には Go + SQLite のまま載らないため、
選択肢と手順は [docs/deploy-cloudflare.md](docs/deploy-cloudflare.md) を参照。

## v0.1 完成条件（要約）

```
./forgeai serve
PDF/MD 投入 → Hybrid Search（日本語対応）→ 引用付き RAG チャット
→ 50問 Golden Dataset 評価（Retrieval + LLM Judge）
→ 設定変更して Before/After 比較（品質 / コスト / レイテンシ）
→ /runtime/v1/apps/:slug/chat を API トークンでデプロイ
```
