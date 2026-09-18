package installation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// ownerCredentialPattern is the custodied form: the complete Authorization
// header value, the Bearer scheme followed by 32 random bytes as unpadded
// base64url (43 token68 characters).
var ownerCredentialPattern = regexp.MustCompile(`^Bearer [A-Za-z0-9_-]{43}$`)

// custodiedOwnerSecret returns the bytes bootstrap custodied, located
// through the store_ref handed to identity.
func custodiedOwnerSecret(t *testing.T, e *testEnv) (string, []byte) {
	t.Helper()
	calls := e.ports.callsOf(peerIdentityBootstrap)
	if len(calls) == 0 {
		t.Fatalf("identity.bootstrap was never called")
	}
	var in identityBootstrapInput
	if err := json.Unmarshal(calls[len(calls)-1].Input, &in); err != nil {
		t.Fatalf("decode identity.bootstrap input: %v", err)
	}
	secret, err := e.secrets.Get(e.ctx, in.StoreRef)
	if err != nil {
		t.Fatalf("owner secret is not custodied under %q: %v", in.StoreRef, err)
	}
	return in.StoreRef, secret
}

// TestOwnerCredentialAuthenticatesAsAnAuthorizationHeader: identity verifies
// the SHA-256 of the custodied bytes against the SHA-256 of the verbatim
// Authorization header value, so the custodied bytes must themselves be a
// legal header value that survives a real HTTP exchange unchanged.
func TestOwnerCredentialAuthenticatesAsAnAuthorizationHeader(t *testing.T) {
	e := newEnv(t)
	e.mustBootstrap()
	_, secret := custodiedOwnerSecret(t, e)
	if !ownerCredentialPattern.Match(secret) {
		t.Fatalf("custodied owner credential is not of the form %s (length %d)", ownerCredentialPattern, len(secret))
	}
	token, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(string(secret), "Bearer "))
	if err != nil || len(token) != 32 {
		t.Fatalf("owner credential token decodes to %d bytes (%v), want 32 random bytes", len(token), err)
	}

	var presented contract.Digest
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		presented = contract.Hash([]byte(r.Header.Get("Authorization")))
	}))
	defer srv.Close()
	req, err := http.NewRequest(http.MethodPost, srv.URL, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Authorization", string(secret))
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("the custodied credential is not a sendable Authorization header value: %v", err)
	}
	_ = resp.Body.Close()
	if want := contract.Hash(secret); presented != want {
		t.Fatalf("header digest %s differs from the custodied digest %s identity stores", presented, want)
	}
}

// TestMintOwnerCredentialIsAlwaysHeaderLegalAndFresh: the raw-byte defect was
// probabilistic, so the mint is exercised many times.
func TestMintOwnerCredentialIsAlwaysHeaderLegalAndFresh(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 512; i++ {
		secret, err := mintOwnerCredential()
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if !ownerCredentialPattern.Match(secret) {
			t.Fatalf("mint %d is not of the form %s", i, ownerCredentialPattern)
		}
		if seen[string(secret)] {
			t.Fatalf("mint %d repeated an earlier credential", i)
		}
		seen[string(secret)] = true
	}
}

// TestOwnerCredentialRefIsAvailableToLocalAssemblyOnly: the store reference
// of a completed bootstrap is readable through the Go API the entrypoint
// uses to provision the local operator's credential profile, and resolves to
// the custodied credential. It is not available before bootstrap or for an
// attempt that never committed.
func TestOwnerCredentialRefIsAvailableToLocalAssemblyOnly(t *testing.T) {
	e := newEnv(t)
	scope := contract.Scope{InstallationID: e.install}
	readRef := func() (OwnerCredential, error) {
		var out OwnerCredential
		err := e.db.Read(e.ctx, e.actor, scope, func(unit contract.Unit) error {
			got, err := e.svc.OwnerCredential(e.ctx, unit)
			out = got
			return err
		})
		return out, err
	}

	if _, err := readRef(); faultCode(err) != contract.CodePrerequisiteMissing {
		t.Fatalf("before bootstrap: err = %v, want prerequisite_missing", err)
	}

	e.ports.set(peerConfigurationBootstrap, func(contract.Invocation) (contract.Payload, error) {
		return failPayload(contract.CodeConflict, "configuration refused")
	})
	if _, err := e.bootstrap(contract.Scope{InstallationID: e.ids.New()}, initInput{CredentialStore: "os", OwnerName: "Doomed Owner"}); err == nil {
		t.Fatalf("expected the doomed attempt to fail")
	}
	if _, err := readRef(); faultCode(err) != contract.CodePrerequisiteMissing {
		t.Fatalf("after a doomed attempt: err = %v, want prerequisite_missing", err)
	}
	delete(e.ports.handlers, peerConfigurationBootstrap)

	st := e.mustBootstrap()
	storeRef, secret := custodiedOwnerSecret(t, e)
	got, err := readRef()
	if err != nil {
		t.Fatalf("after bootstrap: %v", err)
	}
	if got.StoreRef != storeRef {
		t.Fatalf("store ref = %q, want the reference identity was given %q", got.StoreRef, storeRef)
	}
	if got.InstallationID != st.InstallationID || got.OwnerID == "" || got.CredentialID == "" {
		t.Fatalf("owner credential metadata = %+v, want installation %s with owner and credential identities", got, st.InstallationID)
	}
	resolved, err := e.secrets.Get(e.ctx, got.StoreRef)
	if err != nil || !bytes.Equal(resolved, secret) {
		t.Fatalf("store ref does not resolve to the custodied owner credential: %v", err)
	}
	if raw, _ := json.Marshal(got); bytes.Contains(raw, secret) {
		t.Fatalf("owner credential metadata carries the secret")
	}
}

