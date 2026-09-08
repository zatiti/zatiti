# Execution protocol

## Authority and inputs

Read `rfc.md` for product requirements, `plan.md` for the current task graph, and ADR 001 for ownership. The plan is not a command to begin execution. Obtain the user's execution scope, available pool, budget and external-action authorization before starting work that requires them.

Runtime configuration belongs to the operator. Required semantic fields are execution mode, selected harness, model selection strategy (explicit model or explicitly harness-managed), supported reasoning settings if any, credential references, permission profile, total session budget, per-task session allocation, iteration/spend/time limits, build capacity, integration target, and qualified tool versions. These are requirements for the dispatch adapter, NOT a fabricated Kazi configuration schema. T0.3 maps them onto actual installed help/schema. Do not put credential values in goals, command arguments, logs or generated instructions.

No harness/model default or fixed fallback is permitted. A retry may use only the configured profile; a model/harness escalation requires a preconfigured allowed transition or a new operator selection. Unsupported profile capabilities produce a prerequisite failure.

## Before the first task

1. Preserve existing work and identify the integration base; do not reset the user's checkout. Read session coordination messages and machine build policy.
2. Run applicable preflight. Detect installed Kazi and query its machine-readable commands/schemas. Qualification records name the actual binary version and tested harness.
3. Resolve desktop framework, controller dependency pins, Serenity version and live-release prerequisites through T0.1. Missing resources remain explicit blockers for dependent goals.
4. Freeze directory and seam contracts. Reserve root manifests, contract registry and generation outputs to named roles.
5. Run T0.3-T0.6 using disposable fixtures. Verify worktree routing, prompt generation, acceptance protection, concurrency accounting and landing ownership before production task dispatch.

## Per-task lifecycle

1. Select a task whose concrete dependencies are integrated. Outline epics are planning work, not implementation prompts. Resolve current files, ancestor instruction files, active scopes and resource holders.
2. Acquire its task claim and required shared-resource claims using the canonical claim primitive. Proceed only on an explicit WON result. Retain the acquired identity for release. Do not confuse command success with winning a claim.
3. Derive predicates from every `acc:` requirement against the current base. Include real positive and forbidden-behavior cases, relevant suite guards, scope/contract integrity, and the required integration disposition. Distinguish a capability already implemented from a vacuous predicate. Guard failures on an incomplete scaffold mean the task is not ready, not permission to ignore guards.
4. Review and seal the executable goal. Keep its file and acceptance inputs outside the implementation write scope. Record task ID, source plan revision, goal digest, contract revision and base revision. Never relax predicates from inside a failing convergence lane.
5. Allocate an isolated worktree through the qualified owner. Never precreate names reserved by the Kazi scheduler. The per-task allocation includes any inner workers. Enforce one live writer for each owned root across the pool.
6. Render the scope prompt from the sealed goal and current observations using the installed renderer. Refuse collisions with authored instruction files. Launch at the declared root or deliver the equivalent pinned rendered prompt through the qualified lane adapter.
7. Converge with the selected runtime profile. Monitor terminal outcomes and resource limits. Unknown termination is not convergence. Inspect permission failures and infrastructure errors before spending more iterations.
8. Record actual predicates, executed counts, artifacts and unresolved issues. Submit implementation and docs for independent review. Only the integration owner lands after required checks. If Kazi lands internally, that landing must remain within the prequalified review-gated target.
9. Run affected integrated checks, update task disposition and `roadmap.md`, preserve task/run evidence, then release only claims still owned by the acquired identity. Remove worktrees only after ownership and retained work are accounted for.

The generated instruction node is a projection. Updating a source goal invalidates its old projection and requires a new reviewed dispatch; hand-editing AGENTS.md does not change acceptance.

## Failure handling

| Observation | Action |
|---|---|
| Predicate fails and behavior is absent | Continue within configured task budget |
| Goal is mistargeted or internally unsatisfiable | Stop; coordinator repairs the contract with an explicit revision |
| Required API differs from frozen contract | Report exact mismatch; pause affected consumers; architecture owner resolves |
| Worktree or prompt freshness conflict | Stop that lane; preserve evidence; repair ownership before retry |
| Harness lacks required capability | Mark prerequisite missing; do not silently change agent |
| Session dies | Reconcile its claim, process and worktree before replacement; lease expiry alone does not prove the old process stopped |
| Task converges but cannot integrate | Retain branch and evidence; integration failure remains open |
| Provider/live platform unavailable | Mark tests not run; do not fabricate release readiness |

## Session and build allocation

The hypothetical maximum is 24 active sessions across all coordinators, executors, reviewers and child agents. Respect a smaller actual harness/tool limit. The coordinator leases a numerical session allowance to each convergence owner. No unbounded nested parallelism. If Kazi cannot enforce the intended allocation, qualify a serial task mode and keep parallelism in the outer scheduler.

On the mini, all qualifying multi-package heavy commands use the host-wide build lease and load gate specified by the user's machine instructions. At most two heavy lanes per project, and the stricter host-wide lease wins. Tests that need graphical focus run in an explicitly suitable environment. Remote build capacity is used only where already available and authorized.

## Evidence and release

Evidence records task/goal identity, exact tested revision, tool/profile versions, suite names, executed/pass/fail/skipped counts, provider call counts where relevant, result artifacts, integration disposition and unresolved risks. Keep secrets and private environment identifiers out of published evidence.

Local task convergence, integrated behavior, live adapter qualification and released artifact qualification are different states. The release matrix must cover every Z01-Z21 case and use case on the same candidate. Negative acceptance checks must demonstrate rejection of the forbidden behavior; a file-existence check is not implementation evidence.

For this self-hosted desktop product, live verification means installed artifacts talking to the intended operator-controlled controller and explicitly authorized real providers. It does not require inventing a public cloud production deployment. Release publication and external communication require user authorization; generic skill shipping defaults do not grant it.
