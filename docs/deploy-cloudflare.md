# Cloudflare へのデプロイ(最安構成)

## 結論

ForgeAI は **Cloudflare Workers(無料枠)にはそのまま載らない**。理由:

- Go の単一バイナリで、長時間動く HTTP サーバとして動作する(Workers は JS / WASM の isolate で、1 リクエスト単位で起動・終了する)
- SQLite ファイルとアップロードファイルをローカルディスクに書く(Workers には永続ファイルシステムが無い)
- `modernc.org/sqlite` は WASM ターゲットで動作せず、TinyGo でも大きすぎる

したがって選択肢は次の 3 つ。**最安値は 1 の Cloudflare Tunnel(Cloudflare 側 $0)**。

| # | 方式 | Cloudflare 側の月額 | 備考 |
|---|---|---|---|
| 1 | **Cloudflare Tunnel + Cloudflare Access(自前ホスト)** ← 採用 | **$0** | 手元 PC / 既存 VPS / Raspberry Pi 等で `docker compose up` し、`cloudflared` が Cloudflare へアウトバウンド接続して公開する。ポート開放・固定 IP 不要。Access(Zero Trust Free、50 ユーザーまで)でメール OTP 認証を前置できる |
| 2 | Cloudflare Containers | **$5〜**(Workers Paid 必須)+ vCPU/メモリ/ディスク従量 | `Dockerfile` をそのまま動かせるが、コンテナのディスクは**揮発性**(再起動で SQLite が消える)。R2 へのバックアップ実装が別途必要 |
| 3 | Workers + D1 + Vectorize + R2 に移植 | $0(無料枠内) | Go → TypeScript の全面書き換え。v0.1 の「単一バイナリ配布」という設計方針と矛盾する |

ホスト費用は 1 でも発生しうるが、手元 PC や既存サーバを使えば追加 $0。新規に借りる場合でも Oracle Cloud Always Free / 各社の最安 VPS(数百円/月)で足りる(ForgeAI の常駐メモリは 100MB 未満)。

## 必要なもの(方式 1)

1. Cloudflare アカウント(無料)と、Cloudflare にネームサーバを向けたドメイン 1 つ(Tunnel の公開ホスト名に使う。`*.workers.dev` のような無料サブドメインは Tunnel では使えない)
2. Docker(Compose v2)が動くホスト。ForgeAI の`web/dist` はコミット済みなので Node.js は不要
3. `forgeai init` で生成するマスターキーと、OpenAI 互換 API のキー

## 手順(方式 1)

### 1. Tunnel を作成してトークンを取得

Cloudflare ダッシュボード → **Zero Trust** → **Networks** → **Tunnels** → **Create a tunnel** → Cloudflared を選び、名前(例 `forgeai`)を付ける。
表示されるインストールコマンドの末尾にある `eyJ...` がトンネルトークン。これを控える。

### 2. Public hostname を設定

同じ画面の **Public Hostname** タブで:

| 項目 | 値 |
|---|---|
| Subdomain / Domain | `forgeai` / あなたのドメイン |
| Type | `HTTP` |
| URL | `forgeai:8080` ← docker compose 内のサービス名:ポート |

### 3. Access で認証を前置する(必須)

ForgeAI v0.1 の API(`/api/v1/*`)には**アプリ側の認証がまだ無い**(セッション認証は docs/ROADMAP.md の後続週で実装予定)。公開したままだと誰でもチャットして LLM 費用を消費できるので、Access で保護する。

Zero Trust → **Access** → **Applications** → **Add an application** → Self-hosted:

- Application domain: `forgeai.<あなたのドメイン>`
- Policy: Allow / Include → Emails → 自分のメールアドレス(または Emails ending in → 自社ドメイン)

これで初回アクセス時にメール OTP が求められ、許可したメールアドレス以外は到達できなくなる。

### 4. ホスト側で起動

```sh
git clone https://github.com/sibukixxx/rag-poc.git && cd rag-poc
cp .env.example .env
make build && ./dist/forgeai init        # マスターキーを生成(Go が無ければ後述の docker run で代用)
# .env に FORGEAI_MASTER_KEY / FORGEAI_OPENAI_API_KEY / TUNNEL_TOKEN を記入
docker compose up -d --build            # または make up
docker compose logs -f cloudflared      # "Registered tunnel connection" が出れば接続済み
```

Go が無いホストでマスターキーを作るには:

```sh
docker build -t forgeai:local . && docker run --rm forgeai:local init -config /tmp/forgeai.yaml
```

`https://forgeai.<あなたのドメイン>/api/v1/health` が `200` を返せば完了。データは名前付きボリューム `forgeai-data`(`/data`)に永続化される。

### 5. 更新

```sh
git pull && docker compose up -d --build
```

### バックアップ

SQLite とアップロードファイルはすべて `/data` にある。

```sh
docker run --rm -v rag-poc_forgeai-data:/data -v "$PWD":/backup alpine \
  tar czf /backup/forgeai-data-$(date +%Y%m%d).tgz -C /data .
```

## ローカルで Docker イメージだけ試す

```sh
make docker-build
docker run --rm -p 8080:8080 -e FORGEAI_MASTER_KEY=$(openssl rand -base64 32) forgeai:local
curl localhost:8080/api/v1/health
```

## 方式 2: Cloudflare Containers に載せる場合(参考)

Workers Paid プラン($5/月)に加入した上で、次のような Worker を追加する(未実装・未検証の参考例)。

```jsonc
// wrangler.jsonc
{
  "name": "forgeai",
  "main": "worker/index.ts",
  "compatibility_date": "2026-08-01",
  "containers": [
    { "class_name": "ForgeAI", "image": "./Dockerfile", "max_instances": 1, "instance_type": "lite" }
  ],
  "durable_objects": { "bindings": [{ "name": "FORGEAI", "class_name": "ForgeAI" }] },
  "migrations": [{ "tag": "v1", "new_sqlite_classes": ["ForgeAI"] }]
}
```

```ts
// worker/index.ts
import { Container, getContainer } from "@cloudflare/containers";
export class ForgeAI extends Container {
  defaultPort = 8080;
  sleepAfter = "10m";
  envVars = { FORGEAI_MASTER_KEY: "...secret から注入..." };
}
export default {
  fetch: (req: Request, env: { FORGEAI: DurableObjectNamespace<ForgeAI> }) =>
    getContainer(env.FORGEAI, "singleton").fetch(req),
};
```

注意点:

- コンテナのディスクは揮発性。`sleepAfter` で停止すると SQLite もアップロードも消えるため、起動時に R2 から復元・定期的に R2 へ退避する処理が必要
- `lite` インスタンス(256MiB)で常時稼働させると月あたり数ドルの従量が Paid 基本料に上乗せされる

「最安値」を優先するなら方式 1 を選び、Containers は複数ユーザーに常時提供する段階で再検討する。
