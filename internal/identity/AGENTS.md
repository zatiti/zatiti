# Implementation assignment: `internal/identity`

Generated specification revision 3; source digest `874fcc1635dd70b0be97691b99c28aeab5f2d7630645e9ba1959f2cd1eb05173`. This file is committed implementation context. Do not independently edit it. Everything required from the product specification and adjacent interfaces is embedded below; no RFC copy is required.

## Mission and scope

Own principals, grants, authentication metadata, credential references and immediate revocation.

Write scope: **`internal/identity/` only**, excluding this generated AGENTS.md. Go package name: `identity`. Ownership kind: domain; integration wave: 1.

Allowed production imports from this repository: `github.com/zatiti/zatiti/internal/contract`. Tests may use interfaces/fakes defined locally and, once available, storage-backed temporary fixtures; integration owns cross-package tests. No sibling raw SQL. No root dependency edits except the integration exception stated in its own brief.

## Implementation decisions and acceptance focus

Own identity_principals, identity_grants, identity_credentials and immutable revocation records. Hash authentication tokens with a suitable one-way token verifier; raw bytes reside only in platform custody. Runtime principal/grant administration is explicitly authorized under current authority; cannot smuggle a permission expansion through principal.update or revoked credential reattachment. Principal scope and delegated grants can only fit the delegator's ceiling. Separate agent/client/worker/service/human kinds. Same-user OS access is installation trust boundary, never a claim of tenant isolation. Provision accepts only a helper-verified opaque reference for the requested principal. New credentials cannot be returned as model-visible bytes. Authentication checks revocation and expiry on every request; physical claims recheck current state. Retain revocations through backup rewind. Internal bootstrap is callable only from installation under exclusive uninitialized mode; internal promote accepts deterministic qualification bound to prior rule/ceiling and cannot approve evidence itself. Revision 3: _identity.bootstrap accepts optional service_credential_id/service_store_ref so the bootstrap-created controller service principal can receive a credential in the same transaction (an out-of-process controller needs one to authenticate; the in-process explicit-Actor seam does not and may omit them). _identity.authority is gated exactly like every internal operation, by its caller allowlist plus the calling actor being a registered unrevoked principal in the transaction's installation -- never by requiring that actor to already hold the capability named in its own request; do not reintroduce a circular subject-capability check or a standing grant to work around one.

Local proving focus: Revoked/expired credential, forged profile/username/human kind, cross-scope principal reference, grant widening, bootstrap replay, credential secrecy and revoked restore.

## Incoming and outgoing boundaries

Incoming callers: application, configuration, controller, effects, execution, installation, policy, reviews, application (authenticated public operations).

Outgoing owner calls: none; use only declared Go dependency interfaces. Each exact input/output schema appears below. Calls retain current Unit/authority; owner allowlists are mandatory.

Expose `New(contract.Dependencies) (*Service,error)`; `*Service` implements `contract.Module` with Name `identity`, owner-prefixed migrations, all owned descriptors, and strict dispatch. No calls/goroutines during construction. Implement optional authentication/LocalIO interfaces where specified in the common contract. Tables are private under `identity_`; external callers rely only on methods and schemas.

### Imported package APIs and behavior

These briefs are embedded so you need not read a sibling prompt to discover its incoming API. Implement only your own package.

## Shared foundation contract

# Frozen implementation contract, revision 3

These decisions complete the product specification and bind every scope. Report contradictions with an affected-dependency list and proposed coordinated revision; do not change another owner's interface locally.

Revision 3 changes, each a coordinated revision that code written against revision 2 must follow. Every new field named below is additive and optional on an existing type: no existing required field changed type, name or was removed, so old persisted rows validate unchanged and remain inspectable with the new field simply absent. New tables (`WorkerTurn`, proposal records) and new operations have no prior callers to break.

