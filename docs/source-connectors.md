# 外部データ取り込み基盤

## 目的

ForgeAIではCSV、PDF、Slack、Chatwork、Jiraなどを別々の検索基盤として実装しない。各サービス固有のAPIレスポンスを共通の`source.Document`へ変換し、既存の正規化・チャンク分割・埋め込み・検索パイプラインへ渡す。

```text
Provider API → Connector.Pull → source.Document → IngestText → Chunk / Embedding / FTS
                         ↓
             Cursor / Source item / Sync job
```

## レイヤーの責務

| レイヤー | 責務 |
|---|---|
| Connector | API呼び出し、ページング、サービス固有形式からプレーンテキストへの変換 |
| SourceSyncUseCase | 差分カーソル、重複判定、更新・削除、再試行可能な同期処理 |
| SourceStore | 接続、外部IDとForgeAI文書の対応、同期ジョブの永続化 |
| IngestUseCase | Unicode正規化、チャンク分割、埋め込み再利用、FTS登録 |

ConnectorはSQLiteや`knowledge.Store`を直接操作しない。認証情報も`config_json`へ保存せず、`secret_name`でForgeAIの暗号化Secret Storeを参照する。

## 共通データモデル

Connectorは次を返す。

- `ExternalID`: 接続内で不変かつ一意なID
- `Title` / `Body`: 検索対象となるテキスト
- `SourceURL`: 元のSlackスレッドやJira課題へ戻るURL
- `Metadata`: チャンネル、プロジェクト、作成者などの検索・表示用属性
- `Visibility`: 将来ACL判定に使うグループ・チャンネル識別子
- `UpdatedAt`: 外部サービス上の更新時刻
- `Deleted`: 元データが削除されたことを表すtombstone

`Visibility`は現時点では保存だけを行い、検索時の認可にはまだ使用しない。企業データを扱う場合は、顧客ごとにForgeAI環境を分離する。複数顧客を同一環境へ入れるのはACL-aware retrieval実装後とする。

## 同期保証

1. カーソルは1バッチの全アイテムが処理された後だけ進める。
2. バッチ途中で失敗した場合、同じバッチを再取得する。
3. `connection_id + external_id`で同じ元データを識別する。
4. 内容ハッシュが同じ場合は再チャンク・再埋め込みを行わない。
5. 更新時は新しい文書の取り込み成功後に対応表を切り替え、古い文書・Chunk・Embedding・FTSを削除する。
6. 削除tombstoneを受け取った場合は検索対象から即時に取り除く。
7. 全同期は`source_sync_jobs`へ件数とエラーを記録する。

## Connector実装条件

新しいConnectorは`source.Connector`を実装し、アプリ起動時にRegistryへ登録する。

```go
type Connector interface {
    Provider() string
    Pull(ctx context.Context, connection Connection, cursor string) (Batch, error)
}
```

実装時の必須条件:

- 読み取り専用・最小権限を初期値にする
- DM、非公開チャンネル、全プロジェクトを既定で取得しない
- 許可対象を`connection.Config`で明示する
- APIの429と一時障害は再試行可能なエラーとして返す
- `HasMore=true`では必ず`NextCursor`を進める
- 本文サイズは32 MiB以下にする
- APIトークンをログ、DB、同期エラーへ含めない

## ファイルシステム / NAS ソース（#30）

数千〜数十万ファイルのディレクトリを、ブラウザを開いたままにせずサーバー側で取り込む。

```yaml
sources:
  filesystem:
    allowed_roots: [/srv/share, /mnt/nas]   # ここ以外は登録できない。空なら無効
    workers: 4                              # 同時処理ファイル数
    max_file_bytes: 33554432                # これより大きいファイルは失敗扱い
```

```bash
forgeai source add-fs -kb handbook -exclude drafts/ -exclude '*.tmp' /srv/share/handbook
forgeai source sync <connection-id>        # 前景で実行し進捗を表示
forgeai source job <job-id>                # 進捗と失敗ファイル
forgeai source pause|cancel|resume <job-id>
```

HTTP API は `POST /api/v1/source-connections`（`provider: "filesystem"`）、`POST /api/v1/source-connections/{id}/jobs`、`GET /api/v1/ingestion-jobs/{id}`、`POST /api/v1/ingestion-jobs/{id}/pause|cancel|resume`。API から開始したジョブはサーバー内のランナーが 1 件ずつ実行し、`forgeai serve` 起動時に中断・待機中のジョブを再開する。

### 挙動

| 項目 | 挙動 |
|---|---|
| 識別子 | 接続 + ルートからの相対パス（`source_items.external_id`）。引用のファイル名も相対パスになる |
| 走査 | ディレクトリのメタデータだけで列挙し、中身は読まない。1 万ファイルをファイルを開かずに列挙できることをテストで確認 |
| 除外（既定） | `.env*`、`*.pem`、`*.key`、`id_rsa*`、`credentials*`、`.git/`、`.ssh/`、`.aws/` など。件数は `scan.excluded_sensitive` |
| 除外（運用者） | `-include` / `-exclude`。`dir/` はディレクトリ接頭辞、それ以外は `path.Match` をパスとファイル名に適用 |
| シンボリックリンク | 辿らない（件数のみ記録）。読み取りは `os.Root` 経由で、ルート外へは到達できない |
| 未変更ファイル | サイズと mtime が同じなら読まない。内容ハッシュが同じなら再埋め込みしない |
| 更新 | 新しい文書の取り込み成功後に旧文書・チャンク・埋め込み・FTS を置き換える |
| 削除 | 走査が最後まで完了したジョブでのみ、消えたファイルを tombstone にして検索対象から外す |
| 失敗 | 抽出不能ファイルは 1 回で失敗、一時的エラーは最大 3 回リトライ。1 件の失敗でジョブは止まらない |
| 中断・再開 | ファイル単位の状態を `ingestion_job_items` に保存。強制終了後も完了済みファイルは再処理せず、途中だった文書は片付けてから再処理する |
| 一時停止・中止 | 処理中バッチの完了後に反映する |

分散キューや複数ノードのワーカーは対象外。現行の SQLite 総当たりベクトル検索は数百万チャンク規模を想定していない。

## 次の実装

最初の実コネクタはSlack読み取り専用とする。

1. OAuthと暗号化トークン保存
2. 接続可能チャンネルの一覧取得
3. 管理者による対象チャンネルのallowlist
4. 初回履歴同期とスレッド単位の`source.Document`生成
5. Events APIによる追加・更新・削除
6. 定期差分同期による取りこぼし回収
7. 同期状態と元メッセージURLの管理画面表示
