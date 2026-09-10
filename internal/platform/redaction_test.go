package platform

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// collectRedactionCases drives real failures across the platform surface and
// returns their redacted messages together with the values that must never
// appear in them.
func collectRedactionCases(t *testing.T) (messages []string, secrets []string) {
	t.Helper()
	canary := "zatiti-redaction-canary-3f8a"

	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	keyPath := filepath.Join(dir, "master.key")
	if err := os.WriteFile(keyPath, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}

	fail := func(label string, err error) {
		if err == nil {
			t.Fatalf("%s: expected an error", label)
		}
		messages = append(messages, errMessage(err))
	}

	// Loose key permissions.
	loose := filepath.Join(dir, "loose.key")
	if err := os.WriteFile(loose, make([]byte, 32), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: "file:" + loose})
	fail("loose key", err)

	// Missing key.
	_, err = Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: "file:" + filepath.Join(dir, "absent.key")})
	fail("missing key", err)

	// Symlinked state root.
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	_, err = Open(Config{StateDir: link, CredentialBackend: backendHeadless, MasterKeyRef: "file:" + keyPath})
	fail("symlink state", err)

	// A working installation.
	p, st := openForTestWithKey(t, "file:"+keyPath)
	t.Cleanup(func() { _ = p.Close() })
	_ = st
	secretRef, err := p.Secrets().Put(ctx(), "k", []byte(canary))
	if err != nil {
		t.Fatal(err)
	}

	// Tampered secret.
	id := strings.TrimPrefix(secretRef, "hl1:")
	spath := filepath.Join(st, dirNameSecrets, dirNameSecretVals, id)
	if data, rerr := os.ReadFile(spath); rerr == nil {
		data[len(data)-1] ^= 0xFF
		_ = os.WriteFile(spath, data, 0o600)
		_, err = p.Secrets().Get(ctx(), secretRef)
		fail("tampered secret", err)
	}

	// Unknown staging reference and unknown digest.
	err = p.Blobs().Publish(ctx(), "st1:"+strings.Repeat("0", 32), digestOf([]byte("x")))
	fail("unknown staging ref", err)
	_, err = p.Blobs().Open(ctx(), digestOf([]byte("unknown artifact bytes")), 0, 0)
	fail("unknown object", err)

	// Lock contention.
	own, err := p.Acquire(ctx())
	if err != nil {
		t.Fatal(err)
	}
	p2, err := Open(Config{StateDir: st, CredentialBackend: backendHeadless, MasterKeyRef: "file:" + keyPath})
	if err != nil {
		t.Fatal(err)
	}
	p2.lockWatchInterval = 25 * time.Millisecond
	_, err = p2.Acquire(ctx())
	fail("competing controller", err)
	_ = p2.Close()
	_ = own.Close()

	return messages, []string{canary, dir, keyPath, string(filepath.Separator) + "Users"}
}

func TestErrorsAreRedacted(t *testing.T) {
	messages, forbidden := collectRedactionCases(t)
	if len(messages) == 0 {
		t.Fatal("no messages collected")
	}
	for _, msg := range messages {
		for _, bad := range forbidden {
			if bad == "" {
				continue
			}
			if strings.Contains(msg, bad) {
				t.Fatalf("redacted message %q contains forbidden value %q", msg, bad)
			}
		}
		// No path separators at all: messages carry no filesystem shape.
		if strings.Contains(msg, "/") || strings.Contains(msg, string(filepath.Separator)) {
			t.Fatalf("message %q contains a path separator", msg)
		}
		// No backslashes either (Windows-style leakage).
		if strings.Contains(msg, "\\") {
			t.Fatalf("message %q contains a backslash", msg)
		}
	}
}

func TestErrorCauseStaysReachableForPrograms(t *testing.T) {
	// Redaction is about rendered messages; the wrapped cause remains
	// available to programmatic inspection through Unwrap.
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "master.key")
	if err := os.WriteFile(keyPath, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(Config{StateDir: filepath.Join(dir, "state"), CredentialBackend: backendHeadless, MasterKeyRef: "file:" + keyPath + "-missing"})
	if err == nil {
		t.Fatal("expected failure")
	}
	var perr *Error
	if !asPlatformError(err, &perr) {
		t.Fatalf("error is not *Error: %v", err)
	}
	if perr.Message != err.Error() {
		t.Fatal("Error() must render the redacted message")
	}
	if perr.Unwrap() == nil {
		t.Fatal("cause was dropped")
	}
	if !bytes.Contains([]byte(errUnwrapText(perr)), []byte(keyPath+"-missing")) {
		t.Fatal("programmatic cause lost the underlying detail")
	}
}

func TestCodeMapsFaultsAcrossTheVocabulary(t *testing.T) {
	if Code(nil) != "" {
		t.Fatal("nil error must map to an empty code")
	}
	_, err := Open(Config{StateDir: "relative", MasterKeyRef: "file:/x"})
	if Code(err) != contractCodeInvalidInput {
		t.Fatalf("invalid input mapped to %q", Code(err))
	}
	_, oerr := Open(Config{StateDir: t.TempDir(), CredentialBackend: backendHeadless, MasterKeyRef: "file:/nonexistent-zatiti.key"})
	if Code(oerr) != contractCodePrerequisiteMissing {
		t.Fatalf("prerequisite missing mapped to %q", Code(oerr))
	}
	// Non-platform errors fall back to internal_error.
	if got := Code(errGenericTest); got != contractCodeInternalErrorAlias {
		t.Fatalf("unknown error mapped to %q", got)
	}
}
