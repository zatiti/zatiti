# ADR 001: Directory-scoped, harness-neutral execution

## Status

Accepted for execution planning; tool qualification and implementation remain pending.

## Date

2026-09-07

## Context

Zatiti's full RFC spans several interacting domains. Parallel coding sessions need stable ownership, shared interfaces, objective acceptance and a consistent way to recover context. Hand-written prompts copied into each task drift from the actual acceptance criteria. Assigning an entire repository to every session creates conflicting state ownership and integration churn.

The user proposes freezing the directory structure and placing task context and goals beside the implementation. Kazi documents scope-root prompt rendering from authoritative goals, protected contract inclusion, and harness-native instruction delivery. The user explicitly requires freedom to choose the coding agent.

## Decision

Freeze ownership roots and seam contracts per execution wave. Implementation structure inside a root may evolve. The table in `../plan.md` defines the initial roots. Every active implementation goal declares explicit write scope; concurrently rendered roots must be disjoint, including ancestor/descendant relationships. Cross-module work uses explicit dependencies and an integration task.

Keep authored acceptance criteria in the plan and derive executable goals just in time against the actual dispatch base. Seal the executable acceptance inputs before convergence. Store authoritative goals outside implementing roots under `goals/`; use the installed Kazi format rather than inventing a `kazi.goal` schema. The proposed filename convention is `<task-id>.goal.toml`. It is a convention, not a claim that any file with that extension has been validated or approved.

Use Kazi-generated AGENTS.md/CLAUDE.md projections at scope roots, ignored and untracked. Root instructions contain stable conventions. Generated files carry task intent, protected contract, predicates and evidence; they are never independently edited or treated as authority. Qualify the renderer's freshness and forbidden-path behavior before using it for unattended dispatch. Existing hand-written instruction files must not be overwritten.

Store public seam contracts outside implementation write scopes. The architecture lane owns contract revisions; an implementation agent cannot lower its own acceptance bar. A required contract change produces evidence, affected-dependency analysis and a new reviewed contract/goal revision. Preserve old run evidence and invalidate dependent qualifications where necessary.

Select coding harness, provider/model strategy, permissions, resource budgets and optional fallback ladder from explicit operator runtime configuration. No product source, goal or generated prompt pins a coding-agent identity. Validate the selected harness's required capabilities; refuse unsupported execution instead of silently switching. Record resolved identities in protected operational evidence, not public product examples.

Use one cross-task scheduler: apply's claim-driven coordinator. Each task leases a share of the total session budget to Kazi. Inner partitioning must fit that allocation; otherwise use a qualified serial mode. Do not run an independent Kazi fleet scheduler over the same tasks. Design-heavy tasks may use direct agent execution with the same accepted criteria and Kazi check-only evidence where supported.

One integration owner controls landing to the product branch. Kazi automatic landing is permitted only into a disposable or review-gated integration target after its behavior has been demonstrated in a canary. If the installed execution mode cannot preserve the review boundary, use apply-owned execution and Kazi check-only until fixed. Worktree isolation is mandatory for concurrent implementation; it is not a filesystem sandbox or authority boundary by itself.

## Consequences

An agent opened in an owned directory receives reconstructable task context. Goals and contracts remain protected outside the code it edits. Scope and dependency checks expose coordination conflicts early. The directory layout is not inflated to match a session count, and frozen boundaries may be revised with evidence between waves.

Fresh clones need prompt rendering before directory-local work. Nested active goals are unavailable under the documented Kazi rendering model. JIT goal generation means this planning deliverable intentionally contains acceptance rows rather than purportedly executable preapproved goal files. Integration, harness compatibility and acceptance evidence must be qualified independently of the tools' documentation.