- **Durable worker turn.** Execution owns a persisted `WorkerTurn` (see the embedded `WorkerTurn`/`TurnSource` schemas in `internal/execution`'s generated prompt) keyed by unique `(installation_id, source_kind, source_id, source_version, recipient_worker_id)`; re-admission through `_execution.turn.admit` returns the same turn. A pending inbox message and its turn admission/processed marker (`_messaging.processed`) commit in one shared transaction. A message arriving during an active turn is admitted as a safe-boundary injection durably linked to that turn, never silently dropped or processed twice. One active decision stream exists per worker/task lane; different eligible workers may run concurrently within aggregate limits. Each model step/proposal is stored separately as a `ProposalRecord` keyed by unique `(turn_id, step_index, proposal_id)`; the same key with different bytes is `submission_conflict`.
- **Worker turn pipeline operations.** `_execution.work.pending`/`.claim`, `_execution.context.prepare`/`.commit`, `_execution.proposal.prepare`/`.record`, `_execution.report`, `_execution.verification.pending`/`.claim`, `_tasks.evidence.record`, `_tasks.dependencies.wake`, `_messaging.ready`/`.processed` are new internal operations driving the durable worker loop end to end (admit → claim work → prepare/commit context → dispatch a model or tool effect → record the proposal → report → independently verify). Public `task.start` transitions an eligible draft/ready task to ready and enqueues its run in the same transaction; `task.create`/`task.assign` are unchanged. Exact schemas, callers and behavior are embedded in the generated prompts of `internal/execution`, `internal/tasks` and `internal/messaging`.
- **Worker operation executor.** A narrow `contract.WorkerOperator` capability (declared below) lets a worker-authored local proposal invoke an ordinary public operation under the worker's own authenticated actor, scope-intersected with the task/source authorization envelope. It resolves the actor from the persisted turn/worker mapping; `WorkerID` is an asserted match against that turn, never a way to select any principal. Local model-visible operations are an explicit allowlist (authorized configuration authoring/inspection, task/delegation/responsibility actions, approved memory/messaging operations, output publication); grant/policy changes, secrets, internal bookkeeping and human review never become available merely because they exist in the public catalog. The controller's administrative identity authorizes scheduling/bookkeeping only, never a model proposal. **Public/internal caller fix:** `task.assign` is a public operation and carries no caller allowlist (the registry rejects a public descriptor with callers — confirmed 2026-09-18 descriptor-drift finding in docs/roadmap.md); a chief-driven assignment is an ordinary authenticated `task.assign` call made through this executor under the requesting worker's own actor, not a domain-internal caller path.
- **Effects callback routing.** `Operation` gains optional `attempts` (`{attempt_id, generation}` pairs, additive alongside the unchanged `attempt_ids`) and optional `callback_route`; `Dispatch` gains the matching optional `callback_route`. `_effects.prepare` accepts an optional `callback_route` (`CallbackRoute`: `worker_turn`/`job`/`memory`/`skill`/`connection` plus the relevant turn/step/job id) persisted alongside the action and returned at claim. The controller resolves callback routing from this persisted route; it never infers routing by inserting an undeclared `attempt_id` into strict adapter parameters (fixes audit finding G05). `_effects.record` gains an optional `current_generation` for the successor-generation rule: a stray attempt is `not_sent` if never actually claimed under its recorded generation, `outcome_unknown` if claimed but unconfirmed — never silently dropped.
- **Effects reconciliation reaches `Adapter.Reconcile`.** New `_effects.reconciliation.prepare`/`.record` create a separately admitted, separately authorized and accounted bounded reconciliation read distinct from the original write, merging the qualified observation into the original operation's uncertainty without overwriting history or replaying the original action (fixes audit finding G13).
- **Job registry linkage.** `_execution.job.create` gains an optional `operation_id`, linking a network-backed job to its originating effects `Operation` at creation so reconciliation resolves it without scanning another owner's table; a job without `operation_id` is an ordinary local runner. New `_skills.evaluation.record` and `_configuration.export.prepare`/`.record` route skill evaluation and configuration export/import through this durable job ledger instead of handler-local ID minting with no owner-backed lookup (fixes audit findings G11, G12).
- **Registry lookup and schema normalization (already implemented; confirmed for revision 3).** `Lookup(id, 0)` resolves the highest registered version for that operation id. The registry accepts either a bare `$defs`-relative schema or a fully self-contained document at registration, and `Lookup` always returns a self-contained document pruned to reachable `$defs`. These two rules were implemented and tested against application's assumptions before this revision; this entry is their authored record (docs/roadmap.md, 2026-09-18).
- **CLI root tokens (clarification, no schema change).** `Descriptor.CLI` is the subcommand path only and excludes the binary name; `cmd/zatiti` prepends `zatiti` once at command-tree assembly. Generated `AGENTS.md` prose shows the full invocation (for example "CLI `zatiti artifact export`") for readability — that rendering convenience is not part of the wire contract, and an implementer must not treat the printed binary name as part of `Descriptor.CLI` (this ambiguity produced a wrong descriptor token shape in 11 modules; docs/roadmap.md, 2026-09-18).
- **Service-principal bootstrap.** `_identity.bootstrap` gains optional `service_credential_id`/`service_store_ref` so the bootstrap-created controller service principal can receive a credential in the same transaction, letting an out-of-process controller authenticate; omitted fields leave the service principal credential-less for the in-process explicit-`Actor` seam, unchanged from revision 2.
- **Review eligibility (ruling, confirmed for revision 3).** The eligible reviewer of an exact review-class request is the human principal whose current authority admitted that request; services, workers and agents are never eligible, and proposer separation stays mandatory for them. This settles "an eligible owner's decision" left undefined in revision 2's policy/reviews briefs (docs/roadmap.md, 2026-09-18 evening REVISION-3 RULING).
- **Internal authority checks (ruling, confirmed for revision 3).** `_identity.authority`, like every internal operation, is gated by its caller allowlist and by the calling actor being a registered, unrevoked principal in the transaction's installation — never by requiring the calling actor to independently hold the capability named in its own request. A standing grant of `_identity.authority` to work around a circular subject-capability check is unnecessary and must not be relied upon (docs/roadmap.md, 2026-09-19 REVISION-3 RULING). Whether an actor may read another principal's authority is an explicit code-level decision this ruling does not settle; downstream implementers must document it in place rather than infer it.
- **Artifact locator staging (confirmed unchanged).** `PhysicalCallEvidence.request_context` and `ModelOutput.request_context` remain the revision-2 `ArtifactLocator` retype; revision 3 makes no further change here and extends the same staged/artifact pattern to `_execution.context.commit`'s `staged_context`.
- **Responses `prepare_session`/`model_step` split.** See "OpenAI Responses session preparation" below.
- **Small query/result additions.** `conversation.get`/`.list` return the calling principal's optional `caller_unread_count`/`caller_last_read_marker`; new public `conversation.message.list` reads authorized message history for one conversation. New public `memory.list` lists authorized scoped claim refs (source/freshness/lineage/supported actions) without performing paid retrieval. `Artifact` gains optional `source_operation_id`/`purpose` (provenance). `Status` gains optional `runtime_ready`. `Responsibility` gains optional `last_cycle_id`, and `_scheduling.cycle.record` gains a required `cycle_id` replay/conflict fence plus optional `turn_id` linkage. New public `installation.verifier.list` enumerates installed trusted verifier profiles (secret-free) so a task's acceptance contract can name one. `event.list`'s drained-cursor/filter/scope/principal/retention behavior is unchanged; this is its explicit revision-3 confirmation, not a schema change. Public task/turn status projection and installed-capability desktop surfacing are explicitly deferred to `internal/evidence` (P34) and `apps/desktop` (P42) rather than invented here.
- **Local decision tools pinned separately from provider tools.** `ReplyProposal`, `ClarifyProposal`, `ReportOutputsProposal` and `CycleDecisionProposal` (schemas below) are the sealed names/inputs for the context builder's non-provider decision tools (`reply`, `clarify`, `report_outputs`, `cycle_decision`). They are execution-local proposals evaluated by the worker operation executor, never routed through a `connections.Tool`/adapter and never given a provider operation mapping; a final text answer alone may complete a chat turn but never implicitly fulfills a task's required outputs.
- Desktop client (ADR 002): the desktop is a Flutter application at `apps/desktop` that is a direct wire client of internal/server. The Go roots `internal/desktop` and `cmd/zatiti-desktop` are retired; no Go package may import or stand in for the desktop.
- Adapter request context: `PhysicalCallEvidence.request_context` and `ModelOutput.request_context` are an `ArtifactLocator`, not a bare `ArtifactRef`. Adapters return the staged variant; the controller publishes it and substitutes the artifact variant in normalized evidence.
- **MCP client connection adapter (coordinated revision 2026-09-24, dec-1256).** New ownership root `internal/adapters/mcpclient` (adapter name `mcp`, provider `mcp`), the fifth adapter beside responses/github/httpread/serenity, with frozen `MCPClientProfile`/`MCPClientParameters`/`MCPClientEvidence` schemas in `docs/implementation/adapter-schemas.json`; new public `connection.discover` and `connection.tools` and internal `_connections.discovery.record` on `internal/connections`, plus the `MCPDiscoveredTool` DTO. Additive: no existing type, operation or table changes. Section below.
- Installation database seam: `contract.DatabaseBackup` is a one-method capability that entrypoint assembly supplies only to internal/installation through `installation.WithDatabaseBackup`. `contract.Dependencies` is unchanged and no domain gains general database access.

## Build and dependency decisions

Module `github.com/zatiti/zatiti`; language `go 1.26.0`; initial toolchain `go1.26.2`. Integration owns go.mod/go.sum. The Flutter desktop pins its own Flutter/Dart SDK and plugins in `apps/desktop/pubspec.lock`; integration records those pins and licenses in the same lock report and the Go module never depends on them. Domains import standard library and internal/contract, not sibling domains. Application composes owner methods through a checked in-process dispatcher: ordinary Go calls, not another server or scheduler.

Library families: Cobra CLI; official MCP Go SDK, handwritten adapter for MCP `2025-11-25`; Flutter (Dart) desktop application at `apps/desktop` with an operating-system secure-storage plugin, outside the Go module; modernc.org/sqlite. Mint is optional build-time tooling and cannot weaken contracts. Hosted provider v1: a specifically qualified OpenAI Responses endpoint/profile, with configured model and prices. Other compatible endpoints remain unavailable until qualified. GitHub uses documented REST over net/http, with mutation retries disabled. Serenity is a separate pinned service using public interfaces only. Integration must resolve exact dependency releases/commits, checksums, licenses and qualification commands in a lock report BEFORE dispatching dependent implementation. These are selected implementation families, not claims that upstream behavior has been qualified. No agent independently substitutes libraries, models, prices, accounts or Serenity APIs. Dependency resolution/qualification is an explicit foundation assignment.

## Shared Go declarations

The contract owner implements these exact declarations. Omitted function bodies are the implementation assignment, not production stubs. Public JSON uses snake_case. Schema-derived DTO names concatenate operation path segments in PascalCase plus Input/Output and live in the owning package. JSON integer => int64, UUID => contract.ID, timestamp => time.Time, optional scalar => pointer, array => slice, explicitly open JSON => json.RawMessage. Unknown fields are rejected.

```go
package contract
import (
    "context"
    "database/sql"
    "encoding/json"
    "io"
    "net/http"
    "time"
)
type ID string
type Digest string
type Version int64
type Clock interface { Now() time.Time }
type IDSource interface { New() ID }
type Scope struct {
    InstallationID ID `json:"installation_id"`
    OrganizationID ID `json:"organization_id,omitempty"`
    ProjectID ID `json:"project_id,omitempty"`
    WorkerID ID `json:"worker_id,omitempty"`
    TaskID ID `json:"task_id,omitempty"`
}
type Actor struct {
    PrincipalID ID `json:"principal_id"`
    Kind string `json:"kind"` // human | client_agent | worker | service
    CredentialID ID `json:"credential_id"`
}
type Request struct {
    Schema string `json:"schema"` // zatiti.request/v1
    SubmissionKey string `json:"submission_key,omitempty"`
    Input json.RawMessage `json:"input"`
}
type Fault struct {
    Code string `json:"code"`
    Message string `json:"message"`
    Retryable bool `json:"retryable"`
    Details json.RawMessage `json:"details,omitempty"`
}
func (*Fault) Error() string
type Outcome[T any] struct { Status string; Data T; NextCursor *string }
type Payload struct {
    Status string `json:"status"` // completed | accepted | failed
    Data json.RawMessage `json:"data"`
    Error *Fault `json:"error"`
    NextCursor *string `json:"next_cursor"`
}
type Result struct {
    Schema string `json:"schema"` // zatiti.result/v1
    CommandID ID `json:"command_id"`
    Payload
}
type Invocation struct { Operation string; Version int64; Input json.RawMessage }
type Event struct {
    ID ID `json:"id"`
    Sequence int64 `json:"sequence"`
    At time.Time `json:"at"`
    Scope Scope `json:"scope"`
    Kind string `json:"kind"` // owner.entity.transition
    ResourceID ID `json:"resource_id"`
    ResourceVersion Version `json:"resource_version"`
    Data json.RawMessage `json:"data"`
}
type Reader interface {
    QueryContext(context.Context, string, ...any) (*sql.Rows, error)
    QueryRowContext(context.Context, string, ...any) *sql.Row
}
type Unit interface {
    Reader
    ExecContext(context.Context, string, ...any) (sql.Result, error)
    Actor() Actor
    Scope() Scope
    Generation() int64
    ReadOnly() bool
    Emit(context.Context, Event) error
}
type Ownership interface { Held() bool; Lost() <-chan struct{}; Close() error }
type Database interface {
    StartGeneration(context.Context) (int64, error)
    Generation(context.Context) (int64, error)
    Read(context.Context, Actor, Scope, func(Unit) error) error
    Write(context.Context, Actor, Scope, func(Unit) error) error
    Migrate(context.Context, []Migration) error
    Events(context.Context, int64, int) ([]Event, error)
    Backup(context.Context, io.Writer) error
    Close() error
}
type Migration struct { Owner string; Version int64; SQL string; SHA256 Digest }
type Descriptor struct {
    ID string; Version int64; Owner string
    Visibility string // public | internal
    Mode string // query | mutation
    Effect string // local | disclosure | external_read | external_mutation
    InputSchema json.RawMessage; OutputSchema json.RawMessage; CompletionSchema json.RawMessage
    CLI []string; MCP string; ScopeRequired []string; Callers []string
    ExpectedVersion bool; SubmissionKey bool
}
type Handler func(context.Context, Unit, Invocation) (Payload, error)
type Module interface {
    Name() string
    Migrations() []Migration
    Descriptors() []Descriptor
    Handle(context.Context, Unit, Invocation) (Payload, error)
}
type Ports interface { Call(context.Context, Unit, Invocation) (Payload, error) }
type SecretStore interface {
    Put(context.Context, string, []byte) (string, error)
    Get(context.Context, string) ([]byte, error)
    Delete(context.Context, string) error
}
type BlobStore interface {
    Stage(context.Context, io.Reader, int64) (string, Digest, int64, error)
    Publish(context.Context, string, Digest) error
    Open(context.Context, Digest, int64, int64) (io.ReadCloser, error)
    RemoveStaged(context.Context, string) error
}
type Dependencies struct {
    Clock Clock; IDs IDSource; Ports Ports
    Secrets SecretStore; Blobs BlobStore
}
// Every domain owner: New(Dependencies) (*Service, error).
// *Service implements Module. Only declared outgoing ports may be used.
// Sole exception: installation's New also takes options (see DatabaseBackup).
type Dispatch struct {
    OperationID ID `json:"operation_id"`
    AttemptID ID `json:"attempt_id"`
    Generation int64 `json:"generation"`
    Adapter string `json:"adapter"`
    Action json.RawMessage `json:"action"`
    CredentialRef string `json:"credential_ref"`
    ProviderKey string `json:"provider_key,omitempty"`
    Deadline time.Time `json:"deadline"`
}
type Observation struct {
    Disposition string `json:"disposition"` // succeeded | failed | accepted | unknown | not_sent
    ProviderReference string `json:"provider_reference,omitempty"`
    Evidence json.RawMessage `json:"evidence"`
    Usage json.RawMessage `json:"usage"`
    ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
}
type Adapter interface {
    Name() string
    Contract() json.RawMessage
    Invoke(context.Context, Dispatch) (Observation, error)
    Reconcile(context.Context, Dispatch) (Observation, error)
}
type AdapterDependencies struct {
    HTTP *http.Client; Secrets SecretStore; Clock Clock; Blobs BlobStore
}
// Every adapter: New(AdapterDependencies, json.RawMessage) (Adapter, error).
// Config is strictly validated, versioned and secret-free.
type Operator interface { Call(context.Context, string, Request) (Result, error) }
type CredentialSource interface { Credential(context.Context) ([]byte, error) }
```

The registry binds concrete input/output DTO types to descriptors and handlers using `Bind[I, O any](descriptor contract.Descriptor, fn func(context.Context, contract.Unit, I) (contract.Outcome[O], error)) (contract.Handler, error)`. JSON decoding is strict and schema validation precedes execution. Internal cross-owner calls use the same schema-checked Invocation boundary to avoid sibling package imports. They retain the same Unit, actor, scope, generation and transaction; cannot mint authority; and are not exposed as public operations.

## Transaction and ownership rules

Each domain owns tables prefixed with its package name and `_`. Schema and migration bodies inside that namespace are package-private; no other package depends on their layout. Storage owns installation generation, migration metadata and `storage_events`. Evidence owns commands/receipts. Migration ordering is explicit in application assembly. All migrations run under exclusive installation ownership before serving. Cross-owner reads and changes use Ports.Call and the embedded internal schemas. Internal operations have a caller allowlist. The dispatcher detects recursive calls. Queries cannot invoke mutation descriptors. Public clients cannot supply Actor or Unit or invoke internal operations.

One controller, one ordered writer, WAL, foreign keys on every connection, synchronous FULL, busy timeout 5 seconds, consistent read snapshots. Database.Write performs ONE transaction callback attempt with no automatic callback retry. Busy exhaustion returns controller_unavailable/retryable. Unit is valid only inside its callback; no goroutines or retained handles. Unit.Emit appends state-correlated evidence to the storage outbox in the same transaction. Delivery is at least once and consumers deduplicate event IDs. No network/model calls, secret-store access, filesystem streaming or subprocess work inside a Unit. Stage bytes or local helper actions outside transactions, then publish metadata or reconciliation intent through owner methods.

Handler errors roll back the entire transaction. Persist a command refusal in a separate short transaction after rollback where necessary, retaining the original request identity. Query command IDs need not be durable. All mutations return durable command IDs; accepted responses must identify inspectable durable work. Output schemas describe Payload.Data, not the result envelope. Never leak SQL, tokens, provider raw responses or private paths through Fault.

Definition creates/updates/archive stage drafts, including schedules, responsibilities, policies and memory bindings. Only configuration.apply activates definitions. It validates owners' candidate slices, seals base revision/dependencies/requirements, and calls internal activate in the same transaction under old authority. Internal activate is admitted only from the compiler during exact plan application, not by a public flag. Public restrictive pause/revoke/cancel commits immediately without compilation or spending. Archive staging may immediately disable new admissions as an explicitly reported restrictive side effect; final removal waits for obligations.

## Effect and execution rules

Admit: current authorization, exact review/preconditions, all budget reservations, attempt/dispatch intent and evidence atomically. Claim: second transaction rechecks generation/restrictions/expiry and consumes the one-use claim. Invoke the adapter outside transactions. Record: third transaction stores observations, costs or retained uncertainty and events. Each physical call, including reconciliation and retries, has a distinct attempt. Reconciliation is an admitted bounded read and links to the original effect; it does not overwrite history. Internal jobs use explicitly provisioned scoped service identities plus current worker/task restrictions.

Once claimed, crash/timeout/cancel/lease expiry/operator acknowledgement/weak not-found cannot prove nonexecution. Preserve outcome_unknown and its reservation until authoritative evidence. Earlier unknown attempts survive later failures. Mutation SDK/HTTP retries are disabled. Even qualified idempotent retries need fresh authorization and physical-attempt evidence. Trusted adapters resolve credential references outside transactions; no public raw secrets or inherited owner credential environment. Fencing external workers blocks Zatiti mutations but cannot prove their processes stopped.

Task success needs independently established acceptance against pinned verifier code/version, sealed inputs and observations. Worker report/exit zero cannot set succeeded. Defaults: one concurrent attempt/worker, four/installation, 30 minutes/attempt, 100 model steps, eight children, delegation depth three, root deadline 24 hours. Configure explicit currency and finite spend limits before paid work. Amounts are int64 micro-units with checked overflow; rational rates use integer numerator/denominator and round reservations upward. Unknown/advisory usage and missing prices are not zero. Children share root and ancestor budgets.

## Wire conventions and limits

UUIDv4 for new identities; stable UUID references thereafter. SHA-256 lowercase hex digests. Versioned canonical JSON sorts keys, rejects duplicate keys/unknown fields, preserves exact integer values and semantic array order; schema-declared sets sort by stable identity. Preserve exact skill bytes. UTC RFC3339Nano timestamps and int64 versions >=1. Patches and snapshots are distinct; explicit delete only. Inert `extensions` alone permits namespaced extra keys. Scope is explicit and every referenced object is revalidated; no implicit global organization.

Default limits: JSON request 1 MiB; chunk 1 MiB decoded; artifact 256 MiB; skill archive 16 MiB compressed, 64 MiB expanded, 4096 entries, depth 32; list 50/max 200; read 1 MiB; redacted log record 8 KiB; event page 100/max 500; upload expiry 24 hours. Narrowing is allowed, increases require authorized configuration. Reject overflow and unsupported bounds.

Mutations require submission_key (1..128 printable ASCII) except one-time init. Bind principal, operation/version and canonical input hash. Retain >=30 days and through unresolved obligations. Replay identical input returns original disposition BEFORE stale-version validation; changed input => submission_conflict. Concurrent duplicates serialize. Recover lost acknowledgements by command lookup, not a new key. Resource updates require expected_version; apply requires base_revision and plan digest. Creation has no existing version. Opaque authenticated cursors bind principal/scope/filter/order/snapshot and expire explicitly with cursor_expired and snapshot_required=true. Event replay cannot silently omit a gap.

HTTP POST `/v1/operations/{operation_id}` uses the common request envelope. Local HTTP runs over a private Unix socket. The Flutter desktop is a direct client of this endpoint: the private Unix socket locally and, for an explicitly configured remote desktop, mutual TLS mapped to current application principals over the same operation contract, never remote MCP. It shares the envelope, fault mapping, limits, submission-key replay and cursor rules with every other client, holds no business logic or database access, and consumes the generated operation catalog rather than any Go package. Completed => HTTP 200; accepted => 202. Failed mapping: invalid_input 400, permission_denied 403, not_found 404, stale_version/submission_conflict/conflict/review_required/outcome_unknown/artifact_fault 409, cursor_expired 410, prerequisite_missing/external_action_required/budget_unavailable/capability_unsupported/verification_failed 422, controller_unavailable 503, internal_error 500. Domain failures always include the result envelope.

CLI completed/accepted => exit 0; invalid_input 2; permission_denied/review_required 3; stale_version/submission_conflict/conflict 4; prerequisite_missing/external_action_required/budget_unavailable/capability_unsupported 5; controller_unavailable/outcome_unknown 6; other failures 1. CLI --json emits one envelope to stdout, diagnostics stderr, no implicit prompts. MCP structuredContent is the full envelope, text is equivalent JSON, failed => isError true, malformed protocol => protocol error. Dot-separated operation IDs map to CLI tokens and `zatiti_` MCP names. Exceptions: installation.init => `zatiti init` / `zatiti_installation_init`; capabilities.list => `zatiti capabilities` / `zatiti_capabilities`. Lifecycle steps are individual tools. Tool input never selects a credential profile or accepts raw secrets/unrestricted server paths.

## Completion policy

Write only within the assigned root. Do not edit this prompt, siblings, shared contracts, root dependencies or external acceptance inputs. Implement exact fake dependencies locally; do not return invented production success for unavailable prerequisites. Use controlled providers, deterministic clocks and synthetic fixtures. Preserve expected/observed results and failure evidence. Package tests prove local properties; actual subprocess parity, crash recovery, desktop and adapter qualification prove integration. Z01-Z21 and first-release journeys on macOS/Linux must pass before a release is claimed.

## Local IO, authentication, jobs and verification seams

These additional declarations are part of the SAME frozen contract package (using imports already shown). Implementing an interface does not permit calling it inside a Unit except where the signature explicitly takes Unit/Reader.

```go
type Authenticator interface {
    Authenticate(context.Context, Reader, []byte) (Actor, error)
    AuthenticateCertificate(context.Context, Reader, Digest) (Actor, error)
}
type IOPlan struct {
    ID ID
    Owner string
    Invocation Invocation
    Actor Actor
    Scope Scope
    Generation int64
    ExpectedVersions map[ID]Version
    Prepared json.RawMessage
}
type IOResult struct { Data json.RawMessage; Fault *Fault }
type LocalIO interface {
    Prepare(context.Context, Unit, Invocation) (IOPlan, error)
    Perform(context.Context, IOPlan) (IOResult, error)
    Finish(context.Context, Unit, IOPlan, IOResult) (Payload, error)
}
// Request is the exact VerificationRequest JSON schema embedded in the prompt.
type Verification struct { Request json.RawMessage }
// Document is the exact VerificationResult schema, including independently
// observed checks, pinned identity, task/attempt and staged artifact handoff.
type VerificationResult struct { Document json.RawMessage }
type Verifier interface {
    Verify(context.Context, Verification) (VerificationResult, error)
}
type VerifierDependencies struct { Clock Clock; Blobs BlobStore }
// DatabaseBackup is a narrow capability, not database access. Database satisfies
// it structurally. Entrypoint assembly supplies it to installation only.
type DatabaseBackup interface { Backup(ctx context.Context, w io.Writer) error }

// Revision 3: worker operation execution and durable job registry. Same
// package, same import set; no new dependency. (Revision 3 also introduces a
// restore capability, but as shipped it is `controller.RestoreLifecycle`,
// declared in internal/controller, not a contract-package type -- see
// "Restore protocol (revision 3)" below.)
type WorkerRequest struct {
    TurnID ID
    ProposalID string
    WorkerID ID
    Scope Scope
    Operation string
    Version Version
    SubmissionKey string
    Input json.RawMessage
}
// WorkerOperator resolves the actor from the persisted turn/worker mapping and
// re-enters ordinary application authorization; WorkerID is an asserted match
// against that turn, never a way to select any principal. Injected only into
// trusted runtime composition (application, given to execution/controller).
type WorkerOperator interface {
    ExecuteWorker(context.Context, WorkerRequest) (Result, error)
}
// JobWork/JobOutcome/LocalJobRunner are the minimal shared job types, kept in
// contract (not controller) to avoid a domain->controller import. Execution owns
// the durable ledger (execution_jobs); owners retain their domain rows and
// receive idempotent typed completion callbacks through JobOutcome.
type JobWork struct {
    ID ID
    Version Version
    Generation int64
    Owner, Operation string
    Scope Scope
    Input json.RawMessage
}
type JobOutcome struct {
    State string
    Result json.RawMessage
    EvidenceIDs []ID
    Requirements []Requirement
}
type LocalJobRunner interface {
    RunJob(context.Context, JobWork) (JobOutcome, error)
}
// Requirement mirrors the shared $defs/Requirement schema (code/message plus
// optional resource_id/challenge_id); embedded here only for the Go signature.
type Requirement struct {
    Code string `json:"code"`
    Message string `json:"message"`
    ResourceID *ID `json:"resource_id,omitempty"`
    ChallengeID *ID `json:"challenge_id,omitempty"`
}
```

### Restore protocol (revision 3)

The six-step offline restore protocol (contract-proposals.md section 7) is split across two owners as shipped, not implemented by a single entrypoint-owned capability supplied to `internal/installation` alone: `internal/installation`'s public `installation.restore` operation performs steps 1-3 without ever touching the live database file, and `internal/controller` performs steps 4-6. `installation.restore` runs the same synchronous LocalIO Prepare/Perform/Finish flow as `installation.backup` (`internal/installation/restore.go`): Prepare requires exclusive maintenance, validates the pinned backup artifact and creates the durable job; Perform (outside any transaction) decrypts and verifies the uploaded bundle's binding, integrity and framed image, then — before any rewind — exports a sealed, published `RecoveryOverlay` document of this installation's CURRENT pre-restore state (source database digest from the `DatabaseBackup` capability, plus the obligations `snapshotObligations` captured at Prepare time from pending effects and unresolved memory writes); Finish registers that overlay artifact and leaves the job `running` with an `external_action_required` requirement naming the verified backup image. `internal/installation` never swaps the SQLite file itself: a domain module cannot replace the live database out from under its own open handle mid-process, so the installation stays merely paused, not yet rewound, once `installation.restore` completes.

That handoff is driven forward by `RestoreLifecycle`, declared in `internal/controller` (`internal/controller/restore.go`) — not `contract.SnapshotInventory`/`contract.RestoreCoordinator` as this section previously described; no such contract-package types exist. `RestoreLifecycle` has two methods: `StageCandidate(ctx context.Context, restoreJobID contract.ID, dir string) (RestoreCandidate, error)` resolves the job's already-verified backup artifact into a locally staged, decrypted candidate database file, and `MergeOverlay(ctx context.Context, u contract.Unit, restoreJobID contract.ID) error` folds the already-captured `RecoveryOverlay` into every owner's own tables monotonically — never resurrecting a revoked credential, resending a consumed dispatch or erasing a liability absent from the older snapshot — inside one transaction. It is a field of `controller.Collaborators` (`RestoreLifecycle RestoreLifecycle`), attached through `Controller.Attach` exactly like `Verifier`/`Operator`/`Blobs`: only entrypoint assembly may supply an implementation, because only it has direct Go access to `internal/installation`'s private bundle/key/overlay code, and the controller itself never decrypts a backup bundle or resolves a secret reference. `cmd/zatiti/restore.go` defines the one production implementation (unexported `restoreLifecycle{}`); `cmd/zatiti/serve.go`'s `superviseController` wires it into `Collaborators.RestoreLifecycle` alongside every other trusted collaborator before `ctl.Attach(collab)`.

Once a job reaches `running`/`external_action_required`, the controller's own tick flow claims it and `runRestore` drives the remaining steps: quiesce admission and drain every other in-flight unit (`drainOrdinary`), stage the candidate image via `RestoreLifecycle.StageCandidate` and hand it to `storage.Restorable.PrepareRestore`/`CommitRestore` for the atomic file-level swap and generation advance (`performSwap`), fold the overlay via `RestoreLifecycle.MergeOverlay` inside the transaction `storage.Restorable.WriteRestoreOverlay` opens on the freshly reopened, still-paused database, then call `storage.Restorable.ResumeAfterRestore` to lift the write gate (`mergeAndResume`), and finally report the durable disposition back to `internal/installation` through its own internal `_installation.restore.record` operation (`failRestore`/`settleRestore`). A controller lifetime that itself performs the swap cannot continue afterward: its `*application.Application` was built over the now-closed pre-restore database handle and there is no seam to repoint it, so `Controller.Run` returns the `ErrRestoreHandoff` sentinel error — not a fault — the instant the swap, overlay merge and storage resume are durable. `cmd/zatiti`'s `runServe` is an outer loop around exactly this signal: on `ErrRestoreHandoff` it calls `reassembleAfterRestoreHandoff` (closes the stale `Application` and database, reopens storage at the same path — which already observes the swap — and calls `Database.StartGeneration` again to fence out the lifetime that just ended) under the SAME held installation lock, then runs again. A crash mid-protocol recovers the same way at the next startup: `recoverRestoreBeforeFence` runs before the ordinary generation fence and drives any journal entry left open forward from its last durable phase, re-checking `storage.Restorable.RestorePaused` rather than trusting the last written phase, so it never repeats an already-committed swap or loses track of one.

Current state, honestly: production's only `RestoreLifecycle` implementation, `cmd/zatiti`'s `restoreLifecycle{}`, fails both methods closed with `contract.CodePrerequisiteMissing` rather than guessing or fabricating success — exactly the fallback `Collaborators.RestoreLifecycle`'s own doc comment designs for. `StageCandidate` fails because no operation, public or internal, exposes a pending restore job's original backup-artifact reference to entrypoint assembly (public `job.get`'s wire projection never carries `Input`; only the controller's own internal `_execution.job.claim` call does, and that result stays in the controller's private journal entry). `MergeOverlay` fails because no owner-defined merge operation yet exists in `internal/effects`, `internal/identity` or `internal/memory` to fold a `RecoveryOverlay`'s obligations into their own tables, and because credential/grant revocation obligations are never captured into the overlay to begin with: `snapshotObligations` (`internal/installation/restore.go`) records only `claimed_effect`/`unknown_effect` and `memory_write` obligation kinds. This is a scoped, tracked gap — new merge operations spanning effects/identity/memory, revocation-obligation capture in `snapshotObligations`, a job-input lookup path for `StageCandidate` — not a defect in what shipped: a restore observed without a working merge/staging path is recorded failed with `prerequisite_missing`, and the database is never touched by an incomplete attempt. A clean destination additionally needs a secure key-transfer/provisioning route; an opaque source secret-store reference alone is not portability. Missing required Serenity export guarantees blocks a full-memory backup claim; an empty `Brains` array must never be reported complete.

Identity Service also implements Authenticator. Application receives this interface explicitly in New; it uses a dedicated read snapshot and never reads identity-owned tables itself. Only the byte-slice credential boundary carries secret authentication material, never Invocation JSON. Zero sensitive buffers after use where practical; no logging. Certificate authentication resolves a preprovisioned credential reference and follows the same current principal/revocation rules.

Artifacts, skills, connections and installation Services also implement LocalIO. The registry detects this interface at assembly and routes ONLY registered local IO operations through Prepare/Perform/Finish: artifact.upload.chunk/finish/cancel/read/export; skill.import; connection.setup.begin/complete/cancel; installation.init/backup/restore. LocalIO handles local bounded files, secure helpers and backup work, never unadmitted provider/model calls. Prepare strictly validates input, versions, identity and authority and records replayable local intent under IOPlan.ID; Perform receives that exact trusted in-memory plan outside transactions, resolves opaque staging/helper references, and returns metadata; Finish rechecks authority/versions/generation and commits result/evidence. IOPlan is never public, accepted from an agent or stored with secrets. Prepared/Data JSON must use the operation's declared schemas plus private owner-local metadata, which no other package reads. No cross-owner business protocol may hide in those private fields. Perform must support safe replay of local staging/publication by plan ID, or preserve an inspectable failed/unknown local obligation. A synchronous read does not return bytes to client until final authorization check. New raw provider writes always use effects, not this interface.

For synchronous local IO mutations, persist command identity plus accepted internal pending disposition at Prepare, then replace pending disposition once at Finish. Concurrent same-key calls join/inspect that same in-progress command and never run duplicate Perform. The controller can recover abandoned local intents using the execution job API. Async backup/export uses the same interface with an inspectable job returned immediately. The artifact.upload.chunk endpoint has a 2 MiB encoded request cap (all other ordinary JSON requests 1 MiB), allowing the specified 1 MiB decoded chunk plus envelope.

Execution owns shared durable jobs (execution_jobs), distinct from runs and physical effects. `_execution.job.create` stores the original owner/operation/input and idempotent source identity; public job.get inspects any authorized job. Controller lists pending jobs, claims generation-bound ownership, invokes the named LocalIO owner outside transactions through its exact plan, and records result/obligation. Network jobs first prepare effects and wait on their recorded outcomes. `_execution.job.record` stores result data matching the originating operation's explicit completion_schema and links actual artifact/operation evidence. Never mark a job succeeded merely because its physical request was accepted.

Tasks calls `_execution.enqueue` with ready pinned task inside the same transaction; execution creates one run for task/version and returns it. `_tasks.ready` also exposes a bounded recovery scan. Execution's verifier constructor is `NewVerifier(contract.VerifierDependencies) (contract.Verifier,error)`. Verify executes outside Unit and returns actual independently established observations under the pinned accepted profile; `_execution.verification.record` rechecks attempt/task/version before changing task state. A forged worker-provided VerificationResult is never accepted through public report. Concrete context/adapter/verification payload schemas are included in relevant prompts.

Administrative effects that have no task use their command/operation ID as an accountable admission root; no fictional worker/task or fabricated task foreign key is required. Accounting skips absent scope dimensions but always reserves installation and present organization ancestors/project limits. Task effects additionally share root_task_id. A profile without enforceable charge bounds cannot claim a hard cap.

Typed handler bindings return `contract.Outcome[O]` so accepted results and page cursors survive typed registration. The frozen Bind signature is `Bind[I, O any](contract.Descriptor, func(context.Context, contract.Unit, I) (contract.Outcome[O], error)) (contract.Handler, error)`. Errors carry Fault and roll back, while accepted/completed status and cursor come from Outcome.

Assembly order: application.NewPorts() returns an unbound *PortRouter; For(owner string) returns an owner-bound contract.Ports without domain calls; construct identity and all other modules with those ports, passing installation.WithDatabaseBackup(backup-only wrapper over the opened Database) to installation.New and to no other constructor; registry.New(modules); application.New(db, registry, identityAuthenticator, clock, ids); router.Bind(app) exactly once; start serving only after Bind. PortRouter exposes `For(string) contract.Ports` and `Bind(*Application) error`, rejects pre-bind invocation, and never allows a domain to change its bound caller identity. Registry also implements Module for its own capabilities methods. Constructor execution must not query peers or start goroutines.

All native BlobStore objects are encrypted at rest (including public-classification content), because Stage has no classification argument. Metadata publication sets encrypted=true; classification separately governs disclosure. Controller records the raw adapter observation and cost disposition durably first, then publishes StagedOutput bytes outside Unit, then commits artifact metadata and normalized owner callbacks through an outbox consumer. Publication failure is a visible artifact obligation and blocks task/job acceptance; it does not erase a confirmed provider observation or authorize resending. The adapter cannot mint artifact IDs: AdapterDependencies carries no IDSource and BlobStore.Stage returns only a staging reference, digest and size. The same handoff covers the request record. Each adapter stages its exact secret-free request before sending and names it in `physical_call.request_context` as a staged ArtifactLocator with a matching StagedOutput of purpose context; the controller publishes it with the other staged bytes, for every disposition including not_sent and unknown, and delivers normalized evidence whose request_context is the artifact variant. The raw recorded observation keeps the staged locator and is never rewritten.

Installation alone needs the bytes of a consistent SQLite backup: BackupManifest requires database_digest/database_size and RecoveryOverlay requires source_database_digest, while Dependencies {Clock, IDs, Ports, Secrets, Blobs} deliberately exposes no database. The seam is the DatabaseBackup capability above and these exact declarations in internal/installation: `type Option func(*Service)`; `func WithDatabaseBackup(contract.DatabaseBackup) Option`; `func New(contract.Dependencies, ...Option) (*Service, error)`. `New(deps)` without options stays valid, so the common domain constructor shape holds. Dependencies gains no field and no other module receives the capability. Entrypoint assembly passes a wrapper value whose method set is exactly Backup and which delegates to the opened contract.Database, never the Database value itself; installation must not type-assert the capability to any wider interface or retain it beyond the Service. Installation calls Backup only from LocalIO.Perform for installation.backup and for the pre-restore recovery overlay, never inside a Unit, streaming through a hashing writer into BlobStore.Stage so database_digest/database_size and source_database_digest describe the exact staged bytes without unbounded buffering or a caller-supplied path. The capability grants no query, write, migration, event-feed or file-path access and is not the restore rewind mechanism. When the option is absent, installation.backup and installation.restore fail prerequisite_missing naming the database backup capability; they never fabricate a digest, and every other installation operation is unaffected.

Entrypoint assembly owns platform.Acquire -> storage.Open -> Migrate -> Database.StartGeneration, exactly once before controller/server admission. Pass the held contract.Ownership into controller.New; controller never acquires a second lock or advances generation again. Ownership.Lost closes when ownership is lost/released; stop admission immediately. Generation is a persisted local-controller fence, not distributed locking. The installation lock is retained until controller shutdown and DB close.

Remote TLS additionally validates the certificate chain and maps the SHA-256 certificate SPKI fingerprint through application.AuthenticateCertificate(ctx,Digest), which delegates to identity Authenticator.AuthenticateCertificate on a read snapshot. Only a verified TLS peer can enter that API; operation input cannot supply the fingerprint. Store certificate fingerprints as identity-owned authentication metadata on an explicitly provisioned credential reference. If a bearer credential is also supplied, its principal must match the verified certificate principal. No self-asserted certificate name, profile name or fingerprint header grants authority.

Every async operation declares completion_schema separately from its immediate Job response. job.get returns that schema as Job.result only when established; pending jobs omit result. Local IO mutations that may be recovered asynchronously accept either their completed resource output or `{job: Job}` in the descriptor output schema. Bootstrap remains synchronous under its exclusive one-time lock. No unspecified job kind may execute: _execution.job.create requires a registered completion_schema or a specifically registered private local-intent recovery contract.

Evidence retains the complete original result envelope for command replay, including fault message/details/retryability and cursor; projections of status/data/error_code must agree with Command.result. `_evidence.command.finish` takes that exact result. A replay cannot reconstruct a different error or lose an accepted job reference. ScopeRequired lists installation_id where input scope is required; resource-specific organization/project/worker conditions are enforced by the owned operation schemas and current referenced-resource validation, never inferred from a profile name.

Descriptor.Callers carries the exact catalog internal caller allowlist into runtime registration; application enforces this metadata rather than inventing it from operation names. Internal metadata remains available to trusted assembly/registry while public capability output contains public operations only. An empty allowlist never authorizes an internal call.

## MCP client connection adapter (coordinated revision 2026-09-24)

Founder ruling dec-1256 (hq `designs/2026-09-23-marketer-org-on-zatiti.md`, row Z-M2): Zatiti gains MCP *client* ability as a generic connection adapter, not a per-server package; RFC 2.4's "arbitrary third-party server execution" and social-publishing exclusions are superseded for this adapter only. Postiz (streamable HTTP, Bearer/`pos_` token, 13 tools) is the first qualified server. This section is the coordinated-revision record; the field-level schemas are frozen in `docs/implementation/adapter-schemas.json` under `adapter_mapping.mcpclient` and embedded in the root's generated prompt.

**Root and names.** Go package `internal/adapters/mcpclient` (specgen name `mcpclient`); the wire adapter name in `Tool.adapter`/`Dispatch.Adapter` and the `Connection.provider` value are both `mcp`. The package identifier `mcp` is already `internal/mcp` (the stdio MCP *server* over the controller client); the two are unrelated and neither imports the other. `cmd/zatiti` registers `mcpclient.New` under `"mcp"` beside the four existing constructors; `tests/qualification` gains the root as an import for executable qualification against a controlled server and, once bound, Postiz.

**Profile (`zatiti.mcp/v1`).** One profile is one server: `transport` is a `kind`-discriminated `oneOf` — `streamable_http` with exactly one `https` `endpoint`, `allow_private_endpoint` (explicit installation authorization for loopback/private/link-local, dial addresses bound after validation exactly as httpread does) and `max_redirects` const 0; `stdio` is frozen in shape (absolute `command`, `args`) but a profile naming it fails `capability_unsupported` at construction until a later coordinated revision qualifies subprocess custody. `protocol_version` enum `2025-11-25` | `2026-07-28` (the two versions go-sdk v1.7.0 supports; the server must negotiate the profile's value or `open_session` fails `capability_unsupported`). `credential_kind` is `bearer` or `none`; credential-in-path (`/mcp/:apiKey`) and the OAuth authorization-code flow are refused `capability_unsupported` — the first would put the secret in the staged request record and server logs, the second has no frozen token-endpoint shape (see `cmd/zatiti/helper.go`). `allowed_tools` is the server-level allowlist; `tool_call_cost` (`Money`, may be zero) makes every attempt a `bounded_estimate` charge under ordinary accounting; `max_request_bytes`, `max_response_bytes`, `timeout_seconds`, `classifications` (which classifications may be disclosed as arguments) and `capability_evidence` follow the other adapters.

**Destination policy.** The action never carries a URL. The only destination is the profile endpoint, which the `Connection.destinations` and the tool binding's `destinations` must name; `_connections.resolve` checks them as for every adapter. Redirects are refused; a redirect response is a failed attempt with the location recorded, never followed.

**Actions (`zatiti.mcp.action/v1`, `kind`-discriminated).**
- `open_session` — the MCP handshake as the pinned go-sdk client (v1.7.0) actually performs it: `Client.Connect` first sends a SEP-2575 `server/discover` probe (its protocol-version override is unexported, so this cannot be turned off), then, when the server does not answer it, the legacy `initialize` request and `notifications/initialized`. This is **the one declared exception** to one-request-per-attempt, bounded and recorded: at most three JSON-RPC messages over at most three HTTP requests, every one listed in `evidence.handshake` with its own `http_status` and `request_sent`; none carries model-supplied content or causes an external effect, and the discover probe is never omitted from evidence. The profile's `protocol_version` (`2025-11-25` or `2026-07-28`) is what the server must negotiate; any other negotiated version fails `open_session` as `capability_unsupported`. (Corrected 2026-09-24 from "exactly two" after the Z-M2 lane verified the SDK's Connect path against the source.) The adapter keeps the SDK `ClientSession` in a bounded in-memory table keyed by an opaque `MCPSessionHandle` returned in evidence; the raw `Mcp-Session-Id` never leaves the adapter. A stateless server (no session id) yields `session_state: stateless`. `connection.validate` for provider `mcp` is this action (`buildValidationAction`), recording server name/version/protocol/capabilities as the observed account/scopes.
- `list_tools` — exactly one `tools/list` request for one page; `next_cursor` is returned, never followed by the adapter. `connection.discover` admits it as a separately authorized `external_read` and its evidence lands through `_connections.discovery.record` (callback route kind `connection`, same as validation), which records name, pinned `input_schema`, `input_schema_digest`, optional `output_schema` and `annotations` per tool into a connections-private table and marks tools absent from a complete catalog as stale. `connection.tools` lists the recorded catalog; it is never a live call.
- `call_tool` — exactly one `tools/call`. The action carries `session_handle`, `tool`, `arguments`, the pinned `input_schema` and its digest, and the `classification` of the arguments. Before any bytes: the tool must be in profile `allowed_tools`, in the connection's recorded discovery with the same digest, and in the calling worker's binding permissions (`_connections.resolve` composes the returned `Tool` from the recorded catalog, so the existing seam returns the concrete pinned schema); arguments validate strictly against that schema (`invalid_input` otherwise); classification must be in profile `classifications`. Effect classification of an MCP tool binding defaults to `external_mutation`; an operator may bind a tool as `external_read` only through the ordinary reviewed binding change — server annotations (`read_only_hint` and friends) are recorded as hints and change nothing.
- `close_session` — one HTTP `DELETE`; a server that does not support termination yields a failed attempt with the status recorded, and the local handle is dropped either way.

**Evidence (`zatiti.mcp.evidence/v1`).** `physical_call` as for every adapter (the staged secret-free request record as `request_context`, headers with the Authorization value excluded); `kind`, `session_handle`, `session_state`, `handshake`, server identity/capabilities, `tools`/`next_cursor` (list), `tool`/`arguments_digest`/`is_error`/`content_summary`/`structured_content_digest` (call), `refused_server_requests`, `usage`, and the tool result staged as one `StagedOutput` of purpose `tool_result` under the action's classification. `resource_link` and embedded-resource content items are recorded as opaque references inside that staged result and never fetched; a fetch is a separate governed read.

**Unsupported features are refusals, never inferred success.** Every server-to-client request (sampling, elicitation, roots, ping) is answered with JSON-RPC method-not-found and named in `refused_server_requests`; the adapter opens no GET listening stream and never calls resources, prompts or tasks. `Reconcile` is `capability_unsupported` for every kind: the protocol has no authoritative call lookup, so a `call_tool` timeout after bytes were sent, a lost response, or a body above `max_response_bytes` (`error_code: response_oversize`) is `outcome_unknown` with its reservation retained until a separately admitted linked operation resolves it. An unknown `session_handle` (restart, expiry, server 404) is `prerequisite_missing` and `not_sent`; execution opens a new session through a new admitted `open_session`, never inside the failed attempt. SDK retries and reconnects are disabled.

**Phase-0 defects (hq `designs/2026-09-23-zatiti-total-migration-gaps.md`, items 1-2) are routed around, not depended on.** The `connection.setup.complete` receipt-key gap (`cmd/zatiti/helper.go`, `internal/connections/localio.go` reading a literal constant as a secret reference) blocks helper-driven credential capture on a real installation; the adapter root's own proving uses a local fake `SecretStore`, and the real-credential path is Z-M3's (Postiz end to end), which depends on Z-M1.2 or on a verified `connection.create` with the opaque reference `SecretStore.Put` actually returns. The Unix-socket-only CLI/MCP surface is unrelated: this adapter is an outbound client dialing the server's `https` endpoint from the controller, not a listener.

## OpenAI Responses session preparation (revision 3)

The checked-in `internal/adapters/responses` adapter created a provider conversation and then called Responses inside one `Invoke`, which is two physical requests behind a contract that specifies exactly one physical call per claimed effect (audit finding G27). Revision 3 freezes the split into two explicit effects, each making one physical call, with a persisted session handle passed between them:

- `prepare_session` — `kind: "prepare_session"` — creates the provider conversation and returns its authoritative handle as `ResponsesEvidence.session_handle` (a stable provider-issued string, never fabricated locally). **Disclosure:** the action carries no model-visible content beyond what an empty conversation creation requires; no `context_artifact` is sent. **Timeout:** a timeout after the request was sent leaves the session unknown, not absent — the caller must not create a second session on an unconfirmed `prepare_session` outcome; it waits for recovery. **Cost:** session creation itself is not billed by the pinned protocol; usage is `unknown`/advisory only if the provider's behavior deviates from that pin. **Unknown outcome:** if session creation may have succeeded but its response was lost, retain the unknown outcome without resending — a fresh `prepare_session` could create a second, unlinked provider conversation.
- `model_step` — `kind: "model_step"` — names the persisted session handle (`ResponsesParameters.session_handle`, required) and sends the model input (`context_artifact`, `max_output_tokens`, `tool_contract_versions`). **Disclosure:** exactly the pinned `context_artifact`; no other model-visible bytes. **Timeout:** a timeout after bytes were sent is `outcome_unknown`; an empty item list is equally consistent with "never ran" and "still running" (the provider documents no conversation search), so nonexecution is never assumed. **Cost:** reserved against the profile's accepted token bound before dispatch; a lookup that finds output but no usage cannot release the worst-case billing liability, so it stays reserved. **Unknown outcome:** preserved exactly as any other adapter unknown — no automatic fresh call, no silent retry.

`ResponsesParameters` is versioned to require `kind` (`prepare_session` | `model_step`) as a discriminator; `model_step` additionally requires `session_handle`. `session_handle` is scoped to the same conversation/context lineage as the turn that created it and is never reused across turns. P13 implements both translations against the pinned protocol; P15 consumes the resolved handle when preparing a `model_step` action; P12/P23 journal and dispatch each effect separately through the callback-routed `_effects.prepare`/`.record` pipeline above, so a lost `prepare_session` acknowledgement and a lost `model_step` acknowledgement are two independently recoverable unknowns, never conflated into one. Exact `ResponsesParameters`/`ResponsesEvidence` field-level schemas are frozen in `docs/implementation/adapter-schemas.json` and embedded in `internal/adapters/responses`'s generated prompt; this section is the coordinated-revision record for why they carry a `kind` discriminator and a `session_handle`, not a description of a new adapter behavior beyond the split itself.

## Local decision tool schemas (revision 3)

`ReplyProposal { text }`, `ClarifyProposal { question }`, `ReportOutputsProposal { bindings: [{name, artifact}] }` and `CycleDecisionProposal { decision: continue|wait|escalate|done, reason, next_wake? }` are frozen in `docs/implementation/operations.json`'s `$defs` (via `tools/specgen/model.py`) as `LocalDecisionTool`, a `oneOf` over the four. They are the sealed names/inputs contract-proposals.md section 4 requires before P16 interprets model output: execution-local proposals the context builder registers as non-provider tool definitions, never routed through a `connections.Tool`/adapter and never given a provider operation mapping. A final `reply` alone may complete a chat turn; it never implicitly fulfills a task's required outputs, which only `report_outputs` (bound through `_tasks.evidence.record`) can do.

## Owned product requirements

### R2.1-002 (source section 2.1; primary owner configuration)

One installation belongs to one operator or team and has one database, credential custody boundary, controller owner, and administrative trust root. It may contain multiple organizations. An organization is a grouping of teams, projects, workers, and configuration; it is not a hosted tenant.

### R2.1-003 (source section 2.1; primary owner configuration)

Organization and project scopes still restrict ordinary principals and workers. Cross-organization references require an explicit binding, and secrets are never globally available by default. These checks protect against accidental or unauthorized application access. They do not promise isolation from the installation administrator, database owner, or another process with unrestricted access to the same operating-system account.

### R2.1-004 (source section 2.1; primary owner configuration)

Organizations form an acyclic parent-child tree rooted in the personal organization. Bootstrap creates that root and its personal-chief identity; paid execution remains unavailable until the operator configures a provider and limits. Creating another organization creates its chief in the same configuration plan. Each active organization has exactly one designated chief, whose replacement preserves the organization's identity, memory, obligations, and history. Workers have one home organization; cross-organization assignments require explicit bindings. Creating an ordinary worker does not create an organization.

### R2.1-005 (source section 2.1; primary owner configuration)

Parent organizations impose permission ceilings and aggregate budget limits on descendants. Child policy can narrow those limits, never expand them. Parent chiefs receive authorized reports from children, not automatic access to every descendant's private memory, credentials, or project data. Moving an organization or worker is an explicit configuration plan that rechecks inherited limits and memory bindings; it cannot silently transfer old private memories or widen the authority of active work.

### R2.1-006 (source section 2.1; primary owner configuration)

There is no tenant routing, tenant signup, tenant billing, or hosted multitenant control plane in v1. Separate installations are the boundary for mutually untrusted operators. Installation IDs prevent accidental mixing of exports, credentials, and checkpoints; they are not tenant IDs in another form.

### R2.2-002 (source section 2.2; primary owner identity)

An external agent may use MCP or invoke the CLI in JSON mode to discover operations and operate every authorized product capability. Zatiti does not require its own chat UI, a proprietary planning model, or a separate human-only administration API.

### R2.2-003 (source section 2.2; primary owner identity)

Agent access and worker execution are distinct:

### R2.2-004 (source section 2.2; primary owner identity)

- A **client agent** manages Zatiti using a scoped principal. Claude Code, Codex, Cursor, or another compatible client can fill this role.
- A **worker** is a persistent Zatiti identity and configuration. An executor may run a hosted model loop or an external agent may claim work for it.
- An **executor** owns a particular process or cooperative run attempt. Supporting a client over MCP does not imply that Zatiti can launch or sandbox that client's application.

### R2.2-005 (source section 2.2; primary owner identity)

Full operation means complete API coverage under the principal's authority. It does not mean permission to expand that authority, bypass provider consent, read stored secret bytes, or satisfy a human-required decision by claiming that a human approved it.

### R4-002 (source section 4; primary owner contract)

| Entity | Meaning |
|---|---|
| Installation | One administrative trust and storage boundary |
| Principal | Authenticated human, client agent, worker, or service identity |
| Organization | Versioned responsibility scope with one chief, optional parent, shared memory, and inherited authority and budget ceilings |
| Responsibility | Ongoing outcome mandate with an owner, triggers, reasoning policy, limits, and review criteria; creates bounded tasks |
| Memory binding | Explicit read, write, or promotion access to a worker, organization, or installation Serenity brain |
| Autonomy qualification | Evidence-backed permission eligibility for an exact capability, scope, and execution configuration |
| Team | Organizational grouping; membership alone grants no authority |
| Project | Explicit repository, data, connection, artifact, and budget scope |
| Worker | Persistent identity, purpose, instructions, skills, tool bindings, execution profile, limits, and delegation rules |
| Skill version | Immutable instructional package with schemas, requirements, provenance, and evaluation references |
| Tool contract | Typed operation, effect classification, input/output schemas, provider behavior, and recovery rules |
| Connection | Named provider/account identity, credential reference, allowed destinations/scopes, and validation state |
| Binding | Explicit attachment of a tool, skill, connection, worker, or repository to a permitted scope |
| Configuration plan | Exact proposed definition changes, expected revision, dependencies, authority delta, and required decisions |
| Task | Desired outcome, owner, assignee, inputs, dependencies, acceptance contract, and limits |
| Run / run attempt | One task execution / one executor lifetime within it |
| Operation / operation attempt | One logical protected effect / one physical provider invocation |
| Review / decision | A request for an exact action / an immutable eligible principal response |
| Artifact / event | Immutable output bytes / an observed state transition or fact |

### R4-003 (source section 4; primary owner contract)

Use stable UUID identities, explicit enums, UTC timestamps, and monotonically increasing resource versions. Human-readable keys resolve within explicit organization/project scope. Every public mutation validates the referenced objects and current scope. Missing scope is an error when ambiguous; an implicit global organization is not selected on an agent's behalf.

### R4-004 (source section 4; primary owner contract)

Proposed Go ownership boundaries are `identity`, `configuration`, `skills`, `connections`, `policy`, `tasks`, `execution`, `accounting`, `artifacts`, and `evidence`, with transport and platform packages around them. Application services compose transactions through owner methods. A module does not mutate another owner's tables through raw SQL. Add packages when behavior warrants them, not merely to mirror this table.

### R5-002 (source section 5; primary owner identity)

Bootstrap establishes a local owner under explicit OS-level installation access. `installation.init` is available through both interfaces before normal authentication exists, but only in a one-time local bootstrap mode. `zatiti init` and `zatiti mcp serve --bootstrap` use the same initializer. Bootstrap checks the destination is uninitialized, acquires exclusive ownership, creates owner credentials in the selected secure store, and returns metadata only. It refuses reinitialization; it never exports an owner token into model context. Bootstrap mode ends after successful initialization and cannot be used as an alternate administration session.

### R5-003 (source section 5; primary owner identity)

Normal CLI and MCP sessions select an explicitly provisioned local credential profile. The controller authenticates its credential and resolves a principal; a profile name, tool argument, MCP client name, socket access, or OS username alone does not establish application authority. MCP tool arguments cannot select a more privileged profile. Subprocesses do not receive the owner's credential environment by default. Same-user processes with access to the secure store remain within the OS trust boundary described in section 2.

### R5-004 (source section 5; primary owner identity)

An owner can delegate administration of named organizations, projects, workers, skills, and connections to a client-agent principal. This permits an agent to create and activate ordinary configuration within that existing envelope without a human confirming every edit. Requests outside the envelope produce a precise review or denial. Authority is evaluated using current grants and policy, never the proposed replacement policy.

### R5-005 (source section 5; primary owner identity)

Permissions intersect principal scope, project/organization binding, worker/task scope where applicable, policy, active restrictions, resource availability, and required review. Delegation only narrows tool access, destinations, deadlines, and shared budgets. Explicit denials win. Unknown required conditions refuse admission. Worker self-modification and imported skill text cannot install grants.

### R5-006 (source section 5; primary owner identity)

Policy supports standing authorized classes and exact-review classes. Default external publication, outbound messages, merges, deployment, credential-account substitution, and permission expansion require an eligible owner's decision unless the owner has explicitly established a narrower standing policy for that class. New policy is applied under old authority. Enabling automation, including an earned-autonomy promotion rule, is an explicit administrative act, not a competence score or model assertion.

### R5-007 (source section 5; primary owner identity)

Workers and chiefs start with minimum permissions. The product supports increasing autonomy up to the operator's explicitly authorized ceilings. Chiefs propose promotions using recorded outcomes; deterministic rules evaluate independently established evidence against operator-approved requirements before activating a narrowly scoped grant. Without an applicable promotion rule, expansion requires the eligible owner's decision. No worker can approve its own evidence, rewrite its qualification criteria, or enlarge the ceiling that permits promotion.

### R5-008 (source section 5; primary owner identity)

Qualifications bind capability, destination/scope, worker identity, relevant model/tool/skill versions, evidence window, and the promotion-rule version. Success in one capability does not grant unrelated powers. Relevant configuration changes trigger requalification; specified failures or incidents automatically restrict or demote the affected grant before further admission. Promotion and demotion produce durable events and user-visible explanations. Mandatory human-review classes remain mandatory unless the owner explicitly changes their governing policy under existing authority.

### R5-009 (source section 5; primary owner identity)

Reviews declare whether a human is required. The same `review.decide` operation exists in CLI and MCP; both enforce principal kind, eligibility, current version, action digest, expiry, and optional separation of proposer and reviewer. An agent credential cannot satisfy a human-required review, even if it sends `approved_by_human: true` or runs the CLI instead. A human session can use either transport. V1 does not claim a model invocation through a human-authorized process proves physical human presence; installation owners are responsible for credential delegation.

### R5-010 (source section 5; primary owner identity)

Pause, revoke, and cancel commit restrictive state immediately under authorized access, without a model call, configuration compilation, or spend reservation. Resume and expansion use normal authorization. A pause prevents future admissions; it cannot retract a request already transmitted or terminate an uncooperative external process by assertion.

### R6-002 (source section 6; primary owner configuration)

The desktop personal chief is the primary conversational authoring interface for the individual user. Organization chiefs provide the same assistance within their delegated scope. An external coding agent can independently discover schemas, read relevant state, submit drafts, inspect diagnostics and plans, resolve prerequisites, and apply authorized changes through CLI or MCP. All authoring paths use the same compiler. Zatiti stores drafts and plan lineage; it need not store an external client's hidden reasoning or entire conversation.

### R6-003 (source section 6; primary owner configuration)

All definition writes converge on one compiler. Typed resource operations such as `worker.create` and `connection.update` stage changes in a draft; they do not mutate effective configuration. `configuration.plan` validates and seals a complete change plan. `configuration.apply` activates that exact plan. CLI convenience commands cannot invent a second live CRUD path. Runtime commands such as task cancellation and credential revocation have their own explicit transactions.

### R6-004 (source section 6; primary owner configuration)

The organization export schema is `zatiti.organization/v1`. It contains the organization identity, base revision, teams, projects, worker definitions, skill-version references, tool and connection bindings, policies, schedules, and execution profiles. Exports exclude secret values, principals' credentials, task histories, and mutable operation state. Credential references are opaque and require explicit rebinding on another installation. Run history export and encrypted backup are separate operations.

### R6-005 (source section 6; primary owner configuration)

JSON is the canonical interchange format. Reject duplicate keys, invalid references, cyclic dependencies, unknown fields outside inert namespaced extensions, unsafe artifact paths, and unsupported executable bindings. Patches and complete snapshots are distinct types. Deletions are explicit; omission never silently removes live objects. Canonicalization sorts maps and declared sets while preserving semantic order and exact skill bytes.

### R6-006 (source section 6; primary owner configuration)

A plan pins its base revision, candidate digest, changed objects, resolved dependency versions, compiler/schema versions, current authority requirements, and any prerequisite connection/profile checks. It returns a structured preview and missing requirements, not a claim that resources are already active. An unverified connection may exist as a draft, but an executable binding that requires it cannot activate.

### R6-007 (source section 6; primary owner configuration)

Apply atomically rechecks current head, authority, restrictions, dependency identities, prerequisite validity, and required decisions; records the new revision and event; and activates the complete bundle. Two conflicting applies cannot both succeed. A stale plan must be regenerated and any digest-bound decision renewed. Retrying a completed apply with the same submission key returns its original result. Rollback is a new plan against the current revision.

### R6-008 (source section 6; primary owner configuration)

Removing a worker, project, or organization first disables new work. Active runs, retained artifacts, unresolved operations, and policy obligations block destructive removal or require an explicit archival disposition. A successful delete command never implies erased external effects or discarded accounting obligations.

### P00-014 (source section P00; primary owner identity)

_identity.bootstrap accepts optional service_credential_id/service_store_ref so the bootstrap-created controller service principal can receive a credential in the same transaction, letting an out-of-process controller authenticate; an in-process explicit-Actor seam may omit them and remains credential-less as in revision 2. _identity.authority, like every internal operation, is gated by its caller allowlist and by the calling actor being a registered, unrevoked principal in the transaction's installation -- never by requiring that actor to already hold the capability named in its own request; no standing grant of _identity.authority is needed to work around a circular subject-capability check.
## Exact operation and dependency schemas

### `_identity.activate` v1 — identity / internal / mutation / local

Allowed internal callers: configuration, application. Submission key: not required at this internal/query/bootstrap boundary.

Apply owned exact sealed candidate slice inside compiler transaction; caller must hold configuration-apply context established by application. No public activation flag or second compiler.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"candidate":{"$ref":"#/$defs/Candidate"}},"required":["candidate"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096}},"required":["versions"]}
```

### `_identity.authority` v1 — identity / internal / query / local

Allowed internal callers: application, policy, reviews, configuration, execution, effects. Submission key: not required at this internal/query/bootstrap boundary.

Read current principal, grants, expiry/revocations and restrictions; no grants from claimed profile/name or proposed policy.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"principal_id":{"type":"string","format":"uuid"},"scope":{"$ref":"#/$defs/Scope"}},"required":["principal_id","scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Authority"}},"required":["resource"]}
```

