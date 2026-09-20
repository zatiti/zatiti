package artifacts

import "github.com/zatiti/zatiti/internal/contract"

// owner is the migration owner and table namespace prefix.
const owner = "artifacts"

// schemaV1 creates the artifacts-owned tables: immutable content metadata,
// resumable bounded uploads, their received chunks, retention pins that
// keep referenced content out of a future staging sweep, and durable
// integrity faults observed against committed metadata.
//
// Scope dimensions are stored both as explicit columns (for filtered reads
// and containment checks) and as the canonical scope JSON snapshot (for
// exact wire output). Timestamps are UTC RFC3339Nano strings.
const schemaV1 = `
CREATE TABLE artifacts_metadata (
	id              TEXT PRIMARY KEY,
	version         INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	project_id      TEXT NOT NULL DEFAULT '',
	worker_id       TEXT NOT NULL DEFAULT '',
	task_id         TEXT NOT NULL DEFAULT '',
	scope_json      TEXT NOT NULL,
	digest          TEXT NOT NULL,
	size            INTEGER NOT NULL CHECK (size >= 0),
	media_type      TEXT NOT NULL,
	classification  TEXT NOT NULL CHECK (classification IN ('internal','public','restricted')),
	encrypted       INTEGER NOT NULL CHECK (encrypted IN (0,1)),
	state           TEXT NOT NULL CHECK (state IN ('available','fault')),
	created_at      TEXT NOT NULL
);
CREATE INDEX artifacts_metadata_installation_idx
	ON artifacts_metadata (installation_id, state);
CREATE INDEX artifacts_metadata_digest_idx
	ON artifacts_metadata (digest);

CREATE TABLE artifacts_uploads (
	id              TEXT PRIMARY KEY,
	version         INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	project_id      TEXT NOT NULL DEFAULT '',
	worker_id       TEXT NOT NULL DEFAULT '',
	task_id         TEXT NOT NULL DEFAULT '',
	scope_json      TEXT NOT NULL,
	expected_size   INTEGER NOT NULL CHECK (expected_size >= 0),
	expected_digest TEXT NOT NULL,
	received_size   INTEGER NOT NULL DEFAULT 0 CHECK (received_size >= 0),
	media_type      TEXT NOT NULL,
	classification  TEXT NOT NULL CHECK (classification IN ('internal','public','restricted')),
	state           TEXT NOT NULL CHECK (state IN ('open','finished','cancelled','expired')),
	artifact_id     TEXT NOT NULL DEFAULT '',
	expires_at      TEXT NOT NULL,
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);
CREATE INDEX artifacts_uploads_installation_idx
	ON artifacts_uploads (installation_id, state);

CREATE TABLE artifacts_chunks (
	upload_id  TEXT NOT NULL,
	offset_bytes INTEGER NOT NULL CHECK (offset_bytes >= 0),
	length     INTEGER NOT NULL CHECK (length >= 1),
	digest     TEXT NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (upload_id, offset_bytes)
);

CREATE TABLE artifacts_pins (
	id          TEXT PRIMARY KEY,
	artifact_id TEXT NOT NULL,
	reason      TEXT NOT NULL,
	created_at  TEXT NOT NULL
);
CREATE INDEX artifacts_pins_artifact_idx
	ON artifacts_pins (artifact_id);

CREATE TABLE artifacts_faults (
	id          TEXT PRIMARY KEY,
	artifact_id TEXT NOT NULL,
	code        TEXT NOT NULL,
	message     TEXT NOT NULL,
	created_at  TEXT NOT NULL
);
CREATE INDEX artifacts_faults_artifact_idx
	ON artifacts_faults (artifact_id);
`

// schemaV2 adds the revision-3 provenance columns (AGENTS.md: "Artifact
// gains optional source_operation_id/purpose"). Both are additive, default
// to the empty string, and existing rows validate unchanged with the new
// fields simply absent from their wire projection (dto.go's omitempty).
const schemaV2 = `
ALTER TABLE artifacts_metadata ADD COLUMN source_operation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE artifacts_metadata ADD COLUMN purpose TEXT NOT NULL DEFAULT '';
`

// migrations returns the artifacts-owned migration set. Bodies are pinned
// by SHA-256 so storage can detect any drift from the reviewed schema.
func migrations() []contract.Migration {
	return []contract.Migration{
		{Owner: owner, Version: 1, SQL: schemaV1, SHA256: contract.Hash([]byte(schemaV1))},
		{Owner: owner, Version: 2, SQL: schemaV2, SHA256: contract.Hash([]byte(schemaV2))},
	}
}
