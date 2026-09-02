# ForgeAI Web

React source for the Chat Playground embedded into the `forgeai` binary
(see `web/embed.go` and `internal/http/static.go`).

```sh
npm install
npm run dev     # dev server on :5173, proxies /api to localhost:8080
npm run build   # writes web/dist — commit it, it's what go:embed ships
```

`web/dist` is committed to the repo so `go build ./cmd/forgeai` works with
no Node toolchain present. Rebuild and commit it after any change under
`web/src`.
