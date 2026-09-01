# ForgeAI

Self-hosted AI Application / RAG / Agent Platform — Single Binary Distribution.

Go バイナリ1個で、Knowledge 取り込み → RAG → Golden Dataset 評価 →
Before/After 比較 → Runtime API デプロイまでを提供する（v0.1 スコープ）。

## ドキュメント

- [docs/DESIGN_REVIEW.md](docs/DESIGN_REVIEW.md) — 構想の検証結果（採用方針・修正点・技術リスク）
- [docs/V0.1_SPEC.md](docs/V0.1_SPEC.md) — v0.1 確定仕様（スコープ / interface / スキーマ / API / 受け入れテスト）
- [docs/ROADMAP.md](docs/ROADMAP.md) — 週次ロードマップ（v0.1 = 12週）

## v0.1 完成条件（要約）

```
./forgeai serve
PDF/MD 投入 → Hybrid Search（日本語対応）→ 引用付き RAG チャット
→ 50問 Golden Dataset 評価（Retrieval + LLM Judge）
→ 設定変更して Before/After 比較（品質 / コスト / レイテンシ）
→ /runtime/v1/apps/:slug/chat を API トークンでデプロイ
```
