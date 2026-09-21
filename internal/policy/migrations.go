package policy

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// owner is the migration owner and table namespace prefix.
const owner = "policy"

// schemaV1 creates the policy-owned tables. All object names carry the
// policy_ prefix required by the storage namespace validator.
//
// Scope dimensions are stored both as explicit columns (for filtered reads
// and scope-containment checks) and as the canonical scope JSON snapshot
// (for exact wire output). Timestamps are UTC RFC3339Nano strings. JSON
// arrays (rules, destinations, evidence versions) persist as canonical JSON
// text.
const schemaV1 = `
CREATE TABLE policy_policies (
	id              TEXT PRIMARY KEY,
	version         INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	project_id      TEXT NOT NULL DEFAULT '',
	worker_id       TEXT NOT NULL DEFAULT '',
	task_id         TEXT NOT NULL DEFAULT '',
	scope_json      TEXT NOT NULL,
	rules_json      TEXT NOT NULL,
	extensions_json TEXT,
	archived        INTEGER NOT NULL CHECK (archived IN (0,1)),
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);
CREATE INDEX policy_policies_scope_idx
	ON policy_policies (installation_id, organization_id, project_id, archived);

CREATE TABLE policy_promotion_rules (
	id                       TEXT PRIMARY KEY,
	version                  INTEGER NOT NULL CHECK (version >= 1),
	installation_id          TEXT NOT NULL,
	organization_id          TEXT NOT NULL DEFAULT '',
	project_id               TEXT NOT NULL DEFAULT '',
	worker_id                TEXT NOT NULL DEFAULT '',
	task_id                  TEXT NOT NULL DEFAULT '',
	scope_json               TEXT NOT NULL,
	capability               TEXT NOT NULL,
	destinations_json        TEXT NOT NULL,
	required_evidence_json   TEXT NOT NULL,
	minimum_successes        INTEGER NOT NULL CHECK (minimum_successes >= 1),
	evidence_window_seconds  INTEGER NOT NULL CHECK (evidence_window_seconds >= 1),
	disqualifying_events_json TEXT NOT NULL,
	ceiling_grant_id         TEXT NOT NULL,
	human_required_preserved INTEGER NOT NULL CHECK (human_required_preserved IN (0,1)),
	archived                 INTEGER NOT NULL CHECK (archived IN (0,1)),
	created_at               TEXT NOT NULL,
	updated_at               TEXT NOT NULL
);
CREATE INDEX policy_promotion_rules_scope_idx
	ON policy_promotion_rules (installation_id, organization_id, project_id, capability, archived);

CREATE TABLE policy_qualifications (
	id              TEXT PRIMARY KEY,
	version         INTEGER NOT NULL CHECK (version >= 1),
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	project_id      TEXT NOT NULL DEFAULT '',
	worker_id       TEXT NOT NULL DEFAULT '',
	task_id         TEXT NOT NULL DEFAULT '',
	scope_json      TEXT NOT NULL,
	worker_ref      TEXT NOT NULL,
	capability      TEXT NOT NULL,
	destinations_json TEXT NOT NULL,
	rule_id         TEXT NOT NULL,
	rule_version    INTEGER NOT NULL CHECK (rule_version >= 1),
	model           TEXT NOT NULL DEFAULT '',
	tool_versions_json   TEXT NOT NULL,
	skill_versions_json  TEXT NOT NULL,
	evidence_ids_json    TEXT NOT NULL,
	window_start    TEXT NOT NULL,
	window_end      TEXT NOT NULL,
	state           TEXT NOT NULL CHECK (state IN ('proposed','qualified','rejected','restricted','expired')),
	explanation     TEXT NOT NULL DEFAULT '',
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL
);
CREATE INDEX policy_qualifications_scope_idx
	ON policy_qualifications (installation_id, organization_id, worker_ref, state);
CREATE INDEX policy_qualifications_rule_idx
	ON policy_qualifications (rule_id, rule_version);

CREATE TABLE policy_evidence (
	qualification_id  TEXT NOT NULL REFERENCES policy_qualifications(id),
	evidence_id       TEXT NOT NULL,
	kind              TEXT NOT NULL,
	first_state       TEXT NOT NULL,
	first_version     INTEGER NOT NULL CHECK (first_version >= 1),
	first_at          TEXT NOT NULL,
	succeeded_at      TEXT,
	succeeded_version INTEGER CHECK (succeeded_version IS NULL OR succeeded_version >= 1),
	PRIMARY KEY (qualification_id, evidence_id)
);
CREATE INDEX policy_evidence_evidence_idx ON policy_evidence (evidence_id);
`

// schemaV2 adds the columns that connect autonomy evaluation to the cited
// evidence tasks' own pinned acceptance identity and dependency closure
// (P36): which verifier independently established each credited result, and
// which upstream tasks that result itself depended on, so _policy.invalidate
// can restrict a qualification whose evidence no longer stands on an
// unchanged foundation, not merely whose own row was cited directly.
const schemaV2 = `
ALTER TABLE policy_qualifications ADD COLUMN dependencies_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE policy_evidence ADD COLUMN acceptance_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE policy_evidence ADD COLUMN verifier_id TEXT NOT NULL DEFAULT '';
ALTER TABLE policy_evidence ADD COLUMN verifier_version TEXT NOT NULL DEFAULT '';
`

// migrations returns the policy-owned migration set. Bodies are pinned by
// SHA-256 so storage can detect any drift from the reviewed schema.
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
	}}
}

// encodeScope marshals a scope for the scope_json column.
func encodeScope(s contract.Scope) (string, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("policy: encode scope: %w", err)
	}
	return string(raw), nil
}

// decodeScope parses a stored scope snapshot.
func decodeScope(raw string) (contract.Scope, error) {
	var s contract.Scope
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return contract.Scope{}, fmt.Errorf("policy: decode stored scope: %w", err)
	}
	return s, nil
}

// parseStamp parses a stored UTC RFC3339Nano timestamp.
func parseStamp(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("policy: decode stored timestamp: %w", err)
	}
	return t, nil
}

func formatStamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
