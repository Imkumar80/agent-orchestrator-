# 0008. Separate execution outcomes from process cleanup

- Status: Accepted
- Date: 2026-11-18
- Supersedes: None
- Superseded by: None

## Context

A provider root can exit while descendants retain output handles. Waiting
for reader EOF before observing root exit can therefore deadlock. A process
termination request also does not establish that every owned process exited.
Conflating these facts caused cancellation and shutdown inconsistencies
observed during Codex integration verification.

## Decision

Keep OS process ownership inside the Codex adapter's private runtime. Observe
root exit independently of output readers, then reconcile descendants and
bound output draining. Preserve the root execution result separately from
reader errors, cleanup requests, cleanup confirmation and resource closure.

On Windows, create the process suspended, assign its exact handle to a private
kill-on-close Job Object, then resume its exact primary thread. Use an
explicit inherited-handle list and retain non-inheritable parent handles.
Fail startup when containment cannot be established; never resume an
uncontained fallback. Confirm cleanup only through a successful authoritative
zero-active-process query. Closing the job is a containment fallback, not
confirmation. Unix retains its process-group behavior and documented limits.

A successful root exit wins a concurrent cancellation request. Confirmed
interrupted execution is reported through `Wait` as `context.Canceled`, so
the session manager can correlate it with persisted cancellation intent.
Blocking interruption/closure may normalize that confirmed outcome for their
callers while preserving cleanup errors. One supervisor publishes the
terminal event. Process completion does not establish semantic task success.

HTTP shutdown releases only its own SSE subscriptions before draining. It
does not cancel accepted execution directly. Session cleanup precedes broker
shutdown and SQLite closure. Persisted session state remains authoritative
when a stream ends before terminal delivery.

## Consequences

- Provider-neutral interfaces and component ownership remain unchanged.
- Process-group console isolation does not imply protection against all
  Windows console-close, logoff or system-shutdown events.
- Cancellation acceptance, execution outcome and confirmed cleanup remain
  distinct evidence; timeouts and API failures cannot establish termination.
- Cleanup errors stay observable without replacing a successful root result.
- SSE remains ephemeral; shutdown-time terminal delivery is not guaranteed.
- This decision records the lifecycle policy, not unconditional acceptance
  of every implementation or host. The Phase 12B.5 audit remains conditional;
  cancellation-reason metadata on completed sessions needs separate policy.

## Alternatives considered

- **Wait for output EOF before root reconciliation:** rejected because a
  descendant can retain inherited pipes after the root exits.
- **Treat termination API success or job closure as confirmation:** rejected
  because neither proves zero active processes.
- **Make HTTP stream lifetime own execution lifetime:** rejected because
  disconnecting a client must not cancel accepted work, and waiting for SSE
  before session cleanup can block graceful shutdown.

## References

- [Phase 12B.5 audit](../codex-runtime-audit.md), evidence and open findings.
- [Codex integration verification](../codex-integration.md).
- [ADR 0003](0003-build-a-local-first-single-daemon-mvp.md), daemon ownership.
- [ADR 0005](0005-use-bounded-in-memory-session-event-broadcasting.md),
  ephemeral event delivery.
