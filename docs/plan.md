# Zatiti execution plan

## Context

Build the complete first release specified in `rfc.md`: a personal-chief desktop workspace, hierarchical organizations, durable workers and responsibilities, Serenity memory, earned autonomy, and equivalent CLI/MCP management. Target a 48-hour delivery exercise with at most 24 active coding-agent sessions, including coordination and review. This is a planning envelope, not a release promise. All RFC gates Z01-Z21 remain mandatory.

The user requires harness-neutral execution. No task, goal, script, generated prompt, or checked-in runtime default selects a coding harness, provider, model, or reasoning tier. Select these through an operator-supplied execution profile, validate capabilities, and record the resolved selection with run evidence. Product model connections are separate from the coding agents building the product.

Planning is authorized; implementation, fleet dispatch, paid inference, publication, and deployment have not started. The repository is at design stage. This plan does not assert that directories, APIs, goal files, or tests described below already exist.

## Discovery summary

Observed root artifacts: `rfc.md`, `README.md`, and `LICENSE`; no product implementation or existing plan was found in the root inventory. The RFC is the requirements authority. Prior local inspections establish useful references, not reusable conformance evidence: Rakazo for desktop interaction and effect recovery; Serenity for memory; DeepSeek Harness for executor capabilities, durable messages, and recorded model context.

Kazi is installed. Its source documentation describes directory prompt rendering from declared scope, protected contract inclusion, disjoint render roots, and generated untracked AGENTS.md/CLAUDE.md files. The installed binary's exact behavior is NOT qualified by this planning pass. T0.3-T0.6 explicitly resolve that before unattended use.

Use-case discovery: 16 first-release use cases, all PLANNED; none claimed implemented or tested. `docs/usecases.md` is the readable catalog. `.claude/scratch/usecases-manifest.json` is its machine-readable planning projection. Status is not inferred from the existence of a test file.

## Scope and deliverables

| ID | Deliverable | Owner role | Acceptance |
|---|---|---|---|
| D1 | Frozen ownership roots and public seam contracts | Architecture lead | Disjoint active scopes, dependency DAG, protected acceptance inputs |
| D2 | Harness-neutral task dispatch | Execution coordinator | Selected harness passes canary; concurrency bounded; no default agent identity |
| D3 | Controller, registry, CLI and MCP | Backend and transport lanes | Real authenticated operations and Z01-Z16 coverage |
| D4 | Organizations, execution, memory and autonomy | Domain lanes | Personal-chief setup and Z17-Z20 coverage |
| D5 | Desktop workspace | Desktop lanes | Real human journeys and Z21 coverage |
| D6 | Qualified release artifacts | Integration and acceptance leads | Z01-Z21 and all journeys pass on exact release candidate |

Remain within the RFC exclusions: no extra provider breadth, marketplace, mobile client, standalone web product, multi-controller ownership, or automatic controller migration. Kazi and the planning skills are development tools, not Zatiti runtime dependencies.

## Directory ownership contract

Freeze the following ownership roots at T0.2. These are logical modules, not a requirement to create one Go module or process per row. Internal subdirectories may evolve within a root. Never allocate a second active rendered goal below an already owned root.

