# Interactive terminal UI

The interactive terminal UI (TUI) is an HTTP client for the running Agent
Orchestrator server. It does not access SQLite or launch providers directly.
Use Go 1.26 or later to build the project.

## Start the server and TUI

Start the server in one terminal:

```powershell
$env:ORCHESTRATOR_HTTP_ADDR = '127.0.0.1:8080'
$env:ORCHESTRATOR_SQLITE_PATH = './data/orchestrator.db'
$env:ORCHESTRATOR_ENABLED_PROVIDERS = 'codex'
go run ./cmd/orchestrator serve
```

Then start the TUI in another terminal:

```powershell
go run ./cmd/orchestrator tui
```

The TUI uses the CLI server URL setting: `--server URL` or
`ORCHESTRATOR_SERVER_URL`, defaulting to `http://127.0.0.1:8080`.

## Screens and navigation

- **Dashboard** shows server health, session totals, provider availability,
  and recent sessions.
- **New Task** collects a description, an available provider, and an absolute
  working directory. The returned session ID and status mean the server
  accepted the request; they do not mean the task completed.
- **Sessions** lists persisted sessions, sorted by most recently updated.
  It shows IDs, provider, status, timestamps, and any safe session error
  information.
- **Session Detail** shows the selected session's available metadata and
  live events. It does not show task descriptions or working directories,
  which are not exposed by the session API.
- **Providers** and **Settings** are navigation placeholders in this release.

Use `Up`/`Down` (or `j`/`k`) to move through sidebar items or sessions, and
`Enter` to open the selected item. In the Sessions list, `r` refreshes and
`Esc` returns to the dashboard. In Session Detail, `r` fetches authoritative
session state, `e` refreshes the state and reconnects to live events, and
`Esc` returns to Sessions. Press `?` for the in-app help and `q` to quit.

In Session Detail, `c` opens a confirmation prompt for sessions in
`running` or `waiting_for_input`; press `y` to submit the cancellation request
or `n`/`Esc` to dismiss it. The UI reports cancellation only after the server
confirms it and then refreshes the session state.

## Event and recovery limits

Events are live-only, held in memory, and have no replay cursor. A reconnect
can miss events emitted while the client was disconnected. The UI fetches the
latest session snapshot after a stream ends or is reconnected; it never treats
stream EOF as proof that a session completed. The server may disconnect a
slow subscriber. The current client cannot confirm the SSE connection until
an event arrives, and event history is not a persisted transcript.

The event view is bounded in memory. Provider payloads are displayed as
compact JSON where possible, truncated for readability. They are event data,
not a complete execution log.
