package reviews

import "github.com/zatiti/zatiti/internal/contract"

// owner is the migration owner and table namespace prefix.
const owner = "reviews"

// schemaV1 creates the reviews-owned tables. All object names carry the
// reviews_ prefix required by the storage namespace validator.
//
// reviews_requests holds one row per review request. The preview column
// stores the canonical JSON of the exact immutable Action under review and
// requirement_json the canonical DecisionRequirement; the action digest is
// the SHA-256 of the canonical preview, binding account, destination,
// content hashes, timing, preconditions, configuration revision and tool
// versions. Several rows may exist for one digest over history (a request
// that expired is superseded by a fresh one), so uniqueness is enforced by
// the ensure flow, not by a constraint.
//
// reviews_decisions is append-only: a recorded decision is never updated or
// deleted. reviews_delegations is append-only for the same reason; the
// unique index lets the delegate operation treat a repeat delegation as a
// no-op. reviews_reviewers snapshots the eligible-principal set of each
// review as rows so the needs_you list filter stays a pure SQL predicate
// ("apply scope and filter before pagination").
//
// Scope dimensions are stored both as explicit columns (for filtered reads)
// and as the canonical scope JSON snapshot (for exact wire output).
// Timestamps are UTC RFC3339Nano strings.
const schemaV1 = `
CREATE TABLE reviews_requests (
	id              TEXT PRIMARY KEY,
	version         INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	project_id      TEXT NOT NULL DEFAULT '',
	worker_id       TEXT NOT NULL DEFAULT '',
	task_id         TEXT NOT NULL DEFAULT '',
	scope_json         TEXT NOT NULL,
	action_digest      TEXT NOT NULL CHECK (length(action_digest) = 64),
	preview_json       TEXT NOT NULL,
	requirement_json   TEXT NOT NULL,
	proposer_id     TEXT NOT NULL,
	state           TEXT NOT NULL CHECK (state IN ('pending','approved','rejected','expired','invalidated')),
	decision_id     TEXT,
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);
CREATE INDEX reviews_requests_digest_idx
	ON reviews_requests (installation_id, action_digest, created_at, id);
CREATE INDEX reviews_requests_list_idx
	ON reviews_requests (installation_id, state, created_at, id);

CREATE TABLE reviews_decisions (
	id              TEXT PRIMARY KEY,
	review_id       TEXT NOT NULL,
	review_version  INTEGER NOT NULL CHECK (review_version >= 1),
	installation_id TEXT NOT NULL,
	action_digest   TEXT NOT NULL CHECK (length(action_digest) = 64),
	reviewer_id     TEXT NOT NULL,
	decision        TEXT NOT NULL CHECK (decision IN ('approve','reject')),
	decided_at      TEXT NOT NULL,
	reason          TEXT NOT NULL,
	created_at      TEXT NOT NULL
);
CREATE INDEX reviews_decisions_review_idx ON reviews_decisions (review_id);

CREATE TABLE reviews_delegations (
	id              TEXT PRIMARY KEY,
	review_id       TEXT NOT NULL,
	review_version  INTEGER NOT NULL CHECK (review_version >= 1),
	installation_id TEXT NOT NULL,
	delegator_id    TEXT NOT NULL,
	delegatee_id    TEXT NOT NULL,
	created_at      TEXT NOT NULL
);
CREATE UNIQUE INDEX reviews_delegations_pair_idx
	ON reviews_delegations (review_id, delegator_id, delegatee_id);
CREATE INDEX reviews_delegations_delegatee_idx
	ON reviews_delegations (review_id, delegatee_id);

CREATE TABLE reviews_reviewers (
	review_id    TEXT NOT NULL,
	principal_id TEXT NOT NULL,
	created_at   TEXT NOT NULL,
	PRIMARY KEY (review_id, principal_id)
);
CREATE INDEX reviews_reviewers_principal_idx ON reviews_reviewers (principal_id);
`

// migrations returns the reviews-owned migration set. Bodies are pinned by
// SHA-256 so storage can detect any drift from the reviewed schema.
func migrations() []contract.Migration {
	return []contract.Migration{{
		Owner:   owner,
		Version: 1,
		SQL:     schemaV1,
		SHA256:  contract.Hash([]byte(schemaV1)),
	}}
}