| Scope root | Owns | Imports/calls through protected contracts |
|---|---|---|
| `contracts/` | Shared wire schemas, domain ports, events, compatibility fixtures | No product implementation |
| `internal/state/` | SQLite lifecycle, migrations, transaction primitives, ordered writes, ownership | Shared types |
| `internal/identity/` | Principals, grants, profiles, secure credential access | State, shared types |
| `internal/configuration/` | Organizations/chiefs, workers, projects, bindings, compiler and revisions | State, identity/policy ports |
| `internal/skills/` | Safe import, immutable skill packages, qualification metadata | Artifact and configuration ports |
| `internal/connections/` | Provider identities, consent/provisioning, tool contracts, validation | Identity, configuration ports |
| `internal/policy/` | Scope intersection, reviews, promotions, demotions and requalification | State, identity and evidence ports |
| `internal/tasks/` | Task graph, responsibilities, schedules, durable mailboxes and claims | State, policy, execution ports |
| `internal/execution/` | Owned model loop, executor adapters, context/checkpoint lifecycle | Task, effect, memory and artifact ports |
| `internal/effects/` | Logical effects, physical attempts, dispatch claims, adapter invocation and reconciliation | Policy, accounting and connection ports |
| `internal/accounting/` | Currency units, rate arithmetic, hierarchical reservations and settlement | State, shared types |
| `internal/memory/` | Serenity adapter, brain bindings, curation and correction lineage | Policy, accounting, artifact and task ports |
| `internal/artifacts/` | Content-addressed files, bounded transfers, evidence projections, backup/restore coordination | State and memory checkpoint ports |
| `internal/transport/` | Operation registration, HTTP/UDS/TLS, CLI and MCP adapters | Domain application ports |
| `internal/app/` | Composition root, dependency wiring, startup/shutdown | All domain constructors |
| `apps/desktop/` | Human chat UX, hierarchy, work details, decisions, secure client connection | Versioned controller API only |
| `tests/acceptance/` | Independent cross-module tests, fault scenarios, release evidence | Public process/protocol boundaries |
| `scripts/` | Contract/ownership checks, dispatch adapter, packaging commands | Public tooling surfaces |
| `docs/` | Plan, ADRs, use cases, operator and contributor documentation | Source of reviewed intent |

Repo-root files (`go.mod`, `go.sum`, root instructions, packaging manifests), `.github/`, `cmd/zatiti/`, and generated transport outputs have explicitly assigned integration owners. Tasks touching these declare exact paths and resource locks; unscoped goals are prohibited. Kazi does not render at repository root. Prefer a thin command entry point with behavior in owned roots.

Each executable root has a protected seam document at `contracts/specs/<domain>.md` or a narrower checked-in public schema. Implementers may read but not modify their own contracts. A contract-authoring task has no contract reference to the very file it must author; before dependent implementation starts, the contract is sealed and removed from implementers' write scope. Cross-root contract changes stop only affected dependents and create a reviewed revision.

`goals/<task-id>.goal.toml` is the proposed repository convention for authoritative task goals. Actual Kazi payloads are generated just in time from `acc:` criteria, validated with the installed schema, reviewed, and sealed at dispatch. Do not create fake ready-to-run goals during planning. Goals live outside implementation roots. A module may receive successive goals; exactly one is active/rendered there at a time.

At dispatch Kazi generates `<scope>/AGENTS.md` and the supported CLAUDE.md projection from the goal, contract and current evidence. They are ignored/untracked and never manually authored as competing task prompts. Root conventions stay authored and committed. Fresh clones regenerate scope prompts before starting directory-local sessions. See ADR 001 and `docs/execution.md`.

## Checkable work breakdown

Task estimates are focused session time. Each engineering `acc:` entry is an independent requirement to translate into predicates; passing one does not satisfy the others. All tasks are unclaimed. `Owner: pool` is a role, not a named agent. Every implementation task includes paired automated behavior tests and relevant formatter/linter checks; API changes exercise a real request with status/body assertions, and desktop changes exercise a real renderer with happy and failure paths.

### E0 -- Ownership, contracts and execution bootstrap

fidelity: executable

Acceptance: independent workers can be dispatched into disjoint roots using a selected harness, sealed acceptance criteria, and a proven integration path. No product capability is marked complete by this milestone.

- [ ] T0.1 Establish runtime and release prerequisites. Owner: coordinator Est: 45m kind: any delivers: [operator runtime profile and qualification target manifest] deps: [] acc: [profile requires explicit harness/model or harness-managed model selection; records credential references without values; names desktop OS targets, available signing credentials, provider test accounts, controller host and total budget; absent prerequisites have explicit owners]

Scope: operator-local configuration and `docs/execution.md`. Pin controller/toolchain, SQLite driver, MCP generator/SDK, desktop framework, and Serenity version before code dispatch. Desktop framework selection must account for macOS/Linux packaging and the existing Go-controller boundary. Do not silently convert the user's framework preference into an assumed decision. Record a justified selection ADR using available constraints; escalate only a material unanswered product choice. Separate unsigned local qualification from signed distribution.

- [ ] T0.2 Freeze ownership and dependency contracts. Owner: architecture Est: 90m kind: agent lane: agent delivers: [contracts/specs and root conventions] deps: [] acc: [every RFC operation family and state owner maps to one root; contracts name inputs, outputs, errors, transactions, events and failure semantics; no live scope is nested; cross-domain dependency cycles resolve through ports and composition]

