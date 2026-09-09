// internal/state/lease_test.go
package state

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func openForLease(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Bootstrap(context.Background(), st, provStub{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestLeaseAcquireRenewRelease(t *testing.T) {
	st := openForLease(t)
	ctx := context.Background()
	gen, _ := st.Generation(ctx)

	if err := AcquireLease(ctx, st.DB(), "task/a", "w1", gen); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	// Second worker refused while live.
	if err := AcquireLease(ctx, st.DB(), "task/a", "w2", gen); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("contended acquire: %v, want ErrLeaseHeld", err)
	}
	// Holder renews fine; non-holder renew is loss.
	if err := RenewLease(ctx, st.DB(), "task/a", "w1", gen); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if err := RenewLease(ctx, st.DB(), "task/a", "w2", gen); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("foreign renew: %v, want ErrLeaseLost", err)
	}
	if err := ReleaseLease(ctx, st.DB(), "task/a", "w1", gen); err != nil {
		t.Fatalf("release: %v", err)
	}
	// Free again.
	if err := AcquireLease(ctx, st.DB(), "task/a", "w2", gen); err != nil {
		t.Fatalf("reacquire: %v", err)
	}
}

func TestLeaseVoidedByGenerationAdvance(t *testing.T) {
	st := openForLease(t)
	ctx := context.Background()
	gen, _ := st.Generation(ctx)

	if err := AcquireLease(ctx, st.DB(), "task/b", "w1", gen); err != nil {
		t.Fatal(err)
	}
	// Controller restart: generation advances.
	gen2, err := st.AdvanceGeneration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Old holder's renewal fails — the void is detected by the worker.
	if err := RenewLease(ctx, st.DB(), "task/b", "w1", gen); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("post-restart renew: %v, want ErrLeaseLost", err)
	}
	// Resource is immediately acquirable under the new generation,
	// despite the row still physically existing.
	if err := AcquireLease(ctx, st.DB(), "task/b", "w2", gen2); err != nil {
		t.Fatalf("takeover post-restart: %v", err)
	}
}

func TestLeaseHolderReportsStaleAsFree(t *testing.T) {
	st := openForLease(t)
	ctx := context.Background()
	gen, _ := st.Generation(ctx)
	if err := AcquireLease(ctx, st.DB(), "task/c", "w1", gen); err != nil {
		t.Fatal(err)
	}
	gen2, _ := st.AdvanceGeneration(ctx)
	h, err := LeaseHolder(ctx, st.DB(), "task/c", gen2)
	if err != nil || h != "" {
		t.Fatalf("holder = %q, %v; want free under new generation", h, err)
	}
}
