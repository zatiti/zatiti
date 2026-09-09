# E2 expansion draft (T2.0 output from the Ox Alpha console, 2026-09-09)

Status: unreviewed proposal copied verbatim from the console conversation. Not part of `plan.md` until reviewed and the three open decisions at the end are ruled on.

Now I can see the actual terrain — and I need to reconcile honestly before executing, because this plan contradicts the working assumption of my earlier T1.x–T5.x scaffold.

Reconciliation, stated plainly:

The earlier scaffold was built against a different (and now superseded) structure. This plan's ownership roots are internal/configuration, internal/skills, internal/connections, internal/policy, internal/effects, internal/accounting, internal/memory, internal/artifacts, apps/desktop — not the operation-registry-as-composition-root I built. Some scaffold content maps over (state lifecycle T1.1, identity/bootstrap T1.2–T1.3, transport parity T1.5/T1.7, the controller lock), and some doesn't (my review/authz design belongs under internal/policy; the compiler/draft/plan/apply flow I never built is the center of E2 — RFC §6). Nothing from the scaffold should be claimed as E1/E2 credit without a task-by-task mapping.

E2's trigger is T2.0, and it's a PLAN task. Per the plan's own operating procedure, outline intentions are never dispatched as coding tasks; T2.0's deliverable is "E2 executable tasks and desktop journeys" with acc: concrete paths, paired API/desktop failure tests, acc criteria, runtime wiring depending on T1.9, and bootstrap ownership scopes released before dispatch. So the correct execution of "plan E2" is to author that expansion now. Its stated acceptance: desktop and coding-agent interfaces create Engineering and Marketing with atomic chief creation; stale plans, cycles, unsafe skill archives, and unauthorized reparenting fail; desktop shows controller-backed state and reconnects without duplication.

Here is the T2.0 expansion.

E2 expanded — hierarchical chiefs, authoring and desktop shell

Roots: internal/configuration, internal/skills, internal/connections, internal/transport, apps/desktop
Coverage: UC-003/004/008/016; Z03, Z04, Z05, Z15, Z17, Z21
Global prerequisite: T1.9 (foundation parity gate). Every task below with runtime wiring depends on it; pure-compiler and pure-schema tasks may run earlier on disjoint scope per the plan's wave rules.

E2.1 — Compiler core and revision store

Owner: pool · Est: 90m · Root: internal/configuration · Deps: T1.1 (state), T0.8 (schemas)
Delivers: canonical JSON loader (internal/configuration/canonical.go), draft store, revision ledger.
acc:

Canonicalization sorts maps and declared sets, preserves skill byte order, rejects duplicate keys, unknown fields outside x-zatiti.* inert extensions, cyclic references. Negative fixture per rejection class.

Draft create/get/update/list/discard operations stage changes without touching effective configuration — test asserts effective revision unchanged after draft mutation.

Revision list/get return monotonically increasing versions with content digests; digest recomputation from stored definition matches.

Paired failure tests: malformed JSON input to each draft op returns invalid_input envelope; discard of applied draft is a no-op refused with not_found.

E2.2 — Plan validation and sealed preview

Owner: pool · Est: 90m · Root: internal/configuration · Deps: E2.1
Delivers: configuration.plan — base-revision pinning, candidate digest, changed-object set, dependency resolution, missing-requirements report.
acc:

Plan pins base revision; a concurrent revision advance makes apply refuse stale_version and the plan requires regeneration with decision digests renewed (fixture: two writers, one winner).

Plan returns structured preview + missing requirements (unverified connection, missing profile) and never claims activation. Fixture asserts prerequisite_missing code.

Authority delta recorded against current policy, not proposed policy — fixture: plan that expands authority under a policy the plan itself narrows is evaluated under old authority and refused/reviewed accordingly (Z04).

Paired failure tests: plan against unknown base revision, plan with digest mismatch on retry.

E2.3 — Atomic apply with submission-key semantics

Owner: pool · Est: 90m · Root: internal/configuration · Deps: E2.2, T1.6 (commands)
Delivers: configuration.apply — single transaction: recheck head/authority/restrictions/dependencies/decisions, record revision + event + outbox row.
acc:

