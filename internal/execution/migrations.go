package execution

import "github.com/zatiti/zatiti/internal/contract"

// owner is the migration owner and table namespace prefix.
const owner = "execution"

// schemaV1 creates the execution-owned tables: runs pinning ready
// task/version configuration, attempts with their lease, generation and
// reservation bindings, leases, checkpoints, context lineage, verification
// jobs, the shared durable execution_jobs queue, recovery obligations,
// worker pause gates and recorded owned operations.
//
// Scope dimensions are stored both as explicit columns (for filtered reads
// and containment checks) and as the canonical scope JSON snapshot (for
// exact wire output). Timestamps are UTC RFC3339Nano strings.
//
// The partial unique index on live attempts is the storage-level one-owner
// fence: at most one claimed/running/waiting attempt may exist per run,
// so even a code path that misses the conflict check cannot mint a second
// current owner.
const schemaV1 = `
CREATE TABLE execution_runs (
	id                    TEXT PRIMARY KEY,
	version               INTEGER NOT NULL CHECK (version >= 1),
	task_id               TEXT NOT NULL,
	task_version          INTEGER NOT NULL CHECK (task_version >= 1),
	installation_id       TEXT NOT NULL,
	organization_id       TEXT NOT NULL DEFAULT '',
	project_id            TEXT NOT NULL DEFAULT '',
	worker_id             TEXT NOT NULL DEFAULT '',
	task_scope_id         TEXT NOT NULL DEFAULT '',
	scope_json            TEXT NOT NULL,
	configuration_revision INTEGER NOT NULL CHECK (configuration_revision >= 1),
	input_versions_json   TEXT NOT NULL,
	state                 TEXT NOT NULL CHECK (state IN ('ready','running','waiting','verifying','succeeded','failed','cancelled')),
	attempt_ids_json      TEXT NOT NULL DEFAULT '[]',
	created_at            TEXT NOT NULL,
	updated_at            TEXT NOT NULL
);
CREATE INDEX execution_runs_installation_idx
	ON execution_runs (installation_id, state);
CREATE INDEX execution_runs_task_idx
	ON execution_runs (task_id, task_version);

CREATE TABLE execution_attempts (
	id                   TEXT PRIMARY KEY,
	version              INTEGER NOT NULL CHECK (version >= 1),
	run_id               TEXT NOT NULL,
	task_id              TEXT NOT NULL,
	worker_id            TEXT NOT NULL,
	executor             TEXT NOT NULL CHECK (executor IN ('hosted','cooperative')),
	installation_id      TEXT NOT NULL,
	organization_id      TEXT NOT NULL DEFAULT '',
	project_id           TEXT NOT NULL DEFAULT '',
	task_scope_id        TEXT NOT NULL DEFAULT '',
	worker_scope_id      TEXT NOT NULL DEFAULT '',
	scope_json           TEXT NOT NULL,
	generation           INTEGER NOT NULL CHECK (generation >= 1),
	lease_id             TEXT NOT NULL,
	lease_expires_at     TEXT NOT NULL,
	last_heartbeat       TEXT NOT NULL,
	reservation_id       TEXT NOT NULL,
	reservation_version  INTEGER NOT NULL CHECK (reservation_version >= 1),
	state                TEXT NOT NULL CHECK (state IN ('claimed','running','waiting','reported','fenced','stopped','failed')),
	capabilities_json    TEXT NOT NULL DEFAULT '[]',
	context_artifact_json TEXT NOT NULL DEFAULT '',
	recovery_reason      TEXT NOT NULL DEFAULT '',
	model_steps_used     INTEGER NOT NULL DEFAULT 0 CHECK (model_steps_used >= 0),
	outputs_json         TEXT NOT NULL DEFAULT '[]',
	observations_json    TEXT NOT NULL DEFAULT '[]',
	created_at           TEXT NOT NULL,
	updated_at           TEXT NOT NULL
);
CREATE INDEX execution_attempts_installation_idx
	ON execution_attempts (installation_id, state);
CREATE INDEX execution_attempts_run_idx
	ON execution_attempts (run_id, state);
CREATE INDEX execution_attempts_worker_idx
	ON execution_attempts (worker_id, state);
CREATE UNIQUE INDEX execution_attempts_run_current_idx
	ON execution_attempts (run_id) WHERE state IN ('claimed','running','waiting');

CREATE TABLE execution_leases (
	id              TEXT PRIMARY KEY,
	attempt_id      TEXT NOT NULL UNIQUE,
	run_id          TEXT NOT NULL,
	worker_id       TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	generation      INTEGER NOT NULL CHECK (generation >= 1),
	state           TEXT NOT NULL CHECK (state IN ('active','released','expired')),
	expires_at      TEXT NOT NULL,
	last_heartbeat  TEXT NOT NULL,
	created_at      TEXT NOT NULL
);
CREATE INDEX execution_leases_installation_idx
	ON execution_leases (installation_id, state);
CREATE INDEX execution_leases_expiry_idx
	ON execution_leases (state, expires_at);

CREATE TABLE execution_checkpoints (
	id              TEXT PRIMARY KEY,
	attempt_id      TEXT NOT NULL,
	run_id          TEXT NOT NULL,
	lease_id        TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	generation      INTEGER NOT NULL CHECK (generation >= 1),
	context_json    TEXT NOT NULL,
	outputs_json    TEXT NOT NULL DEFAULT '[]',
	created_at      TEXT NOT NULL
);
CREATE INDEX execution_checkpoints_attempt_idx
	ON execution_checkpoints (attempt_id, created_at);

CREATE TABLE execution_context_lineage (
	id              TEXT PRIMARY KEY,
	attempt_id      TEXT NOT NULL,
	run_id          TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	kind            TEXT NOT NULL CHECK (kind IN ('initial','model_output','tool_result','memory_recall','agent_message','mailbox_injection','compaction')),
	artifact_id     TEXT NOT NULL,
	artifact_digest TEXT NOT NULL,
	operation_id    TEXT NOT NULL DEFAULT '',
	created_at      TEXT NOT NULL
);
CREATE INDEX execution_context_lineage_attempt_idx
	ON execution_context_lineage (attempt_id, created_at);

CREATE TABLE execution_verification_jobs (
	id                TEXT PRIMARY KEY,
	attempt_id        TEXT NOT NULL,
	run_id            TEXT NOT NULL,
	task_id           TEXT NOT NULL,
	installation_id   TEXT NOT NULL,
	state             TEXT NOT NULL CHECK (state IN ('pending','passed','failed','tampered','prerequisite_missing','interrupted')),
	request_json      TEXT NOT NULL,
	result_json       TEXT NOT NULL DEFAULT '',
	acceptance_digest TEXT NOT NULL,
	created_at        TEXT NOT NULL,
	updated_at        TEXT NOT NULL
);
CREATE INDEX execution_verification_jobs_installation_idx
	ON execution_verification_jobs (installation_id, state);
CREATE INDEX execution_verification_jobs_attempt_idx
	ON execution_verification_jobs (attempt_id, state);
CREATE INDEX execution_verification_jobs_task_idx
	ON execution_verification_jobs (task_id, state);

CREATE TABLE execution_jobs (
	id                 TEXT PRIMARY KEY,
	version            INTEGER NOT NULL CHECK (version >= 1),
	kind               TEXT NOT NULL,
	state              TEXT NOT NULL CHECK (state IN ('pending','running','succeeded','failed','outcome_unknown','cancelled')),
	installation_id    TEXT NOT NULL,
	organization_id    TEXT NOT NULL DEFAULT '',
	project_id         TEXT NOT NULL DEFAULT '',
	scope_json         TEXT NOT NULL,
	owner              TEXT NOT NULL,
	operation          TEXT NOT NULL,
	input_json         TEXT NOT NULL,
	input_hash         TEXT NOT NULL,
	source_id          TEXT NOT NULL,
	claimed_generation INTEGER NOT NULL DEFAULT 0 CHECK (claimed_generation >= 0),
	requirements_json  TEXT NOT NULL DEFAULT '[]',
	result_json        TEXT NOT NULL DEFAULT '',
	result_artifact_json TEXT NOT NULL DEFAULT '',
	operation_id       TEXT NOT NULL DEFAULT '',
	completion_schema  TEXT NOT NULL DEFAULT '',
	created_at         TEXT NOT NULL,
	updated_at         TEXT NOT NULL
);
CREATE INDEX execution_jobs_installation_idx
	ON execution_jobs (installation_id, state);
CREATE UNIQUE INDEX execution_jobs_source_idx
	ON execution_jobs (source_id, input_hash);

CREATE TABLE execution_obligations (
	id              TEXT PRIMARY KEY,
	kind            TEXT NOT NULL CHECK (kind IN ('lease_conflict','unknown_effect','cost_unresolved')),
	installation_id TEXT NOT NULL,
	run_id          TEXT NOT NULL DEFAULT '',
	attempt_id      TEXT NOT NULL DEFAULT '',
	resource_id     TEXT NOT NULL DEFAULT '',
	message         TEXT NOT NULL,
	resolved_at     TEXT NOT NULL DEFAULT '',
	created_at      TEXT NOT NULL
);
CREATE INDEX execution_obligations_attempt_idx
	ON execution_obligations (attempt_id, resolved_at);
CREATE INDEX execution_obligations_run_idx
	ON execution_obligations (run_id, resolved_at);

CREATE TABLE execution_worker_gates (
	worker_id       TEXT PRIMARY KEY,
	installation_id TEXT NOT NULL,
	paused          INTEGER NOT NULL CHECK (paused IN (0,1)),
	version         INTEGER NOT NULL CHECK (version >= 1),
	paused_by       TEXT NOT NULL DEFAULT '',
	updated_at      TEXT NOT NULL
);
CREATE INDEX execution_worker_gates_installation_idx
	ON execution_worker_gates (installation_id);

CREATE TABLE execution_operations (
	id              TEXT PRIMARY KEY,
	attempt_id      TEXT NOT NULL,
	run_id          TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	kind            TEXT NOT NULL CHECK (kind IN ('model_step','tool_call','continuation','evaluation')),
	state           TEXT NOT NULL CHECK (state IN ('prepared','admitted','recorded','failed')),
	operation_ref   TEXT NOT NULL DEFAULT '',
	step_json       TEXT NOT NULL,
	created_at      TEXT NOT NULL
);
CREATE INDEX execution_operations_attempt_idx
	ON execution_operations (attempt_id, created_at);
`

