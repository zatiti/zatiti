# Implementation assignment: `internal/accounting`

Generated specification revision 1; source digest `c79b39e5ca496cd59114fdaa2c9dd3a36d7c9c0d3b4426bc131063f5cef22ca2`. This file is committed implementation context. Do not independently edit it. Everything required from the product specification and adjacent interfaces is embedded below; no RFC copy is required.

## Mission and scope

Own budget definitions, atomic reservations and exact cost/uncertainty ledgers.

Write scope: **`internal/accounting/` only**, excluding this generated AGENTS.md. Go package name: `accounting`. Ownership kind: domain; integration wave: 2.

Allowed production imports from this repository: `github.com/zatiti/zatiti/internal/contract`. Tests may use interfaces/fakes defined locally and, once available, storage-backed temporary fixtures; integration owns cross-package tests. No sibling raw SQL. No root dependency edits except the integration exception stated in its own brief.

## Implementation decisions and acceptance focus

Own accounting_limits/reservations/entries. Reserve installation, ancestor organizations from root down, project, worker and root task in stable order under one caller transaction. Count model/evaluation/retry/delegation/Serenity spend and concurrency under same limits. Exact integer currency micro-units; rational price numerator/denominator and checked ceil for reservations, never floating-point money. Currency mismatches and overflow fail input/prerequisite, missing price refuses paid admission. Proposed defaults do not grant paid spending. Keep spent/reserved/estimated/unknown distinct; unknown physical effects retain reservations. Settlement can release only established unused bound; unknown/advisory external spend cannot be presented as zero or enforced hard cap. Budget expansion stages configuration under current old authority. Root/ancestor caps are shared across children, not copied to each child as an independent allowance.

Local proving focus: Concurrent child/model reservations, ancestor exhaustion, exact rounding/overflow/currency, missing price, unknown cost preservation, restart settlement, no double reservation on command replay.

## Incoming and outgoing boundaries

Incoming callers: application, configuration, effects, execution, installation, policy, scheduling, tasks, application (authenticated public operations).

Outgoing owner calls: `_configuration.stage`, `_configuration.snapshot`, `_policy.check`. Each exact input/output schema appears below. Calls retain current Unit/authority; owner allowlists are mandatory.

Expose `New(contract.Dependencies) (*Service,error)`; `*Service` implements `contract.Module` with Name `accounting`, owner-prefixed migrations, all owned descriptors, and strict dispatch. No calls/goroutines during construction. Implement optional authentication/LocalIO interfaces where specified in the common contract. Tables are private under `accounting_`; external callers rely only on methods and schemas.

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

### R9-002 (source section 9; primary owner tasks)

Tasks pin an accountable principal, organization/project, assigned worker, outcome, input artifacts, required outputs, acceptance contract and resource envelope. Acceptance identifies verifier code/version, sealed inputs, expected observations and whether independent verification or authorized manual acceptance is required. Workers may propose contracts, but cannot replace their task's accepted verifier during execution.

### R9-003 (source section 9; primary owner tasks)

Task states are `draft`, `ready`, `running`, `waiting`, `verifying`, `succeeded`, `failed`, and `cancelled`. Waiting includes explicit dependencies, human decisions, prerequisites and recovery owners. Cancellation records intent first; terminal cancellation requires owned execution to stop or be conclusively fenced, and unresolved effects remain visible separately. A worker report and process exit do not independently set `succeeded`.

### R9-004 (source section 9; primary owner tasks)

Runs pin effective configuration and input versions. Each run attempt has one executor, lease, generation, heartbeat, resource reservation and output disposition. Retrying creates a new attempt; it cannot silently change the accepted task contract. A failed verification cannot release dependents whose acceptance requires verified success. A manual acceptance decision remains labeled manual rather than represented as automated proof.

### R9-005 (source section 9; primary owner tasks)

Zatiti owns the model loop used by chiefs and ongoing responsibilities. Specialist tasks may use external harness adapters under the same task and effect contracts. Adapters declare supported capabilities, including checkpointing, continuation, interruption, context capture, and cost reporting; unsupported guarantees fail explicitly rather than being inferred from a harness name. An external harness does not own a second copy of Zatiti's task graph.

