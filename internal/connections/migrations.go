package connections

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/zatiti/zatiti/internal/contract"
)

// Connections-owned schema, private under the connections_ namespace.
// connections_contracts holds the built-in trusted adapter contracts shipped
// with the binary (no runtime tool registration exists; contract changes ship
// only with a new release). connections_connections holds connection
// definitions applied through the compiler, connections_challenges the typed
// credential setup challenges, connections_validations the recorded probe
// observations, connections_applied_plans the activation replay fence, and
// connections_pending_probes the callback ownership linking one outstanding
// connection.validate/connection.rotate job to the connection it probes, so
// _connections.validation.record can complete the execution-side job when
// the observation lands.
const migrationV1Head = `
CREATE TABLE connections_contracts (
    id          TEXT PRIMARY KEY,
    version     INTEGER NOT NULL CHECK (version >= 1),
    name        TEXT NOT NULL,
    input_schema TEXT NOT NULL,
    output_schema TEXT NOT NULL,
    effect      TEXT NOT NULL CHECK (effect IN ('local', 'disclosure', 'external_read', 'external_mutation')),
    destinations_json TEXT NOT NULL,
    credential_kind TEXT NOT NULL,
    cost_bound_json TEXT NOT NULL,
    timeout_seconds INTEGER NOT NULL CHECK (timeout_seconds >= 1),
    idempotency TEXT NOT NULL CHECK (idempotency IN ('none', 'qualified_key', 'authoritative_nonexecution')),
    key_retention_seconds INTEGER NOT NULL CHECK (key_retention_seconds >= 0),
    confirmation TEXT NOT NULL CHECK (confirmation IN ('synchronous', 'asynchronous', 'advisory')),
    reconciliation TEXT NOT NULL,
    adapter     TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE UNIQUE INDEX connections_contracts_name_idx ON connections_contracts (name);

CREATE TABLE connections_connections (
    id          TEXT PRIMARY KEY,
    version     INTEGER NOT NULL CHECK (version >= 1),
    installation_id TEXT NOT NULL,
    scope_json  TEXT NOT NULL,
    provider    TEXT NOT NULL,
    account_identity TEXT NOT NULL,
    credential_ref TEXT NOT NULL,
    destinations_json TEXT NOT NULL,
    allowed_scopes_json TEXT NOT NULL,
    validation_state TEXT NOT NULL CHECK (validation_state IN ('unverified', 'valid', 'invalid', 'expired', 'revoked')),
    lifecycle_state TEXT NOT NULL CHECK (lifecycle_state IN ('active', 'archived')),
    validated_at TEXT,
    valid_until TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE INDEX connections_connections_installation_idx
	ON connections_connections (installation_id);

CREATE TABLE connections_challenges (
    id          TEXT PRIMARY KEY,
    version     INTEGER NOT NULL CHECK (version >= 1),
    installation_id TEXT NOT NULL,
    connection_id TEXT NOT NULL REFERENCES connections_connections(id),
    connection_version INTEGER NOT NULL,
    method      TEXT NOT NULL CHECK (method IN ('browser', 'store_reference')),
    principal_id TEXT NOT NULL,
    account_identity TEXT NOT NULL,
    state       TEXT NOT NULL CHECK (state IN ('pending', 'external_action_required', 'completed', 'cancelled', 'expired', 'failed')),
    expires_at  TEXT NOT NULL,
    consent_url TEXT,
    helper_ref  TEXT,
    requirements_json TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE INDEX connections_challenges_connection_idx
	ON connections_challenges (connection_id, state);

CREATE TABLE connections_validations (
    id          TEXT PRIMARY KEY,
    installation_id TEXT NOT NULL,
    connection_id TEXT NOT NULL REFERENCES connections_connections(id),
    connection_version INTEGER NOT NULL,
    disposition TEXT NOT NULL CHECK (disposition IN ('succeeded', 'failed', 'accepted', 'unknown', 'not_sent')),
    observed_account TEXT NOT NULL DEFAULT '',
    observed_scopes_json TEXT NOT NULL,
    evidence_json TEXT NOT NULL,
    usage_json  TEXT,
    provider_reference TEXT,
    confirmed_at TEXT NOT NULL
);
CREATE INDEX connections_validations_connection_idx
	ON connections_validations (connection_id, confirmed_at);

CREATE TABLE connections_applied_plans (
    plan_id     TEXT PRIMARY KEY,
    candidate_digest TEXT NOT NULL,
    base_revision INTEGER NOT NULL CHECK (base_revision >= 1),
    versions_json TEXT NOT NULL,
    applied_at  TEXT NOT NULL
);

CREATE TABLE connections_pending_probes (
    connection_id TEXT PRIMARY KEY REFERENCES connections_connections(id),
    job_id        TEXT NOT NULL,
    job_version   INTEGER NOT NULL CHECK (job_version >= 1),
    kind          TEXT NOT NULL CHECK (kind IN ('validate', 'rotate')),
    created_at    TEXT NOT NULL
);
`

