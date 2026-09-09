// internal/controller/controller_test.go
package controller

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/zatiti/zatiti/internal/state"
)

func TestStartAdvancesGeneration(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	ctx := context.Background()
	logger := log.New(os.Stderr, "", 0)

	// Bootstrap via the state root (controller assumes init ran).
	st, err := openAndBootstrap(dir)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	st.Close()

	c1, err := Start(ctx, dir, logger)
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if c1.Generation() != 2 { // bootstrap=1, first serve advances to 2
		t.Fatalf("generation = %d, want 2", c1.Generation())
	}
	// Release ownership, restart: generation must advance again.
	if err := c1.Run(context.Background()); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	c2, err := Start(ctx, dir, logger)
	if err != nil {
		t.Fatalf("second Start: %v", err)
	}
	defer c2.Run(context.Background())
	if c2.Generation() != 3 {
		t.Fatalf("generation = %d, want 3", c2.Generation())
	}
}

func openAndBootstrap(dir string) (*state.Store, error) {
	st, err := state.Open(dir)
	if err != nil {
		return nil, err
	}
	return st, state.Bootstrap(context.Background(), st, fakeProv{})
}

type fakeProv struct{}

func (fakeProv) ProvisionOwner(ctx context.Context) (state.PrincipalRef, error) {
	return state.PrincipalRef{ID: "test", PublicKey: []byte("k")}, nil
}
