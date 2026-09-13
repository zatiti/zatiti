package evidence

import (
	"github.com/zatiti/zatiti/internal/contract"
)

// schemaV1 creates evidence's owned table: one durable command record per
// (installation, principal, operation, operation version, submission key)
// identity. A row is inserted unfinished by _evidence.command.begin and
// updated exactly once by _evidence.command.finish; the unique index is what
// makes a fresh begin() and a replaying begin() distinguishable, and the
// finished fence is what makes a double finish() impossible.
const schemaV1 = `
CREATE TABLE evidence_commands (
	id                 TEXT PRIMARY KEY,
	installation_id    TEXT NOT NULL,
	principal_id       TEXT NOT NULL,
	operation          TEXT NOT NULL,
	operation_version  INTEGER NOT NULL,
	submission_key     TEXT NOT NULL,
	request_digest     TEXT NOT NULL,
	status             TEXT NOT NULL DEFAULT '',
	data_json          TEXT NOT NULL DEFAULT '{}',
	error_code         TEXT NOT NULL DEFAULT '',
	result_json        TEXT NOT NULL DEFAULT '',
	finished           INTEGER NOT NULL DEFAULT 0,
	created_at         TEXT NOT NULL,
	finished_at        TEXT
);
CREATE UNIQUE INDEX evidence_commands_identity
	ON evidence_commands(installation_id, principal_id, operation, operation_version, submission_key);
`

// migrations returns the single evidence schema migration.
func migrations() []contract.Migration {
	return []contract.Migration{{
		Owner:   ownerName,
		Version: 1,
		SQL:     schemaV1,
		SHA256:  contract.Hash([]byte(schemaV1)),
	}}
}
