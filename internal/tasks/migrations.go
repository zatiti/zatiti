package tasks

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/zatiti/zatiti/internal/contract"
)

// ownerName is the domain owner identity used in descriptors and events.
const ownerName = "tasks"

// migrationV1 creates the tasks-owned tables. All objects live inside the
// tasks_ namespace: the task rows carry the pinned contract, the dependency
// edges carry the graph, evidence rows carry submitted output and verifier
// artifacts, manual decisions carry authorized manual acceptances and the
// transition log carries the durable state history.
const migrationV1 = `CREATE TABLE tasks_tasks (
	id TEXT PRIMARY KEY,
	version INTEGER NOT NULL,
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	scope_json TEXT NOT NULL,
	owner_id TEXT NOT NULL,
	worker_id TEXT NOT NULL,
	parent_id TEXT NOT NULL DEFAULT '',
	root_id TEXT NOT NULL DEFAULT '',
	outcome TEXT NOT NULL,
	inputs_json TEXT NOT NULL,
	required_outputs_json TEXT NOT NULL,
	acceptance_json TEXT NOT NULL,
	acceptance_digest TEXT NOT NULL,
	limits_json TEXT NOT NULL,
	dependencies_json TEXT NOT NULL,
	state TEXT NOT NULL,
	prior_state TEXT NOT NULL DEFAULT '',
	waiting_reason TEXT NOT NULL DEFAULT '',
	cancellation_requested INTEGER NOT NULL DEFAULT 0,
	manual_acceptance INTEGER NOT NULL DEFAULT 0,
	established_by TEXT NOT NULL DEFAULT '',
	attempt INTEGER NOT NULL DEFAULT 0,
	source_id TEXT NOT NULL DEFAULT '',
	occurrence_key TEXT NOT NULL DEFAULT '',
	content_digest TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX tasks_tasks_installation_state ON tasks_tasks (installation_id, state, updated_at);
CREATE INDEX tasks_tasks_org ON tasks_tasks (organization_id);
CREATE INDEX tasks_tasks_parent ON tasks_tasks (parent_id);
CREATE INDEX tasks_tasks_root ON tasks_tasks (root_id);
CREATE TABLE tasks_dependencies (
	task_id TEXT NOT NULL,
	depends_on_id TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (task_id, depends_on_id)
);
CREATE INDEX tasks_dependencies_reverse ON tasks_dependencies (depends_on_id, task_id);
CREATE TABLE tasks_evidence (
	task_id TEXT NOT NULL,
	artifact_id TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	digest TEXT NOT NULL,
	media_type TEXT NOT NULL,
	attempt INTEGER NOT NULL,
	recorded_at TEXT NOT NULL,
	PRIMARY KEY (task_id, artifact_id, attempt)
);
CREATE INDEX tasks_evidence_task ON tasks_evidence (task_id, attempt);
CREATE TABLE tasks_manual_decisions (
	id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	decision TEXT NOT NULL,
	reviewer_id TEXT NOT NULL,
	action_digest TEXT NOT NULL,
	reason TEXT NOT NULL,
	decided_at TEXT NOT NULL
);
CREATE INDEX tasks_manual_decisions_task ON tasks_manual_decisions (task_id);
CREATE TABLE tasks_transitions (
	id TEXT PRIMARY KEY,
	task_id TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	from_state TEXT NOT NULL,
	to_state TEXT NOT NULL,
	evidence_ids_json TEXT NOT NULL,
	waiting_reason TEXT NOT NULL,
	manual INTEGER NOT NULL,
	recorded_at TEXT NOT NULL
);
CREATE INDEX tasks_transitions_task ON tasks_transitions (task_id, recorded_at);`

// tasksMigrations returns the owned migration list.
func tasksMigrations() []contract.Migration {
	return []contract.Migration{{
		Owner:   ownerName,
		Version: 1,
		SQL:     migrationV1,
		SHA256:  contract.Digest(hashHex(migrationV1)),
	}}
}

// hashHex returns the lowercase SHA-256 hex digest of s.
func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
