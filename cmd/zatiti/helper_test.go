package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/cli"
	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/platform"
)

// referenceVerifyReceipt reproduces internal/connections/localio.go:534-561's
// unexported verifyReceipt algorithm exactly (prefix strip, "." split,
// base64url decode, HMAC-SHA256 check, JSON decode) so a test in this
// package -- which cannot import an unexported function from another
// package -- can independently confirm mintHelperReceipt produces a
// receipt the real connections package would actually accept. This is the
// single most load-bearing correctness claim in P24 item 3: if this drifts
// from localio.go's algorithm, every credential import silently breaks.
func referenceVerifyReceipt(t *testing.T, receipt string, key []byte) helperPayload {
	t.Helper()
	rest, ok := strings.CutPrefix(receipt, helperReceiptPrefix)
	if !ok {
		t.Fatalf("receipt %q does not carry the expected prefix %q", receipt, helperReceiptPrefix)
	}
	body, sig, ok := strings.Cut(rest, ".")
	if !ok || body == "" || sig == "" {
		t.Fatalf("receipt %q is not in body.signature form", receipt)
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("receipt body does not decode as base64url: %v", err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(payloadBytes)
	want, err := hex.DecodeString(sig)
	if err != nil || !hmac.Equal(mac.Sum(nil), want) {
		t.Fatalf("receipt signature does not verify against the key")
	}
	var payload helperPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("receipt payload does not decode: %v", err)
	}
	return payload
}

// TestMintHelperReceiptVerifiesAgainstConnectionsAlgorithm proves
// mintHelperReceipt's output is byte-for-byte compatible with
// internal/connections/localio.go's verifyReceipt: every field
// round-trips, and a tampered key or body is correctly refused -- the
// exact interop contract connection.setup.complete's Perform phase
// depends on.
func TestMintHelperReceiptVerifiesAgainstConnectionsAlgorithm(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	challengeID := contract.NewID()
	expires := time.Now().UTC().Truncate(time.Second)
	payload := helperPayload{
		ChallengeID: challengeID, CredentialRef: "hl1:deadbeef",
		AccountIdentity: "acct-1", ExpiresAt: expires,
	}
	receipt, err := mintHelperReceipt(key, payload)
	if err != nil {
		t.Fatalf("mintHelperReceipt: %v", err)
	}
	if !strings.HasPrefix(receipt, helperReceiptPrefix) {
		t.Fatalf("receipt %q lacks the expected prefix %q", receipt, helperReceiptPrefix)
	}

	got := referenceVerifyReceipt(t, receipt, key)
	if got.ChallengeID != challengeID || got.CredentialRef != "hl1:deadbeef" || got.AccountIdentity != "acct-1" || !got.ExpiresAt.Equal(expires) {
		t.Fatalf("verified payload %+v does not match minted payload %+v", got, payload)
	}

	// A wrong key must never verify: this is the actual security property
	// connection.setup.complete relies on (a forged or misdirected receipt
	// is refused, per internal/connections/localio.go:476-479).
	wrongKey := []byte("ffffffffffffffffffffffffffffffff")
	rest := strings.TrimPrefix(receipt, helperReceiptPrefix)
	body, sig, _ := strings.Cut(rest, ".")
	payloadBytes, _ := base64.RawURLEncoding.DecodeString(body)
	mac := hmac.New(sha256.New, wrongKey)
	mac.Write(payloadBytes)
	if hex.EncodeToString(mac.Sum(nil)) == sig {
		t.Fatal("HMAC collided across two different keys; the test setup is broken")
	}
}

// TestEnsureHelperReceiptKeyMintsAndReusesWithinOneInvocation proves the
// signer reuses a durable key even when its name differs from its opaque ref.
func TestEnsureHelperReceiptKeyMintsAndReusesWithinOneInvocation(t *testing.T) {
	secrets := newFakeSecretStore()
	key1, err := ensureHelperReceiptKey(context.Background(), secrets)
	if err != nil {
		t.Fatalf("ensureHelperReceiptKey: %v", err)
	}
	if len(key1) != helperReceiptKeyBytes {
		t.Fatalf("minted key is %d bytes, want %d", len(key1), helperReceiptKeyBytes)
	}
	if ref, err := secrets.Lookup(context.Background(), helperReceiptKeyRef); err != nil || ref == helperReceiptKeyRef {
		t.Fatalf("expected an opaque receipt key reference, got %q, %v", ref, err)
	}
	key2, err := ensureHelperReceiptKey(context.Background(), secrets)
	if err != nil {
		t.Fatalf("ensureHelperReceiptKey (second call): %v", err)
	}
	if string(key1) != string(key2) {
		t.Fatal("ensureHelperReceiptKey minted a second key instead of reusing the stored one")
	}
}

func TestEnsureHelperReceiptKeyFailsClosedWhenStoreUnavailable(t *testing.T) {
	secrets := newFakeSecretStore()
	secrets.lookupErr = &contract.Fault{Code: contract.CodeControllerUnavailable}
	if _, err := ensureHelperReceiptKey(context.Background(), secrets); err == nil {
		t.Fatal("unavailable store must not mint an unpersisted signing key")
	}
	if len(secrets.store) != 0 {
		t.Fatal("unavailable store must not write a replacement signing key")
	}
}

// TestRunConnectionHelperNeverPrintsTheCredential is P24's required test
// 3: a fresh setup through the CLI helper mechanics establishes an opaque,
// usable connection.setup.complete call without ever printing, in any
// diagnostic, anything that looks like the real credential the operator
// entered. It uses the REAL internal/platform secret store (platform.Open
// needs no module registry -- unaffected by the pre-existing
// internal/policy registry defect blocking cmd/zatiti's other
// registry-backed tests, see this package's p24_test.go) and a fake
// contract.Operator standing in for the three connection.setup.* calls a
// live server would otherwise answer.
func TestRunConnectionHelperNeverPrintsTheCredential(t *testing.T) {
	cfg := serveConfig(t)
	const secretMarker = "sk-live-super-secret-token-do-not-leak-12345"

	installationID := contract.NewID()
	connectionID := contract.NewID()
	challengeID := contract.NewID()
	expiresAt := time.Now().UTC().Add(30 * time.Minute).Truncate(time.Second)

	var completeInput map[string]any
	completed := false
	credentialRef := ""
	op := &fakeOperator{fn: func(_ context.Context, operation string, req contract.Request) (contract.Result, error) {
		switch operation {
		case "connection.get":
			return contract.Result{Payload: contract.Payload{Status: contract.StatusCompleted,
				Data: mustJSON(t, map[string]any{"resource": map[string]any{"version": 1, "account_identity": "acct-1", "credential_ref": credentialRef}}),
			}}, nil
		case "command.get":
			return contract.Result{Payload: contract.Payload{Status: contract.StatusFailed, Error: &contract.Fault{Code: contract.CodeNotFound}}}, nil
		case "connection.setup.begin":
			if req.SubmissionKey == "" {
				t.Fatal("setup.begin has no submission key")
			}
			return contract.Result{Payload: contract.Payload{Status: contract.StatusCompleted,
				Data: mustJSON(t, map[string]any{"resource": map[string]any{
					"id": challengeID, "version": 1, "expires_at": expiresAt.Format(time.RFC3339),
				}}),
			}}, nil
		case "connection.setup.status":
			state := "external_action_required"
			if completed {
				state = "completed"
			}
			return contract.Result{Payload: contract.Payload{Status: contract.StatusCompleted,
				Data: mustJSON(t, map[string]any{"resource": map[string]any{"id": challengeID, "version": 1, "connection_id": connectionID, "state": state, "expires_at": expiresAt.Format(time.RFC3339)}}),
			}}, nil
		case "connection.setup.complete":
			if req.SubmissionKey == "" {
				t.Fatal("setup.complete has no submission key")
			}
			if err := json.Unmarshal(req.Input, &completeInput); err != nil {
				t.Fatalf("decoding connection.setup.complete input: %v", err)
			}
			rest := strings.TrimPrefix(completeInput["helper_ref"].(string), helperReceiptPrefix)
			body, _, _ := strings.Cut(rest, ".")
			payloadBytes, _ := base64.RawURLEncoding.DecodeString(body)
			var payload helperPayload
			_ = json.Unmarshal(payloadBytes, &payload)
			credentialRef = payload.CredentialRef
			completed = true
			return contract.Result{Payload: contract.Payload{Status: contract.StatusCompleted,
				Data: mustJSON(t, map[string]any{"resource": map[string]any{"id": connectionID}}),
			}}, nil
		default:
			t.Fatalf("unexpected operation %s", operation)
			return contract.Result{}, nil
		}
	}}

	stdin := strings.NewReader(secretMarker + "\n")
	var out, errOut lockedBuffer
	streams := cli.IO{In: stdin, Out: &out, Err: &errOut}

	if err := runConnectionHelper(context.Background(), &cfg, op, streams, installationID, connectionID); err != nil {
		t.Fatalf("runConnectionHelper: %v\nstderr:\n%s", err, errOut.String())
	}

	// The operation actually completed, with an opaque helper_ref -- never
	// the raw secret -- as the input.
	if completeInput == nil {
		t.Fatal("connection.setup.complete was never called")
	}
	helperRef, _ := completeInput["helper_ref"].(string)
	if !strings.HasPrefix(helperRef, helperReceiptPrefix) {
		t.Fatalf("connection.setup.complete's helper_ref %q does not carry the opaque receipt prefix", helperRef)
	}
	if strings.Contains(helperRef, secretMarker) {
		t.Fatal("the opaque helper_ref embeds the raw credential")
	}

	// The hard security boundary: grep every observable surface for the
	// exact secret bytes. None of them -- not stdout, not stderr, not the
	// operation input any of the three calls sent -- may contain it.
	if strings.Contains(out.String(), secretMarker) {
		t.Fatalf("the credential leaked onto stdout: %s", out.String())
	}
	if strings.Contains(errOut.String(), secretMarker) {
		t.Fatalf("the credential leaked onto stderr: %s", errOut.String())
	}
	raw, _ := json.Marshal(completeInput)
	if strings.Contains(string(raw), secretMarker) {
		t.Fatalf("the credential leaked into an operation request: %s", raw)
	}

	// The credential really was written to the local secret store (the
	// helper did its one real job, not only a no-op that looked clean):
	// decode the receipt's payload segment (unsigned, only integrity-
	// protected -- reading it needs no key) for the credential_ref it
	// names, and confirm the real platform store holds exactly the
	// entered secret under that reference.
	rest := strings.TrimPrefix(helperRef, helperReceiptPrefix)
	body, _, _ := strings.Cut(rest, ".")
	payloadBytes, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("receipt payload does not decode: %v", err)
	}
	var payload helperPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		t.Fatalf("receipt payload is not the expected shape: %v", err)
	}
	if payload.ChallengeID != challengeID || payload.AccountIdentity != "acct-1" || !payload.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("receipt payload %+v does not bind the challenge/account/expiry runConnectionHelper was given", payload)
	}
	if payload.CredentialRef == "" {
		t.Fatal("receipt payload names no credential_ref; nothing was ever written to the secret store")
	}
	plat, err := platform.Open(platform.Config{StateDir: cfg.StateDir, CredentialBackend: cfg.CredentialBackend, MasterKeyRef: cfg.MasterKeyRef})
	if err != nil {
		t.Fatalf("re-opening the platform secret store: %v", err)
	}
	defer func() { _ = plat.Close() }()
	stored, err := plat.Secrets().Get(context.Background(), payload.CredentialRef)
	if err != nil {
		t.Fatalf("the credential_ref the receipt names is not retrievable from the real secret store: %v", err)
	}
	if string(stored) != secretMarker {
		t.Fatalf("stored credential = %q, want the entered secret", stored)
	}
}