### `_identity.bootstrap` v1 — identity / internal / mutation / local

Allowed internal callers: installation. Submission key: not required at this internal/query/bootstrap boundary.

Create one initial human owner and scoped service identities in exclusive bootstrap transaction. Credential bytes were stored by trusted helper beforehand. Optional service_credential_id/service_store_ref provision a credential for the bootstrap-created controller service principal in the same transaction, so an out-of-process controller can authenticate; omitted fields leave the service principal creditalless for an in-process explicit-Actor seam, unchanged from revision 2.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"owner_id":{"type":"string","format":"uuid"},"credential_id":{"type":"string","format":"uuid"},"store_ref":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"installation_id":{"type":"string","format":"uuid"},"service_credential_id":{"type":"string","format":"uuid"},"service_store_ref":{"type":"string","maxLength":8192}},"required":["owner_id","credential_id","store_ref","name","installation_id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Principal"}},"required":["resource"]}
```

### `_identity.promote` v1 — identity / internal / mutation / local

Allowed internal callers: policy. Submission key: not required at this internal/query/bootstrap boundary.

Activate exact evidence-qualified narrow grant after old-policy rule check; verify ceiling/current rule version and immutable qualification.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"principal_id":{"type":"string","format":"uuid"},"qualification":{"$ref":"#/$defs/Qualification"},"ceiling_grant_id":{"type":"string","format":"uuid"}},"required":["principal_id","qualification","ceiling_grant_id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Grant"}},"required":["resource"]}
```

