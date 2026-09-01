# ForgeAI 設計検証（Design Review）

対象: ForgeAI 構想ドキュメント（Self-hosted AI Application / RAG / Agent Platform, Single Binary Distribution）
日付: 2026-09-01
結論: **方向性は妥当。ただし v0.1 の定義に矛盾が1つ、実装前に潰すべき技術リスクが4つある。**

---

## 1. 総評

以下の骨格はそのまま採用してよい。

- Go 単一バイナリ + React embed という配布形態（PocketBase / n8n / Gitea が実証済みのモデル）
- Knowledge / AI Runtime / Workflow / Evaluation の4レイヤー分離
- LLM・Embedding・VectorStore をすべて interface にする Hexagonal 寄りの構成
- OpenAI-compatible endpoint を最優先で実装する方針
- Evaluation を追加機能ではなくコア機能として最初から持つ方針（最大の差別化）
- Embedded（SQLite）/ Production（PostgreSQL + pgvector/Qdrant）の2モード
- Agent / MCP / Human-in-the-Loop を後回しにする判断

競合ポジションも成立する。Dify / RAGFlow / Flowise / AnythingLLM はいずれも
docker-compose 前提の多コンテナ構成が主流で、「バイナリ1個を顧客先に持ち込んで
その場で立ち上がり、Golden Dataset 評価と Before/After 比較まで内蔵」という
組み合わせは空いている。ただし Dify にも評価機能自体はあるため、差別化の主張は
「評価がある」ではなく「**評価→回帰テスト→成果報告（品質/コスト/レイテンシの数値）
までが導入の標準フローに組み込まれている**」に置くこと。

---

## 2. 発見事項（Findings）

### F-1. v0.1 スコープの矛盾【要修正・設計判断】

§35 の v0.1 表には Workflow DAG + 4種の Node が含まれているが、§37 の完成条件

> PDF投入 → RAG作成 → 50問Golden Dataset → 評価 → Prompt/検索設定変更 → Before/After比較 → APIデプロイ

には Workflow が一切登場しない。この完成条件が正しい（売り物として一本筋が通る）ので、
**Workflow Engine は v0.2 に送る**。v0.1 は「評価可能な RAG 実行基盤」に絞る。
これで v0.1 の実装量が体感で 3〜4 割減る。

### F-2. Phase 順序の依存関係バグ【要修正】

§36 では Trace / Token / Cost / Latency が Phase 5 だが、§18 の Experiment 比較
（Phase 3）は「Latency 1.8s→1.2s、Cost $0.042→$0.018」を出すことに価値がある。
つまり **評価はテレメトリに依存する**。トークン数・コスト・所要時間の計測は
Phase 5 まで待てず、**Phase 1 の LLM アダプタ層に最初から埋め込む**必要がある
（`GenerateResponse` に usage を必ず持たせ、呼び出し1回ごとに記録する）。
後付けでの計装は全アダプタの改修になるため、ここは前倒しが正解。

### F-3. Prompt Registry が v0.1 表から漏れている【要修正】

Before/After 比較は「Prompt v7 と v8 を同じ Golden Dataset で走らせる」ことなので、
Prompt のバージョン管理が無いと v0.1 の完成条件自体が満たせない。
§20 の Prompt Registry の最小版（名前 + 連番バージョン + 本文、UI は一覧と diff 程度）
を v0.1 に含める。

### F-4. 日本語の FTS/BM25 は素の SQLite FTS5 では機能しない【重大・技術リスク】

Hybrid Search の Keyword 側を SQLite FTS5 で実装する場合、デフォルトの
unicode61 トークナイザは日本語を分かち書きできず、BM25 が実質的に死ぬ。
日本市場向け製品としては致命的。対応は次のいずれか:

- FTS5 の **trigram トークナイザ**（SQLite 3.34+、modernc.org/sqlite でも利用可）を使う
- インデックス時に自前で **文字 bigram に分割**してから FTS5 に入れる

v0.1 では trigram を採用し、Ingestion 時に正規化（NFKC、全半角統一）を必ず通す。
検索品質の評価ケース（日本語クエリ）を最初の Golden Dataset に含めて回帰で守る。

### F-5. 純 Go での PDF テキスト抽出は品質が低い【最大の技術リスク】

Go の PDF 抽出ライブラリ（ledongthuc/pdf, dslipak/pdf 等）は、表・2段組・
ルビ・縦書き・スキャン PDF に弱い。unipdf は商用ライセンス。ここは製品の入口
（顧客の PDF を食わせる）なので、期待値管理と逃げ道の両方が要る:

- Loader を interface 化し、**外部コンバータを差し込める設計**にする
  （例: docling / pymupdf をサイドカー or HTTP API として任意接続）
- v0.1 の正式サポートは「テキスト系（TXT/MD/HTML/CSV/JSON）+ ベストエフォート PDF」
  とし、**「PDF は事前に Markdown 化して投入する」経路を第一級で用意**する
- OCR は明示的に非対応と明記（v0.3 以降）

### F-6. Single Binary を守るなら CGO_ENABLED=0 を最初に決める【設計制約】

