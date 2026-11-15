# 0003. Build a local-first single-daemon MVP

- Status: Accepted
- Date: 2026-11-16
- Supersedes: None
- Superseded by: None

## Context

The project needs a production-minded MVP without taking on the full
complexity of a desktop/mobile product, multiple native agent protocols,
remote access, or autonomous planning before session and process lifecycle
are reliable. The primary specification calls for local-first operation,
durable sessions, process supervision, workspace isolation, cancellation,
recovery, and event delivery.

## Decision

Start with one Go daemon on the local machine. Bind its API to loopback by
default. Implement subprocess-based execution first, using ordinary pipes for
the initial non-interactive provider path. Add Git worktree isolation for
repository-modifying workers. Build the single-provider path without an LLM
planner; begin with Codex as the first runnable selectable provider.

## Consequences

- The daemon owns process lifetime, cancellation, output capture, persistence,
  and cleanup; clients communicate through its API.
- Process output, execution time, concurrency, and workspace scope need
  explicit bounds.
- Failed multi-step setup and cleanup must be recorded so they can be
  reconciled rather than silently abandoned.
- Native chat, PTY/ConPTY interaction, detached provider hosts, controller
  handoff, desktop/mobile clients, remote pairing, SCM feedback, and
  model-backed planning are deferred until core lifecycle behavior is stable.
- A provider's successful process exit alone does not establish task success.

## Alternatives considered

- **Build interactive chat, terminal transport, and all client surfaces in
  the first slice:** rejected because they expand runtime and security scope
  before core lifecycle invariants are proven.
- **Start with a model-backed multi-agent planner:** rejected because a
  selected provider or deterministic Go flow is enough to establish the
  first execution path.

## References

- [`SKILL.md`](../../SKILL.md), local-first security, process supervision,
  implementation roadmap, and provider integration sections.
