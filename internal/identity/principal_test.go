// internal/identity/principal_test.go
package identity

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestProvisionOwnerStableAcrossCallsAndReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ks")
	ctx := context.Background()

	p1, err := NewProvisioner(dir)
	if err != nil {
		t.Fatalf("NewProvisioner: %v", err)
	}
	ref1, err := p1.ProvisionOwner(ctx)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	ref2, err := p1.ProvisionOwner(ctx)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if ref1.ID != ref2.ID || string(ref1.PublicKey) != string(ref2.PublicKey) {
		t.Fatal("PrincipalRef not stable within one provisioner")
	}

	// Reopen from disk: same identity, proving persistence.
	p2, err := NewProvisioner(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	ref3, err := p2.ProvisionOwner(ctx)
	if err != nil {
		t.Fatalf("after reopen: %v", err)
	}
	if ref1.ID != ref3.ID || string(ref1.PublicKey) != string(ref3.PublicKey) {
		t.Fatal("PrincipalRef not stable across reopen")
	}
}

func TestMetaCorruptRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ks")
	p, err := NewProvisioner(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ProvisionOwner(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	metaPath := filepath.Join(dir, "owner.meta.json")
	if err := writeFile(metaPath, []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ProvisionOwner(context.Background()); err == nil {
		t.Fatal("want error for corrupt metadata, got nil")
	}
}

func writeFile(path string, b []byte) error {
	return os.WriteFile(path, b, 0o644)
}
