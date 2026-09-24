# Runtime API (W10)

ForgeAI separates the management surface from the runtime surface.

- Management API: `/api/v1/*`
- Runtime API: `/runtime/v1/apps/{slug}/*`

A Deployment is an immutable snapshot of the evaluated configuration: knowledge base, active `rag_system` prompt version/content, model alias, `top_k`, and rerank setting.

## 1. Create a deployment

First identify the knowledge-base ID through the management API/UI, then:

```bash
curl -sS -X POST http://localhost:8080/api/v1/deployments \
  -H 'Content-Type: application/json' \
  -d '{
    "slug": "techvit-dogfood",
    "knowledge_base_id": "<KB_ID>",
    "alias": "normal",
    "top_k": 5,
    "rerank": false
  }'
```

Changing the active Prompt Registry version after this point must not change the Deployment. Create a new Deployment when you intentionally want new runtime behavior.

## 2. Issue a runtime token

```bash
curl -sS -X POST http://localhost:8080/api/v1/deployments/<DEPLOYMENT_ID>/tokens \
  -H 'Content-Type: application/json' \
  -d '{"name":"local-dogfood"}'
```

The `token` field is returned only by the creation response. Store it immediately. ForgeAI persists only a SHA-256 hash plus safe metadata/prefix.

## 3. Search

```bash
export FORGEAI_RUNTIME_TOKEN='fai_...'

curl -sS -X POST http://localhost:8080/runtime/v1/apps/techvit-dogfood/search \
  -H "Authorization: Bearer $FORGEAI_RUNTIME_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"query":"What does this project do?"}'
```

The client cannot override `top_k` or rerank. Runtime behavior comes from the frozen Deployment.

## 4. Chat (SSE)

```bash
curl -N -X POST http://localhost:8080/runtime/v1/apps/techvit-dogfood/chat \
  -H "Authorization: Bearer $FORGEAI_RUNTIME_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"query":"Summarize the relevant policy and cite the source."}'
```

The stream returns answer deltas and a terminal event containing usage/cost/citations.

## 5. Revoke the token

```bash
curl -i -X DELETE \
  http://localhost:8080/api/v1/deployments/<DEPLOYMENT_ID>/tokens/<TOKEN_ID>
```

After revocation, both runtime search and chat must return `401 Unauthorized`.

## Security notes

- Treat the one-time token creation response as a secret and do not paste it into logs, issues, or screenshots.
- Runtime tokens are deployment-scoped.
- Runtime endpoints are intentionally separate from demo/management session authentication.
- W10 authentication does **not** by itself guarantee that RAG content remains local. Provider egress/privacy is governed by the v0.1 Security & Privacy gate (#21–#25).
- Until that gate passes, dogfooding should use only TechVit-owned non-confidential/non-personal material.
