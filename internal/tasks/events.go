package tasks

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

// emitTaskEvent appends one state-correlated event for a task change to
// the storage outbox inside the caller's transaction. Identity, sequence
// and timestamp are assigned by storage.
func (s *Service) emitTaskEvent(ctx context.Context, unit contract.Unit, row *taskRow, kind string, data map[string]any) error {
	raw, err := marshalData(data)
	if err != nil {
		return err
	}
	scope, err := row.decodeScope()
	if err != nil {
		return err
	}
	return unit.Emit(ctx, contract.Event{
		Kind:            kind,
		ResourceID:      row.ID,
		ResourceVersion: contract.Version(row.Version),
		Scope:           scope.toContract(),
		Data:            raw,
	})
}

// cursorPayload is the decoded pagination cursor. The signature binds the
// cursor to the principal, installation, operation and exact query
// fingerprint so a cursor minted for one query or caller cannot be
// replayed against another.
type cursorPayload struct {
	Offset int64  `json:"offset"`
	Sig    string `json:"sig"`
}

// cursorSignature binds the cursor to the principal, installation,
// operation and query fingerprint.
func cursorSignature(unit contract.Unit, op, fingerprint string) string {
	sum := contract.Hash([]byte(strings.Join([]string{
		string(unit.Actor().PrincipalID), string(unit.Scope().InstallationID), op, fingerprint,
	}, "|")))
	return string(sum[:16])
}

// encodeCursor mints an opaque cursor for the next page.
func encodeCursor(unit contract.Unit, op, fingerprint string, offset int64) (string, error) {
	raw, err := json.Marshal(cursorPayload{Offset: offset, Sig: cursorSignature(unit, op, fingerprint)})
	if err != nil {
		return "", internalError("cursor encoding failed: %v", err)
	}
	return string(raw), nil
}

// decodeCursor validates a caller-supplied cursor against the exact query
// fingerprint; any mismatch is cursor_expired.
func decodeCursor(unit contract.Unit, op, fingerprint, cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	var p cursorPayload
	if err := contract.DecodeStrict([]byte(cursor), &p); err != nil {
		return 0, cursorExpired("list cursor is malformed")
	}
	if p.Sig != cursorSignature(unit, op, fingerprint) {
		return 0, cursorExpired("list cursor does not match the current query")
	}
	if p.Offset < 0 {
		return 0, cursorExpired("list cursor offset is invalid")
	}
	return p.Offset, nil
}