クロスコンパイル4ターゲット配布（linux/amd64, linux/arm64, darwin/arm64, windows）
を売りにするなら CGO は使えない。帰結:

- SQLite は **modernc.org/sqlite**（純 Go、FTS5 対応）一択
- sqlite-vec 等の C 拡張は使えない → Embedded モードのベクトル検索は
  **Go 実装のブルートフォース（コサイン類似度）**で行く
- 目安: 1536次元 × float32 で 6KB/chunk。10万 chunk ≒ 600MB RAM。
  **Embedded モードの想定上限を「〜5万 chunk」とドキュメントに明記**し、
  それ以上は pgvector/Qdrant へ誘導する。PoC 用途にはこれで十分

### F-7. Reranker の実体を決めていない【要具体化】

v0.1 にはローカルでクロスエンコーダを動かす実行系がない。`"rerank": true` は
**LLM ベースの listwise rerank**（候補20件をプロンプトで並べ替え）として実装し、
Reranker interface を切っておいて Cohere Rerank / Voyage 等の API アダプタを
v0.2 以降に追加する。rerank はコストを増やすのでデフォルト off。

### F-8. コストは API から返ってこない【要具体化】

OpenAI 互換 API は usage（トークン数）は返すが金額は返さない。
**モデル別単価テーブルを config に持ち**（入出力別 / 1M tokens、通貨換算レート付き）、
Trace 記録時に計算する。§34 の「¥1.8/request」を出すために必須。
非 OpenAI モデルのトークン数は近似になる旨を UI に注記する。

### F-9. DB スキーマが v0.1 には過剰【要削減】

§24 の24テーブルのうち、v0.1 に必要なのは約14。
workspaces / users / RBAC / audit_logs / workflow系4 / agents / tools / feedback は
後送り（single-user 前提で開始）。ただし **secrets の AES-GCM 暗号化だけは day 1**
（後から平文を暗号化に移行するのは事故のもと）。削減後のスキーマは
`V0.1_SPEC.md` に記載。

### F-10. ディレクトリ構成は一段浅くして開始【推奨】

§3 の構成は最終形としては良いが、v0.1 で domain 8 パッケージ +
usecase + adapter + infrastructure を全部切るとファイル移動ばかりになる。
`internal/{domain, usecase, adapter, http, app}` の3+2層で開始し、
infrastructure は adapter に統合。interface 境界（LLM / Embedder /
VectorStore / DocumentStore）さえ守れば後で分割できる。

### F-11. その他の確認済み事項（問題なし）

- `//go:embed web/dist/*` での React 埋め込み: 標準機能、問題なし
- SSE ストリーミング: net/http 標準で可。Go 1.22+ の ServeMux でルーティングは足りるが、ミドルウェア量を考え chi 採用を推奨（依存は最小限に）
- chunk の Hash による embedding 再生成スキップ: 正しい。hash は「正規化後テキスト + chunker設定 + embedding モデル名」から取ること（chunker やモデルを変えたら再生成が走るように)
- tiktoken 互換のトークンカウント: pkoukk/tiktoken-go で可
- LLM Router（cheap/normal/judge のエイリアス）: そのまま採用。Workflow/評価側は必ずエイリアス参照とする
- 管理 API（/api/v1）と Runtime API（/runtime/v1）の分離: 正しい。認証も別体系（管理=セッション、runtime=APIトークン）にする
- Evaluation Result に Judge Model / Prompt Version / 実行日 / 理由 を保存: 正しい。judge 自身の再現性のため judge プロンプトもバージョン管理対象に含める

---

## 3. 修正後の v0.1 定義

**v0.1 = 「評価可能な RAG 実行基盤」**（Workflow / Agent / RBAC を含まない）

完成条件（受け入れテストそのもの）:

1. `./forgeai serve` 単体で localhost:8080 に管理画面が立つ（外部依存ゼロ）
2. PDF / MD / TXT を投入して Knowledge Base を作れる
3. Hybrid Search（vector + trigram FTS、RRF マージ）が日本語クエリで動く
4. 引用付き RAG チャットが Playground と Runtime API の両方で動く
5. 50問の Golden Dataset（JSON/CSV）を投入し、Retrieval 評価（Recall@K / MRR / Hit Rate）と LLM Judge 評価（Correctness / Groundedness）が走る
6. Prompt または検索設定を変更して再評価し、**品質 / コスト / レイテンシの Before/After 比較画面**が出る
7. Deployment（prompt vN + model alias + retriever 設定のスナップショット）を発行し、`/runtime/v1/apps/:slug/chat` を API トークンで呼べる
8. すべての実行に Trace（span / トークン / コスト / レイテンシ）が付き、UI で追える

v0.2 以降: Workflow DAG(+HTTP/Condition Node) → Human Approval → PostgreSQL/pgvector →
RBAC/Audit → Agent Runtime / Tools / MCP → コネクタ（Drive/Notion/Slack）→ テンプレート配布。

詳細スコープは `V0.1_SPEC.md`、週次計画は `ROADMAP.md` を参照。
