# Security Policy

## Reporting Security Issues

**Do not** open public issues for security vulnerabilities. Instead, please email sibukixxx@gmail.com with:

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if available)

We will acknowledge receipt within 48 hours and provide updates within 7 days.

## Security Considerations

### Input Validation & Bounds

- **PDF files**: Limited to 2000 pages to prevent parser DoS
- **HTML files**: Iterative walk (not recursive) to prevent stack overflow
- **JSON requests**: 1 MiB limit for API requests
- **File uploads**: 32 MiB limit for uploaded files
- **Message counts**: Limited to 64 messages per chat session

### Secret Management

- Secrets are encrypted at rest with AES-GCM using `FORGEAI_MASTER_KEY`
- Secret name is bound as Additional Authenticated Data (AAD) so ciphertexts cannot be swapped
- CLI `secret set` reads from stdin (never command-line args) to avoid history leakage
- Use `term.ReadPassword()` for hidden input

### Parser Hardening

- **PDF extraction**: Catches panics and enforces page limits
- **HTML extraction**: Non-recursive walk to prevent stack exhaustion
- **CSV/JSON parsing**: Size capped before parsing

### LLM Integration

- Third-party content (website scrapes, chat replies) wrapped in `<untrusted_content>` tags
- Output capped at 2048 tokens server-side
- Provider errors logged but never returned to clients
- Cost tracking via `llm.PriceTable` to detect runaway calls

### Network & CSRF

- CSRF protection via `Sec-Fetch-Site` header checking
- HTTP body size limits enforced
- CSP and X-Frame-Options headers set
- `CF-Connecting-IP` trusted only when running behind Cloudflare

### SSRF Prevention

- HTTP/HTTPS only (no file://, ftp://, etc.)
- Private IP ranges blocked (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 127.0.0.1, ::1)
- Loopback and link-local (169.254.169.254) rejected
- Cloud metadata endpoints blocked
- Redirect validation: each hop checked before following

## Deployment Hardening

- Always deploy behind Cloudflare Access (no app-level authentication yet)
- Run with minimal permissions: read-only database files, write-only to embeddings cache
- Monitor `forgeai doctor` output for configuration issues
- Rotate `FORGEAI_MASTER_KEY` periodically (invalidates all stored secrets)

## Known Limitations

- **v0.1 Alpha**: API and config may change without notice
- **No multi-tenancy**: Single master key for all secrets
- **No request authentication**: Rely on Cloudflare Access in production
- **LLM model choice**: Providers and models impact security; use trusted models only

## Supported Versions

Only the latest release receives security updates. For alpha releases, check our GitHub releases page for patches.
