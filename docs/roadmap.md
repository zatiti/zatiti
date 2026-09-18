# Roadmap

RFC implementation status (docs/rfc.md). Integration lead: the `zatiti`
session; packages land only through its gate (build, vet, lint, stub scan,
tests, race under lease, mutation red→green, rebase, ff-merge).

## Parallel dispatch protocol

- Lanes run in isolated worktrees with disjoint write roots. The lane cap
  is a budget decision, not a fixed two: ten concurrent lanes on this
  4-core laptop exhausted the account session limit in ~25 minutes and
  drove load past 130 (2026-09-18). Eight is the practical ceiling;
  critical-path lanes first.
- One heavy run machine-wide at a time. Every module-wide command, every
  `go test -race` (package-level included), every `flutter test` /
  `flutter build`, and every `git commit` (the hook runs module-wide
  tests) goes through the lease wrapper as ONE foreground command:
  `with-lease.sh "<lane>: <what>" <command>`. It claims `R-build-lease`,
  runs, and always releases. Never call claim.sh directly and never claim
  from a monitor or background task: a lane's monitor once held the lease
  idle for 21 minutes while everything queued behind it. `-p 2` on every
  go invocation.
- The "hold when 1-minute load > 10" rule in the founder's global
  instructions is written for the Mac mini. On the laptop the lease is the
  throttle (founder-confirmed for the wave-3 push, 2026-09-18). Agents
  will not accept a relayed waiver, correctly; point them at the rule's
  own scope and `hostname`.
- Lanes commit early slices. A stalled or limit-killed lane is resumed by
  message into the SAME worktree with its on-disk inventory; never
  restarted from scratch unless the files contradict the brief.
- The lead lands every lane: independent build/vet/gofmt/lint/race, its
  own mutation red->green, rebase, ff-merge, module-wide verify.

## Shipped

- 2026-09-10 — wave 1 complete: contract, storage, platform, client,
  identity, registry, application, configuration (all merged to main;
  module-wide `go test ./...` green at configuration landing).
- 2026-09-10 — registry re-landed in wave 2 (f5657e8), application
  (ab71c8b), configuration (1488db3), skills (169e5be), tasks (8cda814),
  reviews (b1516e2), policy (4051f67). Integration notes:
  docs/implementation/integration-notes.md.
- 2026-09-11 — scheduling landed (3e6f665) and connections landed
  (90f676a): both races green under lease, integration mutations proved
  the scope fences load-bearing (scheduling scopeContains org dimension;
  connections scopeCovers org dimension), module-wide 14-package verify
  green post-merge, worktrees removed.
- 2026-09-11 — messaging landed (f09bbe9): integration mutation proved
  the conversation.create caller-participation fence load-bearing; lane
  agent's mutation covered the mailbox.ack principal fence; 46 behavioral
  tests; two suite-forced production fixes (event scope stamping from the
  unit, fresh-delivery recipient hydration). Worktree removed.
- 2026-09-11 — accounting landed (bc7f58d): wave 2 is 16 of 37 packages.
  Integration mutation proved the settle terminal-replay fence
  load-bearing (a settled reservation with a different usage report would
  otherwise replay as if identical — history rewrite). Module-wide
  16-package verify green post-merge.
