// internal/app/review_test.go
package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zatiti/zatiti/internal/state"
)

func TestReviewLifecycle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	a, out, errOut := newTestApp(t)
	ctx := context.Background()
	setIO(t, out, errOut)

	must := func(args []string, want int) string {
		t.Helper()
		code, err := a.Run(ctx, args)
		if code != want || err != nil {
			t.Fatalf("Run(%v): %d %v, want %d", args, code, err, want)
		}
		return out.String()
	}
	must([]string{"init", "-data-dir", dir}, ExitOK)

	// Second owner-role principal to satisfy quorum (proposer excluded).
	// The scaffold's CLI actor is always the owner, so approvals by the
	// single actor are proposer-refused; create a second owner-role
	// operator to act as approver #1 — and note the design tension this
	// surfaces: role 'admin' also qualifies (owner|admin), but the CLI
	// path cannot BE that principal. MCP session or direct state setup
	// is required; the test uses direct state setup, honestly.
	st, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	approver := state.NewID()
	if _, err := st.DB().Exec(
		`INSERT INTO principals (id, kind, public_key, created_at)
		 VALUES (?, 'operator', 'pending:x', strftime('%s','now'))`, approver); err != nil {
		t.Fatal(err)
	}
	orgID, err := state.RootOrgID(ctx, st.DB())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(
		`INSERT INTO memberships (principal_id, org_id, role) VALUES (?, ?, 'admin')`,
		approver, orgID); err != nil {
		t.Fatal(err)
	}
	approver2 := state.NewID()
	if _, err := st.DB().Exec(
		`INSERT INTO principals (id, kind, public_key, created_at)
		 VALUES (?, 'operator', 'pending:y', strftime('%s','now'))`, approver2); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(
		`INSERT INTO memberships (principal_id, org_id, role) VALUES (?, ?, 'admin')`,
		approver2, orgID); err != nil {
		t.Fatal(err)
	}
	// Second org to purge (root is protected).
	must([]string{"organization.create", "-data-dir", dir, "-name", "doomed"}, ExitOK)
	st.Close()

	// Direct destructive attempt refused even for owner.
	must([]string{"organization.purge", "-data-dir", dir, "-org", "doomed"}, ExitDenied)

	// Propose.
	outStr := must([]string{"review.submit", "-data-dir", dir,
		"-op", "organization.purge", "-org", "doomed"}, ExitOK)
	_ = outStr

	// Pull the review ID from state (CLI output parsing is brittle; the
	// test may consult state directly).
	st, err = state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var reviewID string
	if err := st.DB().QueryRow(
		`SELECT id FROM reviews WHERE state='pending' LIMIT 1`).Scan(&reviewID); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// Proposer (owner) cannot approve own proposal.
	must([]string{"review.approve", "-data-dir", dir, "-review", reviewID}, ExitDenied)

	// Quorum not met → execute is stale.
	must([]string{"review.execute", "-data-dir", dir, "-review", reviewID}, ExitStale)

	// Approval #1 via state (the approver principals have no CLI actor
	// path yet — recorded honestly; actor-level approval via MCP
	// sessions is the production route and is exercised by wiring).
	st, err = state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.ApproveReview(ctx, st.DB(), reviewID, approver, state.AuditEntry{
		ActorID: approver, Op: "review.approve", Result: "ok"}); err != nil {
		t.Fatalf("approve 1: %v", err)
	}
	if err := state.ApproveReview(ctx, st.DB(), reviewID, approver2, state.AuditEntry{
		ActorID: approver2, Op: "review.approve", Result: "ok"}); err != nil {
		t.Fatalf("approve 2: %v", err)
	}
	st.Close()

	// Execute: owner runs review.execute (Mutating → owner allowed),
	// approvals are revalidated against live memberships.
	must([]string{"review.execute", "-data-dir", dir, "-review", reviewID}, ExitOK)

	// doomed is gone; root survives; re-execute refused.
	st, _ = state.Open(dir)
	if _, err := state.OrgIDByName(ctx, st.DB(), "doomed"); err == nil {
		t.Fatal("doomed still exists after purge")
	}
	st.Close()

	// Re-execute refused (review already executed).
	code, _ := a.Run(ctx, []string{"review.execute", "-data-dir", dir, "-review", reviewID})
	if code != ExitStale {
		t.Fatalf("re-execute: code=%d, want ExitStale", code)
	}
}
