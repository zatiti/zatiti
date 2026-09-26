# Mac release execution playbook

Planning artifact accompanying [MAC_RELEASE_PLAN.md](MAC_RELEASE_PLAN.md).
No ticket here changes a frozen contract, grants sibling write access or claims
qualification. Names marked proposed are design requirements to freeze in S0,
not callable APIs. Both arm64 and amd64 are mandatory release targets.

## 1. Dispatch rules

1. Appoint one integration owner. Only that owner assigns base commits, lands
   changes and records ticket state. Use isolated worktrees for implementation.
2. One active writer per ownership root, including its tests, generated files
   and dependency manifests. Disjoint filenames inside a root do not waive this.
   No root/dependency write assignment runs concurrently with any child writer.
3. Run S0 then F0 serially before implementation dispatch. Read-only audits may
   run concurrently. Reopening a shared contract or dependency pin pauses affected
   consumers; a root dependency write pauses all child writers until landed.
4. Each implementation task reads its own directory AGENTS.md and the dispatch
   ticket. Do not copy a sibling implementation or add an undeclared import.
5. Freeze interfaces first, then implement producers and consumers concurrently
   against the same checked fixtures. A fixture pass allows integration; it never
   proves the real producer, OS, provider or release.
6. Land a dependency before landing its consumer. Keep consumer work in its
   worktree while waiting. Rebase and run targeted tests before each landing.
7. Keep at most one ready task per root active. With limited worker capacity,
   prioritize the critical path, then the oldest ready independent task.
8. No task publishes, modifies a real user's workspace, uses a paid provider,
   changes host services or reads production credentials merely to pass a test.
   Use synthetic fixtures by default. Real qualification gets a named disposable
   host, test credential, spend authorization and cleanup plan from its owner.
9. A blocked producer does not block unrelated frozen-contract implementation.
   It does block dependent integration/qualification. Record those separately.
10. Never mark a ticket done because it compiles, has skipped tests, or substitutes
    a demo. A cheaper coding model follows this protocol; evidence and independent
    review establish quality, not the model's completion statement.

## 2. Scheduling graph

`contract-ready` permits implementation against frozen fixtures.
`integrated` permits end-to-end testing against landed dependencies.
`qualified` permits only the capabilities actually evidenced.

| Track | Strict within-root sequence | Can overlap with |
| --- | --- | --- |
| Shared baseline | S0 specification → F0 dependency foundation | Read-only audits only during root writes |
| Platform custody | P1 | Installation, connections, desktop, packaging, CI, test harness work |
| Installation | I1 | P1, N1, A1, K1, D1, C1, T1 |
| Connections | N1 | P1, I1, A1, K1, D1, C1, T1 |
| Provider adapter | A1 | Other roots; live proving waits for explicit qualification resources |
| CLI/helper wiring | H1 | Implement against S0 fixtures alongside P1/I1/N1; integration waits for them |
| Packaging | K1 lifecycle → K2 assembly/upgrade → K3 distribution/signing | All other roots; one packaging writer throughout |
| Desktop | D1 state/API → D2 onboarding → D3 branding/native polish | All other roots; one desktop writer throughout |
| CI | C1 build/evidence pipeline | Other roots, using frozen artifact interface |
| Integration tests | T1 fault harness → T2 integrated journey | Other roots; T2 waits for producers |
| Qualification | Q1 harness → Q2 native matrix | Q1 with implementation; Q2 after candidate assembly |
| Landing/release | R1 review → R2 publication proposal | R1 after Q2, no publication as part of implementation |

After S0/F0, P1, I1, N1, A1, H1, K1, D1, C1, T1 and Q1 are
independent *implementation* lanes. H1/D1/C1/T1/Q1 use fixtures until real
producers land. Do not interpret a broad milestone dependency as a reason to
idle all consumers. Keep critical dependencies explicit in each ticket below.

Primary path: S0 → F0 → P1/I1/N1 → H1 → integrated D2 → K2/K3 → Q2 → R1.
A1 and the required memory runtime's qualification are independent release
blockers that join before Q2's live journey. Begin their feasibility audit early.

Run the Intel and Apple Silicon qualification jobs concurrently after freezing
Q2's runner. They execute identical committed tests and write separate ignored
artifact directories; they do not act as simultaneous source writers in
`tests/qualification`. The qualification owner consolidates their evidence.

## 3. S0: freeze all implementation decisions