Scope: `contracts/specs/`, root instruction files, ADR 001. Use the directory table above. Write seam contracts for all domains before freezing revision 1; document stable event names, version checks, cancellation and uncertainty. No empty interfaces or arbitrary shared utility packages solely to fill the tree. A contract is executable-ready only when an independent implementer can determine observable results without inventing authorization or recovery semantics.

- [ ] T0.3 Qualify installed Kazi and harness profile. Owner: execution Est: 60m verifies: [infrastructure] deps: [T0.1] acc: [machine-readable help/schema identify supported selected-harness dispatch, goal sealing, scope rendering, check-only evaluation, concurrency and landing behavior; selected harness executes a harmless isolated fixture; an unavailable capability refuses without fallback to another harness]

Scope: disposable tooling fixtures and an operator-local qualification report. Read the installed help/schema; do not copy stale invocation flags from skills. Resolve the documented precreated-worktree contradiction and automatic-landing behavior. Record exact supported commands, versions and permission behavior. Do not disable sandbox/approval controls merely to fit a skill recipe.

- [ ] T0.4 Implement ownership and contract lint. Owner: pool Est: 60m verifies: [infrastructure] deps: [T0.2, T0.3] acc: [positive fixture accepts disjoint declared roots; fixtures reject nested roots, unscoped implementation goals, missing contracts, writable protected contracts and unresolved dependency IDs; a goal cannot weaken its acceptance inputs]

Scope: `scripts/` plus disposable fixtures. Use Kazi's native checks where they cover the requirement; add only missing repository checks. Pair each refusal with an executed negative fixture. Changes to root manifests go through the integration owner.

- [ ] T0.5 Implement profile-driven dispatch adapter. Owner: pool Est: 90m verifies: [infrastructure] deps: [T0.3, T0.4] acc: [dry-run outputs resolved task scope and harness selection without secrets; missing selection refuses; coordinator and child sessions share one session ceiling; one claimed task has one convergence owner; generated prompts follow scope and include protected contract]

Scope: `scripts/`. Support the installed Kazi API through an adapter rather than exposing fixed command strings in every task. Test at least two synthetic harness profiles for configuration neutrality; only claim real compatibility for an actually qualified harness. The task coordinator owns cross-task scheduling; Kazi may partition within the leased task allocation only.

- [ ] T0.6 Run execution and landing canary. Owner: execution Est: 60m verifies: [infrastructure] deps: [T0.5] acc: [two disjoint fixture tasks converge into isolated branches and reach a disposable integration branch through review gates; forbidden contract edit and prompt tamper fail; interrupted dispatch retains recoverable evidence; no change lands on product main before review]

Scope: disposable repository, harness profile, evidence. Exercise actual chosen harness and Kazi, not only a fake dispatcher. A nested fan-out cannot exceed its allocated session count. If Kazi's automatic landing cannot be safely targeted, use its check-only mode with apply-owned workers until that execution mode is qualified; disclose the mode, preserve the same predicates, and do not run a second coding loop against the same task.

- [ ] T0.7 Create executable repository and CI scaffold. Owner: integration Est: 90m verifies: [infrastructure] deps: [T0.1, T0.2] acc: [pinned toolchain compiles minimal controller and desktop scaffolds on declared development target; scoped formatter/linter/test commands run through CI; fixture failure fails CI; generated files have an identified owner; no product-ready claim comes from scaffold checks]

Scope: exact root manifests, `cmd/zatiti/`, `internal/app/`, `.github/`, and desktop entry files during bootstrap only. Release these bootstrap scopes before E1 and desktop lanes claim them. No runtime stubs presented as supported operations. Keep product dependencies and coding-harness dependencies separate.

- [ ] T0.8 Freeze transport and acceptance schemas. Owner: acceptance Est: 90m verifies: [UC-002, infrastructure] deps: [T0.2, T0.7] acc: [versioned operation envelope, IDs, errors, pagination, event cursors and command identities have accepted/rejected fixtures; Z01-Z21 map to independent acceptance owners; all capability cases start planned or failing rather than assumed passing]

