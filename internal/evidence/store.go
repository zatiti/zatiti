package evidence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Storage rows and access helpers. Command rows persist in evidence's own
// evidence_commands table. Events read from storage_events: storage owns
// that append-only outbox and persists exactly the bytes a handler's Emit
// call supplies (see storage's own Events doc); this package is the
// authorized, scoped, redacting reader in front of it, never a bypass.

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

func formatStamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseStamp(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

// nullableStamp renders a zero time as SQL NULL and a set time as its UTC
// RFC3339Nano string.
func nullableStamp(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return formatStamp(t)
}

// commandRow is one durable command identity. A fresh row is inserted
// unfinished (finished=0, status/data_json/result_json placeholders) by
// begin() and finished, under the finished=0 fence, by finish(). An
// unfinished row is a private in-transaction detail: if the enclosing write
// transaction never reaches finish(), the whole insert rolls back with it and
// no other transaction ever observes it.
//
// The finished column counts dispositions. 1 is the first disposition; an
// accepted first disposition is the pending disposition of a synchronous
// local IO mutation, which the Finish transaction replaces exactly once
// (finished=2). Nothing replaces a completed or failed disposition, and
// nothing replaces a replacement.
type commandRow struct {
	ID               contract.ID
	InstallationID   contract.ID
	PrincipalID      contract.ID
	Operation        string
	OperationVersion int64
	SubmissionKey    string
	RequestDigest    contract.Digest
	Status           string
	DataJSON         string
	ErrorCode        string
	ResultJSON       string
	Finished         bool
	Dispositions     int64
	CreatedAt        time.Time
	FinishedAt       time.Time
}

const commandColumns = `id, installation_id, principal_id, operation, operation_version, submission_key,
	request_digest, status, data_json, error_code, result_json, finished, created_at, finished_at`

func scanCommand(scan func(dest ...any) error) (*commandRow, error) {
	var c commandRow
	var finished int64
	var created string
	var finishedAt sql.NullString
	err := scan(&c.ID, &c.InstallationID, &c.PrincipalID, &c.Operation, &c.OperationVersion,
		&c.SubmissionKey, &c.RequestDigest, &c.Status, &c.DataJSON, &c.ErrorCode, &c.ResultJSON,
		&finished, &created, &finishedAt)
	if err != nil {
		return nil, err
	}
	c.Finished = finished != 0
	c.Dispositions = finished
	var perr error
	c.CreatedAt, perr = parseStamp(created)
	if perr != nil {
		return nil, fmt.Errorf("evidence: command %s created timestamp: %w", c.ID, perr)
	}
	if finishedAt.Valid && finishedAt.String != "" {
		c.FinishedAt, perr = parseStamp(finishedAt.String)
		if perr != nil {
			return nil, fmt.Errorf("evidence: command %s finished timestamp: %w", c.ID, perr)
		}
	}
	return &c, nil
}

// insertCommand reserves a fresh, unfinished command identity.
func insertCommand(ctx context.Context, unit contract.Unit, c *commandRow) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO evidence_commands (`+commandColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(c.ID), string(c.InstallationID), string(c.PrincipalID), c.Operation, c.OperationVersion,
		c.SubmissionKey, string(c.RequestDigest), c.Status, c.DataJSON, c.ErrorCode, c.ResultJSON,
		c.Dispositions, formatStamp(c.CreatedAt), nullableStamp(c.FinishedAt))
	return err
}

// loadCommandByID returns one command by id, or nil when absent.
func loadCommandByID(ctx context.Context, unit contract.Unit, id contract.ID) (*commandRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+commandColumns+` FROM evidence_commands WHERE id = ?`, string(id))
	c, err := scanCommand(row.Scan)
	if isNoRows(err) {
		return nil, nil
	}
	return c, err
}

// loadCommandByIdentity returns the command bound to one
// (installation, principal, operation, operation_version, submission_key)
// identity, or nil when absent. This is the exact uniqueness key: keys bind
// principal, operation, version and the canonical request hash carried
// alongside it.
func loadCommandByIdentity(ctx context.Context, unit contract.Unit, install, principal contract.ID, operation string, version int64, submissionKey string) (*commandRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+commandColumns+` FROM evidence_commands
		WHERE installation_id = ? AND principal_id = ? AND operation = ? AND operation_version = ? AND submission_key = ?`,
		string(install), string(principal), operation, version, submissionKey)
	c, err := scanCommand(row.Scan)
	if isNoRows(err) {
		return nil, nil
	}
	return c, err
}

// loadFinishedCommandByIdentity is loadCommandByIdentity restricted to
// finished rows and additionally bound to a caller principal: command.get
// never reveals another principal's command, so the principal is part of the
// lookup itself rather than a post-hoc filter.
func loadFinishedCommandByIdentity(ctx context.Context, unit contract.Unit, install, principal contract.ID, operation string, version int64, submissionKey string) (*commandRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+commandColumns+` FROM evidence_commands
		WHERE installation_id = ? AND principal_id = ? AND operation = ? AND operation_version = ? AND submission_key = ? AND finished >= 1`,
		string(install), string(principal), operation, version, submissionKey)
	c, err := scanCommand(row.Scan)
	if isNoRows(err) {
		return nil, nil
	}
	return c, err
}

// finishCommand persists the first disposition of one reserved command,
// fenced on its still-unfinished state so two first dispositions can never
// both land.
func finishCommand(ctx context.Context, unit contract.Unit, c *commandRow, status, dataJSON, errorCode, resultJSON string, now time.Time) error {
	res, err := unit.ExecContext(ctx, `UPDATE evidence_commands
		SET status = ?, data_json = ?, error_code = ?, result_json = ?, finished = 1, finished_at = ?
		WHERE id = ? AND finished = 0`,
		status, dataJSON, errorCode, resultJSON, formatStamp(now), string(c.ID))
	if err != nil {
		return err
	}
	return c.dispositionWritten(res, 1, status, dataJSON, errorCode, resultJSON, now)
}

// replaceable reports whether the row holds a pending disposition: an
// accepted first disposition that has not been replaced yet.
func (c *commandRow) replaceable() bool {
	return c.Dispositions == 1 && c.Status == contract.StatusAccepted
}

// replacePendingCommand replaces the accepted pending disposition of one
// command, fenced on exactly that state so the replacement happens once.
func replacePendingCommand(ctx context.Context, unit contract.Unit, c *commandRow, status, dataJSON, errorCode, resultJSON string, now time.Time) error {
	res, err := unit.ExecContext(ctx, `UPDATE evidence_commands
		SET status = ?, data_json = ?, error_code = ?, result_json = ?, finished = 2, finished_at = ?
		WHERE id = ? AND finished = 1 AND status = ?`,
		status, dataJSON, errorCode, resultJSON, formatStamp(now), string(c.ID), contract.StatusAccepted)
	if err != nil {
		return err
	}
	return c.dispositionWritten(res, 2, status, dataJSON, errorCode, resultJSON, now)
}

// dispositionWritten confirms the fenced update changed exactly this row and
// mirrors the persisted disposition onto c.
func (c *commandRow) dispositionWritten(res sql.Result, dispositions int64, status, dataJSON, errorCode, resultJSON string, now time.Time) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return internalFault("command %s is already finished or does not exist", c.ID)
	}
	c.Status = status
	c.DataJSON = dataJSON
	c.ErrorCode = errorCode
	c.ResultJSON = resultJSON
	c.Finished = true
	c.Dispositions = dispositions
	c.FinishedAt = now
	return nil
}

// projectCommand renders one finished command row onto its wire form.
func projectCommand(row *commandRow) (wireCommand, error) {
	var result contract.Result
	if err := json.Unmarshal([]byte(row.ResultJSON), &result); err != nil {
		return wireCommand{}, internalFault("command %s stored result does not decode: %v", row.ID, err)
	}
	data := json.RawMessage(row.DataJSON)
	if isEmptyOrNullJSON(data) {
		data = json.RawMessage(`{}`)
	}
	return wireCommand{
		ID:               row.ID,
		PrincipalID:      row.PrincipalID,
		Operation:        row.Operation,
		OperationVersion: row.OperationVersion,
		SubmissionKey:    row.SubmissionKey,
		RequestDigest:    row.RequestDigest,
		Status:           row.Status,
		Data:             data,
		ErrorCode:        row.ErrorCode,
		Result:           result,
	}, nil
}

// Event reads. storage_events is storage's own table (see
// internal/storage); this package reads it directly because storage's
// Database.Events method is a trusted internal raw feed handed only to the
// controller/application assembly layer, not to a domain Handle call bound
// to a contract.Unit. Every read here is a parameterized, scope-bound
// SELECT — never a string-interpolated filter.

const eventColumns = `id, sequence, at, installation_id, organization_id, project_id, worker_id, task_id,
	kind, resource_id, resource_version, data`

func scanEvent(scan func(dest ...any) error) (*contract.Event, error) {
	var e contract.Event
	var at string
	var data []byte
	var seq, version int64
	err := scan(&e.ID, &seq, &at, &e.Scope.InstallationID, &e.Scope.OrganizationID,
		&e.Scope.ProjectID, &e.Scope.WorkerID, &e.Scope.TaskID, &e.Kind, &e.ResourceID, &version, &data)
	if err != nil {
		return nil, err
	}
	ts, perr := parseStamp(at)
	if perr != nil {
		return nil, fmt.Errorf("evidence: event %s timestamp: %w", e.ID, perr)
	}
	e.Sequence = seq
	e.At = ts
	e.ResourceVersion = contract.Version(version)
	if isEmptyOrNullJSON(data) {
		data = []byte(`{}`)
	}
	e.Data = json.RawMessage(data)
	return &e, nil
}

// loadEventByID returns one event by id, or nil when absent.
func loadEventByID(ctx context.Context, unit contract.Unit, id contract.ID) (*contract.Event, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM storage_events WHERE id = ?`, string(id))
	e, err := scanEvent(row.Scan)
	if isNoRows(err) {
		return nil, nil
	}
	return e, err
}