// schemaV2 adds the revision-3 durable worker turn pipeline: the WorkerTurn
// store (execution_turns), its ProposalRecord store (execution_proposals),
// the versioned immutable ContextPlan store (execution_context_plans) and a
// narrow claim/generation tracker for sealed verification requests
// (execution_verification_claims), kept separate from the existing
// execution_verification_jobs table so its shape stays untouched.
//
// execution_turns carries the P00-001 unique admission key
// (installation_id, source_kind, source_id, source_version, worker_id) so
// _execution.turn.admit is idempotent by construction, and a second partial
// index enforces one live (non-terminal) decision stream per worker lane —
// a concurrent admission race can create at most one active turn per worker,
// exactly as the storage-level one-owner fence already does for attempts.
const schemaV2 = `
CREATE TABLE execution_turns (
	id                     TEXT PRIMARY KEY,
	version                INTEGER NOT NULL CHECK (version >= 1),
	worker_id              TEXT NOT NULL,
	principal_id           TEXT NOT NULL,
	installation_id        TEXT NOT NULL,
	organization_id        TEXT NOT NULL DEFAULT '',
	project_id             TEXT NOT NULL DEFAULT '',
	task_scope_id          TEXT NOT NULL DEFAULT '',
	scope_json             TEXT NOT NULL,
	source_kind            TEXT NOT NULL CHECK (source_kind IN ('message','task','responsibility','continuation')),
	source_id              TEXT NOT NULL,
	source_version         INTEGER NOT NULL CHECK (source_version >= 1),
	recipient_worker_id    TEXT NOT NULL DEFAULT '',
	requester_id           TEXT NOT NULL,
	configuration_revision INTEGER NOT NULL CHECK (configuration_revision >= 1),
	state                  TEXT NOT NULL CHECK (state IN ('pending','claimed','context_pending','model_pending','proposal_pending','waiting','reporting','completed','failed','cancelled')),
	generation             INTEGER NOT NULL CHECK (generation >= 1),
	limits_json            TEXT NOT NULL,
	root_id                TEXT NOT NULL,
	steps_used             INTEGER NOT NULL DEFAULT 0 CHECK (steps_used >= 0),
	created_at             TEXT NOT NULL,
	updated_at             TEXT NOT NULL,
	conversation_id        TEXT NOT NULL DEFAULT '',
	task_id                TEXT NOT NULL DEFAULT '',
	run_id                 TEXT NOT NULL DEFAULT '',
	attempt_id             TEXT NOT NULL DEFAULT '',
	waiting_reason         TEXT NOT NULL DEFAULT '' CHECK (waiting_reason IN ('','setup','clarification','review','effect','dependency','budget','recovery')),
	waiting_resource_id    TEXT NOT NULL DEFAULT '',
	next_wake              TEXT NOT NULL DEFAULT '',
	lease_id               TEXT NOT NULL DEFAULT '',
	lease_expires_at       TEXT NOT NULL DEFAULT '',
	context_artifact_json  TEXT NOT NULL DEFAULT '',
	last_observation_id    TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX execution_turns_source_idx
	ON execution_turns (installation_id, source_kind, source_id, source_version, worker_id);
CREATE UNIQUE INDEX execution_turns_active_worker_idx
	ON execution_turns (installation_id, worker_id) WHERE state NOT IN ('completed','failed','cancelled');
CREATE INDEX execution_turns_pending_idx
	ON execution_turns (installation_id, state, created_at);
CREATE INDEX execution_turns_task_idx
	ON execution_turns (task_id) WHERE task_id != '';
CREATE INDEX execution_turns_run_idx
	ON execution_turns (run_id) WHERE run_id != '';

CREATE TABLE execution_proposals (
	id                        TEXT PRIMARY KEY,
	installation_id           TEXT NOT NULL,
	turn_id                   TEXT NOT NULL,
	step_index                INTEGER NOT NULL CHECK (step_index >= 0),
	proposal_id               TEXT NOT NULL,
	source_context_digest     TEXT NOT NULL,
	normalized_proposal_json  TEXT NOT NULL DEFAULT '{}',
	state                     TEXT NOT NULL CHECK (state IN ('prepared','recorded','duplicate','stale','superseded')),
	created_at                TEXT NOT NULL,
	updated_at                TEXT NOT NULL,
	command_id                TEXT NOT NULL DEFAULT '',
	effect_operation_id       TEXT NOT NULL DEFAULT '',
	result_artifact_json      TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX execution_proposals_key_idx
	ON execution_proposals (turn_id, step_index, proposal_id);
CREATE INDEX execution_proposals_id_idx
	ON execution_proposals (installation_id, proposal_id, created_at);

CREATE TABLE execution_context_plans (
	id                     TEXT PRIMARY KEY,
	installation_id        TEXT NOT NULL,
	turn_id                TEXT NOT NULL,
	expected_version       INTEGER NOT NULL CHECK (expected_version >= 1),
	generation             INTEGER NOT NULL CHECK (generation >= 1),
	refs_json              TEXT NOT NULL DEFAULT '[]',
	configuration_revision INTEGER NOT NULL CHECK (configuration_revision >= 1),
	byte_bound             INTEGER NOT NULL DEFAULT 0 CHECK (byte_bound >= 0),
	token_bound            INTEGER NOT NULL DEFAULT 0 CHECK (token_bound >= 0),
	committed              INTEGER NOT NULL DEFAULT 0 CHECK (committed IN (0,1)),
	created_at             TEXT NOT NULL
);
CREATE INDEX execution_context_plans_turn_idx
	ON execution_context_plans (turn_id, created_at);

CREATE TABLE execution_verification_claims (
	request_id         TEXT PRIMARY KEY,
	claimed_generation INTEGER NOT NULL CHECK (claimed_generation >= 1),
	claim_token        TEXT NOT NULL,
	claimed_at         TEXT NOT NULL
);

-- _execution.verification.claim fences its claim by exact version+generation
-- (P00's frozen schema); the existing table carried no version column, so
-- this adds one additively, defaulting every already-inserted row to 1.
ALTER TABLE execution_verification_jobs ADD COLUMN version INTEGER NOT NULL DEFAULT 1;
`

