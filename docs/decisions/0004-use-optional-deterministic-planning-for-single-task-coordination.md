# 0004. Use optional deterministic planning for single-task coordination

- Status: Accepted
- Date: 2026-11-16
- Supersedes: None
- Superseded by: None

## Context

The application needs one request path that can validate a task, optionally
plan it, and delegate execution through the existing session manager. The
Planner port carries a goal, working directory, and candidate providers, but
does not carry a task ID or a separate requested-provider field. The project
also requires that planning remain separate from provider execution and that
the selected provider not be silently changed.

## Decision

Use an injected coordinator between application task intake and the session
manager. The planner is optional. When enabled for a single task, the
coordinator passes only the task's explicitly requested provider as the
available-provider candidate. A deterministic planner returns one execution
step with that provider and the original task description.

Before dispatch, the coordinator rejects proposals with a missing or invalid
provider, missing required fields, dependencies, multiple tasks, a changed
description, or a provider that differs from the user's request. It then
passes the original domain task to `SessionManager.CreateAndStart`; session
creation, runtime provider lookup, startup, persistence, and lifecycle remain
owned by the session manager and registry.

## Consequences

- The single-task workflow can run with planning disabled and has no LLM or
  network dependency when using the deterministic planner.
- Provider selection remains an explicit task/session choice and is resolved
  at runtime by the existing registry.
- The deterministic planner is a small, testable proposal generator; it is
  not a multi-agent scheduler or authorization mechanism.
- Plan proposals are validated but not persisted in this phase. Multi-task
  graphs, approval policy, and plan persistence require separate design work.
- The original task ID is preserved because the current `PlanRequest` does
  not carry an ID and the session manager accepts a domain task rather than
  a planner task.

## Alternatives considered

- **Make planning mandatory:** rejected because the existing architecture
  supports single-provider execution without a planner and planning may be
  disabled by the application.
- **Allow the planner to choose any registered provider:** rejected because
  the current request contract has no explicit authority to replace the
  provider selected by the user.
- **Add multi-agent scheduling to the coordinator:** deferred because the
  current session manager starts one task/session and does not provide task
  graph scheduling, dependency resolution, or plan persistence.

## References

- [`0001`](0001-use-go-as-provider-neutral-control-plane.md) — provider-neutral
  control plane and optional planning boundary.
- [`0003`](0003-build-a-local-first-single-daemon-mvp.md) — single-provider
  execution without an LLM planner.
- [`SKILL.md`](../../SKILL.md), provider selection, optional planning,
  validation, and implementation roadmap.
- [`coordinator.go`](../../internal/orchestrator/coordinator.go)
- [`deterministic_planner.go`](../../internal/orchestrator/deterministic_planner.go)
