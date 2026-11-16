# 0006. Keep the CLI on the HTTP API

- Status: Accepted
- Date: 2026-11-16
- Supersedes: None
- Superseded by: None

## Context

The command-line interface is an additional client for the local orchestration
service. It needs task submission, session inspection and cancellation, event
streaming, provider availability, and diagnostics. Directly using application
services, repositories, or provider adapters from CLI commands would create a
second integration path with duplicated lifecycle and persistence behavior.
The server already exposes REST and SSE routes for core session operations;
session listing and provider discovery were not exposed before the CLI phase.

## Decision

Implement the CLI as an HTTP client of the orchestration API. Keep request,
response, and SSE handling in a reusable CLI HTTP client package, separate from
command parsing and output formatting. The CLI must not access SQLite or
instantiate providers, coordinators, session managers, or event brokers.

The `serve` subcommand is the composition-root exception: it invokes the same
reusable application composition used by `cmd/server`, rather than duplicating
server wiring. Add read-only API endpoints for session listing and provider
availability; provider availability reflects the runtime registry and must
not imply that unregistered adapters are operational.

## Consequences

- CLI behavior follows the server's validation, lifecycle, persistence, and
  provider selection rules.
- The HTTP API remains the boundary between command-line clients and backend
  infrastructure.
- The API gains `GET /api/v1/sessions` and `GET /api/v1/providers` for the
  CLI and other clients.
- CLI output can be tested against fake HTTP servers without a database or
  provider installation.
- The `serve` command shares composition code but remains separate from the
  HTTP-client commands.
- SSE events remain ephemeral and cannot be replayed by the CLI after a
  disconnect or server restart.

## Alternatives considered

- **Call internal application services directly:** rejected because it would
  bypass the HTTP contract and couple the CLI to server implementation
  packages.
- **Open SQLite or start providers from the CLI:** rejected because it would
  duplicate backend ownership and session lifecycle behavior.
- **Duplicate the server composition in the CLI:** rejected because startup,
  recovery, configuration, and cleanup could diverge between entry points.
- **Use a full-screen terminal UI:** deferred; this decision covers ordinary
  subcommands and text/JSON output only.

## References

- [`SKILL.md`](../../SKILL.md), API client boundary and dependency direction.
- [`internal/cli`](../../internal/cli), command entry point and HTTP client.
- [`internal/app/app.go`](../../internal/app/app.go), shared application
  composition used by `serve` and `cmd/server`.
- [`internal/api`](../../internal/api), REST and SSE contract.
