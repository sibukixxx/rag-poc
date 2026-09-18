# Private Mode and provider egress

ForgeAI has two explicit outbound-provider modes:

- `external_allowed`: configured LLM and embedding endpoints may be contacted.
- `local_only`: provider calls are allowed only to loopback, optionally private IP addresses, or exact allowlisted URL origins. Everything else fails closed before request transmission.

```yaml
privacy:
  mode: local_only
  allow_private_network: true
  allowed_destinations:
    - https://llm.internal.example:8443

llm:
  providers:
    local:
      type: openai_compatible
      base_url: http://127.0.0.1:11434/v1 # Ollama example
embedding:
  provider:
    type: openai_compatible
    base_url: http://127.0.0.1:11434/v1
```

Run `forgeai doctor -config forgeai.yaml` to inspect the active mode and every configured LLM/embedding destination. `FORGEAI_PRIVACY_MODE` can override the YAML mode.

Enforcement is attached to the OpenAI-compatible HTTP transport, so chat, RAG, judge, rerank, and embedding traffic share the same boundary. An exact allowlist item is an origin (scheme, host, and port); URL paths do not broaden access.

Private Mode means content is not transmitted to an unapproved provider endpoint. A provider statement that data is “not used for training” is different: the data is still transmitted to that provider. Private Mode also does not replace host firewall, DNS, proxy, or administrator controls.