### R9-006 (source section 9; primary owner tasks)

The initial executors are:

### R9-007 (source section 9; primary owner tasks)

- **Hosted loop:** a configured provider adapter runs bounded model steps with only the declared tools and credential broker. Model calls, disclosure and costs follow the same scope and accounting rules.
- **Cooperative external worker:** an authorized client claims work, receives pinned task context and lease identity, sends heartbeats/checkpoints, proposes governed effects, and submits artifacts and completion observations. Zatiti verifies the submitted result independently. The worker's external shell, network and billing remain advisory unless a separately qualified execution boundary contains them.

### R9-008 (source section 9; primary owner tasks)

Claim/heartbeat/report APIs bind the worker, attempt and generation. Concurrent claims cannot create two current owners. Lease expiry fences future Zatiti mutations but does not prove the old process stopped; a replacement waits for explicit recovery disposition of conflicting resources and possible external effects. A caller cannot report for another attempt or turn a stale result into current completion.

### R9-009 (source section 9; primary owner tasks)

Schedules reference pinned task templates, explicit time zones, occurrence keys, and misfire rules. Default missed occurrences coalesce once within a configured catch-up window; pause disables admission. Daylight-saving changes and repeated polling cannot duplicate an admitted occurrence. Conditions such as cancellation or a recorded reply are checked transactionally at wake. v1 reply conditions use explicit authenticated events; external chat-channel intake is a later adapter.

### R9-010 (source section 9; primary owner tasks)

Child tasks inherit intersected authority, project data, deadlines and root budgets. Parent completion criteria explicitly state which child outcomes are required. Delegation depth, child count, concurrency, planner steps and wall time are finite. No nested fleet scheduler may claim ownership of the same Zatiti task graph.

### R9-011 (source section 9; primary owner tasks)

Responsibilities support both durable schedules/event subscriptions and repeated reasoning. A responsibility records what outcome to pursue, what signals to inspect, when to reconsider, its finite per-cycle and aggregate spending limits, and conditions for pausing or escalating. Reasoning cycles may discover and propose useful work even without a new external event. Every cycle is a bounded admitted run with a durable next-wake decision, a minimum reconsideration interval, and accountable outputs; an idle loop cannot spend indefinitely or silently create unbounded tasks. The operator can pause a responsibility without cancelling unrelated work.

### R9-012 (source section 9; primary owner tasks)

Agent messages use durable mailboxes with sender, recipient, organization scope, task references, and stable message identity. Receipt is acknowledged only after durable target admission; redelivery deduplicates by identity. Running workers receive admitted messages at a safe step boundary, and idle workers can be resumed. Coordination messages and unsolicited context remain untrusted observations, never replacement authority. Direct user instructions and chief assignments converge on versioned tasks so concurrent conversations cannot silently overwrite responsibility.

### R9-013 (source section 9; primary owner tasks)

For the owned loop, every model-visible input must be reconstructable from persisted context: user messages, tool definitions and results, memory excerpts, effective instructions, and injected agent messages. Persist the request context before model dispatch and the protected effect intent before tool dispatch. Compaction retains lineage to its source context. External executors declare the limits of their context capture; Zatiti must not advertise exact transcript replay when an adapter cannot supply it.

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

### R11-002 (source section 11; primary owner accounting)

Use exact integer micro-units of an explicit currency and rational rate arithmetic. Reserve enforceable costs atomically across installation, organization, project, worker and root task using a stable lock/update order. Inference, evaluation, retries and delegated tasks share relevant limits. Record spent, reserved, estimated and unknown amounts separately. Missing prices or advisory external usage are not zero cost. Hard caps are claimed only where the selected provider contract bounds charges.

### R11-003 (source section 11; primary owner accounting)

Budgets need finite defaults. Proposed defaults are one concurrent attempt per worker, four per installation, a 30-minute attempt, 100 model steps, eight children, delegation depth three, and a 24-hour root deadline. Installations explicitly select currency and spending limits before paid work; the shipped default refuses paid execution until configured. No fixed provider price is embedded in the RFC.

### R11-004 (source section 11; primary owner accounting)

