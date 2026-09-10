package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// writeKeyFile writes 32 bytes of key material as a 0600 file in its own
// directory (deliberately outside the state directory) and returns the
// file: reference.
func writeKeyFile(t *testing.T, key []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")
	if err := os.WriteFile(path, key, 0o600); err != nil {
		t.Fatalf("key file: %v", err)
	}
	return "file:" + path
}

// openForTest opens a platform on a fresh headless installation with the
// default test key and shortens the ownership watchdog for deterministic
// loss detection.
func openForTest(t *testing.T) (*Platform, string) {
	t.Helper()
	return openForTestWithKey(t, writeKeyFile(t, testMasterKey(t)))
}

func openForTestWithKey(t *testing.T, keyRef string) (*Platform, string) {
	t.Helper()
	state := filepath.Join(t.TempDir(), "state")
	p, err := Open(Config{
		StateDir:          state,
		CredentialBackend: backendHeadless,
		MasterKeyRef:      keyRef,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p.lockWatchInterval = 25 * time.Millisecond
	t.Cleanup(func() { _ = p.Close() })
	return p, state
}

// reopenForTest reopens an existing state directory with the default test
// key material, as a restarted controller would.
func reopenForTest(t *testing.T, state string) (*Platform, error) {
	t.Helper()
	return Open(Config{
		StateDir:          state,
		CredentialBackend: backendHeadless,
		MasterKeyRef:      writeKeyFile(t, testMasterKey(t)),
	})
}

func testMasterKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}

// openForTestWithMax opens a platform whose per-object artifact bound is
// narrowed for cheap limit testing.
func openForTestWithMax(t *testing.T, max int64) (*Platform, string) {
	t.Helper()
	state := filepath.Join(t.TempDir(), "state")
	p, err := Open(Config{
		StateDir:          state,
		CredentialBackend: backendHeadless,
		MasterKeyRef:      writeKeyFile(t, testMasterKey(t)),
		MaxArtifactBytes:  max,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p.lockWatchInterval = 25 * time.Millisecond
	t.Cleanup(func() { _ = p.Close() })
	return p, state
}

// beUint32 reads a big-endian uint32 from a slice (test-side envelope
// parsing).
func beUint32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// asPlatformError is errors.As narrowed to *Error.
func asPlatformError(err error, target **Error) bool {
	return errors.As(err, target)
}

// errUnwrapText renders the wrapped cause chain for programmatic-inspection
// assertions.
func errUnwrapText(e *Error) string {
	cause := e.Unwrap()
	if cause == nil {
		return ""
	}
	return cause.Error()
}

// errGenericTest is a plain non-platform error for fault-code fallback.
var errGenericTest = errors.New("ordinary failure")

// waitForLost fails the test when ownership loss is not observed in time.
func waitForLost(t *testing.T, own contract.Ownership) {
	t.Helper()
	select {
	case <-own.Lost():
	case <-time.After(5 * time.Second):
		t.Fatal("ownership loss was not observed within 5s")
	}
}

func digestOf(b []byte) contract.Digest {
	sum := sha256.Sum256(b)
	return contract.Digest(hex.EncodeToString(sum[:]))
}

func stagedFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("staged dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	return fi.Mode().Perm()
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", code)
	}
	if got := Code(err); got != code {
		t.Fatalf("expected code %s, got %s (err: %v)", code, got, err)
	}
}

// errMessage returns the redacted surface message of an error.
func errMessage(err error) string {
	msg := err.Error()
	var perr *Error
	if errors.As(err, &perr) {
		msg = perr.Message
	}
	return msg
}

func ctx() context.Context { return context.Background() }
