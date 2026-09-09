// internal/app/operation_test.go
package app

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"zatiti/internal/authz"
	"zatiti/internal/state"
)

func newTestApp(t *testing.T) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	a, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	return a, out, errOut
}

func TestOrganizationCreateAuthorizedJournaled(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	a, out, errOut := newTestApp(t)
	ctx := context.Background()
	setIO(t, out, errOut)

	if code, err := a.Run(ctx, []string{"init", "-data-dir", dir}); err != nil || code != ExitOK {
		t.Fatalf("init: code=%d err=%v", code, err)
	}
	code, err := a.Run(ctx, []string{"organization.create", "-data-dir", dir, "-name", "acme"})
	if err != nil || code != ExitOK {
		t.Fatalf("create: code=%d err=%v", code, err)
	}

	// Duplicate name → exit 4 (stale/conflict).
	code, err = a.Run(ctx, []string{"organization.create", "-data-dir", dir, "-name", "acme"})
	if code != ExitStale {
		t.Fatalf("duplicate create: code=%d err=%v, want ExitStale", code, err)
	}

	// Journal: one ok + one denied? No — conflict is not a denial; the
	// mutation transaction never committed, so exactly one ok audit row
	// must exist. Conflicts from UNIQUE violations are returned before
	// the audit insert, by design.
	st, err := state.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st.Close()
	orgs, err := state.ListOrganizations(ctx, st.DB())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// bootstrap root + acme
	if len(orgs) != 2 {
		t.Fatalf("orgs = %d, want 2", len(orgs))
	}
}

func TestUnknownActorDenied(t *testing.T) {
	// Direct authz-level check: an actor absent from memberships is
	// denied, and Decide never errors on absence (fail-closed, not fail-loud).
	dir := filepath.Join(t.TempDir(), "data")
	a, _, _ := newTestApp(t)
	ctx := context.Background()
	if code, err := a.Run(ctx, []string{"init", "-data-dir", dir}); code != ExitOK || err != nil {
		t.Fatalf("init: %d %v", code, err)
	}
	st, _ := state.Open(dir)
	defer st.Close()
	op, _ := a.registry.Lookup("organization.create")
	dec, err := authz.New(st.DB()).Decide(ctx, "no-such-actor", "no-such-org", op)
	if err != nil {
		t.Fatalf("Decide errored: %v", err)
	}
	if dec.Allow {
		t.Fatal("unknown actor/org allowed")
	}
}