- 2026-09-11 — effects landed (dd200cd), 17 of 37. Both wave-2 tail lanes
  had stalled silently (agents idle 11h, zero processes, free lease);
  the lead took over. Gate finding: operation.get's org-dimension fence
  (handlers.go:1204) had no test coverage — the missing
  TestOperationGetOrgDimension was added and then proved the fence
  load-bearing by mutation (neutralized fence lets a foreign-org request
  read another org's operation: expected not_found, got completed).
  Module-wide 17-package verify green post-merge.
- 2026-09-11 — execution landed (c80504d), 18 of 37. The stalled lane's
  tree did not even vet (undefined f in a collision-reconciled test) and
  two suite failures surfaced two real production fixes on landing:
  fenceAttempt now returns a stale-running task to waiting with the fence
  reason (a fenced lease previously deadlocked generation restart — the
  task state could never clear), and attempt generations count prior
  attempts (maxAttemptGeneration) instead of hardcoding 1. Integration
  mutation proved the checkWorkerCall state fence load-bearing (a
  cancelled attempt's heartbeat would otherwise extend its lease and
  revive dead work: expected conflict, got completed); the worker-identity
  dimension is defense-in-depth (narrowScope + checkWorkerCall both guard
  it, single-fence mutation is green by design). Independence fence test
  corrected to the actual layered refusal (bind rejects a
  non-independent result at /result/independent with invalid_input before
  the handler's verification_failed fence can fire). Race 221s green;
  module-wide 18-package verify green post-merge.
- 2026-09-12 — artifacts landed (2a719b8), 19 of 37. Immutable artifact
  metadata, resumable chunked uploads and integrity-checked reads;
  chunks are staged/published individually then re-verified as one
  concatenated blob at finish (BlobStore only serves published content).
  Integration mutation proved the cross-organization scope fence in
  narrowScope load-bearing (neutralized: a foreign-org artifact.get
  completed instead of returning permission_denied). Retention pins have
  no public pin/unpin operation in the frozen catalog, so they are
  populated as internal-only bookkeeping; artifact.export can register a
  durable job but cannot drive it to completion (execution's allowlist
  admits artifacts only to _execution.job.create, not job.claim/record) —
  flagged for whoever owns driving artifacts-initiated export jobs to
  completion. Module-wide 19-package verify green post-merge
  (build/vet/lint/test); worktree removed.
- 2026-09-12 — memory landed (1dcf9b5, plus gofmt fixup), 20 of 37. Scoped
  memory bindings, governed Serenity jobs and promotion/retraction
  lineage; all 17 operations in AGENTS.md implemented (the earlier
  dispatch that stalled had only boilerplate with zero handler bodies and
  a compile error). Gate finding: 5 files were not gofmt-clean
  (misaligned struct fields; golangci-lint's configured linters did not
  catch it) — fixed as a follow-up commit, no behavior change. Integration
  mutation proved the scopeContains binding-authorization fence in
  authorizeBinding load-bearing (neutralized: a binding scoped to a
  different worker returned brain data instead of permission_denied,
  the exact case TestSelectUnauthorizedBrainNeverQueried names). No brain
  ever gets a connection_ref/tool_ref through memory's own operations
  today (bootstrap's schema has no such fields), so recall/remember/
  promote always resolve to a durably-queued pending job in production
  until an integration-level Serenity connection is provisioned — the
  fully-wired dispatch path is implemented and tested by seeding a bound
  brain directly. Retract has no wire Limits field, so it uses a nominal
  zero-cost bound and 1hr validity window rather than inventing a
  default currency. Module-wide 20-package verify green post-merge;
  worktree removed.
- 2026-09-12 — evidence landed (1db2b99), 21 of 37. Durable command
  replay (begin/finish under one writer transaction, so an unfinished
  placeholder row is never visible outside it), authorized event reads,
  and a snapshot cursor. Contract note: the mission line names
  "receipts/retention_pins" but no operation in the frozen schema reads
  or writes either — no tables/endpoints invented for them; retention is
  trivially satisfied this wave since nothing deletes a command row.
  Integration mutation proved the organization-dimension check in
  scopeVisible load-bearing (neutralized: a foreign-org event.get
  succeeded instead of returning not_found). Sibling-package finding
  (not fixed here, out of this lane's write scope): internal/effects's
  checkSchemaDocument passes a map[string]json.RawMessage into
  resolveRefs, whose type switch only matches map[string]any/[]any, so
  $ref resolution silently no-ops there (fails open; effects' own
  schemas happen to be valid so no test catches it) -- verified by
  reading effects/service.go:389-432; needs its own fix and landing-gate
  pass. Module-wide 21-package verify green post-merge (build/vet/lint/
  test); worktree removed.
- 2026-09-12 — installation landed (a0fdd57), 22 of 37. Bootstrap
  (composes identity/configuration/memory/messaging bootstrap in one
  transaction; a doomed attempt never emits an event so it can't be
  mistaken for the real installation on restart), pause/maintenance/
  resume lifecycle, status/doctor, job bookkeeping, and encrypted
  backup/restore. Contract gap (verified, not fixed here):
  contract.Dependencies (internal/contract/module.go:57-63) has no seam
  to produce a consistent digest of the installation's own SQLite
  database, so installation.backup/restore do all reachable real work
  (job creation, key custody, manifest gathering, artifact verification,
  cross-installation binding checks) and then fail honestly with a named
  prerequisite_missing at the exact missing-digest point, rather than
  fabricating a digest -- matches the spec's own "no fake success"
  requirement, tested explicitly rather than left as a silent stub.
  Needs a coordinated fix (a narrow Database-digest or bounded-backup
  seam added to contract.Dependencies) once wave 3's controller/
  entrypoint assembly exists to wire it. Integration mutation proved the
  cross-installation manifest-binding check in performRestore
  load-bearing (neutralized: a backup manifest for a different
  installation fell through to a later, unrelated failure instead of
  being refused immediately). Module-wide 22-package verify green
  post-merge; worktree removed.
- 2026-09-14 — cli landed (5c41190), 23 of 37. Generates all product
  commands from operation descriptors (Cobra -- confirmed against the
  frozen shared contract's own "Library families" list, already pinned
  in go.mod/go.sum since wave 0/1, not a new dependency this lane
  added), structured --input handling, one JSON envelope to stdout per
  call, and contract.CLIExit process-exit mapping. Preceded by a second
  stuck-build-lease incident, worse than the first: the lane finished
  writing on 2026-09-12 around 18:13 PDT, claimed R-build-lease for its
  final module-wide check, and then held it silently for ~41 hours (10x
  the 4h norm) with no commit and no reply, discovered only because
  David asked "did you stop?" -- see the 2026-09-14 conversation record.
  The integration lead independently verified the package's code was
  already correct (build/vet/gofmt/race all clean) while waiting, then
  the lane woke up on its own after being paged and committed for real;
  nothing had to be pruned or redone. Root cause per the lane's own
  report: its background module-wide verification had actually finished
  a while before the 41h mark, but its session stalled and never
  resurfaced that completion to it -- the same likely mechanism behind
  memory's earlier 21h stall, not two unrelated incidents. Integration mutation proved
  contract.CLIExit's non-zero-exit-code path in finish load-bearing
  (neutralized: every fault class -- invalid_input, permission_denied,
  conflict, outcome_unknown, etc. -- silently exited 0 instead of its
  documented code). Module-wide 23-package verify green post-merge;
  worktree removed.
- 2026-09-14 — server landed (8b16756), 24 of 37. Single POST
  /v1/operations/{operation_id} route; local Unix socket (0600 perms)
  authenticates via Application.Authenticate, remote mutual-TLS listener
  authenticates via the verified peer certificate's SPKI SHA-256
  fingerprint and cross-checks any accompanying bearer credential
  resolves to the same principal. Integration mutation proved that
  cert/bearer cross-check load-bearing (neutralized: a request with a
  valid client cert but a bearer credential for a DIFFERENT principal
  succeeded (200) instead of being denied (403) -- the exact case
  TestRemoteMismatchedBearerPrincipalIsDenied names). Landed cleanly,
  no stuck lease this time. Module-wide 24-package verify green
  post-merge; worktree removed.
- 2026-09-14 — mcp landed (35682e5), 25 of 37. Stdio MCP adapter over
  the common authenticated controller client; one MCP tool registered
  per public operation descriptor, strict envelope decode so a
  malformed call is a JSON-RPC protocol error rather than reaching the
  operator. Real upstream SDK gap found and worked around (not fixed
  here, out of this lane's write scope, but verified genuine by the
  integration lead): the pinned github.com/modelcontextprotocol/go-sdk
  v1.7.0 negotiates its own current protocol revision (2026-07-28) by
  default rather than this adapter's required pin (2025-11-25), and the
  SDK's own version-narrowing mechanism (ProtocolVersionSupporter) is
  broken by a side effect in the SDK's discover handler (answering
  "server/discover" at all marks the session initialized, so a client's
  legacy-initialize fallback then gets refused as a duplicate). Worked
  around by rejecting "server/discover" outright via receiving
  middleware before the SDK's own handler runs, verified end-to-end
  against a real, unmodified go-sdk client
  (TestServeBaselineProtocolAndCapabilities). Integration mutation
  proved the strict-decode boundary in toolBinding.call load-bearing
  (neutralized: all 3 named malformed-argument cases reached the
  operator instead of being rejected as invalid_params). Module-wide
  25-package verify green post-merge; worktree removed.
- 2026-09-14 — adapters/github landed (d19b151), 26 of 37: the last
  wave-2 package to land in a pair. Qualified repository read/write REST
  adapter (net/http only, no third-party GitHub SDK) covering all 5 v1
  action kinds plus 6 read_repository resource kinds; capability
  evidence binds the exact profile bytes via its digest; automation
  constraints bound what an action may claim relative to its profile;
  every physical call is exactly one net/http round trip, no hidden
  retries. Genuine contract gap found and verified by the integration
  lead (not fixed here, out of this lane's write scope):
  PhysicalCallEvidence.request_context (docs/implementation/
  adapter-schemas.json:448-450) is typed as a bare ArtifactRef, which
  requires a durable id (adapter-schemas.json:92-107) -- but
  contract.AdapterDependencies (internal/contract/adapter.go:47-52) has
  no IDSource and BlobStore.Stage/Publish mint no id either, so no
  adapter can honestly produce one for request bytes it builds
  synchronously. Confirmed this affects every adapter, not just github.
  Current behavior reuses the profile's own capability_evidence
  ArtifactRef as the least-misleading available value, documented
  in-code as NOT a claim that artifact contains the request bytes.
  Suggested fix, also verified against the existing schema: retype
  request_context as ArtifactLocator (adapter-schemas.json:366-410),
  which already has a "staged" variant (staging_ref+digest, no id) for
  exactly this case. Integration mutation proved classifyNetworkError's
  dial-failure branch in network.go load-bearing (neutralized: a real
  dial failure -- nothing ever reached GitHub -- was classified
  "unknown" instead of "not_sent", which would make a caller treat a
  definitely-unsent mutation as unsafe to retry). Module-wide 26-package
  verify green post-merge; worktree removed.
- 2026-09-14 — adapters/httpread landed (d86b400), 27 of 37: WAVE 2 IS
  COMPLETE (all 21 domain/infrastructure/adapter packages dispatched
  since 2026-09-11 have landed). Bounded, read-only public HTTP GET
  adapter. The real security-relevant mechanism here is DNS-rebinding
  defense: resolveValidatedIPs resolves the host once and validates
  every candidate against loopback/private/link-local/metadata/
  unspecified/multicast, then withPinnedDial/pinnedDialContext dial the
  exact validated IP directly with no second hostname resolution at
  connect time -- closing the classic validate-then-reresolve TOCTOU
  window. New() deliberately builds its own *http.Transport rather than
  reusing deps.HTTP.Transport, since a shared proxy/dial override could
  route around the pinning; verified this directly by reading the code,
  not just the report. Redirects are never followed (max_redirects
  fixed at 0 in the profile schema); oversized bodies are refused with
  no digest recorded (never stages misleading partial content).
  Integration mutation proved validateDialIP's loopback check
  load-bearing (neutralized: the production adapter, via a real
  resolver, stopped refusing a loopback origin upfront and instead
  attempted a genuine network dial -- surfacing as outcome_unknown
  instead of permission_denied -- exactly the SSRF-into-loopback
  exposure this check exists to prevent). Same already-known
  request_context/ArtifactRef gap as github, handled the same way
  (reuses the profile's own capability_evidence artifact, documented
  in-code); no new contract questions. Module-wide 27-package verify
  green post-merge; worktree removed.

## In progress
- 2026-09-11 to 2026-09-14 -- wave 2 is fully landed: memory, artifacts,
  evidence, installation, cli, server, mcp, adapters/github and
  adapters/httpread, all 9 wave-2-tail packages plus everything landed
  earlier in the wave (see Shipped for the complete list and every
  landing's mutation-check evidence). Five of these nine tail lanes
  stalled or hit an unflagged issue mid-lane at some point (quiet before
  committing, a stuck build lock post-commit -- 21h for memory, ~41h for
  cli -- gofmt not checked before commit) and were recovered or
  corrected by the integration lead rather than taken on trust; none
  needed a full restart. This is a recurring pattern, not an occasional
  one -- see the "Parallel dispatch protocol" section above, which still
  has no automatic paging for a lock held past its TTL; both stuck-lease
  incidents were caught by a human asking, not by anything in the
  pipeline itself. mcp, server, adapters/github and adapters/httpread
  all landed clean with no stall -- the back half of the wave went
  smoothly once the two incidents were behind it.
- 2026-09-18 -- David: implement the rest of the plan at maximum
  parallelization; the two-heavy-lane cap is lifted for this push (the
  R-build-lease still serializes module-wide runs). Seven lanes
  dispatched on wave3/* branches, disjoint write roots: controller
  (critical path), tests/integration first increment (landed packages
  only), packaging, .github/workflows, responses adapter (nothing
  guessed: endpoint/model/prices come from the profile; real-endpoint
  qualification stays blocked), serenity adapter (research phase pins
  the inspected Serenity commit first; may legitimately stop at
  PROTOCOL.md), and the internal/effects $ref-resolution fix. Shared
  lane rules: foreground-only module-wide verification, lease held for
  minutes.
- 2026-09-18 -- David decided Flutter placement and transport, recorded
  as docs/adr/002-flutter-desktop-client.md (86d3519): the client lives
  at apps/desktop/ in this repo and talks directly to internal/server's
  Unix socket / mutual-TLS listener; internal/desktop and
  cmd/zatiti-desktop are retired as Go roots. Two more lanes dispatched:
  spec maintenance (wave3/spec: Flutter root, request_context retyped to
  ArtifactLocator, installation database-backup seam; spec inputs and
  rendered files only, three separate commits) and the Flutter client's
  first increment (wave3/flutter-desktop: transport, typed view state,
  UI per the adopted design). Nine lanes in flight. Still waiting:
  cmd/zatiti (needs controller + both adapters), tests/qualification
  (needs the regenerated brief, the Flutter client and both adapters),
  and the Go follow-ups the spec change will create (adapters'
  request_context, contract.Dependencies seam + installation wiring).
- 2026-09-15 -- David: design the desktop app first, before any
  implementation lane, and plan to use Flutter instead of Fyne v2. This
  reverses the frozen shared contract's stated desktop library choice
  ("Fyne v2 native Go desktop", docs/implementation/contracts.md and
  every generated AGENTS.md) and means internal/desktop and
  cmd/zatiti-desktop as currently specified (Go packages importing
  internal/contract + internal/client) do not match the actual plan --
  a Flutter app is a separate toolchain, not a Go package under
  internal/.
- 2026-09-15 -- integration lead published a first visual direction
  (teal-ink "ledger" console, one screen) as a Claude Artifact,
  framework-agnostic. Superseded 2026-09-17, see below.
- 2026-09-17 -- a separately dispatched design pass produced a full
  interactive HTML/CSS/JS prototype at internal/desktop/design/
  (charcoal/mint/amber, chat-first, organization conversation tree,
  exact-decision review dialog, tabbed worker details, connections/
  skills, light+dark, Playwright-validated). David reviewed both
  directions and chose this one as the design of record; the earlier
  artifact direction is retired. Committed 4154162. This is a design
  reference only (README.md's own words) -- it changes no generated
  prompts, contracts, root dependencies or acceptance criteria, and the
  Fyne-to-Flutter contract change is still NOT FORMALIZED: no ADR, no
  tools/specgen change, no repo/directory placement decided for the
  actual Flutter toolchain (which is not a Go package and cannot live
  under internal/ the way Fyne's internal/desktop was specified; the
  org's standing policy is no new repos without routing through the
  seat). The design's own "Flutter handoff" section names every
  affected owner: desktop host, cmd/zatiti-desktop, shared client/
  transport and credential plumbing, installation/packaging, dependency
  foundation, desktop integration and platform qualification. Formalize
  the contract change and placement before any desktop implementation
  lane starts.
- 2026-09-18 -- effects $ref fix landed (8df2dfe): lead reproduced the
  red by restoring main's service.go against the new test, then green;
  module-wide green post-merge. The lane's sweep found the identical
  fail-open bug in internal/accounting/service.go:347-360 and
  internal/execution/service.go:443-456; port lane dispatched
  (wave3/schemaref-port). memory, evidence, registry, contract, cli are
  not affected.
- 2026-09-18 -- FIRST REAL ASSEMBLY DEFECT, found by tests/integration
  and confirmed by the lead: internal/registry cannot assemble the real
  modules and internal/application cannot dispatch through it. Four
  mismatches, all at the registry seam: internal mutations rejected for
  lacking a submission key (validate.go:81-82); Callers validated as
  operation IDs while every module and application use owner names
  (validate.go:84-88, registry.go:110-116); Lookup is exact-version while
  application calls Lookup(id, 0) for "current"; *Registry has no
  LocalIOFor, which application requires for all local-IO operations
  including installation.init. Every package had landed against fakes.
  Fix lane dispatched (wave3/registry-seam); integration continues over a
  labeled test-local catalog built from the real modules, with the
  real-registry test skipped-with-evidence until the fix lands. Blocks
  cmd/zatiti. Eleven lanes in flight.
- 2026-09-18 -- two self-inflicted limits from lifting the lane cap, both
  recovered with no work lost: (1) ten lanes on this 4-core laptop drove
  load to ~130 (memory fine) because package-level -race runs were not
  under the lease; fixed by a THROTTLE rule (every -race / flutter
  test / flutter build under R-build-lease, -p 2 everywhere). David
  waived his load>10 hold for this push on the laptop: the lease is the
  throttle. (2) Ten lanes exhausted the account-wide session limit in
  ~25 minutes at 09:40 PT; every lane stopped mid-work, all work intact
  and uncommitted on disk, lease found free. At 11:15 PT eight lanes
  resumed via message (registry-seam, controller, integration, serenity,
  schemaref-port, spec, responses, flutter); packaging (25 files on
  disk) and ci (4 files on disk) are HELD, not on the critical path,
  resume into the same worktrees when a slot frees. Lanes now commit
  early slices so another limit costs less.
- 2026-09-18 -- serenity lane finding (uncommitted at time of writing):
  Serenity is pinnable (github.com/sirerun/serenity @ f5a5154, protocol
  MEMORY_VERBS v1 over MCP 2025-11-25) but NO adapter operation is
  dispatchable at that pin: a tool call needs a three-request MCP
  handshake while one Invoke is one physical request; fact ids are
  SHA-256 not UUID; no command identity or status lookup, so writes
  cannot be reconciled; no cost bound, revision report, or build that
  reports its commit. The adapter is therefore an honest refuser
  (capability report: all six kinds unsupported). Memory stays
  effectively offline in v1 until Serenity ships those capabilities --
  a product-level consequence for David, not a lane bug.
- 2026-09-18 -- SPEC REVISION 2 LANDED (731a3bd, cdffb45, 1b9534c): 36
  roots (apps/desktop Flutter client replaces internal/desktop and
  cmd/zatiti-desktop; all 9 owned requirements, 27 participant blocks and
  16 named cases carried over, nothing orphaned), request_context retyped
  to ArtifactLocator (adapters stage the request record; the controller
  publishes and substitutes), and the installation database seam as a
  one-method contract.DatabaseBackup supplied only through
  installation.WithDatabaseBackup. Lead verified: zero Go touched, nothing
  outside the spec write scope, render --check green, 13 renderer tests
  pass. Renderer gained a RETIRED-roots deletion path. Go follow-ups NOT
  yet dispatched: (a) internal/contract DatabaseBackup + installation
  option and real streamed-hash backup path + cmd/zatiti wrapper; (b)
  request_context in github, httpread, responses, serenity, memory,
  execution (controller lane already briefed); (c) remove the Fyne pins
  from go.mod/go.sum/tools_deps.go and the lock report, record
  Flutter/Dart/plugin pins. Open ruling: nothing in the contract names
  who swaps the database file during a restore. Spec nit to fix in
  revision 3: every AGENTS.md prints CLI paths with the binary name
  (`zatiti artifact export`), which is where 11 modules got the wrong
  descriptor token shape.
- 2026-09-18 -- registry-seam: fix written and green on named tests
  (internal mutations keyless, Callers = owner names, Lookup(id,0) =
  highest version, LocalIOFor, both schema forms accepted), real registry
  + 16 real modules + real application drive installation.init and public
  mutations in its assembly test; waiting on the lease to commit.
  descriptor-drift: 143 public drift errors fixed in tree with a permanent
  per-package catalog pin test; scope extended to 8 internal
  ExpectedVersion flags and to five modules (accounting, memory, effects,
  execution, evidence) shipping bare internal schemas whose owner-private
  $defs nothing can resolve. schemaref-port: accounting committed
  (19d4fdf), execution fix written. responses: committing. serenity:
  committed f1cdfbf, lead-verified (pin facts checked against the real
  source; overclaim fence proven load-bearing), merge queued on the lease.
- 2026-09-18 -- LEAD ERROR, main red on one test: serenity landed
  (3f80a17) through a one-liner that merged before testing and whose
  pipes swallowed the failure. The red test is legitimate:
  TestEmbeddedSchemasMatchFrozenCatalog pins serenity's embedded
  PhysicalCallEvidence to the catalog, and spec revision 2 (landed
  minutes earlier) retyped request_context. Fixes: scratch land.sh now
  rebases and verifies IN THE WORKTREE (pipefail) and fast-forwards main
  only on full green; the two idle adapter agents are converting all
  four adapters to revision 2 (wave3/reqctx: serenity first to unblock
  main, then httpread; wave3/responses: responses before it lands, then
  github). memory/execution acceptance of kind=artifact still to do.
- 2026-09-18 -- responses committed 2706675 (not landed; converting to
  revision 2 first). Nothing provider-specific hardcoded; AGENTS.md
  specifies NO upstream wire shape, so the real protocol sits behind an
  unexported seam with an empty qualified-protocol registry and every
  production Invoke refuses capability_unsupported after doing all local
  checks; tests use a synthetic protocol resembling no vendor API. With
  serenity also an honest refuser, v1 currently has NO working hosted
  model and NO working memory service until a wire protocol is pinned
  and Serenity ships the missing capabilities. Revision-3 gaps: tool to
  operation mapping absent from ContextToolDefinition;
  BoundEnforcement.evidence.profile_digest circular (found by both
  adapters); no reconciliation fields in ResponsesProfile.
- 2026-09-18 -- integration slice 1 (ready, waiting on lease) found six
  more real seam defects, each with a skipped test; lead confirmed two in
  code. Two fix lanes dispatched. wave3/app-seams: local-IO commands
  finished twice while evidence allows one finish (every synchronous
  local-IO mutation fails internal_error); grant.* and
  configuration.apply are default review classes but application never
  passes a candidate digest, so not even the bootstrap owner can apply
  configuration; a replayed refusal returns a failed Result with nil
  error. wave3/edge-seams: client's post-unknown-ack command.get sends
  only submission_key (frozen input needs scope, operation, version) and
  reports the lookup's fault as the command's; server maps status from
  the Go error not the Result; configuration sends candidate_digest ""
  to every _<owner>.validate; configuration bootstrap emits no event;
  the owner credential is 32 raw bytes, not a legal Authorization value,
  with no exported way to obtain it.

## Planned

- Wave 3: controller, desktop, cmd/zatiti, cmd/zatiti-desktop.
- Wave 4: tests/integration, tests/qualification, packaging,
  .github/workflows.
- Not yet scheduled, no lane assigned: add a narrow database-digest or
  bounded-backup seam to contract.Dependencies so internal/installation
  can complete backup/restore for real (see Shipped, 2026-09-12) --
  requires a coordinated contract change via tools/specgen, not a plain
  Go edit, since contract.Dependencies is frozen/generated (ADR 001).
- DONE 2026-09-18 (8df2dfe), port to accounting/execution in flight: fix internal/effects's
  checkSchemaDocument/resolveRefs type-mismatch (service.go:389-432) so
  $ref resolution actually runs instead of silently no-oping; found
  2026-09-12 during the evidence landing (see Shipped). Needs its own
  build/vet/test/race/mutation pass since effects is already on main.
- Not yet scheduled, no lane assigned: retype PhysicalCallEvidence.
  request_context from bare ArtifactRef to ArtifactLocator's "staged"
  variant in docs/implementation/adapter-schemas.json (see Shipped,
  2026-09-14, adapters/github) -- affects every adapter package
  (github landed; responses/httpread/serenity too), since none of them
  have an IDSource to mint a durable ArtifactRef for request bytes they
  build synchronously. A tools/specgen contract change, not a plain Go
  edit.

## Blocked

- 2026-09-10 — internal/adapters/responses and internal/adapters/serenity:
  external endpoints not resolved per docs/implementation/dependencies.lock.json.
- 2026-09-10 — OpenRouter key 403 resolved: the earlier weekly-limit deaths
  did not recur; the two resume lanes (connections, accounting) completed
  full cycles past the previous death window, and fresh dispatches
  (messaging) are running. No top-up needed unless a fresh 403 appears.
