package mcpclient

import (
	"encoding/json"
	"strings"
)

const redactedPlaceholder = "[redacted]"

// scrubSecret returns s with every occurrence of secret replaced. Request
// building and response interpretation never thread the raw credential
// through evidence or fault text in the first place; this is a
// defense-in-depth backstop applied to any diagnostic text derived from
// provider bytes (for example a JSON-RPC error message that happens to echo
// request content) before it can reach a Fault or evidence document.
func scrubSecret(secret []byte, s string) string {
	if len(secret) == 0 || s == "" {
		return s
	}
	return strings.ReplaceAll(s, string(secret), redactedPlaceholder)
}

// truncateText bounds s to at most n bytes, used to keep provider-supplied
// text (error messages, diagnostics) within the schema's field limits.
func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (s *callState) scrub(text string) string {
	text = scrubSecret(s.secret, text)
	for _, id := range s.sessionIDs {
		text = scrubSecret([]byte(id), text)
	}
	return text
}

// Decode strings before checking so JSON escaping cannot hide reflected secrets.
func (s *callState) containsSensitive(doc []byte) bool {
	var value any
	if json.Unmarshal(doc, &value) != nil {
		return s.scrub(string(doc)) != string(doc)
	}
	var check func(any) bool
	check = func(v any) bool {
		switch x := v.(type) {
		case string:
			return s.scrub(x) != x
		case []any:
			for _, e := range x {
				if check(e) {
					return true
				}
			}
		case map[string]any:
			for k, e := range x {
				if check(k) || check(e) {
					return true
				}
			}
		}
		return false
	}
	return check(value)
}

func (a *Adapter) buildSafeEvidence(ev *wireMCPEvidence, state *callState) (json.RawMessage, json.RawMessage, error) {
	ev.PhysicalCall.ErrorMessage = truncateText(state.scrub(ev.PhysicalCall.ErrorMessage), 2048)
	// Catalog and server metadata are provider-controlled. Suppress confidential
	// material without rewriting schemas or claiming a modified schema was observed.
	metadata, _ := json.Marshal(struct {
		Name, Version, Protocol, Cursor string
		Capabilities                    []string
		Tools                           []wireDiscoveredTool
	}{ev.ServerName, ev.ServerVersion, ev.ProtocolVersion, ev.NextCursor, ev.ServerCapabilities, ev.Tools})
	if state.containsSensitive(metadata) {
		ev.ServerName, ev.ServerVersion, ev.ProtocolVersion, ev.NextCursor = "", "", "", ""
		ev.ServerCapabilities, ev.Tools = nil, nil
		ev.PhysicalCall.Confirmation = "authoritative_failure"
		ev.PhysicalCall.ErrorCode = "response_redacted"
		ev.PhysicalCall.ErrorMessage = "provider metadata contains confidential transport material and was omitted"
	}
	doc, usage, err := a.buildEvidence(*ev)
	if err != nil {
		return nil, nil, err
	}
	if state.containsSensitive(doc) {
		return nil, nil, internalError("mcpclient: evidence contains confidential transport material")
	}
	return doc, usage, nil
}
