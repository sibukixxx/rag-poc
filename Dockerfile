# syntax=docker/dockerfile:1
#
# ForgeAI を単一バイナリのコンテナにする。
# - Cloudflare Tunnel(無料)で自前ホストを公開する場合: docker-compose.yml から利用
# - Cloudflare Containers(Workers Paid)に載せる場合: このファイルをそのまま image に指定
# 詳細: docs/deploy-cloudflare.md

FROM golang:1.25-alpine AS build
WORKDIR /src

# 依存だけ先に取得してレイヤーキャッシュを効かせる
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
# modernc.org/sqlite は pure Go なので CGO 不要 → 静的バイナリになる
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X github.com/sibukixxx/rag-poc/internal/app.Version=${VERSION}" \
      -o /out/forgeai ./cmd/forgeai \
 && mkdir -p /out/data

# 実行イメージ: シェル無し・非 root(uid 65532)
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/forgeai /app/forgeai
# /data は名前付きボリュームにマウントする想定。イメージ側に非 root 所有で作っておくと
# 初回マウント時にその所有権がボリュームへ引き継がれる。
COPY --from=build --chown=65532:65532 /out/data /data

ENV FORGEAI_PORT=8080 \
    FORGEAI_DB_PATH=/data/forgeai.db \
    FORGEAI_STORAGE_PATH=/data/files

VOLUME ["/data"]
EXPOSE 8080

ENTRYPOINT ["/app/forgeai"]
CMD ["serve"]
