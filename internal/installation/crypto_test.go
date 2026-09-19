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
	key, _, _, err := resolveBackupKey(context.Background(), secrets, install, "")
	if err != nil {
		t.Fatalf("resolveBackupKey: %v", err)
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

// TestResolveBackupKeyByReference: the store resolves keys only by the
// reference Put returned. Resolving with that reference yields the same key
// without minting; resolving with no reference, or with a name, mints and
// custodies a fresh key and reports its reference.
func TestResolveBackupKeyByReference(t *testing.T) {
	secrets := newFakeSecrets()
	install := contract.ID("00000000-0000-4000-8000-000000000002")
	k1, ref, minted, err := resolveBackupKey(context.Background(), secrets, install, "")
	if err != nil || !minted || ref == "" {
		t.Fatalf("first resolve: key %d bytes, ref %q, minted %v, err %v", len(k1), ref, minted, err)
	}
	k2, ref2, minted2, err := resolveBackupKey(context.Background(), secrets, install, ref)
	if err != nil || minted2 || ref2 != ref || !bytes.Equal(k1, k2) {
		t.Fatalf("resolve by reference: ref %q minted %v equal %v err %v", ref2, minted2, bytes.Equal(k1, k2), err)
	}
	k3, ref3, minted3, err := resolveBackupKey(context.Background(), secrets, install, backupKeyName(install))
	if err != nil || !minted3 || ref3 == ref || bytes.Equal(k1, k3) {
		t.Fatalf("a name is not a reference: it must mint a new key, got ref %q minted %v err %v", ref3, minted3, err)
	}
}

func TestOpenBundleRejectsTamperedCiphertext(t *testing.T) {
	secrets := newFakeSecrets()
	install := contract.ID("00000000-0000-4000-8000-000000000003")
	key, _, _, err := resolveBackupKey(context.Background(), secrets, install, "")
	if err != nil {
		t.Fatalf("resolveBackupKey: %v", err)
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
	keyA, _, _, err := resolveBackupKey(context.Background(), secrets, "00000000-0000-4000-8000-000000000004", "")
	if err != nil {
		t.Fatalf("resolveBackupKey A: %v", err)
	}
	keyB, _, _, err := resolveBackupKey(context.Background(), secrets, "00000000-0000-4000-8000-000000000005", "")
	if err != nil {
		t.Fatalf("resolveBackupKey B: %v", err)
	}
	sealed, err := sealBundle(keyA, []byte("for installation A only"))
	if err != nil {
		t.Fatalf("sealBundle: %v", err)
	}
	if _, err := openBundle(keyB, sealed); err == nil {
		t.Fatalf("expected the wrong installation's key to fail")
	}
}
