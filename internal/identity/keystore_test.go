// internal/identity/keystore_test.go
package identity

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestProvisionOwnerCreatesAndReloads(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ks")
	p, err := NewProvisioner(dir)
	if err != nil {
		t.Fatalf("NewProvisioner: %v", err)
	}
	ref1, err := p.ProvisionOwner(context.Background())
	if err != nil {
		t.Fatalf("ProvisionOwner: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "owner.key")); err != nil {
		t.Fatalf("key file missing: %v", err)
	}
	info, _ := os.Stat(filepath.Join(dir, "owner.key"))
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key perms %o, want 600", perm)
	}
	// Second call loads the existing key (same public key).
	ref2, err := p.ProvisionOwner(context.Background())
	if err != nil {
		t.Fatalf("second ProvisionOwner: %v", err)
	}
	if string(ref1.PublicKey) != string(ref2.PublicKey) {
		t.Fatal("public key changed across provisioning calls")
	}
}

func TestKeystoreDirPermissionsEnforced(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenKeystore(dir); err == nil {
		t.Fatal("want error for 0755 keystore, got nil")
	}
}
