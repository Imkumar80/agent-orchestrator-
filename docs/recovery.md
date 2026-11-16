# Startup recovery and runtime ownership

The server obtains a renewable SQLite startup lease before provider setup and
holds it for its lifetime. The lease enforces the existing single-daemon
deployment decision when multiple processes point at the same database. If a
live lease exists, the second process exits before recovery or serving HTTP.
The lease is renewed every ten seconds and expires after thirty seconds if the
owner crashes. Lease renewal failure cancels execution and initiates shutdown.

Recovery runs after SQLite migrations and provider registration, and before the
HTTP listener starts. Terminal sessions are left unchanged. A pending or
starting session is marked interrupted because the application cannot prove
that startup was never partially performed. Running and waiting sessions are
resumed only when the registered provider advertises resume and confirms the
same canonical and native session IDs. Waiting sessions then transition to
running through the normal lifecycle manager. All other stale nonterminal
sessions are marked interrupted with a safe recovery error. Recovery never
restarts a task automatically.

The Codex CLI adapter currently advertises no resume capability and keeps its
process references only in memory. The persisted Codex thread ID is not proof
of OS process ownership. Therefore stale Codex sessions are marked interrupted;
recovery does not persist or kill PIDs, and it cannot reconnect to Codex after
an orchestrator crash. This avoids killing an unrelated process or launching a
duplicate execution.

Startup logs include inspected, skipped, reconciled, unrecoverable, error, and
duration counts, plus per-session recovery issue categories. Recovery events
are published only to the current in-memory broker. They are not durable or
replayable, including for clients that connect after startup; the persisted
session returned by the REST API remains authoritative.

Graceful shutdown stops HTTP acceptance, cancels provider execution, asks the
session manager to close tracked sessions, shuts down the event broker, then
releases the SQLite lease and closes SQLite. Errors are returned with their
stage labels. If the configured grace deadline expires, cleanup may remain
incomplete; the server reports that error and the lease will expire if it
cannot be explicitly released.

Events remain in-memory and non-durable. The service binds to loopback by
default; exposing it beyond localhost requires authentication and
authorization, which are not implemented in this phase.
