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

### W2: LLM アダプタ + Playground ✅ 完了
- OpenAI-compatible アダプタ（Generate / Stream、**Usage 必須取得**）+ モックサーバでの単体テスト
- LLM Router（alias 解決）、単価テーブル → コスト計算（`forgeai secret set/delete` で暗号化キー管理も追加）
- **Trace / Span の最小実装をここで導入**（F-2。全 LLM 呼び出しが span を残す。`traces`/`spans` テーブル追加）
- React shell（Vite）+ go:embed（`web/dist` をリポジトリに commit し、Node 無しでも `go build` が通る）、Chat Playground（SSE、alias 切替、token/cost/trace 表示）
- `forgeai doctor` に LLM alias ごとの provider/model/APIキー解決チェックを追加
- **完了条件**: Playground でチャットでき、Trace にトークン数とコストが残る → 確認済み
  （モック OpenAI 互換サーバに対し `forgeai serve` → ブラウザ(Playwright)で実際にメッセージ送信 →
  ストリーミング応答 + `12→4 tok · $0.000004 · trace ...` の表示を確認。単体テストも全緑）

### W3: Ingestion ✅ 完了
- Loader interface + TXT / MD / HTML / CSV / JSON、ベストエフォート PDF（`internal/adapter/extractor`）
  - CSV/JSON は「1行/1要素 = 1 Page」で取り込み、行単位の検索粒度を確保
  - PDF は `ledongthuc/pdf`（純Go）。実PDF（fpdf2生成の2ページファイル）でテスト済み
  - 外部コンバータ差し込み口は `knowledge.Loader` interface が担う（`Registry.Register`）。実際の
    HTTPコンバータ実装自体は先送り（インターフェースの拡張点のみ用意）
- 正規化（NFKC, `golang.org/x/text`）→ Chunker（トークン数ベース, tiktoken-go + オフラインBPEローダーで
  ネットワーク接続なしに動作することを確認済み）→ Hash 付与（`internal/adapter/tokenizer`）
- Embedding パイプライン（`internal/usecase/ingest.go`）: chunk hash + embedding model 名で
  既存embeddingを検索し、一致すればAPI呼び出しをスキップ。ingest系呼び出しもTraceに記録
- `knowledge_bases` / `documents` / `chunks` / `embeddings` テーブル（migration 0003）
- API: `POST/GET /api/v1/knowledge-bases`, `POST/GET /api/v1/knowledge-bases/:id/documents`
  （アップロードは同期処理。W3ではジョブキューを導入しない）
- UI: Knowledge タブ（KB作成/選択、ファイルアップロード、ドキュメント一覧+ステータス）
- **完了条件**: PDF/MD を投げて chunks + embeddings が入る。再投入で再生成されない → 確認済み
  （モックサーバでMD+実PDFをアップロード→chunks/embeddings生成→同一MDを再アップロード→
  embeddings API呼び出しが増えないことをサーバログで確認。ブラウザ(Playwright)でもUI経由の
  アップロードとステータス表示を確認。単体テスト全緑、うちハッシュスキップの回帰テストを含む）

### W4: Hybrid Search ✅ 完了
- `internal/adapter/vecmem`: 埋め込みモード用ブルートフォース cosine（`embeddings`テーブルを
  KB単位でスキャン。上限目安〜数万chunkはドキュメント記載通り）
- `internal/adapter/sqlite/fts_store.go`: FTS5 **trigram**（F-4）。standalone virtual table
  （`chunk_id`/`document_id`をUNINDEXEDで保持し、`ReplaceChunks`内で手動同期。content-rowid方式は
  chunks.idがTEXT PKのため見送り）
  - **重要な追加知見**: trigramトークナイザは3文字未満のクエリでは一切マッチしない
    （2文字の日本語クエリ「返品」等が実機検証で0件だった）。**LIKE '%q%' フォールバック**を
    3文字未満のクエリに適用することで対応（`fts_store.go`の`minTrigramQueryRunes`）
  - MATCH クエリはユーザー入力をFTS5クエリ構文として解釈させず「フレーズリテラル」として
    渡す（ダブルクォートでエスケープ）ことで、ハイフン等を含む任意の入力でも構文エラーにならない
    ようにした
- `internal/usecase/search.go`: クエリ embed → vector top30 + keyword top30 → RRF(k=60)マージ
  → （任意）LLM listwise rerank(alias: cheap, `internal/adapter/llmrerank`) → top_k
  - rerank は fail-soft: LLM呼び出し失敗やレスポンスのパース失敗時は元の順序にフォールバックし、
    検索全体は失敗させない
  - 検索呼び出しも Trace に記録（kind: embed/retrieve/rerank）
- API: `POST /api/v1/knowledge-bases/:id/search {query, top_k, rerank}`
- UI: Knowledge タブ内に Documents/Search サブタブを追加。スコア・ページ・ファイル名を表示
- **完了条件**: 日本語クエリで vector / keyword 両系統がヒットし、マージ結果が返る → 確認済み
  （モックサーバに「返品ポリシー」「配送情報」の2文書をingestし、(1)英語の意味的クエリ
  "refund for returned item"、(2)3文字以上の日本語部分一致クエリ「返品規定について」、
  (3)2文字の日本語クエリ「返品」— の3パターンで返品ポリシー文書が正しく上位に来ることを
  curlで確認。(3)は表層的にはFTS5 trigramでは検出不可能なはずのケースで、LIKEフォールバックが
  効いていることも確認。rerank:trueでも(フェイクサーバがrerank非対応のため)正しくフォールバックし
  結果が返ることを確認。ブラウザ(Playwright)でもSearchサブタブの実行と結果表示を確認、
  既存のChat/Documentsタブの回帰も無し。単体テスト全緑）

