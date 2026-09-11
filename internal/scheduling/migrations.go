package scheduling

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// owner is the migration owner and table namespace prefix.
const owner = "scheduling"

// schemaV1 creates the scheduling-owned tables. All object names carry the
// scheduling_ prefix required by the storage namespace validator.
//
// Scope dimensions are stored both as explicit columns (for filtered reads
// and scope-containment checks) and as the canonical scope JSON snapshot
// (for exact wire output). Timestamps are UTC RFC3339Nano strings. Typed
// definitions and JSON arrays (task templates, signals, triggers, cycle
// outputs) persist as canonical JSON text.
//
// scheduling_wakes is the durable wake queue the controller drains with
// _scheduling.wake.due; admitted_at NULL marks a pending wake and the
// (source_id, occurrence_key) unique index makes duplicate wake inserts
// idempotent. scheduling_occurrences records admitted and skipped
// occurrences and is the dedupe fence for admission.
const schemaV1 = `
CREATE TABLE scheduling_schedules (
	id               TEXT PRIMARY KEY,
	version          INTEGER NOT NULL CHECK (version >= 1),
	installation_id  TEXT NOT NULL,
	organization_id  TEXT NOT NULL DEFAULT '',
	project_id       TEXT NOT NULL DEFAULT '',
	worker_id        TEXT NOT NULL DEFAULT '',
	task_id          TEXT NOT NULL DEFAULT '',
	scope_json       TEXT NOT NULL,
	task_template_json TEXT NOT NULL,
	timezone         TEXT NOT NULL,
	expression       TEXT NOT NULL,
	misfire          TEXT NOT NULL CHECK (misfire IN ('coalesce','skip')),
	catch_up_seconds INTEGER NOT NULL CHECK (catch_up_seconds >= 0),
	paused           INTEGER NOT NULL CHECK (paused IN (0,1)),
	next_wake        TEXT,
	archived         INTEGER NOT NULL CHECK (archived IN (0,1)),
	created_at       TEXT NOT NULL,
	updated_at       TEXT NOT NULL
);
CREATE INDEX scheduling_schedules_scope_idx
	ON scheduling_schedules (installation_id, organization_id, project_id, archived);

CREATE TABLE scheduling_responsibilities (
	id                TEXT PRIMARY KEY,
	version           INTEGER NOT NULL CHECK (version >= 1),
	installation_id   TEXT NOT NULL,
	organization_id   TEXT NOT NULL DEFAULT '',
	project_id        TEXT NOT NULL DEFAULT '',
	worker_id         TEXT NOT NULL,
	task_id           TEXT NOT NULL DEFAULT '',
	scope_json        TEXT NOT NULL,
	outcome           TEXT NOT NULL,
	signals_json      TEXT NOT NULL,
	triggers_json     TEXT NOT NULL,
	reasoning_policy  TEXT NOT NULL,
	min_interval_seconds INTEGER NOT NULL CHECK (min_interval_seconds >= 1),
	cycle_limits_json    TEXT NOT NULL,
	aggregate_limits_json TEXT NOT NULL,
	pause_conditions_json   TEXT NOT NULL,
	escalation_conditions_json TEXT NOT NULL,
	acceptance_json   TEXT NOT NULL,
	paused            INTEGER NOT NULL CHECK (paused IN (0,1)),
	next_wake         TEXT,
	last_cycle_at     TEXT,
	archived          INTEGER NOT NULL CHECK (archived IN (0,1)),
	created_at        TEXT NOT NULL,
	updated_at        TEXT NOT NULL
);
CREATE INDEX scheduling_responsibilities_scope_idx
	ON scheduling_responsibilities (installation_id, organization_id, worker_id, archived);

CREATE TABLE scheduling_wakes (
	id               TEXT PRIMARY KEY,
	installation_id  TEXT NOT NULL,
	organization_id  TEXT NOT NULL DEFAULT '',
	project_id       TEXT NOT NULL DEFAULT '',
	worker_id        TEXT NOT NULL DEFAULT '',
	task_id          TEXT NOT NULL DEFAULT '',
	scope_json       TEXT NOT NULL,
	source_kind      TEXT NOT NULL CHECK (source_kind IN ('schedule','responsibility')),
	source_id        TEXT NOT NULL,
	occurrence_key   TEXT NOT NULL,
	due_at           TEXT NOT NULL,
	condition_version INTEGER NOT NULL CHECK (condition_version >= 1),
	admitted_at      TEXT,
	created_at       TEXT NOT NULL,
	UNIQUE (source_id, occurrence_key)
);
CREATE INDEX scheduling_wakes_due_idx
	ON scheduling_wakes (admitted_at, due_at);

CREATE TABLE scheduling_occurrences (
	id              TEXT PRIMARY KEY,
	installation_id TEXT NOT NULL,
	organization_id TEXT NOT NULL DEFAULT '',
	project_id      TEXT NOT NULL DEFAULT '',
	worker_id       TEXT NOT NULL DEFAULT '',
	task_id         TEXT NOT NULL DEFAULT '',
	scope_json      TEXT NOT NULL,
	source_id       TEXT NOT NULL,
	occurrence_key  TEXT NOT NULL,
	task_ref        TEXT NOT NULL DEFAULT '',
	state           TEXT NOT NULL CHECK (state IN ('admitted','skipped')),
	reason          TEXT NOT NULL DEFAULT '',
	decided_at      TEXT NOT NULL,
	created_at      TEXT NOT NULL,
	UNIQUE (source_id, occurrence_key)
);
CREATE INDEX scheduling_occurrences_source_idx
	ON scheduling_occurrences (source_id, state);

CREATE TABLE scheduling_cycles (
	id                    TEXT PRIMARY KEY,
	responsibility_id     TEXT NOT NULL REFERENCES scheduling_responsibilities(id),
	responsibility_version INTEGER NOT NULL CHECK (responsibility_version >= 1),
	installation_id       TEXT NOT NULL,
	organization_id       TEXT NOT NULL DEFAULT '',
	project_id            TEXT NOT NULL DEFAULT '',
	worker_id             TEXT NOT NULL DEFAULT '',
	task_id               TEXT NOT NULL DEFAULT '',
	scope_json            TEXT NOT NULL,
	next_wake             TEXT NOT NULL,
	outputs_json          TEXT NOT NULL,
	task_ids_json         TEXT NOT NULL,
	recorded_at           TEXT NOT NULL
);
CREATE INDEX scheduling_cycles_responsibility_idx
	ON scheduling_cycles (responsibility_id, recorded_at);
`

// migrations returns the scheduling-owned migration set. Bodies are pinned
// by SHA-256 so storage can detect any drift from the reviewed schema.
func migrations() []contract.Migration {
	return []contract.Migration{{
		Owner:   owner,
		Version: 1,
		SQL:     schemaV1,
		SHA256:  contract.Hash([]byte(schemaV1)),
	}}
}

// encodeScope marshals a scope for the scope_json column.
func encodeScope(s contract.Scope) (string, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("scheduling: encode scope: %w", err)
	}
	return string(raw), nil
}

// decodeScope parses a stored scope snapshot.
func decodeScope(raw string) (contract.Scope, error) {
	var s contract.Scope
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return contract.Scope{}, fmt.Errorf("scheduling: decode stored scope: %w", err)
	}
	return s, nil
}

// parseStamp parses a stored UTC RFC3339Nano timestamp.
func parseStamp(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduling: decode stored timestamp: %w", err)
	}
	return t, nil
}

// formatStamp renders a UTC timestamp with a fixed nine-digit fraction so
// lexicographic string comparison matches chronological order, which the
// keyset pagination and the due-wake scan rely on.
func formatStamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
}