### `_identity.restore.merge` v1 — identity / internal / mutation / local

Allowed internal callers: controller. Submission key: not required at this internal/query/bootstrap boundary.

Re-apply this restore overlay's revoked_credential/revoked_grant obligations to identity's own tables, monotonically. A revocation is only ever re-applied, never lifted; a credential or grant absent from the restored image is never created to carry one, an already-revoked row is left exactly as it is, and an obligation recorded after the overlay's own capture point is refused rather than back-dated into the rewound state. Each obligation is folded at most once, keyed by its own obligation id. The caller is the controller inside storage's paused restore-overlay transaction, outside any application dispatch, so this handler independently rechecks that the acting principal is this installation's live controller service principal before writing.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"restore_job_id":{"type":"string","format":"uuid"},"captured_at":{"type":"string","format":"date-time"},"source_generation":{"type":"integer","minimum":1,"maximum":9223372036854775807},"obligations":{"type":"array","items":{"$ref":"#/$defs/RecoveryObligation"},"maxItems":4096}},"required":["restore_job_id","captured_at","source_generation","obligations"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/RecoveryMerge"}},"required":["resource"]}
```

### `_identity.restrict` v1 — identity / internal / mutation / local

Allowed internal callers: policy, installation. Submission key: not required at this internal/query/bootstrap boundary.

Atomically narrow/revoke effective grant before future admission, never expand.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"principal_id":{"type":"string","format":"uuid"},"capability":{"type":"string","maxLength":8192},"reason":{"type":"string","maxLength":8192}},"required":["principal_id","capability","reason"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Disposition"}},"required":["resource"]}
```

