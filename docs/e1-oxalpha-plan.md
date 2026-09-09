# E1 through the Ox Alpha console

This plan turns epic E1 of `plan.md` (T1.1 to T1.9) into console sessions.
You drive each session by hand at `https://oxalpha.com/chat`; `ox --import`
copies the result into this repository. The console never sees the
repository, so every session starts with pasted context and ends with
complete files.

Source of truth for behavior is `rfc.md`, not the model's memory of an
earlier session. Where the imported scaffold (PR #1) disagrees with the
RFC, the RFC wins and the session replaces the file.

## Session mechanics

One conversation per session. Start a **new chat** for each; `ox --import`
copies every reply in the open conversation, so mixing sessions in one chat
re-imports old files.

1. Build the context paste. From the repository root:

   ```sh
   scripts/ox-context.sh <paths listed under the session> | pbcopy
   ```

   Paste it after the session prompt. Add `docs/rfc.md` only for sessions
   that name it; it is large.
2. Paste the delivery contract below, then the session prompt, then the
   context. Send.
3. When the reply stops with the site's length notice, reply `continue`.
   When the manifest says `complete: false`, reply `continue` again. Stop
   when the manifest says `complete: true`.
4. Import into a fresh branch:

   ```sh
   git switch -c e1/<task-id> main
   (cd ../../dndungu/ox && ./bin/ox --import --out "$OLDPWD")
   GOFLAGS=-mod=mod go build ./... && go vet ./... && \
     go test -timeout 60s ./... && golangci-lint run ./...
   ```

   Pair the extension when the runner prints its token. Files that already
   exist and are not ox-owned are parked under `.ox/conflicts/` instead of
   overwritten; review and move them by hand.
5. If a check fails, reply in the same chat with the failing output and
   `Deliver complete replacement files for every file you change.` Import
   again with the same command; ox re-copies the conversation and rewrites
   only changed files.
6. Commit, open a PR against `main`, and record the task in `roadmap.md`.

## Delivery contract (paste first in every session)

```
You are extending an existing Go project. The repository is not visible to
you; the files pasted below are its current content. Files you do not
receive do not need changes unless you say so explicitly in prose.

Delivery protocol (mandatory):
- Deliver complete file contents, never diffs, ellipses standing for
  omitted code, "(addition)" fragments, or instructions to edit by hand.
  A changed file is resent whole.
- Immediately before each file's fenced code block write exactly:
  OX_FILE: relative/path
- Paths are relative to the repository root. Module path is
  github.com/zatiti/zatiti. Go 1.25. SQLite driver is modernc.org/sqlite
  (pure Go, driver name "sqlite", pragmas via ?_pragma=name(value)).
  No cgo.
- Use fences of at least four backticks; longer if the file contains fences.
- Keep each reply to a manageable batch of complete files. Never truncate
  a file.
- End EVERY reply with the line OX_MANIFEST followed by a fenced JSON
  object: {"files":["every/path/you/have/delivered/or/will/deliver"],
  "complete":false,"next":"remaining work"}. Set complete=true only when
  every file in the list has been delivered whole.
- Tests are table-driven Go tests against real SQLite files in t.TempDir().
  Every test that starts a goroutine or a server must terminate within
  10 seconds with a cancelled context; no test waits on a context nothing
  cancels.
- Do not claim to have run builds or tests. Say what you expect to pass.
- Where the RFC text below and the pasted code disagree, follow the RFC
  and say in one sentence what you replaced.
```

## Session 0: stabilize the imported scaffold

Prerequisite for every task below. The scaffold on `main` after PR #1
builds but has 11 failing tests and 36 lint findings.

Context paste: `go.mod`, `cmd/zatiti`, `internal`, `packaging`.

Prompt:

```
Make this project's own tests and linter pass without adding features.
Known defects, all in the pasted code:
1. internal/transport/cli.go ExtractDataDir builds a FlagSet that defines
   only -data-dir and parses the full argv, so any operation flag such as
   -name fails with "flag provided but not defined". Redesign the
   two-phase parse: strip -data-dir first, then let ParseOpFields define
   the operation's typed flags on its own FlagSet. Seven internal/app
   tests fail on this.
2. internal/transport/mcp_test.go TestToolsListDerivedFromRegistry expects
   organization.list to be the only read-only tool; audit.list is also
   registered. Derive the expectation from the registry instead of a
   literal list.
3. internal/app/parity_test.go TestExitCodes expects
   Run(["capabilities"]) to return exit 5, but no "capabilities" command
   exists. Register a capabilities command that lists the registry's
   implemented operations and returns exit 0, and fix the expectation.
4. internal/state/lease.go: TestLeaseVoidedByGenerationAdvance and
   internal/worker TestRunTaskAbortsOnLeaseLoss fail. A lease row from an
   older generation must be invisible to renewals and takeable by a new
   acquirer; a live same-generation lease must not be takeable. Make the
   code match the tests; the tests state the intended semantics.
5. internal/controller/controller_test.go TestStartAdvancesGeneration hangs
   because Controller.Run blocks on ctx.Done() and the test never cancels.
   Fix the test to cancel; do not change Run's blocking contract.
6. golangci-lint: 33 errcheck findings (unchecked Close/Rollback/Write
   results) and 3 unused symbols (App.runOrgList, osStdin, wire.go app).
   Handle or explicitly discard each error; delete the unused symbols.
Deliver every changed file whole.
```

Done when: `go test -timeout 60s ./...` and `golangci-lint run ./...` are
clean on the branch.

## Session map

| Session | Plan task | Roots touched | Verifies |
|---|---|---|---|
| 1 | T1.1 SQLite ownership and write transactions | `internal/state` | UC-001, UC-015 |
| 2 | T1.2 credential-source adapter | `internal/identity` | UC-001, UC-008 |
| 3 | T1.3 bootstrap and principal authentication | `internal/identity`, `internal/state`, `internal/app` | UC-001 |
| 4 | T1.4 scope intersection policy primitive | `internal/policy` (new) | UC-003, UC-009 |
| 5 | T1.5 operation registry and local API adapter | `internal/operations`, `internal/transport` | UC-002 |
| 6 | T1.6 durable command and event lookup | `internal/state` | UC-002, UC-015 |
| 7 | T1.7 CLI and MCP adapters | `internal/transport`, `cmd/zatiti` | UC-002 |
| 8 | T1.8 controller lifecycle composition | `internal/app`, `internal/controller` | UC-001, UC-002, UC-015 |
| 9 | T1.9 parity and failure suites | `tests/acceptance` (new) | UC-001, UC-002, UC-015 |

Order matters: 1 to 3 are sequential, 4 to 6 can run in any order after 3,
7 needs 5 and 6, 8 needs 3, 4, 6, 7, and 9 needs 8. The dependency on E0
tasks (T0.6 to T0.8) in `plan.md` is waived for this console-driven pass;
record that as a deviation in `roadmap.md` and revisit contracts when E0
runs.

Each session prompt below follows the same shape: the task's acceptance
criteria from `plan.md` verbatim, the RFC sections that bind it, what the
scaffold already has, and what to produce. Paste the RFC section text
named in "RFC" (copy from `docs/rfc.md`) into the prompt; the model does not
have the document.

## Session 1: T1.1 SQLite ownership and write transactions

Context paste: `go.mod`, `internal/state`, `internal/controller`.
RFC: section 3 paragraphs on SQLite, the single ordered write path, and the
event outbox.

```
Task T1.1. Acceptance: second controller cannot acquire installation;
restart increments persisted generation; state and outbox commit or roll
back together; lock loss stops admission; contention returns within a
configured bound.

Existing: Store with flock ownership, generation advance, migrations 1-5,
lease service. Missing: an event outbox that commits in the same
transaction as the state change, a single ordered write path other roots
call instead of opening their own transactions, a bounded busy timeout
that surfaces as a typed retryable error rather than a hang, and lock-loss
detection that flips the store into a refusing state.

Produce in internal/state:
- migration 6: events table (id, seq INTEGER PRIMARY KEY autoincrement,
  kind, subject_id, payload JSON, created_at) as the outbox.
- Write(ctx, func(tx *Tx) error): the ordered write path. Tx wraps *sql.Tx
  and exposes Exec/Query plus Emit(kind, subject, payload) which inserts
  the event in the same transaction. Serialized by a store mutex.
- ErrContention returned when the busy timeout elapses; configurable
  timeout on Open.
- Admission check: every Write re-verifies the lock file descriptor still
  holds the flock; if not, return ErrOwnershipLost and mark the store
  closed for writes.
- Tests: duplicate Open refused; generation increments across two
  Open/Close cycles; a Write whose callback fails leaves no event row; a
  second writer blocked past the timeout gets ErrContention within
  timeout+1s; simulated lock loss (close the lock file) makes the next
  Write return ErrOwnershipLost.
Refactor existing state writers (audit, write.go, principal_write, review,
lease, session) to go through Write; deliver those files whole.
```

## Session 2: T1.2 credential-source adapter

Context paste: `go.mod`, `internal/identity`.
RFC: section 3 "Credentials" table row and section 5 paragraphs 1-2.

```
Task T1.2. Acceptance: secure-store fixture returns credential handles
without exposing bytes in public output; missing key store fails
explicitly; explicitly provisioned headless source works; child process
environment lacks owner credentials.

Existing: file keystore under <data-dir>/keystore with 0600 keys.
Missing: the credential-source abstraction the RFC requires (OS secret
store or explicitly provisioned headless source), handles instead of
bytes, and the guarantee that subprocesses never inherit credentials.

Produce in internal/identity:
- CredentialSource interface: Put(name, secret) (Handle, error),
  Open(Handle) (Secret, error), Delete(Handle) error, Kind() string.
- Handle is an opaque string-like type whose String() and JSON encoding
  never include secret material. Secret exposes Bytes() and a Zero()
  method; its String() and %v formatting print "[redacted]".
- FileSource: the existing keystore reworked behind the interface, with
  the directory required to be mode 0700 and refused otherwise.
- HeadlessSource: reads a master key path from an explicit option (never
  from the environment by default); encrypts stored secrets with
  AES-256-GCM under that key.
- KeychainSource stub is NOT allowed: if macOS Keychain is not implemented
  in this session, expose ErrSourceUnavailable("keychain") from a
  constructor and document it. Do not fake it.
- SubprocessEnv(base []string) []string strips ZATITI_* and any variable
  whose name contains KEY, TOKEN, SECRET or PASSWORD.
- Tests: handle JSON contains no key bytes; opening a handle from a
  missing directory returns a typed error naming the source; headless
  round trip; SubprocessEnv strips a seeded ZATITI_MASTER_KEY and keeps
  PATH.
```

## Session 3: T1.3 bootstrap and principal authentication

Context paste: `go.mod`, `internal/identity`, `internal/state`,
`internal/app`.
RFC: section 5 paragraphs 1-2 and section 8.4 paragraph on submission keys
during bootstrap.

```
Task T1.3. Acceptance: bootstrap succeeds once and refuses
reinitialization; provisioned profile authenticates its principal; profile
name alone grants nothing; revoked credential fails; initialization
transaction creates root organization and chief identity through the
agreed bootstrap port.

Existing: Bootstrap creates an owner principal and root org; challenge
signing exists for MCP sessions; credential.issue binds keys. Missing:
credential profiles, the authentication step every CLI/MCP call performs,
revocation, and the "chief" identity the RFC's root organization carries.

Produce:
- internal/identity/profile.go: Profile{Name, PrincipalID, Handle}
  persisted as <data-dir>/profiles/<name>.json (0600). ListProfiles,
  LoadProfile. The default profile is "owner", written by bootstrap.
- internal/identity/auth.go: Authenticate(ctx, store, profile) resolves
  the principal by signing a controller-issued nonce with the profile's
  key and verifying against the state-bound public key. A profile whose
  principal is revoked returns ErrRevoked. A profile name with no matching
  key returns ErrUnauthenticated.
- internal/state: principals gain status (active|revoked) and
  revoked_at; RevokePrincipal writes through the ordered write path and
  emits an event. Root organization gains a chief_principal_id set to the
  owner at bootstrap in the same transaction.
- internal/app: every command except init and capabilities calls
  Authenticate first; --profile flag selects the profile, default owner.
  Actor is the authenticated principal, never a flag value.
- Tests: second init on the same dir returns exit 4 with a stale/conflict
  error; a profile file with a name but no key fails; revoked principal
  fails with exit 3; bootstrap transaction failure (inject an error after
  the org insert) leaves no principal row and no profile file.
```

## Session 4: T1.4 scope intersection policy primitive

Context paste: `go.mod`, `internal/authz`, `internal/state/lookup.go`,
`internal/state/migrations.go`.
RFC: section 5 paragraphs 3-5 ("An owner can delegate" through "Policy
supports standing").

```
Task T1.4. Acceptance: explicit deny defeats grants; child cannot exceed
any ancestor ceiling; cross-project or organization access without
binding fails; unknown required condition refuses; revocation is checked
from current state.

Existing: internal/authz has a role-capability table with owner-only
overrides. The plan's ownership contract places this behavior in
internal/policy. Create internal/policy and make internal/authz a thin
caller of it or delete authz and update its callers; say which.

Produce in internal/policy:
- Grant{PrincipalID, Scope (org or project id), Capabilities []string,
  Ceiling{Tools []string, Destinations []string, Deadline, Budget}} and
  Denial{PrincipalID, Scope, Capability}. Migration 7 stores both.
- Decide(ctx, tx, Request{Principal, Scope chain (project -> org ->
  ancestors -> installation), Capability, Conditions map[string]string})
  returning Decision{Allow bool, Reason string, Ceiling}. The rules, in
  order: any matching Denial denies; the principal must hold a Grant at
  some level of the chain; the effective ceiling is the intersection of
  every ancestor's ceiling and the request cannot exceed it; a Condition
  key the policy does not know refuses with reason "unknown condition";
  grants are read inside the caller's transaction so revocation is seen
  immediately.
- Tests, table-driven, one row per acceptance clause plus: a child grant
  listing a tool absent from the parent ceiling is narrowed, not
  widened; a project-scoped request against an org the principal has no
  binding to is refused even though the principal holds an installation
  grant elsewhere.
```

## Session 5: T1.5 operation registry and local API adapter

Context paste: `go.mod`, `internal/operations`, `internal/transport`,
`internal/app/app.go`, `internal/app/operation.go`.
RFC: section 8.1 and section 8.4 (all of it).

```
Task T1.5. Acceptance: every registered operation declares schemas,
authorization and error semantics; invalid input returns a stable
envelope/status; a real private socket handles authenticated requests;
unknown operation refuses.

Existing: a registry with Op{Name, Mutability, Fields}. Missing: JSON
Schemas, the versioned request/result envelope, stable error codes, and
the Unix-domain-socket HTTP API that the CLI and MCP adapters must call
instead of touching the store.

Produce:
- internal/operations: Descriptor{ID, Version, Effect (read|mutate|
  destructive), Scope requirement, Input and Output JSON Schema (as
  map[string]any built from typed Go structs, no third-party schema
  library), Idempotency (none|submission_key), Handler}. Registry keeps
  Freeze; generation rejects an ID whose CLI form (dots to spaces) or
  MCP form (zatiti_ prefix, dots to underscores) collides with another.
- internal/transport/envelope.go: Request{Schema "zatiti.request/v1",
  SubmissionKey, Input json.RawMessage} and Result{Schema
  "zatiti.result/v1", CommandID, Status completed|accepted|failed, Data,
  Error *{Code, Message}, NextCursor}. Error codes exactly:
  invalid_input, not_found, permission_denied, stale_version,
  review_required, prerequisite_missing, external_action_required,
  budget_unavailable, capability_unsupported, controller_unavailable,
  outcome_unknown. ExitCode(Result) maps per RFC 8.4.
- internal/transport/api.go: HTTP server on a Unix socket at
  <data-dir>/controller.sock (0600) with POST /v1/ops/<id>; the request
  carries the profile's signed nonce in an Authorization header; the
  server validates input against the descriptor schema before the handler
  runs. Unknown operation returns not_found with HTTP 404.
- internal/transport/client.go: Client that dials the socket and returns
  Result.
- Tests: schema rejection yields invalid_input and exit 2 without invoking
  the handler; unknown op is refused; a request over the real socket with
  a valid credential succeeds and with a bad signature returns
  permission_denied; the name-collision check fires.
```

## Session 6: T1.6 durable command and event lookup

Context paste: `go.mod`, `internal/state`, `internal/transport/envelope.go`.
RFC: section 8.4 paragraphs on submission keys, pagination and cursors.

```
Task T1.6. Acceptance: same principal/key/request returns the original
command; changed request with the same key refuses; lost acknowledgment
is recovered by lookup; expired cursor requires a snapshot rather than
silently skipping events.

Produce in internal/state:
- migration 8: commands(id, principal_id, op_id, op_version,
  submission_key, request_hash, status, result JSON, created_at,
  completed_at, UNIQUE(principal_id, submission_key)).
- BeginCommand(tx, principal, op, version, key, canonicalRequest) which
  returns the existing row when key and hash match (status and stored
  result included), ErrSubmissionMismatch when the key exists with a
  different hash, and inserts a pending row otherwise. CompleteCommand
  stores the result in the same transaction as the handler's writes.
- Canonicalize(json) producing sorted-key compact JSON before hashing.
- Events: ListEvents(after cursor, limit) where the cursor is an opaque
  base64 of the last seq plus a retention epoch; a cursor older than the
  retention floor returns ErrCursorExpired. PruneEvents(before seq)
  raises the floor. Retention of commands is 30 days minimum; a prune
  helper enforces it and tests prove a 29-day-old key is still found.
- Tests for every acceptance clause, plus: two concurrent BeginCommand
  calls with the same key produce exactly one row.
```

## Session 7: T1.7 CLI and MCP adapters

Context paste: `go.mod`, `cmd/zatiti`, `internal/transport`,
`internal/app`, `internal/operations`.
RFC: sections 8.2 and 8.3 in full, plus the naming paragraph in 8.1.

```
Task T1.7. Acceptance: a real CLI subprocess and an MCP client discover
the same implemented operations; JSON envelopes and documented CLI exits
match; tool arguments cannot override the credential profile; the MCP
process does not become a database writer.

Existing: a hand-rolled CLI and a hand-rolled JSON-RPC MCP server that
both open the store directly. Replace both with clients of the socket API
from T1.5. Use the standard library only for the CLI, generated from the
registry (RFC names Cobra as the proposal; keep dependencies at zero for
now and say so in a comment).

Produce:
- cmd/zatiti/main.go stays thin. internal/transport/cli: for each
  descriptor, command path is the op ID split on dots (organization
  create); accepts --input @file, --input -, or --input '<json>', plus
  convenience flags derived from the input schema's top-level string,
  int and bool properties; --json prints exactly one Result on stdout;
  diagnostics on stderr; never prompts; --profile selects the credential
  profile; --data-dir locates the installation; capabilities is the
  explicit shortcut. Exit code from ExitCode(Result).
- internal/transport/mcp: stdio JSON-RPC 2.0 for protocol revision
  2025-11-25 using the standard library only; tools/list derives name
  zatiti_<id with underscores>, inputSchema and outputSchema from the
  registry; tools/call returns the Result as structuredContent and as
  text; domain failures return isError:true; malformed frames return
  JSON-RPC errors; stdout carries frames only; the server selects its
  profile from its own --profile argument at start and ignores any
  profile field in tool arguments; a missing controller socket returns
  the controller_unavailable code, it never opens the database.
- Tests: an exec'd zatiti subprocess and an in-process MCP client over
  pipes list the same operation set; for organization.create both return
  a Result whose status, data keys and error code match; a tool call with
  {"profile":"owner"} in arguments while the server runs as a lesser
  profile is denied; the MCP binary run with no socket present exits 6.
```

## Session 8: T1.8 controller lifecycle composition

Context paste: `go.mod`, `internal/app`, `internal/controller`,
`internal/transport/api.go`, `internal/state/store.go`.
RFC: section 3 paragraphs 1-2 and section 5 paragraph 1.

```
Task T1.8. Acceptance: a real bootstrap/authentication/capabilities
journey works against one controller; a service mutation and its
command/outbox records share the transaction; shutdown joins owned work;
restart preserves identity and commands.

Produce:
- internal/app/serve.go: zatiti serve opens the store, advances the
  generation, starts the socket API, starts the worker runtime, and on
  SIGINT/SIGTERM stops accepting requests, waits for in-flight handlers
  and workers with a bounded deadline, then closes the store and removes
  the socket. zatiti init runs bootstrap and exits; zatiti mcp serve
  --bootstrap runs the same initializer then serves.
- Every mutating handler runs inside one state.Write: BeginCommand, the
  domain change, Emit event, CompleteCommand.
- Tests (in internal/app, using real subprocesses where the acceptance
  says real): init, then serve in the background, then capabilities and
  organization create over the socket, then SIGTERM; assert the socket
  file is gone, the command row and event row exist, and after a second
  serve the principal, the command by submission key and the generation
  (incremented by one) are all readable.
```

## Session 9: T1.9 parity and failure suites

Context paste: `go.mod`, `internal/transport`, `internal/app/serve.go`,
`tests` (if present).
RFC: section 12 rows Z01, Z02, Z16 and the paragraph after the table on
parity normalization.

```
Task T1.9. Acceptance: named subprocess suites execute success,
bootstrap denial, revoked identity, duplicate controller and lost-ack
cases in both transports; normalization preserves authorization, outcome
and version differences; scoped lint and format checks pass on the
integrated revision.

Produce tests/acceptance as its own package that imports nothing from
internal/ except through exec'd binaries and the MCP stdio protocol:
- A fixture that builds the zatiti binary once per test run into
  t.TempDir(), inits an installation, and starts serve.
- Cases, each run once through the CLI subprocess and once through an MCP
  client over the server's stdio, then compared after normalizing only
  generated IDs and timestamps via an explicit mapping: success
  (organization create), bootstrap denial (second init), revoked identity
  (revoke then call), duplicate controller (second serve on the same
  dir), lost acknowledgement (kill the client after send, re-send with
  the same submission key, expect the original command back).
- The comparison fails if status, error code, resource version or the
  set of emitted event kinds differ between transports.
- A Makefile target `make acceptance` and a `make check` that runs
  gofmt -l, go vet, golangci-lint and go test.
```

## After session 9

E1 is integrated when `make check` and `make acceptance` pass on `main`,
`roadmap.md` lists T1.1 to T1.9 under Shipped with PR numbers, and the E0
deviation is recorded. Release qualification is a separate E7 verdict; do
not mark Z01, Z02 or Z16 as passed from these suites alone.
