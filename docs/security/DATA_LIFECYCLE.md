# Customer data inventory and lifecycle (v0.1)

This document is the inventory required by #23. It lists every place ForgeAI v0.1 persists customer-derived data, how that data is deleted, and what ForgeAI does **not** guarantee. The executable contract is `internal/domain/lifecycle` and its tests.

## What is stored, and where

All application data lives in one SQLite database (`database.path`, default `./data/forgeai.db`) plus its `-wal` / `-shm` files.

| Artifact | Table(s) | Contains customer content? | Removed by |
|---|---|---|---|
| Knowledge base | `knowledge_bases` | Name/slug only | KB deletion |
| Document metadata | `documents` | Filename, MIME type, size | Document or KB deletion |
| Extracted text | `chunks` | **Yes, plaintext** | Document or KB deletion (cascade) |
| Full-text index | `chunks_fts` (+ FTS5 shadow tables) | **Yes, plaintext and trigrams** | Document or KB deletion (explicit delete) and `data compact` (segment merge) |
| Embeddings | `embeddings` | Derived vectors | Document or KB deletion (cascade) |
| Source-sync records | `source_connections`, `source_items`, `source_sync_jobs` | Titles, source URLs, metadata; no document body | KB deletion; document deletion removes the document's `source_items` |
| Bulk ingestion jobs | `ingestion_jobs`, `ingestion_job_items` | Relative file paths, sizes, per-file errors; no file content | KB deletion (cascade through the source connection) |
| Golden Dataset | `datasets`, `dataset_cases` | Queries and expected answers written by the operator | KB deletion (cascade) |
| Evaluation results | `evaluation_runs`, `evaluation_results` | **Yes**: generated answers, judge reasons, retrieved filenames | KB deletion, or retention (`retention.evaluation_runs_days`) |
| Traces | `traces`, `spans` | Operation names, models, token/cost, error text. Prompts, questions, and chunk text are **not** stored (the `input`/`output` columns are unused) | Retention (`retention.traces_days`) |
| Deployments | `deployments`, `runtime_api_tokens` | Frozen prompt text (operator-authored) and token hashes | KB deletion with `include_deployments` |
| Secrets | `secrets` | Provider API keys and source OAuth grants (`source-oauth:<connection>`), AES-GCM encrypted | `forgeai secret delete`; OAuth grants are deleted when the connection is disconnected |
| Pending OAuth authorizations | `oauth_states` | State hash, PKCE verifier, 10-minute expiry | Consumed on callback; expired rows are pruned; deleted with the connection |

Uploaded files are **not** kept. Upload, `forgeai ingest`, and filesystem sources extract text in memory; `storage.path` is created but no original document is written to it in v0.1. Filesystem sources read files in place from `sources.filesystem.allowed_roots`; deleting data in ForgeAI never deletes the original files.

## Deletion paths

| Operation | HTTP | CLI |
|---|---|---|
| Delete one document | `DELETE /api/v1/knowledge-bases/{id}/documents/{docID}` | `forgeai data delete-document <document-id>` |
| Disconnect a source | `DELETE /api/v1/source-connections/{id}` | — |
| Delete a knowledge base | `DELETE /api/v1/knowledge-bases/{id}[?include_deployments=true]` | `forgeai data delete-kb -kb <slug> [-include-deployments]` |
| Apply retention | — | `forgeai data retention` |
| Remove deleted pages from the database files | — | `forgeai data compact`, or `-compact` on a delete command |

Semantics:

- Each deletion runs in one transaction and returns a report of what was removed.
- Deleting something already gone succeeds with an all-zero report, so a purge can be re-run.
- A knowledge base that still serves a Deployment is not deleted unless the operator opts in. Deleting it then removes the Deployments and revokes their runtime tokens.
- Deleting a synced document removes its `source_items`. If the upstream object still exists, the next sync imports it again. Stop or delete the source connection to prevent that.
- Retention never deletes documents, Golden Dataset definitions, prompts, or Deployments. `0` days keeps records until explicitly deleted.

## Removing deleted data from disk

SQLite does not overwrite deleted rows immediately. Deleted text can remain in free pages, in the WAL file, and in FTS5 index segments. `forgeai data compact`:

1. merges the FTS5 index so tombstoned terms are dropped,
2. checkpoints and truncates the WAL,
3. runs `VACUUM` to rebuild the database file.

`TestLifecycleStoreDeletedTextIsNotLeftInTheDatabaseFileAfterCompact` and `TestLifecycleStoreCompactLeavesNoFTSIndexSegmentsForDeletedText` verify this. `VACUUM` rewrites the whole file and needs free disk space about the size of the database. Run it during low traffic.

## Storage at rest

- **Implemented:** provider secrets are encrypted with AES-GCM under `FORGEAI_MASTER_KEY`.
- **Not implemented:** chunk text, FTS data, evaluation answers, and embeddings are stored in plaintext inside SQLite. Full-text search needs readable text, and ForgeAI does not add encryption that would break retrieval or decrypt the corpus into temporary files.
- **Operator responsibility:** put the database on an encrypted volume (FileVault, BitLocker, LUKS, encrypted cloud disks) before ingesting confidential data, then declare it:

  ```yaml
  security:
    storage_at_rest: operator_encrypted_volume
  ```

  `forgeai doctor` reports whether this was declared. ForgeAI cannot verify the volume itself. The declaration records the operator's statement.

Do not describe ForgeAI as "encrypting customer data at rest" unless the deployment provides and documents such a volume.

## Backups and external copies

Deletion and `compact` only affect the database files ForgeAI manages. They do not erase:

- backups or snapshots taken before the deletion (volume snapshots, `cp` copies, Litestream/replica copies),
- data already sent to an external LLM/embedding provider and retained under that provider's policy (see `docs/PRIVATE_MODE.md`),
- copies in the source system a connector reads from.

Operators must apply their own retention to backups. Physical erasure on SSD or cloud storage is not guaranteed.

## Logs

ForgeAI logs operation names, IDs, HTTP paths, and sanitized errors. Provider response bodies are not logged (`openaicompat` logs only the host and status). Request bodies, questions, prompts, and chunk text are not written to logs in normal operation.
