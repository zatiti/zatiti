package installation

import "github.com/zatiti/zatiti/internal/contract"

// owner is the migration owner and table namespace prefix.
const owner = "installation"

// schemaV1 creates the installation-owned tables, all carrying the
// installation_ prefix the storage namespace validator requires.
//
// installation_state holds at most one row: it exists only once bootstrap's
// Finish transaction has committed identity, configuration, memory and
// messaging together, so its presence alone answers "is this destination
// initialized" for a fresh bootstrap attempt whose own scope carries a
// freshly minted, otherwise meaningless installation_id.
//
// installation_bootstrap_intents records each init attempt's opaque,
// tokenless intent so a crash between the trusted helper (which custodies
// the owner secret outside any transaction) and the bootstrap transaction
// leaves a recoverable, cleanable trace rather than a silent half state.
//
// installation_jobs is this package's own local shadow of the durable jobs
// it creates through _execution.job.create for backup and restore, so
// installation.job.get answers from local state without raw SQL against
// execution's tables.
//
// installation_recovery_obligations stores the RecoveryObligation rows
// captured into a backup's paused/quiesced snapshot or a restore's recovery
// overlay (restore_job_id names either job), so they remain durably
// inspectable (and mergeable) independent of the manifest/overlay
// artifact's own encrypted bytes.
const schemaV1 = `
CREATE TABLE installation_state (
	id            TEXT PRIMARY KEY,
	version       INTEGER NOT NULL CHECK (version >= 1),
	paused        INTEGER NOT NULL CHECK (paused IN (0,1)),
	maintenance   INTEGER NOT NULL CHECK (maintenance IN (0,1)),
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL
);

CREATE TABLE installation_bootstrap_intents (
	id              TEXT PRIMARY KEY,
	installation_id TEXT NOT NULL,
	owner_id        TEXT NOT NULL,
	credential_id   TEXT NOT NULL,
	owner_name      TEXT NOT NULL,
	credential_store TEXT NOT NULL CHECK (credential_store IN ('os','headless')),
	headless_key_ref TEXT NOT NULL DEFAULT '',
	state           TEXT NOT NULL CHECK (state IN ('pending','completed','failed','abandoned')),
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);
CREATE INDEX installation_bootstrap_intents_state_idx
	ON installation_bootstrap_intents (state);

CREATE TABLE installation_jobs (
	id              TEXT PRIMARY KEY,
	version         INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	kind            TEXT NOT NULL CHECK (kind IN ('backup','restore')),
	state           TEXT NOT NULL CHECK (state IN ('pending','running','succeeded','failed','outcome_unknown','cancelled')),
	requirements_json TEXT NOT NULL DEFAULT '[]',
	result_json     TEXT NOT NULL DEFAULT '{}',
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);
CREATE INDEX installation_jobs_installation_idx
	ON installation_jobs (installation_id, kind);

CREATE TABLE installation_recovery_obligations (
	id                TEXT PRIMARY KEY,
	installation_id   TEXT NOT NULL,
	restore_job_id    TEXT NOT NULL,
	owner             TEXT NOT NULL,
	kind              TEXT NOT NULL,
	resource_id       TEXT NOT NULL,
	resource_version  INTEGER NOT NULL,
	record_artifact_json TEXT NOT NULL,
	record_digest     TEXT NOT NULL,
	state             TEXT NOT NULL,
	recorded_at       TEXT NOT NULL
);
CREATE INDEX installation_recovery_obligations_job_idx
	ON installation_recovery_obligations (restore_job_id);
`

// schemaV2 records, on the completed bootstrap intent, the opaque
// secret-store reference the owner credential is custodied under. The
// reference is not secret (identity keeps the same value); it lets local
// entrypoint assembly provision the operator's credential profile through
// Service.OwnerCredential. Intents of other states keep the empty default.
const schemaV2 = `
ALTER TABLE installation_bootstrap_intents ADD COLUMN store_ref TEXT NOT NULL DEFAULT '';
`

// schemaV3 records the opaque secret-store reference the installation's
// backup encryption key is custodied under. The store resolves keys only by
// the reference Put returned, never by name, so the reference is the only
// durable handle to the key; every bundle also carries it in its clear
// header, and this row lets successive backups reuse one key.
const schemaV3 = `
CREATE TABLE installation_backup_keys (
	installation_id TEXT PRIMARY KEY,
	store_ref       TEXT NOT NULL,
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);
`

// schemaV4 promotes the published recovery-overlay artifact reference from
// a free-form wireRequirement on the restore job to typed columns of the job
// row itself, so _installation.restore.overlay can answer the controller
// exactly and without parsing a message. installation.restore's Finish
// already recorded the same overlay under the recovery_overlay_published
// requirement (and still does, for job.get's inspectable disposition); a
// requirement is a human-readable prerequisite, not a machine-resolvable
// artifact reference -- it carries no digest and no size, and neither is
// recoverable from the id alone. Rows written before this migration keep
// the empty defaults and report not_found rather than a guess.
const schemaV4 = `
ALTER TABLE installation_jobs ADD COLUMN overlay_artifact_id TEXT NOT NULL DEFAULT '';
ALTER TABLE installation_jobs ADD COLUMN overlay_artifact_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE installation_jobs ADD COLUMN overlay_size INTEGER NOT NULL DEFAULT 0;
`

// migrations returns the installation-owned migration set. Each body is
// pinned by SHA-256 so storage can detect any drift from the reviewed
// schema.
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
	}}
}