### `_identity.revocations` v1 — identity / internal / query / local

Allowed internal callers: installation. Submission key: not required at this internal/query/bootstrap boundary.

Report every credential and grant this installation currently holds revoked, so a paused backup or restore can capture revocations into its manifest and recovery overlay and a later merge can re-apply them after a rewind. Read only: it lists revocations, never creates, lifts or explains them, and returns no secret material or store reference.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"revocations":{"type":"array","items":{"$ref":"#/$defs/Revocation"},"maxItems":4096}},"required":["revocations"]}
```

### `_identity.validate` v1 — identity / internal / query / local

Allowed internal callers: configuration, application. Submission key: not required at this internal/query/bootstrap boundary.

Validate only owned candidate slice against current snapshot, collect dependency identities/requirements; no live changes or network. Candidate changes must match their registered concrete definition schemas. Expected-version zero is create-only. Authorization comes from old effective state.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"candidate":{"$ref":"#/$defs/Candidate"}},"required":["candidate"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Validation"}},"required":["resource"]}
```

### `credential.provision` v1 — identity / public / mutation / local

CLI `zatiti credential provision`; MCP `zatiti_credential_provision`. Submission key: required.

Attach a helper-provisioned opaque local reference; authenticate its binding. Never accept or return secret bytes.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"principal_id":{"type":"string","format":"uuid"},"store_ref":{"type":"string","maxLength":8192},"expires_at":{"type":"string","format":"date-time"}},"required":["scope","principal_id","store_ref"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Credential"}},"required":["resource"]}
```

### `credential.revoke` v1 — identity / public / mutation / local

CLI `zatiti credential revoke`; MCP `zatiti_credential_revoke`. Submission key: required.

Commit revocation immediately; future authentication and claims fail. Retain revocation through restore.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Credential"}},"required":["resource"]}
```

### `grant.create` v1 — identity / public / mutation / local

CLI `zatiti grant create`; MCP `zatiti_grant_create`. Submission key: required.

Apply create with current authorization, expected version where existing, durable command replay and atomic evidence. Revocation is immediate restriction.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"type":"object","additionalProperties":false,"properties":{"principal_id":{"type":"string","format":"uuid"},"scope":{"$ref":"#/$defs/Scope"},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"denied":{"type":"boolean"},"expires_at":{"type":"string","format":"date-time"},"parent_grant_id":{"type":"string","format":"uuid"}},"required":["principal_id","scope","capabilities","destinations","denied"]}},"required":["scope","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Grant"}},"required":["resource"]}
```

### `grant.get` v1 — identity / public / query / local

CLI `zatiti grant get`; MCP `zatiti_grant_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Grant"}},"required":["resource"]}
```

### `grant.list` v1 — identity / public / query / local

CLI `zatiti grant list`; MCP `zatiti_grant_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Grant"},"maxItems":500}},"required":["items"]}
```

### `grant.revoke` v1 — identity / public / mutation / local

CLI `zatiti grant revoke`; MCP `zatiti_grant_revoke`. Submission key: required.

Apply revoke with current authorization, expected version where existing, durable command replay and atomic evidence. Revocation is immediate restriction.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Grant"}},"required":["resource"]}
```