// scopeConds builds the exact-match scope WHERE fragment for the non-empty
// fields of scope. Installation is always required; every other scope
// dimension narrows only when the caller supplied it, so a caller scoped to
// just an installation sees every event of that installation and a caller
// scoped to a project sees only that project's events.
func scopeConds(scope contract.Scope) ([]string, []any) {
	conds := []string{"installation_id = ?"}
	args := []any{string(scope.InstallationID)}
	if scope.OrganizationID != "" {
		conds = append(conds, "organization_id = ?")
		args = append(args, string(scope.OrganizationID))
	}
	if scope.ProjectID != "" {
		conds = append(conds, "project_id = ?")
		args = append(args, string(scope.ProjectID))
	}
	if scope.WorkerID != "" {
		conds = append(conds, "worker_id = ?")
		args = append(args, string(scope.WorkerID))
	}
	if scope.TaskID != "" {
		conds = append(conds, "task_id = ?")
		args = append(args, string(scope.TaskID))
	}
	return conds, args
}

// scopeVisible reports whether an event stamped with eventScope is visible
// to a caller scoped to callerScope: installation must match exactly, and
// every other caller-supplied dimension must match the event's own.
func scopeVisible(callerScope, eventScope contract.Scope) bool {
	if callerScope.InstallationID != eventScope.InstallationID {
		return false
	}
	if callerScope.OrganizationID != "" && callerScope.OrganizationID != eventScope.OrganizationID {
		return false
	}
	if callerScope.ProjectID != "" && callerScope.ProjectID != eventScope.ProjectID {
		return false
	}
	if callerScope.WorkerID != "" && callerScope.WorkerID != eventScope.WorkerID {
		return false
	}
	if callerScope.TaskID != "" && callerScope.TaskID != eventScope.TaskID {
		return false
	}
	return true
}

