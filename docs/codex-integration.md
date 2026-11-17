# Real Codex CLI integration verification

This is an opt-in manual integration procedure. The ordinary Go test suite uses controlled fake executables and never requires a Codex account or network access. A real run can incur model usage and must only target a disposable workspace.

## Current verification status

Real Codex integration is **partially verified** on Windows with Go `1.27.4` and Codex CLI `0.169.0` (`codex login status`: logged in using ChatGPT). The standard npm `codex.cmd` shim was configured and the server started on loopback with a temporary SQLite database; `/health` returned `ok`. A read-only CLI task completed with `ORCH-VERIFY-2026` and `Go`, and SSE delivered actual Codex JSONL-derived output through the `process.completed` event. A separate task retrieved a global memory and returned `MEMORY-CODE-731` in its live event stream. The resulting session state was `completed`.

The Codex CLI's PowerShell shell tool failed with `CreateProcessWithLogonW failed: 5`; the first task read the fixture through an available workspace file tool instead. The sample was not modified. Live cancellation/process-tree cleanup, nonzero/timeout scenarios, server shutdown during active work, restart recovery/no-duplicate behavior, and effective filesystem/network sandbox enforcement remain **not run**. Therefore the adapter does not yet meet the full real-integration definition of done. Fake-process regression tests remain separate from these live results.

The current composition root creates the Codex adapter with its defaults: `workspace-write` sandbox and `never` approval prompts. `never` means Codex does not pause for a user approval prompt. The app does not currently expose sandbox or approval settings through environment configuration. The adapter requests the configured sandbox, but effective filesystem/network enforcement remains unverified on this Windows host because Codex's shell process creation was denied. The task below only asks Codex to read files.

## Prerequisites and preflight

