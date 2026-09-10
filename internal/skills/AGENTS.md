# Implementation assignment: `internal/skills`

Generated specification revision 1; source digest `c79b39e5ca496cd59114fdaa2c9dd3a36d7c9c0d3b4426bc131063f5cef22ca2`. This file is committed implementation context. Do not independently edit it. Everything required from the product specification and adjacent interfaces is embedded below; no RFC copy is required.

## Mission and scope

Own immutable skill versions, safe import validation and sealed evaluation jobs.

Write scope: **`internal/skills/` only**, excluding this generated AGENTS.md. Go package name: `skills`. Ownership kind: domain; integration wave: 2.

Allowed production imports from this repository: `github.com/zatiti/zatiti/internal/contract`. Tests may use interfaces/fakes defined locally and, once available, storage-backed temporary fixtures; integration owns cross-package tests. No sibling raw SQL. No root dependency edits except the integration exception stated in its own brief.

## Implementation decisions and acceptance focus

Own skills_versions/dependencies/evaluations. SKILL.md import adapter follows published Agent Skills metadata, retains exact instruction/support file bytes and provenance/license, and stores Zatiti execution schemas/tool requirements separately. Imported text/frontmatter never installs permission. File enumeration/extraction is local IO phase with strict total/entry/path limits before publication. Reject traversal, symlink escape, devices, duplicate/case collisions, cycles and expansion bombs; do not run any imported script. Imported version starts draft; activation via compiler and explicit bindings only. Evaluations become bounded tasks/effects with pinned evaluator identity/version, sealed fixtures, candidate/model/profile/limits. Store actual observations separately from self-authored tests. Changes invalidate only dependent qualifications through policy. Expose accurate evaluation job status/artifact evidence; failed/skipped evaluation grants nothing.

Local proving focus: Archive/path adversarial corpus, immutable content hashing, dependency cycle, schema changes, no import execution, pinned evaluator, evaluation limits and dependent qualification invalidation.

## Incoming and outgoing boundaries

Incoming callers: application, configuration, application (authenticated public operations).

Outgoing owner calls: `_configuration.stage`, `_policy.invalidate`, `_connections.resolve`, `_effects.admit`, `_effects.prepare`, `_artifacts.metadata`, `_artifacts.publish`, `_execution.job.create`, `_execution.job.record`. Each exact input/output schema appears below. Calls retain current Unit/authority; owner allowlists are mandatory.

Expose `New(contract.Dependencies) (*Service,error)`; `*Service` implements `contract.Module` with Name `skills`, owner-prefixed migrations, all owned descriptors, and strict dispatch. No calls/goroutines during construction. Implement optional authentication/LocalIO interfaces where specified in the common contract. Tables are private under `skills_`; external callers rely only on methods and schemas.

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

### R7.1-002 (source section 7.1; primary owner skills)

A skill package contains instructions, optional bounded supporting files, input/output schemas, declared tool/data requirements, and optional evaluation fixtures. Support `SKILL.md` packages using the published Agent Skills format through a validating import adapter; store executable meaning in Zatiti's versioned metadata rather than inventing authority from frontmatter. Skills are reusable across organizations only through explicit bindings.

### R7.1-003 (source section 7.1; primary owner skills)

Import creates an immutable draft version with content hashes, source and license provenance, dependencies, and diagnostics. Reject path traversal, symlink escapes, device files, duplicate/case-colliding paths, oversized extraction, and dependency cycles before publication. Imported scripts do not execute during discovery, import, or archive extraction. Imported text and tool output remain untrusted data.

### R7.1-004 (source section 7.1; primary owner skills)

Evaluation can run against sealed fixture inputs before a worker activates. Pin the candidate, evaluator, inputs, model/profile, limits, and expected observations. Agent-generated tests are development evidence; they do not replace an independently accepted completion contract. A changed skill, evaluator, dependency, or model invalidates only the qualifications that depended on it. Evaluation does not automatically expand authority.
## Exact operation and dependency schemas

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

### `_artifacts.publish` v1 — artifacts / internal / mutation / local

Allowed internal callers: controller, execution, memory, skills, installation. Submission key: not required at this internal/query/bootstrap boundary.

