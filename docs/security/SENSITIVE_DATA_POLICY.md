# Outbound sensitive-data policy (v0.1)

This document covers #25. It applies when ForgeAI sends text to an LLM or embedding provider. The executable contract is `internal/domain/outbound`, `internal/adapter/llmguard`, and the tests named below.

## Choose the right control

| Requirement | Use |
|---|---|
| Customer content must not leave the environment | Private Mode, `privacy.mode: local_only` (`docs/PRIVATE_MODE.md`) |
| An external provider is acceptable, but known identifier formats must not be sent | `outbound_policy: deny_sensitive` or `redact_known_patterns` |
| An external provider is acceptable for this data as-is | `outbound_policy: allow` (explicit choice; says nothing about privacy) |

The policy is independent of the provider. It can be combined with Private Mode.

## Configuration

```yaml
privacy:
  outbound_policy: redact_known_patterns   # allow (default) | deny_sensitive | redact_known_patterns
  sensitive_detectors: [email, phone]      # omit for both; [] disables builtins
  sensitive_rules:                         # optional, RE2 syntax
    - name: employee_id
      pattern: 'EMP-\d{6}'
    - name: customer_account
      pattern: 'CUST-[A-Z]{2}\d{8}'
```

`FORGEAI_OUTBOUND_POLICY` overrides the YAML policy. `forgeai doctor` prints the active policy, detectors, and rule names.

Invalid configuration stops startup: an unknown policy, an unknown builtin, a malformed or empty pattern, a pattern that matches the empty string, or a duplicate rule name. A broken rule never degrades to `allow`.

## Behaviour

- **deny_sensitive**: if any rule matches, the provider is not called. The API returns `422` with `request blocked by outbound sensitive-data policy`. Ingesting a matching document marks it `failed`.
- **redact_known_patterns**: each match is replaced with `[REDACTED:<rule>]` in a copy of the request before the provider adapter serializes it. Stored chunks stay unchanged locally.
- **allow**: text is sent unchanged.

Every match is recorded as an `egress_policy.apply` audit event with the policy, the rule names and counts (`email=1,phone=2`), the operation, provider, and model. The matched value is never recorded.

## Coverage

The guard wraps every provider in the router and the embedder, below all use cases. These paths cannot bypass it:

| Path | Provider call |
|---|---|
| Ingest (upload, `forgeai ingest`, source sync) | embeddings of chunks |
| Search | query embedding; LLM rerank prompt |
| RAG chat (management and Runtime) | question plus retrieved context |
| Plain chat | all messages |
| Evaluation with LLM Judge | RAG answer generation and the judge prompt |

## Supported detection and its limits

| Detector | Detects | Does not detect |
|---|---|---|
| `email` | `local@domain.tld` | obfuscated forms such as `name at domain dot com` |
| `phone` | Japanese numbers with hyphens (`03-1234-5678`, `090-1234-5678`), 11-digit mobiles without separators (`09012345678`), `+<country code>` numbers separated by spaces or hyphens | numbers in parentheses or with dots, numbers split across lines, most foreign domestic formats |
| custom rules | whatever the operator's RE2 pattern matches | anything else |

Names, addresses, dates of birth, free-text descriptions of people, and identifiers without a configured pattern are **not** detected. Pattern matching has false negatives and false positives.

Do not describe this feature as removing all personal information, and do not claim APPI, GDPR, or HIPAA compliance on the basis of redaction. Use Private Mode when content must not leave the environment.

## Tests

- `internal/domain/outbound`: supported patterns, non-matches (dates, versions), and configuration errors.
- `internal/adapter/llmguard`: deny makes zero provider calls; redact sends a redacted copy and leaves the caller's request untouched; embeddings are guarded; audit events hold no original value.
- `TestOutboundRedactionRemovesValuesFromEveryProviderPayload`: ingest, search with rerank, RAG chat, and judged evaluation through the real wiring; the provider's raw request bodies contain no email, phone, or custom identifier.
- `TestOutboundDenyBlocksProviderRequestsAndReturnsStableError`: a blocked chat returns `422`, the provider receives no request, and the audit trail records the block without the value.
