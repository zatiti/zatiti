# ADR 001: Self-contained package implementation specifications

## Status

Accepted. Revised 2026-09-09 for committed, self-contained package prompts.

## Context

Parallel Go coding sessions need disjoint ownership, exact shared contracts, and complete local context. A package agent must be able to implement its assignment without the RFC or a particular coding harness.

## Decision

Freeze package roots, operation schemas, ownership and shared contracts in `docs/implementation/`. Commit a self-contained `AGENTS.md` in every implementation root. Each contains its mission, write scope, requirements, incoming and outgoing interfaces, persistence and failure semantics, and acceptance criteria. References to the RFC never substitute for needed context.

Commit specification sources, a deterministic renderer, and its rendered prompts. Fresh clones require no rendering service. Implementers do not independently edit generated prompts. Architecture changes update sources and affected prompts together; drift and coverage checks verify agreement. There is no dependency on Kazi, apply, a model, or an external goal store.

Freeze contracts before parallel implementation. Internal implementation files can evolve within their package. Public types and schemas have one owner; embedded copies are synchronized projections. Report contract defects with affected dependencies and proposed revisions. Preserve evidence and invalidate qualifications affected by changes.

Agents write only in their assigned root. Concurrent scopes must be disjoint, including ancestor/descendant relationships. Root dependencies, shared contracts, CI and final assembly have designated owners. Use isolated worktrees during parallel implementation and one integration owner for landing. Worktrees are coordination aids, not security boundaries.

Parallel code generation uses frozen interfaces and local fakes. Executable integration follows dependency order. Cross-package transactions, crash recovery, transport parity, real desktop journeys and external adapter qualification have explicit integration owners. Passing unit tests is not release completion.

Harness, model, permissions, budgets and scheduler remain operator choices. Optional execution tooling must honor these scopes and criteria. This ADR does not authorize publishing or external account actions.

## Consequences

Directory-local agents need no RFC copy. Prompts intentionally repeat relevant contracts and requirements. A requirement map and reproducible generation expose missing ownership and stale copies. Dependency qualification remains explicit implementation work, not an assumed property of upstream documentation.

The former ignored Kazi projections, external executable goals, JIT generation and apply-owned scheduling are superseded. References to the former `docs/plan.md` are removed. See `../implementation/README.md` for current ownership and sequencing.
