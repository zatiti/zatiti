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
- 2026-09-18 -- registry seam LANDED (3926941 fix(registry), d98ab05
  test(application)). Lead mutation check: neutralizing the
  unknown-caller fence let assembly accept an unknown internal caller
  (TestNewRejectsUnknownCallers red), restored. The registry was the
  wrong side on all four points; application production code untouched.
  Two rules the contract never states are now implemented as application
  assumed them and need spec revision 3: Lookup version 0 = highest
  registered version; registry accepts bare or self-contained schemas and
  Lookup returns self-contained documents pruned to reachable defs. The
  new internal/application/assembly_test.go drives the REAL registry +
  16 real modules + real application over temp storage through
  installation.init, principal.create with replay, and internal calls.
  Unedited modules still fail registry.New until descriptor-drift lands.
- 2026-09-18 12:17 PT -- SECOND session-limit stop, resets 16:00 PT; all
  eleven lanes down. Lead landing on-disk work meanwhile: serenity
  revision-2 conversion (verified race/lint green, committed and landing,
  turns main green); execution $ref port (race 473s green, lint 0,
  committed with the earlier accounting commit, landing); app-seams'
  three fixes (application/evidence/policy; build/vet/gofmt clean, race
  queued); Flutter increment (8 commits, 15k lines, one third-party
  package flutter_secure_storage; format/analyze/test running). Lease
  queue had reached an hour, so a second lease (R-race-lease) now carries
  package-level race/lint runs while R-build-lease carries commits and
  module-wide runs; wrapper polling is age-aware so the oldest waiter
  wins. One-shot resume scheduled for 16:03 PT.
- 2026-09-18 -- controller lane done (35830e2 + 5f11fe0): 10
  self-mutation checks each red against a named test; write-ahead
  dispatch journal; never resends a claimed effect; Stop drains or returns
  outcome_unknown, never invents cancellation. It reports EIGHT more
  landed defects / contract gaps, two lead-verified in code: (1) no
  service identity exists for the controller; (2) _effects.pending hides
  ready/executing operations; (3) _execution.observation looks up
  execution's OWN record id while the controller only has the effects
  operation id (controller_ops.go:132); (4) _execution.fence compares a
  per-run attempt counter, not the controller generation; (5) context
  pinning, verification and messaging admission are unreachable through
  the allowed calls; (6) the controller cannot enqueue ready tasks; (7)
  memory records its execution job with the creation version while
  claiming bumps it, and never sets Job.operation_id (memory/
  handlers.go:1118, execution/store.go:713); (8) restore expects a
  controller-side database file swap the contract does not provide.
  Lane wave3/exec-seams dispatched for 2, 3, 4, 7; 1 and 6 go to the
  cmd/zatiti assembly brief; 5 and 8 to spec revision 3.
- 2026-09-18 -- execution $ref port landed (b10d4a6 accounting, 7a4d2b3
  execution); serenity revision-2 conversion landed (23f6205): main is
  GREEN again.
- 2026-09-18 -- CONTROLLER LANDED (c7d32c7 core, 9d1afcd revision-2
  request-context publication), 29 of 36. Lead mutation check: without
  the write-ahead "claimed" journal marker, crash recovery recorded a
  possibly-sent effect as not_sent (TestCrashAtEveryBoundaryNeverResends
  red), restored. Landing script verified race (67s), lint 0, module-wide
  green. cmd/zatiti lane dispatched (wave3/cmd) with the assembly brief:
  the end-to-end test (build the binary, serve on a temp socket, init,
  principal.create + replay, list, MCP over stdio) is the milestone.