// schemaV3 (P15) carries the plan's private assembly recipe -- the ordered,
// non-wire components (instructions, task, inbox, prior outputs/tool
// results) StageContext consumes to build the actual zatiti.context/v1
// document deterministically from exactly what prepare already pinned. It
// is never exposed through the frozen wire ContextPlan (whose schema is
// closed to `refs`/bounds only), so it lives in its own column rather than
// widening `refs_json`. schemaV3 also adds a turn-scoped context lineage
// record distinct from the existing attempt-scoped execution_context_lineage
// table: a WorkerTurn need not carry an attempt (a pure chat turn has none),
// so lineage for the turn pipeline is keyed by turn_id/plan_id instead of
// attempt_id/run_id.
const schemaV3 = `
ALTER TABLE execution_context_plans ADD COLUMN components_json TEXT NOT NULL DEFAULT '[]';

CREATE TABLE execution_turn_context_lineage (
	id              TEXT PRIMARY KEY,
	turn_id         TEXT NOT NULL,
	plan_id         TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	kind            TEXT NOT NULL CHECK (kind IN ('instruction','task','inbox','history','tool_result','memory')),
	artifact_id     TEXT NOT NULL DEFAULT '',
	artifact_digest TEXT NOT NULL DEFAULT '',
	created_at      TEXT NOT NULL
);
CREATE INDEX execution_turn_context_lineage_turn_idx
	ON execution_turn_context_lineage (turn_id, created_at);
`