Scope: `contracts/` and `tests/acceptance/` sequentially under explicit ownership. Define provider-call counters, fake clock interfaces, process kill points and evidence output format. Reserve verifier/acceptance fixture write access to the acceptance lane. Implementation lanes can add local tests but cannot relax acceptance thresholds.

### E1 -- Authenticated controller and first real boundary

fidelity: executable

Acceptance: a real controller initializes scoped identity, survives restart, and serves an authenticated operation through CLI and MCP with equivalent state and errors. This is a foundation milestone, not the full personal-chief journey.

- [ ] T1.1 Implement SQLite ownership and write transactions. Owner: pool Est: 90m verifies: [UC-001, UC-015] deps: [T0.6, T0.7, T0.8] acc: [second controller cannot acquire installation; restart increments persisted generation; state and outbox commit or roll back together; lock loss stops admission; contention returns within configured bound]

Scope: `internal/state/`. Pair transactional tests with two real controller ownership processes. No model/network calls inside transaction retries. Migrations are additive, uniquely identified per owner, and applied through the state migration registry without concurrent editing of one migration file.

- [ ] T1.2 Implement credential-source adapter. Owner: pool Est: 60m verifies: [UC-001, UC-008] deps: [T0.6, T0.7, T0.8] acc: [secure-store fixture returns credential handles without exposing bytes in public output; missing key store fails explicitly; explicitly provisioned headless source works; child process environment lacks owner credentials]

Scope: `internal/identity/`. Tests use synthetic secrets and assert absence in captured stdout/stderr/errors. Qualify the real OS store during release qualification; fake-store tests alone do not advertise platform support.

- [ ] T1.3 Implement bootstrap and principal authentication. Owner: pool Est: 90m verifies: [UC-001] deps: [T1.1, T1.2] acc: [bootstrap succeeds once and refuses reinitialization; provisioned profile authenticates its principal; profile name alone grants nothing; revoked credential fails; initialization transaction creates root organization/chief identity through the agreed bootstrap port]

Scope: `internal/identity/`. Test through a real service boundary and persisted state. Bootstrap ownership and secure-store failure have explicit recovery dispositions; no token is returned to model context. Root-chief activation waits for executable prerequisites.

- [ ] T1.4 Implement scope intersection policy primitive. Owner: pool Est: 90m verifies: [UC-003, UC-009] deps: [T0.6, T0.7, T0.8] acc: [explicit deny defeats grants; child cannot exceed any ancestor ceiling; cross-project or organization access without binding fails; unknown required condition refuses; revocation is checked from current state]

Scope: `internal/policy/`. Pure fixtures may precede storage wiring; task acceptance requires the defined state-backed policy port once composed at T1.8. No model-based authorization decisions.

- [ ] T1.5 Implement operation registry and local API adapter. Owner: pool Est: 90m verifies: [UC-002] deps: [T0.6, T0.7, T0.8] acc: [every registered operation declares schemas, authorization and error semantics; invalid input returns stable envelope/status; real private socket handles authenticated requests; unknown operation refuses]

Scope: `internal/transport/`. Start with capabilities, health and bootstrap/profile operations; unavailable operations are not advertised as implemented. Domain behavior stays in application services.

- [ ] T1.6 Implement durable command and event lookup. Owner: pool Est: 90m verifies: [UC-002, UC-015] deps: [T1.1] acc: [same principal/key/request returns original command; changed request with same key refuses; lost acknowledgment is recovered by lookup; expired cursor requires snapshot rather than silently skipping events]

Scope: `internal/state/`. Tests restart the store and inject acknowledgment loss. Command ownership stays separate from transport request IDs.

- [ ] T1.7 Implement CLI and MCP adapters. Owner: pool Est: 90m verifies: [UC-002] deps: [T1.5] acc: [real CLI subprocess and MCP client discover the same implemented operations; JSON envelopes and documented CLI exits match; tool arguments cannot override credential profile; MCP process does not become a database writer]

Scope: `internal/transport/`. Pin Mint-generated output and SDK under the agreed generator owner. If generated output cannot meet the contract, qualify the RFC's direct SDK fallback and record the change; do not waive parity.

