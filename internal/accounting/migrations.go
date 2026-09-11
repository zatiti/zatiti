package accounting

import "github.com/zatiti/zatiti/internal/contract"

// owner is the migration owner and table namespace prefix.
const owner = "accounting"

// schemaV1 creates the accounting-owned tables: budget definitions bound by
// identity to their installation/organization/project/worker, per-dimension
// aggregate positions (the shared counters whose caps root and ancestor
// budgets enforce across all children), reservations with their replay
// fingerprints, and the append-only exact ledger entries.
//
// Scope dimensions are stored both as explicit columns (for filtered reads
// and containment checks) and as the canonical scope JSON snapshot (for
// exact wire output). Timestamps are UTC RFC3339Nano strings. Position
// currency is pinned by the first reservation that creates the row; later
// currency mismatches fail closed.
const schemaV1 = `
CREATE TABLE accounting_budgets (
	id              TEXT PRIMARY KEY,
	version         INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	limits_json     TEXT NOT NULL,
	state           TEXT NOT NULL CHECK (state IN ('active','archived')),
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);
CREATE INDEX accounting_budgets_installation_idx
	ON accounting_budgets (installation_id, state);

CREATE TABLE accounting_positions (
	kind            TEXT NOT NULL CHECK (kind IN ('installation','organization','project','worker','root_task')),
	ref_id          TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	currency        TEXT NOT NULL,
	spent           INTEGER NOT NULL DEFAULT 0 CHECK (spent >= 0),
	reserved        INTEGER NOT NULL DEFAULT 0 CHECK (reserved >= 0),
	unknown         INTEGER NOT NULL DEFAULT 0 CHECK (unknown >= 0),
	estimated       INTEGER NOT NULL DEFAULT 0 CHECK (estimated >= 0),
	concurrency     INTEGER NOT NULL DEFAULT 0 CHECK (concurrency >= 0),
	version         INTEGER NOT NULL CHECK (version >= 1),
	updated_at      TEXT NOT NULL,
	PRIMARY KEY (kind, ref_id)
);
CREATE INDEX accounting_positions_installation_idx
	ON accounting_positions (installation_id);

CREATE TABLE accounting_reservations (
	id              TEXT PRIMARY KEY,
	version         INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	project_id      TEXT NOT NULL DEFAULT '',
	worker_id       TEXT NOT NULL DEFAULT '',
	root_task_id    TEXT NOT NULL DEFAULT '',
	scope_json      TEXT NOT NULL,
	operation_id    TEXT NOT NULL UNIQUE,
	currency        TEXT NOT NULL,
	amount          INTEGER NOT NULL CHECK (amount >= 0),
	concurrency_slots INTEGER NOT NULL CHECK (concurrency_slots IN (0,1)),
	limits_json     TEXT NOT NULL,
	request_json    TEXT NOT NULL,
	positions_json  TEXT NOT NULL,
	state           TEXT NOT NULL CHECK (state IN ('reserved','settled','unknown','released')),
	settle_usage_json TEXT NOT NULL DEFAULT '',
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);
CREATE INDEX accounting_reservations_installation_idx
	ON accounting_reservations (installation_id, state);
CREATE INDEX accounting_reservations_root_idx
	ON accounting_reservations (root_task_id, state);

CREATE TABLE accounting_entries (
	id              TEXT PRIMARY KEY,
	reservation_id  TEXT NOT NULL,
	installation_id TEXT NOT NULL,
	kind            TEXT NOT NULL CHECK (kind IN ('reserved','spent','estimated','unknown','released')),
	currency        TEXT NOT NULL,
	amount          INTEGER NOT NULL CHECK (amount >= 0),
	advisory        INTEGER NOT NULL CHECK (advisory IN (0,1)),
	note            TEXT NOT NULL DEFAULT '',
	created_at      TEXT NOT NULL
);
CREATE INDEX accounting_entries_reservation_idx
	ON accounting_entries (reservation_id);
`

// migrations returns the accounting-owned migration set. Bodies are pinned
// by SHA-256 so storage can detect any drift from the reviewed schema.
func migrations() []contract.Migration {
	return []contract.Migration{{
		Owner:   owner,
		Version: 1,
		SQL:     schemaV1,
		SHA256:  contract.Hash([]byte(schemaV1)),
	}}
}
