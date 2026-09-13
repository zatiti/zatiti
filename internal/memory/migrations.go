package memory

import (
	"github.com/zatiti/zatiti/internal/contract"
)

// migrations returns the owner-prefixed schema for the seven memory tables:
// brains, bindings, writer intents, governed jobs, cached claims, promotion
// lineage and reconciliation obligations.

// schemaV1 creates the memory tables. Timestamps are UTC RFC3339Nano text;
// JSON documents stay TEXT; absent scope dimensions are empty strings. The
// brain scope index enforces one brain per (installation, organization,
// worker, kind) triple even when the nullable dimensions are absent.
const schemaV1 = `
CREATE TABLE memory_brains (
	id TEXT PRIMARY KEY,
	version INTEGER NOT NULL,
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	worker_id TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL,
	classification TEXT NOT NULL,
	state TEXT NOT NULL,
	endpoint TEXT NOT NULL DEFAULT '',
	root_ref TEXT NOT NULL DEFAULT '',
	writer_owner TEXT NOT NULL DEFAULT '',
	allowed_destinations_json TEXT NOT NULL DEFAULT '[]',
	connection_ref_id TEXT NOT NULL DEFAULT '',
	connection_ref_version INTEGER NOT NULL DEFAULT 0,
	tool_ref_id TEXT NOT NULL DEFAULT '',
	tool_ref_version INTEGER NOT NULL DEFAULT 0,
	freshness_at TEXT,
	revision TEXT NOT NULL DEFAULT '',
	digest TEXT NOT NULL DEFAULT '',
	index_revision TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX memory_brains_scope
	ON memory_brains(installation_id, COALESCE(organization_id, ''), COALESCE(worker_id, ''), kind);

CREATE TABLE memory_bindings (
	id TEXT PRIMARY KEY,
	version INTEGER NOT NULL,
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	project_id TEXT NOT NULL DEFAULT '',
	worker_id TEXT NOT NULL DEFAULT '',
	task_id TEXT NOT NULL DEFAULT '',
	brain_id TEXT NOT NULL REFERENCES memory_brains(id),
	permissions_json TEXT NOT NULL,
	classification TEXT NOT NULL,
	state TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX memory_bindings_scope ON memory_bindings(installation_id, created_at, id);
CREATE INDEX memory_bindings_brain ON memory_bindings(brain_id);

CREATE TABLE memory_intents (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	brain_id TEXT NOT NULL REFERENCES memory_brains(id),
	adapter_command_id TEXT NOT NULL UNIQUE,
	operation_id TEXT,
	job_id TEXT,
	payload_digest TEXT NOT NULL,
	state TEXT NOT NULL,
	detail_json TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX memory_intents_brain ON memory_intents(brain_id, state);
CREATE INDEX memory_intents_operation ON memory_intents(operation_id);

CREATE TABLE memory_jobs (
	id TEXT PRIMARY KEY,
	version INTEGER NOT NULL,
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	project_id TEXT NOT NULL DEFAULT '',
	worker_id TEXT NOT NULL DEFAULT '',
	task_id TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL,
	state TEXT NOT NULL,
	operation TEXT NOT NULL,
	operation_ids_json TEXT NOT NULL,
	binding_ids_json TEXT NOT NULL,
	requirements_json TEXT NOT NULL,
	result_json TEXT,
	result_artifact_id TEXT,
	result_artifact_digest TEXT,
	execution_job_id TEXT NOT NULL DEFAULT '',
	execution_job_version INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX memory_jobs_page ON memory_jobs(installation_id, created_at, id);

CREATE TABLE memory_claims (
	brain_id TEXT NOT NULL,
	id TEXT NOT NULL,
	version INTEGER NOT NULL,
	text TEXT NOT NULL,
	sources_json TEXT NOT NULL,
	confidence INTEGER NOT NULL,
	freshness TEXT NOT NULL,
	active INTEGER NOT NULL,
	source_brain_id TEXT,
	source_claim_id TEXT,
	source_claim_version INTEGER,
	curator_id TEXT,
	redaction TEXT,
	recorded_at TEXT NOT NULL,
	PRIMARY KEY (brain_id, id, version)
);
CREATE INDEX memory_claims_source
	ON memory_claims(source_brain_id, source_claim_id, source_claim_version);

CREATE TABLE memory_promotions (
	id TEXT PRIMARY KEY,
	intent_id TEXT NOT NULL UNIQUE REFERENCES memory_intents(id),
	job_id TEXT NOT NULL,
	source_brain_id TEXT NOT NULL,
	source_claim_id TEXT NOT NULL,
	source_claim_version INTEGER NOT NULL,
	destination_binding_id TEXT NOT NULL,
	destination_brain_id TEXT NOT NULL DEFAULT '',
	destination_claim_id TEXT,
	destination_claim_version INTEGER,
	curator_id TEXT NOT NULL,
	redaction TEXT,
	state TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX memory_promotions_source
	ON memory_promotions(source_brain_id, source_claim_id, source_claim_version);

CREATE TABLE memory_reconciliation (
	id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	promotion_id TEXT,
	intent_id TEXT,
	source_brain_id TEXT,
	source_claim_id TEXT,
	source_claim_version INTEGER,
	destination_brain_id TEXT,
	reason TEXT NOT NULL,
	state TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX memory_reconciliation_pending ON memory_reconciliation(installation_id, state);
`

// migrations returns the single memory schema migration.
func migrations() []contract.Migration {
	return []contract.Migration{{
		Owner:   ownerName,
		Version: 1,
		SQL:     schemaV1,
		SHA256:  contract.Hash([]byte(schemaV1)),
	}}
}
