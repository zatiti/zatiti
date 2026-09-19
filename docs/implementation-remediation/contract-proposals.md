# Contract changes required before dispatch

These are prescriptive design inputs for P00, **not implemented APIs and not amendments to the currently frozen revision 2 contracts**. P00 must turn them into exact JSON Schemas, Go interfaces, migrations, caller lists and committed generated assignments. Downstream models must code against that landed revision, not guess omitted fields from this document. Preserve the existing result envelope, fault codes, submission identities, authorization and transaction rules.

Use existing ownership roots. Execution owns intent interpretation and worker turns; messaging owns delivery/history; tasks owns accepted work; controller drives bounded IO and recovery. Do not create an unowned “planner service” that bypasses these owners. A conversation turn can exist without a task: otherwise asking the chief to create the first task creates a circular prerequisite.

## 1. Durable identity and state

Add an execution-owned `WorkerTurn` persisted with these fields:

| Field | Type / rule |
|---|---|
| `id`, `worker_id`, `principal_id` | UUIDs; worker/principal mapping resolved by identity, never chosen by model output |
| `scope` | Existing Scope; authorized intersection at admission and again at effect admission |
| `source` | Tagged `message`, `task`, `responsibility` or `continuation` source with its ID and version; message includes recipient ID |
| `requester_id` | Authenticated originating principal; retained for attribution, not impersonated during execution |
| `conversation_id`, `task_id`, `run_id`, `attempt_id` | Optional UUIDs as appropriate; task-related IDs required for task turns |
| `version`, `configuration_revision` | Positive versions; optimistic mutation fences |
| `state` | `pending`, `claimed`, `context_pending`, `model_pending`, `proposal_pending`, `waiting`, `reporting`, `completed`, `failed`, `cancelled` |
| `waiting_reason` | Typed reason: `setup`, `clarification`, `review`, `effect`, `dependency`, `budget`, `recovery`; associated resource IDs and next wake when applicable |
| `generation`, `lease_id`, `lease_expires_at` | Controller generation and attempt lease; stale worker cannot continue |
| `limits`, `root_id`, `steps_used` | Finite accepted limits; shared cumulative root accounting across replacements |
| `context_artifact`, `last_observation_id` | Published immutable references, never made-up IDs for unstored bytes |
| `created_at`, `updated_at` | Controller timestamps |

Unique source identity is `(installation_id, source_kind, source_id, source_version, recipient_worker_id)`; re-admission returns the same turn. A pending inbox message and its turn admission/processed marker commit in one shared transaction. A message arriving during an active turn is admitted as a safe-boundary injection with a durable message/context link; never silently dropped or processed twice. Record one active decision stream per worker/task lane, while different eligible workers may run concurrently within aggregate limits.

Store each model step/proposal separately: turn ID, step index, provider proposal ID, exact source-context digest, normalized proposal bytes, command/effect ID, result artifact, state and timestamps. Unique `(turn_id, step_index, proposal_id)` is the proposal deduplication fence. Same key with different bytes is `submission_conflict`.

## 2. Exact operation inventory to add or revise

All operation inputs below are strict objects; use existing Scope, Ref, ArtifactRef, Limits and result envelope definitions. Lists accept `limit` 1–100 and an opaque cursor, return `items` and an envelope cursor. Mutations carry an expected version where named. Public mutations require a submission key. P00 adds the detailed output definitions, bounds and non-success examples; do not leave inert arbitrary objects where executable semantics are required.

