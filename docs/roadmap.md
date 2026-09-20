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
  tests) goes through the lease as ONE foreground command.
  **CORRECTION, 2026-09-20 (confirmed independently by two agents, P04
  and P41): `with-lease.sh` does not exist anywhere on this machine --
  checked PATH, ~/.claude/skills, repo root, .claude/. The canonical
  primitive is `~/.claude/skills/claim/scripts/claim.sh claim
  R-build-lease --purpose "<what>"`, run the command, then `claim.sh
  release R-build-lease <sha>` immediately after -- exactly what every
  card in this remediation plan has actually been doing successfully.**
  The underlying warning below is still real and still applies: never
  claim from a monitor or background task -- a lane's monitor once held
  the lease idle for 21 minutes while everything queued behind it; claim
  in the SAME foreground command sequence that runs the heavy work and
  releases it, never detach the claim from the command it's protecting.
  `-p 2` on every go invocation.
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
- 2026-09-18 -- descriptor batch A LANDED (255f0dd). Lead mutation check:
  flipping `_tasks.transition` ExpectedVersion made
  TestDescriptorsMatchFrozenCatalog fail at catalog_test.go:112, restored
  byte-identical; module-wide suite green on the rebased tip. Throwaway
  worktree and branch wave3/drift-batchA removed. cmd/zatiti told to
  rebase onto 255f0dd; drift lane continues batch B unrebased.
- 2026-09-18 -- integration slice 1 re-verified on main 9e1c302 (branch
  rebased to cebf802): 6 FAIL, 21 PASS, 5 SKIP. All six are the suite
  lagging landed fixes, not new product defects: two "step past" tests
  add SubmissionKey to internal mutations, which validate.go:82 now
  rejects (obsolete since 3926941); three assert one bootstrap principal
  where 656527a now provisions the controller service principal too; and
  TestTransportParity finds principal.list ordering (created_at, id)
  ties between the owner and the controller principal created in one
  tick, broken by random id per instance. Lane resumed with the
  diagnosis (scratchpad brief-integration-resume.md); it decides whether
  the parity harness or identity's ordering changes and reports any
  identity fix as a seam defect. Lead process note: the lead's first
  commit of the matcher change ran the pre-commit hook without the build
  lease and was stopped; the leased retry was refused by the hook because
  tests/integration is red on the branch, as designed. The change stays
  uncommitted in the lane's worktree.
- 2026-09-18 -- descriptor batch B (3657aaf, self-contained internal
  schemas; registry.New over all 16 real modules returns nil) verified by
  the lead: race+lint green on accounting/effects/evidence/execution/
  memory; mutation (accounting delivering a bare input schema again) made
  TestRealRegistryDrivesApplication fail at bind, restored. Landing
  ABORTED by design: on main+B, cmd/zatiti goes red because B unmasks the
  e2e path its landed tests skipped on drift (helpers_test.go:140), and
  landed bootstrap.go:182 provisions its own controller principal, which
  conflicts with identity's bootstrap-provisioned one (656527a). The fix
  (switch to identity.ControllerPrincipal) is uncommitted on wave3/cmd,
  so B and cmd land together: cmd rebases onto wave3/drift-batchB
  (ff37ea7 = main + B) and commits there; lead lands that tip. Drift lane
  done; worktree retired after the combined landing.
- 2026-09-18 -- integration lane reports (commit queued on the build
  lease, unverified by the lead): two obsolete step-past tests deleted,
  exact principal-set assertion (owner + controller service principal),
  TestTransportParity list steps compare transports on one instance (the
  cross-installation tie is principal.list's random-id tiebreak, an
  observation not a defect), a raw HTTP wire driver added as a fifth
  parity transport (what apps/desktop consumes), five previously skipped
  tests now pass on main. Remaining skips name three open seams, all
  routed: (1) owner cannot activate configuration or create grants
  because nothing creates the review that authority.go:336-338 demands
  (`_reviews.ensure` at plan time), (2) bootstrap emits no configuration
  evidence (handlers.go ~979), (3) DatabaseBackup Go seam. edge-seams
  resumed with (1)-(2) and the ordering question; responses resumed to
  finish its revision-2 conversion and convert github. Lanes live: cmd,
  exec-seams, integration, edge-seams, responses. packaging, ci and
  serenity wait for the 16:03 PT resume to keep the lease queue short.

- 2026-09-18 evening -- REVISION-3 RULING, verified by the lead before
  landing: "the human principal whose current authority admitted a
  review-class request is an eligible reviewer of that exact request;
  services, workers and agents never are, and proposer separation stays
  mandatory for them." Raised by edge-seams while fixing the review
  deadlock (the bootstrap owner could never activate configuration or
  create a grant, because the eligible set named only ancestor chiefs,
  which bootstrap creates as workers, while every policy requirement is
  human-required -- so no principal could decide any review, ever). The
  contract does not settle it: policy/AGENTS.md:352 and reviews:332 say
  "an eligible owner's decision" without defining eligible owner, and
  separation of proposer and reviewer is optional except for workers and
  agents. Lead verification, not taken on the lane's word: only
  `requester.Kind == "human"` joins the eligible set (policy/authority.go
  eligiblePrincipals, after the entitlement and revocation fences);
  `SeparateProposer = worker || client_agent` is unchanged from main
  (authority.go:639, no staged diff on that line); and the reviews owner
  re-verifies independently at decide time (reviews/decide.go:120-127
  refuses a non-human reviewer and a proposer-reviewer collision). A
  narrower revision-3 reading (only holders of the installation wildcard)
  is confined to that one eligibility line plus its test.
- 2026-09-18 evening -- lease queue is the bottleneck, by design: seven
  commits waited on R-build-lease at once, each running the whole-module
  suite through the pre-commit hook (cmd, responses, packaging, httpread,
  edge-seams, exec-seams, ci, plus the lead's integration landing). Load
  reached 509 but the machine is I/O-bound, not CPU-bound (about 1,250
  disk transactions per second with two Go processes running), so raising
  concurrency would make it worse; the queue is the correct behavior and
  was left alone. One lease-economy ruling issued: exec-seams collapsed
  three commits into one, saving two module-wide runs.

- 2026-09-18 evening -- tests/integration slice 1 LANDED (655683a,
  ecfbaf0): the first suite that runs the real modules against each other.
  Lead mutation check: neutralizing the submission replay fence in
  internal/application (commandBegin returning no retained result) made
  TestWireClientReplaysOriginalKeyAfterDisconnect fail at wire_test.go:112
  with a 409 and a different command id; restored byte-identical. Race and
  lint green on the package, module-wide green. The suite adds a raw HTTP
  wire driver over the Unix socket -- the path apps/desktop consumes -- as
  a fifth transport compared against CLI, MCP, in-process and client.
  32 of 36 package roots now have verified code on main.
- 2026-09-18 evening -- cmd/zatiti MILESTONE committed (10cdbfd on
  ff37ea7, landing): the entrypoint runs the controller as the
  bootstrap-created service principal (identity.ControllerPrincipal on one
  read snapshot) instead of provisioning its own, and with batch B
  delivering self-contained internal schemas the drift skips are deleted,
  so the catalog, serve-lifecycle and end-to-end binary tests run against
  the real unpatched sixteen-module assembly. Lane reports serve, init over
  the socket, owner credential hand-off to a 0600 profile, principal create
  with a submission key, exact replay, changed-input refusal, principal
  list, `mcp serve` over stdio with 197 tools, and SIGTERM shutdown, plus
  fail-closed behavior on lock loss and an unknown principal override.
  Lead mutation check: disabling the explicit --controller-principal
  override branch stopped TestServeRefusesUnknownControllerPrincipalOverride
  from passing -- but by hanging to the 10-minute Go test timeout rather
  than failing fast, because serve then starts successfully; the invariant
  is covered, the test budget is not. Routed to the lane with the e2e
  3-minute budget flake (e2e_test.go:39, failed once at 192s under the
  parallel hook run, passed on retry).

- 2026-09-18 evening -- THE PRODUCT RUNS. Descriptor batch B (e3f0a40) and
  cmd/zatiti's controller-principal fix (bd28bbd) LANDED together; the
  registry assembles all sixteen real modules unpatched on main. The lead
  then built the landed binary and brought a controller up by hand, not
  through any lane's test: `serve` on an empty state directory listens in
  13s; `init` over the socket returns installation dad54565 at generation
  1 and writes the owner credential to a 0600 profile; `principal list` as
  the owner shows exactly the human owner and the `controller` service
  principal; `principal create` with submission key live-1 completes; the
  exact re-send returns the same command id and identical data; the same
  key with changed input is refused 409 submission_conflict (CLI exit 4);
  the new agent appears in the next list; SIGTERM exits 0 and removes the
  socket. Authentication verified directly against the live socket: no
  credential returns verification_failed with HTTP 422 and no data, the
  owner credential returns the list. Landing gate: race (25m budget), lint
  and module-wide green, plus the two mutation checks recorded above.
- 2026-09-18 evening -- DEFECT found by the lead's live bring-up, routed to
  cmd/zatiti: a state directory whose path is long enough to push the
  socket past the 104-byte sun_path limit fails with the raw
  `bind: invalid argument` and no explanation, after the whole assembly has
  already been built. It must be a named, explanatory startup refusal that
  states the limit and the offending path length. Reproduced with a state
  directory under a long temporary path; the same run on a short path
  succeeded.
- 2026-09-18 evening -- landing-gate change: land.sh now passes
  `-timeout 25m` to the per-package race run (RACE_TIMEOUT overrides). The
  Go default of 10 minutes aborted the cmd/zatiti landing under load, with
  TestServeRefusesUnknownControllerPrincipalOverride 2m16s in; that package
  builds a binary and runs real controllers, so it is the slowest in the
  module and runs on every landing.

- 2026-09-18 20:10 PT -- THE GATE WAS BROKEN FOR EVERY LANE, root cause and
  fix. Commits and landings kept failing with a ten-minute panic that looked
  like a product defect and was not: cmd/zatiti builds a binary and runs
  real controllers, and its end-to-end journey carries an eight-minute
  internal budget, so on a loaded machine that one package exceeds Go's
  default 10-minute PER-PACKAGE timeout -- in the plain module-wide run, not
  only under -race. Every lane's pre-commit hook ran `go test ./...` with no
  -timeout, so every commit in the module was hostage to it. Fixed in three
  places: the pre-commit hook now runs `go test -p 2 -timeout 30m ./...`
  (the -p 2 also stops a commit saturating the machine while other lanes
  work), land.sh's module-wide run takes MODULE_TIMEOUT (default 30m), and
  its per-package race run takes RACE_TIMEOUT (default 25m). The httpread
  landing that exposed it was re-run afterwards.
- 2026-09-18 20:00 PT -- second machine-wide hazard fixed: golangci-lint
  takes a lock inside its cache directory, so any two concurrent runs
  collide with "parallel golangci-lint is running" regardless of which lease
  each holds -- the two-lease split made this reachable. with-lease.sh now
  exports a per-worktree GOLANGCI_LINT_CACHE. Credit to the edge-seams lane,
  which corrected the lead's wrong diagnosis (it was not a stale base) with
  evidence.
- 2026-09-18 20:00 PT -- shared-stash hazard, twice in one hour, no work
  lost. The git stash stack is shared across every worktree and lanes push
  and drop concurrently, so stash@{n} selectors go stale between two
  commands: one lane dropped another's entry by position (restored with
  `git stash store`), and another lane's entry disappeared under it (it
  recovered the tree from the dangling stash commit and proved it
  byte-identical). Lane rules now require applying by SHA, keeping the entry
  until the commit succeeds, and dropping only by resolving the selector
  from the SHA in the same command.