// maxEventSequence returns the current checkpoint: the highest sequence
// number of any event visible to scope, or zero when none exist.
func maxEventSequence(ctx context.Context, unit contract.Unit, scope contract.Scope) (int64, error) {
	conds, args := scopeConds(scope)
	var seq int64
	err := unit.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(sequence), 0) FROM storage_events WHERE `+strings.Join(conds, " AND "),
		args...).Scan(&seq)
	return seq, err
}

// queryEventsPage returns up to limit events after sequence after, visible
// to scope and narrowed by filter, in increasing sequence order, plus
// whether a following page exists. It fetches one extra row to learn that
// without a second round trip.
func queryEventsPage(ctx context.Context, unit contract.Unit, scope contract.Scope, filter eventFilter, after, limit int64) ([]contract.Event, bool, error) {
	conds, args := scopeConds(scope)
	conds = append(conds, "sequence > ?")
	args = append(args, after)
	if filter.OrganizationID != "" {
		conds = append(conds, "organization_id = ?")
		args = append(args, string(filter.OrganizationID))
	}
	if filter.WorkerID != "" {
		conds = append(conds, "worker_id = ?")
		args = append(args, string(filter.WorkerID))
	}
	if filter.TaskID != "" {
		conds = append(conds, "task_id = ?")
		args = append(args, string(filter.TaskID))
	}
	args = append(args, limit+1)
	rows, err := unit.QueryContext(ctx, `SELECT `+eventColumns+` FROM storage_events
		WHERE `+strings.Join(conds, " AND ")+` ORDER BY sequence LIMIT ?`, args...)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]contract.Event, 0)
	for rows.Next() {
		e, err := scanEvent(rows.Scan)
		if err != nil {
			return nil, false, err
		}
		out = append(out, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := int64(len(out)) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}
