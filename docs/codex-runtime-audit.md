# Phase 12B.5: independent post-integration audit

Audit date: 2026-11-18. Verdict: **conditionally accepted** for targeted
Windows console-group isolation, Codex cancellation outcome mapping, and
HTTP-server-owned SSE subscription release. No critical or high-severity
production correctness defect was reproduced.

This records verification of the local working-tree implementation after
Phase 12B.4. That snapshot included uncommitted source changes; publishing
this report does not publish those changes or establish that the committed
source has the same runtime behavior. The audit made no lasting source edits
and ran no live Codex tasks. Historical real-model evidence is identified
separately below.

## Process ownership and console isolation

Inspection confirmed suspended process creation, assignment of the exact
process handle to its private Job Object before resume, an explicit list of
three child standard handles, and non-inheritable retained parent handles.
Kill-on-job-close remains enabled, breakaway remains disabled, and assignment
failure has no uncontained fallback. Root waiting remains independent of
stdout/stderr EOF.

Existing real Windows process tests passed for child/grandchild membership,
inherited output pipes after root exit, explicit cancellation, active-process
accounting, repeated/concurrent cleanup, and startup failure paths. Failure
injection complements these actual process tests; it is not independent
evidence that every host permits Job Object assignment.

An additional controlled probe used a dedicated hidden console and an
explicit parent process group. A targeted Ctrl+Break reached the parent;
the native-runtime child replied afterward and remained active in its job.
Explicit cancellation then produced authoritative, confirmed zero-active
cleanup. No user console or unrelated process was targeted.

