# ForgeAI (rag-poc)

Self-hosted RAG / AI-app platform shipped as one Go binary (`forgeai`). Spec: `docs/V0.1_SPEC.md`, weekly plan: `docs/ROADMAP.md`. v0.1 = SQLite + local files + OpenAI-compatible LLM/embeddings only.

## Commands

```bash
make build                       # CGO_ENABLED=0 go build -o dist/forgeai ./cmd/forgeai
make test | make vet             # go test ./... (CGO off)
go test ./internal/usecase -run TestName
./dist/forgeai init | doctor | serve | secret set <name>   # -config path optional; secret value comes from stdin, never argv
cd web && npm install && npm run dev     # UI dev; proxies /api → :8080
cd web && npm run build                  # writes web/dist — COMMIT it (go:embed)
docker compose up -d --build             # self-host + Cloudflare Tunnel, see docs/deploy-cloudflare.md
```

Go 1.25 per `go.mod`; always build with `CGO_ENABLED=0` (modernc sqlite is pure Go, binary must stay static).

## Architecture (clean layering under `internal/`)

```
cmd/forgeai → app (bootstrap, server, doctor, wiring)
            → http (chi router, handlers, SSE) → usecase (chat, ingest)
            → domain (types + interfaces only, zero external deps)
adapter/ implements domain interfaces: openaicompat (LLM+Embedder), sqlite (repos + embedded migrations),
         crypto (AES-GCM SecretBox), tokenizer (offline tiktoken + chunker), extractor (TXT/MD/HTML/CSV/JSON/PDF), vecenc
```

- Dependencies point inward: `domain` imports nothing from the repo; `usecase` depends on `domain` interfaces; `adapter` and `http` are wired only in `app`.
- `llm.Router` maps business aliases (`cheap` / `normal` / `judge`) → provider+model. Every LLM/embedding call records a Trace+Span with tokens and cost (`llm.PriceTable`) — keep this when adding calls.
- Config: `config.Default()` → YAML file → `FORGEAI_*` env overrides (`FORGEAI_PORT`, `FORGEAI_DB_PATH`, `FORGEAI_STORAGE_PATH`). Secrets are encrypted at rest with `FORGEAI_MASTER_KEY`; `api_key_env` wins over the secret store when set.
- Migrations: add `internal/adapter/sqlite/migrations/NNNN_name.sql`; they are embedded and applied in order at `sqlite.Open`.
- Ingest dedupes chunks by content hash (text + chunker config + model) so identical re-uploads skip the embedding API.
- `web/dist` is tracked on purpose so `go build` needs no Node.

## Conventions

- HTTP surface: management API under `/api/v1`, runtime API `/runtime/v1` (later weeks); everything else serves the embedded SPA.
- No app-level auth yet — public deployments must sit behind Cloudflare Access. `router.go` adds the compensating controls: body caps (1 MiB JSON / 32 MiB upload), `Sec-Fetch-Site` CSRF guard, throttle, CSP/frame headers, CF-Connecting-IP only. Keep new routes inside `r.Route("/api/v1")` so they inherit them.
- Untrusted input hardening: loaders run under `loadWithGuard` (30 s timeout + panic recovery); PDF pages are capped; HTML walk is iterative. Upstream provider errors are logged, never returned to clients (`readAPIError`).
- Secrets: `SecretBox.Seal/Open` take the secret name as AAD; changing a name invalidates its ciphertext.
- `.github/workflows` cannot be pushed from Claude sessions; CI template lives in `docs/ci-workflow.yml`.
