# ADR 002: E2 shape decisions

Date: 2026-09-09. Status: accepted (David).

## Context

The console's T2.0 expansion (`docs/e2-expansion-draft.md`) surfaced three
decisions it would not assume, and the E1 console plan needed a sequencing
call relative to E2.

## Decisions

1. **Sequence.** E1 runs first through the console sessions in
   `docs/e1-oxalpha-plan.md`, starting with session 0 (stabilize the
   imported scaffold). E2 tasks are scheduled, not dispatched, until T1.6
   and T1.9 exist.
2. **Scaffold authorization and review code** is refactored into
   `internal/policy` rather than discarded. E1 session 4 (T1.4) performs
   the move; `internal/authz` disappears or becomes a thin caller.
3. **Chief representation.** A chief is a worker with a chief designation
   on its organization, as RFC section 4 defines it. Chief replacement swaps
   the organization's reference and preserves organization identity, memory
   binding and history. A chief is not a separate principal kind.
4. **Desktop framework.** `apps/desktop` is built with Flutter, talking to
   the controller only through the versioned socket API. This resolves the
   T0.1 dependency that blocked E2.9.

## Consequences

- The seam contract `contracts/specs/configuration.md` must encode decision 3
  before E2.4 dispatch.
- E2.9's desktop tests target Flutter integration tests against a real
  controller process; no framework-agnostic test harness is planned.
- E1 session 4's prompt already matches decision 2; no plan edit needed.
