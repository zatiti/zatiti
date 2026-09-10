package platform

import (
	"bytes"
	"context"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHeadlessSecretRoundtrip(t *testing.T) {
	p, state := openForTest(t)
	secrets := p.Secrets()

	value := []byte("provider-api-key-value-do-not-log")
	ref, err := secrets.Put(ctx(), "primary", value)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !strings.HasPrefix(ref, "hl1:") {
		t.Fatalf("reference %q is not an opaque hl1: reference", ref)
	}
	if bytes.Contains([]byte(ref), value) {
		t.Fatal("reference carries the secret bytes")
	}

	got, err := secrets.Get(ctx(), ref)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, value) {
		t.Fatal("roundtrip mismatch")
	}

	// Stored files are 0600 under the 0700 secrets tree.
	id := strings.TrimPrefix(ref, "hl1:")
	if got := modeOf(t, filepath.Join(state, dirNameSecrets, dirNameSecretVals, id)); got != 0o600 {
		t.Fatalf("secret file mode is %o, want 600", got)
	}
	if got := modeOf(t, filepath.Join(state, dirNameSecrets, fileSecretIndex)); got != 0o600 {
		t.Fatalf("secret index mode is %o, want 600", got)
	}

	if err := secrets.Delete(ctx(), ref); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := secrets.Get(ctx(), ref); err == nil {
		t.Fatal("Get after Delete must fail")
	}
	// Deletion is idempotent.
	if err := secrets.Delete(ctx(), ref); err != nil {
		t.Fatalf("second Delete: %v", err)
	}
}

