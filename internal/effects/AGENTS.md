# Implementation assignment: `internal/effects`

Generated specification revision 1; source digest `c79b39e5ca496cd59114fdaa2c9dd3a36d7c9c0d3b4426bc131063f5cef22ca2`. This file is committed implementation context. Do not independently edit it. Everything required from the product specification and adjacent interfaces is embedded below; no RFC copy is required.

## Mission and scope

Own immutable actions, logical effects, physical attempts, dispatch claims and reconciliation history.

Write scope: **`internal/effects/` only**, excluding this generated AGENTS.md. Go package name: `effects`. Ownership kind: domain; integration wave: 2.

Allowed production imports from this repository: `github.com/zatiti/zatiti/internal/contract`. Tests may use interfaces/fakes defined locally and, once available, storage-backed temporary fixtures; integration owns cross-package tests. No sibling raw SQL. No root dependency edits except the integration exception stated in its own brief.

## Implementation decisions and acceptance focus

Own effects_actions/operations/attempts/claims/observations/obligations. Persist exact action before any dispatch. Action preconditions support repository_head, expected_resource_versions, not_before, expires_at and allowed_automation; tool contract fixes allowed keys and parameter schema. Unknown preconditions fail. States prepared→awaiting_review/ready/denied; ready→executing after claimed admitted attempt; executing→succeeded/failed/awaiting_confirmation/outcome_unknown. Unsent expired/cancelled/denied allowed only if no earlier attempt can still act. Known provider acceptance is awaiting_confirmation, not success. Admit and claim each recheck relevant current policy and exact review. Current authorization plus budget reservation/attempt/intent/evidence is one transaction. Controller owns actual Invoke and record transaction; service never does network. Idempotency qualifications bind equivalent request/account/destination and real provider retention window. Retry is separate physical attempt with current checks, never hidden adapter retry. Unknown older attempt survives newer failure. Reconciliation bounded read needs own authorization/attempt/cost; authoritative no-effect evidence differs from eventual-consistency not-found. Compensation/replacement new linked operation with independent review. Late contradictory evidence appends explicit correction/dispute; immutable observations never rewritten. Expose unknown status honestly to UI/CLI/MCP/backup.

Local proving focus: Admission/claim/revoke/crash interleavings with exact physical call count, lost provider success ack, failed retry after unknown, expired key, delayed confirmation/cancel, late contradiction, compensation review.

## Incoming and outgoing boundaries

Incoming callers: connections, controller, execution, installation, memory, skills, application (authenticated public operations).

Outgoing owner calls: `_identity.authority`, `_configuration.snapshot`, `_policy.check`, `_reviews.ensure`, `_reviews.check`, `_accounting.reserve`, `_accounting.settle`, `_accounting.inspect`, `_connections.resolve`, `_connections.validation.record`, `_tasks.snapshot`, `_memory.select`, `_artifacts.metadata`, `_execution.job.create`, `_execution.job.record`. Each exact input/output schema appears below. Calls retain current Unit/authority; owner allowlists are mandatory.

Expose `New(contract.Dependencies) (*Service,error)`; `*Service` implements `contract.Module` with Name `effects`, owner-prefixed migrations, all owned descriptors, and strict dispatch. No calls/goroutines during construction. Implement optional authentication/LocalIO interfaces where specified in the common contract. Tables are private under `effects_`; external callers rely only on methods and schemas.

### Imported package APIs and behavior

These briefs are embedded so you need not read a sibling prompt to discover its incoming API. Implement only your own package.

## Shared foundation contract

# Frozen implementation contract, revision 1

These decisions complete the product specification and bind every scope. Report contradictions with an affected-dependency list and proposed coordinated revision; do not change another owner's interface locally.

## Build and dependency decisions

Module `github.com/zatiti/zatiti`; language `go 1.26.0`; initial toolchain `go1.26.2`. Integration owns go.mod/go.sum. Domains import standard library and internal/contract, not sibling domains. Application composes owner methods through a checked in-process dispatcher: ordinary Go calls, not another server or scheduler.

Library families: Cobra CLI; official MCP Go SDK, handwritten adapter for MCP `2025-11-25`; Fyne v2 native Go desktop; modernc.org/sqlite. Mint is optional build-time tooling and cannot weaken contracts. Hosted provider v1: a specifically qualified OpenAI Responses endpoint/profile, with configured model and prices. Other compatible endpoints remain unavailable until qualified. GitHub uses documented REST over net/http, with mutation retries disabled. Serenity is a separate pinned service using public interfaces only. Integration must resolve exact dependency releases/commits, checksums, licenses and qualification commands in a lock report BEFORE dispatching dependent implementation. These are selected implementation families, not claims that upstream behavior has been qualified. No agent independently substitutes libraries, models, prices, accounts or Serenity APIs. Dependency resolution/qualification is an explicit foundation assignment.

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

HTTP POST `/v1/operations/{operation_id}` uses the common request envelope. Local HTTP runs over a private Unix socket. Explicit remote desktop uses mutual TLS mapped to current application principals over the same operation contract, never remote MCP. Completed => HTTP 200; accepted => 202. Failed mapping: invalid_input 400, permission_denied 403, not_found 404, stale_version/submission_conflict/conflict/review_required/outcome_unknown/artifact_fault 409, cursor_expired 410, prerequisite_missing/external_action_required/budget_unavailable/capability_unsupported/verification_failed 422, controller_unavailable 503, internal_error 500. Domain failures always include the result envelope.

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
```

Identity Service also implements Authenticator. Application receives this interface explicitly in New; it uses a dedicated read snapshot and never reads identity-owned tables itself. Only the byte-slice credential boundary carries secret authentication material, never Invocation JSON. Zero sensitive buffers after use where practical; no logging. Certificate authentication resolves a preprovisioned credential reference and follows the same current principal/revocation rules.

Artifacts, skills, connections and installation Services also implement LocalIO. The registry detects this interface at assembly and routes ONLY registered local IO operations through Prepare/Perform/Finish: artifact.upload.chunk/finish/cancel/read/export; skill.import; connection.setup.begin/complete/cancel; installation.init/backup/restore. LocalIO handles local bounded files, secure helpers and backup work, never unadmitted provider/model calls. Prepare strictly validates input, versions, identity and authority and records replayable local intent under IOPlan.ID; Perform receives that exact trusted in-memory plan outside transactions, resolves opaque staging/helper references, and returns metadata; Finish rechecks authority/versions/generation and commits result/evidence. IOPlan is never public, accepted from an agent or stored with secrets. Prepared/Data JSON must use the operation's declared schemas plus private owner-local metadata, which no other package reads. No cross-owner business protocol may hide in those private fields. Perform must support safe replay of local staging/publication by plan ID, or preserve an inspectable failed/unknown local obligation. A synchronous read does not return bytes to client until final authorization check. New raw provider writes always use effects, not this interface.

For synchronous local IO mutations, persist command identity plus accepted internal pending disposition at Prepare, then replace pending disposition once at Finish. Concurrent same-key calls join/inspect that same in-progress command and never run duplicate Perform. The controller can recover abandoned local intents using the execution job API. Async backup/export uses the same interface with an inspectable job returned immediately. The artifact.upload.chunk endpoint has a 2 MiB encoded request cap (all other ordinary JSON requests 1 MiB), allowing the specified 1 MiB decoded chunk plus envelope.

Execution owns shared durable jobs (execution_jobs), distinct from runs and physical effects. `_execution.job.create` stores the original owner/operation/input and idempotent source identity; public job.get inspects any authorized job. Controller lists pending jobs, claims generation-bound ownership, invokes the named LocalIO owner outside transactions through its exact plan, and records result/obligation. Network jobs first prepare effects and wait on their recorded outcomes. `_execution.job.record` stores result data matching the originating operation's explicit completion_schema and links actual artifact/operation evidence. Never mark a job succeeded merely because its physical request was accepted.

Tasks calls `_execution.enqueue` with ready pinned task inside the same transaction; execution creates one run for task/version and returns it. `_tasks.ready` also exposes a bounded recovery scan. Execution's verifier constructor is `NewVerifier(contract.VerifierDependencies) (contract.Verifier,error)`. Verify executes outside Unit and returns actual independently established observations under the pinned accepted profile; `_execution.verification.record` rechecks attempt/task/version before changing task state. A forged worker-provided VerificationResult is never accepted through public report. Concrete context/adapter/verification payload schemas are included in relevant prompts.

Administrative effects that have no task use their command/operation ID as an accountable admission root; no fictional worker/task or fabricated task foreign key is required. Accounting skips absent scope dimensions but always reserves installation and present organization ancestors/project limits. Task effects additionally share root_task_id. A profile without enforceable charge bounds cannot claim a hard cap.

Typed handler bindings return `contract.Outcome[O]` so accepted results and page cursors survive typed registration. The frozen Bind signature is `Bind[I, O any](contract.Descriptor, func(context.Context, contract.Unit, I) (contract.Outcome[O], error)) (contract.Handler, error)`. Errors carry Fault and roll back, while accepted/completed status and cursor come from Outcome.

Assembly order: application.NewPorts() returns an unbound *PortRouter; For(owner string) returns an owner-bound contract.Ports without domain calls; construct identity and all other modules with those ports; registry.New(modules); application.New(db, registry, identityAuthenticator, clock, ids); router.Bind(app) exactly once; start serving only after Bind. PortRouter exposes `For(string) contract.Ports` and `Bind(*Application) error`, rejects pre-bind invocation, and never allows a domain to change its bound caller identity. Registry also implements Module for its own capabilities methods. Constructor execution must not query peers or start goroutines.

All native BlobStore objects are encrypted at rest (including public-classification content), because Stage has no classification argument. Metadata publication sets encrypted=true; classification separately governs disclosure. Controller records the raw adapter observation and cost disposition durably first, then publishes StagedOutput bytes outside Unit, then commits artifact metadata and normalized owner callbacks through an outbox consumer. Publication failure is a visible artifact obligation and blocks task/job acceptance; it does not erase a confirmed provider observation or authorize resending. The adapter cannot mint artifact IDs.

Entrypoint assembly owns platform.Acquire -> storage.Open -> Migrate -> Database.StartGeneration, exactly once before controller/server admission. Pass the held contract.Ownership into controller.New; controller never acquires a second lock or advances generation again. Ownership.Lost closes when ownership is lost/released; stop admission immediately. Generation is a persisted local-controller fence, not distributed locking. The installation lock is retained until controller shutdown and DB close.

Remote TLS additionally validates the certificate chain and maps the SHA-256 certificate SPKI fingerprint through application.AuthenticateCertificate(ctx,Digest), which delegates to identity Authenticator.AuthenticateCertificate on a read snapshot. Only a verified TLS peer can enter that API; operation input cannot supply the fingerprint. Store certificate fingerprints as identity-owned authentication metadata on an explicitly provisioned credential reference. If a bearer credential is also supplied, its principal must match the verified certificate principal. No self-asserted certificate name, profile name or fingerprint header grants authority.

Every async operation declares completion_schema separately from its immediate Job response. job.get returns that schema as Job.result only when established; pending jobs omit result. Local IO mutations that may be recovered asynchronously accept either their completed resource output or `{job: Job}` in the descriptor output schema. Bootstrap remains synchronous under its exclusive one-time lock. No unspecified job kind may execute: _execution.job.create requires a registered completion_schema or a specifically registered private local-intent recovery contract.

Evidence retains the complete original result envelope for command replay, including fault message/details/retryability and cursor; projections of status/data/error_code must agree with Command.result. `_evidence.command.finish` takes that exact result. A replay cannot reconstruct a different error or lose an accepted job reference. ScopeRequired lists installation_id where input scope is required; resource-specific organization/project/worker conditions are enforced by the owned operation schemas and current referenced-resource validation, never inferred from a profile name.

Descriptor.Callers carries the exact catalog internal caller allowlist into runtime registration; application enforces this metadata rather than inventing it from operation names. Internal metadata remains available to trusted assembly/registry while public capability output contains public operations only. An empty allowlist never authorizes an internal call.

## Owned product requirements

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

### R7.2-002 (source section 7.2; primary owner connections)

A tool contract defines stable identity/version, schemas, read/disclosure/mutation classification, destinations, credential requirements, cost bounds, timeout, idempotency semantics, provider confirmations, and reconciliation behavior. Registered Go adapters execute trusted effects. A worker receives only the tools in its pinned binding closure.

### R7.2-003 (source section 7.2; primary owner connections)

Zatiti's MCP server exposes the management API to clients. Connecting to an external MCP server as a worker tool source is a separate integration capability. Discovery is not execution authorization: schema changes, redirects, authentication, callbacks and subprocess behavior need a reviewed adapter contract. V1 does not execute arbitrary external MCP servers or imported plugins merely because a client can describe one.

### R7.3-002 (source section 7.3; primary owner connections)

Creating a connection records provider kind, account identity, allowed scopes/destinations, and credential reference. It grants no worker access until binding and activation. `connection.validate` performs a separately authorized bounded probe and records observed identity/scopes and validation freshness. Credential rotation for the same account and substituting a different account are distinct operations.

### R7.3-003 (source section 7.3; primary owner connections)

Credential setup uses a typed challenge lifecycle: begin, status, complete, cancel. Both CLI and MCP can start and inspect setup; external consent may open a provider's browser flow. An existing OS-store or headless-store reference can be attached through either interface. A trusted helper consumes secret input locally and stores it; tool arguments, ordinary command flags, exported manifests, logs, and MCP results never carry raw provider secrets. Authorization codes and tokens are exchanged by the helper, not pasted into chat. When automation cannot finish a provider prerequisite, return `external_action_required` and an actionable challenge reference.

### R7.3-004 (source section 7.3; primary owner connections)

Connection and execution profiles may authorize disclosure to configured model providers. Repository and task content default to internal classification; lowering classification or expanding provider destinations requires current authorization. A missing connection, price bound, or model profile produces a named refusal; no silent provider or billing fallback.

### R10-002 (source section 10; primary owner effects)

External reads, model disclosure and mutations use registered contracts. Separate immutable **action** (exact intent), **operation** (logical effect) and **attempt** (physical invocation). An action binds account identity, destination, content/media hashes, timing, preconditions, and relevant configuration/tool versions. Reviews bind this digest. A changed action requires a new review where policy requires one.

### R10-003 (source section 10; primary owner effects)

Operation states are `prepared`, `awaiting_review`, `ready`, `executing`, `awaiting_confirmation`, `outcome_unknown`, `succeeded`, `failed`, and unsent terminal states `denied`, `expired`, `cancelled`. An operation is unsent only if no earlier attempt can still take effect. Provider acceptance and confirmed completion are distinct observations.

### R10-004 (source section 10; primary owner effects)

Execution uses three short local transactions around a network call:

### R10-005 (source section 10; primary owner effects)

1. **Admit:** validate current authority/review/preconditions, reserve all applicable budgets, create attempt and dispatch intent, and append evidence atomically.
2. **Claim:** the trusted sender consumes an attempt-bound claim after checking current generation, revocation and expiry. Workers never receive a reusable dispatch credential.
3. **Record:** after invocation, atomically store provider observations, outcome, settlement or retained uncertainty, and events. A failed record write does not authorize repeating the provider call.

### R10-006 (source section 10; primary owner effects)

Once dispatch is claimed, a crash or lost response can mean the provider acted. Recovery retains `outcome_unknown` and its reservation until supported evidence resolves it. An expired lease, timeout, cancellation, operator acknowledgement or eventually consistent not-found cannot establish non-execution. Revocation before claim prevents dispatch; revocation after claim cannot retract transmitted bytes.

### R10-007 (source section 10; primary owner effects)

Automatic mutation retries are disabled unless a qualified contract establishes request equivalence and safe idempotency within the provider's actual retention window, or authoritative non-execution. Each retry has current authorization and a separate attempt record. Disable hidden SDK retries that would bypass physical-call accounting. A failed retry cannot erase an earlier unknown attempt.

### R10-008 (source section 10; primary owner effects)

Reconciliation is a separately authorized, bounded read. Compensation and replacement are new linked operations with their own consequences and review, not a rewrite of the original outcome. Conflicting late evidence creates an explicit correction/dispute. The first GitHub adapter must bind repository, branch/head, exact change and relevant automation constraints; opening a pull request must not be treated as harmless when downstream automation can cause broader effects.
## Exact operation and dependency schemas

### `_accounting.inspect` v1 — accounting / internal / query / local

Allowed internal callers: policy, tasks, execution, effects, scheduling, installation. Submission key: not required at this internal/query/bootstrap boundary.

Return current intersected limits and honest usage to admission/doctor.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"limits":{"$ref":"#/$defs/Limits"},"usage":{"$ref":"#/$defs/Usage"}},"required":["limits","usage"]}
```

