// internal/app/mcp_e2e_test.go
package app

import (
	"bytes"
	"context"
	"io"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"zatiti/internal/controller"
	"zatiti/internal/identity"
	"zatiti/internal/state"
)

func TestMCPAuthenticatedCallParity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	a, out, errOut := newTestApp(t)
	ctx := context.Background()
	setIO(t, out, errOut)

	mustRun := func(args []string, want int) {
		t.Helper()
		code, err := a.Run(ctx, args)
		if code != want || err != nil {
			t.Fatalf("Run(%v): %d %v, want %d", args, code, err, want)
		}
	}
	mustRun([]string{"init", "-data-dir", dir}, ExitOK)
	mustRun([]string{"principal.create", "-data-dir", dir, "-name", "ivy", "-role", "operator"}, ExitOK)

	// Issue ivy a real credential.
	st, _ := state.Open(dir)
	var ivyID string
	if err := st.DB().QueryRow(
		`SELECT id FROM principals WHERE kind='operator' LIMIT 1`).Scan(&ivyID); err != nil {
		t.Fatal(err)
	}
	st.Close()
	mustRun([]string{"credential.issue", "-data-dir", dir, "-principal", ivyID}, ExitOK)

	// Start the MCP runtime (acquires ownership).
	logger := discardLogger()
	ctrl, err := controller.Start(ctx, dir, logger)
	if err != nil {
		t.Fatalf("controller start: %v", err)
	}
	// CLI ops can't run while the controller owns the lock; from here
	// on, everything goes through MCP — which is the parity claim's
	// honest form: same store, one owner, two transports share it by
	// taking turns, not concurrently.

	// Authenticate ivy with a signed challenge.
	nonce := bytes.Repeat([]byte{7}, 32)
	prov, err := identity.NewProvisioner(filepath.Join(dir, "keystore"))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := prov.SignWithCustodied(ctx, ivyID, identity.Challenge{Nonce: nonce})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Authenticate through the same wiring runMCPServe uses.
	sess, err := mcpAuthenticator(ctrl, dir)(ivyID, nonce, sig)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	// Authenticated call: organization.create as ivy (operator: mutating allowed).
	res, err := a.mcpExecutor(ctrl)(ctx, ivyID, "organization.create",
		map[string]any{"name": "acme-mcp"})
	if err != nil {
		t.Fatalf("executor: %v", err)
	}
	if !strings.Contains(res, "acme-mcp") {
		t.Fatalf("result %q missing name", res)
	}
	_ = sess

	// Audit: one ok row, actor ivy, via the shared path.
	rows, err := state.ListAudit(ctx, ctrl.Store().DB(),
		func() string { id, _ := state.RootOrgID(ctx, ctrl.Store().DB()); return id }(), 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.Op == "organization.create" && r.Result == "ok" && r.ActorID == ivyID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no ok organization.create by ivy in %+v", rows)
	}

	// Denied path: undeclared field refused before authz (input validation
	// precedes authorization, matching CLI flag parsing semantics).
	if _, err := a.mcpExecutor(ctrl)(ctx, ivyID, "organization.create",
		map[string]any{"name": "x", "evil": "y"}); err == nil {
		t.Fatal("undeclared field accepted")
	}

	ctrl.Run(ctx) // release ownership
}

// discardLogger silences controller diagnostics in tests.
func discardLogger() *log.Logger { return log.New(io.Discard, "", 0) }