- [ ] T1.8 Compose controller lifecycle. Owner: integration Est: 90m verifies: [UC-001, UC-002, UC-015] deps: [T1.3, T1.4, T1.6, T1.7] acc: [real bootstrap/authentication/capabilities journey works against one controller; service mutation and command/outbox records share the transaction; shutdown joins owned work; restart preserves identity and commands]

Scope: `internal/app/`, thin command entry point under its resource lock. Test the real binary; dependency injection fixtures are not sufficient. No bypass application path for desktop or MCP.

- [ ] T1.9 Prove foundation parity and failure behavior. Owner: acceptance Est: 90m verifies: [UC-001, UC-002, UC-015] deps: [T1.8] acc: [named subprocess suites execute success, bootstrap denial, revoked identity, duplicate controller and lost-ack cases in both transports; normalization preserves authorization/outcome/version differences; scoped lint and format checks pass on integrated revision]

Scope: `tests/acceptance/`. Retain executed case counts and failing-case evidence. Missing suites or zero executed cases do not pass. M1 completes only after reviewed integration and this gate pass.

### E2 -- Hierarchical chiefs, authoring and desktop shell

fidelity: outline

Intent: compiler/revisions, organization/chief creation and replacement, skills, connections, personal-chief onboarding, chat list, hierarchy and real configuration cards. Roots: configuration, skills, connections, desktop, transport. Covers UC-003/004/008/016; Z03-Z05, Z15, Z17, Z21.
Acceptance: desktop and coding-agent interfaces create Engineering and Marketing with atomic chief creation; stale plans, cycles, unsafe skill archives and unauthorized reparenting fail; desktop shows controller-backed state and reconnects without duplication.
- [ ] T2.0 PLAN: expand E2 from frozen contracts and scaffold evidence. Owner: coordinator Est: 60m kind: plan delivers: [E2 executable tasks and desktop journeys] deps: [T0.7, T0.8] acc: [E2 implementation tasks have concrete paths, paired API/desktop failure tests and acc criteria; runtime wiring depends on T1.9; bootstrap ownership scopes are released before dispatch]

### E3 -- Governed execution and first verified artifact

fidelity: outline

Intent: task/run ownership, owned loop and cooperative executor, acceptance verifier, protected effect dispatch, integer accounting, content-addressed artifacts and qualified GitHub action. Roots: tasks, execution, effects, accounting, artifacts. Covers UC-005/006/009/010; Z06-Z13.
Acceptance: configured worker produces independently verified output; model assertion and exit zero alone fail; crash after provider acceptance retains uncertainty and reservation; child tasks share root limits; a changed repository head invalidates applicable review.
- [ ] T3.0 PLAN: expand E3 and its first artifact vertical slice. Owner: coordinator Est: 90m kind: plan delivers: [E3 executable tasks and verifier contract] deps: [T1.9, T2.0] acc: [implementation DAG names actual E2 activation prerequisites; verifier isolation and independent evidence are specified; every physical-call fault has a proving case]

### E4 -- Serenity memory and automatic chief curation

fidelity: outline

Intent: qualify public Serenity interfaces; separate worker/org/installation brains, authorize before retrieval, snapshot recalled context, curate/promote with lineage, reconcile writes and corrections. Root: memory. Covers UC-012/013; Z13-Z15, Z18.
Acceptance: no unauthorized brain is queried; permitted promotion preserves provenance; source correction creates reconciliation work; lost write acknowledgment cannot duplicate claims blindly; paid memory calls obey Zatiti accounting or remain unavailable under hard-cap policy.
- [ ] T4.0 PLAN: expand E4 after adapter feasibility findings. Owner: coordinator Est: 60m kind: plan delivers: [E4 executable tasks and pinned Serenity capability matrix] deps: [T0.1, T0.8] acc: [public read/write/refresh/cost/backup capabilities have source-backed findings and explicit gaps; dependency on E3 accounting/artifacts uses concrete task IDs when expanded; upstream work is tracked rather than hidden in an adapter promise]

### E5 -- Responsibilities, communication and earned autonomy

fidelity: outline