### `grant.update` v1 — identity / public / mutation / local

CLI `zatiti grant update`; MCP `zatiti_grant_update`. Submission key: required.

Apply update with current authorization, expected version where existing, durable command replay and atomic evidence. Revocation is immediate restriction.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":{"type":"object","additionalProperties":false,"properties":{"principal_id":{"type":"string","format":"uuid"},"scope":{"$ref":"#/$defs/Scope"},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"denied":{"type":"boolean"},"expires_at":{"type":"string","format":"date-time"},"parent_grant_id":{"type":"string","format":"uuid"}},"required":["principal_id","scope","capabilities","destinations","denied"]}},"required":["scope","id","expected_version","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Grant"}},"required":["resource"]}
```

### `principal.create` v1 — identity / public / mutation / local

CLI `zatiti principal create`; MCP `zatiti_principal_create`. Submission key: required.

Apply create with current authorization, expected version where existing, durable command replay and atomic evidence. Revocation is immediate restriction.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":{"type":"object","additionalProperties":false,"properties":{"kind":{"type":"string","enum":["human","client_agent","worker","service"]},"name":{"type":"string","maxLength":8192},"scope":{"$ref":"#/$defs/Scope"},"revoked":{"type":"boolean"}},"required":["kind","name","scope","revoked"]}},"required":["scope","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Principal"}},"required":["resource"]}
```

### `principal.get` v1 — identity / public / query / local

CLI `zatiti principal get`; MCP `zatiti_principal_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Principal"}},"required":["resource"]}
```

