package evidence

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

// Command lifecycle events. Emitted once, by finish(), in the same
// transaction as the terminal disposition it correlates to. Data stays
// empty, following the established sibling convention (effects emits
// transition events with no Data): readers re-fetch the command's own state
// through command.get rather than trusting an event payload to carry it,
// so there is nothing here that redaction would ever need to touch.
const (
	eventCommandCompleted = "evidence.command.completed"
	eventCommandAccepted  = "evidence.command.accepted"
	eventCommandFailed    = "evidence.command.failed"
)

// commandEventKind maps a finished command's status to its transition event
// kind.
func commandEventKind(status string) string {
	switch status {
	case contract.StatusCompleted:
		return eventCommandCompleted
	case contract.StatusAccepted:
		return eventCommandAccepted
	default:
		return eventCommandFailed
	}
}

// Opaque event.list cursors.
//
// A cursor binds the operation, installation scope, serialized filter and
// last emitted sequence under an HMAC, and carries an absolute expiry. A
// tampered, foreign or filter-mismatched cursor is invalid_input; an expired
// one is cursor_expired with snapshot_required=true, so clients re-read a
// fresh snapshot instead of retrying a stale position — an event replay
// never silently presents a gap as complete history.

const (
	eventCursorTTL    = 15 * time.Minute
	eventCursorPrefix = "v1"
)

// cursorKeyLocked returns the lazily minted cursor authentication key. The
// caller must hold cursorMu. A process restart invalidates outstanding
// cursors, which clients observe as cursor_expired.
func (s *Service) cursorKeyLocked() ([]byte, error) {
	if len(s.cursorKey) == 0 {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("evidence: mint cursor key: %w", err)
		}
		s.cursorKey = key
	}
	return s.cursorKey, nil
}

// mintEventCursor seals the last emitted sequence for one scope and filter.
func (s *Service) mintEventCursor(scope contract.Scope, filter eventFilter, lastSequence int64, now time.Time) (string, error) {
	filterJSON, err := json.Marshal(filter)
	if err != nil {
		return "", fmt.Errorf("evidence: bind cursor filter: %w", err)
	}
	scopeJSON, err := json.Marshal(scope)
	if err != nil {
		return "", fmt.Errorf("evidence: bind cursor scope: %w", err)
	}
	payload := strings.Join([]string{
		eventCursorPrefix,
		opEventList,
		fmt.Sprintf("%x", sha256.Sum256(scopeJSON)),
		fmt.Sprintf("%x", sha256.Sum256(filterJSON)),
		strconv.FormatInt(lastSequence, 10),
		strconv.FormatInt(now.Add(eventCursorTTL).Unix(), 10),
	}, "|")
	mac, err := s.hmac(payload)
	if err != nil {
		return "", err
	}
	return base64url([]byte(payload)) + "." + base64url(mac), nil
}

// readEventCursor validates a client cursor and returns its bound sequence
// position.
func (s *Service) readEventCursor(scope contract.Scope, filter eventFilter, raw string) (int64, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return 0, invalidInput("cursor is malformed")
	}
	payload, perr := base64.RawURLEncoding.DecodeString(parts[0])
	if perr != nil {
		return 0, invalidInput("cursor is malformed")
	}
	mac, merr := base64urlDecode(parts[1])
	if merr != nil {
		return 0, invalidInput("cursor is malformed")
	}
	expected, herr := s.hmac(string(payload))
	if herr != nil || !hmac.Equal(mac, expected) {
		return 0, invalidInput("cursor is malformed")
	}
	fields := strings.Split(string(payload), "|")
	if len(fields) != 6 || fields[0] != eventCursorPrefix || fields[1] != opEventList {
		return 0, invalidInput("cursor does not belong to this query")
	}
	scopeJSON, err := json.Marshal(scope)
	if err != nil {
		return 0, fmt.Errorf("evidence: bind cursor scope: %w", err)
	}
	if fields[2] != fmt.Sprintf("%x", sha256.Sum256(scopeJSON)) {
		return 0, invalidInput("cursor does not match the current scope")
	}
	filterJSON, err := json.Marshal(filter)
	if err != nil {
		return 0, fmt.Errorf("evidence: bind cursor filter: %w", err)
	}
	if fields[3] != fmt.Sprintf("%x", sha256.Sum256(filterJSON)) {
		return 0, invalidInput("cursor does not match the current filter")
	}
	lastSequence, cerr := strconv.ParseInt(fields[4], 10, 64)
	if cerr != nil {
		return 0, invalidInput("cursor is malformed")
	}
	expiry, cerr := strconv.ParseInt(fields[5], 10, 64)
	if cerr != nil {
		return 0, invalidInput("cursor is malformed")
	}
	if s.deps.Clock.Now().Unix() >= expiry {
		return 0, cursorExpired()
	}
	return lastSequence, nil
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

// Event payload redaction, applied at exposure time (event.get/event.list),
// never at persistence time: storage already committed the exact bytes a
// handler emitted (see storage's Events doc — redaction happens "before
// exposure", is "authorized by evidence owner"), and this package has no
// hook into another owner's Emit call to redact any earlier. Arbitrary
// secret recognition is not claimed; this is a conservative, denylisted key
// match applied to every object key at any depth, "where possible" rather
// than a guarantee.

var redactedKeys = map[string]bool{
	"secret":        true,
	"secrets":       true,
	"password":      true,
	"token":         true,
	"credential":    true,
	"credentials":   true,
	"api_key":       true,
	"apikey":        true,
	"private_key":   true,
	"access_key":    true,
	"client_secret": true,
	"authorization": true,
}

const redactedPlaceholder = "[REDACTED]"

// redactJSON returns a copy of raw with the value of every object key that
// case-insensitively matches the redaction denylist replaced by a fixed
// placeholder, at any nesting depth. Empty or unparsable input becomes an
// empty object, matching the schema's non-nullable object requirement for
// event data and never leaking unparsable bytes verbatim.
func redactJSON(raw json.RawMessage) json.RawMessage {
	if isEmptyOrNullJSON(raw) {
		return json.RawMessage(`{}`)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return json.RawMessage(`{}`)
	}
	out, err := json.Marshal(redactValue(v))
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(out)
}

func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if redactedKeys[strings.ToLower(k)] {
				out[k] = redactedPlaceholder
				continue
			}
			out[k] = redactValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, el := range t {
			out[i] = redactValue(el)
		}
		return out
	default:
		return v
	}
}
