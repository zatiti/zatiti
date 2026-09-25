package effects

import (
	"github.com/zatiti/zatiti/internal/contract"
)

// migrations returns the owner-prefixed schema for the six effects tables:
// immutable actions, logical operations, physical attempts, one-use claims,
// append-only observations and open obligations.

// schemaV1 creates the effects tables. Actions and observations are append
// only; operations and attempts carry versions and states; claims are
// consumed exactly once.
const schemaV1 = `
CREATE TABLE effects_actions (
	id TEXT PRIMARY KEY,
	version INTEGER NOT NULL,
	installation_id TEXT NOT NULL,
	digest TEXT NOT NULL UNIQUE,
	action_json TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE effects_operations (
	id TEXT PRIMARY KEY,
	version INTEGER NOT NULL,
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	project_id TEXT NOT NULL DEFAULT '',
	worker_id TEXT NOT NULL DEFAULT '',
	task_id TEXT NOT NULL DEFAULT '',
	action_id TEXT NOT NULL REFERENCES effects_actions(id),
	action_digest TEXT NOT NULL,
	source_key TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL,
	linked_operation_id TEXT,
	relationship TEXT,
	job_id TEXT,
	attempt_count INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX effects_operations_page ON effects_operations(installation_id, created_at, id);
CREATE INDEX effects_operations_state ON effects_operations(installation_id, state);
CREATE INDEX effects_operations_linked ON effects_operations(linked_operation_id);
CREATE UNIQUE INDEX effects_operations_source
	ON effects_operations(installation_id, source_key) WHERE source_key <> '';

CREATE TABLE effects_attempts (
	id TEXT PRIMARY KEY,
	version INTEGER NOT NULL,
	operation_id TEXT NOT NULL REFERENCES effects_operations(id),
	attempt_no INTEGER NOT NULL,
	state TEXT NOT NULL,
	disposition TEXT,
	adapter TEXT NOT NULL,
	credential_ref TEXT NOT NULL,
	deadline TEXT NOT NULL,
	dispatch_json TEXT NOT NULL,
	reservation_id TEXT,
	reservation_version INTEGER,
	connection_id TEXT NOT NULL,
	connection_version INTEGER NOT NULL,
	evidence_json TEXT,
	usage_json TEXT,
	provider_reference TEXT,
	confirmed_at TEXT,
	generation INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	recorded_at TEXT
);
CREATE UNIQUE INDEX effects_attempts_order ON effects_attempts(operation_id, attempt_no);

CREATE TABLE effects_claims (
	attempt_id TEXT PRIMARY KEY REFERENCES effects_attempts(id),
	generation INTEGER NOT NULL,
	expires_at TEXT NOT NULL,
	consumed INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);

CREATE TABLE effects_observations (
	id TEXT PRIMARY KEY,
	operation_id TEXT NOT NULL REFERENCES effects_operations(id),
	attempt_id TEXT REFERENCES effects_attempts(id),
	kind TEXT NOT NULL,
	disposition TEXT NOT NULL,
	evidence_json TEXT,
	usage_json TEXT,
	provider_reference TEXT,
	confirmed_at TEXT,
	recorded_at TEXT NOT NULL
);

CREATE TABLE effects_obligations (
	id TEXT PRIMARY KEY,
	operation_id TEXT NOT NULL REFERENCES effects_operations(id),
	kind TEXT NOT NULL,
	state TEXT NOT NULL,
	detail_json TEXT,
	created_at TEXT NOT NULL,
	resolved_at TEXT
);
`

// schemaV2 carries revision 3: the persisted callback route alongside the
// immutable action (P00-006), and the attempt kind/target-attempt linkage
// that separates a reconciliation bounded read from the original dispatch
// (P00-007). kind defaults dispatch so every attempt written before this
// migration reads back as an ordinary physical invocation.
const schemaV2 = `
ALTER TABLE effects_operations ADD COLUMN callback_route_json TEXT NOT NULL DEFAULT '';
ALTER TABLE effects_attempts ADD COLUMN kind TEXT NOT NULL DEFAULT 'dispatch';
ALTER TABLE effects_attempts ADD COLUMN target_attempt_id TEXT NOT NULL DEFAULT '';
`

// migrations returns the effects schema migrations. Bodies are pinned by
// digest so storage refuses any later byte change.
func migrations() []contract.Migration {
	return []contract.Migration{{
		Owner:   ownerName,
		Version: 1,
		SQL:     schemaV1,
		SHA256:  contract.Hash([]byte(schemaV1)),
	}, {
		Owner:   ownerName,
		Version: 2,
		SQL:     schemaV2,
		SHA256:  contract.Hash([]byte(schemaV2)),
	}, {
		Owner:   ownerName,
		Version: 3,
		SQL:     schemaV3,
		SHA256:  contract.Hash([]byte(schemaV3)),
	}}
}

// schemaV3 pins the exact secret-free adapter configuration resolved for a
// hosted execution profile at operation preparation. Historical operations
// never follow a later worker selection.
const schemaV3 = `ALTER TABLE effects_operations ADD COLUMN adapter_profile_json TEXT NOT NULL DEFAULT '';
ALTER TABLE effects_operations ADD COLUMN profile_connection_version INTEGER NOT NULL DEFAULT 0;`
