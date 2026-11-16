# Architecture decisions

This directory is the project's architecture decision log. An architecture
decision record (ADR) captures one significant choice, why the project made
it, and the consequences developers should preserve. ADRs explain the
project's design; they do not replace the implementation plan, issue tracker,
or `SKILL.md`, which remains the primary architectural specification.

## Current decisions

| ADR | Decision | Status |
| --- | --- | --- |
| [0001](0001-use-go-as-provider-neutral-control-plane.md) | Use Go as the provider-neutral orchestration control plane | Accepted |
| [0002](0002-persist-session-facts-and-derive-status.md) | Persist session facts and derive display status | Accepted |
| [0003](0003-build-a-local-first-single-daemon-mvp.md) | Build the local-first MVP around one supervised daemon | Accepted |
| [0004](0004-use-optional-deterministic-planning-for-single-task-coordination.md) | Use optional deterministic planning for single-task coordination | Accepted |
| [0005](0005-use-bounded-in-memory-session-event-broadcasting.md) | Use bounded in-memory session event broadcasting | Accepted |

## When to write an ADR

Write an ADR when a choice has meaningful consequences for system boundaries,
provider integration, persistence, security, reliability, or future
development and a future contributor would need the rationale. Skip routine,
local implementation details and choices already fully determined by an
existing accepted ADR or project rule.

## Workflow

1. Copy [`template.md`](template.md) to the next numbered filename, using a
   short lowercase, hyphen-separated, present-tense decision phrase.
2. Record the context and relevant alternatives before implementation when
   practical. Mark the record `Proposed` while the choice is still under
   review; use `Accepted` only once the decision has been made.
3. Keep each ADR focused on one decision. Link related ADRs and relevant
   requirements, issues, pull requests, and implementation evidence.
4. Do not silently rewrite an accepted decision's history. If the choice
   changes, add a new ADR that explains the change and marks the prior record
   as `Superseded by ADR NNNN`. A record that no longer applies for other
   reasons can be marked `Deprecated` with an explanation.
5. Update this index when adding or changing an ADR's status. Include the ADR
   number in implementation work when it helps reviewers trace the rationale.

## Statuses

- **Proposed** — being discussed; not an implementation mandate.
- **Accepted** — the project has chosen this direction.
- **Deprecated** — no longer recommended, without a direct replacement.
- **Superseded** — replaced by a later ADR; retain the historical record.

## Numbering

Use the next available four-digit number. Do not reuse numbers, even if an
ADR is later superseded. Keep filenames stable so links remain useful.