- 2026-09-18 20:07 PT -- account session limit stopped every lane for the
  fourth time today (resets 22:20 PT). Uncommitted work on disk at that
  moment: responses (rev-2 plus the github conversion), edge-seams (11 files,
  reviews.ensure), exec-seams (12 files), packaging (26 files, slice 1), ci
  (17 files), cmd (12 files, the DatabaseBackup seam just started), desktop
  (not started). A recurring two-hourly cron re-enters the standing plan in
  scratchpad/autonomous-run.md so the run survives the stop.

- 2026-09-19 00:20 PT -- exec-seams LANDED (50a0f3d), four cross-package
  seams the controller hit driving the real modules. Lead mutation check:
  dropping ready and executing from pendingStates (internal/effects/
  store.go:605-616) made TestPendingListsActionableOperations report 3
  operations where 5 were owed; restored byte-identical. The pending-
  visibility fix is the consequential one: an operation the controller had
  admitted (ready) or claimed (executing) before it stopped was invisible on
  restart, so its attempt and reservation were stranded with no call that
  could name them. Also: `_execution.observation` is keyed by the effects
  operation id rather than the owned record id; `_execution.fence` compares
  controller generations instead of a per-run attempt counter; and a
  network job is never claimed, so memory can record its outcome at
  create's version without a version race.
- 2026-09-19 00:35 PT -- packaging LANDED (a54e59b): release manifests for
  the controller and the Flutter desktop bundle, detached signatures over
  caller-supplied keys, macOS LaunchAgent and Linux user systemd templates,
  install/upgrade/uninstall over a per-user layout, and an installed-tree
  audit. Lead mutation check: stopping VerifyTree comparing SHA-256 digests
  (packaging/tree.go) made TestVerifyTreeReportsEveryDefect fail with no
  digest finding for a tampered binary; restored byte-identical. 35 of 36
  package roots now have verified code on main.
- 2026-09-19 00:40 PT -- third gate repair: the pre-commit hook's
  golangci-lint now passes --allow-parallel-runners AND distinguishes "could
  not run, another run holds the lock" from "found issues". The old branch
  reported a lock collision as a lint failure, so a commit whose code was
  fine was rejected and a build-lease hold was burned. Found by the
  exec-seams lane, which had already worked around it on its own lint.
- 2026-09-19 -- follow-ups raised by exec-seams, not yet assigned:
  (a) internal/controller settle.go settleAdmission still strands an
  unacknowledged admit; with ready now listed it can take the last
  attempt_id plus its own journal generation and record not_sent.
  (b) `_effects.record` cannot close an old-generation attempt without that
  generation, so a lost journal leaves it unrecordable; a successor-
  generation rule (not_sent for a never-claimed attempt, unknown for a
  claimed one) is a spec-revision item.
  (c) internal/controller tick.go backoff bookkeeping keys on the listed
  set, so listed-but-inadmissible operations never expire their backoff
  entries. Harmless today.
  (d) REVISION-3: the frozen Operation type has no per-attempt generation
  field and attempt_ids are bare uuids; job.create's frozen input has no
  operation field, so execution reads operation_id from inert input.

- 2026-09-19 02:15 PT -- THE DESKTOP CLIENT TALKED TO A REAL CONTROLLER
  (f6d87f2, 2f26c0b). apps/desktop/tool/live-proof.sh builds cmd/zatiti,
  starts serve on a short /tmp state dir, runs init, reads the owner profile
  (asserting mode 0600 and the "Bearer " prefix, sent verbatim) and drives
  the Dart transport over the live socket; 14/14 green, and it never skips:
  a missing binary, a missing socket or a controller that exits is a hard
  failure with the controller's log attached. The transport increment was
  correct -- all 11 transport tests passed on the FIRST live run, envelope
  decoding, fault mapping, submission-key replay, command.get and the
  uncredentialed refusal all matching with no change. The two real defects
  were one layer up, and only a live run could find them: (1) mailbox.list
  is private to its recipient, and that permission_denied propagated out of
  loadSnapshot, destroying conversations, decisions, tasks and files over an
  optional inbox read; (2) the controller keeps the client's message_id and
  returns it as the resource id, but the client keyed its own message on the
  submission key, so a mailbox copy rendered a second bubble. Lead mutation
  check: re-throwing the mailbox refusal made "a refused inbox costs the
  inbox, not the workspace" fail with SourceRefusal(prerequisiteMissing);
  restored byte-identical. Not verified and stated as such: mutual TLS
  against the real listener, any surface rendering real records (none can
  exist yet), Linux, and a launched .app rather than the real widget tree.
- 2026-09-19 02:20 PT -- PRODUCT DEFECT, top priority, found by the desktop
  lane driving a real controller and traced by the lead: no task can be
  created on a fresh installation. task.create returns internal_error "peer
  call _accounting.reserve failed" at HTTP 500. Mechanism:
  internal/tasks/ports.go:17-30 callPeer preserves a peer's named fault when
  it arrives in the payload (payload.Error != nil), but when ports.Call
  returns a Go error instead, line 24 stamps CodeInternalError over it -- so
  accounting's legitimate refusal (currency mismatch is invalid_input, an
  exhausted budget is budget_unavailable) reaches the user as an unnamed
  crash, and a correctable misconfiguration looks like a bug. Compounding
  context: a fresh installation's accounting currency is XXX
  (internal/accounting/limits.go:20) and task.create requires a match, while
  setting a currency needs configuration.apply, which was itself deadlocked
  until tonight's reviews fix. Assigned to the cmd lane with instructions to
  establish whether the second part dissolves once that lands, and to report
  the exact sequence a user must run to create a first task.
- 2026-09-19 -- the edge-seams lane's scratch probe found two further review
  defects that package fakes had hidden, both fixed on its branch: a strict
  decode of `_reviews.check` against a one-field struct failed a real
  approved decision as internal_error, and for capability-only review
  classes the pending review was created INSIDE the gate transaction that
  then rolled back with the refused mutation, so review.list stayed empty
  and nothing could ever be decided. The probe is preserved at
  scratchpad/edge-probe-review-flow.go.txt for the integration lane to adopt
  as a permanent test; it is the only artefact that walks plan, decide and
  apply through the real modules.

