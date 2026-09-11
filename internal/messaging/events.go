package messaging

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Event emission and pagination cursors.

// Event kinds emitted by messaging. Each marks a durable state change a
// controller or client can correlate; routine coordination emits only the
// delivery events of meaningful messages.
const (
	eventConversationCreated      = "messaging.conversation.created"
	eventConversationUpdated      = "messaging.conversation.updated"
	eventMessageAdmitted          = "messaging.message.admitted"
	eventMessageAcknowledged      = "messaging.message.acknowledged"
	eventConversationBootstrapped = "messaging.conversation.bootstrapped"
)

// emitConversationEvent appends one event for a conversation change to the
// storage outbox inside the caller's transaction. The event scope is the
// transaction scope: storage stamps events with the unit scope and refuses
// mismatches, so a stored scope that narrows the transaction (a worker-scoped
// conversation) must never leak into the emitted document.
func (s *Service) emitConversationEvent(ctx context.Context, unit contract.Unit, row *conversationRow, kind string) error {
	raw, err := marshalData(map[string]any{
		"kind":            row.Kind,
		"participant_ids": mustIDList(row.ParticipantIDsJSON),
		"title":           row.Title,
	})
	if err != nil {
		return err
	}
	return unit.Emit(ctx, contract.Event{
		Kind:            kind,
		ResourceID:      row.ID,
		ResourceVersion: contract.Version(row.Version),
		Scope:           unit.Scope(),
		Data:            raw,
	})
}

// emitMessageEvent appends one event for a message delivery change, stamped
// with the transaction scope for the same reason.
func (s *Service) emitMessageEvent(ctx context.Context, unit contract.Unit, row *messageRow, kind string) error {
	raw, err := marshalData(map[string]any{
		"state":           row.State,
		"sender_id":       row.SenderID,
		"conversation_id": row.ConversationID,
	})
	if err != nil {
		return err
	}
	return unit.Emit(ctx, contract.Event{
		Kind:            kind,
		ResourceID:      row.ID,
		ResourceVersion: contract.Version(row.Version),
		Scope:           unit.Scope(),
		Data:            raw,
	})
}

// mustIDList decodes a stored id array; malformed owned state is an
// internal fault.
func mustIDList(raw string) []contract.ID {
	var ids []contract.ID
	if err := decodeJSON("participant ids", raw, &ids); err != nil {
		panic(err)
	}
	if ids == nil {
		ids = []contract.ID{}
	}
	return ids
}

// cursorPayload is the decoded pagination cursor. The signature binds the
// cursor to the principal, installation, operation, exact query fingerprint
// and the evidence checkpoint the page was minted against, so a cursor
// minted for one query or caller cannot be replayed against another.
type cursorPayload struct {
	Offset   int64  `json:"offset"`
	Snapshot int64  `json:"snapshot"`
	Expires  string `json:"expires"`
	Sig      string `json:"sig"`
}

// cursorTTL bounds how long a paging cursor stays usable; expired cursors
// restart from a fresh snapshot.
const cursorTTL = 15 * time.Minute

// cursorFingerprint is the exact-match serialization of a structured query
// that the cursor signature binds to.
func cursorFingerprint(op string, parts ...string) string {
	return strings.Join(append([]string{op}, parts...), "|")
}

// cursorSignature binds the cursor to the principal, installation,
// operation, query fingerprint, evidence checkpoint and expiry.
func (s *Service) cursorSignature(unit contract.Unit, fingerprint string, snapshot int64, expires string) string {
	sum := contract.Hash([]byte(strings.Join([]string{
		string(unit.Actor().PrincipalID), string(unit.Scope().InstallationID), fingerprint,
		snapshotText(snapshot), expires,
	}, "|")))
	return string(sum[:16])
}

func snapshotText(snapshot int64) string {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return "0"
	}
	return string(raw)
}

// encodeCursor mints an opaque cursor for the next page.
func (s *Service) encodeCursor(unit contract.Unit, fingerprint string, snapshot int64, at time.Time, offset int64) (string, error) {
	expires := at.Add(cursorTTL).Format(timeLayout)
	p := cursorPayload{Offset: offset, Snapshot: snapshot, Expires: expires, Sig: s.cursorSignature(unit, fingerprint, snapshot, expires)}
	raw, err := json.Marshal(p)
	if err != nil {
		return "", internalError("cursor encoding failed: %v", err)
	}
	return string(raw), nil
}

// decodeCursor validates a caller-supplied cursor against the exact query
// fingerprint and its own expiry; any mismatch or lapse is cursor_expired
// with snapshot_required=true so the client restarts from a fresh page.
func (s *Service) decodeCursor(unit contract.Unit, fingerprint, cursor string) (int64, int64, error) {
	if cursor == "" {
		return 0, 0, nil
	}
	var p cursorPayload
	if err := contract.DecodeStrict([]byte(cursor), &p); err != nil {
		return 0, 0, cursorExpired("list cursor is malformed")
	}
	if p.Sig != s.cursorSignature(unit, fingerprint, p.Snapshot, p.Expires) {
		return 0, 0, cursorExpired("list cursor does not match the current query")
	}
	exp, err := time.Parse(timeLayout, p.Expires)
	if err != nil || s.clock.Now().After(exp) {
		return 0, 0, cursorExpired("list cursor has expired")
	}
	if p.Offset < 0 {
		return 0, 0, cursorExpired("list cursor offset is invalid")
	}
	return p.Offset, p.Snapshot, nil
}
