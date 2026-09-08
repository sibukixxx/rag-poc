# 問い合わせ客へForgeAIデモを発行する

## 仕組み

顧客デモは二段階で保護する。

1. **Cloudflare Access**: 問い合わせで確認した勤務先メールアドレスだけを許可し、メールOTPで本人性を確認する。
2. **ForgeAI**: 顧客ごとの期限付きユーザーIDとランダムパスワードを発行する。パスワードはソルト付きPBKDF2-SHA256ハッシュだけをSQLiteへ保存し、ログイン後はHttpOnly・SameSite=StrictのセッションCookieを使う。

Tunnel単体にはメール許可機能はない。Tunnelで非公開ホストへ接続し、その手前にAccessを置く。

## 1. デモモードを有効化する

`.env`に次を設定して再起動する。

```dotenv
FORGEAI_DEMO_AUTH_ENABLED=true
FORGEAI_REQUIRE_CLOUDFLARE_ACCESS=true
```

`FORGEAI_REQUIRE_CLOUDFLARE_ACCESS=true`では、Accessが付ける`Cf-Access-Authenticated-User-Email`とアカウントのメールが一致しないログイン・セッション利用を拒否する。ローカル確認だけを行う場合は後者を`false`にする。

## 2. 問い合わせを確認する

TechVitの問い合わせ通知で次を確認する。

- ForgeAIデモ希望が`yes`
- 氏名
- 勤務先・組織名
- 勤務先メールアドレス
- 利用目的と試したい資料

フリーメール、共有アドレス、目的不明の申込は自動承認しない。

## 3. 個別アカウントを発行する

Docker Composeで稼働しているホスト上で実行する。

```sh
docker compose exec forgeai forgeai demo-user create \
  -email user@example.co.jp \
  -company "Example株式会社" \
  -ttl 336h
```

出力されるユーザーIDとパスワードを安全な経路で本人へ伝える。平文パスワードはこの出力時しか表示されない。既定の有効期間は14日。

## 4. Cloudflare Accessへ同じメールを許可する

Zero Trust → Access → Applications → ForgeAIのSelf-hosted application → Policiesを開き、AllowポリシーのIncludeへ申込者のメールを追加する。One-time PINをログイン方法として有効化する。

AccessのAPIで自動化する場合に必要な権限は`Access: Apps and Policies Write`。APIトークンやAccount IDをForgeAIのDBやリポジトリへ保存しない。

公式資料:

- https://developers.cloudflare.com/cloudflare-one/integrations/identity-providers/one-time-pin/
- https://developers.cloudflare.com/cloudflare-one/access-controls/policies/
- https://developers.cloudflare.com/api/resources/zero_trust/subresources/access/subresources/applications/subresources/policies/

## 5. 利用状況を確認する

```sh
docker compose exec forgeai forgeai demo-user list
docker compose logs --since=24h forgeai
```

LLM利用量と処理時間はForgeAIのTracesで確認する。顧客ごとの完全なデータ分離はこのデモ認証の対象外であるため、デモ環境には公開可能なサンプル資料だけを置く。

## 6. 終了・緊急停止

```sh
docker compose exec forgeai forgeai demo-user revoke demo-xxxxxxxx
```

この操作でアカウントを無効化し、既存セッションを即時削除する。その後、Cloudflare AccessのAllowポリシーから同じメールアドレスを削除する。二つの操作を完了して失効とする。

## 顧客へ送る案内

```text
ForgeAIデモ環境をご用意しました。

URL: https://forgeai.example.com
ユーザーID: demo-xxxxxxxx
パスワード: （別送）
利用期限: YYYY-MM-DD

最初に勤務先メールアドレスへCloudflareの確認コードが届きます。
確認後、上記のForgeAIアカウントでログインしてください。
アカウントの共有、機密資料・個人情報のアップロードはお控えください。
```