**Scope limitation:** `CREATE_NEW_PROCESS_GROUP` does not detach the child
from the console or establish isolation from console-close, logoff, or system
shutdown events. The audit did not exercise those events. Targeted Ctrl+Break
evidence must not be generalized into a guarantee for all console signals.
See Microsoft's [process creation flags](https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags)
and [console control handlers](https://learn.microsoft.com/en-us/windows/console/console-control-handlers).

## Cancellation and persisted outcomes

A deterministic probe combined the actual Codex supervisor and session
manager with a controlled runtime and temporary SQLite database. It read the
persisted cancellation intent before allowing provider completion.

| Scenario | Provider `Wait` outcome | Persisted status | Terminal events |
| --- | --- | --- | --- |
| Confirmed interruption | `context.Canceled` | `interrupted` | One |
| Unconfirmed interruption with root failure | Error | `failed` | One |
| Successful root racing cancellation | `nil` | `completed` | One |
| Unexpected root failure | Error | `failed` | One |
| Injected execution deadline | Deadline error | `failed` | One |

The Codex fix preserves `context.Canceled` for execution observers while
blocking `Interrupt` and `Close` normalize confirmed interruption for their
callers. Windows interruption classification requires cancellation intent,
the expected termination exit code, and authoritative confirmed cleanup;
a successful termination request alone is insufficient. A successful root
exit retains completion classification when cancellation races it.

Existing tests also passed for duplicate cancellation, persistence failure
during terminal observation, retained-outcome reconciliation, acknowledgment
uncertainty, and cleanup deadlines. Recovery preserves an existing
cancellation reason. The injected execution-deadline probe above is distinct
from the native runtime's separate cleanup-timeout tests.

## SSE ownership and shutdown

A focused probe used two real HTTP servers sharing a real broker, two
sessions, and three concurrent SSE connections. Shutting down server A
released only A's streams; server B still received a terminal event. The
accepted-execution context remained active. Broker closure, already-ended
streams, and repeated server shutdown completed cleanly. A cancelled HTTP
drain force-closed connections and returned an observable error.

Application inspection confirmed this order:

1. Release this HTTP server's SSE subscriptions and drain HTTP requests.
2. Close tracked sessions through the existing manager/runtime contract.
3. Cancel the execution context.
4. Shut down the event broker.
5. Stop lease renewal, release the lease, and close SQLite.

SSE release does not directly cancel provider execution. Because streams end
before tracked-session cleanup, shutdown clients can receive EOF without a
terminal event. Session inspection remains authoritative. Events are
in-memory, nondurable, and not replayable.

## Findings and follow-up

| Severity | Finding | Follow-up |
| --- | --- | --- |
| Medium | Broad console-close/logoff/shutdown isolation is unverified and cannot be inferred from the process-group flag. | Keep claims limited; use a dedicated disposable-console test before claiming broader support. |
| Low | A successful completion racing cancellation retains `Error.Code=user_cancelled` on the completed record. | Decide whether this field represents cancellation history or an execution error before changing persistence semantics. |
| Low | `AgentExecutionWaiter` comments do not fully specify the outcome convention consumed by the manager. | Document the existing nil/cancelled/error convention without changing the interface. |
| Informational | Shutdown can close SSE before terminal delivery. | Clients must inspect persisted state; do not promise shutdown-time terminal delivery or replay. |

No production correction was made during this audit. Conditional acceptance
does not resolve the metadata-policy ambiguity or verify every Windows host,
console event, or possible goroutine interleaving.

## Fresh verification

Windows: NT 10.0.26300.0, amd64, Go 1.27.4. Linux: Ubuntu 24.04 under WSL2,
kernel 6.6.87.2, amd64, Go 1.26.0. These were runtime executions, not
cross-compilation. Test runs used `-count=1`, not cached test results.

| Environment | Command | Result |
| --- | --- | --- |
| Windows | `go test -v -count=1 -timeout 90s ./internal/providers/codex` | Passed; one legacy CIM-based test skipped because process inspection was denied. Native Job Object tests executed. |
| Windows | `go test -count=1 -timeout 90s ./internal/session ./internal/api/... ./internal/app` | Passed |
| Windows | `go test -count=1 -timeout 90s ./...` | Passed |
| Windows | `go vet ./...` | Passed |
| Windows | `git -c core.safecrlf=false diff --check` | Passed |
| Windows | `go test -race -count=1 ./internal/providers/codex` | Could not run: CGO disabled and no GCC on PATH. No Windows race verification. |
| Linux | `go test -count=1 -timeout 120s ./...` | Passed |
| Linux | `go test -race -count=1 -timeout 180s ./...` | Passed |
| Linux | `go vet ./...` | Passed |

The temporary focused probes passed separately. Their sources and outputs
were retained with the audit evidence, then removed from the repository.
The first console harness incorrectly targeted a host PID that was not an
explicit process-group ID; correcting that harness required no production
change. The initial WSL toolchain download timed out; the retry succeeded.

## Independent review of historical integration evidence

The preserved Phase 12B.4 evidence supports:

- Real session `ses_b5169336dd58b1bd1bf3945cfc0d0bd6` returned
  `ORCHESTRATOR_E2E_OK`, persisted its native thread reference and completed
  status, and emitted one terminal event. Creation was
  2026-11-17T21:40:32.4984518Z; completion was 21:40:38.6092994Z.
- Real cancellation `ses_11808a9ee0667e4dce973a718e9296d8` persisted
  `interrupted`/`user_cancelled` and emitted one `process.interrupted` with
  authoritative, confirmed Job Object cleanup. The captured payload records
  production cleanup evidence, not a separate external active-count metric.
- Offline revalidation of the stored captures passed for five basic SSE
  events and two cancellation events, including JSON, frame IDs/types and
  session correlation.
- Connected-stream shutdown closed SSE before terminal delivery. Separate
  persisted-state, process-exit and lease snapshots support subsequent
  `application_shutdown` interruption and clean exit; there was no external
  instrumentation of every shutdown stage.
- Restart inspected and skipped 12 terminal sessions before listening.
  Crash-recovery coverage used a synthetic stale-session fixture, not a
  deliberately orphaned live Codex execution.

These are historical artifacts reviewed independently; they were not new
live-model executions during Phase 12B.5. See the
[integration guide](codex-integration.md) for the original verification scope.

## Evidence and worktree integrity

Raw logs, temporary probe sources and before/after manifests were retained
outside Git in the local temporary directory named
`orchestrator-phase12b5-b01c7d2a526e4cc684c383c6806d2525`. Temporary artifacts
are machine-local and may expire; this document preserves the conclusions
and commands, not a permanent raw-evidence archive.

At audit completion, initial/final status and HEAD matched, all inventoried
pre-existing content hashes and modification timestamps matched, and no
changes were staged or committed. Migration `0004` and Unix runtime/test
hashes matched their recorded baselines. This documentation publication is
a later, separately authorized change.
