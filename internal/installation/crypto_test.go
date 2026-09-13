package installation

import (
	"bytes"
	"context"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestSealAndOpenBundleRoundTrip(t *testing.T) {
	secrets := newFakeSecrets()
	install := contract.ID("00000000-0000-4000-8000-000000000001")
	key, err := ensureBackupKey(context.Background(), secrets, install)
	if err != nil {
		t.Fatalf("ensureBackupKey: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("key length = %d, want 32", len(key))
	}

	plaintext := []byte(`{"schema":"zatiti.backup/v1"}`)
	sealed, err := sealBundle(key, plaintext)
	if err != nil {
		t.Fatalf("sealBundle: %v", err)
	}
	if bytes.Contains(sealed, plaintext) {
		t.Fatalf("sealed bundle leaks the plaintext verbatim")
	}
	opened, err := openBundle(key, sealed)
	if err != nil {
		t.Fatalf("openBundle: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("round trip mismatch: got %q, want %q", opened, plaintext)
	}
}

func TestEnsureBackupKeyIsStableAcrossCalls(t *testing.T) {
	secrets := newFakeSecrets()
	install := contract.ID("00000000-0000-4000-8000-000000000002")
	k1, err := ensureBackupKey(context.Background(), secrets, install)
	if err != nil {
		t.Fatalf("first ensureBackupKey: %v", err)
	}
	k2, err := ensureBackupKey(context.Background(), secrets, install)
	if err != nil {
		t.Fatalf("second ensureBackupKey: %v", err)
	}
	if !bytes.Equal(k1, k2) {
		t.Fatalf("backup key changed across calls for the same installation")
	}
}

func TestOpenBundleRejectsTamperedCiphertext(t *testing.T) {
	secrets := newFakeSecrets()
	install := contract.ID("00000000-0000-4000-8000-000000000003")
	key, err := ensureBackupKey(context.Background(), secrets, install)
	if err != nil {
		t.Fatalf("ensureBackupKey: %v", err)
	}
	sealed, err := sealBundle(key, []byte("integrity matters"))
	if err != nil {
		t.Fatalf("sealBundle: %v", err)
	}
	sealed[len(sealed)-1] ^= 0xFF
	if _, err := openBundle(key, sealed); err == nil {
		t.Fatalf("expected tampered ciphertext to fail authentication")
	}
}

func TestOpenBundleRejectsWrongKey(t *testing.T) {
	secrets := newFakeSecrets()
	keyA, err := ensureBackupKey(context.Background(), secrets, "00000000-0000-4000-8000-000000000004")
	if err != nil {
		t.Fatalf("ensureBackupKey A: %v", err)
	}
	keyB, err := ensureBackupKey(context.Background(), secrets, "00000000-0000-4000-8000-000000000005")
	if err != nil {
		t.Fatalf("ensureBackupKey B: %v", err)
	}
	sealed, err := sealBundle(keyA, []byte("for installation A only"))
	if err != nil {
		t.Fatalf("sealBundle: %v", err)
	}
	if _, err := openBundle(keyB, sealed); err == nil {
		t.Fatalf("expected the wrong installation's key to fail")
	}
}
