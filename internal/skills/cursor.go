package skills

import (
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

// Opaque list cursors.
//
// A cursor binds the operation, installation, serialized filter and keyset
// position (name, version) under an HMAC, and carries an absolute expiry.
// A tampered, foreign or filter-mismatched cursor is invalid_input; an
// expired one is cursor_expired, so clients re-read a fresh page instead of
// retrying a stale snapshot.

const (
	cursorTTL    = 15 * time.Minute
	cursorPrefix = "v1"
)

// listResult marshals a list page and attaches the next cursor when the
// page was full, signaling more rows may follow.
func listResult(items any, next *string) (contract.Payload, error) {
	body, err := marshalData(items)
	if err != nil {
		return contract.Payload{}, err
	}
	payload := contract.Payload{Status: contract.StatusCompleted, Data: body}
	payload.NextCursor = next
	return payload, nil
}

// cursorKeyLocked returns the lazily minted cursor authentication key. The
// caller must hold s.cursorMu. A process restart invalidates outstanding
// cursors, which clients observe as cursor_expired.
func (s *Service) cursorKeyLocked() ([]byte, error) {
	if len(s.cursorKey) == 0 {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, faultWrap(internalError("cursor key generation failed"), err)
		}
		s.cursorKey = key
	}
	return s.cursorKey, nil
}

// mintCursor seals a keyset position for one list operation.
func (s *Service) mintCursor(op string, unitScope contract.Scope, filter any, lastName string, lastVersion int64, now time.Time) (string, error) {
	filterJSON, err := json.Marshal(filter)
	if err != nil {
		return "", faultWrap(internalError("cursor filter encoding failed"), err)
	}
	payload := strings.Join([]string{
		cursorPrefix,
		op,
		string(unitScope.InstallationID),
		fmt.Sprintf("%x", sha256.Sum256(filterJSON)),
		lastName,
		strconv.FormatInt(lastVersion, 10),
		strconv.FormatInt(now.Add(cursorTTL).Unix(), 10),
	}, "|")
	mac, err := s.hmac(payload)
	if err != nil {
		return "", err
	}
	return base64url([]byte(payload)) + "." + base64url(mac), nil
}

// readCursor validates a client cursor and returns its keyset position.
func (s *Service) readCursor(op string, unitScope contract.Scope, filter any, raw string) (string, int64, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return "", 0, invalidInput("cursor is malformed")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", 0, invalidInput("cursor is malformed")
	}
	mac, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", 0, invalidInput("cursor is malformed")
	}
	expected, err := s.hmac(string(payload))
	if err != nil || !hmac.Equal(mac, expected) {
		return "", 0, invalidInput("cursor is malformed")
	}
	fields := strings.Split(string(payload), "|")
	if len(fields) != 7 || fields[0] != cursorPrefix || fields[1] != op ||
		fields[2] != string(unitScope.InstallationID) {
		return "", 0, invalidInput("cursor does not belong to this query")
	}
	filterJSON, err := json.Marshal(filter)
	if err != nil {
		return "", 0, faultWrap(internalError("cursor filter encoding failed"), err)
	}
	if fields[3] != fmt.Sprintf("%x", sha256.Sum256(filterJSON)) {
		return "", 0, invalidInput("cursor does not match the current filter")
	}
	expiry, err := strconv.ParseInt(fields[6], 10, 64)
	if err != nil {
		return "", 0, invalidInput("cursor is malformed")
	}
	if s.clock.Now().Unix() >= expiry {
		return "", 0, cursorExpired("cursor has expired")
	}
	version, err := strconv.ParseInt(fields[5], 10, 64)
	if err != nil {
		return "", 0, invalidInput("cursor is malformed")
	}
	return fields[4], version, nil
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
