package integration_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/application"
	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/platform"
	"github.com/zatiti/zatiti/internal/registry"
)

// failRegressedDefect fails a test that has just re-observed a cross-package
// defect main already fixed. Each call site once skipped with this evidence
// while the defect was open; now that every recorded cause is fixed, the
// evidence branch is a regression report, never a skip, so no case in this
// package can pass by skipping a path it claims to cover.
func failRegressedDefect(t *testing.T, cause, observed string) {
	t.Helper()
	t.Fatalf("REGRESSED DEFECT. original cause: %s. observed: %s", cause, observed)
}

// descriptorView presents a real module with an edited descriptor list. The
// seam tests use it only to get past one registry defect so the next one can
// be observed; Handle and the LocalIO seam stay the real module's.
type descriptorView struct {
	contract.Module
	descs []contract.Descriptor
}

func (v descriptorView) Descriptors() []contract.Descriptor { return v.descs }

// localIOView keeps the LocalIO seam visible through a descriptorView.
type localIOView struct {
	descriptorView
	contract.LocalIO
}

func viewOf(m contract.Module, edit func(contract.Descriptor) (contract.Descriptor, bool)) contract.Module {
	var descs []contract.Descriptor
	for _, d := range m.Descriptors() {
		if out, keep := edit(d); keep {
			descs = append(descs, out)
		}
	}
	view := descriptorView{Module: m, descs: descs}
	if local, ok := m.(contract.LocalIO); ok {
		return localIOView{descriptorView: view, LocalIO: local}
	}
	return view
}

// realModules constructs the sixteen landed modules over real platform
// custody without opening storage: descriptor-level tests need no database.
func realModules(t *testing.T) []contract.Module {
	t.Helper()
	plat, err := platform.Open(platform.Config{
		StateDir: filepath.Join(t.TempDir(), "state"), CredentialBackend: "headless", MasterKeyRef: writeMasterKey(t),
	})
	if err != nil {
		t.Fatalf("platform: %v", err)
	}
	t.Cleanup(func() { _ = plat.Close() })
	modules, _, err := buildModules(application.NewPorts(), newStepClock(), plat.Secrets(), plat.Blobs(), nil)
	if err != nil {
		t.Fatalf("modules: %v", err)
	}
	return modules
}

// TestRealRegistryAssemblesLandedModules: the real registry assembles the
// sixteen real modules unedited and exposes the whole frozen public
// surface.
func TestRealRegistryAssemblesLandedModules(t *testing.T) {
	t.Parallel()
	reg, err := registry.New(realModules(t))
	if err != nil {
		t.Fatalf("registry.New over the landed modules: %v", err)
	}
	if n := len(reg.Public()); n != 197 {
		t.Fatalf("registry exposes %d public operations, the frozen catalog holds 197", n)
	}
}

// editedModules applies edit to every descriptor of every real module.
func editedModules(t *testing.T, edit func(contract.Descriptor) (contract.Descriptor, bool)) []contract.Module {
	t.Helper()
	var modules []contract.Module
	for _, m := range realModules(t) {
		modules = append(modules, viewOf(m, edit))
	}
	return modules
}

// moduleDrift registers one real module's public descriptors, exactly as
// delivered, against the real registry and returns every descriptor defect
// it reports. The registry reports descriptor defects
// before catalog completeness and one at a time, so each reported operation
// is set aside and the rest retried; a completeness error means every
// remaining descriptor matches the frozen catalog.
func moduleDrift(t *testing.T, m contract.Module) map[string]string {
	t.Helper()
	drift := map[string]string{}
	for {
		view := viewOf(m, func(d contract.Descriptor) (contract.Descriptor, bool) {
			if _, seen := drift[d.ID]; seen || d.Visibility != contract.VisibilityPublic {
				return d, false
			}
			return d, true
		})
		_, err := registry.New([]contract.Module{view})
		if err == nil || strings.Contains(err.Error(), "is not registered by any module") {
			return drift
		}
		id := ""
		for _, d := range view.Descriptors() {
			if strings.Contains(err.Error(), "operation "+d.ID+":") || strings.Contains(err.Error(), fmt.Sprintf("operation %q", d.ID)) {
				id = d.ID
			}
		}
		if id == "" {
			t.Fatalf("module %s: registry error names no registered operation: %v", m.Name(), err)
		}
		drift[id] = err.Error()
	}
}