Commands, configuration revisions, state transitions, decisions, provider observations and artifact digests produce durable evidence. Receipts are projections over these records. Hash integrity is not a claim of an incorruptible log or independent external custody. A host/database administrator is outside the evidence threat model; external signing/anchoring is future work.

### R11-005 (source section 11; primary owner accounting)

Backups use the database's consistent backup mechanism and a pinned artifact manifest; copying a live database file without its consistency requirements is not a backup contract. Encrypt backups, preserve key-recovery prerequisites separately, and verify before advertising completion. Restore acquires exclusive maintenance ownership and starts paused. Preserve unresolved operations, reservations, credentials' revocation state and retained command identities. Reconcile pending dispatches before resuming. A database rewind cannot undo provider effects.

### R11-006 (source section 11; primary owner accounting)

Bound input sizes, archive expansion, log payloads, lists and event replay. Redact credentials before persistence and logging where possible; arbitrary secret recognition is not claimed. Retention never removes unresolved obligations to make the system appear healthy. Disk pressure pauses new artifact-producing work; it does not discard unknown effects. A doctor report names missing prerequisites and current execution/profile limitations without exposing secrets.
## Exact operation and dependency schemas

### `_accounting.activate` v1 — accounting / internal / mutation / local

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

### `_accounting.validate` v1 — accounting / internal / query / local

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

### `_configuration.stage` v1 — configuration / internal / mutation / local

Allowed internal callers: configuration, skills, connections, policy, accounting, scheduling, memory. Submission key: not required at this internal/query/bootstrap boundary.

Strictly validate typed definition schema then append draft change. No effective mutation. For creates allocate identity once using submission replay.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"change":{"$ref":"#/$defs/Change"},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","change"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Draft"}},"required":["resource"]}
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

### `budget.get` v1 — accounting / public / query / local

CLI `zatiti budget get`; MCP `zatiti_budget_get`. Submission key: not required at this internal/query/bootstrap boundary.

Inspect effective intersected installation/ancestor/project/worker/root limits.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"limits":{"$ref":"#/$defs/Limits"}},"required":["limits"]}
```

### `budget.propose` v1 — accounting / public / mutation / local

CLI `zatiti budget propose`; MCP `zatiti_budget_propose`. Submission key: required.

Stage exact budget change under old authority; no paid execution until explicit currency/finite spend.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"limits":{"$ref":"#/$defs/Limits"},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","expected_version","limits"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Draft"}},"required":["resource"]}
```

### `usage.get` v1 — accounting / public / query / local

CLI `zatiti usage get`; MCP `zatiti_usage_get`. Submission key: not required at this internal/query/bootstrap boundary.