// migrationV1Tail seeds the built-in trusted adapter contracts: exact
// executable tool contracts for model calls, public HTTP reads, repository
// operations (split into a read-only and a mutating tool, since Tool.effect
// is a single classification) and supported memory reads, using the exact
// schemas frozen in docs/implementation/adapter-schemas.json (builtin_tools.go).
// These identities are separate from ExecutionProfile IDs: a Tool contract
// names an adapter action shape, never a hosted execution profile. Cost
// bounds and pinned public destinations are placeholders until integration
// qualification pins real prices and real destinations; they never claim
// upstream compatibility.
var migrationV1Tail = `
INSERT INTO connections_contracts
	(id, version, name, input_schema, output_schema, effect, destinations_json,
	 credential_kind, cost_bound_json, timeout_seconds, idempotency,
	 key_retention_seconds, confirmation, reconciliation, adapter, created_at, updated_at)
VALUES
	('0a000000-0000-4000-8000-0000000000c1', 1, '` + toolNameModelResponses + `',
	 '` + schemaModelResponsesIn + `',
	 '` + schemaModelResponsesOut + `',
	 'disclosure', '["api.openai.com"]', 'api_key',
	 '{"currency":"USD","micro_units":0}', 600, 'none', 0, 'advisory', 'none',
	 'zatiti/model-responses/v1', '1970-01-01T00:00:00Z', '1970-01-01T00:00:00Z'),
	('0a000000-0000-4000-8000-0000000000c2', 1, '` + toolNameGitHubRead + `',
	 '` + schemaGitHubReadIn + `',
	 '` + schemaGitHubEvidenceOut + `',
	 'external_read', '["api.github.com"]', 'token',
	 '{"currency":"USD","micro_units":0}', 60, 'authoritative_nonexecution', 0,
	 'synchronous', 'none', 'zatiti/provider-rest-read/v1',
	 '1970-01-01T00:00:00Z', '1970-01-01T00:00:00Z'),
	('0a000000-0000-4000-8000-0000000000c3', 1, '` + toolNameGitHubMutate + `',
	 '` + schemaGitHubMutateIn + `',
	 '` + schemaGitHubEvidenceOut + `',
	 'external_mutation', '["api.github.com"]', 'token',
	 '{"currency":"USD","micro_units":0}', 60, 'qualified_key', 86400,
	 'asynchronous', 'attempt-status', 'zatiti/provider-rest-mutate/v1',
	 '1970-01-01T00:00:00Z', '1970-01-01T00:00:00Z'),
	('0a000000-0000-4000-8000-0000000000c4', 1, '` + toolNameHTTPPublicRead + `',
	 '` + schemaHTTPPublicReadIn + `',
	 '` + schemaHTTPPublicReadOut + `',
	 'external_read', '["example.com"]', 'none',
	 '{"currency":"USD","micro_units":0}', 60, 'none', 0,
	 'synchronous', 'none', 'zatiti/public-http-read/v1',
	 '1970-01-01T00:00:00Z', '1970-01-01T00:00:00Z'),
	('0a000000-0000-4000-8000-0000000000c5', 1, '` + toolNameMemoryRead + `',
	 '` + schemaMemoryReadIn + `',
	 '` + schemaMemoryReadOut + `',
	 'external_read', '[]', 'token',
	 '{"currency":"USD","micro_units":0}', 60, 'none', 0,
	 'synchronous', 'none', 'zatiti/memory-read/v1',
	 '1970-01-01T00:00:00Z', '1970-01-01T00:00:00Z');
`

// migrationV1 is the complete owned migration body: schema plus seed data.
var migrationV1 = migrationV1Head + migrationV1Tail

// Migrations returns the owned migration set. Bodies are pinned by digest so
// storage refuses any later byte change.
func connectionsMigrations() []contract.Migration {
	return []contract.Migration{{
		Owner:   "connections",
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