// TestLandedPublicDescriptorsMatchFrozenCatalog audits every landed public
// descriptor against the frozen catalog through the real registry, one
// module at a time so every drift is listed, not only the first (Z02
// registry-enumeration precondition). Schema bodies, modes, effects, owners,
// CLI paths, MCP names, scope requirements, submission-key and
// expected-version flags must all match; any drift is a real report.
func TestLandedPublicDescriptorsMatchFrozenCatalog(t *testing.T) {
	t.Parallel()
	audited := 0
	for _, m := range realModules(t) {
		for _, d := range m.Descriptors() {
			if d.Visibility == contract.VisibilityPublic {
				audited++
			}
		}
		for id, drift := range moduleDrift(t, m) {
			t.Errorf("operation %s drifts from the frozen catalog: %s", id, drift)
		}
	}
	// The frozen catalog holds 197 public operations; the registry owns two
	// (capabilities.list, capabilities.schema) and the domains the rest.
	if audited != 195 {
		t.Fatalf("audited %d domain public descriptors, want 195", audited)
	}
}

// TestRealRegistryRoutesLocalIO: internal/application routes the twelve
// registered local IO operations through application.IOLookup. This is a
// type-level seam, so no registry instance is needed to observe it.
func TestRealRegistryRoutesLocalIO(t *testing.T) {
	t.Parallel()
	var catalog application.Catalog = (*registry.Registry)(nil)
	if _, ok := catalog.(application.IOLookup); !ok {
		t.Fatal("*registry.Registry does not implement application.IOLookup; internal/application/dispatch.go requires it for the twelve local IO operations")
	}
}

// TestRealRegistryResolvesCurrentVersion: internal/application addresses the
// catalog with version 0, meaning the current version. Observing this needs
// a registry instance, which needs the complete frozen surface registered.
func TestRealRegistryResolvesCurrentVersion(t *testing.T) {
	t.Parallel()
	reg, err := registry.New(editedModules(t, func(d contract.Descriptor) (contract.Descriptor, bool) {
		return d, d.Visibility == contract.VisibilityPublic
	}))
	if err != nil {
		t.Fatalf("real public descriptors do not assemble: %v", err)
	}
	if _, _, err := reg.Lookup("installation.status", 0); err != nil {
		t.Fatalf("Lookup(id, 0) must resolve the current version, as internal/application/dispatch.go addresses the catalog: %v", err)
	}
}

// TestBootstrapThroughRealRegistry: bootstrap through application over the
// real registry, and the registry-owned capabilities catalog is served
// through the same dispatcher afterwards.
func TestBootstrapThroughRealRegistry(t *testing.T) {
	t.Parallel()
	f, err := assemble(t, fixtureOptions{})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	res, err := f.app.Invoke(context.Background(), bootstrapActor(), "installation.init",
		contract.Request{Schema: contract.SchemaRequest, Input: initInput()})
	if err != nil {
		t.Fatalf("installation.init through the real registry: %v", err)
	}
	if res.Status != contract.StatusCompleted {
		t.Fatalf("installation.init status %q", res.Status)
	}
	f.owner = f.authenticate(f.ownerToken())
	caps, err := f.invoke(f.owner, "capabilities.list", "", map[string]any{})
	if err != nil {
		t.Fatalf("capabilities.list through the real registry: %v", err)
	}
	if !strings.Contains(string(caps.Data), `"installation.status"`) {
		t.Fatalf("capabilities.list does not enumerate the public surface: %.300s", caps.Data)
	}
}
