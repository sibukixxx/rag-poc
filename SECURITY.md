# Security Policy

## Reporting Security Issues

**Do not** open public issues for security vulnerabilities. Instead, please email sibukixxx@gmail.com with:

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if available)

We will acknowledge receipt within 48 hours and provide updates within 7 days.

## Security Model at a Glance

v0.1 release evidence, threat model, and data-flow diagram: [docs/security/V0.1_SECURITY_EVIDENCE.md](docs/security/V0.1_SECURITY_EVIDENCE.md).

### Implemented guarantees

- `privacy.mode: local_only` blocks LLM and embedding requests to unapproved destinations before transmission ([docs/PRIVATE_MODE.md](docs/PRIVATE_MODE.md))
- `privacy.outbound_policy` can block or redact documented identifier formats before any provider call ([docs/security/SENSITIVE_DATA_POLICY.md](docs/security/SENSITIVE_DATA_POLICY.md))
- Deleting a document or knowledge base removes every derived artifact, and `forgeai data compact` removes deleted text from the database files ([docs/security/DATA_LIFECYCLE.md](docs/security/DATA_LIFECYCLE.md))
- Provider secrets are AES-GCM encrypted; runtime tokens and demo sessions are stored only as hashes
- Security-relevant actions are audited without secrets, prompts, or document text ([docs/security/AUDIT_TRAIL.md](docs/security/AUDIT_TRAIL.md))

### Operator responsibilities

- Run one ForgeAI instance per customer security boundary (`profile: production` checks this)
- Put the database on an encrypted volume and declare `security.storage_at_rest`
- Put `/api/v1` and the UI behind an authenticating reverse proxy and declare `security.management_boundary`
- Protect `FORGEAI_MASTER_KEY`, provider keys, hosts, logs, and backups
- Choose `local_only` when content must not leave the environment; otherwise set the outbound policy deliberately

### Known limitations

- **v0.1 Alpha**: API and config may change without notice
- **Plaintext content in SQLite**: chunk text, the full-text index, and evaluation answers are not application-encrypted
- **No tenant isolation**: demo accounts share one ForgeAI workspace; use only approved sample data. Production is one instance per customer
- **No built-in production login for the management API**
- **Pattern-based detection only**: the outbound policy does not detect names, addresses, or other free-text personal data
- **CLI actor identity is not authenticated**: CLI audit events carry the OS user name as a label
- **Deletion scope**: backups, snapshots, source systems, and data already sent to a provider are not affected
- **LLM model choice**: providers and models impact security; use trusted models only

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

### Customer Demo Authentication

- Optional application authentication is enabled with `FORGEAI_DEMO_AUTH_ENABLED=true`
- Each customer receives a separate, expiring account; passwords are stored as salted PBKDF2-SHA256 hashes
- Session tokens are random, stored only as SHA-256 hashes, and sent in HttpOnly + SameSite=Strict cookies
- `FORGEAI_REQUIRE_CLOUDFLARE_ACCESS=true` binds each app account to the matching Access-authenticated email
- Revoking a demo user deletes all of that user's active sessions

### SSRF Prevention

- HTTP/HTTPS only (no file://, ftp://, etc.)
- Private IP ranges blocked (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 127.0.0.1, ::1)
- Loopback and link-local (169.254.169.254) rejected
- Cloud metadata endpoints blocked
- Redirect validation: each hop checked before following

### Customer Data Storage and Deletion

- Extracted chunk text, the full-text index, and evaluation answers are stored **in plaintext** in SQLite; only provider secrets are application-encrypted
- Put the database on an encrypted volume before ingesting confidential data and declare it with `security.storage_at_rest: operator_encrypted_volume` (`forgeai doctor` reports the declaration; ForgeAI cannot verify the volume)
- Documents and knowledge bases can be deleted with every derived artifact through the API or `forgeai data`; `forgeai data compact` removes deleted text from the database files
- Traces and evaluation runs can expire via `retention.traces_days` / `retention.evaluation_runs_days`
- Deletion does not reach backups, snapshots, or data already sent to an external provider
- Full inventory: [docs/security/DATA_LIFECYCLE.md](docs/security/DATA_LIFECYCLE.md)

### Outbound Sensitive Data

- `privacy.outbound_policy: deny_sensitive` blocks a provider call when an email, phone, or operator-defined pattern matches; `redact_known_patterns` replaces matches first
- The guard wraps every provider and the embedder, so chat, RAG, rerank, judge, and ingestion share it
- Blocked calls return `422 request blocked by outbound sensitive-data policy`; matches are audited without their values
- Details and detection limits: [docs/security/SENSITIVE_DATA_POLICY.md](docs/security/SENSITIVE_DATA_POLICY.md)

### Audit Trail and Production Profile

- Security-relevant actions (token issue/revoke/rejection, deployment creation, deletions, demo logins, provider invocations, secret changes, server start) are recorded in `audit_events` without secrets, prompts, or document text
- `profile: production` makes `forgeai doctor` fail when demo authentication is on or when the management boundary / encrypted storage is not declared
- v0.1 is single-tenant: one ForgeAI instance per customer security boundary
- Details: [docs/security/AUDIT_TRAIL.md](docs/security/AUDIT_TRAIL.md)

## Deployment Hardening

- Always deploy customer demos behind Cloudflare Access and enable ForgeAI demo authentication
- Run with minimal permissions: read-only database files, write-only to embeddings cache
- Monitor `forgeai doctor` output for configuration issues
- Rotate `FORGEAI_MASTER_KEY` periodically (invalidates all stored secrets)

## Supported Versions

Only the latest release receives security updates. For alpha releases, check our GitHub releases page for patches.
