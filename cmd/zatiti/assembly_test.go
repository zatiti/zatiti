package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/application"
	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/platform"
)

// TestBackupWrapperMethodSetIsExactlyBackup pins the installation seam: the
// value handed to installation exposes Backup and nothing else, and is not
// the Database.
func TestBackupWrapperMethodSetIsExactlyBackup(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeOf(databaseBackup{})
	if typ.NumMethod() != 1 || typ.Method(0).Name != "Backup" {
		names := make([]string, 0, typ.NumMethod())
		for i := 0; i < typ.NumMethod(); i++ {
			names = append(names, typ.Method(i).Name)
		}
		t.Fatalf("databaseBackup exposes %v, want exactly [Backup]", names)
	}
	var capability contract.DatabaseBackup = databaseBackup{}
	if _, wider := capability.(contract.Database); wider {
		t.Fatal("the backup capability must not widen to contract.Database")
	}
	if _, reader := capability.(contract.Reader); reader {
		t.Fatal("the backup capability must not widen to contract.Reader")
	}
	if _, err := bindInstallationBackup(contract.Dependencies{Clock: systemClock{}, IDs: randomIDs{}}, nil); err == nil {
		t.Fatal("a nil capability wrapper must be refused at assembly")
	}
}

// TestModulesFollowTheAssemblyOrder: sixteen real modules, constructed in
// the frozen order, identity first as the authenticator, without any
// platform, storage or goroutine.
func TestModulesFollowTheAssemblyOrder(t *testing.T) {
	t.Parallel()
	router := application.NewPorts()
	mods, auth, jobRunners, err := modules(router, systemClock{}, randomIDs{}, nil, inertBlobs{}, databaseBackup{})
	if err != nil {
		t.Fatalf("modules: %v", err)
	}
	if len(mods) != len(moduleOrder) {
		t.Fatalf("%d modules, want %d", len(mods), len(moduleOrder))
	}
	for i, m := range mods {
		if m.Name() != moduleOrder[i] {
			t.Fatalf("module %d is %s, want %s", i, m.Name(), moduleOrder[i])
		}
	}
	if auth == nil || mods[0].Name() != "identity" {
		t.Fatal("identity is not the first module and the authenticator")
	}
	// Every landedJobKinds owner (execution, skills, configuration) must
	// have been discovered generically as a contract.LocalJobRunner.
	for _, owner := range []string{"execution", "skills", "configuration"} {
		if _, ok := jobRunners[owner]; !ok {
			t.Fatalf("modules() did not discover %s as a contract.LocalJobRunner", owner)
		}
	}
	// artifacts is deliberately NOT a LocalJobRunner on this tree (P24's
	// verified catalog gap, see jobs.go's catalogJobKinds doc comment); if
	// this ever starts failing, catalogJobKinds/landedJobKinds must be
	// updated in the same change that lands internal/artifacts' RunJob.
	if _, ok := jobRunners["artifacts"]; ok {
		t.Fatal("internal/artifacts now implements contract.LocalJobRunner; update jobs.go's landedJobKinds/catalogJobKinds to attach its runner instead of leaving artifact.export unattached")
	}
	// Ports are unbound before Bind: no module can call a peer during
	// construction or before the application exists.
	if _, err := router.For("identity").Call(context.Background(), nil, contract.Invocation{Operation: "_identity.authority"}); err == nil {
		t.Fatal("unbound ports accepted a call")
	}
}

func TestInertBlobsFailClosed(t *testing.T) {
	t.Parallel()
	var blobs contract.BlobStore = inertBlobs{}
	if _, _, _, err := blobs.Stage(context.Background(), strings.NewReader("x"), 1); faultCode(err) != contract.CodeControllerUnavailable {
		t.Fatalf("Stage: %v", err)
	}
	if _, err := blobs.Open(context.Background(), "", 0, 1); faultCode(err) != contract.CodeControllerUnavailable {
		t.Fatalf("Open: %v", err)
	}
}

// TestCatalogIsTheFrozenPublicCatalog derives the CLI/MCP catalog exactly as
// the controller registers it: the 205 frozen public operations, every one
// with CLI and MCP mappings.
func TestCatalogIsTheFrozenPublicCatalog(t *testing.T) {
	t.Parallel()
	descs, err := catalog()
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if len(descs) != 205 {
		t.Fatalf("catalog holds %d operations, want 205", len(descs))
	}
	seen := map[string]bool{}
	for _, d := range descs {
		if d.Visibility != contract.VisibilityPublic || len(d.CLI) == 0 || d.MCP == "" {
			t.Fatalf("%s is not a complete public descriptor: %+v", d.ID, d)
		}
		seen[d.ID] = true
	}
	for _, want := range []string{bootstrapOperation, "principal.create", "principal.list", "capabilities.list"} {
		if !seen[want] {
			t.Fatalf("catalog lacks %s", want)
		}
	}
}

// TestOpenInstallationHoldsTheLockAndAdvancesGeneration proves the startup
// order through its observable effects: the lock is exclusive for the
// handle's lifetime, the generation advanced exactly once per open, and the
// database is uninitialized until bootstrap.
func TestOpenInstallationHoldsTheLockAndAdvancesGeneration(t *testing.T) {
	cfg := serveConfig(t)
	h := openTestInstallation(t, cfg)
	if h.generation != 1 || !h.own.Held() {
		t.Fatalf("generation %d held %v after first open", h.generation, h.own.Held())
	}
	if _, err := openInstallation(context.Background(), cfg); platform.Code(err) != contract.CodeControllerUnavailable {
		t.Fatalf("second open while held: %v, want controller_unavailable", err)
	}
	if id, err := h.initialized(context.Background()); err != nil || id != "" {
		t.Fatalf("fresh installation reports initialized %q, %v", id, err)
	}
	h.close()
	again, err := openInstallation(context.Background(), cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer again.close()
	if again.generation != 2 {
		t.Fatalf("generation after reopen = %d, want 2", again.generation)
	}
}
