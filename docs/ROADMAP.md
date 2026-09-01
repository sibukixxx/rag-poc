# ForgeAI ロードマップ（週次）

前提: 副業ペース（週 8〜12h 想定）。各週は「動くもの + 完了条件」で区切る。
v0.1 は 12 週。詰まったら週番号をずらすのではなく、その週のスコープを削る。

---

## v0.1（W1–W12）: 評価可能な RAG 実行基盤

### W1: 骨格 ✅ 完了
- Go モジュール初期化、`cmd/forgeai`、config 読み込み（YAML + env）
- modernc SQLite + migrations（embed）、`projects` / `settings` / `secrets`（AES-GCM）
- chi ルータ、`/api/v1/health`、`forgeai doctor`（DB / filesystem チェック）
- CI（vet + test + CGO_ENABLED=0 での4ターゲットビルド + smoke test）を
  `docs/ci-workflow.yml` に用意（このセッションのトークンには GitHub Actions
  workflow ファイルへの push 権限がないため、`.github/workflows/ci.yml` への
  設置はリポジトリ管理者が手動で行うこと）
- **完了条件**: `go build` 一発のバイナリで serve / doctor が動く → 確認済み
  （`forgeai init` → `forgeai doctor` → `forgeai serve` → `/api/v1/health` が 200 を返す）

### W2: LLM アダプタ + Playground
- OpenAI-compatible アダプタ（Generate / Stream、**Usage 必須取得**）
- LLM Router（alias 解決）、単価テーブル → コスト計算
- **Trace / Span の最小実装をここで入れる**（F-2。全 LLM 呼び出しが span を残す）
- React shell（Vite）+ go:embed、Chat Playground（SSE）
- **完了条件**: Playground でチャットでき、Trace にトークン数とコストが残る

### W3: Ingestion
- Loader interface + TXT / MD / HTML / CSV / JSON、ベストエフォート PDF
- 外部コンバータ差し込み口（HTTP converter、未設定なら内蔵 loader）
- 正規化（NFKC）→ Chunker（トークン数ベース, tiktoken-go）→ Hash 付与
- Embedding パイプライン（hash 一致でスキップ）、documents API + UI（一覧 / 状態）
- **完了条件**: PDF/MD を投げて chunks + embeddings が入る。再投入で再生成されない

### W4: Hybrid Search
- vecmem（ブルートフォース cosine）+ FTS5 **trigram**（F-4）
- RRF マージ、search API、UI の Search Playground（スコア・ページ表示）
- 日本語クエリの手動確認セットで検索品質をチェック
- **完了条件**: 日本語クエリで vector / keyword 両系統がヒットし、マージ結果が返る

### W5: RAG チャット
- Context Builder（トークン予算内で chunk 詰め）→ Prompt → LLM → 引用付き回答
- LLM rerank（任意、デフォルト off）
- Playground を KB 接続チャットに拡張（引用リンク → chunk 表示）
- **完了条件**: 「返品規定について教えて」に引用付きで答える

### W6: Prompt Registry + Trace UI
- prompts / prompt_versions CRUD + UI（バージョン一覧・diff）
- RAG パイプラインが prompt version を参照する形に変更
- Trace 一覧 / 詳細 UI（span ツリー、レイテンシ・コスト内訳）
- **完了条件**: prompt を v2 に切り替えて挙動が変わり、Trace で全過程を追える

### W7: Golden Dataset + Retrieval 評価
- datasets / dataset_cases、JSON / CSV インポート（UI + CLI）
- Evaluation Runner（非同期 job）: Recall@K / Precision@K / MRR / Hit Rate
- `examples/` に日本語サンプル文書 + 50問 Golden Dataset を作る（実データ整備も工数）
- **完了条件**: `forgeai eval run demo-golden` で Retrieval Hit Rate が出る

### W8: LLM Judge 評価
- Judge（alias: judge、judge プロンプトもバージョン管理）
- Correctness / Groundedness / Relevance + reason 保存
- run 詳細 UI（ケース別スコア、失敗ケースのドリルダウン）
- **完了条件**: 50問の judge 評価が完走し、低スコアケースの理由が読める

### W9: Experiment 比較
- run 間比較 API + UI（品質 / Groundedness / P95 レイテンシ / コスト、Winner 表示）
- 比較結果の Markdown エクスポート（顧客向け成果報告の種）
- **完了条件**: 設定を変えた 2 run の Before/After 表が出て、エクスポートできる

### W10: Deployment + Runtime API
- deployments（設定スナップショット = prompt vN + alias + retriever 設定）
- API トークン発行（hash 保存）、`/runtime/v1/apps/:slug/chat|search`（SSE）
- レート制限・入力サイズ制限（最小限）
- **完了条件**: curl + Bearer トークンで外部からチャットできる

### W11: パッケージング
- goreleaser（linux/amd64, linux/arm64, darwin/arm64, windows/amd64）
- Dockerfile（distroless）、`forgeai init`（対話式初期設定）
- README / クイックスタート / 設定リファレンス
- **完了条件**: 素の VM にバイナリ 1 個置いて 5 分で demo が動く

### W12: E2E + バッファ
- 受け入れテスト自動化（モック LLM で CI、実 LLM でローカル）
- v0.1 タグ付け。溢れた作業の回収
- **完了条件**: `V0.1_SPEC.md` §9 の受け入れテストが全部通る

---

## v0.2（W13–W20 目安): Workflow + 本番 DB

- Workflow DAG（YAML 定義、import/export/version）
- Node: Trigger(Webhook/Schedule) / LLM / KnowledgeSearch / HTTP / Condition / Transform / Output
- Human Approval（承認キュー + 修正回答の Golden Dataset 候補化）
- PostgreSQL + pgvector アダプタ（Embedded → 本番移行パス）
- Rerank API アダプタ（Cohere / Voyage）、Anthropic / Gemini 専用アダプタ

## v0.3 以降

- Agent Runtime（AgentPolicy: max_steps / max_cost / timeout / tool allowlist）
- Tool System（HTTP / OpenAPI / MCP client）、ForgeAI 自体の MCP server 化
- RBAC / users / audit_logs、外部 Secret Manager アダプタ
- コネクタ（Google Drive / Notion / Slack / GitHub）、OCR
- テンプレート配布（`forgeai template install customer-support` など）
- Feedback loop（本番フィードバック → Bad Cases → Dataset → Experiment → Deploy）

---

## 運用ルール

- 週の頭にその週のスコープを issue 化、週末に「動くもの」をコミットして閉じる
- 仕様変更はまず `DESIGN_REVIEW.md` / `V0.1_SPEC.md` を更新してから実装する
- v0.1 完了までは新レイヤー（Workflow / Agent / コネクタ）の実装に着手しない