### `_accounting.reserve` v1 — accounting / internal / mutation / local

Allowed internal callers: effects, execution, tasks, scheduling. Submission key: not required at this internal/query/bootstrap boundary.

Reserve enforceable cost and concurrency in stable installation→ancestor organizations→project→worker→root order, inside caller transaction. All dimensions atomic; reject unknown price/advisory hard-cap claim.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"root_task_id":{"type":"string","format":"uuid"},"operation_id":{"type":"string","format":"uuid"},"amount":{"$ref":"#/$defs/Money"},"limits":{"$ref":"#/$defs/Limits"}},"required":["scope","operation_id","amount","limits"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Reservation"}},"required":["resource"]}
```

### `_accounting.settle` v1 — accounting / internal / mutation / local

Allowed internal callers: effects, execution, installation. Submission key: not required at this internal/query/bootstrap boundary.

Settle observed cost or retain unknown reservation; release only proven unused portion and conclusive no-effect/no-cost evidence. Check currency and overflow.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"reservation_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"usage":{"$ref":"#/$defs/Usage"},"authoritative_nonexecution":{"type":"boolean"}},"required":["reservation_id","expected_version","usage","authoritative_nonexecution"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Reservation"}},"required":["resource"]}
```

### `_artifacts.metadata` v1 — artifacts / internal / query / local

Allowed internal callers: execution, tasks, effects, memory, messaging, skills, installation. Submission key: not required at this internal/query/bootstrap boundary.

Validate scope, digests, availability and classification of pinned artifacts before disclosure or acceptance.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096}},"required":["scope","artifacts"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"artifacts":{"type":"array","items":{"$ref":"#/$defs/Artifact"},"maxItems":4096}},"required":["artifacts"]}
```

### `_configuration.snapshot` v1 — configuration / internal / query / local

Allowed internal callers: application, policy, tasks, execution, effects, memory, reviews, accounting, scheduling, messaging, connections, installation. Submission key: not required at this internal/query/bootstrap boundary.

Read current ancestry, effective bindings, worker/project and revision; no automatic descendant private data access.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/ScopeSnapshot"}},"required":["resource"]}
```

### `_connections.resolve` v1 — connections / internal / query / local

Allowed internal callers: effects, execution, memory, skills, configuration. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact validated account/tool/destination/binding and current revocation/freshness; return opaque credential reference only to trusted dispatcher.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"connection":{"$ref":"#/$defs/Ref"},"tool":{"$ref":"#/$defs/Ref"},"destination":{"type":"string","maxLength":8192}},"required":["scope","connection","tool","destination"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"connection":{"$ref":"#/$defs/Connection"},"tool":{"$ref":"#/$defs/Tool"}},"required":["connection","tool"]}
```

### `_connections.validation.record` v1 — connections / internal / mutation / local

Allowed internal callers: effects, controller. Submission key: not required at this internal/query/bootstrap boundary.

Record authorized probe identity/scopes/freshness and invalidation without accepting account substitution.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"connection_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"observation":{"$ref":"#/$defs/Observation"}},"required":["connection_id","expected_version","observation"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Connection"}},"required":["resource"]}
```

### `_effects.admit` v1 — effects / internal / mutation / local

Allowed internal callers: controller, execution, memory, skills, connections. Submission key: not required at this internal/query/bootstrap boundary.

Atomic current policy/review/preconditions, complete budget reservation, physical attempt/dispatch intent and outbox event. Operation action is immutable.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"operation_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["operation_id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Operation"}},"required":["resource"]}
```

### `_effects.claim` v1 — effects / internal / mutation / local

Allowed internal callers: controller. Submission key: not required at this internal/query/bootstrap boundary.

Consume one-use attempt claim after current generation, revocation, expiry, restriction and action checks. Dispatch never returned to cooperative workers.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"operation_id":{"type":"string","format":"uuid"},"attempt_id":{"type":"string","format":"uuid"},"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["operation_id","attempt_id","generation"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Dispatch"}},"required":["resource"]}
```

### `_effects.pending` v1 — effects / internal / query / local

Allowed internal callers: controller, installation. Submission key: not required at this internal/query/bootstrap boundary.

List pending intents/confirmation/reconciliation obligations; claimed attempts after restart become unknown, not ready for resend.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"limit":{"type":"integer","minimum":1,"maximum":100}},"required":["limit"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"operations":{"type":"array","items":{"$ref":"#/$defs/Operation"},"maxItems":100}},"required":["operations"]}
```

### `_effects.prepare` v1 — effects / internal / mutation / local

Allowed internal callers: execution, memory, connections, skills, installation. Submission key: not required at this internal/query/bootstrap boundary.

Persist immutable action and logical effect for hosted steps/memory/probes/evaluation; required decisions produce awaiting_review. No physical call here.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"action":{"$ref":"#/$defs/Action"},"source_id":{"type":"string","format":"uuid"}},"required":["scope","action","source_id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Operation"}},"required":["resource"]}
```

### `_effects.record` v1 — effects / internal / mutation / local

Allowed internal callers: controller. Submission key: not required at this internal/query/bootstrap boundary.

Store actual provider observation plus cost settlement/uncertainty atomically. Lost record is recoverable without blind resend. Contradictory late evidence records correction/dispute.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"operation_id":{"type":"string","format":"uuid"},"attempt_id":{"type":"string","format":"uuid"},"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807},"observation":{"$ref":"#/$defs/Observation"}},"required":["operation_id","attempt_id","generation","observation"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Operation"}},"required":["resource"]}
```

### `_execution.job.create` v1 — execution / internal / mutation / local

Allowed internal callers: configuration, skills, connections, memory, artifacts, installation, execution, effects, application. Submission key: not required at this internal/query/bootstrap boundary.

Validate owner/operation against catalog and original input schema; deduplicate source identity with canonical input hash. No arbitrary executable jobs.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"owner":{"type":"string","maxLength":8192},"operation":{"type":"string","maxLength":8192},"input":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"source_id":{"type":"string","format":"uuid"}},"required":["scope","owner","operation","input","source_id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

### `_execution.job.record` v1 — execution / internal / mutation / local

Allowed internal callers: controller, application, effects, memory, skills, connections, installation. Submission key: not required at this internal/query/bootstrap boundary.

Record real result conforming originating operation schema, preserve unknown obligations and actual evidence; result changes require current claim.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"job_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807},"state":{"type":"string","enum":["pending","running","succeeded","failed","outcome_unknown","cancelled"]},"result":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"evidence_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096}},"required":["job_id","expected_version","generation","state","result","evidence_ids"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
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

### `_memory.select` v1 — memory / internal / query / local

Allowed internal callers: execution, effects, configuration. Submission key: not required at this internal/query/bootstrap boundary.

Filter current authorized brains before retrieval; parentage/group membership grants no access. Unavailable freshness creates prerequisite failure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"binding_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"permission":{"type":"string","enum":["read","write","curate","promote","retract"]},"minimum_freshness":{"type":"string","format":"date-time"}},"required":["scope","binding_ids","permission","minimum_freshness"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"bindings":{"type":"array","items":{"$ref":"#/$defs/MemoryBinding"},"maxItems":4096}},"required":["bindings"]}
```

### `_policy.check` v1 — policy / internal / query / local

Allowed internal callers: application, configuration, tasks, execution, effects, memory, messaging, connections, installation, reviews, accounting. Submission key: not required at this internal/query/bootstrap boundary.

Intersect authenticated current grants, ancestry/bindings, task/worker scope, policy, restrictions and required conditions. Explicit deny wins; unknown required conditions fail closed. Check exact human review requirements without accepting user assertions.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"capability":{"type":"string","maxLength":8192},"action":{"$ref":"#/$defs/Action"},"candidate_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["scope","capability"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/PolicyResult"}},"required":["resource"]}
```

### `_reviews.check` v1 — reviews / internal / query / local

Allowed internal callers: effects, configuration, policy, tasks. Submission key: not required at this internal/query/bootstrap boundary.

Recheck current eligible reviewer/grants, principal kind, expiry, version, proposer separation and exact action digest; false is not permission.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["scope","action_digest"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"eligible":{"type":"boolean"},"decision":{"$ref":"#/$defs/Decision"}},"required":["eligible"]}
```

### `_reviews.ensure` v1 — reviews / internal / mutation / local

Allowed internal callers: effects, configuration, policy. Submission key: not required at this internal/query/bootstrap boundary.