### `principal.list` v1 — identity / public / query / local

CLI `zatiti principal list`; MCP `zatiti_principal_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Principal"},"maxItems":500}},"required":["items"]}
```

### `principal.revoke` v1 — identity / public / mutation / local

CLI `zatiti principal revoke`; MCP `zatiti_principal_revoke`. Submission key: required.

Apply revoke with current authorization, expected version where existing, durable command replay and atomic evidence. Revocation is immediate restriction.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Principal"}},"required":["resource"]}
```

### `principal.update` v1 — identity / public / mutation / local

CLI `zatiti principal update`; MCP `zatiti_principal_update`. Submission key: required.

Apply update with current authorization, expected version where existing, durable command replay and atomic evidence. Revocation is immediate restriction.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":{"type":"object","additionalProperties":false,"properties":{"kind":{"type":"string","enum":["human","client_agent","worker","service"]},"name":{"type":"string","maxLength":8192},"scope":{"$ref":"#/$defs/Scope"},"revoked":{"type":"boolean"}},"required":["kind","name","scope","revoked"]}},"required":["scope","id","expected_version","definition"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Principal"}},"required":["resource"]}
```

### Local schema definitions

The schemas above resolve exclusively against this embedded `$defs` object. Input objects reject additional properties except explicitly open schema/data fields. Field semantics are completed by the owned requirements and operation descriptions. Output `resource`, `items`, `draft`, `job`, etc. are literal keys. Pagination cursor lives in the common envelope.

```json
{"$defs":{"Acceptance":{"type":"object","additionalProperties":false,"properties":{"verifier_id":{"type":"string","maxLength":8192},"verifier_version":{"type":"string","maxLength":8192},"sealed_inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"expected_observations":{"type":"array","items":{"$ref":"#/$defs/Adapter_ExpectedVerificationObservation"},"maxItems":512},"mode":{"type":"string","enum":["independent","manual"]},"required_child_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"profile":{"$ref":"#/$defs/Adapter_VerificationProfile"}},"required":["verifier_id","verifier_version","sealed_inputs","expected_observations","mode","required_child_ids","profile"]},"Action":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"tool":{"$ref":"#/$defs/Ref"},"connection":{"$ref":"#/$defs/Ref"},"account_identity":{"type":"string","maxLength":8192},"destination":{"type":"string","maxLength":8192},"content":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"not_before":{"type":"string","format":"date-time"},"expires_at":{"type":"string","format":"date-time"},"preconditions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"configuration_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"parameters":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"cost_bound":{"$ref":"#/$defs/Money"}},"required":["scope","tool","connection","account_identity","destination","content","not_before","expires_at","preconditions","configuration_revision","parameters","cost_bound"]},"Adapter_ArtifactRef":{"type":"object","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/Adapter_ID"},"digest":{"$ref":"#/$defs/Adapter_Digest"}},"required":["id","digest"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ArtifactVerifierProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verifier-profile/v1","type":"string"},"kind":{"const":"artifact_contract","type":"string"},"id":{"type":"string","minLength":1,"maxLength":256},"version":{"type":"string","minLength":1,"maxLength":128},"code_digest":{"$ref":"#/$defs/Adapter_Digest"},"supported_checks":{"type":"array","items":{"type":"string","enum":["presence","digest","json_schema"]},"minItems":1,"maxItems":3},"max_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"capability_evidence":{"$ref":"#/$defs/Adapter_CapabilityEvidence"}},"required":["schema","kind","id","version","code_digest","supported_checks","max_bytes","timeout_seconds","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_CapabilityEvidence":{"type":"object","additionalProperties":false,"properties":{"artifact":{"$ref":"#/$defs/Adapter_ArtifactRef"},"adapter_version":{"type":"string","minLength":1,"maxLength":128},"source_revision":{"type":"string","minLength":1,"maxLength":128},"protocol_revision":{"type":"string","minLength":1,"maxLength":128},"profile_digest":{"$ref":"#/$defs/Adapter_Digest"},"qualified_at":{"$ref":"#/$defs/Adapter_UTC"},"capabilities":{"type":"array","items":{"type":"string","minLength":1,"maxLength":128},"minItems":0,"maxItems":128},"limitations":{"type":"array","items":{"type":"string","minLength":1,"maxLength":2048},"minItems":0,"maxItems":128}},"required":["artifact","adapter_version","source_revision","protocol_revision","profile_digest","qualified_at","capabilities","limitations"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_Digest":{"type":"string","pattern":"^[0-9a-f]{64}$","maxLength":64,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ExpectedVerificationObservation":{"type":"object","additionalProperties":false,"properties":{"check_id":{"type":"string","minLength":1,"maxLength":256},"kind":{"type":"string","enum":["artifact_presence","artifact_digest","json_schema","repository_patch_applies","repository_command"]},"expected":{"type":"string","enum":["pass","fail"]},"artifact_name":{"type":"string","minLength":1,"maxLength":128},"expected_digest":{"$ref":"#/$defs/Adapter_Digest"},"schema":{"$ref":"#/$defs/Adapter_InertSchema"},"command_id":{"type":"string","minLength":1,"maxLength":256},"expected_exit_code":{"type":"integer","minimum":-2147483648,"maximum":2147483647}},"required":["check_id","kind","expected"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ID":{"type":"string","format":"uuid","maxLength":36,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_InertSchema":{"type":"object","description":"A bounded JSON Schema 2020-12 document. Local references only; validators reject remote references, duplicate keys, excessive depth/node count and unsupported executable behavior. This is schema data, not authority.","maxProperties":256,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_RepositoryVerifierProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verifier-profile/v1","type":"string"},"kind":{"const":"repository_patch","type":"string"},"id":{"type":"string","minLength":1,"maxLength":256},"version":{"type":"string","minLength":1,"maxLength":128},"code_digest":{"$ref":"#/$defs/Adapter_Digest"},"runner_profile":{"type":"string","minLength":1,"maxLength":256},"command_id":{"type":"string","minLength":1,"maxLength":256},"command_digest":{"$ref":"#/$defs/Adapter_Digest"},"environment_profile":{"type":"string","minLength":1,"maxLength":256},"network":{"type":"string","enum":["disabled","qualified_allowlist"]},"max_output_bytes":{"type":"integer","minimum":1,"maximum":1048576},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"capability_evidence":{"$ref":"#/$defs/Adapter_CapabilityEvidence"}},"required":["schema","kind","id","version","code_digest","runner_profile","command_id","command_digest","environment_profile","network","max_output_bytes","timeout_seconds","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_UTC":{"type":"string","format":"date-time","pattern":"Z$","maxLength":40,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_VerificationProfile":{"oneOf":[{"$ref":"#/$defs/Adapter_ArtifactVerifierProfile"},{"$ref":"#/$defs/Adapter_RepositoryVerifierProfile"}],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ArtifactRef":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["id","digest"]},"Authority":{"type":"object","additionalProperties":false,"properties":{"principal":{"$ref":"#/$defs/Principal"},"grants":{"type":"array","items":{"$ref":"#/$defs/Grant"},"maxItems":4096},"restrictions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096}},"required":["principal","grants","restrictions"]},"Binding":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"kind":{"type":"string","enum":["tool","skill","connection","worker","repository","reporting","memory"]},"target_id":{"type":"string","format":"uuid"},"permissions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"source_scope":{"$ref":"#/$defs/Scope"},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096}},"required":["id","version","scope","kind","target_id","permissions"]},"CallbackRoute":{"type":"object","additionalProperties":false,"properties":{"kind":{"type":"string","enum":["worker_turn","job","memory","skill","connection"]},"turn_id":{"type":"string","format":"uuid"},"step_index":{"type":"integer","minimum":0,"maximum":9223372036854775807},"job_id":{"type":"string","format":"uuid"}},"required":["kind"]},"Candidate":{"type":"object","additionalProperties":false,"properties":{"plan_id":{"type":"string","format":"uuid"},"base_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"candidate_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"changes":{"type":"array","items":{"$ref":"#/$defs/Change"},"maxItems":4096},"dependencies":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096}},"required":["plan_id","base_revision","candidate_digest","changes","dependencies"]},"Change":{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"organization"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Organization"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"team"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Team"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"project"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Project"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"worker"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Worker"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"binding"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Binding"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"execution_profile"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/ExecutionProfile"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"skill"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Skill"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"connection"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Connection"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"policy"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Policy"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"autonomy_rule"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/PromotionRule"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"schedule"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Schedule"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"responsibility"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Responsibility"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"memory_binding"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/MemoryBinding"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"budget"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Limits"}},"required":["kind","action","id","expected_version","definition"]}]},"Connection":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"provider":{"type":"string","maxLength":8192},"account_identity":{"type":"string","maxLength":8192},"credential_ref":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"allowed_scopes":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"validation_state":{"type":"string","enum":["unverified","valid","invalid","expired","revoked"]},"validated_at":{"type":"string","format":"date-time"},"valid_until":{"type":"string","format":"date-time"}},"required":["id","version","scope","provider","account_identity","credential_ref","destinations","allowed_scopes","validation_state"]},"Credential":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"principal_id":{"type":"string","format":"uuid"},"store_ref":{"type":"string","maxLength":8192},"revoked":{"type":"boolean"},"expires_at":{"type":"string","format":"date-time"}},"required":["id","version","principal_id","store_ref","revoked"]},"Diagnostic":{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string","maxLength":8192},"code":{"type":"string","maxLength":8192},"message":{"type":"string","maxLength":8192},"severity":{"type":"string","enum":["error","warning","info"]}},"required":["path","code","message","severity"]},"Disposition":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"state":{"type":"string","maxLength":8192},"job":{"$ref":"#/$defs/Job"},"operation":{"$ref":"#/$defs/Operation"},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096}},"required":["id","version","state"]},"ExecutionProfile":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"executor":{"type":"string","enum":["hosted","cooperative"]},"model":{"type":"string","maxLength":8192},"connection_id":{"type":"string","format":"uuid"},"provider_destination":{"type":"string","maxLength":8192},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"cost_bound":{"$ref":"#/$defs/Money"},"classification":{"type":"string","enum":["internal","public","restricted"]},"context_capture":{"type":"string","enum":["complete","partial","advisory"]}},"required":["id","version","executor","model","connection_id","provider_destination","capabilities","cost_bound","classification","context_capture"]},"Grant":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"principal_id":{"type":"string","format":"uuid"},"scope":{"$ref":"#/$defs/Scope"},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"denied":{"type":"boolean"},"expires_at":{"type":"string","format":"date-time"},"parent_grant_id":{"type":"string","format":"uuid"}},"required":["id","version","principal_id","scope","capabilities","destinations","denied"]},"Job":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"kind":{"type":"string","maxLength":8192},"state":{"type":"string","enum":["pending","running","succeeded","failed","outcome_unknown","cancelled"]},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096},"result_artifact":{"$ref":"#/$defs/ArtifactRef"},"operation_id":{"type":"string","format":"uuid"},"owner":{"type":"string","maxLength":8192},"operation":{"type":"string","maxLength":8192},"result":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","kind","state","requirements","owner","operation"]},"Limits":{"type":"object","additionalProperties":false,"properties":{"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"spend_micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807},"concurrency":{"type":"integer","minimum":1,"maximum":9223372036854775807},"model_steps":{"type":"integer","minimum":1,"maximum":9223372036854775807},"child_count":{"type":"integer","minimum":0,"maximum":9223372036854775807},"delegation_depth":{"type":"integer","minimum":0,"maximum":9223372036854775807},"attempt_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"root_deadline":{"type":"string","format":"date-time"}},"required":["currency","spend_micro_units","concurrency","model_steps","child_count","delegation_depth","attempt_seconds","root_deadline"]},"MemoryBinding":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"brain_id":{"type":"string","format":"uuid"},"permissions":{"type":"array","items":{"type":"string","enum":["read","write","curate","promote","retract"]},"maxItems":4096},"classification":{"type":"string","enum":["internal","public","restricted"]}},"required":["id","version","scope","brain_id","permissions","classification"]},"Money":{"type":"object","additionalProperties":false,"properties":{"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807}},"required":["currency","micro_units"]},"Operation":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"action":{"$ref":"#/$defs/Action"},"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"state":{"type":"string","enum":["prepared","awaiting_review","ready","executing","awaiting_confirmation","outcome_unknown","succeeded","failed","denied","expired","cancelled"]},"attempt_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"linked_operation_id":{"type":"string","format":"uuid"},"relationship":{"type":"string","enum":["retry","reconciliation","compensation","replacement"]},"attempts":{"type":"array","items":{"$ref":"#/$defs/OperationAttempt"},"maxItems":4096},"callback_route":{"$ref":"#/$defs/CallbackRoute"}},"required":["id","version","action","action_digest","state","attempt_ids"]},"OperationAttempt":{"type":"object","additionalProperties":false,"properties":{"attempt_id":{"type":"string","format":"uuid"},"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["attempt_id","generation"]},"Organization":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"chief_id":{"type":"string","format":"uuid"},"parent_id":{"type":"string","format":"uuid"},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","key","name","chief_id"]},"Policy":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"rules":{"type":"array","items":{"$ref":"#/$defs/Rule"},"maxItems":4096},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","scope","rules"]},"Principal":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"kind":{"type":"string","enum":["human","client_agent","worker","service"]},"name":{"type":"string","maxLength":8192},"scope":{"$ref":"#/$defs/Scope"},"revoked":{"type":"boolean"}},"required":["id","version","kind","name","scope","revoked"]},"Project":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"repositories":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"classification":{"type":"string","enum":["internal","public","restricted"]},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","organization_id","key","name","repositories","bindings","classification"]},"PromotionRule":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"capability":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"required_evidence":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"minimum_successes":{"type":"integer","minimum":1,"maximum":9223372036854775807},"evidence_window_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"disqualifying_events":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"ceiling_grant_id":{"type":"string","format":"uuid"},"human_required_preserved":{"type":"boolean"}},"required":["id","version","scope","capability","destinations","required_evidence","minimum_successes","evidence_window_seconds","disqualifying_events","ceiling_grant_id","human_required_preserved"]},"Qualification":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"worker_id":{"type":"string","format":"uuid"},"capability":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"rule":{"$ref":"#/$defs/Ref"},"model":{"type":"string","maxLength":8192},"tool_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"skill_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"evidence_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"window_start":{"type":"string","format":"date-time"},"window_end":{"type":"string","format":"date-time"},"state":{"type":"string","enum":["proposed","qualified","rejected","restricted","expired"]},"explanation":{"type":"string","maxLength":8192}},"required":["id","version","worker_id","capability","destinations","rule","model","tool_versions","skill_versions","evidence_ids","window_start","window_end","state","explanation"]},"RecoveryMerge":{"type":"object","additionalProperties":false,"properties":{"restore_job_id":{"type":"string","format":"uuid"},"owner":{"type":"string","maxLength":8192},"applied":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"skipped":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096}},"required":["restore_job_id","owner","applied","skipped"]},"RecoveryObligation":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"owner":{"type":"string","maxLength":8192},"kind":{"type":"string","maxLength":8192},"resource_id":{"type":"string","format":"uuid"},"resource_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"record_artifact":{"$ref":"#/$defs/ArtifactRef"},"record_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"state":{"type":"string","maxLength":8192},"recorded_at":{"type":"string","format":"date-time"}},"required":["id","owner","kind","resource_id","resource_version","record_artifact","record_digest","state","recorded_at"]},"Ref":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["id","version"]},"Requirement":{"type":"object","additionalProperties":false,"properties":{"code":{"type":"string","maxLength":8192},"message":{"type":"string","maxLength":8192},"resource_id":{"type":"string","format":"uuid"},"challenge_id":{"type":"string","format":"uuid"}},"required":["code","message"]},"Responsibility":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"worker_id":{"type":"string","format":"uuid"},"outcome":{"type":"string","maxLength":8192},"signals":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"triggers":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"reasoning_policy":{"type":"string","maxLength":8192},"min_interval_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"cycle_limits":{"$ref":"#/$defs/Limits"},"aggregate_limits":{"$ref":"#/$defs/Limits"},"pause_conditions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"escalation_conditions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"acceptance":{"$ref":"#/$defs/Acceptance"},"paused":{"type":"boolean"},"next_wake":{"type":"string","format":"date-time"},"last_cycle_id":{"type":"string","format":"uuid"}},"required":["id","version","scope","worker_id","outcome","signals","triggers","reasoning_policy","min_interval_seconds","cycle_limits","aggregate_limits","pause_conditions","escalation_conditions","acceptance","paused"]},"Revocation":{"type":"object","additionalProperties":false,"properties":{"kind":{"type":"string","enum":["credential","grant"]},"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"principal_id":{"type":"string","format":"uuid"}},"required":["kind","id","version","principal_id"]},"Rule":{"type":"object","additionalProperties":false,"properties":{"capability":{"type":"string","maxLength":8192},"effect":{"type":"string","enum":["local","disclosure","external_read","external_mutation"]},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"decision":{"type":"string","enum":["allow","deny","review"]},"human_required":{"type":"boolean"},"conditions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["capability","effect","destinations","decision","human_required","conditions"]},"Schedule":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"task_template":{"$ref":"#/$defs/Task"},"timezone":{"type":"string","maxLength":8192},"expression":{"type":"string","maxLength":8192},"misfire":{"type":"string","enum":["coalesce","skip"]},"catch_up_seconds":{"type":"integer","minimum":0,"maximum":9223372036854775807},"paused":{"type":"boolean"},"next_wake":{"type":"string","format":"date-time"}},"required":["id","version","scope","task_template","timezone","expression","misfire","catch_up_seconds","paused"]},"Scope":{"type":"object","additionalProperties":false,"properties":{"installation_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"project_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"}},"required":["installation_id"]},"Skill":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"name":{"type":"string","maxLength":8192},"instruction_artifact":{"$ref":"#/$defs/ArtifactRef"},"content_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"input_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"output_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"requirements":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"dependencies":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"source":{"type":"string","maxLength":8192},"license":{"type":"string","maxLength":8192},"evaluation_refs":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096}},"required":["id","version","name","instruction_artifact","content_digest","input_schema","output_schema","requirements","dependencies","source","license","evaluation_refs","diagnostics"]},"Task":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"owner_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"outcome":{"type":"string","maxLength":8192},"inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"required_outputs":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"acceptance":{"$ref":"#/$defs/Acceptance"},"limits":{"$ref":"#/$defs/Limits"},"dependencies":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"state":{"type":"string","enum":["draft","ready","running","waiting","verifying","succeeded","failed","cancelled"]},"parent_id":{"type":"string","format":"uuid"},"root_id":{"type":"string","format":"uuid"},"waiting_reason":{"type":"string","maxLength":8192},"cancellation_requested":{"type":"boolean"},"manual_acceptance":{"type":"boolean"}},"required":["id","version","scope","owner_id","worker_id","outcome","inputs","required_outputs","acceptance","limits","dependencies","state"]},"Team":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"worker_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","organization_id","key","name","worker_ids"]},"Validation":{"type":"object","additionalProperties":false,"properties":{"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096},"dependencies":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096}},"required":["diagnostics","requirements","dependencies"]},"Worker":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"purpose":{"type":"string","maxLength":8192},"instructions":{"type":"string","maxLength":8192},"skill_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"profile":{"anyOf":[{"$ref":"#/$defs/ExecutionProfile"},{"type":"null"}]},"limits":{"anyOf":[{"$ref":"#/$defs/Limits"},{"type":"null"}]},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","organization_id","key","name","purpose","instructions","skill_versions","bindings","profile","limits"]}}}
```