// schemaV4 (P16) backs findTurnByAttemptID's lookup: _execution.observation
// now resolves the WorkerTurn bound to a hosted attempt by attempt_id on
// every delivery, a hot path with no existing index to serve it -- every
// other attempt_id column in this file (execution_checkpoints,
// execution_context_lineage, execution_verification_jobs,
// execution_obligations, execution_operations, all above) already carries
// its own dedicated index; execution_turns was the one exception.
const schemaV4 = `
CREATE INDEX execution_turns_attempt_idx
	ON execution_turns (attempt_id) WHERE attempt_id != '';
`

// schemaV5 (execution-dispatch-model-step, the same-day P22 gap fix) adds
// the missing chain from a committed context to an actual dispatched
// Responses effect. session_handle persists the confirmed OpenAI Responses
// provider session (AGENTS.md's "OpenAI Responses session preparation"
// prepare_session/model_step split) on the turn that owns it -- never
// reused across turns, never guessed. execution_turn_dispatches is a new,
// dedicated bookkeeping table (kept separate from the pre-turn hosted
// loop's own execution_operations table, whose kind CHECK constraint this
// migration does not touch) so a redelivered prepare_session observation
// always resolves its own recorded kind by exact operation_ref, even after
// the turn has moved on to dispatching model_step -- never misrouted into
// interpretTurnObservation's strict ModelOutput validation.
const schemaV5 = `
ALTER TABLE execution_turns ADD COLUMN session_handle TEXT NOT NULL DEFAULT '';

CREATE TABLE execution_turn_dispatches (
	id              TEXT PRIMARY KEY,
	turn_id         TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	kind            TEXT NOT NULL CHECK (kind IN ('prepare_session','model_step')),
	state           TEXT NOT NULL CHECK (state IN ('prepared','recorded','failed')) DEFAULT 'prepared',
	operation_ref   TEXT NOT NULL,
	created_at      TEXT NOT NULL
);
CREATE UNIQUE INDEX execution_turn_dispatches_ref_idx
	ON execution_turn_dispatches (turn_id, operation_ref);
CREATE INDEX execution_turn_dispatches_turn_idx
	ON execution_turn_dispatches (turn_id, created_at);
`

// migrations returns the execution-owned migration set. Bodies are pinned
// by SHA-256 so storage can detect any drift from the reviewed schema.
func migrations() []contract.Migration {
	return []contract.Migration{{
		Owner:   owner,
		Version: 1,
		SQL:     schemaV1,
		SHA256:  contract.Hash([]byte(schemaV1)),
	}, {
		Owner:   owner,
		Version: 2,
		SQL:     schemaV2,
		SHA256:  contract.Hash([]byte(schemaV2)),
	}, {
		Owner:   owner,
		Version: 3,
		SQL:     schemaV3,
		SHA256:  contract.Hash([]byte(schemaV3)),
	}, {
		Owner:   owner,
		Version: 4,
		SQL:     schemaV4,
		SHA256:  contract.Hash([]byte(schemaV4)),
	}, {
		Owner:   owner,
		Version: 5,
		SQL:     schemaV5,
		SHA256:  contract.Hash([]byte(schemaV5)),
	}}
}