Two conflicting concurrent applies: exactly one succeeds, loser gets stale_version (real two-process fixture, not goroutine-only).

Retry of completed apply with same submission key returns the original command disposition (Z04, T1.6 reuse).

Rollback is a new plan against current revision — fixture asserts it is not a version rewind.

Destructive removal with active runs/unresolved operations is blocked; with explicit archival disposition it proceeds and leaves accounting obligations visible (§6).

Paired failure tests: apply with decision bound to superseded digest refused; apply with dependency whose identity changed since plan sealed.

E2.4 — Organization/chief atomic creation

Owner: pool · Est: 90m · Roots: internal/configuration, internal/identity (via ports) · Deps: E2.3
Delivers: organization.create flow that stages child organization and its designated chief worker in one plan; chief replacement operation preserving org identity/memory/obligations.
acc:

Creating an organization and its chief is one plan/apply; crash between staging and activation leaves a draft, never a chiefless live org (kill-point fixture).

Cycle rejection: reparent A→B→A refused with invalid_input naming the cycle (Z17).

Chief replacement preserves organization ID, revision lineage, memory binding, and history — fixture asserts all survive.

Ordinary worker creation does not create an organization — fixture asserts no org row.

Scoped reparenting: mover without authority over both source and destination ancestry denied (Z17); reparent rechecks inherited ceilings and memory bindings, refusing widening moves.

Paired failure tests: unauthorized reparent (Z17 fixture), child ceiling exceeding ancestor refused at apply.

E2.5 — Skills import and qualification metadata

Owner: pool · Est: 90m · Root: internal/skills · Deps: E2.1 (definitions), T0.8
Delivers: validating SKILL.md/Agent-Skills import adapter; immutable draft versions with hashes, provenance, diagnostics.
acc:

Negative fixtures each executed: path traversal, symlink escape, device file, duplicate/case-colliding paths, oversized extraction, dependency cycle (Z05). Import refuses before any extraction executes; no script runs during import (fixture asserts zero subprocess spawns).

Imported version is immutable — attempted content mutation creates a new version, never rewrites (fixture).

Evaluation against sealed fixtures pins candidate/evaluator/inputs/profile/limits; a changed skill or evaluator invalidates only dependent qualifications (fixture listing affected quals before/after).

Imported text cannot install grants — fixture imports skill whose body declares a policy/grant extension and asserts it lands as inert content (Z05).

Paired failure tests: archive bomb, colliding paths differing only in case.

E2.6 — Connections, credential references and validation

Owner: pool · Est: 90m · Roots: internal/connections, internal/identity (ports) · Deps: T1.2 (credential source)
Delivers: connection.create/list/get/update/validate; typed setup lifecycle (begin/status/complete/cancel); rotate distinct from substitute-account.
acc:

Connection creation grants nothing until binding + activation — fixture asserts worker with unbound connection gets permission_denied on use.

connection.validate performs bounded probe, records observed identity/scopes + freshness; unverified connection blocks executable binding activation with prerequisite_missing (E2.2 fixture interplay).

Setup lifecycle: raw secret appears in no tool argument, no log line, no MCP result, no export (scrub fixtures over captured stdout/stderr/envelopes — Z13).

Rotation vs. account substitution are distinct operations with distinct authorization checks (fixture: substitution requires owner-class decision, rotation does not).

Missing connection/price/profile yields named refusal prerequisite_missing, no fallback (fixture per name).

E2.7 — Export/import portability

Owner: pool · Est: 60m · Root: internal/configuration · Deps: E2.4, E2.5, E2.6
Delivers: zatiti.organization/v1 export and import.
acc:

Round trip: export org → import into second installation (fixture uses second data dir) → identical canonical definition, stable IDs where required, rebinding required for credential references (asserted as prerequisite_missing post-import).

Export excludes secret values, credentials, task histories, operation state — fixture scans export bytes for seeded synthetic secrets (Z15).