- 2026-09-18 -- Flutter first increment complete on wave3/flutter-desktop
  (9 commits, tip 1ac1e3c; landing queued). Lane-verified and
  lead-verified: format 0 changes, analyze 0 issues, 150 tests pass,
  `flutter build macos --debug` builds (sandbox off, documented
  unqualified). Real: strict-JSON transport over Unix socket and mutual
  TLS, one submission key per mutation reused on explicit retry, never
  auto-resends, unknown acknowledgment locks until command.get; typed
  models for 17 resources; live source over 21 operations with a
  capabilities.list check at connect; full UI per the design with dark/
  light, scaling, keyboard, semantics. Demo-only: the labeled in-memory
  source. NOT proven: any exchange with the real Go controller, mutual
  TLS against internal/server, Linux/Windows builds, secure storage at
  run time, assistive technology. Brief conflicts deferred to the next
  increment (brief wins): generated bindings + catalog digest check,
  secure-storage drafts, restored selection on relaunch, named
  prerequisite when secure storage is unavailable. Covers parts of
  Z21.exact_decision, Z21.reconnect_no_duplicate,
  Z21.hierarchy_navigation, Z21.capability_and_pause_cards; not
  Z21.first_conversation, two_child_chiefs, delegate_work, close_client,
  JOURNEY.desktop_daily_work.
- 2026-09-18 -- integration slice 1 lead-verified (16 pass, 16 skipped
  with evidence, lint clean), committed on the lane's behalf, landing
  queued. edge-seams on-disk work (client lookup, server status mapping,
  configuration candidate digest + bootstrap event, header-legal owner
  credential) builds, vets, and passes all four package race suites;
  one lint issue outstanding; _reviews.ensure at plan time not started.
- 2026-09-18 -- integration slice 1 committed (c135a9a on
  wave3/integration) but its landing correctly ABORTED: the landing
  script's module-wide run found its real-registry tests failing "in an
  unrecorded way" because the registry fix is now on main and the
  failure text changed. Two facts fell out: (a) internal/identity
  declares SubmissionKey=true on `_identity.activate`, which the fixed
  registry rejects (catalog: all 43 internal mutations are keyless) --
  routed to descriptor-drift as one more drift class; (b) the integration
  tests' skip-with-evidence matching must be updated once the drift
  lands, then the branch lands. main NOT moved; the abort is the script
  doing its job.
- 2026-09-18 -- defect from the Flutter lane, lead-confirmed:
  internal/messaging list handlers (handlers_public.go:184, :462) put
  next_cursor INSIDE data, while the catalog's mailbox.list output schema
  is additionalProperties:false with only items and every other owner
  uses the envelope's next_cursor field. Needs a messaging fix lane.
  Also from the Flutter lane: nine places the public catalog has no
  operation for something the design shows (conversation history, caller
  identity, unread/preview, memory claim listing, review labels, artifact
  names/provenance, responsibility schedule/time zone, task check
  failures, event tailing cursor) -- recorded in apps/desktop/README.md
  "Catalog gaps"; the client renders empty or prerequisite states and
  invents nothing. Product-level input for spec revision 3.
- 2026-09-18 -- lead process defect, fixed: three of the lead's docs
  commits landed on lane branches instead of main because the shell's
  working directory drifted between tool calls (one caused a rebase
  conflict on wave3/app-seams). All lead git commands now use explicit
  `git -C <path>`.
