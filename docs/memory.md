# Provider-independent memory

## Architecture

The orchestrator owns memory. The HTTP API and CLI call the application memory manager; the manager validates records, retrieves relevant references, and delegates persistence to the `MemoryRepository` port. SQLite implements that port on the same connection and migration lifecycle as session storage. Providers receive optional reference text only through the provider-neutral `StartRequest.MemoryContext` field. Provider adapters do not read or write the memory repository.

Memory is available over the API and CLI even when retrieval for new tasks is disabled. Set `ORCHESTRATOR_MEMORY_ENABLED=false` to disable task context injection. `ORCHESTRATOR_MEMORY_CONTEXT_MAX_BYTES` bounds the UTF-8 context passed to the provider (default 4000 bytes).

## Records and scope

Each record has a typed ID, scope, category, content, optional summary, source, confidence, importance from 1 to 5, tags, JSON metadata, timestamps, and optional expiration/archive/supersession fields.

Categories:

- **Working**: temporary context. It must have an expiry timestamp; searches archive it after expiry.
- **Episodic**: a sourced account of an event, outcome, or prior task.
- **Semantic**: reusable knowledge. A newer semantic record may supersede an older record in the same scope.

Sources distinguish user, agent, tool, and system records. Agent-generated content is not automatically verified; clients should use source and confidence as provenance, not proof.

Scopes implemented today are **global**, **task**, and **session**. Global records are shared across providers. Task scope is tied to one task ID, and session scope to one session ID. There is no user or project entity in the current domain model, so those scopes are not represented. Scope IDs are validated against the matching task/session identifier.

## SQLite schema

Migration `0003_create_memories.sql` creates a unified `memories` table with checks for valid scope, type, source, confidence, importance, and timestamp relationships. Session-scoped records reference `sessions`; supersession references another memory. Indexes support scope/type/update ordering, working-memory expiry, and one superseder per record. Tags and metadata are stored as JSON text. Schema migration runs through the existing SQLite repository; session storage and daemon lease continue using the same database handle.

Archived records remain retrievable by ID but are omitted from search and task context. Delete is explicit and permanent. Superseded semantic records are omitted from search; the old record remains available by ID for provenance. Expired working records are archived during search and excluded from results.

## Deterministic retrieval

Search uses case-insensitive lexical token matching over summary, content, and tags. It filters by requested scope, type, tags, minimum importance, active/archive state, and expiry. It is not semantic similarity: there are no embeddings, synonym expansion, or vector database.

The manager adds these transparent scores:

| Signal | Score |
| --- | ---: |
| Importance | importance * 3 |
| Recency | +10 under 1 day; +7 under 7 days; +4 under 30 days; otherwise +1 |
| Requested scope match | +20 |
| Requested type match | +5 |
| Each query term in summary | +12 |
| Each query term in content | +8 |
| Each query term in a tag | +6 |
| Exact query phrase in summary or content | +8 |

For a given term, only its strongest text match is counted. Results sort by score descending, update time descending, then ID ascending. Search returns the score and reason strings with each result. Lexical matches can miss relevant wording and can return coincidental matches; review retrieved content as reference data.

## Task context

Before session creation, the coordinator asks the injected memory manager for global and same-task matches against the task description. The manager ranks results, keeps a deterministic order, and enforces the configured byte limit. It labels the material as untrusted reference data, separate from the authoritative user task. The Codex adapter preserves the task and serializes the task and reference block as separate JSON strings in its prompt. Other adapters can ignore `MemoryContext` until they implement a safe provider-specific prompt strategy.

Memory retrieval errors fail task submission rather than silently running without requested context. Set `ORCHESTRATOR_MEMORY_ENABLED=false` to run tasks without retrieval. The initial version does not automatically extract or store memories from agent output.

## HTTP API

All endpoints use JSON DTOs and the injected memory manager:

- `POST /api/v1/memories` creates a record; defaults are global, semantic, user source, confidence 1, and importance 3.
- `GET /api/v1/memories/{memory_id}` retrieves a record, including archived records.
- `POST /api/v1/memories/search` accepts a `MemorySearchQuery` and returns `results` with score and reasons.
- `PATCH /api/v1/memories/{memory_id}` updates supplied fields of an active record.
- `POST /api/v1/memories/{memory_id}/archive` archives a record.
- `DELETE /api/v1/memories/{memory_id}` explicitly deletes it and returns 204.

Example:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/memories \
  -H 'Content-Type: application/json' \
  -d '{"scope":"global","type":"semantic","content":"The service uses SQLite","source":"user","confidence":1,"importance":4,"tags":["storage"]}'

curl -X POST http://127.0.0.1:8080/api/v1/memories/search \
  -H 'Content-Type: application/json' \
  -d '{"query":"SQLite storage","scope":"global","limit":10}'
```

## CLI

The CLI uses the same HTTP API, configured with `--server URL` or `ORCHESTRATOR_SERVER_URL`:

```bash
agent-orchestrator memory create --content "The service uses SQLite" --scope global --type semantic --tags storage
agent-orchestrator memory get MEMORY_ID
agent-orchestrator memory search --query "SQLite storage" --scope global --limit 10
agent-orchestrator memory archive MEMORY_ID
agent-orchestrator memory delete MEMORY_ID
```

Memory create/get/search accept `--json` for machine-readable output. Deletion is explicit; archive is preferred for ordinary lifecycle management.

## Limitations

- Retrieval is lexical and deterministic, not semantic.
- User and project scopes are unavailable until those domain entities exist.
- Memory extraction and verification are manual; no model automatically promotes statements to facts.
- Task context is currently delivered through the Codex CLI adapter. The provider-neutral field preserves compatibility for future adapters, which must decide whether and how to use it safely.
- Memory persistence is SQLite-backed, while session events remain in-memory and non-durable.