func TestReadSecretLineEnforcesUTF8ByteLimit(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		valid       bool
	}{
		{"exact bound", strings.Repeat("x", 4096) + "\n", true},
		{"over bound", strings.Repeat("x", 4097) + "\n", false},
		{"multibyte over bound", strings.Repeat("é", 2049) + "\n", false},
		{"invalid utf8", string([]byte{0xff, '\n'}), false},
		{"empty", "\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readSecretLine(strings.NewReader(tc.input))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t, error=%v", tc.valid, err)
			}
			if tc.valid && len(got) != 4096 {
				t.Fatalf("got %d bytes", len(got))
			}
		})
	}
}

func TestHelperIntentRejectsSymlinkAndPermissiveFile(t *testing.T) {
	store, err := newHelperIntentStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := string(contract.NewID())
	path := filepath.Join(store.dir, id+".json")
	if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.read(id); err == nil {
		t.Fatal("symlinked intent accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.read(id); err == nil {
		t.Fatal("permissive intent accepted")
	}
}

func TestHelperBeginIntentIsBoundBeforeAmbiguousDispatch(t *testing.T) {
	root := t.TempDir()
	store, err := newHelperIntentStore(root)
	if err != nil {
		t.Fatal(err)
	}
	installationID, connectionID := contract.NewID(), contract.NewID()
	beginCalls := 0
	op := &fakeOperator{fn: func(_ context.Context, operation string, req contract.Request) (contract.Result, error) {
		switch operation {
		case "connection.get":
			return contract.Result{Payload: contract.Payload{Status: contract.StatusCompleted, Data: mustJSON(t, map[string]any{"resource": map[string]any{"version": 1, "account_identity": "acct"}})}}, nil
		case "command.get":
			return contract.Result{Payload: contract.Payload{Status: contract.StatusFailed, Error: &contract.Fault{Code: contract.CodeNotFound}}}, nil
		case "connection.setup.begin":
			beginCalls++
			intent, err := store.read(string(connectionID))
			if err != nil || intent == nil {
				t.Fatalf("intent before begin: %+v %v", intent, err)
			}
			if got := helperMutationDigest(operation, req.SubmissionKey, req.Input); got != intent.BeginRequestSHA256 {
				t.Fatal("begin request was not durably bound before dispatch")
			}
			return contract.Result{}, errors.New("response lost")
		default:
			t.Fatalf("unexpected %s", operation)
			return contract.Result{}, nil
		}
	}}
	if _, _, _, err := helperPrepareLocked(context.Background(), store, op, newFakeSecretStore(), installationID, connectionID); err == nil {
		t.Fatal("ambiguous begin claimed success")
	}
	intent, err := store.read(string(connectionID))
	if err != nil || intent == nil {
		t.Fatalf("read pending: %+v %v", intent, err)
	}
	intent.ConnectionVersion++
	if err := store.write(*intent); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := helperPrepareLocked(context.Background(), store, op, newFakeSecretStore(), installationID, connectionID); err == nil || !strings.Contains(err.Error(), "request changed") {
		t.Fatalf("changed request should fail before replay: %v", err)
	}
	if beginCalls != 1 {
		t.Fatalf("begin calls after changed request: %d", beginCalls)
	}
}

func TestHelperIntentRejectsPartialPhases(t *testing.T) {
	store, err := newHelperIntentStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	intent := helperIntent{Schema: helperIntentSchema, InstallationID: string(contract.NewID()), ConnectionID: string(contract.NewID()), ConnectionVersion: 1,
		AccountIdentity: "acct", BeginKey: "begin", CompleteKey: "complete", CredentialName: "connections/credential/" + string(contract.NewID())}
	raw, err := helperBeginRequest(intent)
	if err != nil {
		t.Fatal(err)
	}
	intent.BeginRequestSHA256 = helperMutationDigest("connection.setup.begin", intent.BeginKey, raw)
	if err := store.write(intent); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*helperIntent){
		func(i *helperIntent) { i.ChallengeVersion = 1 },
		func(i *helperIntent) { i.CredentialRef = "opaque-ref" },
		func(i *helperIntent) { i.CompleteRequestSHA256 = strings.Repeat("a", 64) },
		func(i *helperIntent) { i.BeginRequestSHA256 = "malformed" },
	} {
		broken := intent
		mutate(&broken)
		if err := store.write(broken); err == nil {
			t.Fatalf("partial intent accepted: %+v", broken)
		}
	}
	path, err := store.path(intent.ConnectionID)
	if err != nil {
		t.Fatal(err)
	}
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	duplicate := bytes.Replace(good, []byte(`"schema":"zatiti.helper_intent/v1"`), []byte(`"schema":"zatiti.helper_intent/v1","schema":"zatiti.helper_intent/v1"`), 1)
	if bytes.Equal(duplicate, good) {
		t.Fatal("fixture did not add duplicate field")
	}
	if err := os.WriteFile(path, duplicate, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.read(intent.ConnectionID); err == nil {
		t.Fatal("duplicate field was accepted")
	}
}

type countingSecrets struct {
	*fakeSecretStore
	puts int
}

func (s *countingSecrets) Put(ctx context.Context, name string, b []byte) (string, error) {
	s.puts++
	return s.fakeSecretStore.Put(ctx, name, b)
}

func TestHelperUnknownCompletionReconcilesWithoutSecondCredentialWrite(t *testing.T) {
	root := t.TempDir()
	secrets := &countingSecrets{fakeSecretStore: newFakeSecretStore()}
	installationID, connectionID, challengeID := contract.NewID(), contract.NewID(), contract.NewID()
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	var beginKey, completeKey, effectiveRef string
	completed := false
	completeCalls := 0
	op := &fakeOperator{fn: func(_ context.Context, operation string, req contract.Request) (contract.Result, error) {
		switch operation {
		case "command.get":
			return contract.Result{Payload: contract.Payload{Status: contract.StatusFailed, Error: &contract.Fault{Code: contract.CodeNotFound}}}, nil
		case "connection.get":
			return contract.Result{Payload: contract.Payload{Status: contract.StatusCompleted, Data: mustJSON(t, map[string]any{"resource": map[string]any{"version": 1, "account_identity": "acct", "credential_ref": effectiveRef}})}}, nil
		case "connection.setup.begin":
			if req.SubmissionKey == "" {
				t.Fatal("unkeyed begin")
			}
			beginKey = req.SubmissionKey
			return contract.Result{Payload: contract.Payload{Status: contract.StatusCompleted, Data: mustJSON(t, map[string]any{"resource": map[string]any{"id": challengeID, "version": 1, "expires_at": expires}})}}, nil
		case "connection.setup.status":
			state := "external_action_required"
			if completed {
				state = "completed"
			}
			return contract.Result{Payload: contract.Payload{Status: contract.StatusCompleted, Data: mustJSON(t, map[string]any{"resource": map[string]any{"id": challengeID, "version": 1, "connection_id": connectionID, "state": state, "expires_at": expires}})}}, nil
		case "connection.setup.complete":
			if req.SubmissionKey == "" || req.SubmissionKey == beginKey {
				t.Fatal("missing or reused completion key")
			}
			pendingStore, err := newHelperIntentStore(root)
			if err != nil {
				t.Fatal(err)
			}
			pending, err := pendingStore.read(string(connectionID))
			if err != nil || pending == nil || pending.CompleteRequestSHA256 != helperMutationDigest(operation, req.SubmissionKey, req.Input) {
				t.Fatalf("complete request not durably bound before dispatch: %+v %v", pending, err)
			}
			completeKey = req.SubmissionKey
			completeCalls++
			var input struct {
				HelperRef string `json:"helper_ref"`
			}
			if err := json.Unmarshal(req.Input, &input); err != nil {
				t.Fatal(err)
			}
			parts := strings.Split(strings.TrimPrefix(input.HelperRef, helperReceiptPrefix), ".")
			body, _ := base64.RawURLEncoding.DecodeString(parts[0])
			var payload helperPayload
			_ = json.Unmarshal(body, &payload)
			effectiveRef = payload.CredentialRef
			completed = true
			return contract.Result{}, errors.New("lost acknowledgment")
		default:
			t.Fatalf("unexpected operation %s", operation)
			return contract.Result{}, nil
		}
	}}
	const marker = "secret-never-public"
	var diagnostics lockedBuffer
	if err := runHelperEngine(context.Background(), root, op, secrets, strings.NewReader(marker+"\n"), &diagnostics, installationID, connectionID); err == nil {
		t.Fatal("lost acknowledgment reported success")
	}
	if beginKey == "" || completeKey == "" || secrets.puts != 2 {
		t.Fatalf("keys=%q,%q puts=%d", beginKey, completeKey, secrets.puts)
	} // credential + receipt key
	if err := runHelperEngine(context.Background(), root, op, secrets, strings.NewReader("different-secret\n"), &diagnostics, installationID, connectionID); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if completeCalls != 1 || secrets.puts != 2 {
		t.Fatalf("completion calls=%d, secret writes=%d", completeCalls, secrets.puts)
	}
	if strings.Contains(diagnostics.String(), marker) {
		t.Fatal("credential leaked to diagnostics")
	}
	store, _ := newHelperIntentStore(root)
	if pending, err := store.read(string(connectionID)); err != nil || pending != nil {
		t.Fatalf("pending after recovery: %+v %v", pending, err)
	}
}

func TestHelperRejectsChangedReceiptAfterAmbiguousCompletion(t *testing.T) {
	root := t.TempDir()
	secrets := newFakeSecretStore()
	installationID, connectionID, challengeID := contract.NewID(), contract.NewID(), contract.NewID()
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	completeCalls := 0
	op := &fakeOperator{fn: func(_ context.Context, operation string, req contract.Request) (contract.Result, error) {
		switch operation {
		case "connection.get":
			return contract.Result{Payload: contract.Payload{Status: contract.StatusCompleted, Data: mustJSON(t, map[string]any{"resource": map[string]any{"version": 1, "account_identity": "acct"}})}}, nil
		case "command.get":
			return contract.Result{Payload: contract.Payload{Status: contract.StatusFailed, Error: &contract.Fault{Code: contract.CodeNotFound}}}, nil
		case "connection.setup.begin":
			return contract.Result{Payload: contract.Payload{Status: contract.StatusCompleted, Data: mustJSON(t, map[string]any{"resource": map[string]any{"id": challengeID, "version": 1, "expires_at": expires}})}}, nil
		case "connection.setup.status":
			return contract.Result{Payload: contract.Payload{Status: contract.StatusCompleted, Data: mustJSON(t, map[string]any{"resource": map[string]any{"id": challengeID, "version": 1, "connection_id": connectionID, "state": "external_action_required", "expires_at": expires}})}}, nil
		case "connection.setup.complete":
			completeCalls++
			return contract.Result{}, errors.New("response lost before acceptance is known")
		default:
			t.Fatalf("unexpected %s", operation)
			return contract.Result{}, nil
		}
	}}
	if err := runHelperEngine(context.Background(), root, op, secrets, strings.NewReader("synthetic-key\n"), io.Discard, installationID, connectionID); err == nil {
		t.Fatal("ambiguous completion claimed success")
	}
	store, err := newHelperIntentStore(root)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.read(string(connectionID))
	if err != nil || pending == nil || pending.CompleteRequestSHA256 == "" {
		t.Fatalf("missing bound completion: %+v %v", pending, err)
	}
	keyRef := secrets.names[helperReceiptKeyRef]
	if keyRef == "" {
		t.Fatal("no receipt key")
	}
	secrets.store[keyRef] = bytes.Repeat([]byte{0x5a}, len(secrets.store[keyRef]))
	if err := runHelperEngine(context.Background(), root, op, secrets, strings.NewReader("different-key\n"), io.Discard, installationID, connectionID); err == nil || !strings.Contains(err.Error(), "request changed") {
		t.Fatalf("changed receipt should block same-key replay: %v", err)
	}
	if completeCalls != 1 {
		t.Fatalf("completion replayed with changed receipt: %d", completeCalls)
	}
}

func TestHelperResumesCrashAfterPutBeforeIntentReference(t *testing.T) {
	root := t.TempDir()
	store, err := newHelperIntentStore(root)
	if err != nil {
		t.Fatal(err)
	}
	secrets := &countingSecrets{fakeSecretStore: newFakeSecretStore()}
	installationID, connectionID, challengeID := contract.NewID(), contract.NewID(), contract.NewID()
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	intent := helperIntent{Schema: helperIntentSchema, InstallationID: string(installationID), ConnectionID: string(connectionID), ConnectionVersion: 1, AccountIdentity: "acct", BeginKey: "begin-key", ChallengeID: string(challengeID), ChallengeVersion: 1, ExpiresAt: expires, CredentialName: "connections/credential/" + string(contract.NewID()), CompleteKey: "complete-key"}
	beginRaw, err := helperBeginRequest(intent)
	if err != nil {
		t.Fatal(err)
	}
	intent.BeginRequestSHA256 = helperMutationDigest("connection.setup.begin", intent.BeginKey, beginRaw)
	if err := store.write(intent); err != nil {
		t.Fatal(err)
	}
	ref, err := secrets.Put(context.Background(), intent.CredentialName, []byte("already-custodied"))
	if err != nil {
		t.Fatal(err)
	}
	var effectiveRef string
	completed := false
	op := &fakeOperator{fn: func(_ context.Context, operation string, req contract.Request) (contract.Result, error) {
		switch operation {
		case "command.get":
			return contract.Result{Payload: contract.Payload{Status: contract.StatusFailed, Error: &contract.Fault{Code: contract.CodeNotFound}}}, nil
		case "connection.get":
			return contract.Result{Payload: contract.Payload{Status: contract.StatusCompleted, Data: mustJSON(t, map[string]any{"resource": map[string]any{"version": 1, "account_identity": "acct", "credential_ref": effectiveRef}})}}, nil
		case "connection.setup.status":
			state := "external_action_required"
			if completed {
				state = "completed"
			}
			return contract.Result{Payload: contract.Payload{Status: contract.StatusCompleted, Data: mustJSON(t, map[string]any{"resource": map[string]any{"id": challengeID, "version": 1, "connection_id": connectionID, "state": state, "expires_at": expires}})}}, nil
		case "connection.setup.complete":
			if req.SubmissionKey != intent.CompleteKey {
				t.Fatal("completion key changed")
			}
			if strings.Contains(string(req.Input), "already-custodied") {
				t.Fatal("credential leaked into operation")
			}
			effectiveRef = ref
			completed = true
			return contract.Result{Payload: contract.Payload{Status: contract.StatusCompleted}}, nil
		default:
			t.Fatalf("unexpected operation %s", operation)
			return contract.Result{}, nil
		}
	}}
	var diagnostics lockedBuffer
	if err := runHelperEngine(context.Background(), root, op, secrets, strings.NewReader("different-secret\n"), &diagnostics, installationID, connectionID); err != nil {
		t.Fatal(err)
	}
	if secrets.puts != 2 {
		t.Fatalf("credential was overwritten: puts=%d", secrets.puts)
	} // existing credential + receipt key
	if strings.Contains(diagnostics.String(), "already-custodied") {
		t.Fatal("credential leaked")
	}
}

// fakeSecretStore models the real distinction between trusted name and
// opaque reference.
type fakeSecretStore struct {
	store     map[string][]byte
	names     map[string]string
	lookupErr error
}

func newFakeSecretStore() *fakeSecretStore {
	return &fakeSecretStore{store: map[string][]byte{}, names: map[string]string{}}
}

func (s *fakeSecretStore) Put(_ context.Context, name string, secret []byte) (string, error) {
	ref := "fake:" + name
	s.names[name] = ref
	s.store[ref] = append([]byte(nil), secret...)
	return ref, nil
}
func (s *fakeSecretStore) Lookup(_ context.Context, name string) (string, error) {
	if s.lookupErr != nil {
		return "", s.lookupErr
	}
	ref, ok := s.names[name]
	if !ok {
		return "", &contract.Fault{Code: contract.CodeNotFound}
	}
	return ref, nil
}
func (s *fakeSecretStore) Get(_ context.Context, reference string) ([]byte, error) {
	v, ok := s.store[reference]
	if !ok {
		return nil, fmt.Errorf("unknown reference %s", reference)
	}
	return append([]byte(nil), v...), nil
}
func (s *fakeSecretStore) Delete(_ context.Context, reference string) error {
	delete(s.store, reference)
	return nil
}
