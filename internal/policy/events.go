package policy

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Event emission. Storage stamps event identity, sequence, timestamp and
// scope; handlers supply the kind and the resource coordinates. Kinds follow
// owner.entity.transition with at least three dot segments.

const (
	eventPolicyActivated    = "policy.policy.activated"
	eventPolicyArchived     = "policy.policy.archived"
	eventRuleActivated      = "policy.promotion_rule.activated"
	eventRuleArchived       = "policy.promotion_rule.archived"
	eventQualificationEvent = "policy.qualification.transition"
	eventQualificationSeen  = "policy.qualification.observed"
)

// emitTransition appends one state-correlated event to the transaction
// outbox. A failure fails the handler and rolls back the mutation.
func emitTransition(ctx context.Context, unit contract.Unit, kind string, resourceID contract.ID, version contract.Version) error {
	err := unit.Emit(ctx, contract.Event{
		Kind:            kind,
		ResourceID:      resourceID,
		ResourceVersion: version,
	})
	if err != nil {
		return fmt.Errorf("policy: emit %s: %w", kind, err)
	}
	return nil
}

// Opaque list cursors.
//
// A cursor binds the operation, installation, serialized filter and keyset
// position (created_at, id) under an HMAC, and carries an absolute expiry.
// A tampered, foreign or filter-mismatched cursor is invalid_input; an
// expired one is cursor_expired with snapshot_required=true, so clients
// re-read a fresh page instead of retrying a stale snapshot.

const (
	cursorTTL    = 15 * time.Minute
	cursorPrefix = "v1"
)

// listResult marshals a list page and attaches the next cursor when the
// page was full, signaling more rows may follow.
func listResult(items any, next *string) (contract.Payload, error) {
	body, err := json.Marshal(items)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("policy: encode list page: %w", err)
	}
	payload := contract.Payload{Status: contract.StatusCompleted, Data: body}
	payload.NextCursor = next
	return payload, nil
}

// cursorKeyLocked returns the lazily minted cursor authentication key. The
// caller must hold cursorMu. A process restart invalidates outstanding
// cursors, which clients observe as cursor_expired.
func (s *Service) cursorKeyLocked() ([]byte, error) {
	if len(s.cursorKey) == 0 {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("policy: mint cursor key: %w", err)
		}
		s.cursorKey = key
	}
	return s.cursorKey, nil
}

// mintCursor seals a keyset position for one list operation.
func (s *Service) mintCursor(op string, unitScope contract.Scope, filter any, lastCreated time.Time, lastID contract.ID, now time.Time) (string, error) {
	filterJSON, err := json.Marshal(filter)
	if err != nil {
		return "", fmt.Errorf("policy: bind cursor filter: %w", err)
	}
	payload := strings.Join([]string{
		cursorPrefix,
		op,
		string(unitScope.InstallationID),
		fmt.Sprintf("%x", sha256.Sum256(filterJSON)),
		formatStamp(lastCreated),
		string(lastID),
		strconv.FormatInt(now.Add(cursorTTL).Unix(), 10),
	}, "|")
	mac, err := s.hmac(payload)
	if err != nil {
		return "", err
	}
	return base64url([]byte(payload)) + "." + base64url(mac), nil
}

// readCursor validates a client cursor and returns its keyset position.
func (s *Service) readCursor(op string, unitScope contract.Scope, filter any, raw string) (createdAt time.Time, lastID contract.ID, err error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return time.Time{}, "", invalidInput("cursor is malformed")
	}
	payload, perr := base64.RawURLEncoding.DecodeString(parts[0])
	if perr != nil {
		return time.Time{}, "", invalidInput("cursor is malformed")
	}
	mac, merr := base64urlDecode(parts[1])
	if merr != nil {
		return time.Time{}, "", invalidInput("cursor is malformed")
	}
	expected, herr := s.hmac(string(payload))
	if herr != nil || !hmac.Equal(mac, expected) {
		return time.Time{}, "", invalidInput("cursor is malformed")
	}
	fields := strings.Split(string(payload), "|")
	if len(fields) != 7 || fields[0] != cursorPrefix || fields[1] != op ||
		fields[2] != string(unitScope.InstallationID) {
		return time.Time{}, "", invalidInput("cursor does not belong to this query")
	}
	filterJSON, err := json.Marshal(filter)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("policy: bind cursor filter: %w", err)
	}
	if fields[3] != fmt.Sprintf("%x", sha256.Sum256(filterJSON)) {
		return time.Time{}, "", invalidInput("cursor does not match the current filter")
	}
	expiry, cerr := strconv.ParseInt(fields[6], 10, 64)
	if cerr != nil {
		return time.Time{}, "", invalidInput("cursor is malformed")
	}
	if s.deps.Clock.Now().Unix() >= expiry {
		return time.Time{}, "", cursorExpired()
	}
	created, terr := parseStamp(fields[4])
	if terr != nil {
		return time.Time{}, "", invalidInput("cursor is malformed")
	}
	return created, contract.ID(fields[5]), nil
}

// hmac computes the cursor MAC under the lazily minted key.
func (s *Service) hmac(payload string) ([]byte, error) {
	s.cursorMu.Lock()
	defer s.cursorMu.Unlock()
	key, err := s.cursorKeyLocked()
	if err != nil {
		return nil, err
	}
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(payload))
	return m.Sum(nil), nil
}

func base64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func base64urlDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