Install Codex using the [official Codex CLI guide](https://developers.openai.com/codex/cli/) and authenticate using its documented sign-in method. Check the installation and authentication without displaying credentials:

```powershell
go version
codex --version
where.exe codex
codex login status
codex exec --help
```

`codex login status` exits successfully when credentials are present and reports the authentication mode. Do not print `auth.json`, tokens, API keys, or secret environment values. Do not run `codex logout`, modify global `config.toml`, or change user-level permissions for this test.

The provider invocation is `codex exec --json --cd <workdir> --sandbox workspace-write -c 'approval_policy="never"' -`, with the prompt on stdin and the process working directory set to the same workspace. For Codex versions that expose `--config` rather than the older `--ask-for-approval` flag, the adapter sends the canonical `approval_policy` setting as one argument. Current CLI help documents JSONL output, `--cd`, `--sandbox`, `--config`, and `-` as the stdin-prompt sentinel. The adapter waits for `thread.started` to confirm startup, forwards JSONL stdout as opaque provider output events, forwards stderr separately, and records process completion/failure/interruption. See [Codex non-interactive mode](https://developers.openai.com/codex/cli/non-interactive-mode) and the [CLI command reference](https://developers.openai.com/codex/cli/reference/).

On Windows, the standard npm `codex.cmd` shim is resolved to its installed `@openai/codex/bin/codex.js` entry point and launched through `node.exe` directly. The adapter does not invoke `cmd.exe` to run the shim. This path was exercised with Codex CLI `0.169.0`; other npm layouts should be checked against their shim and package entry point.

These docs confirm the current invocation shape, not compatibility with every installed release. Save the installed version and compare `codex exec --help` before testing. If local help differs, stop and update the adapter against the installed version before attempting the live run.

## Isolated workspace and server configuration

Use a new directory outside the project checkout, and initialize it as a Git repository because Codex non-interactive mode expects a Git workspace:

```powershell
$testRoot = Join-Path $env:TEMP 'codex-integration-test'
New-Item -ItemType Directory -Force -Path $testRoot | Out-Null
Set-Content -LiteralPath (Join-Path $testRoot 'README.md') -Value '# Disposable Codex integration fixture'
Set-Content -LiteralPath (Join-Path $testRoot 'sample.txt') -Value @'
The project identifier is ORCH-VERIFY-2026.
The application language is Go.
'@
git -C $testRoot init
git -C $testRoot status --short
```

Check that the path exists, is writable by the signed-in Windows user, and contains only the disposable fixture. Do not pass the main repository as the task working directory. Confirm the database file and parent directory are writable; point SQLite at a separate temporary file for this run.

In one PowerShell terminal, configure a loopback-only server and isolated database, then start it:

```powershell
$env:ORCHESTRATOR_HTTP_ADDR = '127.0.0.1:8080'
$env:ORCHESTRATOR_SQLITE_PATH = Join-Path $env:TEMP 'codex-integration-test.db'
$env:ORCHESTRATOR_ENABLED_PROVIDERS = 'codex'
go run ./cmd/server
```

Use a fresh database path or remove only a database file you created specifically for this test after shutting the server down. Do not point this procedure at your normal orchestrator database if you need to preserve its sessions.

## Basic task and live events

In another terminal, create the read-only task through the CLI:

```powershell
$testRoot = Join-Path $env:TEMP 'codex-integration-test'
go run ./cmd/orchestrator task create `
  --description 'Read sample.txt and report its project identifier and application language. Do not modify files.' `
  --provider codex `
  --workdir $testRoot
```

Record the returned session ID. Verify `GET /api/v1/sessions/{id}` or the CLI `session get` reports an initial running state, then wait for it to become `completed` or `failed`. Check that the session persisted in the isolated SQLite database and that completion/error state matches the process outcome. A successful answer should include `ORCH-VERIFY-2026` and `Go`. Compare the test workspace before and after; no file should have changed.

For a live event stream, start `session logs <session_id>` immediately after creating a long-enough task. Events are in-memory and not replayable, so subscribing after completion does not show prior events. Confirm that JSONL output and stderr events arrive in a sensible order and that the CLI exits after the terminal event. The SSE stream closes on a terminal event; it is not a durable transcript.

## Cancellation

Create a longer task against the disposable fixture with an explicit instruction not to edit files. Cancel it using:

```powershell
go run ./cmd/orchestrator session cancel SESSION_ID
go run ./cmd/orchestrator session get SESSION_ID
```

Confirm the session is `interrupted`. To inspect process cleanup, identify the Codex process by its command line and parent process while the task is running, and verify that exact process and its children exit after cancellation. Do not terminate a guessed PID or use a broad process-kill command. On Windows the adapter uses `taskkill.exe /PID <owned-child-pid> /T /F` for the Codex process tree, with a direct-process fallback; this path still requires real runtime verification.

## Failure cases

Run only in the temporary database/workspace:

- **Invalid executable**: set `CODEX_EXECUTABLE` to a nonexistent temporary path and start the server. Startup should fail during provider initialization before the listener accepts tasks.
- **Invalid working directory**: submit a task using a nonexistent path. The request should fail validation and no Codex process should appear.
- **Nonzero process exit**: use a temporary executable shim only if you control it and can guarantee it is invoked instead of the real CLI. Fake-process regression tests cover nonzero exit handling; do not corrupt a real installation to trigger failure.
- **Startup timeout**: test with a controlled fake executable and short `StartupTimeout` in an opt-in Go test, not by delaying or modifying a real Codex installation.
- **Malformed JSON and large lines**: existing fake-process tests cover malformed/unexpected output handling and the configured 1 MiB scanner limit. Preserve sanitized captures only; never save credentials or unrelated workspace output.
- **Unauthenticated state**: do not log out the real user. Mark this scenario untested unless a separately isolated Codex home with no credentials can be configured without exposing or copying the real credential store.
- **Server shutdown**: use a disposable execution and stop only the test server. Verify the session closes as interrupted and inspect the exact child process tree for cleanup.

## Memory context

Create a global semantic record in the isolated database through the CLI or API with content `The integration verification code is MEMORY-CODE-731.`. Submit a task asking only for that code, with `ORCHESTRATOR_MEMORY_ENABLED=true`. Confirm the selected memory appears in the bounded `StartRequest.MemoryContext` boundary and is labeled untrusted reference data, while the original user task remains separate. Verify the actual answer contains `MEMORY-CODE-731`. This test uses a live model response and is manual; automated tests cover retrieval, coordinator forwarding, and prompt formatting with deterministic fakes.

## Restart recovery

Start a long task in the disposable workspace and stop the orchestrator while it is active. Restart it with the same temporary database. The Codex adapter does not advertise resume, so recovery should mark the stale session interrupted and must not start duplicate work or kill a process by a persisted PID. Check the session through the CLI and review startup recovery logs. Inspect the old process tree by its verified parent/command-line relationship; do not kill a process if ownership is ambiguous.

## Compatibility and security checks

- The installed CLI's `--json`, `--cd`, `--sandbox`, `--ask-for-approval`, and stdin `-` behavior must match the adapter invocation. Current official CLI docs document these options and the `thread.started` JSONL event, but a real version-specific run remains necessary.
- `workspace-write` is the current application default. Inspect effective policy before a live run; do not use `danger-full-access` or grant extra writable roots.
- The HTTP server should bind to `127.0.0.1` for this test. Never expose the test server publicly.
- Inspect logs and captured fixtures for secrets before sharing them. Keep prompts and fixture content synthetic.
- Confirm only the temporary fixture and temporary SQLite data were involved; do not run destructive prompts against the project repository.

## Troubleshooting and status reporting

If `where.exe codex` finds nothing, configure the server's `CODEX_EXECUTABLE` only after installing the CLI in a known location. If `codex login status` fails, authenticate through Codex's supported sign-in flow; never paste tokens into commands or logs. If the session remains `starting`, inspect sanitized startup stderr and confirm `thread.started` is emitted by that CLI version. If it remains `running` after the CLI exits, inspect the persisted session lifecycle and terminal event. If SSE shows no events, the subscription may have started too late because event history is not durable.

Record each scenario as **passed**, **failed**, or **not run**, with the CLI version, final session status, event types observed, and whether the exact process tree exited. Do not label real integration verified until a real authenticated Codex run, live SSE, cancellation, final persistence, memory prompt delivery, and restart recovery have all been observed. The current machine is blocked at installation because `codex` is missing; none of those real scenarios are claimed as passed.
