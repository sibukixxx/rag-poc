# Security audit trail and single-tenant production profile (v0.1)

This document covers #24. The executable contract is `internal/domain/audit`, the `audit_events` table, and the tests named below.

## Single-tenant production profile

ForgeAI v0.1 is **single-tenant**. One production ForgeAI instance, with its own database and master key, serves one customer security boundary. ForgeAI does not implement tenant isolation.

Demo accounts (`FORGEAI_DEMO_AUTH_ENABLED`) are for approved sample-data demos. All demo accounts share one workspace. They are **not** tenant isolation and must not be described as such.

Enable the production profile to make `forgeai doctor` enforce this:

```yaml
profile: production            # or FORGEAI_PROFILE=production
security:
  management_boundary: reverse_proxy_identity
  storage_at_rest: operator_encrypted_volume
```

With `profile: production`, `forgeai doctor` fails when:

| Check | Fails when |
|---|---|
| Production tenancy | demo authentication is enabled |
| Production management boundary | `security.management_boundary` is not declared |
| Production storage at rest | `security.storage_at_rest` is not declared |

`forgeai serve` logs the same failures as warnings at startup. Both declarations are operator statements. ForgeAI cannot verify a reverse proxy or disk encryption.

### Network and identity assumptions

- The management UI and `/api/v1` have no built-in production login. Put them behind an authenticating reverse proxy (Cloudflare Access, an OAuth2 proxy, an SSO gateway) inside the customer's boundary.
- `/runtime/v1` is authenticated per request with deployment-scoped Bearer tokens and may be exposed separately.
- Outbound provider traffic is governed by Private Mode (`docs/PRIVATE_MODE.md`) and the sensitive-data policy (`docs/security/SENSITIVE_DATA_POLICY.md`).
- `CF-Connecting-IP` is trusted as the client address. Only deploy with that header reachable through Cloudflare.

## What is recorded

Every event has `id`, `occurred_at`, `action`, `outcome` (`success`, `failure`, `denied`), `actor`, `target`, and string `metadata`.

| Action | When | Metadata |
|---|---|---|
| `auth.login` | demo login attempt | `reason` on failure (`invalid_credentials`, `account_unavailable`) |
| `runtime_token.issue` | token created | `token_id`, `token_name`, `token_fingerprint` |
| `runtime_token.revoke` | token revoked | `token_id` |
| `runtime_token.rejected` | Runtime request refused | `reason`, `token_fingerprint` of the presented token |
| `deployment.create` | Deployment created | `slug`, `knowledge_base_id`, `alias`, `prompt_version`, `top_k`, `rerank` |
| `document.delete`, `knowledge_base.delete` | deletion attempted | removed counts, or `reason` when refused |
| `provider.invoke` | LLM or embedding request | `provider`, `model`, `operation`, `endpoint` (origin only), `endpoint_class`, `inputs` for embeddings |
| `egress_policy.apply` | an outbound sensitive-data rule matched | `policy`, `matches` (rule counts), `operation`, `provider`, `model` |
| `server.start` | `forgeai serve` starts | `version`, `profile`, `privacy_mode`, `outbound_policy`, allowed destinations and provider origins |
| `source_connection.create` | a filesystem source is registered | `provider`, `knowledge_base_id`, `root`, `include`, `exclude` |
| `ingestion_job.control` | a bulk job is started, paused, cancelled, resumed, or reconciles removed files | `request`, `connection_id` or `deleted` |
| `source_connection.authorize` | an OAuth consent completes, is denied, uses an invalid state, or a grant needs reauthorization | `oauth_provider` or `reason` |
| `source_connection.control` | a connection is enabled, disabled, synced, or disconnected | `request`, `documents_removed` |
| `secret.set`, `secret.delete` | CLI changed a stored secret | target is the secret name |

Successful Runtime authentications are not recorded one by one. The provider calls they cause are recorded with the runtime token ID as actor.

## What is never recorded

- API keys, secret values, passwords, session cookies, or runtime token plaintext. Tokens appear only as a 12-character SHA-256 fingerprint, which matches the prefix of the hash ForgeAI stores.
- Prompts, questions, retrieved chunks, answers, or document text.
- Provider URL paths, query strings, or userinfo. Only the origin is kept.

Tests: `TestDeploymentAuditRecordsCreateIssueAndRevokeWithoutTokenPlaintext`, `TestRuntimeAuthenticateRecordsRejectionWithFingerprintOnly`, `TestDemoLoginAuditRecordsSuccessAndFailureWithoutPassword`, `TestLLMGenerateRecordsProviderModelAndEndpointWithoutPrompt`, and the audit step of `TestAcceptanceE2EMockProviderJourney`.

## Actors and their limits

| Actor | Meaning |
|---|---|
| `demo_user:<username>` | signed-in demo account |
| `http:<address>` | management request without a ForgeAI login; the address is the client IP or `CF-Connecting-IP` |
| `runtime_token:<token id>` | Runtime API request |
| `cli:<os user>` | local CLI command. The OS user name is a label, not an authenticated identity |
| `system` | server-internal action such as startup |

When the management surface sits behind a reverse proxy, the proxy's own logs hold the authenticated identity. ForgeAI records the client address it sees. The audit trail is evidence for investigation, not non-repudiation.

## Reading and retention

```bash
forgeai audit list [-limit 100]
curl http://localhost:8080/api/v1/audit-events?limit=100
```

Audit retention is separate from traces:

```yaml
retention:
  audit_days: 400   # 0 (default) keeps audit events
```

`forgeai data retention` applies it. `TestAuditStoreRetentionDeletesOnlyOldAuditEvents` shows it never deletes ordinary product data.

A failed audit write is logged and does not change the result of the audited operation.
