// internal/state/store_test.go
package state

import (
	"context"
	"errors"
	"testing"
)

func TestOpenExclusiveOwnership(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	defer s1.Close()

	_, err = Open(dir)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second Open: want ErrLocked, got %v", err)
	}
}

func TestBootstrapRefusesReinit(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	p := fakeProvisioner{}
	ctx := context.Background()
	if err := Bootstrap(ctx, s, p); err != nil {
		t.Fatalf("first Bootstrap: %v", err)
	}
	err = Bootstrap(ctx, s, p)
	if !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("second Bootstrap: want ErrAlreadyInitialized, got %v", err)
	}
}

func TestBootstrapIdempotentSchemaOnRetry(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	ctx := context.Background()
	if err := Bootstrap(ctx, s, fakeProvisioner{}); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	s.Close()

	// Reopen and re-migrate: no error, ledger consistent.
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if err := Migrate(ctx, s2.DB()); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	g, err := s2.Generation(ctx)
	if err != nil || g != 1 {
		t.Fatalf("generation: got %d, %v", g, err)
	}
}

// fakeProvisioner stands in for the identity root in state tests,
// mirroring the port without importing identity.
type fakeProvisioner struct{}

func (fakeProvisioner) ProvisionOwner(ctx context.Context) (PrincipalRef, error) {
	return PrincipalRef{ID: newID(), PublicKey: []byte("test-key"), CreatedAt: 0}, nil
}

// provStub is the name later replies use for fakeProvisioner.
type provStub = fakeProvisioner