Intent: schedules and repeated reasoning, durable wakes/mailboxes, chief reports, exact qualification evidence, promotions/demotions and requalification. Roots: tasks and policy, serialized with preceding goals in those roots. Covers UC-007/011/014; Z09-Z12, Z19-Z20.
Acceptance: restart preserves both scheduled and reasoning-driven responsibility; bounded quiet cycles do not consume unlimited budget; duplicate mail has one admission; direct user/chief changes conflict visibly; a capability-specific promotion never enlarges its ceiling or waives mandatory human review.
- [ ] T5.0 PLAN: expand E5 from task/effect contracts. Owner: coordinator Est: 60m kind: plan delivers: [E5 executable tasks and autonomy rule fixtures] deps: [T3.0] acc: [tasks depend on actual E3 lifecycle/evidence outputs before code dispatch; periodic and event-driven behavior both have failure tests; implementation cannot edit accepted qualification thresholds]

### E6 -- Daily workspace, recovery and packaging

fidelity: outline

Intent: desktop task/results/decision flows, hierarchy filtering, memory details, Needs you, quiet chief summaries, TLS connection, backup/restore, platform packaging and install docs. Covers UC-009/012/015/016; Z14, Z16, Z21.
Acceptance: close desktop while controller continues; reconnect preserves drafts and exact decisions; restore starts paused and preserves pending effects and brain revisions; real macOS/Linux release artifacts install and execute supported journeys.
- [ ] T6.0 PLAN: expand E6 from integrated product surfaces. Owner: coordinator Est: 60m kind: plan delivers: [E6 executable tasks and platform packaging matrix] deps: [T2.0, T3.0, T4.0] acc: [implementation tasks carry concrete producer dependencies and real desktop tests; signing/unavailable platform prerequisites remain visible; deployment targets and live verification are defined without public-hosting assumptions]

### E7 -- Full release qualification

fidelity: outline

Intent: independent Z01-Z21, all three RFC release journeys, desktop human journeys, real client compatibility, injected failures and clean-install/restore evidence. Root: acceptance tests, then release integration ownership. Covers UC-001 through UC-016.
Acceptance: every mandatory gate passes on one exact release candidate; documentation matches measured compatibility and limitations; unresolved mandatory failure means preview or blocked release, never a silently reduced first release.
- [ ] T7.0 PLAN: expand E7 into exact release probes. Owner: acceptance Est: 90m kind: plan delivers: [release predicate matrix and qualification task DAG] deps: [T5.0, T6.0] acc: [every Z gate and use case maps to named cases and actual producer tasks; release gates depend on implementation completion rather than planning completion; zero-case or skipped suites remain not run]

## Parallel work and dispatch waves

At most 24 active sessions total: one coordinator, one integration owner, one independent acceptance lead, up to 18 implementation sessions, and three review/fault-investigation sessions. These are ceilings and roles, not permanent harness identities. Unused sessions remain idle; directory ownership is not multiplied to keep them busy. The current interactive environment may expose fewer slots: enforce its actual limit until an external pool is explicitly configured and qualified.

| Wave | Eligible tasks | Task sessions | Dependency boundary |
|---|---|---|---|
| W0 | T0.1, T0.2 | 2 | Runtime inputs and ownership draft |
| W1 | T0.3, T0.7 | 2 | Contracts and profile ready |
| W2 | T0.4, T0.8 | 2 | Disjoint script/contract scopes |
| W3 | T0.5, T2.0, T4.0 | 1 implementation + 1 coordinator processing the two planning tasks serially | Actual E2/E4 tasks only become schedulable after expansion |
| W4 | T0.6 | 1 | Canary must pass before product coding fleet |
| W5 | T1.1, T1.2, T1.4, T1.5 | 4 | Independent roots |
| W6 | T1.3, T1.6, T1.7 | 3 | Identity/state/transport roots released by W5 |
| W7 | T1.8 | 1 | Composition sync point |
| W8 | T1.9 | 1 acceptance | Foundation gate |

Waves express dependency frontiers, not mandatory batch barriers. A task may start immediately when its own dependencies and ownership locks clear. Expanded E2/E4 work can run alongside foundations only on disjoint scopes and declared contracts; it must integrate with real producers before acceptance. Later planning tasks amend this wave table with exact tasks/counts; outline intentions are never dispatched as coding tasks.

Shared-file locks cover root manifests, migrations registry, contract registry, plan and generated outputs. Resource claims are acquired separately from task claims, and released with their acquired identity. Worktrees alone do not make overlapping ownership safe. No task claims both an ancestor directory and another lane's child root.