Owner: specification/integration maintenance. Write only authored specification
inputs and their renderer-owned outputs under the existing maintenance process.
Do not hand-edit generated AGENTS.md, catalogs or acceptance files.

Before dispatch, publish a contract freeze commit containing all of these:

| Contract | Required exact decisions | Producers / consumers |
| --- | --- | --- |
| Local discovery | Schema version, field types, required/optional fields, exact file location, ownership/modes, maximum bytes, atomic publication, unsupported version behavior and corruption recovery. Metadata only: installation identity, local endpoint and secure credential profile reference. No raw credentials. | packaging/helper → Flutter/CLI |
| Lifecycle helper | Installed executable identity/path, allowed verbs, request/result schema, invocation transport, timeouts, size limits, exit mapping, authorization, cancellation, idempotence and allowed repair actions. No arbitrary command execution. | packaging/cmd → Flutter |
| Owner credential custody | Keychain service/account/access policy; which process provisions and reads it; signed-process access; upgrade continuity; interrupted handoff; revocation. No new plaintext desktop token file. | platform/installation/helper → Flutter |
| Provider secret submission | Exact trusted local channel, challenge/version binding, opaque receipt verification and expiry; no secret in public operation arguments, stdout, argv, environment, diagnostics or model context. | Flutter/helper/platform → connections |
| Setup progress | Map every UI stage and transition to existing operation IDs and payload schemas; choose authoritative readiness fields and repair dispositions. Add an operation only through coordinated schema maintenance. | installation/connections/configuration/accounting → Flutter |
| Setup identity/retry | Bootstrap lock and post-crash discovery, stable submission-key custody for each subsequent mutation, command reconciliation and deterministic object lookup. | all setup producers → Flutter |
| Release descriptor | Version, OS/arch mapping, component versions/digests, compatibility, signed metadata, trusted key rotation, artifact filenames and hosting layout, install channel and upgrade selection. | packaging → CI/bootstrap/cask/tests |
| Service/layout | Decide SMAppService versus existing LaunchAgent approach; helper placement; user identity; executable/socket/data paths; launch/stop/repair ownership; consent, login, lock and sleep semantics. | packaging/platform/cmd → Flutter/tests |
| Support matrix | Exact minimum/current OS rows per architecture, Flutter/Dart/Xcode/plugin/runtime versions, signing identities and qualification host availability. | foundation → every lane |
| Capability policy | Required provider and memory behaviors; which runtime blockers must be fixed; whether a reduced-capability preview is explicitly approved. Never silently remove existing requirements. | integration/spec owner → all lanes |

Each contract needs accepted and rejected JSON/byte fixtures, a size-limit case,
unknown-version case, error mapping, and producer/consumer test references.
Use existing wire envelopes where applicable; do not wrap secrets in them.
Assign the fixture source and generation/check command to one existing owner.
Copies must be generated or digest-checked, never independently hand-maintained.

Known source observations to resolve, not blindly preserve:

- `cmd/zatiti/profile.go` currently describes/writes a raw authorization value
  in a protected profile file. That is not the proposed desktop Keychain handoff.
- `cmd/zatiti/helper.go` describes a terminal-oriented credential flow and a
  receipt-reference gap. Reproduce whether the gap still exists; trace actual
  call sites rather than treating comments as definitive current behavior.
- `apps/desktop/lib/src/app/startup.dart` expects environment configuration;
  `main.dart` constructs the live client from that profile.
- Current desktop packaging prohibits embedded controller/helper payloads. Default
  to an outer installer coordinating separate manifests. Do not switch layouts
  or introduce native service registration without freezing the consequences.

S0 exit: no unresolved interface field, secret handoff, lifecycle choice or
supported-platform row needed by a dispatched ticket. Run spec render and check.
Unsettled independent release resources may remain tracked external blockers.

## 4. F0: dependency baseline

Owner: integration foundation; serialized root exception. Audit the actual lock
report and executable evidence, not just README status. Resolve exact toolchain,
plugin/native/runtime versions and licensing for both CPUs. Use current source
and upstream evidence to verify Intel support before choosing the Flutter pin.
Do not automatically upgrade dependencies to latest.

Exit: committed lock/checksums/licenses and two-architecture build feasibility;
explicit pass/blocked table for required live provider and Serenity guarantees.
Missing guarantees stay blocked. If resolving a runtime requires upstream work,
record owner and required evidence; freeze fixtures so independent work can
continue, but do not claim F0's dependency qualification is complete.