| Operation | Owner; allowed caller | Input / output / atomic behavior |
|---|---|---|
| `identity.current` | identity; authenticated public | `{scope}` → authenticated principal metadata; no secret/ref lookup or actor selection |
| `_identity.worker.resolve` | identity; application, execution, configuration, installation | `{scope, worker_id}` → current worker principal and allowed scope; reject missing/revoked mapping |
| `task.start` | tasks; public | `{scope,id,expected_version}` → ready Task and associated Run reference; run enqueue in same transaction; existing draft-only create stays compatible |
| `_tasks.evidence.record` | tasks; execution | `{task_id,attempt_id,acceptance_digest,verification_artifact,output_bindings,verdict}` → recorded evidence refs; only trusted verified lineage can establish success |
| `_tasks.dependencies.wake` | tasks; execution, controller | bounded completed-task source/cursor → revalidated eligible dependents; no success for a failed required child |
| `_messaging.ready` | messaging; execution, controller | bounded scoped recipient scan → worker IDs/source message IDs; fair ordering, no acknowledgment |
| `_messaging.processed` | messaging; execution | `{message_id,recipient_id,turn_id,context_artifact?}` → durable delivery disposition; must share the turn/context commit Unit |
| `conversation.message.list` | messaging; public | `{scope,conversation_id,cursor?,limit?}` → authorized sent/received Message history including only disclosed membership intervals |
| `conversation.get/list` revision | messaging; public | add current caller's preview/read marker/unread projection; preserve quiet coordination and metadata authorization |
| `_execution.turn.admit` | execution; controller, scheduling | `{source,worker_id,scope}` → WorkerTurn; derive requester/limits from durable source, not asserted fields |
| `_execution.work.pending` | execution; controller | bounded scan → typed claim/context/proposal/resume work items; includes ready hosted runs and safely waiting turns; excludes cooperative auto-claims |
| `_execution.work.claim` | execution; controller | `{work_id,expected_version,generation}` → immutable work item plus claim token/version; retries inspect the same claim |
| `_execution.context.prepare` | execution; controller | `{turn_id,expected_version,generation}` → context plan with exact authorized refs, versions and byte/token bounds; no IO in Unit |
| `_execution.context.commit` | execution; controller | `{plan_id,expected_version,generation,staged_context}` → published Context/Turn; recheck pins and current authority; stale plan cannot dispatch |
| `_execution.proposal.prepare` | execution; controller | `{turn_id,step_index,proposal_id,expected_version}` → typed proposal from persisted normalized model evidence; caller cannot submit substituted proposal bytes |
| `_execution.proposal.record` | execution; controller | `{proposal_id,expected_version,command_result_or_effect_ref}` → next durable turn disposition and result artifact refs; duplicate same result replays |
| `_execution.report` | execution; controller | turn/attempt/version/lease + published named outputs → same report/verification transition as cooperative attempt.report, with current worker subject |
| `_execution.verification.pending/claim` | execution; controller | bounded scan / exact version+generation claim → sealed VerificationRequest and trusted verifier identity; no worker can claim verifier authority |
| `_execution.verification.record` revision | execution; controller | record exact claimed request/attempt/version plus independently produced result; idempotent duplicate, mismatched bytes refuse |
| `_execution.job.get` | execution; controller, application and exact owning module | `{job_id}` → current owned claim/version/generation/original input; resolves lost claim ack without scanning another owner's tables |
| `_effects.prepare/claim` revisions | effects; existing owners/controller | persist validated typed callback route and dispatch mode separately from adapter parameters; dispatch exposes only authoritative mode |
| `_effects.reconciliation.prepare/record` | effects; controller | original operation/version + qualified lookup → separate policy/accounted read; observation merges into original uncertainty; no replay of original write |
| `_scheduling.cycle.record` revision | scheduling; execution | include unique cycle/turn ID and typed next-wake decision; same result replays, different result conflicts |
| `_skills.evaluation.record` | skills; execution/controller | exact evaluation/job/version + published verifier evidence → evaluation state; current immutable skill version must match |
| `_configuration.export.prepare/record` | configuration; controller/application | immutable export snapshot/versions → published Job/Artifact with canonical definitions; no phantom completed job |
| `memory.list` and inspection revisions | memory; public | scoped authorized claim refs, source/freshness/lineage and supported actions; never query unauthorized brains |
| `event.list` revision | evidence; public | drained results retain resume position; filter/scope/principal/retention remain bound |

P00 must also spell out the small query/result revisions needed for artifact names/provenance, verification observations, task/turn status, installed verifier profiles, responsibility cycle linkage and runtime readiness. Each addition must have both CLI/MCP mapping. Keep public operation versions compatible where an optional addition is allowed; breaking strict schemas require an explicit new operation/schema version and a documented old-record reader.

## 3. Worker operation executor

Add a narrow `contract.WorkerOperator` capability. Proposed method:

```go
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
type WorkerOperator interface {
    ExecuteWorker(context.Context, WorkerRequest) (Result, error)
}
```

P00 finalizes the exact Go primitive types using existing contract declarations. Implement in application; inject only into trusted runtime composition. It resolves actor from persisted turn/worker mapping and re-enters ordinary application authorization. `WorkerID` is an asserted match against that turn, not a way to select any principal. The controller's administrative identity authorizes scheduling/bookkeeping only; model proposals execute under the worker's scoped authority intersected with the task and source authorization envelope. Source user text never becomes authority. Worker effects can spend only their configured envelope.

Use a deterministic submission key derived from turn/step/proposal identity. Store it before execution and use command lookup on uncertain acknowledgment. A local operation uses the same policy, replay and LocalIO route as CLI/MCP. Do not call public handlers directly with an invented Actor and do not use controller privileges to bypass a worker denial.