- 2026-09-18 -- lead process notes: (1) stopping a background job that
  had already won R-build-lease orphaned the lease for ~5 minutes (the
  stop is a SIGKILL, so the wrapper's release trap never ran); released
  by hand after confirming no live holder. Rule: never stop a wrapped
  job; let it fail and release itself. (2) A flaky test found by a
  landing run: scheduling's cursor-tamper test overwrote one base64 byte
  with 'x', a no-op 1 run in 64; fixed (guaranteed-different byte,
  proven 20/20) and landed in e100870. (3) Two seam lanes' branches were
  cut before the serenity conversion, so their commit hooks hit the
  then-red serenity test; both branches rebased onto current main via
  tagged stash before committing.
- 2026-09-18 -- descriptor-drift: 14 of 16 public-descriptor commits on
  the branch, hook green each time; accounting and execution staged,
  waiting for a rebase past the landed $ref fixes; internal-descriptor
  classes (ExpectedVersion, SubmissionKey, ScopeRequired) and the
  self-contained internal schemas still to do on the same branch. Lead
  lands the public batch first so cmd/zatiti and integration can move.
- 2026-09-18 -- app-seams LANDED (dd43cf7 evidence, 0a4828f application,
  44582a0 policy). Contract quote decided the double-finish case
  ("persist command identity plus accepted internal pending disposition
  at Prepare, then replace pending disposition once at Finish"), so
  evidence now admits exactly one replacement of an accepted disposition
  and no other second finish (SQL-fenced as well as handler-checked;
  lead's single-fence mutation stayed green because of the second guard,
  defense in depth). Application binds configuration.apply's sealed
  candidate_digest to _policy.check from a closed per-operation map
  (nothing else may supply a digest), and a failed disposition always
  returns with its fault as the error, first time and on replay; one
  existing Z14 test that encoded the old nil-error replay was updated
  by the lead. Policy's default review class narrowed from every
  `grant.*` to grant.create and grant.update (R5-010: revoke commits
  restrictive state immediately; reads expand nothing). Still open:
  configuration never calls _reviews.ensure at plan time (edge-seams
  lane, resumes 16:00); grant.create/update have no digest to bind a
  review to (spec revision 3).
- 2026-09-18 -- FLUTTER DESKTOP FIRST INCREMENT LANDED (9 commits,
  tip bdabcbc), 30 of 36 roots on main. Lead-verified: format 0 changes,
  analyze 0 issues, 150 tests pass, module-wide Go verify green (the
  first landing attempt exposed the flaky scheduling cursor test, since
  fixed). apps/desktop is a real wire client (Unix socket + mutual TLS,
  strict JSON, submission-key discipline, 21 operations) with the full
  UI from the design; never run against the real controller yet. Lane
  stopped; next increment brief to be written after cmd/zatiti exists.
- 2026-09-18 -- MILESTONE, one step early: the cmd/zatiti lane reports
  serve + init + generated CLI + MCP-over-stdio passing END TO END
  against a real assembled controller, using a throwaway descriptor
  patch identical to the one the registry-seam assembly test uses (the
  descriptor drift is the last thing between the real modules and
  registry.New). Not yet committed: its drift-dependent tests are being
  converted to skip-with-evidence so slices can commit; the patch itself
  is never committed. Two gaps it hit: (1) the raw-bytes owner
  credential -- fixed on wave3/edge-seams, landing; (2) NEW, confirmed
  against the brief: no sanctioned authority for the controller's
  service principal. identity's brief (AGENTS.md:483) assigns "one
  initial human owner and scoped service identities" to the bootstrap
  transaction, but landed identity bootstrap creates only the owner, and
  grant.create is a review class with no public review path, so an
  ungranted service principal fails every application.Internal call.
  Lane wave3/identity-service dispatched. Follow-up noted: registry.New
  over 16 modules costs ~2s per CLI invocation (catalog build); fetch
  capabilities from the controller later.
- 2026-09-18 -- edge-seams LANDED (client 59a2ed6, server 0c93821,
  configuration c1 + installation 626d6b1; see git log). Client's
  post-unknown-ack command.get now sends the frozen input (scope,
  submission_key, operation, operation_version) and a failed lookup
  keeps the outcome unknown; server maps HTTP status from the Result via
  contract.HTTPStatus, not from the Go error; configuration computes the
  candidate digest for owner validation and emits its bootstrap event;
  installation mints the owner credential as the complete "Bearer
  <base64url>" header value and custodies it. Lead verified all four
  package race suites and lint; one unchecked-error lint finding in a
  new test fixed by the lead. Still open on this lane: configuration's
  _reviews.ensure at plan time (lane resumes 16:00).
- 2026-09-18 -- CORRECTION: the "identity declares SubmissionKey=true on
  _identity.activate" item recorded earlier is retracted. The
  descriptor-drift lane's per-field probe shows zero submission_key
  drift in any module; the lead's count of 30 came from public
  mutations, which legitimately require keys. The integration suite's
  failure text on that point will be re-examined against its own patched
  catalog when that lane resumes. The confirmed remaining assembly
  blocker is owner-private $defs referenced by internal operations in
  accounting, memory, effects and execution (bare schema bodies); in the
  drift lane's scope as the second batch.
- 2026-09-18 -- cmd/zatiti committed on wave3/cmd (8170620 assembly +
  serve + CLI/MCP wiring, f7f01b6 command-tree/exit/bootstrap tests,
  6a2b239 end-to-end binary journey), landing. On the current tree 17
  tests pass and 7 skip-on-known-drift (the end-to-end journey, serve
  lifecycle incl. ownership loss, catalog pin, open-installation lock):
  those seven are the milestone tests and stay skipped until the
  descriptor batch lands, after which the lane deletes the skips and
  the lead re-verifies (a lead mutation of the ownership-loss case was
  inconclusive for exactly this reason). The throwaway descriptor patch
  was verified never committed.
- 2026-09-18 -- identity-service lane done (2b56a67 on
  wave3/identity-service, landing): identity bootstrap now creates the
  controller's scoped service principal (kind=service, name
  "controller", installation-wide) in the same transaction as the owner,
  with exactly one unbounded grant, _identity.authority -- the single
  capability the application.Internal dispatch path actually checks
  (dispatch.go:148-176 gates operations by descriptor caller allowlist +
  KindService; revalidateAuthority only asks _identity.authority). New
  API identity.Service.ControllerPrincipal(ctx, reader) hands the Actor
  to the entrypoint; prerequisite_missing before bootstrap,
  permission_denied once revoked; revocation denies the very next call
  and survives reopen. Bootstrap refuses an owner named "controller".
  Schema gap for revision 3: the frozen _identity.bootstrap input has no
  field for a service credential, so the principal has no credential;
  fine for the in-process seam (explicit Actor), not for an
  out-of-process controller. cmd/zatiti's provisional
  principal.create/grant.create path is superseded on its rebase.
- 2026-09-18 -- cmd/zatiti LANDED (e3ab03c assembly/serve/CLI/MCP,
  fb8fb6e tests, 72c0e3e end-to-end binary journey), 31 of 36 roots on
  main. Seven milestone tests still skip on known descriptor drift until
  the descriptor batch lands (next in the lease queue), after which the
  lane deletes the skips, switches controller-identity provisioning to
  identity.ControllerPrincipal, and the end-to-end journey runs for real.
- 2026-09-18 -- identity-service LANDED (656527a). Lead mutation check:
  adding one extra capability to the controller principal's grant made
  TestControllerPrincipalHoldsNothingElse fail on principal.create;
  restored. cmd/zatiti switches to identity.ControllerPrincipal on its
  next rebase. Worktree removed.
- 2026-09-18 -- DESCRIPTOR BATCH 1 LANDED: 16 per-package commits (tip
  c7f9cf3) plus a permanent per-package catalog pin test in every
  module; lead mutation check (re-introducing the binary-name CLI token
  turned artifacts' pin test red) and module-wide green. Every public
  descriptor now matches the frozen catalog. Remaining assembly blocker:
  bare internal schemas referencing owner-private $defs in accounting,
  memory, effects, execution (batch 2, same lane), plus the internal
  ExpectedVersion/ScopeRequired classes. cmd/zatiti signalled to rebase,
  drop its drift skips and run the end-to-end journey for real.
- 2026-09-18 -- descriptor batch A committed (b126eba, landing): the
  per-package pin test now covers every descriptor, public and internal,
  field by field. It found 47 internal descriptors declaring
  ScopeRequired=[installation_id] where the catalog says [] (the catalog
  rule "installation_id exactly when the input schema requires scope"
  holds for all 262 domain ops, so packages now derive it from the input
  schema) plus the 8 ExpectedVersion flags; all 8 handlers already
  enforced the version (audited with file:line), so the flag was the
  only drift. Confirms zero internal SubmissionKey drift anywhere.
  Catalog defect for spec revision 3: task.assign is public but the
  catalog gives it callers ["messaging"]; the registry rejects any
  public descriptor with callers. Batch B (self-contained internal
  schemas) in progress: the last blocker for unpatched assembly.

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
