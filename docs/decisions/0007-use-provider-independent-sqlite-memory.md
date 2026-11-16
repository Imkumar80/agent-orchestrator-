# 0007. Use provider-independent SQLite memory

* Status: Accepted
* Date: 2026-11-16

## Context

Tasks need a persistent place to store and retrieve useful information across sessions and providers. Provider adapters are replaceable execution engines and must not own shared application knowledge. The existing SQLite repository already owns migrations and the database connection used by sessions and the daemon lease.

## Decision

Add one provider-neutral memory domain model with working, episodic, and semantic categories and global, task, and session scopes. Persist it through a `MemoryRepository` implemented on the existing SQLite repository. Keep validation, deterministic lexical ranking, expiration, archival, and task-context assembly in an application memory manager. The coordinator may inject retrieved references through an additive provider-neutral start-request field. Context is labeled as untrusted reference data and bounded by configuration.

## Consequences

- Memory survives process restart and is shared independently of provider choice.
- Search ordering is deterministic and explainable but does not provide semantic similarity.
- User/project scopes and automatic extraction are deferred until corresponding domain behavior can be represented and validated.
- Codex currently consumes task context; future providers may adopt the field without depending on the memory manager or SQLite.
- Existing `MemoryRepository` callers use the same SQLite connection and migration system as session persistence.
