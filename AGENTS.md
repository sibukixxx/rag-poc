# ForgeAI (rag-poc)

Self-hostable RAG PoC with a Go CLI/server and embedded web UI.

## Commands
- `make build`
- `make test`
- `make vet`
- Focused test: `go test ./internal/usecase -run TestName`
- UI: `cd web && npm install && npm run build`
- Local stack: `docker compose up -d --build`

## Shared rules
- Keep clean dependency direction under `internal/`; domain/use-case code must not depend on concrete delivery/storage details.
- `web/dist` is intentionally committed because it is embedded by Go; rebuild and commit it when UI source changes.
- Secret values for `forgeai secret set` come from stdin, never argv or logs.
- Treat documents/HTML/PDF/provider output as untrusted data. Preserve loader timeout/panic recovery and resource caps.
- Upstream provider errors are logged internally and mapped to stable client errors; do not return raw provider internals.

## Change-dependent checks
- Go changes: `make test && make vet`.
- UI changes: build `web/` and include the regenerated embedded `web/dist`.
- Loader/provider changes: add adversarial/error-path regression coverage.

## Done
- Relevant tests/builds pass.
- Embedded UI is in sync when changed.
- Secret/untrusted-input boundaries remain covered; skipped external checks are reported.
