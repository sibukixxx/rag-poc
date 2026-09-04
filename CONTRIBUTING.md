# Contributing to ForgeAI

We welcome contributions! This project is in alpha, so expect rapid changes and refactoring.

## Development Setup

```bash
git clone https://github.com/sibukixxx/rag-poc.git
cd rag-poc
make build
make test
```

## Building

```bash
CGO_ENABLED=0 go build -o dist/forgeai ./cmd/forgeai
```

The binary must be built with `CGO_ENABLED=0` to remain static.

## Testing

```bash
make test      # Run all tests
make vet       # Run go vet
```

## Code Style

- Run `go fmt ./...` before committing
- Use `go vet ./...` to catch issues
- Keep packages focused and respect the layered architecture
  - `domain/` — interfaces only
  - `usecase/` — business logic
  - `adapter/` — external implementations
  - `http/` / `cmd/` — endpoints and CLI

## Adding Features

When adding a new feature, update:

1. `internal/domain/` interfaces if needed
2. `internal/adapter/` implementations
3. `internal/usecase/` business logic
4. `internal/http/handler/` routes
5. `web/` UI (if applicable)
6. Tests for each layer

## Security

This project handles sensitive data (LLM API keys, embeddings). Before submitting:

- Review `SECURITY.md` for hardening guidelines
- Ensure no secrets leak into logs or error messages
- Test with malformed input (corrupted PDFs, oversized files)

## Reporting Issues

- Check existing issues first
- Provide reproduction steps and environment details
- For security issues, see `SECURITY.md`

## License

By contributing, you agree your code will be licensed under the MIT License.