Local model-visible operations are an explicit allowlist: authorized configuration authoring/inspection, task/delegation/responsibility actions, approved memory and messaging operations, output publication. Grant/policy changes, secrets, internal bookkeeping and human review do not become available merely because they exist in the public catalog. Any legitimately delegated configuration change still checks old authority before apply. Model cannot nominate an arbitrary operation ID through a generic execute-anything tool.

## 4. Context and model output

Keep `zatiti.context/v1`, existing ContextPart/ModelOutput definitions and the real Responses adapter as the starting point. Context assembly is three phases:

1. Prepare a versioned immutable plan through execution ports: exact accepted task, worker/instructions/skill closure, authorized conversation/inbox, tool contracts, selected memory, previous context, outputs and remaining limits.
2. Outside Unit, read bounded bytes, preserve transcript ordering, label origin/classification, truncate only by a deterministic recorded policy, stage canonical context bytes. No provider call yet.
3. Publish bytes/metadata and commit the pinned context after rechecking generation/versions/authority. A stale plan is discarded/rebuilt without spending or sending.

The current execution parameters `{attempt_id,run_id,model}` do **not** satisfy the Responses adapter. Emit exactly:

```json
{
  "schema": "zatiti.responses.action/v1",
  "kind": "model_step",
  "context_artifact": {"id": "<published UUID>", "digest": "<sha256>"},
  "max_output_tokens": 1024,
  "tool_contract_versions": []
}
```

`continuation_reference` is optional only when supported and scoped to the same conversation/context lineage. Actual token bound is the accepted profile/task remaining allowance, not the example 1024. Resolve Action.tool from the trusted executable model tool; resolve Action.connection and account from the exact connection snapshot. ExecutionProfile.id is not inherently a Tool.id. Keep route IDs in effect metadata, since strict Responses parameters reject them.

Interpret normalized adapter evidence as follows:

| Observation / decision | Required behavior |
|---|---|
| Provider `accepted` without confirmed model output | await confirmation/reconciliation; do not call the next step |
| Definite failure/refusal | retain usage/evidence and fail or request a narrowly bounded correction according to accepted policy |
| Unknown/timeout after possible send | preserve liability, wait for recovery; no automatic fresh call |
| Malformed/truncated output | bounded recorded failure/correction; never execute partially parsed tools |
| Text reply/clarification | publish reply once; complete chat turn or wait on a correlated reply; not task success |
| Tool proposal | validate ID, schema, source context, pinned tool and allowed operation; dispatch through worker authority; wait on committed result |
| Configuration/task/responsibility proposal | use local governed APIs; draft/plan/apply checks remain intact; only the required exact action may request review |
| Delegation | narrow parent scope/limits/acceptance; create child via tasks and attach lineage; no unlimited recursive spawning |
| Task completion | publish output bindings, enter report/verification; only independent accepted evidence or explicitly labeled manual acceptance completes task |
| No-work/wait/escalate | record reason, accountable artifacts and next wake; no unbounded thinking loop |

If the model needs an explicit final/clarification tag beyond current ModelOutput, use registered local decision tools with strict schemas (`reply`, `clarify`, `report_outputs`, `cycle_decision`) in the context. They are execution-local proposals, not arbitrary JSON free text and not provider-side executable tools. P00 seals their names/inputs before P16. A final text answer alone may complete a chat turn, but never fulfills task required outputs implicitly.

## 5. Output acceptance and independent verifier

Freeze **output names and checks**, not unknowable generated bytes. An accepted output slot includes name, classification, media type, max bytes and check definitions. `artifact_presence`/`json_schema` identify that slot; `artifact_digest` for known expected content additionally pins a digest. On report, the runtime binds each slot to a published artifact from that attempt and seals the verification request. After binding, bytes/digests cannot change. The accepted task/criterion digest stays immutable throughout.

VerificationRequest.Outputs must be populated from those bindings. Verifier reads the resolved actual bytes, establishes observations and emits published request/verdict artifacts. Tasks records this trusted evidence through its owner port before evaluating success. Existing implementation generates empty Outputs and tasks' presence validation insists on an admission-time digest; fix both together.

Verifier code/profile identity must refer to the installed trusted verifier, checked against actual assembled build/capability evidence. Matching a worker-supplied `independent: true` or an arbitrary evidence blob is insufficient. Repository checks use a pinned base, patch artifact and accepted command IDs, with a truthful controlled-runner capability declaration. Generic shell execution is not proof of containment.

## 6. Durable jobs and IO interfaces

Use execution as the one durable job ledger. Every job carries owner, operation, source ID, input digest, current version, scope, state, claim generation/token, result artifacts and typed requirements. Job.get for a returned ID must resolve a persisted row. Owners retain their domain rows and receive idempotent typed completion callbacks. Network-backed jobs link an effects operation and are not mistaken for ordinary local runners; reconciliation is an explicitly admitted lookup, not an unclaimable job pointing back at the original unknown effect.

