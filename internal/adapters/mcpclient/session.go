package mcpclient

import (
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxSessions bounds the in-memory session table. This adapter has no
// IDSource, so it never fabricates a durable session identity; sessions
// live only as long as this process and this map. A profile's normal
// lifecycle (open_session, use, close_session) keeps entries below this
// bound by construction -- hitting it means a caller is leaking sessions,
// which fails closed rather than growing without limit.
const maxSessions = 65536

// sessionEntry pairs a live SDK ClientSession with the callRoundTripper its
// http.Client was built on (so later calls can arm/disarm it) and the
// session_state the evidence reports (stateless vs active): a stateless
// server's session is not durable server-side, so a subsequent request
// against it may find no server-side session even though this adapter's
// handle is still live.
type sessionEntry struct {
	mu            sync.Mutex
	credentialRef string
	session       *mcp.ClientSession
	roundTripper  *callRoundTripper
	stateless     bool
}

// sessionTable is the bounded, mutex-protected map from opaque
// MCPSessionHandle to the live SDK session it names. The raw Mcp-Session-Id
// the SDK negotiates never leaves this table: only the handle this adapter
// mints is ever returned to a caller.
type sessionTable struct {
	mu      sync.Mutex
	entries map[string]*sessionEntry
}

func newSessionTable() *sessionTable {
	return &sessionTable{entries: make(map[string]*sessionEntry)}
}

// newSessionHandle mints a fresh opaque handle. It never derives from or
// exposes the SDK's own session ID.
func newSessionHandle() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", internalError("mcpclient: generating a session handle failed: %v", err)
	}
	return hex.EncodeToString(buf), nil
}

// add stores entry under a freshly minted handle, refusing once the table
// is at its bound.
func (t *sessionTable) add(entry *sessionEntry) (string, error) {
	handle, err := newSessionHandle()
	if err != nil {
		return "", err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.entries) >= maxSessions {
		return "", internalError("mcpclient: session table is at its bound (%d); close unused sessions before opening more", maxSessions)
	}
	t.entries[handle] = entry
	return handle, nil
}

// get looks up handle. The second return is false for an unknown handle:
// after a controller restart, an expired handle, or one this process never
// minted, and is reported prerequisite_missing, never re-derived.
func (t *sessionTable) get(handle string) (*sessionEntry, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[handle]
	return e, ok
}

// remove drops handle unconditionally. Used both when close_session
// succeeds and when it fails: "the local handle is dropped either way."
func (t *sessionTable) remove(handle string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, handle)
}