## 5. Implementation tickets

All write paths below are repository-relative owner roots. Existing files are
starting points, not permission to rewrite the package. New private files stay
inside the same root. Every ticket inherits sections 7–9.

### P1 — OS custody and protected metadata primitives

- Root: `internal/platform/`; inspect `secret.go`, `files.go`, `socket.go`,
  `fs_darwin.go` and their tests. Implement S0 custody/metadata primitives only.
- Reuse existing secret-store contracts. Native helper implementation location
  must already be assigned by S0; do not place it in an arbitrary new root.
- Test locked/unavailable store, wrong owner, path traversal, symlink substitution,
  truncated metadata, oversized input, interrupted atomic write and repeat custody.
- Exit: fixture conformance and package tests. Real signed Keychain access is Q2,
  not a claim supported by a fake helper.

### I1 — Resumable installation readiness

- Root: `internal/installation/`; inspect `bootstrap.go`, `owner_credential.go`,
  `lifecycle.go`, `doctor_test.go`, `bootstrap_test.go`.
- Preserve existing bootstrap creation of owner/organization/chief. Do not add a
  second chief-creation route in Flutter. Expose only S0-authorized readiness.
- Implement crash recovery around secret custody and transactional finish; after
  ambiguous completion inspect authoritative state before retrying bootstrap.
- Test pre-custody, post-custody/pre-commit and post-commit response-loss failures;
  concurrent first launch; initialized installation; no paid work before readiness.
- Exit: one durable installation/owner/chief and metadata-only bootstrap result.

### N1 — Provider setup and receipt correctness

- Root: `internal/connections/`; inspect `localio.go` and setup tests.
- Reproduce and fix any receipt key/reference mismatch under the frozen contract.
  Bind receipts to installation, challenge, current version and authority.
- Keep raw keys outside operation schemas. Distinguish key capture from provider
  validation; validation must be separately bounded/authorized when external.
- Test valid, replayed, forged, expired, cancelled and cross-installation receipts;
  validation failure/revocation; secret cleanup and ambiguous completion recovery.
- Exit: real store-reference semantics demonstrated in local integration fixtures,
  not a mock that accepts any receipt. Provider availability remains A1/Q2.

### A1 — First-chat provider profile

- Root: `internal/adapters/responses/`; inspect existing protocol/adapter tests.
- Implement only changes needed for the frozen provider profile and required
  capability guarantees. Unknown outcomes never cause blind paid retries.
- Test authentication failure, rate limit, timeout before/after transmission,
  response loss, unsupported model, usage parsing and cost uncertainty.
- Exit: fixture suite plus an explicit live qualification handoff. Do not write
  root dependency reports from this assignment or claim live proving occurred.
- Required Serenity changes are a separate `internal/adapters/serenity/` ticket
  issued only after S0's capability decision. Its exact fixes must follow the
  documented gap table and upstream feasibility; do not instruct a model to
  fabricate missing upstream behavior. Qualification remains a release gate.

### H1 — Trusted setup helper and controller wiring

- Root: `cmd/zatiti/`; inspect `bootstrap.go`, `profile.go`, `helper.go`,
  `readiness.go`, `assembly.go`, `run.go`.
- Add only the S0-defined desktop helper entrypoints. No shell interpolation,
  secrets in argv/stdout, arbitrary executable override or direct Flutter DB access.
- Wire real P1/I1/N1 implementations; keep CLI compatibility explicit. Do not reuse
  the file-token profile as desktop secure custody merely because it exists.
- Test helper status/error mapping, bounded requests, caller rejection, timeout,
  cancellation after commit, repeated invocation and secret-redacted diagnostics.
- Implement against frozen fakes concurrently; integration exit requires landed
  P1/I1/N1 and a complete custody → bootstrap → discovery → authenticated call.

### K1 — Install layout and lifecycle

- Root: `packaging/`; inspect `layout.go`, `plan.go`, `apply.go`,
  `servicemanager.go`, `desktop_install.go`, `templates/launchd/`.
- Implement S0 layout and lifecycle choice. Derive paths from validated user
  context; never run the per-user controller as root, including from installer hooks.
- Serialize installs with an install lock; stage and verify before activation;
  reject wrong architecture/version before modifying existing installation.
