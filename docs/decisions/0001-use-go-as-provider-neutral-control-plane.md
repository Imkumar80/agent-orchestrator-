# 0001. Use Go as the provider-neutral orchestration control plane

- Status: Accepted
- Date: 2026-11-16
- Supersedes: None
- Superseded by: None

## Context

The system must coordinate coding agents with different launch modes,
protocols, capabilities, and session identifiers. The orchestration layer
must remain useful when the selected agent changes. The project's primary
architecture specification requires Go to own session management, process
supervision, lifecycle, persistence, and event delivery. It names Codex,
OpenCode, Hermes, and OpenClaw as selectable providers, with Codex first for
the demo rather than the permanent planner.

## Decision

Use Go as the provider-neutral control plane. Keep provider-specific launch,
protocol, and event decoding in adapters behind narrow interfaces. Treat the
orchestrator session ID as canonical and provider-native IDs as adapter-owned
metadata. Keep planning separate from execution and optional; no provider is
the permanent central planner.

The initial provider choices are Codex, OpenCode, Hermes, and OpenClaw. Codex
is the first operational adapter; unimplemented providers must be reported as
unavailable rather than appearing functional.

## Consequences

- Session identity, persistence, lifecycle, and process ownership do not
  depend on one agent product.
- Provider capabilities and unsupported operations must be represented
  honestly at the adapter boundary.
- Provider protocol payloads stay out of domain and transport packages.
- The first demo can execute a single selected provider without introducing
  an LLM planner or multi-agent task graph.
- Each additional provider requires its own adapter and contract-level
  verification; provider behavior must be checked against the installed
  version.

## Alternatives considered

- **Make Codex the central planner and execution engine:** rejected because
  it would make a demo provider a permanent architectural dependency.
- **Put provider-specific process logic in session services:** rejected
  because it would couple orchestration lifecycle to individual runtimes.

## References

- [`SKILL.md`](../../SKILL.md), especially the provider-neutral architecture,
  provider adapter, and initial demo sections.
