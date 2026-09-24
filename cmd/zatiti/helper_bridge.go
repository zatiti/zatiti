//go:build darwin

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/zatiti/zatiti/internal/contract"
)

const bridgePrepareSchema = "zatiti.gui-credential-prepare/v1"
const bridgeResultSchema = "zatiti.gui-credential-capture-result/v1"
const bridgeSessionLifetime = 5 * time.Minute

// bridgeDependencies are deliberately injected for tests. Production creates
// them only from protected default discovery and fixed Keychain selectors.
type bridgeDependencies struct {
	stateDir string
	op       contract.Operator
	secrets  contract.SecretStore
	close    func()
}

type bridgeFactory func(context.Context, contract.ID) (bridgeDependencies, error)

type bridgeSession struct {
	installationID contract.ID
	connectionID   contract.ID
	deps           bridgeDependencies
	deadline       time.Time
}

// nativeBridgeResult never carries a credential, receipt or StoreRef. Prepare
// alone carries the process-local handle and the public provider/account label.
type nativeBridgeResult struct {
	Schema          string `json:"schema"`
	Status          string `json:"status"`
	ReasonCode      string `json:"reason_code"`
	Handle          string `json:"handle,omitempty"`
	Provider        string `json:"provider,omitempty"`
	AccountIdentity string `json:"account_identity,omitempty"`
}

type nativeBridge struct {
	mu       sync.Mutex
	sessions map[string]bridgeSession
	factory  bridgeFactory
	now      func() time.Time
}

func newNativeBridge(factory bridgeFactory) *nativeBridge {
	return &nativeBridge{sessions: make(map[string]bridgeSession), factory: factory, now: time.Now}
}

func bridgeFailure(schema, reason string) nativeBridgeResult {
	return nativeBridgeResult{Schema: schema, Status: "repair_required", ReasonCode: reason}
}

func (b *nativeBridge) prepare(ctx context.Context, installationID, connectionID string) nativeBridgeResult {
	if !helperIDPattern.MatchString(installationID) || !helperIDPattern.MatchString(connectionID) {
		return bridgeFailure(bridgePrepareSchema, "invalid_identity")
	}
	b.mu.Lock()
	for handle, session := range b.sessions {
		if handle != "" && !b.now().Before(session.deadline) {
			delete(b.sessions, handle)
			session.deps.close()
		}
	}
	if len(b.sessions) != 0 {
		b.mu.Unlock()
		return nativeBridgeResult{Schema: bridgePrepareSchema, Status: "retryable", ReasonCode: "capture_busy"}
	}
	// Reserve the single slot before opening any Keychain item or socket.
	b.sessions[""] = bridgeSession{}
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.sessions, ""); b.mu.Unlock() }()
	if b.factory == nil {
		return bridgeFailure(bridgePrepareSchema, "helper_unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deps, err := b.factory(ctx, contract.ID(installationID))
	if err != nil {
		return bridgeFailure(bridgePrepareSchema, "local_prerequisite")
	}
	if deps.close == nil {
		deps.close = func() {}
	}
	store, err := newHelperIntentStore(deps.stateDir)
	if err != nil {
		deps.close()
		return bridgeFailure(bridgePrepareSchema, "recovery_state")
	}
	unlock, err := store.lock(connectionID)
	if err != nil {
		deps.close()
		return bridgeFailure(bridgePrepareSchema, "recovery_state")
	}
	intent, conn, done, err := helperPrepareLocked(ctx, store, deps.op, deps.secrets, contract.ID(installationID), contract.ID(connectionID))
	unlock()
	if err != nil {
		deps.close()
		return bridgeFailure(bridgePrepareSchema, "setup_prerequisite")
	}
	if done {
		deps.close()
		return nativeBridgeResult{Schema: bridgePrepareSchema, Status: "completed", ReasonCode: "already_completed"}
	}
	if intent == nil || len(conn.Provider) > 256 || len(conn.AccountIdentity) > 8192 {
		deps.close()
		return bridgeFailure(bridgePrepareSchema, "invalid_label")
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		deps.close()
		return bridgeFailure(bridgePrepareSchema, "helper_unavailable")
	}
	handle := hex.EncodeToString(random[:])
	for i := range random {
		random[i] = 0
	}
	b.mu.Lock()
	b.sessions[handle] = bridgeSession{installationID: contract.ID(installationID), connectionID: contract.ID(connectionID), deps: deps, deadline: b.now().Add(bridgeSessionLifetime)}
	b.mu.Unlock()
	return nativeBridgeResult{Schema: bridgePrepareSchema, Status: "ready", ReasonCode: "none", Handle: handle, Provider: conn.Provider, AccountIdentity: conn.AccountIdentity}
}

func (b *nativeBridge) take(handle string) (bridgeSession, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(handle) != 64 {
		return bridgeSession{}, false
	}
	s, ok := b.sessions[handle]
	if ok {
		delete(b.sessions, handle)
	}
	return s, ok
}

func (b *nativeBridge) commit(ctx context.Context, handle string, credential []byte) nativeBridgeResult {
	s, ok := b.take(handle)
	if !ok {
		return bridgeFailure(bridgeResultSchema, "invalid_handle")
	}
	defer s.deps.close()
	if !b.now().Before(s.deadline) {
		return bridgeFailure(bridgeResultSchema, "capture_expired")
	}
	if len(credential) == 0 || len(credential) > maxHelperCredentialBytes || !utf8.Valid(credential) || bytes.IndexByte(credential, '\n') >= 0 || bytes.IndexByte(credential, '\r') >= 0 {
		return bridgeFailure(bridgeResultSchema, "invalid_credential")
	}
	// The C wrapper copies no more than 4096 bytes into this slice. The
	// reader adds a terminator without creating an extra credential string.
	reader := io.MultiReader(bytes.NewReader(credential), strings.NewReader("\n"))
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := runHelperEngine(ctx, s.deps.stateDir, s.deps.op, s.deps.secrets, reader, io.Discard, s.installationID, s.connectionID); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nativeBridgeResult{Schema: bridgeResultSchema, Status: "retryable", ReasonCode: "outcome_unknown"}
		}
		// The durable intent is authoritative; a later Prepare reconciles it.
		return nativeBridgeResult{Schema: bridgeResultSchema, Status: "retryable", ReasonCode: "inspect_setup_status"}
	}
	return nativeBridgeResult{Schema: bridgeResultSchema, Status: "completed", ReasonCode: "none"}
}

func (b *nativeBridge) cancel(handle string) nativeBridgeResult {
	s, ok := b.take(handle)
	if !ok {
		return bridgeFailure(bridgeResultSchema, "invalid_handle")
	}
	s.deps.close()
	// The challenge/intent is retained. The next explicit Prepare reconciles
	// it; cancellation never starts a second challenge or deletes a key.
	return nativeBridgeResult{Schema: bridgeResultSchema, Status: "cancelled", ReasonCode: "user_cancelled"}
}

func (b *nativeBridge) active() int { b.mu.Lock(); defer b.mu.Unlock(); return len(b.sessions) }
