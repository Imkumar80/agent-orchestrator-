# 0002. Persist session facts and derive status

- Status: Accepted
- Date: 2026-11-16
- Supersedes: None
- Superseded by: None

## Context

Sessions must remain coherent across process exits, daemon restarts, retries,
and repeated observations. Persisting several independently updated display
statuses can produce contradictory states and makes recovery ambiguous.

## Decision

Persist durable session facts and lifecycle transitions, then derive display
status from those facts when assembling read models. Keep session identity
stable and independent from process IDs, provider session IDs, worktree paths,
and branch names. Use SQLite as the initial local persistence store, with
forward-only migrations and transactional writes for related durable changes.

## Consequences

- A session's activity and its logical termination are separate facts.
- Lifecycle reducers and read-model status derivation must be deterministic,
  idempotent, and concurrency-safe.
- Events must correspond to committed durable changes; reconnecting clients
  can refetch authoritative state.
- Startup recovery must reconcile persisted state with actual process and
  workspace resources rather than trusting in-memory state alone.
- UI status labels and database rows are not public domain contracts.

## Alternatives considered

- **Persist each UI status as an independent source of truth:** rejected
  because updates can drift and conflict.
- **Use provider-native session identifiers as project session IDs:**
  rejected because provider sessions can change or be recreated.

## References

- [`SKILL.md`](../../SKILL.md), observe/persist/derive model, lifecycle,
  SQLite, and event delivery sections.