- Test repeat install, concurrent installers, malicious archive/path/symlink,
  partial download, occupied destination, disabled service and launch failure.
- Exit: bounded install/inspect/repair results match helper fixtures. Host service
  calls remain recorded test doubles until dedicated native qualification.

### K2 — Matched release assembly and recoverable upgrade

- Root: `packaging/`, after K1 lands. Inspect `manifest.go`, `desktop_bundle.go`,
  `signature.go`, `audit.go`, existing CLI tests.
- Build separate arm64/amd64 artifacts with one release descriptor and independently
  verified component manifests. Include notices, required runtime and compatibility.
- Implement staged update/health check, safe handling of active or unknown work,
  data-compatible rollback and explicit backup/restore recovery otherwise.
- Test interrupted stage/activation/migration/restart, mismatched components,
  tampering, downgrade refusal, reinstall preserving state, and data-retaining
  uninstall without orphan services. Never restore revoked authority silently.
- Exit: fixture installs/upgrades pass; real candidate components integrate when
  H1/D2 and runtime artifacts are ready. No fabricated artifact attestation.

### K3 — User distribution and signing

- Root: `packaging/`, after K2. Produce direct installer, bootstrap script and
  project cask descriptors from the same frozen release metadata.
- Bootstrap detects host CPU correctly, including an Apple Silicon machine running
  a translated shell; verify implementation on actual hardware. Download only
  declared HTTPS artifacts; verify signatures/hashes before execution; clean up
  partial state; support rerun. No developer tools, Gatekeeper disabling or curl
  flags that suppress TLS checks. Missing permissions yield a clear repair path.
- Keep signing identity out of source; sign nested code in the required order,
  notarize/staple through CI and verify the downloadable result, not only the
  pre-signing build. Signed-byte digests must describe the final artifacts.
- Exit: candidate installers and scripts staged privately; no public URL/cask
  submission or release publication in this ticket.

### D1 — Desktop discovery, helper client and setup state

- Root: `apps/desktop/`; inspect `lib/main.dart`, `lib/src/app/startup.dart`,
  `credential_store.dart`, transport retry logic and `workspace_controller.dart`.
- Isolate proposed private collaborators in `lib/src/app/`: discovery reader,
  bounded helper client and setup controller. Final names are local choices;
  external payloads must exactly match S0. Reuse existing operation bindings.
- Implement section 6's state machine with dependency injection for tests.
  Production selects real collaborators; fake/demo collaborators remain explicit.
- Discover protected local profile before considering development environment
  overrides according to S0 precedence. Corruption is a recoverable error, not
  permission to initialize another installation.
- Persist only allowed metadata and stable pending submission identities; secrets
  and unsent drafts use approved secure storage. Never generate a replacement
  mutation key merely because the UI relaunched.
- Exit: state/transport tests for every transition and crash boundary; no widgets
  implement bootstrap policy or decide controller readiness locally.

### D2 — Onboarding and first conversation

- Root: `apps/desktop/`, after D1. Add private UI in `lib/src/ui/` and wire through
  `app.dart`/`main.dart`. Preserve existing chat/navigation and acknowledgment UI.
- Screens: Welcome → Preparing → Provider key → Validation → Spending limits →
  Ready conversation. Show progress text, a primary next/retry action and only
  relevant recovery links; never expose operation IDs, filesystem paths or schema.
- Mask key entry, clear it after custody, allow cancel, explain paid validation
  before confirmation. Disable duplicate submit; errors preserve nonsecret input.
- Read committed limits/chief/conversation before opening the composer. A timeout
  shows reconciliation, not success. The first reply comes through normal chat.
- Test keyboard order, repeated clicks, back/cancel, process relaunch, invalid key,
  offline state, spending refusal, locked Keychain and ambiguous acknowledgments.
- Exit: all fake-backed UI tests plus integrated first chat against H1/I1/N1/A1
  in T2. Do not substitute a prewritten greeting for provider evidence.

### D3 — Brand and native UX

- Root: `apps/desktop/`, after D2. Use approved `assets/brand/` mark; create a
  consistent master/export set without redesigning it. Update macOS app assets,
  display name and installer-facing assets in this root; hand paths to packaging.
- Verify Finder/Dock/app-switcher icon from installed app, not source previews.
- Inspect real dark/light screens at minimum window size, keyboard-only operation,
  VoiceOver and large text. Fix clipping and inaccessible recovery controls.