Create or inspect exact digest-bound review, retaining immutable history and eligibility constraints.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"action":{"$ref":"#/$defs/Action"},"requirement":{"$ref":"#/$defs/DecisionRequirement"}},"required":["scope","action","requirement"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Review"}},"required":["resource"]}
```

### `_tasks.snapshot` v1 — tasks / internal / query / local

Allowed internal callers: execution, effects, scheduling, memory, reviews, policy. Submission key: not required at this internal/query/bootstrap boundary.

Return current pinned contract and task scope, parent/root/dependency state; caller still obeys authority.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Task"}},"required":["resource"]}
```

### `operation.compensation.propose` v1 — effects / public / mutation / local

CLI `zatiti operation compensation propose`; MCP `zatiti_operation_compensation_propose`. Submission key: required.

New linked compensation operation with own review/budget and consequences; original history remains unchanged.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"action":{"$ref":"#/$defs/Action"}},"required":["scope","id","expected_version","action"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Operation"}},"required":["resource"]}
```

### `operation.get` v1 — effects / public / query / local

CLI `zatiti operation get`; MCP `zatiti_operation_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Operation"}},"required":["resource"]}
```

### `operation.list` v1 — effects / public / query / local

CLI `zatiti operation list`; MCP `zatiti_operation_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Operation"},"maxItems":500}},"required":["items"]}
```

### `operation.propose` v1 — effects / public / mutation / local

CLI `zatiti operation propose`; MCP `zatiti_operation_propose`. Submission key: required.

Canonicalize immutable exact action, resolve current prerequisites/policy and create logical operation. No provider call in handler.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"action":{"$ref":"#/$defs/Action"}},"required":["scope","action"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Operation"}},"required":["resource"]}
```

### `operation.reconcile` v1 — effects / public / mutation / external_read

CLI `zatiti operation reconcile`; MCP `zatiti_operation_reconcile`. Submission key: required.

Create separately admitted bounded reconciliation read; retain original unknown outcome and reservation until supported evidence resolves it.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Operation"}},"required":["resource"]}
```

### `operation.replacement.propose` v1 — effects / public / mutation / local

CLI `zatiti operation replacement propose`; MCP `zatiti_operation_replacement_propose`. Submission key: required.

New linked replacement operation with own review/budget and consequences; original history remains unchanged.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"action":{"$ref":"#/$defs/Action"}},"required":["scope","id","expected_version","action"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Operation"}},"required":["resource"]}
```

### Local schema definitions

The schemas above resolve exclusively against this embedded `$defs` object. Input objects reject additional properties except explicitly open schema/data fields. Field semantics are completed by the owned requirements and operation descriptions. Output `resource`, `items`, `draft`, `job`, etc. are literal keys. Pagination cursor lives in the common envelope.

```json
{"$defs":{"Acceptance":{"type":"object","additionalProperties":false,"properties":{"verifier_id":{"type":"string","maxLength":8192},"verifier_version":{"type":"string","maxLength":8192},"sealed_inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"expected_observations":{"type":"array","items":{"$ref":"#/$defs/Adapter_ExpectedVerificationObservation"},"maxItems":512},"mode":{"type":"string","enum":["independent","manual"]},"required_child_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"profile":{"$ref":"#/$defs/Adapter_VerificationProfile"}},"required":["verifier_id","verifier_version","sealed_inputs","expected_observations","mode","required_child_ids","profile"]},"Action":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"tool":{"$ref":"#/$defs/Ref"},"connection":{"$ref":"#/$defs/Ref"},"account_identity":{"type":"string","maxLength":8192},"destination":{"type":"string","maxLength":8192},"content":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"not_before":{"type":"string","format":"date-time"},"expires_at":{"type":"string","format":"date-time"},"preconditions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"configuration_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"parameters":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"cost_bound":{"$ref":"#/$defs/Money"}},"required":["scope","tool","connection","account_identity","destination","content","not_before","expires_at","preconditions","configuration_revision","parameters","cost_bound"]},"Adapter_ArtifactRef":{"type":"object","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/Adapter_ID"},"digest":{"$ref":"#/$defs/Adapter_Digest"}},"required":["id","digest"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ArtifactVerifierProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verifier-profile/v1","type":"string"},"kind":{"const":"artifact_contract","type":"string"},"id":{"type":"string","minLength":1,"maxLength":256},"version":{"type":"string","minLength":1,"maxLength":128},"code_digest":{"$ref":"#/$defs/Adapter_Digest"},"supported_checks":{"type":"array","items":{"type":"string","enum":["presence","digest","json_schema"]},"minItems":1,"maxItems":3},"max_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"capability_evidence":{"$ref":"#/$defs/Adapter_CapabilityEvidence"}},"required":["schema","kind","id","version","code_digest","supported_checks","max_bytes","timeout_seconds","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_CapabilityEvidence":{"type":"object","additionalProperties":false,"properties":{"artifact":{"$ref":"#/$defs/Adapter_ArtifactRef"},"adapter_version":{"type":"string","minLength":1,"maxLength":128},"source_revision":{"type":"string","minLength":1,"maxLength":128},"protocol_revision":{"type":"string","minLength":1,"maxLength":128},"profile_digest":{"$ref":"#/$defs/Adapter_Digest"},"qualified_at":{"$ref":"#/$defs/Adapter_UTC"},"capabilities":{"type":"array","items":{"type":"string","minLength":1,"maxLength":128},"minItems":0,"maxItems":128},"limitations":{"type":"array","items":{"type":"string","minLength":1,"maxLength":2048},"minItems":0,"maxItems":128}},"required":["artifact","adapter_version","source_revision","protocol_revision","profile_digest","qualified_at","capabilities","limitations"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_Digest":{"type":"string","pattern":"^[0-9a-f]{64}$","maxLength":64,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ExpectedVerificationObservation":{"type":"object","additionalProperties":false,"properties":{"check_id":{"type":"string","minLength":1,"maxLength":256},"kind":{"type":"string","enum":["artifact_presence","artifact_digest","json_schema","repository_patch_applies","repository_command"]},"expected":{"type":"string","enum":["pass","fail"]},"artifact_name":{"type":"string","minLength":1,"maxLength":128},"expected_digest":{"$ref":"#/$defs/Adapter_Digest"},"schema":{"$ref":"#/$defs/Adapter_InertSchema"},"command_id":{"type":"string","minLength":1,"maxLength":256},"expected_exit_code":{"type":"integer","minimum":-2147483648,"maximum":2147483647}},"required":["check_id","kind","expected"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ID":{"type":"string","format":"uuid","maxLength":36,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_InertSchema":{"type":"object","description":"A bounded JSON Schema 2020-12 document. Local references only; validators reject remote references, duplicate keys, excessive depth/node count and unsupported executable behavior. This is schema data, not authority.","maxProperties":256,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_RepositoryVerifierProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verifier-profile/v1","type":"string"},"kind":{"const":"repository_patch","type":"string"},"id":{"type":"string","minLength":1,"maxLength":256},"version":{"type":"string","minLength":1,"maxLength":128},"code_digest":{"$ref":"#/$defs/Adapter_Digest"},"runner_profile":{"type":"string","minLength":1,"maxLength":256},"command_id":{"type":"string","minLength":1,"maxLength":256},"command_digest":{"$ref":"#/$defs/Adapter_Digest"},"environment_profile":{"type":"string","minLength":1,"maxLength":256},"network":{"type":"string","enum":["disabled","qualified_allowlist"]},"max_output_bytes":{"type":"integer","minimum":1,"maximum":1048576},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"capability_evidence":{"$ref":"#/$defs/Adapter_CapabilityEvidence"}},"required":["schema","kind","id","version","code_digest","runner_profile","command_id","command_digest","environment_profile","network","max_output_bytes","timeout_seconds","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_UTC":{"type":"string","format":"date-time","pattern":"Z$","maxLength":40,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_VerificationProfile":{"oneOf":[{"$ref":"#/$defs/Adapter_ArtifactVerifierProfile"},{"$ref":"#/$defs/Adapter_RepositoryVerifierProfile"}],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Artifact":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"size":{"type":"integer","minimum":0,"maximum":9223372036854775807},"media_type":{"type":"string","maxLength":8192},"classification":{"type":"string","enum":["internal","public","restricted"]},"encrypted":{"type":"boolean"},"state":{"type":"string","enum":["available","fault"]},"created_at":{"type":"string","format":"date-time"}},"required":["id","version","scope","digest","size","media_type","classification","encrypted","state","created_at"]},"ArtifactRef":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["id","digest"]},"Authority":{"type":"object","additionalProperties":false,"properties":{"principal":{"$ref":"#/$defs/Principal"},"grants":{"type":"array","items":{"$ref":"#/$defs/Grant"},"maxItems":4096},"restrictions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096}},"required":["principal","grants","restrictions"]},"Binding":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"kind":{"type":"string","enum":["tool","skill","connection","worker","repository","reporting","memory"]},"target_id":{"type":"string","format":"uuid"},"permissions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"source_scope":{"$ref":"#/$defs/Scope"},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096}},"required":["id","version","scope","kind","target_id","permissions"]},"Connection":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"provider":{"type":"string","maxLength":8192},"account_identity":{"type":"string","maxLength":8192},"credential_ref":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"allowed_scopes":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"validation_state":{"type":"string","enum":["unverified","valid","invalid","expired","revoked"]},"validated_at":{"type":"string","format":"date-time"},"valid_until":{"type":"string","format":"date-time"}},"required":["id","version","scope","provider","account_identity","credential_ref","destinations","allowed_scopes","validation_state"]},"Decision":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"review_id":{"type":"string","format":"uuid"},"review_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"reviewer_id":{"type":"string","format":"uuid"},"decision":{"type":"string","enum":["approve","reject"]},"at":{"type":"string","format":"date-time"},"reason":{"type":"string","maxLength":8192}},"required":["id","review_id","review_version","action_digest","reviewer_id","decision","at","reason"]},"DecisionRequirement":{"type":"object","additionalProperties":false,"properties":{"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"human_required":{"type":"boolean"},"eligible_principals":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"expires_at":{"type":"string","format":"date-time"},"separate_proposer":{"type":"boolean"}},"required":["action_digest","human_required","eligible_principals","expires_at","separate_proposer"]},"Dispatch":{"type":"object","additionalProperties":false,"properties":{"operation_id":{"type":"string","format":"uuid"},"attempt_id":{"type":"string","format":"uuid"},"generation":{"type":"integer","minimum":1,"maximum":9223372036854775807},"adapter":{"type":"string","maxLength":8192},"action":{"$ref":"#/$defs/Action"},"credential_ref":{"type":"string","maxLength":8192},"deadline":{"type":"string","format":"date-time"},"provider_key":{"type":"string","maxLength":8192}},"required":["operation_id","attempt_id","generation","adapter","action","credential_ref","deadline"]},"ExecutionProfile":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"executor":{"type":"string","enum":["hosted","cooperative"]},"model":{"type":"string","maxLength":8192},"connection_id":{"type":"string","format":"uuid"},"provider_destination":{"type":"string","maxLength":8192},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"cost_bound":{"$ref":"#/$defs/Money"},"classification":{"type":"string","enum":["internal","public","restricted"]},"context_capture":{"type":"string","enum":["complete","partial","advisory"]}},"required":["id","version","executor","model","connection_id","provider_destination","capabilities","cost_bound","classification","context_capture"]},"Grant":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"principal_id":{"type":"string","format":"uuid"},"scope":{"$ref":"#/$defs/Scope"},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"denied":{"type":"boolean"},"expires_at":{"type":"string","format":"date-time"},"parent_grant_id":{"type":"string","format":"uuid"}},"required":["id","version","principal_id","scope","capabilities","destinations","denied"]},"Job":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"kind":{"type":"string","maxLength":8192},"state":{"type":"string","enum":["pending","running","succeeded","failed","outcome_unknown","cancelled"]},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096},"result_artifact":{"$ref":"#/$defs/ArtifactRef"},"operation_id":{"type":"string","format":"uuid"},"owner":{"type":"string","maxLength":8192},"operation":{"type":"string","maxLength":8192},"result":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","kind","state","requirements","owner","operation"]},"Limits":{"type":"object","additionalProperties":false,"properties":{"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"spend_micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807},"concurrency":{"type":"integer","minimum":1,"maximum":9223372036854775807},"model_steps":{"type":"integer","minimum":1,"maximum":9223372036854775807},"child_count":{"type":"integer","minimum":0,"maximum":9223372036854775807},"delegation_depth":{"type":"integer","minimum":0,"maximum":9223372036854775807},"attempt_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"root_deadline":{"type":"string","format":"date-time"}},"required":["currency","spend_micro_units","concurrency","model_steps","child_count","delegation_depth","attempt_seconds","root_deadline"]},"MemoryBinding":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"brain_id":{"type":"string","format":"uuid"},"permissions":{"type":"array","items":{"type":"string","enum":["read","write","curate","promote","retract"]},"maxItems":4096},"classification":{"type":"string","enum":["internal","public","restricted"]}},"required":["id","version","scope","brain_id","permissions","classification"]},"Money":{"type":"object","additionalProperties":false,"properties":{"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807}},"required":["currency","micro_units"]},"Observation":{"type":"object","additionalProperties":false,"properties":{"disposition":{"type":"string","enum":["succeeded","failed","accepted","unknown","not_sent"]},"evidence":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"usage":{"$ref":"#/$defs/Usage"},"provider_reference":{"type":"string","maxLength":8192},"confirmed_at":{"type":"string","format":"date-time"}},"required":["disposition","evidence","usage"]},"Operation":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"action":{"$ref":"#/$defs/Action"},"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"state":{"type":"string","enum":["prepared","awaiting_review","ready","executing","awaiting_confirmation","outcome_unknown","succeeded","failed","denied","expired","cancelled"]},"attempt_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"linked_operation_id":{"type":"string","format":"uuid"},"relationship":{"type":"string","enum":["retry","reconciliation","compensation","replacement"]}},"required":["id","version","action","action_digest","state","attempt_ids"]},"Organization":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"chief_id":{"type":"string","format":"uuid"},"parent_id":{"type":"string","format":"uuid"},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","key","name","chief_id"]},"PolicyResult":{"type":"object","additionalProperties":false,"properties":{"decision":{"type":"string","enum":["allow","deny","review","prerequisite_missing"]},"reasons":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"requirements":{"type":"array","items":{"$ref":"#/$defs/DecisionRequirement"},"maxItems":4096}},"required":["decision","reasons","requirements"]},"Principal":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"kind":{"type":"string","enum":["human","client_agent","worker","service"]},"name":{"type":"string","maxLength":8192},"scope":{"$ref":"#/$defs/Scope"},"revoked":{"type":"boolean"}},"required":["id","version","kind","name","scope","revoked"]},"Project":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"repositories":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"classification":{"type":"string","enum":["internal","public","restricted"]},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","organization_id","key","name","repositories","bindings","classification"]},"Ref":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["id","version"]},"Requirement":{"type":"object","additionalProperties":false,"properties":{"code":{"type":"string","maxLength":8192},"message":{"type":"string","maxLength":8192},"resource_id":{"type":"string","format":"uuid"},"challenge_id":{"type":"string","format":"uuid"}},"required":["code","message"]},"Reservation":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"root_task_id":{"type":"string","format":"uuid"},"operation_id":{"type":"string","format":"uuid"},"amount":{"$ref":"#/$defs/Money"},"state":{"type":"string","enum":["reserved","settled","unknown","released"]}},"required":["id","version","scope","operation_id","amount","state"]},"Review":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"preview":{"$ref":"#/$defs/Action"},"requirement":{"$ref":"#/$defs/DecisionRequirement"},"proposer_id":{"type":"string","format":"uuid"},"state":{"type":"string","enum":["pending","approved","rejected","expired","invalidated"]},"decision_id":{"type":"string","format":"uuid"}},"required":["id","version","scope","action_digest","preview","requirement","proposer_id","state"]},"Scope":{"type":"object","additionalProperties":false,"properties":{"installation_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"project_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"}},"required":["installation_id"]},"ScopeSnapshot":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"ancestors":{"type":"array","items":{"$ref":"#/$defs/Organization"},"maxItems":4096},"bindings":{"type":"array","items":{"$ref":"#/$defs/Binding"},"maxItems":4096},"worker":{"$ref":"#/$defs/Worker"},"project":{"$ref":"#/$defs/Project"}},"required":["scope","revision","ancestors","bindings"]},"Task":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"owner_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"outcome":{"type":"string","maxLength":8192},"inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"required_outputs":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"acceptance":{"$ref":"#/$defs/Acceptance"},"limits":{"$ref":"#/$defs/Limits"},"dependencies":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"state":{"type":"string","enum":["draft","ready","running","waiting","verifying","succeeded","failed","cancelled"]},"parent_id":{"type":"string","format":"uuid"},"root_id":{"type":"string","format":"uuid"},"waiting_reason":{"type":"string","maxLength":8192},"cancellation_requested":{"type":"boolean"},"manual_acceptance":{"type":"boolean"}},"required":["id","version","scope","owner_id","worker_id","outcome","inputs","required_outputs","acceptance","limits","dependencies","state"]},"Tool":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"name":{"type":"string","maxLength":8192},"input_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"output_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"effect":{"type":"string","enum":["local","disclosure","external_read","external_mutation"]},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"credential_kind":{"type":"string","maxLength":8192},"cost_bound":{"$ref":"#/$defs/Money"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"idempotency":{"type":"string","enum":["none","qualified_key","authoritative_nonexecution"]},"key_retention_seconds":{"type":"integer","minimum":0,"maximum":9223372036854775807},"confirmation":{"type":"string","enum":["synchronous","asynchronous","advisory"]},"reconciliation":{"type":"string","maxLength":8192},"adapter":{"type":"string","maxLength":8192}},"required":["id","version","name","input_schema","output_schema","effect","destinations","credential_kind","cost_bound","timeout_seconds","idempotency","key_retention_seconds","confirmation","reconciliation","adapter"]},"Usage":{"type":"object","additionalProperties":false,"properties":{"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"spent":{"type":"integer","minimum":0,"maximum":9223372036854775807},"reserved":{"type":"integer","minimum":0,"maximum":9223372036854775807},"estimated":{"type":"integer","minimum":0,"maximum":9223372036854775807},"unknown":{"type":"integer","minimum":0,"maximum":9223372036854775807},"advisory":{"type":"boolean"}},"required":["currency","spent","reserved","estimated","unknown","advisory"]},"Worker":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"purpose":{"type":"string","maxLength":8192},"instructions":{"type":"string","maxLength":8192},"skill_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"profile":{"anyOf":[{"$ref":"#/$defs/ExecutionProfile"},{"type":"null"}]},"limits":{"anyOf":[{"$ref":"#/$defs/Limits"},{"type":"null"}]},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","organization_id","key","name","purpose","instructions","skill_versions","bindings","profile","limits"]}}}
```

## Local adapter, context, verifier and backup payloads

These local schemas freeze the handoff between execution, adapters and artifact publication. They do not assert upstream compatibility.

These are exact Zatiti-side contracts, not representations of upstream APIs. Adapter implementation translates to a pinned, separately qualified public upstream protocol; unsupported semantics fail capability_unsupported or preserve outcome_unknown.
To validate a named schema, place definitions under the root $defs and reference the selected name. The catalog container uses definitions for packaging only; every embedded #/$defs/Name resolves against that assembled validation document. JSON Schema dialect is 2020-12.
Validate strict objects, formats, explicit field limits, duplicate-key rejection and exact integer bounds. Runtime must also enforce total JSON/artifact byte limits, at most 64 JSON levels and 100000 schema/input nodes; schema documents and dynamic tool arguments cannot escape the qualified local validation dialect.
Opaque references, URLs, headers and diagnostics must never contain secret bytes. Profile configuration is trusted local configuration; public operation arguments cannot install profiles, select a more privileged credential, open an arbitrary server path or replace capability evidence.
Before dispatch, profile capability evidence must match the canonical profile digest computed with the capability_evidence field omitted; self-referential capability evidence and qualification records do not grant authority. The binding/effect owner independently checks current account, scope, destinations, classification, prices and qualified capability.
PhysicalCallEvidence records exactly one physical network request per claimed attempt. request_context identifies the pre-dispatch persisted request. No adapter adds preflight, redirect, polling, SDK retry or model tool execution inside that request; each such external action requires a separate governed attempt.
StagedOutput is an IO handoff, not a published ArtifactRef. Adapter stages bytes and returns staging_ref/digest metadata; controller uses the artifacts owner to publish metadata and replace staged locators with real ArtifactRefs before delivering normalized observations to execution/memory/other domains. output_artifacts may be empty at adapter return and contains the resulting published refs in recorded domain evidence. ArtifactLocator staged references must match exactly one StagedOutput in the same observation. Failed publication remains a visible obligation/fault.
ContextArtifact.messages and each parts array preserve the exact model-visible semantic order. Tool definitions, memory excerpts and injected messages must all appear in the persisted request context before dispatch. Context source artifacts and compaction lineage do not replace retaining the actual selected text. Complete capture is required for owned hosted loops; external capture limitations remain explicit.
Model tool proposals are observations. tool ID/version must match the pinned context tool closure; operation ID/version must be the qualified mapping for that tool. Validate input against that exact schema and apply current authority/review/budgets before a separate operation. Never execute a model proposal directly in an adapter.
Responses input_rate.unit must equal input_token and output_rate.unit output_token; profile currencies and all reported/reserved usage must agree. Round admission reservations upward using checked integer rational arithmetic. Missing or unbounded required charges refuse hard-cap mode. A null/absent upstream usage report maps to unknown/advisory billing, never free success.
GitHubParameters is discriminated by kind. read_repository requires branch for ref, sha for commit/tree/blob, and pull_request_number for pull_request; irrelevant resource selectors are rejected. Relative repository paths and branch syntax receive Git-aware validation. Profile allowed_actions must be separately qualified against real API preconditions and downstream automation.
GitHub push_commit publishes an already prepared exact commit SHA in one qualified physical request. Creating remote blobs, trees or commits is not hidden inside Invoke: it needs an explicit separately admitted decomposition and qualification before enabling that route. Preflight evidence cannot substitute for atomic provider preconditions where the action requires protection against a race; refuse unsupported guarantees.
GitHub open_pull_request and merge_pull_request are consequential effects bound to exact account/repository/head/base/content/timing/automation constraints. A successful HTTP response or accepted request is not independently assumed confirmed completion; response interpretation follows the qualified contract. Eventual not-found never proves nonexecution.
HTTPRead v1 max_redirects is exactly zero. Return redirect_location as evidence/prerequisite and require a separately authorized read for the new URL. Validate DNS results and bind validated dial addresses; reject local/private/link-local/metadata targets unless explicitly permitted by an installation-authorized destination binding. Header values reject controls and duplicate header names; Authorization, Cookie and arbitrary raw credential input are excluded.
Serenity profile endpoints/root_ref/writer_owner are opaque locally provisioned mappings, not model-selectable arbitrary filesystem locations. They identify real pinned public protocol/read-facade support. Zatiti never imports upstream internal packages or invents public endpoints from these local operation names.
Serenity recall/remember/promote may incur disclosure or charges. All nested upstream provider destinations and spend must fit enforcement; unsupported enforceable bounds disable the mode or require explicitly permitted advisory use. Promote resolves and authorizes source content before preparing the destination write and retains the source evidence; it does not perform an unaccounted extra source read inside Invoke.
Serenity adapter_command_id is the durable pre-dispatch command identity. Its existence does not prove upstream idempotency: reconcile through qualified status semantics, retaining unknown after lost acknowledgement if authoritative lookup is unavailable. One writer owner per brain. Retract removes active recall and leaves historical_erasure false; promotion correction lineage remains a durable memory-owner obligation.
Verification profiles are installation-owned and pinned before task execution. command_id, runner_profile and environment_profile resolve only to prequalified local verifiers; no request supplies executable paths or argv. ArtifactVerifierProfile runs presence/digest/schema checks; RepositoryVerifierProfile runs the pinned controlled patch/check profile outside Unit. A worker cannot alter profile bytes, accepted inputs or result authority.
VerificationResult.independent=true is necessary structure, not trust evidence: only the trusted runner may submit that result through its internal admission identity. Match task/attempt, acceptance digest, verifier identity/version/digest and request artifact before accepting. Passed requires all sealed expected checks observed as required; unavailable, interrupted, worker assertion and process exit zero alone cannot succeed a task. Manual acceptance uses the separate task operation and never fabricates an independent result.
BackupManifest is inside an authenticated encrypted bundle; it never includes decrypting master keys. archive_entry values are normalized relative entries with no traversal, absolute paths, symlinks, duplicate/case collisions or devices. Verify database/artifact/brain digests and qualified revision exports before marking backup verified. Large manifests stream through bounded artifact IO, not public JSON request bodies.
Restore starts paused under exclusive maintenance, validates installation binding and key prerequisites, and retains current recovery overlay before rewinding. Merge revocations, durable command identities, claimed/unknown effects, reservations and memory obligations monotonically via their owners; fail closed on conflict and retain the overlay. Overlay record artifacts must be included/pinned and encrypted. No database/brain rewind is represented as undoing an external effect.

```json
{"$defs":{"ID":{"type":"string","format":"uuid","maxLength":36,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Digest":{"type":"string","pattern":"^[0-9a-f]{64}$","maxLength":64,"$schema":"https://json-schema.org/draft/2020-12/schema"},"UTC":{"type":"string","format":"date-time","pattern":"Z$","maxLength":40,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Version":{"type":"integer","minimum":1,"maximum":9223372036854775807,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Currency":{"type":"string","pattern":"^[A-Z]{3}$","maxLength":3,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Classification":{"type":"string","enum":["public","internal","restricted"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"HTTPSURL":{"type":"string","format":"uri","pattern":"^https://","maxLength":4096,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Scope":{"type":"object","additionalProperties":false,"properties":{"installation_id":{"$ref":"#/$defs/ID"},"organization_id":{"$ref":"#/$defs/ID"},"project_id":{"$ref":"#/$defs/ID"},"worker_id":{"$ref":"#/$defs/ID"},"task_id":{"$ref":"#/$defs/ID"}},"required":["installation_id"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"VersionRef":{"type":"object","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/ID"},"version":{"$ref":"#/$defs/Version"}},"required":["id","version"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ArtifactRef":{"type":"object","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/ID"},"digest":{"$ref":"#/$defs/Digest"}},"required":["id","digest"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Money":{"type":"object","additionalProperties":false,"properties":{"currency":{"$ref":"#/$defs/Currency"},"micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807}},"required":["currency","micro_units"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Usage":{"type":"object","additionalProperties":false,"properties":{"currency":{"$ref":"#/$defs/Currency"},"spent":{"type":"integer","minimum":0,"maximum":9223372036854775807},"reserved":{"type":"integer","minimum":0,"maximum":9223372036854775807},"estimated":{"type":"integer","minimum":0,"maximum":9223372036854775807},"unknown":{"type":"integer","minimum":0,"maximum":9223372036854775807},"advisory":{"type":"boolean"}},"required":["currency","spent","reserved","estimated","unknown","advisory"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"RationalRate":{"type":"object","additionalProperties":false,"properties":{"numerator_micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807},"denominator_units":{"type":"integer","minimum":1,"maximum":9223372036854775807},"unit":{"type":"string","enum":["input_token","output_token","request","byte","second"]}},"required":["numerator_micro_units","denominator_units","unit"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"CapabilityEvidence":{"type":"object","additionalProperties":false,"properties":{"artifact":{"$ref":"#/$defs/ArtifactRef"},"adapter_version":{"type":"string","minLength":1,"maxLength":128},"source_revision":{"type":"string","minLength":1,"maxLength":128},"protocol_revision":{"type":"string","minLength":1,"maxLength":128},"profile_digest":{"$ref":"#/$defs/Digest"},"qualified_at":{"$ref":"#/$defs/UTC"},"capabilities":{"type":"array","items":{"type":"string","minLength":1,"maxLength":128},"minItems":0,"maxItems":128},"limitations":{"type":"array","items":{"type":"string","minLength":1,"maxLength":2048},"minItems":0,"maxItems":128}},"required":["artifact","adapter_version","source_revision","protocol_revision","profile_digest","qualified_at","capabilities","limitations"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"BoundEnforcement":{"type":"object","additionalProperties":false,"properties":{"cost":{"type":"string","enum":["enforced","advisory","unsupported"]},"disclosure":{"type":"string","enum":["enforced","advisory","unsupported"]},"maximum_cost":{"$ref":"#/$defs/Money"},"provider_destinations":{"type":"array","items":{"$ref":"#/$defs/HTTPSURL"},"minItems":0,"maxItems":64},"classifications":{"type":"array","items":{"$ref":"#/$defs/Classification"},"minItems":1,"maxItems":3},"evidence":{"$ref":"#/$defs/CapabilityEvidence"}},"required":["cost","disclosure","maximum_cost","provider_destinations","classifications","evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"StagedOutput":{"type":"object","additionalProperties":false,"properties":{"staging_ref":{"type":"string","minLength":1,"maxLength":256},"digest":{"$ref":"#/$defs/Digest"},"size":{"type":"integer","minimum":0,"maximum":268435456},"media_type":{"type":"string","minLength":1,"maxLength":256},"classification":{"$ref":"#/$defs/Classification"},"purpose":{"type":"string","enum":["provider_response","model_text","context","tool_result","repository","public_source","memory","verification","backup","export"]}},"required":["staging_ref","digest","size","media_type","classification","purpose"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ArtifactLocator":{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"artifact","type":"string"},"artifact":{"$ref":"#/$defs/ArtifactRef"}},"required":["kind","artifact"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"staged","type":"string"},"staging_ref":{"type":"string","minLength":1,"maxLength":256},"digest":{"$ref":"#/$defs/Digest"}},"required":["kind","staging_ref","digest"]}],"$schema":"https://json-schema.org/draft/2020-12/schema"},"PhysicalCallEvidence":{"type":"object","additionalProperties":false,"properties":{"operation_id":{"$ref":"#/$defs/ID"},"attempt_id":{"$ref":"#/$defs/ID"},"account_identity":{"type":"string","minLength":1,"maxLength":512},"requested_destination":{"type":"string","minLength":1,"maxLength":4096},"resolved_destination":{"type":"string","minLength":1,"maxLength":4096},"profile_digest":{"$ref":"#/$defs/Digest"},"capability_evidence":{"$ref":"#/$defs/ArtifactRef"},"started_at":{"$ref":"#/$defs/UTC"},"finished_at":{"$ref":"#/$defs/UTC"},"request_context":{"$ref":"#/$defs/ArtifactRef"},"request_sent":{"type":"string","enum":["no","yes","unknown"]},"confirmation":{"type":"string","enum":["authoritative_success","authoritative_failure","provider_accepted","authoritative_nonexecution","unknown"]},"http_status":{"type":"integer","minimum":100,"maximum":599},"provider_reference":{"type":"string","minLength":0,"maxLength":1024},"error_code":{"type":"string","minLength":0,"maxLength":128},"error_message":{"type":"string","minLength":0,"maxLength":2048}},"required":["operation_id","attempt_id","account_identity","requested_destination","resolved_destination","profile_digest","capability_evidence","started_at","finished_at","request_context","request_sent","confirmation"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ProviderUsage":{"type":"object","additionalProperties":false,"properties":{"accounting":{"$ref":"#/$defs/Usage"},"billing":{"type":"string","enum":["observed","bounded_estimate","unknown","advisory","no_charge"]},"input_tokens":{"type":"integer","minimum":0,"maximum":9223372036854775807},"output_tokens":{"type":"integer","minimum":0,"maximum":9223372036854775807},"input_rate":{"$ref":"#/$defs/RationalRate"},"output_rate":{"$ref":"#/$defs/RationalRate"},"provider_usage_reference":{"type":"string","minLength":0,"maxLength":1024}},"required":["accounting","billing"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"InertSchema":{"type":"object","description":"A bounded JSON Schema 2020-12 document. Local references only; validators reject remote references, duplicate keys, excessive depth/node count and unsupported executable behavior. This is schema data, not authority.","maxProperties":256,"$schema":"https://json-schema.org/draft/2020-12/schema"},"ToolArguments":{"type":"object","description":"Strictly validate against the exact pinned tool input schema before use. Unknown tool fields fail. This open container is never an executable grant.","maxProperties":256,"$schema":"https://json-schema.org/draft/2020-12/schema"},"ModelToolProposal":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","minLength":1,"maxLength":256},"tool":{"$ref":"#/$defs/VersionRef"},"operation_id":{"type":"string","minLength":1,"maxLength":128},"operation_version":{"$ref":"#/$defs/Version"},"input":{"$ref":"#/$defs/ToolArguments"},"source_context":{"$ref":"#/$defs/ArtifactRef"},"explanation":{"type":"string","minLength":0,"maxLength":8192}},"required":["id","tool","operation_id","operation_version","input","source_context"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextText":{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"text","type":"string"},"text":{"type":"string","minLength":0,"maxLength":262144}},"required":["kind","text"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextArtifactPart":{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"artifact","type":"string"},"artifact":{"$ref":"#/$defs/ArtifactRef"},"media_type":{"type":"string","minLength":1,"maxLength":256},"classification":{"$ref":"#/$defs/Classification"},"label":{"type":"string","minLength":0,"maxLength":256}},"required":["kind","artifact","media_type","classification"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextToolCall":{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"tool_call","type":"string"},"proposal":{"$ref":"#/$defs/ModelToolProposal"}},"required":["kind","proposal"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextToolResult":{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"tool_result","type":"string"},"proposal_id":{"type":"string","minLength":1,"maxLength":256},"operation_id":{"$ref":"#/$defs/ID"},"status":{"type":"string","enum":["completed","accepted","failed"]},"artifact":{"$ref":"#/$defs/ArtifactRef"},"error_code":{"type":"string","minLength":0,"maxLength":128}},"required":["kind","proposal_id","operation_id","status","artifact"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextMemoryExcerpt":{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"memory_excerpt","type":"string"},"brain_id":{"$ref":"#/$defs/ID"},"claim":{"$ref":"#/$defs/VersionRef"},"text":{"type":"string","minLength":0,"maxLength":262144},"sources":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256},"confidence":{"type":"integer","minimum":0,"maximum":1000000},"freshness":{"$ref":"#/$defs/UTC"},"scope":{"$ref":"#/$defs/Scope"},"selected_context":{"$ref":"#/$defs/ArtifactRef"}},"required":["kind","brain_id","claim","text","sources","confidence","freshness","scope","selected_context"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextPart":{"oneOf":[{"$ref":"#/$defs/ContextText"},{"$ref":"#/$defs/ContextArtifactPart"},{"$ref":"#/$defs/ContextToolCall"},{"$ref":"#/$defs/ContextToolResult"},{"$ref":"#/$defs/ContextMemoryExcerpt"}],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextMessage":{"type":"object","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/ID"},"role":{"type":"string","enum":["system","developer","user","assistant","tool"]},"origin":{"type":"string","enum":["effective_instruction","user_message","model_output","tool_result","memory_recall","agent_message","compaction"]},"parts":{"type":"array","items":{"$ref":"#/$defs/ContextPart"},"minItems":1,"maxItems":512},"source_artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":512},"sender_id":{"$ref":"#/$defs/ID"},"message_id":{"$ref":"#/$defs/ID"},"created_at":{"$ref":"#/$defs/UTC"}},"required":["id","role","origin","parts","source_artifacts"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextToolDefinition":{"type":"object","additionalProperties":false,"properties":{"tool":{"$ref":"#/$defs/VersionRef"},"name":{"type":"string","minLength":1,"maxLength":128},"description":{"type":"string","minLength":0,"maxLength":16384},"input_schema":{"$ref":"#/$defs/InertSchema"},"output_schema":{"$ref":"#/$defs/InertSchema"},"effect":{"type":"string","enum":["local","disclosure","external_read","external_mutation"]},"destinations":{"type":"array","items":{"type":"string","minLength":1,"maxLength":4096},"minItems":0,"maxItems":64},"binding_id":{"$ref":"#/$defs/ID"},"schema_digest":{"$ref":"#/$defs/Digest"}},"required":["tool","name","description","input_schema","output_schema","effect","destinations","binding_id","schema_digest"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ContextArtifact":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.context/v1","type":"string"},"attempt_id":{"$ref":"#/$defs/ID"},"scope":{"$ref":"#/$defs/Scope"},"configuration_revision":{"$ref":"#/$defs/Version"},"worker":{"$ref":"#/$defs/VersionRef"},"execution_profile":{"$ref":"#/$defs/VersionRef"},"skill_versions":{"type":"array","items":{"$ref":"#/$defs/VersionRef"},"minItems":0,"maxItems":256},"messages":{"type":"array","items":{"$ref":"#/$defs/ContextMessage"},"minItems":0,"maxItems":4096},"tools":{"type":"array","items":{"$ref":"#/$defs/ContextToolDefinition"},"minItems":0,"maxItems":256},"source_artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":4096},"capture":{"type":"string","enum":["complete","partial","advisory"]},"created_at":{"$ref":"#/$defs/UTC"},"compaction":{"type":"object","additionalProperties":false,"properties":{"source_contexts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":1,"maxItems":256},"compactor_profile":{"$ref":"#/$defs/VersionRef"},"summary_artifact":{"$ref":"#/$defs/ArtifactRef"}},"required":["source_contexts","compactor_profile","summary_artifact"]}},"required":["schema","attempt_id","scope","configuration_revision","worker","execution_profile","skill_versions","messages","tools","source_artifacts","capture","created_at"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ModelOutput":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.model-output/v1","type":"string"},"response_id":{"type":"string","minLength":0,"maxLength":1024},"request_context":{"$ref":"#/$defs/ArtifactRef"},"finish_reason":{"type":"string","enum":["completed","tool_calls","length_limit","refused","interrupted","failed","unknown"]},"text_outputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactLocator"},"minItems":0,"maxItems":256},"tool_proposals":{"type":"array","items":{"$ref":"#/$defs/ModelToolProposal"},"minItems":0,"maxItems":256},"usage":{"$ref":"#/$defs/ProviderUsage"},"refusal":{"type":"string","minLength":0,"maxLength":8192},"continuation_reference":{"type":"string","minLength":0,"maxLength":1024}},"required":["schema","response_id","request_context","finish_reason","text_outputs","tool_proposals","usage"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ResponsesProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses/v1","type":"string"},"endpoint":{"$ref":"#/$defs/HTTPSURL"},"model":{"type":"string","minLength":1,"maxLength":256},"connection_id":{"$ref":"#/$defs/ID"},"max_input_tokens":{"type":"integer","minimum":1,"maximum":10000000},"max_output_tokens":{"type":"integer","minimum":1,"maximum":1000000},"max_response_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"currency":{"$ref":"#/$defs/Currency"},"input_rate":{"$ref":"#/$defs/RationalRate"},"output_rate":{"$ref":"#/$defs/RationalRate"},"enforcement":{"$ref":"#/$defs/BoundEnforcement"},"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"}},"required":["schema","endpoint","model","connection_id","max_input_tokens","max_output_tokens","max_response_bytes","timeout_seconds","currency","input_rate","output_rate","enforcement","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ResponsesParameters":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses.action/v1","type":"string"},"kind":{"const":"model_step","type":"string"},"context_artifact":{"$ref":"#/$defs/ArtifactRef"},"max_output_tokens":{"type":"integer","minimum":1,"maximum":1000000},"tool_contract_versions":{"type":"array","items":{"$ref":"#/$defs/VersionRef"},"minItems":0,"maxItems":256},"continuation_reference":{"type":"string","minLength":0,"maxLength":1024}},"required":["schema","kind","context_artifact","max_output_tokens","tool_contract_versions"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ResponsesEvidence":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.responses.evidence/v1","type":"string"},"physical_call":{"$ref":"#/$defs/PhysicalCallEvidence"},"response_id":{"type":"string","minLength":0,"maxLength":1024},"output":{"$ref":"#/$defs/ModelOutput"},"staged_outputs":{"type":"array","items":{"$ref":"#/$defs/StagedOutput"},"minItems":0,"maxItems":256},"output_artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256}},"required":["schema","physical_call","response_id","output","staged_outputs","output_artifacts"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Repository":{"type":"object","additionalProperties":false,"properties":{"owner":{"type":"string","pattern":"^[A-Za-z0-9][A-Za-z0-9-]{0,99}$"},"name":{"type":"string","pattern":"^[A-Za-z0-9_.-]{1,100}$"}},"required":["owner","name"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitSHA":{"type":"string","pattern":"^([0-9a-f]{40}|[0-9a-f]{64})$","maxLength":64,"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitBranch":{"type":"string","minLength":1,"maxLength":1024,"description":"Validate as a safe Git branch/ref name; reject traversal, control characters and invalid Git ref syntax.","$schema":"https://json-schema.org/draft/2020-12/schema"},"RepositoryPath":{"type":"string","minLength":1,"maxLength":4096,"description":"Normalized relative repository path; reject absolute paths, dot/dot-dot components, NUL, backslash ambiguity and path escapes.","$schema":"https://json-schema.org/draft/2020-12/schema"},"AutomationConstraints":{"type":"object","additionalProperties":false,"properties":{"allowed_workflows":{"type":"array","items":{"type":"string","minLength":1,"maxLength":512},"minItems":0,"maxItems":256},"allowed_deployment_environments":{"type":"array","items":{"type":"string","minLength":1,"maxLength":256},"minItems":0,"maxItems":128},"allow_external_notifications":{"type":"boolean"},"allow_automatic_merge":{"type":"boolean"},"unknown_automation":{"type":"string","enum":["deny","review"]},"evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256}},"required":["allowed_workflows","allowed_deployment_environments","allow_external_notifications","allow_automatic_merge","unknown_automation","evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubIdempotencyProfile":{"type":"object","additionalProperties":false,"properties":{"mode":{"type":"string","enum":["none","qualified_key","authoritative_nonexecution"]},"retention_seconds":{"type":"integer","minimum":0,"maximum":9223372036854775807},"equivalence_fields":{"type":"array","items":{"type":"string","minLength":1,"maxLength":128},"minItems":0,"maxItems":64},"evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":64}},"required":["mode","retention_seconds","equivalence_fields","evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.github/v1","type":"string"},"api_base":{"$ref":"#/$defs/HTTPSURL"},"allowed_repositories":{"type":"array","items":{"$ref":"#/$defs/Repository"},"minItems":1,"maxItems":256},"allowed_actions":{"type":"array","items":{"type":"string","enum":["read_repository","create_branch","push_commit","open_pull_request","merge_pull_request"]},"minItems":1,"maxItems":5},"max_response_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"idempotency_profile":{"$ref":"#/$defs/GitHubIdempotencyProfile"},"automation_constraints":{"$ref":"#/$defs/AutomationConstraints"},"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"}},"required":["schema","api_base","allowed_repositories","allowed_actions","max_response_bytes","timeout_seconds","idempotency_profile","automation_constraints","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubReadRepository":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.github.action/v1","type":"string"},"repository":{"$ref":"#/$defs/Repository"},"kind":{"const":"read_repository","type":"string"},"resource":{"type":"string","enum":["metadata","ref","commit","tree","blob","pull_request"]},"branch":{"$ref":"#/$defs/GitBranch"},"sha":{"$ref":"#/$defs/GitSHA"},"path":{"$ref":"#/$defs/RepositoryPath"},"pull_request_number":{"type":"integer","minimum":1,"maximum":9223372036854775807},"preflight_for_operation_id":{"$ref":"#/$defs/ID"}},"required":["schema","repository","kind","resource"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubCreateBranch":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.github.action/v1","type":"string"},"repository":{"$ref":"#/$defs/Repository"},"kind":{"const":"create_branch","type":"string"},"branch":{"$ref":"#/$defs/GitBranch"},"base_sha":{"$ref":"#/$defs/GitSHA"},"expected_absent":{"const":"required","type":"string"},"automation_constraints":{"$ref":"#/$defs/AutomationConstraints"},"preflight_evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":1,"maxItems":64}},"required":["schema","repository","kind","branch","base_sha","expected_absent","automation_constraints","preflight_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubPushCommit":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.github.action/v1","type":"string"},"repository":{"$ref":"#/$defs/Repository"},"kind":{"const":"push_commit","type":"string"},"branch":{"$ref":"#/$defs/GitBranch"},"expected_head_sha":{"$ref":"#/$defs/GitSHA"},"prepared_commit_sha":{"$ref":"#/$defs/GitSHA"},"base_sha":{"$ref":"#/$defs/GitSHA"},"patch":{"$ref":"#/$defs/ArtifactRef"},"content_artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":4096},"force":{"const":false,"type":"boolean"},"automation_constraints":{"$ref":"#/$defs/AutomationConstraints"},"preflight_evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":1,"maxItems":64}},"required":["schema","repository","kind","branch","expected_head_sha","prepared_commit_sha","base_sha","patch","content_artifacts","force","automation_constraints","preflight_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubOpenPullRequest":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.github.action/v1","type":"string"},"repository":{"$ref":"#/$defs/Repository"},"kind":{"const":"open_pull_request","type":"string"},"head_branch":{"$ref":"#/$defs/GitBranch"},"head_sha":{"$ref":"#/$defs/GitSHA"},"base_branch":{"$ref":"#/$defs/GitBranch"},"base_sha":{"$ref":"#/$defs/GitSHA"},"title":{"$ref":"#/$defs/ArtifactRef"},"body":{"$ref":"#/$defs/ArtifactRef"},"patch":{"$ref":"#/$defs/ArtifactRef"},"draft":{"type":"boolean"},"automation_constraints":{"$ref":"#/$defs/AutomationConstraints"},"preflight_evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":1,"maxItems":64}},"required":["schema","repository","kind","head_branch","head_sha","base_branch","base_sha","title","body","patch","draft","automation_constraints","preflight_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubMergePullRequest":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.github.action/v1","type":"string"},"repository":{"$ref":"#/$defs/Repository"},"kind":{"const":"merge_pull_request","type":"string"},"pull_request_number":{"type":"integer","minimum":1,"maximum":9223372036854775807},"expected_head_sha":{"$ref":"#/$defs/GitSHA"},"expected_base_sha":{"$ref":"#/$defs/GitSHA"},"merge_method":{"type":"string","enum":["merge","squash","rebase"]},"commit_title":{"$ref":"#/$defs/ArtifactRef"},"commit_body":{"$ref":"#/$defs/ArtifactRef"},"automation_constraints":{"$ref":"#/$defs/AutomationConstraints"},"preflight_evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":1,"maxItems":64}},"required":["schema","repository","kind","pull_request_number","expected_head_sha","expected_base_sha","merge_method","commit_title","commit_body","automation_constraints","preflight_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubParameters":{"oneOf":[{"$ref":"#/$defs/GitHubReadRepository"},{"$ref":"#/$defs/GitHubCreateBranch"},{"$ref":"#/$defs/GitHubPushCommit"},{"$ref":"#/$defs/GitHubOpenPullRequest"},{"$ref":"#/$defs/GitHubMergePullRequest"}],"$schema":"https://json-schema.org/draft/2020-12/schema"},"GitHubEvidence":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.github.evidence/v1","type":"string"},"physical_call":{"$ref":"#/$defs/PhysicalCallEvidence"},"kind":{"type":"string","enum":["read_repository","create_branch","push_commit","open_pull_request","merge_pull_request"]},"repository":{"$ref":"#/$defs/Repository"},"usage":{"$ref":"#/$defs/ProviderUsage"},"staged_outputs":{"type":"array","items":{"$ref":"#/$defs/StagedOutput"},"minItems":0,"maxItems":256},"output_artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256},"branch":{"$ref":"#/$defs/GitBranch"},"observed_head_sha":{"$ref":"#/$defs/GitSHA"},"observed_base_sha":{"$ref":"#/$defs/GitSHA"},"commit_sha":{"$ref":"#/$defs/GitSHA"},"pull_request_number":{"type":"integer","minimum":1,"maximum":9223372036854775807},"pull_request_url":{"$ref":"#/$defs/HTTPSURL"},"automation_evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256}},"required":["schema","physical_call","kind","repository","usage","staged_outputs","output_artifacts"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"HTTPOrigin":{"type":"string","format":"uri","pattern":"^https?://[^/?#]+$","maxLength":2048,"$schema":"https://json-schema.org/draft/2020-12/schema"},"HTTPURL":{"type":"string","format":"uri","pattern":"^https?://","maxLength":4096,"$schema":"https://json-schema.org/draft/2020-12/schema"},"HTTPReadProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.httpread/v1","type":"string"},"allowed_origins":{"type":"array","items":{"$ref":"#/$defs/HTTPOrigin"},"minItems":1,"maxItems":256},"max_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"max_redirects":{"type":"integer","const":0},"allowed_media_types":{"type":"array","items":{"type":"string","minLength":1,"maxLength":256},"minItems":1,"maxItems":128},"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"}},"required":["schema","allowed_origins","max_bytes","timeout_seconds","max_redirects","allowed_media_types","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"HTTPReadHeader":{"type":"object","additionalProperties":false,"properties":{"name":{"type":"string","enum":["Accept","Accept-Language","If-None-Match","If-Modified-Since","User-Agent"]},"value":{"type":"string","maxLength":1024,"pattern":"^[^\r\n]*$"}},"required":["name","value"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"HTTPReadParameters":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.httpread.action/v1","type":"string"},"kind":{"const":"read","type":"string"},"url":{"$ref":"#/$defs/HTTPURL"},"method":{"const":"GET","type":"string"},"headers":{"type":"array","items":{"$ref":"#/$defs/HTTPReadHeader"},"minItems":0,"maxItems":16},"expected_media_type":{"type":"string","minLength":1,"maxLength":256}},"required":["schema","kind","url","method","headers","expected_media_type"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"HTTPReadEvidence":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.httpread.evidence/v1","type":"string"},"physical_call":{"$ref":"#/$defs/PhysicalCallEvidence"},"requested_url":{"$ref":"#/$defs/HTTPURL"},"resolved_url":{"$ref":"#/$defs/HTTPURL"},"validated_dial_addresses":{"type":"array","items":{"type":"string","minLength":1,"maxLength":128},"minItems":0,"maxItems":32},"status":{"type":"integer","minimum":100,"maximum":599},"media_type":{"type":"string","minLength":0,"maxLength":256},"freshness":{"$ref":"#/$defs/UTC"},"usage":{"$ref":"#/$defs/ProviderUsage"},"staged_outputs":{"type":"array","items":{"$ref":"#/$defs/StagedOutput"},"minItems":0,"maxItems":1},"output_artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":1},"content_digest":{"$ref":"#/$defs/Digest"},"content_size":{"type":"integer","minimum":0,"maximum":268435456},"etag":{"type":"string","minLength":0,"maxLength":1024},"last_modified":{"type":"string","minLength":0,"maxLength":128},"redirect_location":{"$ref":"#/$defs/HTTPURL"}},"required":["schema","physical_call","requested_url","resolved_url","validated_dial_addresses","status","media_type","freshness","usage","staged_outputs","output_artifacts"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityBrainMapping":{"type":"object","additionalProperties":false,"properties":{"brain_id":{"$ref":"#/$defs/ID"},"endpoint":{"type":"string","minLength":1,"maxLength":4096},"root_ref":{"type":"string","minLength":1,"maxLength":512},"writer_owner":{"type":"string","minLength":1,"maxLength":512},"classification":{"$ref":"#/$defs/Classification"}},"required":["brain_id","endpoint","root_ref","writer_owner","classification"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityLookupSemantics":{"type":"object","additionalProperties":false,"properties":{"mode":{"type":"string","enum":["authoritative","non_authoritative","unsupported"]},"retention_seconds":{"type":"integer","minimum":0,"maximum":9223372036854775807},"command_identity_supported":{"type":"boolean"},"evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":64}},"required":["mode","retention_seconds","command_identity_supported","evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityFreshnessCapability":{"type":"object","additionalProperties":false,"properties":{"source_revision_supported":{"type":"boolean"},"index_revision_supported":{"type":"boolean"},"minimum_freshness_enforceable":{"type":"boolean"},"read_facade":{"type":"string","enum":["qualified_local","unsupported"]},"evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":64}},"required":["source_revision_supported","index_revision_supported","minimum_freshness_enforceable","read_facade","evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityBackupProtocol":{"type":"object","additionalProperties":false,"properties":{"mode":{"type":"string","enum":["qualified_pinned_revision","unsupported"]},"protocol_profile":{"type":"string","minLength":1,"maxLength":256},"immutable_revision_export":{"type":"boolean"},"restore_supported":{"type":"boolean"},"evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":64}},"required":["mode","protocol_profile","immutable_revision_export","restore_supported","evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.serenity/v1","type":"string"},"version":{"type":"string","minLength":1,"maxLength":128},"commit":{"type":"string","minLength":1,"maxLength":128},"brain_mappings":{"type":"array","items":{"$ref":"#/$defs/SerenityBrainMapping"},"minItems":0,"maxItems":4096},"supported_operations":{"type":"array","items":{"type":"string","enum":["recall","remember","inspect","promote","retract","export_revision"]},"minItems":0,"maxItems":6},"enforcement":{"$ref":"#/$defs/BoundEnforcement"},"command_status_lookup":{"$ref":"#/$defs/SerenityLookupSemantics"},"freshness":{"$ref":"#/$defs/SerenityFreshnessCapability"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"max_bytes":{"type":"integer","minimum":1,"maximum":268435456},"backup_revision_protocol":{"$ref":"#/$defs/SerenityBackupProtocol"},"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"}},"required":["schema","version","commit","brain_mappings","supported_operations","enforcement","command_status_lookup","freshness","timeout_seconds","max_bytes","backup_revision_protocol","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"MemoryClaim":{"type":"object","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/ID"},"version":{"$ref":"#/$defs/Version"},"brain_id":{"$ref":"#/$defs/ID"},"text":{"type":"string","minLength":0,"maxLength":262144},"sources":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256},"confidence":{"type":"integer","minimum":0,"maximum":1000000},"freshness":{"$ref":"#/$defs/UTC"},"active":{"type":"boolean"},"source_brain_id":{"$ref":"#/$defs/ID"},"source_claim":{"$ref":"#/$defs/VersionRef"},"curator_id":{"$ref":"#/$defs/ID"},"redaction":{"type":"string","minLength":0,"maxLength":8192}},"required":["id","version","brain_id","text","sources","confidence","freshness","active"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"BrainRevision":{"type":"object","additionalProperties":false,"properties":{"brain_id":{"$ref":"#/$defs/ID"},"revision":{"type":"string","minLength":1,"maxLength":256},"digest":{"$ref":"#/$defs/Digest"},"observed_at":{"$ref":"#/$defs/UTC"},"index_revision":{"type":"string","minLength":1,"maxLength":256}},"required":["brain_id","revision","digest","observed_at"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityRecall":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.serenity.action/v1","type":"string"},"brain_id":{"$ref":"#/$defs/ID"},"adapter_command_id":{"$ref":"#/$defs/ID"},"kind":{"const":"recall","type":"string"},"query":{"type":"string","minLength":1,"maxLength":8192},"minimum_freshness":{"$ref":"#/$defs/UTC"},"max_claims":{"type":"integer","minimum":1,"maximum":200},"maximum_cost":{"$ref":"#/$defs/Money"},"allowed_provider_destinations":{"type":"array","items":{"$ref":"#/$defs/HTTPSURL"},"minItems":0,"maxItems":64},"classification":{"$ref":"#/$defs/Classification"}},"required":["schema","brain_id","adapter_command_id","kind","query","minimum_freshness","max_claims","maximum_cost","allowed_provider_destinations","classification"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityRemember":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.serenity.action/v1","type":"string"},"brain_id":{"$ref":"#/$defs/ID"},"adapter_command_id":{"$ref":"#/$defs/ID"},"kind":{"const":"remember","type":"string"},"text":{"type":"string","minLength":1,"maxLength":8192},"sources":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256},"writer_owner":{"type":"string","minLength":1,"maxLength":512},"maximum_cost":{"$ref":"#/$defs/Money"},"allowed_provider_destinations":{"type":"array","items":{"$ref":"#/$defs/HTTPSURL"},"minItems":0,"maxItems":64}},"required":["schema","brain_id","adapter_command_id","kind","text","sources","writer_owner","maximum_cost","allowed_provider_destinations"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityInspect":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.serenity.action/v1","type":"string"},"brain_id":{"$ref":"#/$defs/ID"},"adapter_command_id":{"$ref":"#/$defs/ID"},"kind":{"const":"inspect","type":"string"},"claim":{"$ref":"#/$defs/VersionRef"},"minimum_freshness":{"$ref":"#/$defs/UTC"}},"required":["schema","brain_id","adapter_command_id","kind","claim","minimum_freshness"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityPromote":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.serenity.action/v1","type":"string"},"brain_id":{"$ref":"#/$defs/ID"},"adapter_command_id":{"$ref":"#/$defs/ID"},"kind":{"const":"promote","type":"string"},"source_brain_id":{"$ref":"#/$defs/ID"},"source_claim":{"$ref":"#/$defs/VersionRef"},"source_disclosure_evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":1,"maxItems":64},"text":{"type":"string","minLength":1,"maxLength":8192},"sources":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256},"curator_id":{"$ref":"#/$defs/ID"},"writer_owner":{"type":"string","minLength":1,"maxLength":512},"maximum_cost":{"$ref":"#/$defs/Money"},"allowed_provider_destinations":{"type":"array","items":{"$ref":"#/$defs/HTTPSURL"},"minItems":0,"maxItems":64},"redaction":{"type":"string","minLength":0,"maxLength":8192}},"required":["schema","brain_id","adapter_command_id","kind","source_brain_id","source_claim","source_disclosure_evidence","text","sources","curator_id","writer_owner","maximum_cost","allowed_provider_destinations"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityRetract":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.serenity.action/v1","type":"string"},"brain_id":{"$ref":"#/$defs/ID"},"adapter_command_id":{"$ref":"#/$defs/ID"},"kind":{"const":"retract","type":"string"},"claim":{"$ref":"#/$defs/VersionRef"},"reason":{"type":"string","minLength":1,"maxLength":8192},"writer_owner":{"type":"string","minLength":1,"maxLength":512},"removal":{"const":"active_recall","type":"string"}},"required":["schema","brain_id","adapter_command_id","kind","claim","reason","writer_owner","removal"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityExportRevision":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.serenity.action/v1","type":"string"},"brain_id":{"$ref":"#/$defs/ID"},"adapter_command_id":{"$ref":"#/$defs/ID"},"kind":{"const":"export_revision","type":"string"},"revision":{"$ref":"#/$defs/BrainRevision"}},"required":["schema","brain_id","adapter_command_id","kind","revision"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityParameters":{"oneOf":[{"$ref":"#/$defs/SerenityRecall"},{"$ref":"#/$defs/SerenityRemember"},{"$ref":"#/$defs/SerenityInspect"},{"$ref":"#/$defs/SerenityPromote"},{"$ref":"#/$defs/SerenityRetract"},{"$ref":"#/$defs/SerenityExportRevision"}],"$schema":"https://json-schema.org/draft/2020-12/schema"},"SerenityEvidence":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.serenity.evidence/v1","type":"string"},"physical_call":{"$ref":"#/$defs/PhysicalCallEvidence"},"kind":{"type":"string","enum":["recall","remember","inspect","promote","retract","export_revision"]},"brain_id":{"$ref":"#/$defs/ID"},"adapter_command_id":{"$ref":"#/$defs/ID"},"command_status":{"type":"string","enum":["not_admitted","accepted","completed","failed","unknown"]},"claims":{"type":"array","items":{"$ref":"#/$defs/MemoryClaim"},"minItems":0,"maxItems":200},"brain_revisions":{"type":"array","items":{"$ref":"#/$defs/BrainRevision"},"minItems":0,"maxItems":64},"usage":{"$ref":"#/$defs/ProviderUsage"},"staged_outputs":{"type":"array","items":{"$ref":"#/$defs/StagedOutput"},"minItems":0,"maxItems":256},"output_artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256},"writer_owner":{"type":"string","minLength":1,"maxLength":512},"active_recall_removed":{"type":"boolean"},"historical_erasure":{"type":"boolean","const":false},"selected_context":{"$ref":"#/$defs/ArtifactLocator"},"lookup_authoritative":{"type":"boolean"}},"required":["schema","physical_call","kind","brain_id","adapter_command_id","command_status","claims","brain_revisions","usage","staged_outputs","output_artifacts"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"VerifierOutputRequirement":{"type":"object","additionalProperties":false,"properties":{"name":{"type":"string","minLength":1,"maxLength":128},"artifact":{"$ref":"#/$defs/ArtifactRef"},"media_type":{"type":"string","minLength":1,"maxLength":256},"json_schema":{"$ref":"#/$defs/InertSchema"}},"required":["name","artifact","media_type"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ArtifactVerifierProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verifier-profile/v1","type":"string"},"kind":{"const":"artifact_contract","type":"string"},"id":{"type":"string","minLength":1,"maxLength":256},"version":{"type":"string","minLength":1,"maxLength":128},"code_digest":{"$ref":"#/$defs/Digest"},"supported_checks":{"type":"array","items":{"type":"string","enum":["presence","digest","json_schema"]},"minItems":1,"maxItems":3},"max_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"}},"required":["schema","kind","id","version","code_digest","supported_checks","max_bytes","timeout_seconds","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"RepositoryVerifierProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verifier-profile/v1","type":"string"},"kind":{"const":"repository_patch","type":"string"},"id":{"type":"string","minLength":1,"maxLength":256},"version":{"type":"string","minLength":1,"maxLength":128},"code_digest":{"$ref":"#/$defs/Digest"},"runner_profile":{"type":"string","minLength":1,"maxLength":256},"command_id":{"type":"string","minLength":1,"maxLength":256},"command_digest":{"$ref":"#/$defs/Digest"},"environment_profile":{"type":"string","minLength":1,"maxLength":256},"network":{"type":"string","enum":["disabled","qualified_allowlist"]},"max_output_bytes":{"type":"integer","minimum":1,"maximum":1048576},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"}},"required":["schema","kind","id","version","code_digest","runner_profile","command_id","command_digest","environment_profile","network","max_output_bytes","timeout_seconds","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"VerificationProfile":{"oneOf":[{"$ref":"#/$defs/ArtifactVerifierProfile"},{"$ref":"#/$defs/RepositoryVerifierProfile"}],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ExpectedVerificationObservation":{"type":"object","additionalProperties":false,"properties":{"check_id":{"type":"string","minLength":1,"maxLength":256},"kind":{"type":"string","enum":["artifact_presence","artifact_digest","json_schema","repository_patch_applies","repository_command"]},"expected":{"type":"string","enum":["pass","fail"]},"artifact_name":{"type":"string","minLength":1,"maxLength":128},"expected_digest":{"$ref":"#/$defs/Digest"},"schema":{"$ref":"#/$defs/InertSchema"},"command_id":{"type":"string","minLength":1,"maxLength":256},"expected_exit_code":{"type":"integer","minimum":-2147483648,"maximum":2147483647}},"required":["check_id","kind","expected"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ObservedVerificationCheck":{"type":"object","additionalProperties":false,"properties":{"check_id":{"type":"string","minLength":1,"maxLength":256},"kind":{"type":"string","enum":["artifact_presence","artifact_digest","json_schema","repository_patch_applies","repository_command"]},"status":{"type":"string","enum":["passed","failed","unavailable","tampered"]},"evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactLocator"},"minItems":0,"maxItems":128},"explanation":{"type":"string","minLength":0,"maxLength":8192},"observed_digest":{"$ref":"#/$defs/Digest"},"observed_exit_code":{"type":"integer","minimum":-2147483648,"maximum":2147483647}},"required":["check_id","kind","status","evidence","explanation"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"VerificationRequest":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verification-request/v1","type":"string"},"job_id":{"$ref":"#/$defs/ID"},"task_id":{"$ref":"#/$defs/ID"},"attempt_id":{"$ref":"#/$defs/ID"},"scope":{"$ref":"#/$defs/Scope"},"acceptance_digest":{"$ref":"#/$defs/Digest"},"profile":{"$ref":"#/$defs/VerificationProfile"},"sealed_inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":4096},"outputs":{"type":"array","items":{"$ref":"#/$defs/VerifierOutputRequirement"},"minItems":0,"maxItems":4096},"expected_observations":{"type":"array","items":{"$ref":"#/$defs/ExpectedVerificationObservation"},"minItems":1,"maxItems":512},"deadline":{"$ref":"#/$defs/UTC"},"repository":{"$ref":"#/$defs/Repository"},"base_sha":{"$ref":"#/$defs/GitSHA"},"patch":{"$ref":"#/$defs/ArtifactRef"}},"required":["schema","job_id","task_id","attempt_id","scope","acceptance_digest","profile","sealed_inputs","outputs","expected_observations","deadline"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"VerificationResult":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verification-result/v1","type":"string"},"job_id":{"$ref":"#/$defs/ID"},"task_id":{"$ref":"#/$defs/ID"},"attempt_id":{"$ref":"#/$defs/ID"},"acceptance_digest":{"$ref":"#/$defs/Digest"},"verifier_id":{"type":"string","minLength":1,"maxLength":256},"verifier_version":{"type":"string","minLength":1,"maxLength":128},"verifier_code_digest":{"$ref":"#/$defs/Digest"},"request_artifact":{"$ref":"#/$defs/ArtifactRef"},"status":{"type":"string","enum":["passed","failed","prerequisite_missing","tampered","interrupted"]},"observations":{"type":"array","items":{"$ref":"#/$defs/ObservedVerificationCheck"},"minItems":0,"maxItems":512},"started_at":{"$ref":"#/$defs/UTC"},"finished_at":{"$ref":"#/$defs/UTC"},"staged_outputs":{"type":"array","items":{"$ref":"#/$defs/StagedOutput"},"minItems":0,"maxItems":256},"output_artifacts":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"minItems":0,"maxItems":256},"independent":{"type":"boolean","const":true}},"required":["schema","job_id","task_id","attempt_id","acceptance_digest","verifier_id","verifier_version","verifier_code_digest","request_artifact","status","observations","started_at","finished_at","staged_outputs","output_artifacts","independent"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"BackupArtifactEntry":{"type":"object","additionalProperties":false,"properties":{"artifact":{"$ref":"#/$defs/ArtifactRef"},"size":{"type":"integer","minimum":0,"maximum":268435456},"media_type":{"type":"string","minLength":1,"maxLength":256},"classification":{"$ref":"#/$defs/Classification"},"encrypted":{"type":"boolean"},"archive_entry":{"type":"string","minLength":1,"maxLength":512},"pins":{"type":"array","items":{"$ref":"#/$defs/ID"},"minItems":0,"maxItems":4096}},"required":["artifact","size","media_type","classification","encrypted","archive_entry","pins"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"BackupBrainEntry":{"type":"object","additionalProperties":false,"properties":{"revision":{"$ref":"#/$defs/BrainRevision"},"export_artifact":{"$ref":"#/$defs/ArtifactRef"},"writer_owner":{"type":"string","minLength":1,"maxLength":512},"adapter_profile_digest":{"$ref":"#/$defs/Digest"},"key_prerequisites":{"type":"array","items":{"type":"string","minLength":1,"maxLength":512},"minItems":0,"maxItems":64}},"required":["revision","export_artifact","writer_owner","adapter_profile_digest","key_prerequisites"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"RecoveryObligation":{"type":"object","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/ID"},"owner":{"type":"string","enum":["identity","evidence","effects","accounting","execution","memory","installation"]},"kind":{"type":"string","enum":["credential_revocation","principal_revocation","command_identity","claimed_effect","unknown_effect","reservation","lease_conflict","memory_write","memory_promotion","memory_retraction"]},"resource_id":{"$ref":"#/$defs/ID"},"resource_version":{"$ref":"#/$defs/Version"},"record_artifact":{"$ref":"#/$defs/ArtifactRef"},"record_digest":{"$ref":"#/$defs/Digest"},"state":{"type":"string","minLength":1,"maxLength":128},"recorded_at":{"$ref":"#/$defs/UTC"}},"required":["id","owner","kind","resource_id","resource_version","record_artifact","record_digest","state","recorded_at"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"BackupManifest":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.backup/v1","type":"string"},"installation_id":{"$ref":"#/$defs/ID"},"backup_id":{"$ref":"#/$defs/ID"},"generation":{"$ref":"#/$defs/Version"},"created_at":{"$ref":"#/$defs/UTC"},"database_digest":{"$ref":"#/$defs/Digest"},"database_size":{"type":"integer","minimum":1,"maximum":9223372036854775807},"database_archive_entry":{"const":"state.sqlite","type":"string"},"database_schema_versions":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"owner":{"type":"string","minLength":1,"maxLength":128},"version":{"$ref":"#/$defs/Version"},"migration_digest":{"$ref":"#/$defs/Digest"}},"required":["owner","version","migration_digest"]},"minItems":0,"maxItems":256},"artifacts":{"type":"array","items":{"$ref":"#/$defs/BackupArtifactEntry"},"minItems":0,"maxItems":1000000},"brains":{"type":"array","items":{"$ref":"#/$defs/BackupBrainEntry"},"minItems":0,"maxItems":100000},"retained_obligations":{"type":"array","items":{"$ref":"#/$defs/RecoveryObligation"},"minItems":0,"maxItems":1000000},"key_prerequisites":{"type":"array","items":{"type":"string","minLength":1,"maxLength":512},"minItems":0,"maxItems":256},"source_revision":{"type":"string","minLength":1,"maxLength":128},"controller_version":{"type":"string","minLength":1,"maxLength":128},"required_protocol_profiles":{"type":"array","items":{"type":"string","minLength":1,"maxLength":256},"minItems":0,"maxItems":256},"paused":{"type":"boolean","const":true},"recovery_overlay":{"$ref":"#/$defs/ArtifactRef"}},"required":["schema","installation_id","backup_id","generation","created_at","database_digest","database_size","database_archive_entry","database_schema_versions","artifacts","brains","retained_obligations","key_prerequisites","source_revision","controller_version","required_protocol_profiles","paused"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"RecoveryOverlay":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.recovery-overlay/v1","type":"string"},"installation_id":{"$ref":"#/$defs/ID"},"captured_at":{"$ref":"#/$defs/UTC"},"source_generation":{"$ref":"#/$defs/Version"},"source_database_digest":{"$ref":"#/$defs/Digest"},"obligations":{"type":"array","items":{"$ref":"#/$defs/RecoveryObligation"},"minItems":0,"maxItems":1000000},"artifact_entries":{"type":"array","items":{"$ref":"#/$defs/BackupArtifactEntry"},"minItems":0,"maxItems":1000000},"key_prerequisites":{"type":"array","items":{"type":"string","minLength":1,"maxLength":512},"minItems":0,"maxItems":256}},"required":["schema","installation_id","captured_at","source_generation","source_database_digest","obligations","artifact_entries","key_prerequisites"],"$schema":"https://json-schema.org/draft/2020-12/schema"}},"adapter_mapping":{"responses":{"profile":"ResponsesProfile","parameters":"ResponsesParameters","evidence":"ResponsesEvidence"},"github":{"profile":"GitHubProfile","parameters":"GitHubParameters","evidence":"GitHubEvidence"},"httpread":{"profile":"HTTPReadProfile","parameters":"HTTPReadParameters","evidence":"HTTPReadEvidence"},"serenity":{"profile":"SerenityProfile","parameters":"SerenityParameters","evidence":"SerenityEvidence"}}}
```

## Named acceptance cases

Tests are implementation deliverables, not claims of already executed qualification. Retain expected/observed results, exact source/config/tool versions and failure evidence.

### Z04.explicit_removal — Z04

Setup: An active configuration contains workers, projects and retained obligations.

Action: Apply a patch omitting an object, then explicitly remove/archive an object that owns active work or unknown effects.

Expected:

- Omission does not delete live objects.
- Explicit removal first disables new work and requires archival disposition or refuses destructive removal while obligations remain.
- Artifacts, operation uncertainty and accounting obligations are not discarded.

### Z05.callback_discovery — Z05

Setup: A discovered tool or HTTP response advertises a callback, redirect, external server or subprocess.

Action: Attempt to follow that behavior outside the registered reviewed destination/adapter contract.

Expected:

- Discovery grants no execution or disclosure authority.
- Unreviewed callbacks, destination changes and arbitrary external MCP/subprocess execution are refused.

### Z06.admission_claim_revoke — Z06

Setup: A controlled adapter exposes barriers before admission, before claim and before invocation.

Action: Race revocation or pause against admission and claim in each ordering.

Expected:

- Revocation committed before claim prevents invocation; physical call count is zero.
- Admission reserves and writes intent/evidence atomically; claimed dispatch follows the specified irreversible boundary.
- Revocation after claim is recorded honestly and does not claim to retract transmitted bytes.

### Z06.crash_call_counts — Z06

Setup: A controlled provider counts physical invocations and fault injection surrounds admit, claim, network return and record.

Action: Crash and restart at each boundary, then inspect and reconcile the operation.

Expected:

- No crash recovery blindly repeats a potentially sent effect.
- Attempt records and provider call counts correspond exactly; failed record persistence does not authorize another send.

### Z06.no_hidden_retries — Z06

Setup: Qualified adapters use a provider simulator with transient errors, timeouts and connection resets.

Action: Invoke each adapter once under a contract that has not established safe retry.

Expected:

- The adapter performs exactly one physical request per claimed attempt.
- SDK or transport mutation retries are disabled; any permitted retry receives a new authorized recorded attempt.

### Z07.exact_action_changes — Z07

Setup: An approved action binds account, destination, content/media hashes, timing, preconditions, repository head and relevant versions.

Action: Independently change content, destination, timing, repository head or account before dispatch.

Expected:

- Every material change invalidates the original digest-bound approval where review is required.
- Dispatch waits for a new eligible decision over the exact changed preview.

### Z08.lost_success_response — Z08

Setup: A provider commits a mutation but drops the response before Zatiti records its outcome.

Action: Restart or timeout the sender and inspect the logical operation.

Expected:

- The operation remains outcome_unknown with retained reservation and its original attempt.
- Neither the task result nor the command reports the external write as confirmed successful or safely unsent.

### Z08.failed_retry_after_unknown — Z08

Setup: An earlier attempt has an unresolved potentially successful outcome; a qualified contract permits a later attempt.

Action: Run the later attempt and return a definitive failure for that attempt.

Expected:

- Earlier uncertainty remains attached to the logical operation.
- Failed retry does not erase the earlier attempt, release its unknown reservation or prove no external effect occurred.

### Z08.expired_provider_key — Z08

Setup: A provider idempotency key has exceeded its qualified retention window for an unresolved operation.

Action: Attempt automatic redispatch using the old key.

Expected:

- The expired key is not treated as proof of safe idempotency.
- Dispatch is blocked unless authoritative nonexecution or another qualified safe contract is established.

### Z08.delayed_confirmation — Z08

Setup: An operation awaits confirmation or is outcome_unknown and the provider initially returns eventually consistent not-found.

Action: Reconcile, then deliver delayed authoritative success; separately deliver contradictory late evidence.

Expected:

- Non-authoritative not-found does not establish nonexecution.
- Authoritative success settles according to evidence; contradictory evidence creates a visible correction or dispute instead of rewriting history.

### Z08.cancel_unknown — Z08

Setup: An effect is already claimed and may have reached its provider.

Action: Cancel the task or operation and acknowledge the uncertainty as an operator.

Expected:

- Cancellation records restrictive intent but does not reclassify the possibly sent attempt as unsent.
- Unknown outcomes and reservations remain inspectable until supported evidence resolves them.

### Z09.settlement_recovery — Z09

Setup: Reservations include confirmed spend, refundable unused bounds and unknown attempts using exact micro-units and rational rates.

Action: Record each disposition with a crash before or after transaction commit and recover.

Expected:

- Spent, reserved, estimated and unknown values remain distinct and exact without floating-point drift.
- Settlement is atomic and idempotent; unresolved reservations survive restart and are not released twice.

### Z10.conflicting_replacement — Z10

Setup: An expired attempt may still control a repository resource or have an unresolved provider effect.

Action: Request a replacement attempt.

Expected:

- Replacement waits for explicit recovery disposition of conflicting resources and possible effects.
- No unsupported containment or successful stop is inferred from lease expiry.

### Z14.outbox_revocation_restore — Z14

Setup: The backup contains unresolved dispatch/outbox intents, unknown reservations, revoked credentials and pending memory writes/promotions.

Action: Restore, inspect state, attempt use of revoked credentials and reconcile before resuming.

Expected:

- Unresolved obligations and revocation states are preserved and inspectable.
- Pending effect and memory outcomes require reconciliation; restoring an older database does not claim to undo provider effects.

### Z18.memory_write_unknown — Z18

Setup: A Serenity writer commits a command but its acknowledgement is lost; recall may also incur model charges.

Action: Recover the command and run recall under a hard disclosure/spend bound.

Expected:

- The prior persisted intent and adapter command identity are reconciled without blind repeat writing.
- Exactly one canonical writer owns each brain; unsupported enforceable recall charge/disclosure bounds make that mode unavailable or explicitly advisory where policy permits.

### Z19.immediate_demotion — Z19

Setup: An active earned grant has an owner-approved incident/failure demotion rule.

Action: Record a qualifying incident concurrently with a new effect admission.

Expected:

- Restriction or demotion commits before any subsequently authorized admission can use the old grant.
- The user can inspect durable reasons and events; no model call or new spending is required to apply restriction.

### Z21.exact_decision — Z21

Setup: Several organizations contain unresolved human-required actions and one stale reviewed action.

Action: Open Needs you, inspect an exact action card, approve one eligible action and try the stale action.

Expected:

- The card shows the actual action/destination/content/preconditions and acknowledged disposition with evidence links.
- The stale action cannot reuse approval; Needs you links to unresolved exact decisions.
- A chat assertion does not serve as approval or completion authority.

### JOURNEY.cross_interface_fault_matrix — JOURNEY

Setup: Prepare each organization, engineering and research journey at mutation acknowledgement, claim, checkpoint, review, upload, effect return and restart boundaries.

Action: Interrupt at each boundary and continue through the opposite interface in both directions.

Expected:

- Submission identity, pinned contracts, events and authorization remain equivalent.
- No duplicate protected effect occurs; unknown outcomes remain unknown until qualified reconciliation.
- The evidence names each fault boundary and both starting/continuing transports.

### QUALIFICATION.adapter_bounds — QUALIFICATION

Setup: Pin public provider, GitHub, bounded HTTP and Serenity adapters with controlled fault simulators and authorized live qualification fixtures where required.

Action: Exercise cost bounds, disclosure destinations, timeouts, retries, confirmations, repository-head checks, memory freshness and lost acknowledgements.

Expected:

- Record exact adapter/source/protocol/profile versions and observed physical calls.
- Unsupported hard caps, safe-idempotency windows, continuation/context guarantees or memory operations remain unavailable or explicitly advisory as permitted, never inferred from upstream claims.

## Delivery

Implement production behavior and meaningful local tests within scope. Report files changed, commands actually run, observed results, unresolved dependency qualifications and contract defects. A missing dependency may use an exact local fake for development; production must return a named prerequisite/unsupported error instead of fake success. Integration owns shared dependency changes, real assembly, cross-package proving and landing. Do not advertise release or client/platform support from compilation alone.
