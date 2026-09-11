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

// migrations returns the execution-owned migration set. Bodies are pinned
// by SHA-256 so storage can detect any drift from the reviewed schema.
func migrations() []contract.Migration {
	return []contract.Migration{{
		Owner:   owner,
		Version: 1,
		SQL:     schemaV1,
		SHA256:  contract.Hash([]byte(schemaV1)),
	}}
}
