package configuration

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/zatiti/zatiti/internal/contract"
)

// Configuration-owned schema, private under the configuration_ namespace.
// Effective entities (organizations, teams, projects, workers, bindings,
// execution profiles) carry versioned definitions; drafts, plans and
// revisions hold the immutable compiler lineage; configuration_head is the
// singleton apply fence.
const migrationV1 = `
CREATE TABLE configuration_organizations (
	id          TEXT PRIMARY KEY,
	version     INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	key         TEXT NOT NULL,
	name        TEXT NOT NULL,
	chief_id    TEXT NOT NULL,
	parent_id   TEXT REFERENCES configuration_organizations(id),
	limits_json TEXT,
	extensions_json TEXT,
	state       TEXT NOT NULL CHECK (state IN ('active', 'archiving', 'archived')),
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);
CREATE UNIQUE INDEX configuration_organizations_key_idx
	ON configuration_organizations (installation_id, key);

CREATE TABLE configuration_teams (
	id          TEXT PRIMARY KEY,
	version     INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL REFERENCES configuration_organizations(id),
	key         TEXT NOT NULL,
	name        TEXT NOT NULL,
	worker_ids_json TEXT NOT NULL,
	extensions_json TEXT,
	state       TEXT NOT NULL CHECK (state IN ('active', 'archiving', 'archived')),
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);
CREATE UNIQUE INDEX configuration_teams_key_idx
	ON configuration_teams (installation_id, organization_id, key);

CREATE TABLE configuration_projects (
	id          TEXT PRIMARY KEY,
	version     INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL REFERENCES configuration_organizations(id),
	key         TEXT NOT NULL,
	name        TEXT NOT NULL,
	repositories_json TEXT NOT NULL,
	bindings_json TEXT NOT NULL,
	classification TEXT NOT NULL CHECK (classification IN ('internal', 'public', 'restricted')),
	limits_json TEXT,
	extensions_json TEXT,
	state       TEXT NOT NULL CHECK (state IN ('active', 'archiving', 'archived')),
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);
CREATE UNIQUE INDEX configuration_projects_key_idx
	ON configuration_projects (installation_id, organization_id, key);

CREATE TABLE configuration_workers (
	id          TEXT PRIMARY KEY,
	version     INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL REFERENCES configuration_organizations(id),
	key         TEXT NOT NULL,
	name        TEXT NOT NULL,
	purpose     TEXT NOT NULL,
	instructions TEXT NOT NULL,
	skill_versions_json TEXT NOT NULL,
	bindings_json TEXT NOT NULL,
	profile_json TEXT,
	limits_json TEXT,
	extensions_json TEXT,
	state       TEXT NOT NULL CHECK (state IN ('active', 'archiving', 'archived')),
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);
CREATE UNIQUE INDEX configuration_workers_key_idx
	ON configuration_workers (installation_id, organization_id, key);

CREATE TABLE configuration_bindings (
	id          TEXT PRIMARY KEY,
	version     INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	scope_json  TEXT NOT NULL,
	kind        TEXT NOT NULL CHECK (kind IN ('tool', 'skill', 'connection', 'worker', 'repository', 'reporting', 'memory')),
	target_id   TEXT NOT NULL,
	permissions_json TEXT NOT NULL,
	source_scope_json TEXT,
	destinations_json TEXT,
	state       TEXT NOT NULL CHECK (state IN ('active', 'archiving', 'archived')),
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);

CREATE TABLE configuration_execution_profiles (
	id          TEXT PRIMARY KEY,
	version     INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	executor    TEXT NOT NULL CHECK (executor IN ('hosted', 'cooperative')),
	model       TEXT NOT NULL,
	connection_id TEXT NOT NULL,
	provider_destination TEXT NOT NULL,
	capabilities_json TEXT NOT NULL,
	cost_bound_json TEXT NOT NULL,
	classification TEXT NOT NULL CHECK (classification IN ('internal', 'public', 'restricted')),
	context_capture TEXT NOT NULL CHECK (context_capture IN ('complete', 'partial', 'advisory')),
	state       TEXT NOT NULL CHECK (state IN ('active', 'archiving', 'archived')),
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);

CREATE TABLE configuration_drafts (
	id          TEXT PRIMARY KEY,
	version     INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	base_revision INTEGER NOT NULL CHECK (base_revision >= 1),
	changes_json TEXT NOT NULL,
	diagnostics_json TEXT NOT NULL,
	state       TEXT NOT NULL CHECK (state IN ('open', 'discarded')),
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);

CREATE TABLE configuration_plans (
	id          TEXT PRIMARY KEY,
	version     INTEGER NOT NULL CHECK (version >= 1),
	draft_id    TEXT NOT NULL REFERENCES configuration_drafts(id),
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	base_revision INTEGER NOT NULL CHECK (base_revision >= 1),
	candidate_digest TEXT NOT NULL,
	changes_json TEXT NOT NULL,
	dependencies_json TEXT NOT NULL,
	compiler_version TEXT NOT NULL,
	schema_version TEXT NOT NULL,
	authority_requirements_json TEXT NOT NULL,
	decisions_json TEXT NOT NULL,
	diagnostics_json TEXT NOT NULL,
	requirements_json TEXT NOT NULL,
	state       TEXT NOT NULL CHECK (state IN ('sealed', 'applied')),
	created_at  TEXT NOT NULL,
	updated_at  TEXT NOT NULL
);
CREATE INDEX configuration_plans_draft_idx ON configuration_plans (draft_id);

CREATE TABLE configuration_revisions (
	id          TEXT PRIMARY KEY,
	version     INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	plan_id     TEXT NOT NULL REFERENCES configuration_plans(id),
	candidate_digest TEXT NOT NULL,
	activated_at TEXT NOT NULL
);

CREATE TABLE configuration_revision_objects (
	revision_id TEXT NOT NULL REFERENCES configuration_revisions(id),
	kind        TEXT NOT NULL,
	object_id   TEXT NOT NULL,
	action      TEXT NOT NULL,
	before_json TEXT,
	after_json  TEXT,
	PRIMARY KEY (revision_id, kind, object_id)
);

CREATE TABLE configuration_head (
	id        INTEGER PRIMARY KEY CHECK (id = 1),
	revision  INTEGER NOT NULL CHECK (revision >= 0)
);
INSERT INTO configuration_head (id, revision) VALUES (1, 0);
`

// Migrations returns the owned migration set. Bodies are pinned by digest so
// storage refuses any later byte change.
func configurationMigrations() []contract.Migration {
	return []contract.Migration{{
		Owner:   "configuration",
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