func TestHeadlessSecretReplaceKey(t *testing.T) {
	p, _ := openForTest(t)
	secrets := p.Secrets()

	ref1, err := secrets.Put(ctx(), "primary", []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	ref2, err := secrets.Put(ctx(), "primary", []byte("second"))
	if err != nil {
		t.Fatalf("replace Put: %v", err)
	}
	if ref1 == ref2 {
		t.Fatal("replacement reused the same reference")
	}
	got, err := secrets.Get(ctx(), ref2)
	if err != nil {
		t.Fatalf("Get after replace: %v", err)
	}
	if !bytes.Equal(got, []byte("second")) {
		t.Fatal("replaced value not returned")
	}
	// The old reference is gone.
	if _, err := secrets.Get(ctx(), ref1); err == nil {
		t.Fatal("old reference still resolves after replace")
	}
}

func TestHeadlessSecretInputValidation(t *testing.T) {
	p, _ := openForTest(t)
	secrets := p.Secrets()

	cases := []struct {
		name  string
		key   string
		value []byte
	}{
		{"empty key", "", []byte("v")},
		{"oversized key", strings.Repeat("k", 257), []byte("v")},
		{"NUL in key", "bad\x00key", []byte("v")},
		{"empty value", "k", nil},
		{"oversized value", "k", make([]byte, secretMaxValue+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := secrets.Put(ctx(), tc.key, tc.value)
			wantCode(t, err, contractCodeInvalidInput)
		})
	}
}

func TestHeadlessSecretUnknownReferences(t *testing.T) {
	p, _ := openForTest(t)
	secrets := p.Secrets()

	_, err := secrets.Get(ctx(), "hl1:"+strings.Repeat("0", 32))
	wantCode(t, err, contractCodeNotFound)

	// Deletion of an unknown reference is idempotent, matching the keychain
	// backend's item-not-found behavior.
	if err := secrets.Delete(ctx(), "hl1:"+strings.Repeat("0", 32)); err != nil {
		t.Fatalf("Delete of an unknown reference: %v", err)
	}

	_, err = secrets.Get(ctx(), "not-a-ref")
	wantCode(t, err, contractCodeNotFound)
}

func TestHeadlessSecretTamperIsRefused(t *testing.T) {
	p, state := openForTest(t)
	ref, err := p.Secrets().Put(ctx(), "k", []byte("sensitive"))
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimPrefix(ref, "hl1:")
	path := filepath.Join(state, dirNameSecrets, dirNameSecretVals, id)

	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte{}, original...)
	tampered[len(tampered)-1] ^= 0xFF
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = p.Secrets().Get(ctx(), ref)
	wantCode(t, err, contractCodeControllerUnavailable)
	if msg := errMessage(err); !strings.Contains(msg, "integrity") {
		t.Fatalf("unexpected tamper message: %q", msg)
	}

	// Truncation is equally refused.
	if err := os.WriteFile(path, original[:len(original)-4], 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = p.Secrets().Get(ctx(), ref)
	wantCode(t, err, contractCodeControllerUnavailable)
}

func TestHeadlessSecretMissingValueFileIsNotFound(t *testing.T) {
	p, _ := openForTest(t)
	ref, err := p.Secrets().Put(ctx(), "k", []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimPrefix(ref, "hl1:")
	if err := os.Remove(filepath.Join(p.stateDir, dirNameSecrets, dirNameSecretVals, id)); err != nil {
		t.Fatal(err)
	}
	_, err = p.Secrets().Get(ctx(), ref)
	wantCode(t, err, contractCodeNotFound)
}

func TestHeadlessSecretSwappedValueFileIsRefused(t *testing.T) {
	// Swapping two stored values between references must fail: each envelope
	// is bound to its key material and position by magic + AAD.
	p, state := openForTest(t)
	refA, err := p.Secrets().Put(ctx(), "a", []byte("value-a"))
	if err != nil {
		t.Fatal(err)
	}
	refB, err := p.Secrets().Put(ctx(), "b", []byte("value-b"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(state, dirNameSecrets, dirNameSecretVals)
	pathA := filepath.Join(dir, strings.TrimPrefix(refA, "hl1:"))
	pathB := filepath.Join(dir, strings.TrimPrefix(refB, "hl1:"))

	dataB, _ := os.ReadFile(pathB)
	if err := os.WriteFile(pathA, dataB, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Secrets().Get(ctx(), refA); err == nil {
		t.Fatal("swapped secret value was accepted")
	} else {
		wantCode(t, err, contractCodeControllerUnavailable)
	}
}

func TestHeadlessSecretCorruptIndexDegradesButRefsSurvive(t *testing.T) {
	p, state := openForTest(t)
	ref, err := p.Secrets().Put(ctx(), "k", []byte("value"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, dirNameSecrets, fileSecretIndex), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A reopen loads the corrupt index: the convenience lookup degrades to
	// empty, but the reference itself stays resolvable.
	_ = p.Close()
	p2, err := reopenForTest(t, state)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p2.Close() }()
	if got := p2.secrets.refForKey("k"); got != "" {
		t.Fatalf("corrupt index returned a reference: %q", got)
	}
	got, err := p2.Secrets().Get(ctx(), ref)
	if err != nil || string(got) != "value" {
		t.Fatalf("reference lost after index corruption: %q, %v", got, err)
	}
}

func TestHeadlessSecretsRefusePlaintextFallbackAfterKeyLoss(t *testing.T) {
	// Losing the master key must make the store unavailable, not fall back
	// to plaintext: Open fails outright and nothing readable remains.
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	keyPath := filepath.Join(dir, "k")
	if err := os.WriteFile(keyPath, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: "file:" + keyPath})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := p.Secrets().Put(ctx(), "k", []byte("value"))
	if err != nil {
		t.Fatal(err)
	}
	_ = p.Close()

	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	_, err = Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: "file:" + keyPath})
	wantCode(t, err, contractCodePrerequisiteMissing)

	// A wrong key opens (identity is file-based, not key-based) but must
	// serve nothing: stored values fail integrity under a different subkey.
	if err := os.WriteFile(keyPath, bytes.Repeat([]byte{0xBB}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	p2, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: "file:" + keyPath})
	if err != nil {
		t.Fatalf("reopen with a different key: %v", err)
	}
	defer func() { _ = p2.Close() }()
	got, err := p2.Secrets().Get(ctx(), ref)
	if err == nil {
		t.Fatal("a different master key decrypted the stored secret")
	}
	wantCode(t, err, contractCodeControllerUnavailable)
	_ = got
}

func TestKeychainRefRoundtripIsPureEncoding(t *testing.T) {
	// Reference encoding is exercised without invoking the OS helper.
	for _, key := range []string{"primary", "provider/openai", "ключ", "with space"} {
		ref := keychainRef(key)
		if !strings.HasPrefix(ref, "kc1:") || bytes.Contains([]byte(ref), []byte(key)) {
			t.Fatalf("reference %q is not opaque for key %q", ref, key)
		}
		got, err := parseKeychainRef(ref)
		if err != nil || got != key {
			t.Fatalf("parseKeychainRef(%q) = %q, %v", ref, got, err)
		}
	}
	if _, err := parseKeychainRef("kc1:!!!not-base64!!!"); err == nil {
		t.Fatal("malformed keychain reference accepted")
	}
	if _, err := parseKeychainRef("hl1:deadbeef"); err == nil {
		t.Fatal("cross-store reference accepted by keychain parser")
	}
}

func TestKeychainHelperUnavailableIsANamedPrerequisite(t *testing.T) {
	// The store degrades to a named fault when the OS helper is absent; no
	// plaintext fallback exists.
	k := &keychainSecrets{service: "com.zatiti.test", helperPath: "/nonexistent/zatiti-security-helper"}
	_, err := k.Put(ctx(), "k", []byte("v"))
	wantCode(t, err, contractCodePrerequisiteMissing)
	if msg := errMessage(err); !strings.Contains(msg, "helper") {
		t.Fatalf("unexpected helper-unavailable message: %q", msg)
	}
	_, err = k.Get(ctx(), keychainRef("k"))
	wantCode(t, err, contractCodePrerequisiteMissing)
	err = k.Delete(ctx(), keychainRef("k"))
	wantCode(t, err, contractCodePrerequisiteMissing)
}

func TestKeychainPutInputValidation(t *testing.T) {
	k := &keychainSecrets{service: "com.zatiti.test", helperPath: "/nonexistent/zatiti-security"}
	if _, err := k.Put(ctx(), "", []byte("v")); err == nil {
		t.Fatal("empty key accepted")
	}
	if _, err := k.Put(ctx(), "k", nil); err == nil {
		t.Fatal("empty value accepted")
	}
	if _, err := k.Put(ctx(), "k", make([]byte, 32*1024+1)); err == nil {
		t.Fatal("oversized value accepted")
	}
}

func TestOpenWithKeychainBackendAndFileKeyDoesNotTouchHelper(t *testing.T) {
	// Keychain mode plus a file: master key opens without any helper call:
	// the helper is only reached for credential operations.
	dir := t.TempDir()
	p, err := Open(Config{StateDir: filepath.Join(dir, "state"), CredentialBackend: backendKeychain, MasterKeyRef: writeKeyFile(t, testMasterKey(t))})
	if err != nil {
		t.Fatalf("keychain-backed Open: %v", err)
	}
	_ = p.Close()
}

func TestSecretsFailClosedAfterPlatformClose(t *testing.T) {
	// Close zeroizes the in-memory keys: subsequent Get must fail closed
	// instead of serving decrypted material.
	p, _ := openForTest(t)
	ref, err := p.Secrets().Put(ctx(), "k", []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	_ = p.Close()
	_, err = p.Secrets().Get(ctx(), ref)
	if err == nil {
		t.Fatal("Get served decrypted material after zeroKeys")
	}
	wantCode(t, err, contractCodeControllerUnavailable)
}

func TestSecretValueNeverAppearsInEnvironmentOfChildProcess(t *testing.T) {
	canary := []byte("zatiti-canary-secret-9f2b7c")
	p, _ := openForTest(t)
	if _, err := p.Secrets().Put(ctx(), "canary", canary); err != nil {
		t.Fatal(err)
	}

	// Fault surfaces must not carry the secret either.
	_, err := p.Secrets().Get(ctx(), "hl1:not-a-real-reference-000000000000000")
	if err == nil {
		t.Fatal("expected not-found error")
	}
	if msg := errMessage(err); strings.Contains(msg, string(canary)) {
		t.Fatal("fault message carries the secret bytes")
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^$", "-test.v=false")
	cmd.Env = append(os.Environ(),
		childEnvVar+"=env-scan",
		"ZATITI_TEST_CANARY_HEX="+hex.EncodeToString(canary),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child found the secret in its environment or failed: %v (%s)", err, out)
	}
}

func TestHeadlessSecretsTimeoutContext(t *testing.T) {
	p, _ := openForTest(t)
	cctx, cancel := context.WithTimeout(ctx(), 50*time.Millisecond)
	defer cancel()
	// Operations complete well inside the deadline on a local store; this
	// asserts no path blocks indefinitely on the headless backend.
	if _, err := p.Secrets().Put(cctx, "k", []byte("v")); err != nil {
		t.Fatalf("Put under deadline: %v", err)
	}
}