func faultCode(err error) string {
	var fault *contract.Fault
	if errors.As(err, &fault) {
		return fault.Code
	}
	return ""
}

// echoingSecrets is a hostile secret store whose Put error repeats the
// secret it was given, as a careless helper process error might.
type echoingSecrets struct{ *fakeSecrets }

func (s echoingSecrets) Put(_ context.Context, _ string, secret []byte) (string, error) {
	return "", fmt.Errorf("helper failed: argv=%s", secret)
}

// TestOwnerSecretNeverLeavesCustody: the owner secret appears in no
// operation result, peer call, event, log record or error — on the success
// path, when a peer refuses after custody, and when custody itself fails
// with an error that echoes the secret.
func TestOwnerSecretNeverLeavesCustody(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	e := newEnv(t)
	var surfaces []string
	record := func(label string, v any) {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal %s: %v", label, err)
		}
		surfaces = append(surfaces, label+": "+string(raw))
	}

	// A peer refuses after the secret was custodied.
	e.ports.set(peerMemoryBootstrap, func(contract.Invocation) (contract.Payload, error) {
		return failPayload(contract.CodeConflict, "memory refused")
	})
	_, doomedErr := e.bootstrap(contract.Scope{InstallationID: e.ids.New()}, initInput{CredentialStore: "os", OwnerName: "Doomed Owner"})
	if doomedErr == nil {
		t.Fatalf("expected the doomed attempt to fail")
	}
	_, doomedSecret := custodiedOwnerSecret(t, e)
	surfaces = append(surfaces, "doomed error: "+doomedErr.Error())
	delete(e.ports.handlers, peerMemoryBootstrap)

	// The success path.
	scope := contract.Scope{InstallationID: e.install}
	payload, err := e.driveLocalIOScope(opInit, initInput{CredentialStore: "os", OwnerName: "Ada Owner"}, scope, true)
	if err != nil || payload.Status != contract.StatusCompleted {
		t.Fatalf("bootstrap: %v (status %q)", err, payload.Status)
	}
	_, secret := custodiedOwnerSecret(t, e)
	record("result", payload)
	for _, call := range e.ports.calls {
		record("peer call "+call.Operation, call)
	}
	events, err := e.db.Events(e.ctx, 0, 500)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("bootstrap emitted no events to inspect")
	}
	record("events", events)
	var ref OwnerCredential
	if err := e.db.Read(e.ctx, e.actor, scope, func(unit contract.Unit) error {
		got, err := e.svc.OwnerCredential(e.ctx, unit)
		ref = got
		return err
	}); err != nil {
		t.Fatalf("owner credential metadata: %v", err)
	}
	record("owner credential metadata", ref)

	// Custody itself fails, and the store's error echoes the secret.
	hostile := newEnv(t)
	hostileSvc, err := New(contract.Dependencies{
		Clock: hostile.clock, IDs: hostile.ids, Ports: hostile.ports,
		Secrets: echoingSecrets{hostile.secrets}, Blobs: hostile.blobs,
	})
	if err != nil {
		t.Fatalf("New with hostile secrets: %v", err)
	}
	hostile.svc = hostileSvc
	hostilePayload, hostileErr := hostile.driveLocalIOScope(opInit, initInput{CredentialStore: "os", OwnerName: "Ada Owner"},
		contract.Scope{InstallationID: hostile.install}, true)
	if hostileErr == nil && hostilePayload.Status != contract.StatusFailed {
		t.Fatalf("bootstrap with a failing secret store must fail, got status %q", hostilePayload.Status)
	}
	if hostileErr != nil {
		surfaces = append(surfaces, "custody error: "+hostileErr.Error())
	}
	record("custody failure result", hostilePayload)
	if fault := hostilePayload.Error; fault == nil || fault.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("custody failure fault = %+v, want prerequisite_missing", fault)
	}
	surfaces = append(surfaces, "logs: "+logs.String())

	// No surface may carry a Bearer credential at all (this covers the
	// hostile store's secret, which the test never sees), nor either known
	// secret in whole or as its bare token.
	anyCredential := regexp.MustCompile(`Bearer [A-Za-z0-9_-]{43}`)
	for _, surface := range surfaces {
		if anyCredential.MatchString(surface) {
			t.Fatalf("a bearer credential leaked into %.60s...", surface)
		}
		for _, s := range [][]byte{secret, doomedSecret} {
			token := strings.TrimPrefix(string(s), "Bearer ")
			if strings.Contains(surface, token) {
				t.Fatalf("the owner secret leaked into %.60s...", surface)
			}
		}
	}
}
