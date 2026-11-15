# Codex instructions for this repository

Apply these instructions to Codex work in this repository. The root [`SKILL.md`](SKILL.md) is the primary architectural specification and takes precedence for project architecture. The [`docs/decisions/`](docs/decisions/README.md) directory is the decision log; accepted decisions should be followed unless a later ADR supersedes them.

The behavioral source for these guidelines is [`CLAUDE.md`](CLAUDE.md), fetched from [forrestchang/andrej-karpathy-skills](https://github.com/forrestchang/andrej-karpathy-skills/blob/main/CLAUDE.md). This file adapts that guidance for Codex and this project.

## Before changing files

- Inspect the relevant code, repository structure, and project instructions first.
- State material assumptions and tradeoffs. Ask when a consequential ambiguity cannot be resolved from the request or repository; otherwise proceed with a clear, conservative assumption.
- For multi-step work, give a short plan with observable completion criteria.

## Implementation style

- Make the smallest change that fully satisfies the request. Avoid speculative features, single-use abstractions, and unrelated cleanup.
- Keep each changed line tied to the requested outcome. Preserve existing conventions and architecture.
- Keep provider-specific execution logic in provider adapters. Go owns orchestration, sessions, lifecycle, process supervision, persistence, and event delivery.
- Do not make Codex the permanent planner. Keep the system provider-agnostic and keep provider availability honest.
- Follow the incremental phase boundaries and engineering requirements in `SKILL.md`; do not silently change its architecture.
- For significant architectural choices, use the process in `docs/decisions/README.md`. Do not rewrite accepted ADR history; supersede a decision with a new ADR.

## Verification and reporting

- Verify the requested outcome with focused checks. Follow explicit verification requirements in the user request and `SKILL.md`; report checks that were run and anything left unverified.
- Add tests for meaningful functionality when implementation is requested, consistent with the task's verification requirements. Do not claim a feature works without evidence.
- Keep changes surgical. Remove only artifacts made obsolete by your own changes.