### W5: RAG チャット ✅ 完了
- `internal/usecase/rag_context.go`: Context Builder。SearchUseCase の結果を
  `[1]`, `[2]`... と採番し、トークン予算（既定2000）に収まる範囲で詰める。
  ランク最上位のチャンクは予算を超えても必ず1件は含める（0件で答えるよりまし）
- `internal/usecase/rag_chat.go`: `RAGChatUseCase.ChatStream` — Search → Context Builder →
  system prompt（「番号付き引用元のみに基づき、`[n]`で逐次引用して回答せよ」）→ LLM Stream。
  ChatUseCase と同様に Trace/Span を記録
- LLM rerank は W4 で実装済みの `retrieval.Options.Rerank` をそのまま渡す形で対応
  （既定 off、リクエストで `rerank: true` にすると有効）
- API: `POST /api/v1/knowledge-bases/:id/chat`（SSE。`{alias, query, rerank}` →
  delta イベント + 最終 `{done, usage, cost_usd, citations, no_context}` イベント）
- UI: Chat タブに KB セレクタを追加。KB 未選択時は従来通りの複数ターンチャット、
  KB 選択時はその質問単体で RAG 検索し直す1問1答モードに切り替わる。回答の下に
  引用チップ（`[1] filename.md p.2`）を表示し、クリックで該当チャンク本文を展開表示
- **完了条件**: 「返品規定について教えて」に引用付きで答える → 確認済み
  （モックサーバに返品ポリシー文書をingestし、RAGチャットAPIへ日本語で質問→
  「返品は商品到着後30日以内であれば可能です [1]。」という引用付き回答と、
  filename/text入りのcitationsが返ることをcurlで確認。空のKBに対する質問では
  `no_context:true`が正しく返ることも確認。ブラウザ(Playwright)でもKB選択→
  質問→引用チップ表示→クリックで本文展開、を実演。既存の平文チャット/
  Documents/Searchタブの回帰も無し。単体テスト全緑）

### W6: Prompt Registry + Trace UI ✅ 完了
- `internal/domain/prompt` + `internal/adapter/sqlite/prompt_store.go`: `prompts`/`prompt_versions`
  テーブル（migration 0005）。`CreateVersion`は自動採番（1始まり）し、**最初の1件だけ自動的に
  active化**、2件目以降は明示的な`SetActiveVersion`が必要（誤って新バージョンがいきなり
  本番投入されるのを防ぐ）
- API: `POST/GET /api/v1/prompts`, `GET/POST /api/v1/prompts/:id/versions`,
  `POST /api/v1/prompts/:id/activate`
- `internal/usecase/rag_chat.go`: system prompt を Prompt Registry の active version から
  都度取得する形に変更（`RAGChatUseCase.systemPrompt`）。レジストリ未設定/未シードでも
  `DefaultRAGSystemPrompt` にフォールバックする fail-soft 設計
- `internal/app/prompts.go`: `forgeai serve` 起動時に `rag_system` プロンプトのv1を
  （旧ハードコード文言と同一内容で）自動シード。既存インストールの挙動を変えずに
  レジストリへ移行できる
- Trace: `GET /api/v1/traces`（一覧）/ `GET /api/v1/traces/:id`（trace + spans）。
  span同士に親子関係は無いため「span ツリー」はv0.1では時系列フラットリストに簡略化
- UI: 新規 Prompts タブ（プロンプト作成、バージョン一覧、`diff`パッケージによる
  隣接バージョンの差分表示、ワンクリックでのactivate）。新規 Traces タブ
  （trace一覧→クリックでspan内訳＝kind/duration/tokens/cost/statusを表示）
- **完了条件**: prompt を v2 に切り替えて挙動が変わり、Trace で全過程を追える → 確認済み
  （モックサーバの system prompt に応じて応答を分岐させ、v1では通常の日本語回答、
  v2（"PIRATE_MODE"）に`activate`で切り替えた直後の呼び出しから応答が変化することを
  **コード再デプロイなしで** curl 実証。Trace一覧/詳細APIで rag_chat/search/ingest:embed
  各traceとそのspan内訳（tokens/cost/status）を取得できることも確認。ブラウザ(Playwright)
  でもPromptsタブでのバージョン作成・diff表示・activate、Traces タブでの一覧→詳細展開を
  確認。全Go単体テスト緑（`TestRAGChatUsesActivePromptVersionAndReactsToSwitch`が
  この完了条件そのものを検証）
- **既知の積み残し**（W6スコープ外、次回対応）: `SearchUseCase`（W4実装）のembed/retrieve/
  rerank spanは token数・costが未記録（`llm.Embedder`がusageを返さない設計のため、
  ingestion側のように`Tokenizer.Count`で概算する対応が必要）。Trace UIで実際に
  search系traceのcostが¥0固定で表示されることをE2E確認時に発見。SearchUseCaseの
  コンストラクタ変更が複数箇所に波及するため、W6の変更範囲としては見送り

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