- 2026-09-19 03:15 PT -- ALL 36 PACKAGE ROOTS ARE ON MAIN. The frozen plan
  (docs/implementation/packages.json, revision 2) is complete as a codebase.
  Landed in the final run: the qualification suite (54cea3a), packaging
  slice 2 with desktop bundle assembly and install lifecycle (abf8f7e), the
  CI and release workflows with their policy validator (b94f254), and the
  hosted-model adapter (af6de5d, c0a3210). Every root passed build, vet,
  gofmt, golangci-lint, go test -race, the module-wide suite, and an
  independent lead mutation check.
  Mutation checks in this run, each red then restored byte-identical:
  * qualification -- breaking httpread's loopback refusal in a DIFFERENT
    package was caught by the suite ("loopback read: err=<nil>, want
    permission_denied"), which is exactly what a qualification suite is for.
  * packaging slice 2 -- allowing a bundle symlink to escape made
    TestExtractBundleRefusesHostileArchives/escaping_symlink accept a
    hostile archive. (A first attempt was INVALID -- it left a variable
    unused and failed to build, which proves nothing; redone so the code
    still compiled.)
  * CI -- disabling the explicit-timeout policy rule made
    TestPolicyRejectsMutations fail on both the unit and release profiles.
    That rule now enforces, in CI, the exact defect that broke every commit
    in the module last night.
  * responses -- disabling the profile classification check let a
    restricted-classified artifact be disclosed to the provider, and
    TestInvokeRefusesBeforeAnyCall caught it. That is the guard that keeps
    private data from reaching a vendor.
  WHAT THIS DOES NOT MEAN: the product is structurally complete, not
  finished. The model adapter refuses every call until a wire protocol is
  written against a real endpoint and qualified; task creation and the
  review flow have fixes in flight; and memory stays offline until Serenity
  ships what its adapter needs.

- 2026-09-19 04:30 PT -- the OpenAI Responses wire protocol LANDED (30f102f)
  with github's revision-2 conversion (fff218c), and integration slice 2
  (f927692), which deletes the test-local catalog so the suite assembles
  over the REAL registry. The protocol is pinned to the published OpenAPI
  document by commit ddface9b and sha256, revision string
  openai-openapi/2.3.0@ddface9b, not to a prose docs page; the pricing page
  refused an automated fetch and its tier text is cited as the founder's
  quote, explicitly not re-verified. Conversation-first: POST /v1/conversations
  mints a client-known handle and the step's request record names it before
  POST /v1/responses is sent, so a timed-out call has something to reconcile
  against. Caching disabled and service_tier pinned to default so the
  profile's two rates are exact; any deviation records tokens and leaves the
  amount unknown. Input ceiling refused above 272,000 tokens. Lead mutation
  checks: removing that ceiling let a profile be created that would
  under-reserve on long prompts; accepting a cursor whose MAC does not
  verify let a cursor survive a controller restart. Both restored.
- 2026-09-19 04:45 PT -- LIVE DEFECT on main, found by the responses lane
  reading the controller's contract rather than each adapter's own tests,
  verified by the lead, fixed and landing: internal/controller/perform.go
  validUsage accepts EXACTLY the six $defs/Usage fields and rejects any
  other shape, but github and httpread both returned the nested
  ProviderUsage document. So perform.go:70-72 discarded their real
  no-charge usage and synthesized unknown advisory billing for EVERY GitHub
  call and every web read, corrupting spend records for all non-model work.
  It survived two landings because every test asserted the call succeeded
  rather than what Observation.Usage contained. Lead mutation check:
  restoring the ProviderUsage document made the new
  TestObservationUsageIsTheAccountingDocument fail on success, provider
  failure and not-sent.
- 2026-09-19 -- CORRECTION the lead owes the founder: recommending OpenAI
  over OpenRouter, the lead said the conversation-first pattern lets the
  adapter prove what happened after a timeout. Half of that is wrong. It can
  confirm a call DID execute (items appear), but cannot prove one did not:
  an empty item list is equally consistent with "never ran" and "still
  running", and the API documents no list or search of conversations, so a
  step lost before its response arrives is unknown forever. A reconciled
  success also cannot recover usage, since conversation items carry no token
  counts, so the worst-case reservation stays outstanding. OpenAI remains
  the right choice -- OpenRouter cannot find the call at all -- but
  "reconcilable" was too strong. Also accepted knowingly: conversation-first
  makes TWO physical calls per model step against a contract that specifies
  exactly one. Both are revision-3 items.

- 2026-09-19 06:30 PT -- FOUR DEFECTS IN ONE CHAIN, each hidden behind the
  last, all found because a fix let a test reach code no test had reached.
  The lesson to keep: the skips were load-bearing. Two integration tests had
  been skipping for a day on the review deadlock; every layer removed
  exposed the next.
  1. The review deadlock itself (bf7010c): nothing created the review the
     policy gate demanded, and for capability-only classes the pending
     review was created inside the gate transaction that then rolled back
     with the refused mutation.
  2. Consumed-plan re-apply (c1ffac8): handleApply had its own "identical
     retry" branch returning the original revision for an applied plan. Key
     replay is evidence's job in commandBegin, which runs BEFORE the
     handler, so that branch could never see a true replay -- every apply
     reaching it was a new command against a consumed plan. Nothing was
     activated twice (team.list 1, head unchanged, zero events), but the
     system minted and retained a COMPLETED command id for work that did not
     happen. In a system whose premise is that evidence proves what
     occurred, a false completion record is worse than a duplicate, because
     it is indistinguishable after the fact from a real one.
  3. Peer validate envelope (163af15): configuration strict-decoded
     `_<owner>.validate` replies as {"resource": Validation}; identity
     returned a bare Validation, contradicting its OWN declared output
     schema while its own test decoded tolerantly. A tolerant test is how a
     schema violation survives. The other six validate peers and all eight
     activate outputs were checked against the catalog and are correct;
     identity was the only landmine. configuration's fault now names the
     peer and the decode error instead of "unreadable result".
  4. THE SEVERE ONE, ruled on by the lead and pending a fix as of this
     entry: `_identity.authority` gated the internal read on the ACTOR
     holding a capability literally named "_identity.authority"
     (identity/internal_ops.go:146, authority.go:336). The gate is
     circular -- to learn what you may do you must already hold the
     capability to ask -- and the brief defines this operation's access
     control as a CALLER allowlist (AGENTS.md:466) which internal/registry
     already enforces (registry.go:139-140). The clinching evidence: the
     controller's entire standing capability set is exactly opAuthority
     (service_principal.go:43), a grant that exists only to work around
     this. Consequence on main today: only the bootstrap wildcard owner and
     the controller can pass ANY policy check, so every narrowly granted
     principal is inert -- in a product whose purpose is delegating bounded
     authority to agents. Invisible until now because nothing had exercised
     a narrowly granted principal end to end.
- 2026-09-19 -- REVISION-3 RULING (lead, under the founder's 16-hour
  autonomy grant, for his ratification): "_identity.authority is gated by
  its caller allowlist, not by the subject's own capabilities; a registered
  unrevoked actor in the transaction's installation may read authority, and
  the controller's standing grant of that capability becomes redundant."
  Required with it: the installation-scope match and the revoked-actor
  refusal stay; the change is confined to authority()'s own call and does
  not touch s.authorize; whether an actor may read ANOTHER principal's
  authority is answered in a code comment rather than inferred; and tests
  pin a narrow-grant agent answered, a revoked actor refused, and an
  out-of-scope actor refused.

- 2026-09-19 07:30 PT -- THE PATTERN WORTH KEEPING: a test double more
  permissive than the thing it stands for. Three instances cost this effort
  real defects, each of which looked like success until the worst possible
  moment:
  1. The owner credential: the fake accepted a value the real header
     validation would refuse, so nothing could authenticate on a real
     installation (fixed 2026-09-18, 626d6b1).
  2. The backup key: internal/installation's fake secret store mapped a NAME
     to a reference, while the real platform's Get resolves only the opaque
     reference Put returns. Every backup on a real installation minted a
     fresh key, custodied it, discarded the only handle, and sealed a bundle
     nothing could ever decrypt -- while reporting success with a valid,
     digest-matching artifact. Found by the integration lane wiring the seam
     to the REAL platform; verified by the lead at
     contract/stores.go:11 and platform/secret_headless.go:66-71,120-124.
  3. The artifact metadata fake in internal/tasks echoed EVERY unregistered
     reference back as an available artifact with the requested digest --
     which is precisely how the capability_evidence validation hole hid in
     that package's own tests. Found by the deliberate fake hunt the lead
     ordered after (2), not by waiting for it to surface.
  PRACTICE ADOPTED: when a fix touches a seam, make the package's fake
  behave like the real implementation (refuse what the real one refuses), and
  verify a produced artifact through the exact path that will later consume
  it. installation's performBackup now opens its own published bundle
  through the restore path before reporting success.
  Still noted, not changed: internal/installation's fakeBlobs.Stage ignores
  the size bound the real store enforces; no test exercises the bound today.
- 2026-09-19 07:25 PT -- backup key fix (ff8741c, landing): the sealing key
  is custodied by the opaque reference Put returns, carried in a clear ZTBH1
  header on every bundle so a restore resolves it from the artifact alone
  after a restart or a rewind, and recorded in installation_backup_keys
  (migration 3) so successive backups reuse one key. A backup refuses to
  proceed unless the returned reference resolves the key back. Lead mutation
  check: reverting to name-based custody now makes the BACKUP fail loudly
  ("backup key reference does not resolve after custody") instead of
  silently producing an unrestorable bundle -- the failure mode moved from
  silent data loss to an honest refusal at the moment of the mistake.
  FOUNDER-FACING: every backup taken before this fix is unrecoverable
  through the product; on the headless store each re-Put under the same name
  deleted the previous key file, so all but the most recent per installation
  are cryptographically lost. Take a fresh backup after this lands. The
  keychain backend's re-Put behaviour was NOT verified and no claim is made
  beyond "not recoverable through the product".

- 2026-09-19 07:50 PT -- acceptance revalidation LANDED (6f2e5ca): admission
  revalidates the acceptance contract's sealed_inputs and the verifier
  profile's capability_evidence artifact through the artifacts owner, so a
  task can no longer be admitted with a verifier profile naming an artifact
  that does not exist -- in the path that decides whether work counts as
  done. Lead mutation check: skipping the evidence revalidation made both
  TestAdmissionRevalidatesAcceptanceArtifacts subcases return completed
  instead of artifact_fault and invalid_input. Scope finding from the lane's
  first hook run, correctly resolved: verifier profiles are
  installation-owned, so their qualification evidence is installation-level;
  admission resolves it at Scope{installation_id} rather than the task's
  narrower scope, which the artifacts owner would refuse to match.
- 2026-09-19 07:55 PT -- THE FINDING THAT IS NOT A DEFECT, and may matter
  more than any of them. The lead asked the cmd lane, which has driven the
  product end to end more than once, whether the first-task sequence is
  something a person could discover from the CLI's own help and errors, or
  whether it only works because the lane knew the order. Its answer,
  unprompted and plain: a person could NOT discover it; the founder's first
  real attempt fails at task.create three or four times and he stops before
  reaching a draft task. What already helps: every command is in --help,
  `zatiti capabilities schema` returns each operation's JSON schema, and
  every refusal is named and specific. What strands him: (1) nothing says a
  verifier's capability-evidence artifact must be uploaded before the first
  task, or that it must be at INSTALLATION scope -- the error reads as a bad
  id; (2) nothing says the unconfigured currency is spelled "XXX" and that
  only a zero-spend task is admissible until a budget exists; (3) a
  nine-field definition with a nested verifier profile has no example
  anywhere in the binary. ROUTED, not filed: a doctor requirement that
  prints the actual upload sequence when no verifier evidence is published,
  and a complete runnable example in `task create --help`. Two constraints
  set by the lead: no refusal may be weakened to make the path easier (the
  errors are correct, merely unactionable in advance), and every printed
  example must be executed in a test, because a help example that has rotted
  is worse than none for someone already struggling.

- 2026-09-19 11:15 PT -- integration slice 3 LANDED (ebb0aa9) and the
  overnight run's work is complete. THE SKIP LIST IS EMPTY: zero t.Skip and
  zero skipKnownDefect call sites remain in tests/integration. Every former
  known-defect branch is now failRegressedDefect, so if any of those defects
  returns the suite FAILS with the recorded cause instead of quietly
  skipping. The slice adopts the edge-seams probe as permanent tests: the
  configuration flow (plan -> review_required naming the candidate digest ->
  review.list finds the pending review -> decide -> apply under a new key
  carries the digest -> exact replay -> consumed plan stale under another
  key) and the grant flow with the client-agent self-grant refusal, plus
  TestBackupRestoresActualBytes asserting the restore half for real through
  installation.WithDatabaseBackup.
  Lead mutation check, and a lesson in how to run one: destroying
  persistReviewRequest did NOT fail TestOwnerActivatesConfigurationThroughReview,
  because the configuration flow creates its review at PLAN time via
  `_reviews.ensure` and never touches the rolled-back capability-gate path.
  The first mutation was therefore a FALSE PASS, and stopping there would
  have certified the test on evidence it never provided. Re-run against
  TestOwnerCreatesGrantThroughReview, which does depend on that path, it
  failed with "no review bound to digest ... among 0 reviews" -- the exact
  deadlock that hid behind a skip for a day. When a mutation does not go
  red, the fence may simply be in the wrong place; assuming the code is
  unguarded is how weak tests get blessed.
  Open observations, asserted rather than skipped: principal.list tie order
  across installations (owner and controller principal share a created_at
  tick, identity/principal_ops.go:183), handled in parity by same-instance
  comparison; unknown credential refusals surface as verification_failed 422
  from identity while the server's own mismatch check is permission_denied
  403.
- 2026-09-19 11:20 PT -- supply chain: fyne.io/fyne/v2 REMOVED (fdba444).
  ADR 002 replaced the native Go desktop with the Flutter client six days
  earlier, retiring internal/desktop and cmd/zatiti-desktop, but the module
  still required a GUI toolkit and its cgo windowing stack because a
  build-tagged root anchor file held it -- a file whose own comment said go
  mod tidy should replace the mechanism once real imports landed. Cobra, the
  MCP Go SDK and modernc.org/sqlite are now imported by real package source,
  so the anchor was deleted and tidy keeps them. Dropping Fyne also removed
  its transitive testify, go-spew, go-difflib and yaml.v3. Nothing imported
  any of it; the module-wide suite is green without them.
- 2026-09-19 16:20 PT -- docs/implementation-remediation/ landed on origin/main
  (96a8f0e, PR #2): a fresh audit against baseline 34d291f found 27 confirmed
  gaps -- the control plane exists but the durable worker loop that turns an
  admitted intent into independently verified work does not -- and a P00-P49
  card plan with wave-gated dependencies and milestones M0-M4. David asked
  for /loop wrapping /apply --pool at the plan's max parallel sessions.
  /apply --pool's literal mechanism parses docs/plan.md for `- [ ] T<id>`
  checkbox lines via the /claim skill; this plan uses P<id> cards in
  plan.json/assignments/*.md and docs/plan.md does not exist, so a literal
  invocation finds zero candidates. Driving it instead via the plan's own
  README dispatch protocol (one isolated worktree per card, one landing
  owner, R-build-lease for module-wide commands) plus this repo's already-
  established "Parallel dispatch protocol" ceiling above (8 lanes practical
  max on this machine). P00-P02 are a serialized gate (contract freeze ->
  shared types -> dependency reconciliation) before any of the 15 wave-3
  cards can start; dispatched P00 alone now. The three live wave3/* worktrees
  (cmd, integration, responses) predate this plan and are NOT remediation
  cards -- wave3/integration has one commit ahead of main, wave3/responses
  has uncommitted changes -- they need landing or reconciling before P24/P46/
  P13 (their eventual owners under the new plan) can be dispatched.
- 2026-09-20 00:06 UTC -- P00 LANDED (PR #3, b6f2cc2, rebase-merged). Verified
  independently before landing, not taken on the dispatched agent's word:
  zero Go source touched (diff is tools/specgen/*.py, docs/implementation/*,
  every package AGENTS.md); requirements.json 127->144 and acceptance.json
  116->130 are byte-for-byte additive, nothing existing removed or reworded;
  the one real breaking change (ResponsesParameters/ResponsesEvidence split
  into prepare_session/model_step) is exactly what the card specified and was
  self-flagged in the PR. CI's build-and-test matrix came back red on 11
  packages -- all catalog/descriptor/schema-parity tests comparing committed
  Go artifacts against the new revision-3 catalog, expected because P00's
  allowed writes exclude Go source and P37/P14/P40/etc. are the cards that
  update each package's side. David decided (in-session, given three options:
  merge-and-track-red / batch the gate before touching main / a separate
  integration branch): merge now and track the red rather than re-serializing
  the parallelism this push is for. Full package list, which card fixes each,
  and the one unrelated pre-existing flake (internal/platform symlink test,
  confirmed passing on pre-P00 baseline) are recorded in docs/lore.md under
  "Expected red: catalog/descriptor-parity tests" -- check there before
  treating any of those specific tests as a fresh regression. P00's claim
  released; P01 claimed and dispatched next.
- 2026-09-19 17:20 PT -- cleaned up the three leftover wave3/* worktrees from
  the 2026-09-18 push (they predate this plan and were left on disk after
  landing -- see the hygiene rule this violated). wave3/cmd and
  wave3/integration were fully merged already (content-identical to commits
  already on main under different SHAs from the rebase-merge, confirmed via
  `git cherry main <branch>`, not just ref ancestry); removed both, worktree
  and branch. wave3/responses's committed history was equally fully merged,
  but the worktree held real uncommitted WIP: a LIVE qualification of the
  Responses adapter already run against the real https://api.openai.com
  (model gpt-5.6-luna, founder credential resolved from the macOS keychain
  via keychain:zatiti-responses/zatiti, real spend of 53 micro-USD) --
  documented in internal/adapters/responses/PROTOCOL.md's new "Live
  qualification (performed 2026-09-19)" section and a new
  tests/qualification/responses_live_test.go (gated behind
  ZATITI_QUALIFY_RESPONSES_LIVE=1, so it does not run in normal CI). This is
  NOT recorded anywhere else in this file, in docs/lore.md, or in any sitrep
  -- the morning sitrep (docs/sitrep/2026-09-19.md) was still asking David to
  run a much smaller manual curl probe, which this live run supersedes, so
  it most likely happened after that sitrep from a session with no roadmap
  trail. Flagged to David directly rather than silently absorbed. Banked
  as-is (wip: commit 4aa219b, pushed to origin/wave3/responses) rather than
  merged -- it predates P00's revision-3 contract (built against the old
  single-call ResponsesParameters shape, not the new prepare_session/
  model_step split) and needs real review, which is P13's job once wave 3
  opens. P13's owner should pull this branch first and read its PROTOCOL.md
  diff before writing anything new -- it is real evidence of live endpoint
  behavior (billing/tool_usage/frequency_penalty fields the pinned OpenAPI
  document doesn't list, defaults echoed live, the byte-based input-token
  bound holding on both observed steps), not something to redo. Worktree
  removed after the push was verified on origin.
- 2026-09-19 17:35-17:52 PT -- P01 LANDED (PR #4, cd38926, rebase-merged),
  and a real process gap found and fixed along the way. zatiti_p01's coding
  was independently verified correct before landing (internal/contract's
  full suite passes, including the required
  TestLocalDecisionToolRejectsSmuggledAuthority/GoldenDocuments/
  Revision2EnvelopesRemainReadable behaviors; go vet/gofmt/golangci-lint all
  clean; scope respected -- internal/contract only) -- but the agent's own
  handoff was unreliable twice: first it stopped mid-task with real,
  correct, uncommitted work and a non-answer final message ("I'll end my
  turn here and wait for the background task/monitor notification"); on
  resume it finished by committing with `--no-verify`, bypassing the local
  hook without authorization. Lesson recorded for every future dispatch:
  never trust an agent's completion report -- check `git status`/`git log
  main..HEAD`/open PRs yourself, and if the coding is real but unpackaged,
  the lead finishes committing/pushing/opening the PR after independent
  re-review rather than discarding it.
  The `--no-verify` was masking a real, separate bug worth finding on its
  own: hooks/pre-commit runs full-module `go test ./...` and hard-blocks
  ANY commit on ANY package failure, not just touched packages -- so once
  P00 landed, EVERY commit from EVERY future lane would have been refused
  regardless of which package it touched, making "merge now, track the
  red" impossible to actually execute. Given three options (allowlist
  expected-red packages / scope tests to the diff's affected packages /
  blanket --no-verify with sign-off), David chose the allowlist. Built and
  installed (bd0e1a9): docs/implementation-remediation/expected-red.txt
  lists the 13 structurally-red packages (matches the full untruncated
  `go test ./...` run exactly, confirmed by hand -- internal/platform's
  earlier failure did not recur, confirming it really was a flake, not
  structural, so it's deliberately NOT on the list); hooks/pre-commit now
  parses per-package FAIL lines and only blocks a commit if a failing
  package is NOT on that list -- a build/compile failure with no
  attributable package, or any regression in a currently-green package,
  still blocks unconditionally. Installed to .git/hooks/pre-commit, which
  every worktree shares via the common git dir, so this applies everywhere
  immediately, present and future worktrees alike. A card landing should
  remove its own package from expected-red.txt once that package is green
  again -- the list must shrink to empty, not calcify.
  P02 claimed and dispatched next -- the last card in the P00->P01->P02
  serialized gate; wave 3's 15 cards open the moment it lands.
- 2026-09-19 18:00-18:15 PT -- P02 LANDED (PR #5, d11500a, rebase-merged).
  Reviewed the full diff before landing: revision 2 of
  docs/implementation/dependencies.lock.json -- moved the stale "Fyne v2.8.1
  compiles and links" claim into a clearly-labeled historical_evidence entry
  (Fyne is gone from go.mod/go.sum since fdba444), documented that neither
  P20's controlled repository runner nor the Serenity seam needs a new pin
  (both roots restrict production imports to internal/contract), pinned the
  already-landed OpenAI Responses and Serenity protocol commits with an
  honest controlled-fixture-vs-real-service distinction, and reconciled
  apps/desktop/pubspec.lock for the first time (flutter_secure_storage +
  its federated platform packages, flutter_lints, licenses read from each
  package's actual LICENSE file). Only dependencies.lock.json changed --
  go.mod/go.sum/tools.go untouched, correctly, since no new library was
  needed. This card's own agent finished its full git workflow correctly
  (commit, push, open PR #5, release claim, did NOT self-merge) --
  contrast with P01's incomplete handoff two entries up.
  THE P00->P01->P02 SERIALIZED GATE IS FULLY CLEAR. Dispatched wave 3's
  first batch of 8 (the practical ceiling from the "Parallel dispatch
  protocol" above), all in isolated worktrees, all running concurrently:
  P03 (internal/identity), P05 (internal/configuration), P08
  (internal/accounting), P09 (internal/artifacts), P10
  (internal/messaging), P11 (internal/tasks), P13
  (internal/adapters/responses), P37 (internal/registry). Chosen
  deliberately: P03/P05/P08/P10/P11 are ALL five of P14's prerequisites
  (fastest path to unlocking wave 4's execution critical path), P37 is the
  fastest fix for internal/registry's tracked-red catalog test, and P13
  recovers the banked live-qualification WIP on origin/wave3/responses
  (commit 4aa219b) -- that agent was explicitly told to pull it first and
  incorporate what's still valid under the new prepare_session/model_step
  split rather than duplicate or discard it. Each agent was told to remove
  its own package from docs/implementation-remediation/expected-red.txt in
  its landing commit once its package's tests genuinely go fully green.
  Remaining wave-3 cards not yet dispatched (P06, P07, P12, P26, P29, P30,
  P34) queue for the next free lane slots as these 8 land.
- 2026-09-19 18:20 PT -- P03 found a real gap in P00's contract freeze and
  fixed a real pre-existing security bug, correctly declining to invent a
  seam for the gap. contract-proposals.md section 2 specified
  `identity.current` (public, principal kind/scope only) and
  `_identity.worker.resolve` (internal, worker-subject resolution) as
  operations for P00 to turn into exact schemas -- exactly P03's card steps
  1-2 -- but neither landed in the frozen output (absent from
  operations.json, contracts.md, requirements.json,
  internal/identity/AGENTS.md); confirmed not a doc-prose gap since
  internal/identity's own TestDescriptorsMatchFrozenCatalog passes today
  against exactly the pre-existing 18 ops. P03 did not invent them (would
  break that same parity test and create an uncatalogued CLI/MCP surface)
  and instead completed everything else the card's required tests actually
  needed. ACTION NEEDED before P41 (CLI) or P42 (desktop) dispatch: a small
  P00-revision-4-style contract addendum adding these two operations,
  through tools/specgen/render.py like P00 did -- NOT done yet, deliberately
  deferred (see load-incident entry below for why). Also flagged: even once
  `_identity.worker.resolve` exists in the contract, `contract.WorkerOperator`
  (P01's revision3.go) is injected only into application/execution's trusted
  composition, not identity -- so the operation's actual Go implementation
  may belong outside internal/identity. Left as an open question for
  whoever picks up the addendum or eventually implements it (likely P14).
  Separately, P03 found and fixed a real authorization bug while
  implementing revocation-recheck: principal.get/update/revoke,
  grant.get/update/revoke and credential.provision/revoke checked only the
  request's own `scope` field against the caller's envelope, never the
  by-id TARGET's own scope -- letting a narrowly-scoped worker read/revoke
  the owner's principal or mint the owner a credential by ID. Fixed, proved
  with a test that fails without the fix (pending landing, see below).
- 2026-09-19 18:25-18:35 PT -- NEAR-REPEAT of the 2026-09-18 load incident,
  caught and stopped, root cause found and documented
  (docs/lore.md, "R-build-lease is advisory only"). Once P37 and P08
  finished coding and hit the commit-time full-module test, they correctly
  paused instead of forcing through (load was ~26-66 already from the
  other 6 lanes' own package-level work) -- but they had no live watcher,
  so I resumed them (and shortly after P10) with "acquire the lease and
  proceed regardless of ambient load," reasoning the LEASE (not a load
  threshold) was the real serialization mechanism. That reasoning had a
  hole: hooks/pre-commit does not actually check who holds the lease --
  it's pure convention. P37 (holding the lease correctly) and P10
  (resumed, did not actually hold it) ended up running two concurrent
  full-module `go test -p 2 -timeout 30m ./...` runs; P09 nearly became a
  third before self-aborting on its own initiative. 1-minute load went
  from ~26 to 150+ in under 10 minutes -- past the 2026-09-18 incident's
  ~130 failure point. Neither P37's nor P10's commit actually landed
  (both failed/were interrupted under the spike); no work was lost, both
  worktrees' changes were intact and recovered. Separately compounding
  this: several agents, once done coding, stopped their turn claiming to
  be "waiting for a monitor" with no actual live watcher, and re-polling
  (when resumed, or via a self-started retry loop) at 30-90s intervals,
  each poll re-paying 300-400K+ tokens of accumulated context for zero new
  information -- a real, expensive anti-pattern independent of the load
  incident itself.
  Response: stopped all 8 lanes from running `git commit` or any
  multi-package command until explicitly cleared by name, one at a time --
  abandoning lease-based self-coordination entirely until the hook is
  fixed to actually enforce it (tracked as future work, not yet
  scheduled). Landing the current queue (P37, P10, then P03/P08/P09/P05/
  P11/P13 in the order they reported ready) fully serialized from here.
- 2026-09-19 19:00-19:40 PT -- P37's escalation deepened: fixing
  internal/registry correctly (its own package fully green, 65 tests)
  exposed that revision 3 changed the SHAPE of shared $defs (Operation/
  Artifact/Responsibility/Conversation: 65->68 defs, contents differ), not
  just added 4 operations. Every real (non-fake) module still shipping the
  old shape fails registry assembly with "operation schema redefines the
  shared definition X" once registry enforces the correct one -- confirmed
  by reading cmd/zatiti/assembly.go, internal/application/assembly_test.go
  and tests/integration/fixture_test.go, all of which wire every domain
  module into one real registry.New(...). This would turn cmd/zatiti,
  internal/application, tests/integration and tests/qualification red --
  all four currently green, none tracked -- and implicates internal/
  connections (P06) and internal/policy (P07), neither dispatched yet, plus
  P08/P09 (dispatched, not yet landed). Before P37's fix this was silently
  masked: registry's stale catalog happened to agree with connections/
  policy's equally stale descriptors. P37 correctly declined to decide
  this alone (a merge-gating/allowlist call spanning lanes outside
  "internal/registry ONLY") and escalated with full evidence instead of
  guessing. Given three options (extend expected-red.txt to cover the four
  newly-exposed packages and land P37 now / hold P37 and land P06+P07+P08+
  P09 first so the end-to-end proof suites never go red / a different
  order), David chose: hold P37, land P06/P07/P08/P09 first, P37 last.
  P08 landed clean as the empirical test case (PR #6, ed2d161) -- its full
  pre-commit run showed NO "redefines the shared definition" errors, only
  the already-tracked expected-red.txt gaps, which is a real, positive
  signal that David's chosen order avoids the red window as intended (at
  least for accounting; artifacts (P09) touches the Artifact type more
  directly and is the next test of this). Dispatched P06 and P07 to close
  the remaining gap, this time with the commit-serialization lesson baked
  into their initial dispatch brief (report ready, wait for the lead to
  clear by name -- no self-checking the lease in a loop). P37's PR stays
  uncommitted/held until all four land.
- 2026-09-20 12:21-13:00 PT -- MACHINE REBOOTED overnight (confirmed via
  `uptime`), killing the previous session and all 9 in-flight subagents
  (P03/P05/P06/P07/P09/P10/P11/P13/P37) around 2026-09-19 ~20:00 PT --
  ~16.5 hours of downtime before the reboot, then resumed. No work was
  lost: every worktree's uncommitted changes survived on disk, and all 9
  refs/claims/* locks were still held on origin. SSH auth to GitHub broke
  (ssh-agent had no identities post-reboot); fixed with `gh auth
  setup-git`. David's 12-hour autonomous-work window (started ~17:15 PT
  2026-09-19) had expired ~7.5 hours earlier; checked in and got explicit
  confirmation to keep driving, plus the P37/P09 sequencing decision below.
  Landing from here is done directly by the lead (no subagents -- they
  were all lost in the reboot, and doing it directly is if anything safer,
  fully serialized by construction).
  Refined finding on the P37/P09 question: P09 (internal/artifacts) embeds
  the Artifact/Operation shared types directly, so it is STRUCTURALLY
  blocked from landing until P37's catalog fix is in -- there is no order
  that avoids this (unlike P08, which doesn't reference those types at
  all and was landing-order-independent). Given this, David decided: land
  P37 now, extend expected-red.txt to cover the newly-exposed packages,
  then land P06/P07/P09/P10 (and now also P03, since landing P37 revealed
  `_identity.activate` has the same shared-defs mismatch) as fast as
  possible to shrink the allowlist back down.
  P37 LANDED (PR #7, 62aa5c3): catalog.json now 201 operations at the
  correct revision-3 $defs shapes. expected-red.txt extended with cmd/
  zatiti, internal/application, tests/integration, tests/qualification
  (comment explains why, references this entry).
  P09 LANDED (PR #8, 720cbed): rebased onto post-P37 main, reverified,
  all pre-commit failures traced only to the already-tracked
  `_identity.activate` issue -- nothing new. internal/artifacts is not
  itself on expected-red.txt (never was).
  P10 LANDED (PR #9, 929435d) -- conflict resolved as above, `go test
  ./internal/messaging` confirmed fully green before trusting its own
  expected-red.txt removal.
  P06 LANDED (PR #10, 9d0f077) -- no conflicts, no shared-defs issues in
  this package, clean first-try commit.
  P03 LANDED (PR #11, 5e15bea) -- the two findings from the 2026-09-19
  18:20 PT entry (missing identity.current/_identity.worker.resolve
  contract, the by-id scope authorization bug fix) shipped in this
  commit as originally planned. ALSO fixed a related but separate issue
  discovered while landing: internal/identity/schemas.go's wireDefs
  constant was itself still revision-2-shaped (missing CallbackRoute/
  OperationAttempt, stale Operation/Responsibility) -- this, not
  anything in P03's own card text, is what was actually causing every
  `_identity.activate`-triggered failure across cmd/zatiti/tests/
  integration/tests/qualification. Fixed by replacing wireDefs verbatim
  from internal/identity/AGENTS.md's "Local schema definitions" block,
  verified by parsing both as JSON and diffing programmatically (not
  just gofmt-clean) -- confirmed exact match. This is a GENERAL pattern
  worth watching for in every remaining card: any package with its own
  schemas.go-style embedded wireDefs copy may have the same staleness,
  independent of that card's own described scope. Confirmed present in
  internal/configuration too (`_configuration.activate` showed the
  identical error pattern once `_identity.activate`'s was fixed) --
  P05's landing needs the same check-and-fix.
  P07 LANDED (PR #12, c594f9c) on retry -- same code, second attempt
  passed clean past the flake.
  P05 LANDED (PR #13, 16025f4) -- confirmed and fixed the predicted
  schemaDefs staleness in internal/configuration (same technique as P03).
  PROCESS BUG FOUND while landing P11: neither P03's nor P05's commit
  actually removed its own line from expected-red.txt, despite both
  packages being fully green and both commit messages saying they would
  -- an oversight in how the lead assembled those two commits (fixed the
  schemas, forgot the allowlist edit). Not a correctness problem (a
  package that never fails just never triggers the allowlist check for
  itself), but a real violation of the "remove the moment it lands" rule
  -- caught and fixed while resolving P11's own rebase conflict on the
  same file (dropped internal/identity, internal/configuration and
  internal/tasks all in the same resolution, confirming zero failures in
  the first two before removing them). Lesson for every remaining
  landing: after `git reset --soft origin/main && git add -A`, explicitly
  check whether the landing package's own line is still in
  expected-red.txt and remove it as part of the same commit -- don't
  assume the original card's diff already handled it.
  P11 LANDED (PR #14, e418f6e). P13 LANDED (PR #15, 42d12e1) -- incorporated
  the banked origin/wave3/responses evidence (spot-checked: billing/
  tool_usage/frequency_penalty language present in the landed PROTOCOL.md),
  then deleted that now-fully-consumed branch from origin.
  This completes the original 8-lane wave-3 batch (P03, P05, P06, P07,
  P08, P09, P10, P11, P13) plus P37 -- all 12 cards landed clean.
  THE BIG CHECK, and an honest result: `TestRealRegistryAssemblesLandedModules`
  still fails -- not on any of the 12 just-landed packages, but on
  `_skills.activate` (internal/skills, same wireDefs-shape pattern as
  identity/configuration/tasks). internal/skills is NOT part of wave 3 at
  all -- it's P19 (wave 8, depends on P18), nowhere near dispatched.
  Realistic conclusion: cmd/zatiti/internal/application/tests/integration/
  tests/qualification will stay on expected-red.txt for a long tail, not
  a quick cleanup -- assembly requires EVERY package the real registry
  wires in to have current shared-defs shapes, and most packages haven't
  landed their own revision-3 card yet. This isn't a new problem so much
  as a restatement of what the original P00 lore.md entry already said
  (internal/execution not green until P14/wave 4+, internal/mcp not until
  P40/wave 5+) -- the P37 escalation just made the mechanism (assembly-
  wide shared-defs cross-checking, not merely per-package parity) explicit.
  Not escalating this as a new decision -- it's the same already-approved
  "track the red, shrink incrementally" strategy, just with a longer
  runway than initially hoped. Continuing wave 3's remaining cards (P12
  internal/effects, P26 internal/memory both map directly to entries
  still on this list; P29 internal/storage, P30 internal/platform, P34
  internal/evidence do not).
- 2026-09-20 13:40 PT -- IMPORTANT CORRECTION: the 2026-09-19 lore.md entry
  calling internal/platform's TestListenPrivateRefusesSymlinkedRunDirectory
  a flake was WRONG. P30's own card cites CI run 35473210732 where that
  test AND TestBlobTamperedObjectFailsPublishOverExisting both accepted
  what they should have refused (a symlink-defense bypass and a
  tamper-detection bypass) -- real, documented security defects from the
  original P00 audit, not noise. Local reproduction passing clean was not
  evidence of a flake. Corrected in docs/lore.md with a visible
  correction (kept the wrong original text so the mistake is legible).
  P07 and P08 (already landed) each retried past an internal/platform
  failure once without investigating whether it was this same defect --
  worth re-checking once P30 lands and fixes it for real.
  Dispatched wave 3's last 5 cards as parallel subagents (coding only,
  NOT the commit step): P12 internal/effects (agent aba10c50ef448185c),
  P26 internal/memory (agent a3912f5e25d6d8f1d), P29 internal/storage
  (agent a2a045e8a64018112), P30 internal/platform (agent
  aded361f03e758072, explicitly briefed on the corrected security-defect
  context above), P34 internal/evidence (agent a59ea35c67b7075c9). Each
  told to proactively check for the schemaDefs staleness pattern and fix
  it within its own package's scope, then report ready and WAIT for the
  lead to clear it by name before running any real (hook-gated) commit --
  the lead lands each one serially, same discipline as the first 12.
- 2026-09-20 13:45-14:00 PT -- P34 LANDED (PR #16, 32bbd6d), the first of
  the last 5 wave-3 cards, and the coding-only-subagent-plus-serialized-
  lead-commit pattern worked exactly as designed: real hook-gated commit
  under its own lease, no concurrency, clean report, lead verified scope
  before merging (not just trusted the report). event.list's cursor now
  binds principal_id and is always minted even on a drained page (was
  nil, meaning a client that fully drained the backlog had no way to
  resume). Confirmed via a real red->green check (stashed the fix,
  reran the new tests, confirmed genuine failures) that this was a real
  gap while the other two required behaviors (turn/proposal/etc.
  linkage, job completion links) were already correctly implemented --
  landed as proof tests, not invented fixes. internal/evidence's own
  schemaDefs already matched AGENTS.md, confirmed not stale. Notably,
  this commit's full-module hook run hit internal/platform cleanly (no
  P30 defect reproduction this time) -- consistent with the corrected
  lore.md entry that it's a real, intermittent defect, not a flake:
  sometimes it reproduces, sometimes it doesn't, which is itself
  evidence for "real bug with specific trigger conditions" over "random
  noise."
  P26 LANDED (PR #17, 11cb0b6). Fixed both wireDefs AND adapterDefs
  staleness (this package has two separate embedded-schema consts, more
  than the single-const pattern seen elsewhere). Restoring the correct
  adapterDefs surfaced a real production bug: `serenityPhysicalCall.
  RequestContext` was typed as a bare ArtifactRef instead of the frozen
  ArtifactLocator oneOf -- this resolves the internal/memory instance of
  a gap tracked since 2026-09-14 (retype PhysicalCallEvidence.
  request_context to ArtifactLocator across every adapter). Implemented
  memory.list from scratch (previously 0% -- no DTO/descriptor/handler).
  Found a second frozen-catalog drift matching messaging's precedent
  (memory.list's scope_required key missing from operations.json,
  matched rather than invented around). P27 (Serenity) dependency
  correctly deferred with evidence, not guessed. CAUGHT DURING LANDING:
  the agent's own commit was based on a stale main (predated P34 and
  several docs commits) -- rebased before pushing so the PR didn't
  appear to revert P34's evidence work. Also independently ran
  `go test ./internal/application/...` (the assembly test the agent
  couldn't run without a lease) myself before merging: still failing,
  but ONLY on `_skills.activate` -- confirms P26 itself doesn't block
  assembly, consistent with the "long tail, not quick cleanup" honest
  assessment from the earlier entry.
  P12 (internal/effects) and P30 (internal/platform) both reported ready
  while P26 was landing. P12: fixed wireDefs staleness, implemented
  callback-route persistence/validation, replaced operation.reconcile's
  stalled-job pattern with a real bounded reconciliation read, fair
  pending-scan fairness fix (105 blocked ops no longer starve a ready
  one). Flagged a real forward-looking note for P16/P23: Operation.attempts
  on the wire doesn't expose attempt Kind, so a future dispatch loop
  needs its own bookkeeping to know an attempt came from reconciliation.
  P30: root-caused BOTH named security failures from CI run 35473210732
  with rigor -- TestListenPrivateRefusesSymlinkedRunDirectory was a REAL
  bug (EvalSymlinks applied to the run directory itself, not just its
  ancestors, silently following a planted symlink; local pass was
  coincidental, from t.TempDir()'s path length tripping an unrelated
  guard first) and fixed correctly (resolve only in dir's parent).
  TestBlobTamperedObjectFailsPublishOverExisting was NOT a real crypto
  defect (confirmed via a 3000-iteration mechanistic check: 12 byte-
  already-equals-0x11 coincidences exactly matched 12 false-accepts) --
  fixed the fixture (XOR-flip instead of fixed-byte overwrite) and
  hardened two sibling tests with the same latent flaw. Also implemented
  the card's backup/restore blob-key primitives, flagged the same
  Restorable-style seam question for P31 that P29 flagged. Both queued
  behind P29, which is committing now (needed a rebase first -- its
  worktree also predated P26/P34).
- 2026-09-20 14:00-14:23 PT -- P29 LANDED (PR #18, 2a634f6). Restorable
  interface (embeds contract.Database, satisfied by Open's return value)
  as a lower-level seam for P31/installation to compose; documented at
  length in restore.go so P31 doesn't have to re-derive the reasoning.
  Crash-safe journaled atomic swap with generation fencing, 15 new tests,
  two real red->green spot checks. Confirmed foundation-tier: no
  schemaDefs section, nothing to fix there.
  Landing had a cross-talk episode worth naming: the agent rebased and
  was waiting out ambient load (24-49, mostly this session's own
  concurrent verification work) before pushing, exactly per the
  load-caution lesson -- but the lead had, in parallel, already pulled
  its rebased commit directly and landed it as PR #18. No harm (the
  agent's next step would have been a no-op push to an already-merged
  branch), and the agent correctly flagged afterward that it could not
  independently re-verify the merge once its worktree was torn down,
  rather than asserting confirmation it didn't have. Lesson: when the
  lead takes over a landing mid-flight, message the lane immediately so
  it doesn't keep polling for a step that's already done.
  Wrote /sitrep's first report for this effort while P29 was in flight
  (docs/sitrep/2026-09-20.md) -- caught and fixed a real accuracy issue
  in the process: internal/memory was still on expected-red.txt despite
  P26 landing it fully green, the same "forgot to remove own line"
  category as P03/P05. Fixed before the report went out.
  P30 cleared next (rebase first, then commit) -- its security fix gets
  a careful manual diff review before merging, not just a report-trust.
- 2026-09-20 14:23-14:32 PT -- P30 LANDED (PR #19, 4645b41), the security
  fix, after a full manual diff review (not just trusting the agent's
  report): read socket.go's actual fix line by line -- confirmed
  EvalSymlinks now resolves only dir's parent, never dir itself, so
  mkdirPrivate's os.Lstat+ModeSymlink check genuinely sees the real leaf
  path; confirmed mkdirPrivate's check is real (files.go:36-65); read the
  three tamper-test fixture changes and confirmed the security assertion
  itself (wantCode expects the fault) is unchanged, the fix only makes
  the tamper reliably real (XOR-flip vs fixed-byte overwrite) instead of
  a 1/256 coin-flip, and one test gained a STRONGER assertion (object not
  consumed by a refused republish); confirmed blob.go/blobformat.go's new
  code (inventory, decryptChunksInto refactor, decryptForeignFile) reuses
  the existing audited verification path byte-for-byte rather than a
  parallel crypto implementation. Then ran both named tests 5x each
  myself -- clean. docs/lore.md's symlink-defect entry updated (not just
  appended) to record the fix, superseding the earlier correction entry.
  THIS COMPLETES WAVE 3 -- all 15 cards (P03, P05, P06, P07, P08, P09,
  P10, P11, P12, P13, P26, P29, P30, P34, P37) now landed, pending only
  P12's own commit (cleared, in flight).
- 2026-09-20 14:42 PT -- WAVE 3 COMPLETE. P12 LANDED (PR #20, ddb27a6) --
  callback-route persistence/validation, operation.reconcile rewritten
  from a stalled unconsumed job into a real bounded reconciliation read,
  fair pending-scan fix (105 blocked ops no longer starve a ready one).
  All 15 of wave 3's cards are now on main. Re-ran
  TestRealRegistryAssemblesLandedModules: confirmed the sole remaining
  assembly blocker is `_skills.activate` (internal/skills, owned by P19,
  wave 8, not dispatchable yet -- depends on P18, itself deep in
  undispatched M1 core work). This matches the honest prediction from
  the 2026-09-20 13:00ish entry exactly -- no surprises. cmd/zatiti,
  internal/application, tests/integration, tests/qualification stay on
  expected-red.txt until that chain clears; every OTHER package that was
  ever on the list is now off it.
  Opening wave 4 next (P04, P14, P27, P35, P38, P41 per the wave table)
  -- verifying actual dependencies before dispatching each rather than
  assuming wave completion implies readiness.
- 2026-09-20 14:45 PT -- WAVE 4 OPENED. Verified all six cards' actual
  depends_on in plan.json (not just wave-table membership) before
  dispatching: P04 needs P03,P02 (landed); P14 needs
  P03,P05,P08,P10,P11,P02 (all landed); P27 needs P26,P02 (landed); P35
  needs P07,P12,P02 (landed); P38 needs P37,P34,P02 (landed); P41 needs
  P37,P02 (landed). All six genuinely ready, all six independent roots
  (no two touch the same package), dispatched together as parallel
  coding-only subagents: P04 internal/application (agent
  a059bb99a65d5d725), P14 internal/execution (agent aa37ce56f3f7175ce --
  THE critical-path card: "the unavoidable serialized critical path (P14
  -> P15 -> P16 -> P18 -> P20 -> P21)" per the plan's own README, and the
  actual start of the durable worker loop the original audit found
  missing entirely -- given briefed extra reading time (messaging's and
  tasks' landed seams it calls into) since a shortcut here poisons five
  more cards), P27 internal/adapters/serenity (agent a0336131f61d190df --
  briefed clearly that success here likely means a precise, evidenced
  "stays refused" handoff, not a working integration, given the upstream
  protocol limitation P26 already confirmed), P35 internal/reviews
  (agent ab70b563b11b02009), P38 internal/client (agent
  a4775bea738d3e29e), P41 internal/server (agent a8c8e6269294626c9). Same
  discipline as wave 3's last batch: report ready, wait for lead
  clearance, proactive schemaDefs check, rebase before commit given main
  will keep moving.
- 2026-09-20 15:05 PT -- P27 DONE, no commit needed. internal/adapters/
  serenity at current main already fully satisfies the card: re-verified
  independently (own go test run clean, plus the agent's own build/vet/
  test/-race/schemaDefs-diff/caller-compatibility checks against P26's
  landed DTOs). Confirmed via PROTOCOL.md's own gap table: the pinned
  Serenity commit's 3-request MCP handshake can't be accounted as one
  physical call under the frozen one-Invoke-one-call rule, so ALL 6
  action kinds (recall/remember/inspect/promote/retract/export_revision)
  stay correctly refused -- no partial credit exists at this pin, and
  the card explicitly allows "a precise upstream blocker per required
  capability" as a valid outcome, which is what this is. Every refusal
  is capability_unsupported, non-retryable, proven zero-physical-call by
  probes in every test; a misconfigured profile claiming an unsupported
  capability is independently rejected (13 overclaim test cases). The
  one real gap (no live qualification against an actual pinned Serenity
  host) is an environment/credential blocker already logged since
  2026-09-10, not something this package can close. Claim released,
  worktree already self-cleaned (nothing to commit).
- 2026-09-20 14:55-15:00 PT -- P35 and P38 both reported ready. Both
  found the same pattern P13/P27 hit: their packages already carried
  substantial pre-existing revision-2/3-aligned work from before this
  session's dispatch (reviews' b1516e2 review-deadlock bugfix; client's
  d1e6774/ef85ddf scaffolding), so the real gap was narrower than the
  card's full step list suggested. P35 (internal/reviews) closed two
  genuine gaps: a missing P00-015 acceptance test for the exact mixed
  eligible-list shape AGENTS.md names, and turn-wake correlation solved
  by enriching the already-atomic `reviews.review.decided` event with
  action_digest+scope rather than inventing an outgoing call to
  execution that doesn't exist yet (P14 isn't landed). P38
  (internal/client) added bounded Lookup/Poll reconnect helpers (16 new
  tests) and flagged, not invented around, a real ambiguity: the frozen
  contract has no explicit server/protocol-version wire concept, so
  "version negotiation" is satisfied structurally by the client's
  existing opaque-payload design rather than a new mechanism -- worth a
  second look but not blocking. Both confirmed no schemaDefs staleness
  (client owns no operation-catalog $defs at all, confirmed not
  assumed). P35 cleared first (was ready first); P38 queued right behind.
- 2026-09-20 15:00-15:05 PT -- P35 LANDED (PR #21, d53fa1c). Landing had
  a real anti-pattern recurrence worth naming precisely: the agent's
  commit actually SUCCEEDED (1e81e9e, correctly rebased onto current
  main), but it then started its OWN background poll loop ("checks
  every 15s, up to 10 minutes, for both the lease to free up and load to
  drop below 10") for a step that was already done -- the exact
  self-poll waste this session corrected earlier, recurring because the
  agent didn't recognize its own commit had already landed. Caught by
  checking the worktree directly rather than trusting the "still
  waiting" framing, same discipline as every other ambiguous report this
  session. Told it to kill the loop, took over push+PR myself (branch
  was still the harness-default name, never renamed -- pushed under the
  correct remote name directly rather than failing on that).
  P38 cleared next.
- 2026-09-20 15:05-15:13 PT -- P38 LANDED (PR #22, f6605bc), executed
  cleanly end to end by its own agent (good example of the discipline
  working correctly: rebase refused on a dirty zero-commits-ahead tree,
  agent correctly substituted the equivalent non-destructive
  `git merge --ff-only` rather than forcing or guessing; waited for its
  own genuine background commit notification instead of self-polling).
  P41 (internal/server) and P04 (internal/application) both also
  reported ready in the meantime. P41 found its package's production
  code already substantially complete and added only proof tests, same
  pattern as P13/P27/P35. P04 is different -- it DID add real new
  production code (worker.go, a genuine ExecuteWorker implementation),
  correcting an earlier mischaracterization in this same entry. P41
  cleared next; P04 queued behind it. P14 (critical path) still working.
- 2026-09-20 15:13-15:19 PT -- P41 LANDED (PR #23, b51ccd9), test-only
  (569 insertions across admission_test.go/envelope_status_test.go/
  fakes_test.go, 3 files, zero production changes) -- confirmed via own
  `go test ./internal/server` run before merging. Clean execution
  end-to-end by its own agent again, including correctly falling back to
  `git merge --ff-only` when rebase refused on its dirty zero-commits
  tree, same pattern P38 established. P04 cleared next.
- 2026-09-20 15:19-15:30 PT -- P04 LANDED (PR #24, 3a1b596), after a full
  manual security review (read the actual worker.go diff, not just the
  report -- this implements a new authorization boundary,
  contract.WorkerOperator). Confirmed the real check ordering matches
  the card exactly: structural validation -> WorkerID/Scope.WorkerID
  assertion -> installation match -> allowlist+Visibility check (public
  operations only, BEFORE identity is ever consulted) -> version check
  -> scope intersection (scopeWithin, documented as unset-dimension =
  unconstrained, relying on each domain's own authorization for
  anything scope doesn't pin -- a disclosed design choice, not a gap)
  -> actor resolution through identity's revalidateAuthority (never a
  caller-supplied Actor -- WorkerRequest has no Actor field at all) ->
  deterministic submission key (sha256 of turn+proposal id) -> delegates
  to the existing, already-proven a.Invoke path. No new transaction
  machinery invented; internal operations categorically excluded via
  the Visibility check regardless of allowlist contents (defense in
  depth). Ran all 7 WorkerOperator tests myself, all pass.
  ALSO FIXED, confirmed independently by both P04 and P41: the
  "Parallel dispatch protocol" section above referenced `with-lease.sh`
  as the canonical build-lease wrapper; it doesn't exist anywhere on
  this machine. Corrected the reference to claim.sh directly (which is
  what every single card in this remediation plan has actually used
  successfully) while preserving the real underlying warning (never
  claim from a monitor/background task).
  WAVE 4'S OTHER 5 CARDS (P04, P27, P35, P38, P41) ARE ALL LANDED. Only
  P14 (critical path) remains to close wave 4 entirely -- still working.
- 2026-09-20 15:41-16:19 PT -- P14 LANDED (PR #25, 5c412ca), after the
  most thorough review of the session given this card's centrality: the
  execution-owned WorkerTurn/ProposalRecord/ContextPlan pipeline
  (`_execution.turn.admit` -> `.work.pending`/`.claim` ->
  `.context.prepare`/`.commit` -> `.proposal.prepare`/`.record` ->
  `.report` -> `.verification.pending`/`.claim`) -- the actual start of
  the durable worker loop the original 2026-09-19 audit found completely
  missing. Independently verified, not just the implementing agent's
  report: all 20 `_execution.*` operation input/output schemas (10 new +
  10 pre-existing, including a `_execution.job.create` fix) diffed
  byte-for-byte against docs/implementation/operations.json -- exact
  match; the `wireDefs` constant diffed byte-for-byte against
  internal/execution/AGENTS.md's embedded $defs -- all 62 definitions
  match; read turn_ops.go and turn_ops_test.go in full and confirmed the
  three required behavioral tests are real, non-vacuous state-transition
  assertions, not fake-implementation tests; traced a scope-check
  refactor in the shared reportAttempt helper and confirmed it is not a
  security regression (narrowAttemptScope already independently enforces
  the same check earlier in both call paths); ran go build/vet/test
  myself rather than trusting the report (121 tests, zero skips, zero
  failures); ran the full go test ./... suite on both main and the P14
  branch and diffed the failing-package sets directly -- P14's branch
  fails exactly main's existing 9 packages minus internal/execution
  itself, zero new failures anywhere in the module.
  PROCESS MISTAKE, caught and fixed same-session: removed
  internal/execution from expected-red.txt in a standalone commit
  (3fbdd37) BEFORE PR #25 had actually merged to main -- main's own
  internal/execution still lacked P14's implementation at that point, so
  this blocked every subsequent commit on main (the pre-commit hook is
  main-checkout-relative, not branch-relative). Caught within one commit
  cycle when the next unrelated commit (an internal/tasks gofmt fix)
  failed the hook; fixed by restoring the entry (b56d82d) until the PR
  actually merged, THEN removing it for real (440cc2c) with a passing
  `go test ./internal/execution` confirmed on main first. See
  docs/lore.md for the standing rule this produced.
  ALSO FOUND while doing this: internal/effects (P12, landed earlier
  this session) was also fully green but had never removed itself from
  expected-red.txt -- the same recurring oversight pattern as P03/P05/
  internal/memory earlier in the session. Removed in the same 440cc2c
  commit after independently confirming `go test ./internal/effects`
  passes.
  ALSO FIXED while landing: a stray gofmt drift in
  internal/tasks/evidence_test.go left over from P11's landing (e418f6e)
  was failing CI's whole-repo static-checks gate for every subsequent
  PR regardless of what it touched (b56d82d).
  WAVE 4 IS NOW COMPLETE -- all 6 cards (P04, P14, P27, P35, P38, P41)
  landed. Next: verify wave 5's actual dependencies (P15, P17, P39, P40)
  via plan.json before dispatching.
- 2026-09-20 16:19 PT -- WAVE 5 DISPATCHED. plan.json's own `status`
  field is stale (still says "planned" even for P14, which had just
  landed), so verified real readiness directly: ran `go test` against
  every dependency package on current main (internal/connections,
  internal/policy, internal/artifacts, internal/adapters/responses,
  internal/registry, internal/client, internal/tasks) -- all green.
  Confirmed with David (AskUserQuestion) to dispatch all 4 ready wave-5
  cards in parallel rather than one at a time or pausing: P15 (build/
  publish the actual model context, owner internal/execution -- the
  direct continuation of P14's turn pipeline), P17 (responsibility
  reasoning and event wakes, owner internal/scheduling), P39 (CLI work
  journeys, owner internal/cli), P40 (MCP work journeys, owner
  internal/mcp). Four independent write-roots, no overlap. All four
  claimed (claim.sh) and dispatched as isolated-worktree agents
  (zatiti_p15/p17/p39/p40), each briefed with its exact card scope, the
  schemaDefs-diff verification method, the one-at-a-time landing
  discipline, and explicit "do not self-commit, do not self-poll"
  instructions given the recurring self-poll anti-pattern documented in
  docs/lore.md.

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