- Exit: native captures and recorded findings; widget captures using substitute
  test fonts do not close the prior typography QA gap.

### C1 — Native build and evidence pipeline

- Root: `.github/workflows/`; inspect `ci.yml` and AGENTS.md.
- Separate native Intel and Apple Silicon jobs; use S0's verified runner labels
  and pinned toolchains. Do not assume a hosted label's CPU or use emulation as
  native qualification. Fail early on host/build artifact architecture mismatch.
- Build Go controller/runtime and Flutter release app from one revision; invoke
  packaging-owned commands rather than copying install/signing logic into YAML.
- Separate untrusted PR tests from protected signing credentials; release jobs
  accept only reviewed revisions. Never use pull-request code with release secrets.
- Retain per-architecture hashes, versions, notices, test logs and notary outputs;
  join only when both jobs pass. Publication is a separate gated workflow.
- Exit: validated workflow and successful unsigned artifact rehearsal on both
  native runners; protected signed execution waits for K3 and release resources.

### T1/T2 — Fault harness then integrated journey

- Root: `tests/integration/`. T1 may begin after S0/F0; T2 follows landed producers.
- Build a process-level harness with controlled failures at custody/commit/reply
  boundaries. Exercise real package assembly where available; catalog conformance
  fakes must preserve envelope and retry semantics.
- T2: bootstrap → helper custody → provider setup → limits → chief/conversation
  → send → persisted reply → restart. Cover double launch and response loss.
- Assert durable object counts, stable submission IDs, acknowledged state and
  secret absence in captured diagnostics. Count physical provider calls where
  the harness controls them; report what live providers cannot prove separately.
- Exit: exact command, fixture seed and observed results. Missing production wiring
  is a failing/blocked integration case, never a skipped success.

### Q1/Q2 — Native release qualification

- Root: `tests/qualification/`. Q1 defines executable runners and evidence schema
  early; Q2 executes the unchanged runner on frozen signed candidates.
- Run every release-plan matrix row for both CPUs and each declared OS row.
  Test actual downloadable installer bytes, quarantined first launch, native
  Keychain access, service behavior, real provider and known-good prior-version
  upgrade. Never label an arbitrary synthetic version a supported prior release.
- For the first public release, construct and record a genuine earlier candidate
  with real state to exercise upgrade; call it a candidate-to-candidate proof.
- Required evidence: commit, target, OS, hardware, artifact hashes, versions,
  expected/observed result, test command, redacted logs/captures, live provider
  profile, cost authorization and cleanup result. All emitted paths are portable.
- Exit: two native reports with no skipped required cases. Unavailable signing,
  provider, runtime or hardware is BLOCKED, not passed. R1 checks evidence scope.

### R1/R2 — Independent review and release proposal

- Integration owner reviews diff/test evidence independently of the implementer.
  Reject unsupported guarantees, new plaintext custody, raw secret exposure,
  invented API calls, fake readiness and silently reduced release scope.
- Reproduce clean-host install-to-chat from published-form instructions with the
  candidate staging URL. Verify both artifact identities and prior evidence links.
- Prepare versioned notes, supported platforms/capabilities, install instructions,
  known limitations, recovery/uninstall guidance and exact publication actions.
- Exit R1: reviewable candidate and complete evidence index. R2 is a separately
  authorized publication step; do not ask for approval before preparing the result.

## 6. Prescriptive setup state machine

These are proposed private UI states; S0 freezes their operation mapping. No
new wire enum is implied. Recovery always re-reads authoritative status.

| State | Evidence/condition | Allowed next action | Must not do |
| --- | --- | --- | --- |
| inspecting | App opened; local metadata/custody/service unknown | Bounded inspect | Mark ready from file existence alone |
| welcome | No installation confirmed, OS prerequisites pass | User starts setup | Start paid validation |
| preparing | Service/bootstrap in progress | Await/reconcile; surface repair | Restart bootstrap after ambiguous commit |
| needsProvider | Installation/owner confirmed; provider missing | Capture key via trusted helper | Send key in public operation payload |
| validating | Authorized probe accepted | Read job/challenge disposition | Treat accepted job as validated |
| needsLimits | Qualified provider available; limits unconfirmed | Stage/review/apply via existing flow | Invent rates or silently enable spending |
| ready | Committed prerequisites, chief and conversation verified | Open composer | Fabricate response or optimistic completion |
| reconciling | Mutation acknowledgment unknown | Query original command, retain key | Resubmit with new key or assume cancellation |
| repairable | Named prerequisite/permission/network failure | Specific retry/settings/cancel | Erase installation or fallback to plaintext |
| unsupported | Version/capability/platform mismatch | Explain supported upgrade/repair | Guess operation or downgrade schema |