Return spent/reserved/estimated/unknown separately; advisory/missing price is not zero.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Usage"}},"required":["resource"]}
```

### Local schema definitions

The schemas above resolve exclusively against this embedded `$defs` object. Input objects reject additional properties except explicitly open schema/data fields. Field semantics are completed by the owned requirements and operation descriptions. Output `resource`, `items`, `draft`, `job`, etc. are literal keys. Pagination cursor lives in the common envelope.

```json
{"$defs":{"Acceptance":{"type":"object","additionalProperties":false,"properties":{"verifier_id":{"type":"string","maxLength":8192},"verifier_version":{"type":"string","maxLength":8192},"sealed_inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"expected_observations":{"type":"array","items":{"$ref":"#/$defs/Adapter_ExpectedVerificationObservation"},"maxItems":512},"mode":{"type":"string","enum":["independent","manual"]},"required_child_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"profile":{"$ref":"#/$defs/Adapter_VerificationProfile"}},"required":["verifier_id","verifier_version","sealed_inputs","expected_observations","mode","required_child_ids","profile"]},"Action":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"tool":{"$ref":"#/$defs/Ref"},"connection":{"$ref":"#/$defs/Ref"},"account_identity":{"type":"string","maxLength":8192},"destination":{"type":"string","maxLength":8192},"content":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"not_before":{"type":"string","format":"date-time"},"expires_at":{"type":"string","format":"date-time"},"preconditions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"configuration_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"parameters":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"cost_bound":{"$ref":"#/$defs/Money"}},"required":["scope","tool","connection","account_identity","destination","content","not_before","expires_at","preconditions","configuration_revision","parameters","cost_bound"]},"Adapter_ArtifactRef":{"type":"object","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/Adapter_ID"},"digest":{"$ref":"#/$defs/Adapter_Digest"}},"required":["id","digest"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ArtifactVerifierProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verifier-profile/v1","type":"string"},"kind":{"const":"artifact_contract","type":"string"},"id":{"type":"string","minLength":1,"maxLength":256},"version":{"type":"string","minLength":1,"maxLength":128},"code_digest":{"$ref":"#/$defs/Adapter_Digest"},"supported_checks":{"type":"array","items":{"type":"string","enum":["presence","digest","json_schema"]},"minItems":1,"maxItems":3},"max_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"capability_evidence":{"$ref":"#/$defs/Adapter_CapabilityEvidence"}},"required":["schema","kind","id","version","code_digest","supported_checks","max_bytes","timeout_seconds","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_CapabilityEvidence":{"type":"object","additionalProperties":false,"properties":{"artifact":{"$ref":"#/$defs/Adapter_ArtifactRef"},"adapter_version":{"type":"string","minLength":1,"maxLength":128},"source_revision":{"type":"string","minLength":1,"maxLength":128},"protocol_revision":{"type":"string","minLength":1,"maxLength":128},"profile_digest":{"$ref":"#/$defs/Adapter_Digest"},"qualified_at":{"$ref":"#/$defs/Adapter_UTC"},"capabilities":{"type":"array","items":{"type":"string","minLength":1,"maxLength":128},"minItems":0,"maxItems":128},"limitations":{"type":"array","items":{"type":"string","minLength":1,"maxLength":2048},"minItems":0,"maxItems":128}},"required":["artifact","adapter_version","source_revision","protocol_revision","profile_digest","qualified_at","capabilities","limitations"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_Digest":{"type":"string","pattern":"^[0-9a-f]{64}$","maxLength":64,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ExpectedVerificationObservation":{"type":"object","additionalProperties":false,"properties":{"check_id":{"type":"string","minLength":1,"maxLength":256},"kind":{"type":"string","enum":["artifact_presence","artifact_digest","json_schema","repository_patch_applies","repository_command"]},"expected":{"type":"string","enum":["pass","fail"]},"artifact_name":{"type":"string","minLength":1,"maxLength":128},"expected_digest":{"$ref":"#/$defs/Adapter_Digest"},"schema":{"$ref":"#/$defs/Adapter_InertSchema"},"command_id":{"type":"string","minLength":1,"maxLength":256},"expected_exit_code":{"type":"integer","minimum":-2147483648,"maximum":2147483647}},"required":["check_id","kind","expected"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ID":{"type":"string","format":"uuid","maxLength":36,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_InertSchema":{"type":"object","description":"A bounded JSON Schema 2020-12 document. Local references only; validators reject remote references, duplicate keys, excessive depth/node count and unsupported executable behavior. This is schema data, not authority.","maxProperties":256,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_RepositoryVerifierProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verifier-profile/v1","type":"string"},"kind":{"const":"repository_patch","type":"string"},"id":{"type":"string","minLength":1,"maxLength":256},"version":{"type":"string","minLength":1,"maxLength":128},"code_digest":{"$ref":"#/$defs/Adapter_Digest"},"runner_profile":{"type":"string","minLength":1,"maxLength":256},"command_id":{"type":"string","minLength":1,"maxLength":256},"command_digest":{"$ref":"#/$defs/Adapter_Digest"},"environment_profile":{"type":"string","minLength":1,"maxLength":256},"network":{"type":"string","enum":["disabled","qualified_allowlist"]},"max_output_bytes":{"type":"integer","minimum":1,"maximum":1048576},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"capability_evidence":{"$ref":"#/$defs/Adapter_CapabilityEvidence"}},"required":["schema","kind","id","version","code_digest","runner_profile","command_id","command_digest","environment_profile","network","max_output_bytes","timeout_seconds","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_UTC":{"type":"string","format":"date-time","pattern":"Z$","maxLength":40,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_VerificationProfile":{"oneOf":[{"$ref":"#/$defs/Adapter_ArtifactVerifierProfile"},{"$ref":"#/$defs/Adapter_RepositoryVerifierProfile"}],"$schema":"https://json-schema.org/draft/2020-12/schema"},"ArtifactRef":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["id","digest"]},"Binding":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"kind":{"type":"string","enum":["tool","skill","connection","worker","repository","reporting","memory"]},"target_id":{"type":"string","format":"uuid"},"permissions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"source_scope":{"$ref":"#/$defs/Scope"},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096}},"required":["id","version","scope","kind","target_id","permissions"]},"Candidate":{"type":"object","additionalProperties":false,"properties":{"plan_id":{"type":"string","format":"uuid"},"base_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"candidate_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"changes":{"type":"array","items":{"$ref":"#/$defs/Change"},"maxItems":4096},"dependencies":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096}},"required":["plan_id","base_revision","candidate_digest","changes","dependencies"]},"Change":{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"organization"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Organization"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"team"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Team"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"project"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Project"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"worker"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Worker"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"binding"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Binding"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"execution_profile"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/ExecutionProfile"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"skill"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Skill"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"connection"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Connection"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"policy"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Policy"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"autonomy_rule"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/PromotionRule"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"schedule"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Schedule"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"responsibility"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Responsibility"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"memory_binding"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/MemoryBinding"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"budget"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Limits"}},"required":["kind","action","id","expected_version","definition"]}]},"Connection":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"provider":{"type":"string","maxLength":8192},"account_identity":{"type":"string","maxLength":8192},"credential_ref":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"allowed_scopes":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"validation_state":{"type":"string","enum":["unverified","valid","invalid","expired","revoked"]},"validated_at":{"type":"string","format":"date-time"},"valid_until":{"type":"string","format":"date-time"}},"required":["id","version","scope","provider","account_identity","credential_ref","destinations","allowed_scopes","validation_state"]},"DecisionRequirement":{"type":"object","additionalProperties":false,"properties":{"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"human_required":{"type":"boolean"},"eligible_principals":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"expires_at":{"type":"string","format":"date-time"},"separate_proposer":{"type":"boolean"}},"required":["action_digest","human_required","eligible_principals","expires_at","separate_proposer"]},"Diagnostic":{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string","maxLength":8192},"code":{"type":"string","maxLength":8192},"message":{"type":"string","maxLength":8192},"severity":{"type":"string","enum":["error","warning","info"]}},"required":["path","code","message","severity"]},"Draft":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"base_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"changes":{"type":"array","items":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"maxItems":4096},"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096}},"required":["id","version","base_revision","changes","diagnostics"]},"ExecutionProfile":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"executor":{"type":"string","enum":["hosted","cooperative"]},"model":{"type":"string","maxLength":8192},"connection_id":{"type":"string","format":"uuid"},"provider_destination":{"type":"string","maxLength":8192},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"cost_bound":{"$ref":"#/$defs/Money"},"classification":{"type":"string","enum":["internal","public","restricted"]},"context_capture":{"type":"string","enum":["complete","partial","advisory"]}},"required":["id","version","executor","model","connection_id","provider_destination","capabilities","cost_bound","classification","context_capture"]},"Limits":{"type":"object","additionalProperties":false,"properties":{"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"spend_micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807},"concurrency":{"type":"integer","minimum":1,"maximum":9223372036854775807},"model_steps":{"type":"integer","minimum":1,"maximum":9223372036854775807},"child_count":{"type":"integer","minimum":0,"maximum":9223372036854775807},"delegation_depth":{"type":"integer","minimum":0,"maximum":9223372036854775807},"attempt_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"root_deadline":{"type":"string","format":"date-time"}},"required":["currency","spend_micro_units","concurrency","model_steps","child_count","delegation_depth","attempt_seconds","root_deadline"]},"MemoryBinding":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"brain_id":{"type":"string","format":"uuid"},"permissions":{"type":"array","items":{"type":"string","enum":["read","write","curate","promote","retract"]},"maxItems":4096},"classification":{"type":"string","enum":["internal","public","restricted"]}},"required":["id","version","scope","brain_id","permissions","classification"]},"Money":{"type":"object","additionalProperties":false,"properties":{"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807}},"required":["currency","micro_units"]},"Organization":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"chief_id":{"type":"string","format":"uuid"},"parent_id":{"type":"string","format":"uuid"},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","key","name","chief_id"]},"Policy":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"rules":{"type":"array","items":{"$ref":"#/$defs/Rule"},"maxItems":4096},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","scope","rules"]},"PolicyResult":{"type":"object","additionalProperties":false,"properties":{"decision":{"type":"string","enum":["allow","deny","review","prerequisite_missing"]},"reasons":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"requirements":{"type":"array","items":{"$ref":"#/$defs/DecisionRequirement"},"maxItems":4096}},"required":["decision","reasons","requirements"]},"Project":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"repositories":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"classification":{"type":"string","enum":["internal","public","restricted"]},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","organization_id","key","name","repositories","bindings","classification"]},"PromotionRule":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"capability":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"required_evidence":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"minimum_successes":{"type":"integer","minimum":1,"maximum":9223372036854775807},"evidence_window_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"disqualifying_events":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"ceiling_grant_id":{"type":"string","format":"uuid"},"human_required_preserved":{"type":"boolean"}},"required":["id","version","scope","capability","destinations","required_evidence","minimum_successes","evidence_window_seconds","disqualifying_events","ceiling_grant_id","human_required_preserved"]},"Ref":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["id","version"]},"Requirement":{"type":"object","additionalProperties":false,"properties":{"code":{"type":"string","maxLength":8192},"message":{"type":"string","maxLength":8192},"resource_id":{"type":"string","format":"uuid"},"challenge_id":{"type":"string","format":"uuid"}},"required":["code","message"]},"Reservation":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"root_task_id":{"type":"string","format":"uuid"},"operation_id":{"type":"string","format":"uuid"},"amount":{"$ref":"#/$defs/Money"},"state":{"type":"string","enum":["reserved","settled","unknown","released"]}},"required":["id","version","scope","operation_id","amount","state"]},"Responsibility":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"worker_id":{"type":"string","format":"uuid"},"outcome":{"type":"string","maxLength":8192},"signals":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"triggers":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"reasoning_policy":{"type":"string","maxLength":8192},"min_interval_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"cycle_limits":{"$ref":"#/$defs/Limits"},"aggregate_limits":{"$ref":"#/$defs/Limits"},"pause_conditions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"escalation_conditions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"acceptance":{"$ref":"#/$defs/Acceptance"},"paused":{"type":"boolean"},"next_wake":{"type":"string","format":"date-time"}},"required":["id","version","scope","worker_id","outcome","signals","triggers","reasoning_policy","min_interval_seconds","cycle_limits","aggregate_limits","pause_conditions","escalation_conditions","acceptance","paused"]},"Rule":{"type":"object","additionalProperties":false,"properties":{"capability":{"type":"string","maxLength":8192},"effect":{"type":"string","enum":["local","disclosure","external_read","external_mutation"]},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"decision":{"type":"string","enum":["allow","deny","review"]},"human_required":{"type":"boolean"},"conditions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["capability","effect","destinations","decision","human_required","conditions"]},"Schedule":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"task_template":{"$ref":"#/$defs/Task"},"timezone":{"type":"string","maxLength":8192},"expression":{"type":"string","maxLength":8192},"misfire":{"type":"string","enum":["coalesce","skip"]},"catch_up_seconds":{"type":"integer","minimum":0,"maximum":9223372036854775807},"paused":{"type":"boolean"},"next_wake":{"type":"string","format":"date-time"}},"required":["id","version","scope","task_template","timezone","expression","misfire","catch_up_seconds","paused"]},"Scope":{"type":"object","additionalProperties":false,"properties":{"installation_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"project_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"}},"required":["installation_id"]},"ScopeSnapshot":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"ancestors":{"type":"array","items":{"$ref":"#/$defs/Organization"},"maxItems":4096},"bindings":{"type":"array","items":{"$ref":"#/$defs/Binding"},"maxItems":4096},"worker":{"$ref":"#/$defs/Worker"},"project":{"$ref":"#/$defs/Project"}},"required":["scope","revision","ancestors","bindings"]},"Skill":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"name":{"type":"string","maxLength":8192},"instruction_artifact":{"$ref":"#/$defs/ArtifactRef"},"content_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"input_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"output_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"requirements":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"dependencies":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"source":{"type":"string","maxLength":8192},"license":{"type":"string","maxLength":8192},"evaluation_refs":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096}},"required":["id","version","name","instruction_artifact","content_digest","input_schema","output_schema","requirements","dependencies","source","license","evaluation_refs","diagnostics"]},"Task":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"owner_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"outcome":{"type":"string","maxLength":8192},"inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"required_outputs":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"acceptance":{"$ref":"#/$defs/Acceptance"},"limits":{"$ref":"#/$defs/Limits"},"dependencies":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"state":{"type":"string","enum":["draft","ready","running","waiting","verifying","succeeded","failed","cancelled"]},"parent_id":{"type":"string","format":"uuid"},"root_id":{"type":"string","format":"uuid"},"waiting_reason":{"type":"string","maxLength":8192},"cancellation_requested":{"type":"boolean"},"manual_acceptance":{"type":"boolean"}},"required":["id","version","scope","owner_id","worker_id","outcome","inputs","required_outputs","acceptance","limits","dependencies","state"]},"Team":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"worker_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","organization_id","key","name","worker_ids"]},"Usage":{"type":"object","additionalProperties":false,"properties":{"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"spent":{"type":"integer","minimum":0,"maximum":9223372036854775807},"reserved":{"type":"integer","minimum":0,"maximum":9223372036854775807},"estimated":{"type":"integer","minimum":0,"maximum":9223372036854775807},"unknown":{"type":"integer","minimum":0,"maximum":9223372036854775807},"advisory":{"type":"boolean"}},"required":["currency","spent","reserved","estimated","unknown","advisory"]},"Validation":{"type":"object","additionalProperties":false,"properties":{"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096},"dependencies":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096}},"required":["diagnostics","requirements","dependencies"]},"Worker":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"purpose":{"type":"string","maxLength":8192},"instructions":{"type":"string","maxLength":8192},"skill_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"profile":{"anyOf":[{"$ref":"#/$defs/ExecutionProfile"},{"type":"null"}]},"limits":{"anyOf":[{"$ref":"#/$defs/Limits"},{"type":"null"}]},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","organization_id","key","name","purpose","instructions","skill_versions","bindings","profile","limits"]}}}
```

## Named acceptance cases

Tests are implementation deliverables, not claims of already executed qualification. Retain expected/observed results, exact source/config/tool versions and failure evidence.

### Z06.admission_claim_revoke — Z06

Setup: A controlled adapter exposes barriers before admission, before claim and before invocation.

Action: Race revocation or pause against admission and claim in each ordering.

Expected:

- Revocation committed before claim prevents invocation; physical call count is zero.
- Admission reserves and writes intent/evidence atomically; claimed dispatch follows the specified irreversible boundary.
- Revocation after claim is recorded honestly and does not claim to retract transmitted bytes.

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

### Z09.concurrent_reservations — Z09

Setup: Installation, ancestor organizations, project, worker and root-task budgets are nearly exhausted.

Action: Concurrently admit child tasks, evaluation and model calls competing for the remaining funds.

Expected:

- Atomic reservations across all applicable scopes prevent overcommit and use deterministic lock/update order.
- Children and retries consume shared root/ancestor limits rather than independent copies.

### Z09.unknown_external_cost — Z09

Setup: A cooperative external worker or provider cannot establish a reliable charge bound.

Action: Admit work requiring a hard spending cap and report unknown/advisory external usage.

Expected:

- Unenforceable hard-cap mode is refused or explicitly advisory only where policy permits.
- Unknown and advisory usage is reported separately and never treated as zero.

### Z09.missing_price — Z09

Setup: A model profile lacks a required price bound, currency or configured spending limit.

Action: Attempt a paid model step or memory/evaluation operation requiring that provider.

Expected:

- Admission returns a named prerequisite or budget refusal without a provider call.
- There is no hardcoded account, provider, billing or model fallback.

### Z09.settlement_recovery — Z09

Setup: Reservations include confirmed spend, refundable unused bounds and unknown attempts using exact micro-units and rational rates.

Action: Record each disposition with a crash before or after transaction commit and recover.

Expected:

- Spent, reserved, estimated and unknown values remain distinct and exact without floating-point drift.
- Settlement is atomic and idempotent; unresolved reservations survive restart and are not released twice.

### Z12.delegation_narrows — Z12

Setup: A root task has finite scope, allowed tools/destinations, project data, deadline and shared budget.

Action: Independently delegate children expanding scope, tool access, data access, deadline or spending authority.

Expected:

- Each expansion is refused or reduced only through an explicit valid narrowing contract.
- Delegation cannot grant authority absent from its parent or inherited organization policy.

### Z12.root_limits_shared — Z12

Setup: A root task is configured with finite child count, depth, concurrency, planner steps and wall-time limits.

Action: Delegate concurrently and recursively until each limit is reached.

Expected:

- All branches share root budgets and finite limits; limits cannot be reset by making another child.
- Default limits match one worker attempt, four installation attempts, 30 minutes per attempt, 100 steps, eight children, depth three and a 24-hour root deadline where applicable.

### Z14.outbox_revocation_restore — Z14

Setup: The backup contains unresolved dispatch/outbox intents, unknown reservations, revoked credentials and pending memory writes/promotions.

Action: Restore, inspect state, attempt use of revoked credentials and reconcile before resuming.

Expected:

- Unresolved obligations and revocation states are preserved and inspectable.
- Pending effect and memory outcomes require reconciliation; restoring an older database does not claim to undo provider effects.

### Z17.ancestor_budget_exhaustion — Z17

Setup: A child has local funds but an applicable ancestor budget is exhausted.

Action: Admit a paid child task or model call.

Expected:

- Admission is refused without provider dispatch.
- Every descendant reserves against applicable ancestor budgets and cannot bypass an exhausted aggregate limit.

### Z18.chief_promotion_provenance — Z18

Setup: A chief has explicit source read/disclosure and destination curate/write permissions under finite budgets.

Action: Promote a suitable claim from worker or child-organization memory into shared memory.

Expected:

- The new destination claim links source brain, claim/version, supporting artifacts, curator identity and redaction.
- Promotion is scoped admitted work; the copied claim is not independent corroboration of its source.

### Z18.memory_write_unknown — Z18

Setup: A Serenity writer commits a command but its acknowledgement is lost; recall may also incur model charges.

Action: Recover the command and run recall under a hard disclosure/spend bound.

Expected:

- The prior persisted intent and adapter command identity are reconciled without blind repeat writing.
- Exactly one canonical writer owns each brain; unsupported enforceable recall charge/disclosure bounds make that mode unavailable or explicitly advisory where policy permits.

### Z20.bounded_idle_spend — Z20

Setup: A responsibility repeatedly finds no useful work and has finite per-cycle/aggregate funds and a minimum interval.

Action: Advance the deterministic clock through many potential cycles.

Expected:

- The minimum interval prevents a tight reasoning loop.
- Aggregate exhaustion or pause conditions stop new spend; idle reasoning cannot create unlimited tasks or spend indefinitely.

### QUALIFICATION.adapter_bounds — QUALIFICATION

Setup: Pin public provider, GitHub, bounded HTTP and Serenity adapters with controlled fault simulators and authorized live qualification fixtures where required.

Action: Exercise cost bounds, disclosure destinations, timeouts, retries, confirmations, repository-head checks, memory freshness and lost acknowledgements.

Expected:

- Record exact adapter/source/protocol/profile versions and observed physical calls.
- Unsupported hard caps, safe-idempotency windows, continuation/context guarantees or memory operations remain unavailable or explicitly advisory as permitted, never inferred from upstream claims.

## Delivery

Implement production behavior and meaningful local tests within scope. Report files changed, commands actually run, observed results, unresolved dependency qualifications and contract defects. A missing dependency may use an exact local fake for development; production must return a named prerequisite/unsupported error instead of fake success. Integration owns shared dependency changes, real assembly, cross-package proving and landing. Do not advertise release or client/platform support from compilation alone.
