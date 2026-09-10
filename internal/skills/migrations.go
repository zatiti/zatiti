package skills

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/zatiti/zatiti/internal/contract"
)

// Skills-owned schema, private under the skills_ namespace. skills_versions
// holds one immutable row per skill version (state draft | active |
// archived); skills_dependencies normalizes each version's resolved
// dependency pins for invalidation lookups; skills_evaluations holds the
// sealed evaluation records whose actual observations stay separate from the
// self-authored acceptance until recorded downstream.
const migrationV1 = `
CREATE TABLE skills_versions (
	id          TEXT NOT NULL,
	version     INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	name        TEXT NOT NULL,
	description TEXT NOT NULL,
	state       TEXT NOT NULL CHECK (state IN ('draft', 'active', 'archived')),
	content_digest TEXT NOT NULL,
	instruction_digest TEXT NOT NULL,
	instruction_artifact_id TEXT NOT NULL DEFAULT '',
	manifest_json TEXT NOT NULL,
	requirements_json TEXT NOT NULL,
	dependencies_json TEXT NOT NULL,
	input_schema_json TEXT NOT NULL,
	output_schema_json TEXT NOT NULL,
	source      TEXT NOT NULL,
	license     TEXT NOT NULL,
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL,
	PRIMARY KEY (id, version)
);
CREATE UNIQUE INDEX skills_versions_name_idx
	ON skills_versions (installation_id, name, version);
CREATE INDEX skills_versions_state_idx
	ON skills_versions (installation_id, state, name, version);

CREATE TABLE skills_dependencies (
	installation_id TEXT NOT NULL,
	skill_id    TEXT NOT NULL,
	skill_version INTEGER NOT NULL,
	dep_name    TEXT NOT NULL,
	dep_id      TEXT NOT NULL,
	dep_version INTEGER NOT NULL,
	PRIMARY KEY (skill_id, skill_version, dep_name),
	FOREIGN KEY (skill_id, skill_version) REFERENCES skills_versions (id, version)
);
CREATE INDEX skills_dependencies_dep_idx
	ON skills_dependencies (installation_id, dep_id, dep_version);

CREATE TABLE skills_evaluations (
	id          TEXT PRIMARY KEY,
	version     INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	skill_id    TEXT NOT NULL,
	skill_version INTEGER NOT NULL,
	state       TEXT NOT NULL CHECK (state IN ('pending', 'running', 'succeeded', 'failed', 'outcome_unknown', 'cancelled')),
	verifier_id TEXT NOT NULL,
	verifier_version TEXT NOT NULL,
	mode        TEXT NOT NULL CHECK (mode IN ('independent', 'manual')),
	acceptance_json TEXT NOT NULL,
	profile_json TEXT NOT NULL,
	limits_json TEXT NOT NULL,
	observations_json TEXT,
	evidence_json TEXT,
	job_id      TEXT,
	job_json    TEXT,
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL,
	FOREIGN KEY (skill_id, skill_version) REFERENCES skills_versions (id, version)
);
CREATE INDEX skills_evaluations_skill_idx
	ON skills_evaluations (installation_id, skill_id, skill_version);
`

// Migrations returns the owned migration set. Bodies are pinned by digest so
// storage refuses any later byte change.
func skillsMigrations() []contract.Migration {
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
