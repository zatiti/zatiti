// internal/app/operation_e2e_test.go
package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zatiti/zatiti/internal/state"
)

func TestOperatorProvisioningAndRoleGating(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	a, out, errOut := newTestApp(t)
	ctx := context.Background()
	setIO(t, out, errOut)

	if code, err := a.Run(ctx, []string{"init", "-data-dir", dir}); code != ExitOK || err != nil {
		t.Fatalf("init: %d %v", code, err)
	}
	// Owner creates an auditor.
	code, err := a.Run(ctx, []string{
		"principal.create", "-data-dir", dir, "-name", "ivy", "-role", "auditor"})
	if code != ExitOK || err != nil {
		t.Fatalf("principal.create: %d %v", code, err)
	}
	// Bad role refused as input error.
	code, _ = a.Run(ctx, []string{
		"principal.create", "-data-dir", dir, "-name", "x", "-role", "superuser"})
	if code != ExitInputError {
		t.Fatalf("bad role: code=%d, want ExitInputError", code)
	}

	// journal now holds: bootstrap facts are not audited (bootstrap
	// predates the audit table's mutation path — by design, it IS the
	// establishment of trust, not an act within it), plus principal.create ok.
	st, err := state.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	orgID, err := state.RootOrgID(ctx, st.DB())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := state.ListAudit(ctx, st.DB(), orgID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Result != "ok" || rows[0].Op != "principal.create" {
		t.Fatalf("audit rows = %+v, want one ok principal.create", rows)
	}
}
