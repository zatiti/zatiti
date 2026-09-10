package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// eventScanner is the subset of transaction execution an event write needs.
// *sql.Conn satisfies it.
type eventExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// appendEvent inserts one event into the storage outbox. It must run inside
// the caller's write transaction: BEGIN IMMEDIATE holds the writer lock, so
// the MAX(sequence) read and the insert are race-free and the event commits
// atomically with the state change it correlates to. The event identity,
// sequence and timestamp are assigned here; caller values are ignored.
func appendEvent(ctx context.Context, x eventExec, scope contract.Scope, event contract.Event) error {
	if err := validateEvent(event); err != nil {
		return err
	}

	var seq int64
	if err := x.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence), 0) + 1 FROM storage_events").Scan(&seq); err != nil {
		return storageFault("allocate event sequence", err)
	}

	data := []byte(event.Data)
	if len(data) == 0 {
		data = nil
	}
	id := contract.NewID()
	at := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := x.ExecContext(ctx, `INSERT INTO storage_events
		(id, sequence, at, installation_id, organization_id, project_id, worker_id, task_id,
		 kind, resource_id, resource_version, data)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(id), seq, at,
		string(scope.InstallationID), string(scope.OrganizationID), string(scope.ProjectID),
		string(scope.WorkerID), string(scope.TaskID),
		event.Kind, string(event.ResourceID), int64(event.ResourceVersion), data,
	); err != nil {
		return storageFault("persist event", err)
	}
	return nil
}

// validateEvent checks the event shape storage persists. Redaction and
// authorization of the payload belong to the evidence owner before exposure;
// storage persists the exact bytes it is given.
func validateEvent(event contract.Event) error {
	segments := strings.Split(event.Kind, ".")
	if len(segments) < 3 {
		return invalidInputFault("event kind %q must be owner.entity.transition", event.Kind)
	}
	for _, s := range segments {
		if s == "" {
			return invalidInputFault("event kind %q contains an empty segment", event.Kind)
		}
	}
	if event.ResourceID == "" {
		return invalidInputFault("event resource id is required")
	}
	if !validUUIDShape(string(event.ResourceID)) {
		return invalidInputFault("event resource id %q is not a UUID", event.ResourceID)
	}
	if event.ResourceVersion < 1 {
		return invalidInputFault("event resource version must be at least 1")
	}
	if len(event.Data) > 0 && !json.Valid(event.Data) {
		return invalidInputFault("event data is not valid JSON")
	}
	return nil
}

// Events returns up to limit events with a sequence greater than after, in
// increasing sequence order. It is the trusted internal raw feed used by
// delivery and recovery; payloads are redacted and authorized by the
// evidence owner before any public exposure. A limit of zero or less selects
// the default page of 100; a limit above 500 is rejected.
func (d *database) Events(ctx context.Context, after int64, limit int) ([]contract.Event, error) {
	if d.closed.Load() {
		return nil, closedFault()
	}
	if limit <= 0 {
		limit = defaultEventPage
	}
	if limit > maxEventPage {
		return nil, invalidInputFault("event page limit %d exceeds the maximum of %d", limit, maxEventPage)
	}

	rows, err := d.db.QueryContext(ctx, `SELECT id, sequence, at, installation_id, organization_id,
		project_id, worker_id, task_id, kind, resource_id, resource_version, data
		FROM storage_events WHERE sequence > ? ORDER BY sequence LIMIT ?`, after, limit)
	if err != nil {
		return nil, storageFault("query events", err)
	}
	defer func() { _ = rows.Close() }()

	var events []contract.Event
	for rows.Next() {
		var (
			e       contract.Event
			seq     int64
			at      string
			version int64
			data    []byte
		)
		if err := rows.Scan(&e.ID, &seq, &at,
			&e.Scope.InstallationID, &e.Scope.OrganizationID, &e.Scope.ProjectID,
			&e.Scope.WorkerID, &e.Scope.TaskID,
			&e.Kind, &e.ResourceID, &version, &data); err != nil {
			return nil, storageFault("scan event", err)
		}
		ts, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			return nil, fmt.Errorf("storage: parse event timestamp %q: %w", at, err)
		}
		e.Sequence = seq
		e.At = ts
		e.ResourceVersion = contract.Version(version)
		e.Data = json.RawMessage(data)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, storageFault("read events", err)
	}
	return events, nil
}