## Named acceptance cases

Tests are implementation deliverables, not claims of already executed qualification. Retain expected/observed results, exact source/config/tool versions and failure evidence.

### Z01.revoked_principal — Z01

Setup: A client principal has an authenticated session and an authorized scoped mutation.

Action: Revoke its credential or grant, then repeat the mutation using the existing session.

Expected:

- Current authorization denies the mutation.
- Revocation is durable and survives controller restart.

### Z01.cross_scope_reference — Z01

Setup: Two organizations contain separate projects, workers and connections with no cross-scope binding.

Action: Use an otherwise authorized principal to reference the other organization or project in a draft and task.

Expected:

- The unauthorized reference is rejected.
- No activation, task admission or secret lookup occurs until an explicit valid binding exists.

### Z01.credential_custody — Z01

Setup: A scoped agent can inspect connection metadata but has no access to owner or provider secret material.

Action: Inspect credentials through capabilities, CLI, MCP, connection inspection and malformed credential/profile arguments.

Expected:

- Only opaque references and permitted metadata are returned.
- Profile names, MCP client names and socket access do not independently confer authority; owner profile cannot be selected through tool arguments.

### Z03.bootstrap_parity — Z03

Setup: Two matching empty installation destinations and secure credential stores are available.

Action: Initialize one with zatiti init and the other with zatiti mcp serve --bootstrap; attempt initialization again.

Expected:

- Both use the one-time local initializer and create owner, personal organization and personal chief consistently.
- Results contain metadata only; reinitialization is refused and bootstrap cannot become a normal administration session.

### Z04.old_authority — Z04

Setup: A principal may edit a draft but cannot expand its own effective grants.

Action: Submit a plan containing a policy or grant that would authorize its own application.

Expected:

- Apply evaluates existing authority and rejects or requests an eligible exact decision.
- Proposed policy and imported instructions cannot authorize their own activation.

### Z05.agent_self_grant — Z05

Setup: A client agent can create ordinary scoped definitions under a fixed delegation envelope.

Action: Attempt to enlarge its scope, authorize worker self-modification or establish an autonomy rule above the envelope.

Expected:

- No authority expands without an eligible act under existing policy.
- The exact denial or review requirement identifies the missing authority.

### Z07.agent_impersonates_human — Z07

Setup: A pending review requires a human principal and an agent can call review.decide.

Action: Submit an approval with an approved_by_human assertion using both transports.

Expected:

- The agent credential cannot satisfy the review regardless of its payload or transport.
- No approval or protected dispatch is committed.

### Z07.reviewer_eligibility — Z07

Setup: Reviews include current version, expiry, proposer-separation and reviewer delegation restrictions.

Action: Decide using revoked or ineligible delegated credentials, an expired review, a stale version or the prohibited proposer.

Expected:

- Each ineligible decision is refused using current identity and policy.
- A permitted delegation is scoped and cannot bypass human-required or proposer-separation rules.

### Z13.secret_surfaces — Z13

Setup: Synthetic uniquely marked secrets exist in owner/provider secure stores and setup helpers.

Action: Exercise setup, rotation, failure logging, CLI/MCP output, organization export, artifact metadata and diagnostics.

Expected:

- No planted raw credential is exposed in tool arguments, ordinary flags, CLI/MCP results, logs or organization exports.
- Setup exposes challenge references and metadata; subprocesses do not inherit owner credential environment by default.

### Z14.outbox_revocation_restore — Z14

Setup: The backup contains unresolved dispatch/outbox intents, unknown reservations, revoked credentials and pending memory writes/promotions.

Action: Restore, inspect state, attempt use of revoked credentials and reconcile before resuming.

Expected:

- Unresolved obligations and revocation states are preserved and inspectable.
- Pending effect and memory outcomes require reconciliation; restoring an older database does not claim to undo provider effects.

### Z15.cross_installation_rebind — Z15

Setup: An organization export contains stable IDs and opaque connection references from installation A.

Action: Import into installation B and attempt activation before and after explicit local credential/account rebinding.

Expected:

- Foreign credential references do not become live authority automatically.
- Required rebinding is surfaced; authorized activation uses B-specific bindings while preserving portable definition identity semantics.

### Z17.atomic_chief_creation — Z17

Setup: A root organization exists and a permitted author proposes a child organization with its chief.

Action: Apply successfully, then inject a transaction failure during a second child creation.

Expected:

- Each active organization has exactly one designated chief.
- Organization and chief activate together or neither activates; creating an ordinary worker does not create an organization.

### Z19.self_promotion — Z19

Setup: A worker has minimum permissions and may propose a capability promotion.

Action: Have it approve its own evidence, edit its qualification criteria or raise the governing ceiling.

Expected:

- The worker cannot authorize any of those expansions.
- Without an applicable owner-approved rule, promotion waits for an eligible owner decision.

### Z19.capability_promotion — Z19

Setup: A rule permits one exact capability/destination for one worker under specified model/tool/skill versions.

Action: Supply qualifying evidence and evaluate the rule.

Expected:

- Only the exact permitted capability/scope activates within the prior ceiling.
- Unrelated powers remain unavailable; durable events identify the evidence and rule version responsible.

### Z19.immediate_demotion — Z19

Setup: An active earned grant has an owner-approved incident/failure demotion rule.

Action: Record a qualifying incident concurrently with a new effect admission.

Expected:

- Restriction or demotion commits before any subsequently authorized admission can use the old grant.
- The user can inspect durable reasons and events; no model call or new spending is required to apply restriction.

### P00.identity_authority_caller_allowlist_only — P00

Setup: A narrowly granted principal holds no _identity.authority capability itself; policy is on _identity.authority's caller allowlist.

Action: Have policy call _identity.authority for that principal's current grants, then have an off-allowlist caller attempt the same internal call, then attempt it for a revoked principal and for a principal in a different installation.

Expected:

- Policy's call succeeds despite the subject holding no _identity.authority capability of its own.
- The off-allowlist caller is refused.
- The revoked principal and cross-installation principal are refused.

## Delivery

Implement production behavior and meaningful local tests within scope. Report files changed, commands actually run, observed results, unresolved dependency qualifications and contract defects. A missing dependency may use an exact local fake for development; production must return a named prerequisite/unsupported error instead of fake success. Integration owns shared dependency changes, real assembly, cross-package proving and landing. Do not advertise release or client/platform support from compilation alone.