Move the minimal shared job types out of controller-specific definitions into contract (or use thin entrypoint wrappers over exact domain methods; P00 chooses the former to avoid domain→controller imports). Freeze:

```go
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
```

Define shared Requirement with existing code/message and optional resource_id/challenge_id fields; preserve its strict schema. Local runners do no direct domain writes: prepare/claim occurs through owner ports, RunJob performs bounded approved IO outside Unit, controller publishes outputs, then owner finish and execution record commit together. A runner that needs governed network IO must create effects through its owner pipeline instead of invoking a provider ad hoc. Verifier remains a distinct trusted interface. Context builder is also outside-Unit IO with an immutable prepared plan, not a generic job that can read arbitrary files.

Minimum complete job registry: skill.evaluate; configuration organization/team/project export; run.export; operation.reconcile; verification; existing network connection/memory jobs with owner callbacks. Restore is a lifecycle handoff, **not** an ordinary in-process job that swaps a database while it is open. Enumerate every actual job producer during P00; classify synchronous LocalIO jobs separately so controller never races them.

Recover lost claim acknowledgement with exact job.get/claim token, not “not found in pending means unknowable.” Rerun only demonstrably pure/read-only or safely replayable IO; uncertain external work stays unknown. A terminal callback replay returns the same result; different bytes conflict.

## 7. Restore and backup capabilities

Keep domains off storage file paths. Freeze a narrow entrypoint-owned `SnapshotInventory`/offline `RestoreCoordinator` capability separate from ordinary DatabaseBackup. Its prepared bundle includes database image+schema versions, referenced encrypted artifact bytes, brain snapshots, key prerequisites, configuration/profile revisions and retained obligations. Use bounded streams and digests; do not load arbitrarily large backups into memory.

Required restore protocol:

1. Enter exclusive maintenance; close new admissions and account for in-flight effects.
2. Verify source bundle, all required artifacts, installation binding, schema compatibility and secure key availability in staging.
3. Capture an authenticated **current** monotonic overlay containing complete records for revocations, cancellation, consumed claims, provider dedup keys, unresolved effects/costs and retained evidence.
4. Shut down old application/server/database handles while retaining exclusive installation ownership. Stage image and blobs; journal and atomically switch the database without mixing WAL files.
5. Reopen with a newer controller generation; import overlay through owner-defined merge methods. Never resurrect a revoked credential, resend a consumed dispatch or erase a liability because it was absent in the older snapshot.
6. Verify image/blob digests and record durable restore result. Expose paused state; explicit authorized resume checks prerequisites again.

A clean destination needs a secure key-transfer/provisioning route; an opaque source secret-store reference alone is not portability. P00 specifies owner merge ports and serialized commit phases with no new raw SQL privilege. Missing required Serenity export guarantees blocks a full-memory backup claim; do not fill an empty Brains array and call it complete.

## 8. Handoffs and schema completion gate

P00 must deliver an operation-by-operation table with exact JSON schemas, Go method signatures, allowed callers, transaction/IO phase, version/idempotency key, events, migrations, and positive/negative fixtures. In particular complete the proposed job type, restore capability, context builder and worker operator signatures against the existing primitives. No lower-reasoning implementation assignment starts with unresolved interface choices. Unknown upstream capability is a named prerequisite with a defined refusal test, never a TODO that returns success.

## 9. OpenAI session preparation and existing contract drift

The checked-in adapter currently makes a conversation-creation request followed by a Responses request inside one Invoke. Preserve the one-physical-call-per-claimed-effect invariant by separating them: a strict `prepare_session` action creates the provider conversation and records its authoritative handle; `model_step` names that persisted handle and sends the model input. P00 must version the adapter action schema and define session ownership, destination, classification, expiry and callback result. P13 implements both translations, P15 consumes the resolved handle, P12/P23 journal and dispatch each effect separately. Do not silently change one physical attempt into an unjournaled two-request transaction.

If session creation may have succeeded but its response was lost, retain its unknown outcome without resending. If a model step times out, an empty conversation is not authoritative nonexecution. A lookup that finds output but no usage cannot release worst-case billing liability. These are explicit limitations of the checked-in pin; qualification must verify the actual supported recovery behavior without promising every unknown can be resolved.

P00 also reviews the existing revision-3 decisions recorded in docs/roadmap.md: version-0 registry lookup, self-contained schema closure, CLI path normalization, public/internal callers, controller service identity, staged ArtifactLocator values, review eligibility and internal authority-read gating. Some are already implemented and tested; the task is to align authored contracts and preserve compatibility, not reimplement fixed defects. Historical reports are evidence pointers, not permission to override the current user's instructions.