On process restart: inspecting first, then reconstruct progress from controller
state and approved local metadata. Never advance from a previously displayed
screen alone. A user cancelling local progress does not cancel accepted work.

## 7. Required ticket payload for a coding model

The dispatcher fills every field; do not send a model only this long document.
Attach the applicable directory AGENTS.md and the small, exact frozen contracts.

```text
Ticket: <ID and one-sentence observable outcome>
Base: <landed commit SHA>
Write root: <one existing ownership root>
Read context: <AGENTS.md, 2–6 relevant source files, contract/fixture references>
Contract revision/digests: <exact values; no TBD fields>
Prerequisites: <contract-ready vs integrated vs qualified dependency states>
Files to add/change: <explicit list; justified additions remain within root>
Behavior: <numbered steps, input/output/error mapping and state transitions>
Forbidden shortcuts: <ticket-specific examples plus section 1 rules>
Tests: <named cases, command, expected observations, fault injection points>
Acceptance: <observable assertions; no prose-only 'works correctly'>
External resources: <none, or owner-provisioned disposable host/test credentials>
Deliverables: <diff, actual commands/results, remaining blockers, consumer notes>
Stop conditions: <missing contract/pin/authority; do not guess or edit siblings>
```

If a defect belongs to another owner, return a small reproducible fixture,
expected/observed behavior, affected callers and proposed contract revision.
Continue independent in-root work; do not bypass the defect with a fake success.
Do not ask the user about routine implementation choices already fixed here.

## 8. Verification and handoff protocol

Before implementation, record `git status --short` and run the relevant baseline
check. Preserve unrelated changes. Do not run a repository-wide formatter.
For Go roots, run their package tests; for Flutter, run `flutter analyze` and
relevant unit/widget suites with the frozen SDK; for specification, run render
then render `--check`. Release/native tests are separately owned Q2 commands.
Use existing harness commands where present and report the exact invocation.

For every ticket:

- Check changed-path ownership and `git diff --check`.
- Test the happy path, each named negative case and one interrupted boundary.
  Fault tests must fail against the broken behavior, not mirror implementation.
- Distinguish preexisting baseline failures from new regressions with evidence.
  Do not delete assertions, skip required suites or change expected errors solely
  to make the result green. Fix fixtures only when the frozen contract changed.
- Report counts, failures/skips and actual environment. No executed test means
  no test-pass claim. Tests involving paid services require provisioned authority.
- Consumer review checks schemas, errors, retry identities and version handling.
  Security/custody and migration changes require integration-owner review before
  landing, even when package tests pass.
- On the rebased integration branch, run impacted producer/consumer checks once.
  Run the full milestone suite at the assembled candidate, not after every tiny edit.

Handoff record (stored in the assigned root or existing tracking system):

```text
Ticket / base / head:
Status: implemented | integrated | qualified | blocked
Changed paths:
Behavior delivered:
Frozen contract and fixture digests:
Commands executed / results / skipped cases:
Fault cases exercised:
Evidence artifacts (repository-relative or approved artifact URLs):
Dependencies still stubbed or unavailable:
Contract requests / affected callers:
Consumer integration instructions:
```

The integration owner records queued → ready → implementing → review → landed,
with separate integration and qualification states. A producer landing unblocks
consumer testing, not automatic consumer approval.

## 9. Completion checklist

- Both native CPUs and all supported OS rows qualified; no architecture deferred.
- First real provider-backed chat from a clean installation without manual config.
- No duplicate durable setup objects or uncontrolled retry after interruption.
- Native Keychain custody demonstrated across bootstrap, launch and upgrade.
- Required runtime/provider capabilities actually qualified or explicitly revised
  through the coordinated specification process before dispatch/shipping.
- Signed downloadable artifacts, upgrade recovery, user-safe uninstall, notices,
  supported-scope documentation and complete evidence index reviewed.
- No public installation command until it points to the approved real release.

This playbook maximizes independent implementation work after a small frozen
baseline. It deliberately keeps root writes, same-package changes, integration
landing and release authorization serialized.