## Timeline and milestones

Hours run from authorized execution start; planning time is not evidence of implementation progress. The controller and adapter feasibility work define the critical path. A slip changes the forecast, not the release requirements.

| Milestone | Target window | Required evidence |
|---|---|---|
| M0 | 0-4h | T0.1-T0.8 complete; qualified execution canary and versioned contracts |
| M1 | 4-12h | T1.9 plus E2/E3 vertical slice: desktop chief, child org, bounded task, verified artifact |
| M2 | 12-24h | All RFC journeys integrated; Serenity, responsibilities and earned autonomy exercise real state |
| M3 | 24-36h | Failure recovery, packaging, actual integrations and platform qualification substantially complete |
| M4 | 36-48h | Feature freeze; fix mandatory failures and produce exact-candidate release verdict |

If all required journeys are not integrated at hour 24, report which path blocks the full release and update the forecast. The 48-hour target is not credible until the execution profile, build capacity, provider access and desktop packaging prerequisites are available. Do not defer Serenity feasibility or platform signing discovery until the final window.

## Risk register

| Risk | Consequence | Mitigation/owner |
|---|---|---|
| Acceptance scope exceeds available time | Incomplete release | Preserve Z gates; report preview honestly; coordinator |
| Serenity API cannot enforce spend or reconcile writes | Memory cannot satisfy RFC | Early T4.0 feasibility; narrow public adapter or explicit upstream dependency |
| Kazi skills differ from installed behavior | Lost work or premature landing | T0.3/T0.6 disposable canary; one integration owner |
| Shared contracts evolve during dispatch | Independently green modules fail together | Versioned freeze and affected-task pause/rebase |
| Many sessions overwhelm one host | Time lost to resource contention | Host-wide build lease, bounded test jobs, approved remote capacity |
| Agent changes its own tests/contracts | Vacuous completion | Sealed inputs, independent acceptance ownership, red-at-start and negative fixtures |
| Desktop signing/provider credentials unavailable | Distribution or live gates cannot run | T0.1 manifest; report not run; do not silently change supported targets |
| Private context enters public artifacts | Disclosure | Synthetic fixtures; scrub evidence; keep machine profiles/secrets outside repo |

## Operating procedure and definition of done

Use `docs/execution.md` for dispatch and recovery. `/plan` owns intent and acceptance rows; `/apply` owns task claims, dispatch and review; Kazi owns the leased engineering convergence loop; the integration owner controls landing; the acceptance lead owns cross-product gates. No competing full-fleet scheduler.

Before broad execution, inspect current user changes and preserve them, establish clean integration ancestry, qualify the toolchain, and read the local machine's build policy. On the mini, check uptime and hold heavy work above the specified load threshold; use the shared heavy-build lease with its prescribed TTL and identity-checked release. Do not publish machine paths or hostnames into public plans or reports.

A task can be recorded integrated only after its actual behavior tests, applicable API/desktop tests, format/lint, documentation, independent review, CI and integration checks pass. Record task integration separately from release qualification. The complete first release additionally requires installed-artifact journeys on the intended operator-controlled runtime and declared desktop targets; there is no assumed hosted production URL for this single-tenant product. Publication, external messaging, paid execution and deployment obey the user's authorization, not a generic skill's automatic ship wording.

Do not rerun unchanged predicates solely to duplicate evidence. Rerun affected checks after integration changes and execute broader release predicates where task-level predicates cannot establish the system invariant. Record skipped suites and unavailable real integrations as not run.

## Progress log

2026 09 07: Created E0-E7 execution plan, 17 executable frontier tasks and six deferred planning tasks; established directory ownership and harness-neutral dispatch under ADR 001. No implementation dispatched or tested.

## Hand off

Next eligible work: T0.1 and T0.2. First execute the prerequisite discovery and contract freeze; do not immediately launch 24 coding sessions. Read `rfc.md`, ADR 001, this plan, `docs/usecases.md`, and `docs/execution.md`. Existing task criteria are authored intent; no task goal is approved until JIT authoring and qualification. Expand outlines at their declared triggers and add concrete producer dependencies before dispatch.