Publish metadata only after trusted IO phase has staged/hashed/published bytes; create visible fault if later bytes unavailable.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"size":{"type":"integer","minimum":0,"maximum":9223372036854775807},"media_type":{"type":"string","maxLength":8192},"classification":{"type":"string","enum":["internal","public","restricted"]},"encrypted":{"type":"boolean"}},"required":["scope","digest","size","media_type","classification","encrypted"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Artifact"}},"required":["resource"]}
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

### `_policy.invalidate` v1 — policy / internal / mutation / local

Allowed internal callers: configuration, skills, connections, execution, tasks. Submission key: not required at this internal/query/bootstrap boundary.

Invalidate only dependent qualifications, immediately restrict affected grants and emit explanations before further admission.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"changed_dependencies":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"reason":{"type":"string","maxLength":8192}},"required":["changed_dependencies","reason"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"qualification_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096}},"required":["qualification_ids"]}
```

### `_skills.activate` v1 — skills / internal / mutation / local

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

### `_skills.validate` v1 — skills / internal / query / local

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

### `skill.archive` v1 — skills / public / mutation / local

CLI `zatiti skill archive`; MCP `zatiti_skill_archive`. Submission key: required.

Stage a typed archive in configuration; return draft and resource identity. Never directly activate a definition. Explicit archive checks retained work and obligations; no omission deletion.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Skill"}},"required":["draft","resource"]}
```

### `skill.evaluate` v1 — skills / public / mutation / local

CLI `zatiti skill evaluate`; MCP `zatiti_skill_evaluate`. Submission key: required.

Create admitted evaluation with sealed fixtures/evaluator/profile versions. Skill or evaluator changes invalidate only dependent qualifications; evaluation grants no authority.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"skill":{"$ref":"#/$defs/Ref"},"acceptance":{"$ref":"#/$defs/Acceptance"},"profile":{"$ref":"#/$defs/ExecutionProfile"},"limits":{"$ref":"#/$defs/Limits"}},"required":["scope","skill","acceptance","profile","limits"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"evaluation_id":{"type":"string","format":"uuid"},"passed":{"type":"boolean"},"evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096}},"required":["evaluation_id","passed","evidence"]}
```

### `skill.evaluation.status` v1 — skills / public / query / local

CLI `zatiti skill evaluation status`; MCP `zatiti_skill_evaluation_status`. Submission key: not required at this internal/query/bootstrap boundary.

Inspect actual evaluation disposition and retained evidence.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}
```

### `skill.get` v1 — skills / public / query / local

CLI `zatiti skill get`; MCP `zatiti_skill_get`. Submission key: not required at this internal/query/bootstrap boundary.

Resolve exact identity/version under current authorization; return not_found or permission_denied without cross-scope data disclosure.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Skill"}},"required":["resource"]}
```

### `skill.import` v1 — skills / public / mutation / local

CLI `zatiti skill import`; MCP `zatiti_skill_import`. Submission key: required.

Stage immutable content-hashed skill after bounded archive validation. Never execute imported scripts; reject traversal, escaping symlinks, devices, collisions and dependency cycles.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"artifact":{"$ref":"#/$defs/ArtifactRef"},"source":{"type":"string","maxLength":8192},"license":{"type":"string","maxLength":8192},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","artifact","source","license"]}
```
Output data schema:
```json
{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Skill"}},"required":["resource"]},{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}]}
```

Eventual job result schema (job.get resource.result):
```json
{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Skill"}},"required":["resource"]}
```

### `skill.list` v1 — skills / public / query / local

CLI `zatiti skill list`; MCP `zatiti_skill_list`. Submission key: not required at this internal/query/bootstrap boundary.

Read an authorized consistent snapshot. Apply scope and filter before pagination. Cursor binds principal and query. Filters are structured exact-match fields (AND semantics); unsupported fields for this resource refuse invalid_input. Never interpolate filter strings as SQL.

Input schema:
```json
{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}
```
Output data schema:
```json
{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Skill"},"maxItems":500}},"required":["items"]}
```

### Local schema definitions

The schemas above resolve exclusively against this embedded `$defs` object. Input objects reject additional properties except explicitly open schema/data fields. Field semantics are completed by the owned requirements and operation descriptions. Output `resource`, `items`, `draft`, `job`, etc. are literal keys. Pagination cursor lives in the common envelope.

```json
{"$defs":{"Acceptance":{"type":"object","additionalProperties":false,"properties":{"verifier_id":{"type":"string","maxLength":8192},"verifier_version":{"type":"string","maxLength":8192},"sealed_inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"expected_observations":{"type":"array","items":{"$ref":"#/$defs/Adapter_ExpectedVerificationObservation"},"maxItems":512},"mode":{"type":"string","enum":["independent","manual"]},"required_child_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"profile":{"$ref":"#/$defs/Adapter_VerificationProfile"}},"required":["verifier_id","verifier_version","sealed_inputs","expected_observations","mode","required_child_ids","profile"]},"Action":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"tool":{"$ref":"#/$defs/Ref"},"connection":{"$ref":"#/$defs/Ref"},"account_identity":{"type":"string","maxLength":8192},"destination":{"type":"string","maxLength":8192},"content":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"not_before":{"type":"string","format":"date-time"},"expires_at":{"type":"string","format":"date-time"},"preconditions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"configuration_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"parameters":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"cost_bound":{"$ref":"#/$defs/Money"}},"required":["scope","tool","connection","account_identity","destination","content","not_before","expires_at","preconditions","configuration_revision","parameters","cost_bound"]},"Adapter_ArtifactRef":{"type":"object","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/Adapter_ID"},"digest":{"$ref":"#/$defs/Adapter_Digest"}},"required":["id","digest"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ArtifactVerifierProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verifier-profile/v1","type":"string"},"kind":{"const":"artifact_contract","type":"string"},"id":{"type":"string","minLength":1,"maxLength":256},"version":{"type":"string","minLength":1,"maxLength":128},"code_digest":{"$ref":"#/$defs/Adapter_Digest"},"supported_checks":{"type":"array","items":{"type":"string","enum":["presence","digest","json_schema"]},"minItems":1,"maxItems":3},"max_bytes":{"type":"integer","minimum":1,"maximum":268435456},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"capability_evidence":{"$ref":"#/$defs/Adapter_CapabilityEvidence"}},"required":["schema","kind","id","version","code_digest","supported_checks","max_bytes","timeout_seconds","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_CapabilityEvidence":{"type":"object","additionalProperties":false,"properties":{"artifact":{"$ref":"#/$defs/Adapter_ArtifactRef"},"adapter_version":{"type":"string","minLength":1,"maxLength":128},"source_revision":{"type":"string","minLength":1,"maxLength":128},"protocol_revision":{"type":"string","minLength":1,"maxLength":128},"profile_digest":{"$ref":"#/$defs/Adapter_Digest"},"qualified_at":{"$ref":"#/$defs/Adapter_UTC"},"capabilities":{"type":"array","items":{"type":"string","minLength":1,"maxLength":128},"minItems":0,"maxItems":128},"limitations":{"type":"array","items":{"type":"string","minLength":1,"maxLength":2048},"minItems":0,"maxItems":128}},"required":["artifact","adapter_version","source_revision","protocol_revision","profile_digest","qualified_at","capabilities","limitations"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_Digest":{"type":"string","pattern":"^[0-9a-f]{64}$","maxLength":64,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ExpectedVerificationObservation":{"type":"object","additionalProperties":false,"properties":{"check_id":{"type":"string","minLength":1,"maxLength":256},"kind":{"type":"string","enum":["artifact_presence","artifact_digest","json_schema","repository_patch_applies","repository_command"]},"expected":{"type":"string","enum":["pass","fail"]},"artifact_name":{"type":"string","minLength":1,"maxLength":128},"expected_digest":{"$ref":"#/$defs/Adapter_Digest"},"schema":{"$ref":"#/$defs/Adapter_InertSchema"},"command_id":{"type":"string","minLength":1,"maxLength":256},"expected_exit_code":{"type":"integer","minimum":-2147483648,"maximum":2147483647}},"required":["check_id","kind","expected"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_ID":{"type":"string","format":"uuid","maxLength":36,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_InertSchema":{"type":"object","description":"A bounded JSON Schema 2020-12 document. Local references only; validators reject remote references, duplicate keys, excessive depth/node count and unsupported executable behavior. This is schema data, not authority.","maxProperties":256,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_RepositoryVerifierProfile":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.verifier-profile/v1","type":"string"},"kind":{"const":"repository_patch","type":"string"},"id":{"type":"string","minLength":1,"maxLength":256},"version":{"type":"string","minLength":1,"maxLength":128},"code_digest":{"$ref":"#/$defs/Adapter_Digest"},"runner_profile":{"type":"string","minLength":1,"maxLength":256},"command_id":{"type":"string","minLength":1,"maxLength":256},"command_digest":{"$ref":"#/$defs/Adapter_Digest"},"environment_profile":{"type":"string","minLength":1,"maxLength":256},"network":{"type":"string","enum":["disabled","qualified_allowlist"]},"max_output_bytes":{"type":"integer","minimum":1,"maximum":1048576},"timeout_seconds":{"type":"integer","minimum":1,"maximum":1800},"capability_evidence":{"$ref":"#/$defs/Adapter_CapabilityEvidence"}},"required":["schema","kind","id","version","code_digest","runner_profile","command_id","command_digest","environment_profile","network","max_output_bytes","timeout_seconds","capability_evidence"],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_UTC":{"type":"string","format":"date-time","pattern":"Z$","maxLength":40,"$schema":"https://json-schema.org/draft/2020-12/schema"},"Adapter_VerificationProfile":{"oneOf":[{"$ref":"#/$defs/Adapter_ArtifactVerifierProfile"},{"$ref":"#/$defs/Adapter_RepositoryVerifierProfile"}],"$schema":"https://json-schema.org/draft/2020-12/schema"},"Artifact":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"size":{"type":"integer","minimum":0,"maximum":9223372036854775807},"media_type":{"type":"string","maxLength":8192},"classification":{"type":"string","enum":["internal","public","restricted"]},"encrypted":{"type":"boolean"},"state":{"type":"string","enum":["available","fault"]},"created_at":{"type":"string","format":"date-time"}},"required":["id","version","scope","digest","size","media_type","classification","encrypted","state","created_at"]},"ArtifactRef":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["id","digest"]},"Binding":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"kind":{"type":"string","enum":["tool","skill","connection","worker","repository","reporting","memory"]},"target_id":{"type":"string","format":"uuid"},"permissions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"source_scope":{"$ref":"#/$defs/Scope"},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096}},"required":["id","version","scope","kind","target_id","permissions"]},"Candidate":{"type":"object","additionalProperties":false,"properties":{"plan_id":{"type":"string","format":"uuid"},"base_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"candidate_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"changes":{"type":"array","items":{"$ref":"#/$defs/Change"},"maxItems":4096},"dependencies":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096}},"required":["plan_id","base_revision","candidate_digest","changes","dependencies"]},"Change":{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"organization"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Organization"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"team"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Team"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"project"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Project"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"worker"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Worker"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"binding"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Binding"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"execution_profile"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/ExecutionProfile"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"skill"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Skill"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"connection"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Connection"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"policy"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Policy"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"autonomy_rule"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/PromotionRule"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"schedule"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Schedule"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"responsibility"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Responsibility"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"memory_binding"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/MemoryBinding"}},"required":["kind","action","id","expected_version","definition"]},{"type":"object","additionalProperties":false,"properties":{"kind":{"const":"budget"},"action":{"type":"string","enum":["create","update","archive","delete"]},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":0,"maximum":9223372036854775807},"definition":{"$ref":"#/$defs/Limits"}},"required":["kind","action","id","expected_version","definition"]}]},"Connection":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"provider":{"type":"string","maxLength":8192},"account_identity":{"type":"string","maxLength":8192},"credential_ref":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"allowed_scopes":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"validation_state":{"type":"string","enum":["unverified","valid","invalid","expired","revoked"]},"validated_at":{"type":"string","format":"date-time"},"valid_until":{"type":"string","format":"date-time"}},"required":["id","version","scope","provider","account_identity","credential_ref","destinations","allowed_scopes","validation_state"]},"Diagnostic":{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string","maxLength":8192},"code":{"type":"string","maxLength":8192},"message":{"type":"string","maxLength":8192},"severity":{"type":"string","enum":["error","warning","info"]}},"required":["path","code","message","severity"]},"Draft":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"base_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"changes":{"type":"array","items":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"maxItems":4096},"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096}},"required":["id","version","base_revision","changes","diagnostics"]},"ExecutionProfile":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"executor":{"type":"string","enum":["hosted","cooperative"]},"model":{"type":"string","maxLength":8192},"connection_id":{"type":"string","format":"uuid"},"provider_destination":{"type":"string","maxLength":8192},"capabilities":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"cost_bound":{"$ref":"#/$defs/Money"},"classification":{"type":"string","enum":["internal","public","restricted"]},"context_capture":{"type":"string","enum":["complete","partial","advisory"]}},"required":["id","version","executor","model","connection_id","provider_destination","capabilities","cost_bound","classification","context_capture"]},"Job":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"kind":{"type":"string","maxLength":8192},"state":{"type":"string","enum":["pending","running","succeeded","failed","outcome_unknown","cancelled"]},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096},"result_artifact":{"$ref":"#/$defs/ArtifactRef"},"operation_id":{"type":"string","format":"uuid"},"owner":{"type":"string","maxLength":8192},"operation":{"type":"string","maxLength":8192},"result":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","kind","state","requirements","owner","operation"]},"Limits":{"type":"object","additionalProperties":false,"properties":{"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"spend_micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807},"concurrency":{"type":"integer","minimum":1,"maximum":9223372036854775807},"model_steps":{"type":"integer","minimum":1,"maximum":9223372036854775807},"child_count":{"type":"integer","minimum":0,"maximum":9223372036854775807},"delegation_depth":{"type":"integer","minimum":0,"maximum":9223372036854775807},"attempt_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"root_deadline":{"type":"string","format":"date-time"}},"required":["currency","spend_micro_units","concurrency","model_steps","child_count","delegation_depth","attempt_seconds","root_deadline"]},"MemoryBinding":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"brain_id":{"type":"string","format":"uuid"},"permissions":{"type":"array","items":{"type":"string","enum":["read","write","curate","promote","retract"]},"maxItems":4096},"classification":{"type":"string","enum":["internal","public","restricted"]}},"required":["id","version","scope","brain_id","permissions","classification"]},"Money":{"type":"object","additionalProperties":false,"properties":{"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807}},"required":["currency","micro_units"]},"Operation":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"action":{"$ref":"#/$defs/Action"},"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"state":{"type":"string","enum":["prepared","awaiting_review","ready","executing","awaiting_confirmation","outcome_unknown","succeeded","failed","denied","expired","cancelled"]},"attempt_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"linked_operation_id":{"type":"string","format":"uuid"},"relationship":{"type":"string","enum":["retry","reconciliation","compensation","replacement"]}},"required":["id","version","action","action_digest","state","attempt_ids"]},"Organization":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"chief_id":{"type":"string","format":"uuid"},"parent_id":{"type":"string","format":"uuid"},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","key","name","chief_id"]},"Policy":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"rules":{"type":"array","items":{"$ref":"#/$defs/Rule"},"maxItems":4096},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","scope","rules"]},"Project":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"repositories":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"classification":{"type":"string","enum":["internal","public","restricted"]},"limits":{"$ref":"#/$defs/Limits"},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","organization_id","key","name","repositories","bindings","classification"]},"PromotionRule":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"capability":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"required_evidence":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"minimum_successes":{"type":"integer","minimum":1,"maximum":9223372036854775807},"evidence_window_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"disqualifying_events":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"ceiling_grant_id":{"type":"string","format":"uuid"},"human_required_preserved":{"type":"boolean"}},"required":["id","version","scope","capability","destinations","required_evidence","minimum_successes","evidence_window_seconds","disqualifying_events","ceiling_grant_id","human_required_preserved"]},"Ref":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["id","version"]},"Requirement":{"type":"object","additionalProperties":false,"properties":{"code":{"type":"string","maxLength":8192},"message":{"type":"string","maxLength":8192},"resource_id":{"type":"string","format":"uuid"},"challenge_id":{"type":"string","format":"uuid"}},"required":["code","message"]},"Responsibility":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"worker_id":{"type":"string","format":"uuid"},"outcome":{"type":"string","maxLength":8192},"signals":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"triggers":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"reasoning_policy":{"type":"string","maxLength":8192},"min_interval_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"cycle_limits":{"$ref":"#/$defs/Limits"},"aggregate_limits":{"$ref":"#/$defs/Limits"},"pause_conditions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"escalation_conditions":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"acceptance":{"$ref":"#/$defs/Acceptance"},"paused":{"type":"boolean"},"next_wake":{"type":"string","format":"date-time"}},"required":["id","version","scope","worker_id","outcome","signals","triggers","reasoning_policy","min_interval_seconds","cycle_limits","aggregate_limits","pause_conditions","escalation_conditions","acceptance","paused"]},"Rule":{"type":"object","additionalProperties":false,"properties":{"capability":{"type":"string","maxLength":8192},"effect":{"type":"string","enum":["local","disclosure","external_read","external_mutation"]},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"decision":{"type":"string","enum":["allow","deny","review"]},"human_required":{"type":"boolean"},"conditions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["capability","effect","destinations","decision","human_required","conditions"]},"Schedule":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"task_template":{"$ref":"#/$defs/Task"},"timezone":{"type":"string","maxLength":8192},"expression":{"type":"string","maxLength":8192},"misfire":{"type":"string","enum":["coalesce","skip"]},"catch_up_seconds":{"type":"integer","minimum":0,"maximum":9223372036854775807},"paused":{"type":"boolean"},"next_wake":{"type":"string","format":"date-time"}},"required":["id","version","scope","task_template","timezone","expression","misfire","catch_up_seconds","paused"]},"Scope":{"type":"object","additionalProperties":false,"properties":{"installation_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"project_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"}},"required":["installation_id"]},"Skill":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"name":{"type":"string","maxLength":8192},"instruction_artifact":{"$ref":"#/$defs/ArtifactRef"},"content_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"input_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"output_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"requirements":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"dependencies":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"source":{"type":"string","maxLength":8192},"license":{"type":"string","maxLength":8192},"evaluation_refs":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096}},"required":["id","version","name","instruction_artifact","content_digest","input_schema","output_schema","requirements","dependencies","source","license","evaluation_refs","diagnostics"]},"Task":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"owner_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"outcome":{"type":"string","maxLength":8192},"inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"required_outputs":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"acceptance":{"$ref":"#/$defs/Acceptance"},"limits":{"$ref":"#/$defs/Limits"},"dependencies":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"state":{"type":"string","enum":["draft","ready","running","waiting","verifying","succeeded","failed","cancelled"]},"parent_id":{"type":"string","format":"uuid"},"root_id":{"type":"string","format":"uuid"},"waiting_reason":{"type":"string","maxLength":8192},"cancellation_requested":{"type":"boolean"},"manual_acceptance":{"type":"boolean"}},"required":["id","version","scope","owner_id","worker_id","outcome","inputs","required_outputs","acceptance","limits","dependencies","state"]},"Team":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"worker_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","organization_id","key","name","worker_ids"]},"Tool":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"name":{"type":"string","maxLength":8192},"input_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"output_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"effect":{"type":"string","enum":["local","disclosure","external_read","external_mutation"]},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"credential_kind":{"type":"string","maxLength":8192},"cost_bound":{"$ref":"#/$defs/Money"},"timeout_seconds":{"type":"integer","minimum":1,"maximum":9223372036854775807},"idempotency":{"type":"string","enum":["none","qualified_key","authoritative_nonexecution"]},"key_retention_seconds":{"type":"integer","minimum":0,"maximum":9223372036854775807},"confirmation":{"type":"string","enum":["synchronous","asynchronous","advisory"]},"reconciliation":{"type":"string","maxLength":8192},"adapter":{"type":"string","maxLength":8192}},"required":["id","version","name","input_schema","output_schema","effect","destinations","credential_kind","cost_bound","timeout_seconds","idempotency","key_retention_seconds","confirmation","reconciliation","adapter"]},"Validation":{"type":"object","additionalProperties":false,"properties":{"diagnostics":{"type":"array","items":{"$ref":"#/$defs/Diagnostic"},"maxItems":4096},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096},"dependencies":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096}},"required":["diagnostics","requirements","dependencies"]},"Worker":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"organization_id":{"type":"string","format":"uuid"},"key":{"type":"string","maxLength":8192},"name":{"type":"string","maxLength":8192},"purpose":{"type":"string","maxLength":8192},"instructions":{"type":"string","maxLength":8192},"skill_versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096},"bindings":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"profile":{"anyOf":[{"$ref":"#/$defs/ExecutionProfile"},{"type":"null"}]},"limits":{"anyOf":[{"$ref":"#/$defs/Limits"},{"type":"null"}]},"extensions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","organization_id","key","name","purpose","instructions","skill_versions","bindings","profile","limits"]}}}
```

## Named acceptance cases

Tests are implementation deliverables, not claims of already executed qualification. Retain expected/observed results, exact source/config/tool versions and failure evidence.

### Z03.agent_setup_mcp — Z03

Setup: A clean installation and scoped client-agent profile exist; paid execution is initially unavailable.

Action: Using MCP discovery alone, create an organization and workers, import/evaluate a skill, set up and validate a connection, resolve prerequisites, plan/apply and start work.

Expected:

- The complete authorized setup succeeds without dashboard or repository-source knowledge.
- A verified output artifact is inspectable; paid work remains refused until provider, currency and limits are configured.
- Credential prerequisites return actionable challenges without secret bytes in model-visible input or output.

### Z03.agent_setup_cli — Z03

Setup: Create a fixture equivalent to the MCP setup case with the same authority.

Action: Perform the complete discovery, organization/skill/worker/connection, prerequisite, activation and first-work journey using CLI JSON mode.

Expected:

- The CLI completes the same lifecycle and produces equivalent authorized state and verified output.
- No TTY or silent prompt is required; incomplete input returns a structured actionable result.

### Z05.malicious_instructions — Z05

Setup: A skill includes instructions to read secrets, self-grant permissions and ignore reviews.

Action: Import, bind and execute the skill in a constrained task.

Expected:

- Instructions remain untrusted input and install no grants.
- Tool, data and effect access remains limited to the pinned authorized binding closure.

### Z05.archive_safety — Z05

Setup: Prepared archives contain traversal, absolute paths, symlink escapes, device entries, duplicate/case-colliding paths, oversized expansion and dependency cycles.

Action: Import every malicious archive plus a bounded valid control archive.

Expected:

- Every unsafe archive is rejected before publication and performs no executable side effects.
- Valid import produces immutable hashed draft content and source/license provenance; discovery and extraction execute no scripts.

### Z15.canonical_roundtrip — Z15

Setup: A definition contains ordered semantic lists, unordered maps/sets, exact skill bytes, extensions and explicit removals.

Action: Export, import, plan, activate and export again within the same installation.

Expected:

- Canonical definition meaning and stable identities round-trip without changing semantic order or skill bytes.
- Duplicate keys, invalid references, unsafe paths, unknown executable fields and cycles are rejected; omission is not deletion.

### Z19.version_requalification — Z19

Setup: A worker has qualifications depending on recorded model, tool, skill and evaluator versions.

Action: Change one relevant version, and separately change an unrelated dependency.

Expected:

- Affected qualifications require requalification before qualifying further admission.
- Only qualifications that depended on the changed configuration are invalidated; unsupported prior evidence is not reused silently.

### JOURNEY.organization_setup_cli — JOURNEY

Setup: Use a fresh controlled installation and scoped external agent principal.

Action: Discover capabilities, create an organization and two workers, import/evaluate a skill, establish a provider connection, plan/apply and complete first work entirely through CLI.

Expected:

- The first artifact is independently verified and inspectable.
- Provider secrets never enter agent-visible arguments/results; prerequisite resolution requires no dashboard or source knowledge.
- Retain operation traces, state/event observations and verification artifacts.

### JOURNEY.organization_setup_mcp — JOURNEY

Setup: Use a matching fresh installation with a scoped agent MCP client.

Action: Perform the full organization, two-worker, skill evaluation, provider setup, activation and first verified artifact journey entirely through MCP.

Expected:

- The same authorized product lifecycle and evidence is obtained as through CLI.
- Every prerequisite and long-running operation remains discoverable and inspectable without optional MCP features.

## Delivery

Implement production behavior and meaningful local tests within scope. Report files changed, commands actually run, observed results, unresolved dependency qualifications and contract defects. A missing dependency may use an exact local fake for development; production must return a named prerequisite/unsupported error instead of fake success. Integration owns shared dependency changes, real assembly, cross-package proving and landing. Do not advertise release or client/platform support from compilation alone.