Omission of an object in a complete snapshot never deletes live objects; deletion is explicit (fixture).

E2.8 — Transport surface for E2 operations

Owner: pool · Est: 90m · Root: internal/transport · Deps: E2.3/E2.4/E2.5/E2.6/E2.7 handlers registered
Delivers: registry descriptors the organization/skill/connection/configuration/draft families with both CLI and MCP mappings; zatiti_organization_create naming per §8.2.
acc:

Every E2 operation exposed in both transports with identical schemas, error mapping, pagination (Z02 subset — full Z02 remains E7).

Capability listing advertises only implemented ops; unavailable E2 ops are absent, not stubbed.

MCP arguments cannot select a more privileged credential profile (fixture attempts override, refused).

Paired failure tests: unknown operation, malformed input, denied principal — same envelope and exit code in both transports.

E2.9 — Desktop shell: chat list, hierarchy, organization cards

Owner: desktop lane · Est: 2×90m · Root: apps/desktop · Deps: T1.9, E2.8 (versioned API only), T0.1 (framework selection)
Delivers: first-launch personal-chief conversation, conversation list with All-chats default, org selector + expandable hierarchy, breadcrumb path (Studio > Engineering > Quality), organization cards showing proposed/awaiting/created from committed state, reconnect-without-duplication.
acc:

Real renderer journeys (per plan: paired desktop tests, happy + failure): create Engineering and Marketing via conversation → cards render controller-committed state; killing the desktop mid-flight and reopening shows no duplicate organizations and no phantom card (Z21).

Hierarchy filter selects org + descendants without navigation; ambiguous search shows ancestry; membership not conveyed by color alone.

Unsent draft is visually distinct from sent; displayed state is cached and labeled stale while disconnected — a claimed approval offline is never shown as acknowledged (§3).

Closing desktop while controller runs: controller continues (fixture: controller process alive, task admission proceeding).

Failure path: controller unavailable renders named availability error, does not spawn a second controller (Z16-adjacent fixture).

E2.10 — E2 integration and parity gate

Owner: acceptance · Est: 90m · Root: tests/acceptance/ · Deps: E2.4, E2.5, E2.6, E2.7, E2.8, E2.9
Delivers: executed Z03/Z04/Z05/Z15/Z17 cases; Z21 desktop journey evidence; M1's desktop-chief vertical slice.
acc:

The plan's E2 acceptance sentence becomes named cases: agent creates Engineering + Marketing with atomic chief creation via both MCP and CLI (Z03 journey start); stale plan fails (E2.3 fixture executed at integration); cycle fails; unsafe archive fails; unauthorized reparent fails.

Desktop + coding-agent interfaces observe identical controller state for the same operations (Z21 ↔ Z02 normalization rules: only IDs/timestamps normalized).

Retained failure evidence per executed negative case; zero-case suites record not run, never pass.

Sequencing note for the wave table: E2.1/E2.2 and E2.5 can start in parallel with E1's later waves on disjoint roots (consistent with W3's note that expanded E2 work may run alongside foundations). E2.3 onward needs T1.6; E2.4+ needs T1.9's real controller. E2.9 must not begin before T0.1 resolves the desktop framework — the plan explicitly forbids silently assuming an operator framework preference, so that's an escalation point, not a default.

Open decisions T2.0 must surface to the coordinator (per its acc):

Desktop framework selection (T0.1 dependency) — blocking E2.9, material product choice, escalate don't assume.

Whether the earlier scaffold's review/authz work is refactored into internal/policy or discarded — architecture call, affects E2.4's authority checks.

Chief "designated worker" representation: a principal kind or a worker with chief flag — contract-authoring decision that must be sealed in contracts/specs/configuration.md before E2.4 dispatch (the plan's no-self-authored-contract rule applies).

Next eligible after this expansion: T0.1/T0.2 remain the true frontier per the plan's hand-off — E2 tasks are scheduled, not dispatched, until contracts freeze and the E0 canary passes. Want me to draft any of the sealed seam contracts (configuration.md, skills.md, connections.md) next, or take the scaffold-mapping question first?
