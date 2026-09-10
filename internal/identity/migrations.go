package identity

import "github.com/zatiti/zatiti/internal/contract"

// owner is the migration owner and table namespace prefix.
const owner = "identity"

// schemaV1 creates the identity-owned tables. All object names carry the
// identity_ prefix required by the storage namespace validator.
//
// Scope dimensions are stored both as explicit columns (for filtered reads
// and scope-containment checks) and as the canonical scope JSON snapshot
// (for exact wire output). Timestamps are UTC RFC3339Nano strings. JSON
// arrays (capabilities, destinations) persist as canonical JSON text.
//
// token_digest is the SHA-256 hex digest of the credential bytes held in
// platform custody under store_ref; raw bytes never persist here.
const schemaV1 = `
CREATE TABLE identity_principals (
	id             TEXT PRIMARY KEY,
	version        INTEGER NOT NULL CHECK (version >= 1),
	kind           TEXT NOT NULL CHECK (kind IN ('human','client_agent','worker','service')),
	name           TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	project_id      TEXT NOT NULL DEFAULT '',
	worker_id       TEXT NOT NULL DEFAULT '',
	task_id         TEXT NOT NULL DEFAULT '',
	scope_json      TEXT NOT NULL,
	revoked        INTEGER NOT NULL CHECK (revoked IN (0,1)),
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);
CREATE INDEX identity_principals_scope_idx
	ON identity_principals (installation_id, organization_id, project_id, kind);

CREATE TABLE identity_grants (
	id              TEXT PRIMARY KEY,
	version         INTEGER NOT NULL CHECK (version >= 1),
	principal_id    TEXT NOT NULL REFERENCES identity_principals(id),
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	project_id      TEXT NOT NULL DEFAULT '',
	worker_id       TEXT NOT NULL DEFAULT '',
	task_id         TEXT NOT NULL DEFAULT '',
	scope_json         TEXT NOT NULL,
	capabilities_json  TEXT NOT NULL,
	destinations_json  TEXT NOT NULL,
	denied          INTEGER NOT NULL CHECK (denied IN (0,1)),
	expires_at      TEXT,
	parent_grant_id TEXT,
	revoked         INTEGER NOT NULL CHECK (revoked IN (0,1)),
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);
CREATE INDEX identity_grants_principal_idx
	ON identity_grants (principal_id, installation_id, revoked);
CREATE INDEX identity_grants_parent_idx ON identity_grants (parent_grant_id);

CREATE TABLE identity_credentials (
	id              TEXT PRIMARY KEY,
	version         INTEGER NOT NULL CHECK (version >= 1),
	principal_id    TEXT NOT NULL REFERENCES identity_principals(id),
	installation_id TEXT NOT NULL,
	store_ref       TEXT NOT NULL,
	token_digest    TEXT NOT NULL UNIQUE,
	expires_at      TEXT,
	revoked         INTEGER NOT NULL CHECK (revoked IN (0,1)),
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);
CREATE INDEX identity_credentials_principal_idx
	ON identity_credentials (principal_id, installation_id);

CREATE TABLE identity_revocations (
	id          TEXT PRIMARY KEY,
	entity_kind TEXT NOT NULL CHECK (entity_kind IN ('principal','grant','credential')),
	entity_id   TEXT NOT NULL,
	reason      TEXT NOT NULL DEFAULT '',
	created_at  TEXT NOT NULL
);
CREATE INDEX identity_revocations_entity_idx ON identity_revocations (entity_kind, entity_id);

CREATE TABLE identity_restrictions (
	id              TEXT PRIMARY KEY,
	version         INTEGER NOT NULL CHECK (version >= 1),
	principal_id    TEXT NOT NULL REFERENCES identity_principals(id),
	installation_id TEXT NOT NULL,
	capability      TEXT NOT NULL,
	reason          TEXT NOT NULL DEFAULT '',
	active          INTEGER NOT NULL CHECK (active IN (0,1)),
	created_at      TEXT NOT NULL
);
CREATE INDEX identity_restrictions_principal_idx
	ON identity_restrictions (principal_id, capability, active);

CREATE TABLE identity_promotions (
	qualification_id      TEXT NOT NULL,
	qualification_version INTEGER NOT NULL CHECK (qualification_version >= 1),
	grant_id      TEXT NOT NULL REFERENCES identity_grants(id),
	ceiling_id    TEXT NOT NULL,
	rule_id       TEXT NOT NULL,
	rule_version  INTEGER NOT NULL CHECK (rule_version >= 1),
	created_at    TEXT NOT NULL,
	PRIMARY KEY (qualification_id, qualification_version)
);
`

// migrations returns the identity-owned migration set. Bodies are pinned by
// SHA-256 so storage can detect any drift from the reviewed schema.
func migrations() []contract.Migration {
	return []contract.Migration{{
		Owner:   owner,
		Version: 1,
		SQL:     schemaV1,
		SHA256:  contract.Hash([]byte(schemaV1)),
	}}
}
