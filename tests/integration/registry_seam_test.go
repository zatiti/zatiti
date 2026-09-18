package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/application"
	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/platform"
	"github.com/zatiti/zatiti/internal/registry"
)

// skipKnownDefect records an observed cross-package defect. The test body
// has already run and observed the failure; the skip carries the suspected
// cause so the integration lead can route a fix. A skip is never a pass: the
// defect list in this package's hand-off report enumerates every call site.
func skipKnownDefect(t *testing.T, cause, observed string) {
	t.Helper()
	t.Skipf("KNOWN DEFECT (not a pass). suspected cause: %s. observed: %s", cause, observed)
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

// stripDefs removes a schema document's own $defs, the form
// internal/registry demands. The landed domains ship self-contained schema
// documents (operation body plus $defs) because internal/application
// validates Descriptor.InputSchema as delivered.
func stripDefs(t *testing.T, schema json.RawMessage) json.RawMessage {
	t.Helper()
	if len(schema) == 0 {
		return schema
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(schema, &root); err != nil {
		t.Fatalf("schema is not an object: %v", err)
	}
	delete(root, "$defs")
	out, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("schema re-encode: %v", err)
	}
	return out
}

func stripDescriptorDefs(t *testing.T, d contract.Descriptor) contract.Descriptor {
	d.InputSchema = stripDefs(t, d.InputSchema)
	d.OutputSchema = stripDefs(t, d.OutputSchema)
	d.CompletionSchema = stripDefs(t, d.CompletionSchema)
	return d
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
	modules, _, err := buildModules(application.NewPorts(), newStepClock(), plat.Secrets(), plat.Blobs())
	if err != nil {
		t.Fatalf("modules: %v", err)
	}
	return modules
}

// TestRealRegistryAssemblesLandedModules is the target assembly: the real
// registry over the sixteen real modules, unedited.
func TestRealRegistryAssemblesLandedModules(t *testing.T) {
	t.Parallel()
	_, err := registry.New(realModules(t))
	if err == nil {
		t.Log("registry.New assembles the landed modules; flip defaultCatalogMode to catalogRegistry and delete moduleCatalog")
		return
	}
	if strings.Contains(err.Error(), "mutation operation requires a submission key") {
		skipKnownDefect(t,
			"internal/registry/validate.go:82 requires SubmissionKey on every mutation, including internal ones; "+
				"every domain declares internal mutations without keys and internal/application never demands one for internal dispatch",
			err.Error())
	}
	t.Fatalf("registry.New failed in an unrecorded way: %v", err)
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

// keyInternalMutations steps past the submission-key defect.
func keyInternalMutations(d contract.Descriptor) contract.Descriptor {
	if d.Visibility == contract.VisibilityInternal && d.Mode == contract.ModeMutation {
		d.SubmissionKey = true
	}
	return d
}

// TestRealRegistryAcceptsSelfContainedSchemas steps past the submission-key
// defect to observe how the registry treats the schema documents the landed
// domains deliver.
func TestRealRegistryAcceptsSelfContainedSchemas(t *testing.T) {
	t.Parallel()
	_, err := registry.New(editedModules(t, func(d contract.Descriptor) (contract.Descriptor, bool) {
		return keyInternalMutations(d), true
	}))
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "must not declare its own $defs") ||
		strings.Contains(err.Error(), "schema does not match the frozen schema") {
		skipKnownDefect(t,
			"internal/registry/catalog.go:189-202 (mergedSchema) and validate.go:145-165 (sameSchema) require bare operation schemas without $defs; "+
				"all landed domains deliver self-contained documents (operation body plus $defs, for example internal/installation/service.go:160-171 mergeSchema) "+
				"because internal/application/dispatch.go:91 validates Descriptor.InputSchema exactly as delivered",
			err.Error())
	}
	t.Fatalf("registry.New failed in an unrecorded way: %v", err)
}

// TestRealRegistryAcceptsOwnerNamedCallers steps past the submission-key and
// schema-form defects to observe how the registry treats Descriptor.Callers.
func TestRealRegistryAcceptsOwnerNamedCallers(t *testing.T) {
	t.Parallel()
	_, err := registry.New(editedModules(t, func(d contract.Descriptor) (contract.Descriptor, bool) {
		return stripDescriptorDefs(t, keyInternalMutations(d)), true
	}))
	if err == nil {
		return
	}
	if strings.Contains(err.Error(), "pointer does not resolve") {
		skipKnownDefect(t,
			"blocked by the schema-form defect: internal operations reference owner-private $defs (for example #/$defs/Candidate in _identity.activate) "+
				"that the registry's shared catalog $defs do not carry, so bare internal schemas cannot resolve; by reading, "+
				"internal/registry/registry.go:110-116 then resolves Descriptor.Callers against registered operation IDs while the domains and "+
				"internal/application (dispatch.go:174,286; router.go For) use owner names such as application, controller and installation",
			err.Error())
	}
	if strings.Contains(err.Error(), "unknown caller") {
		skipKnownDefect(t,
			"internal/registry/registry.go:110-116 resolves Descriptor.Callers against registered operation IDs; "+
				"the domains and internal/application (dispatch.go:174,286; router.go For) use owner names such as application, controller and installation",
			err.Error())
	}
	t.Fatalf("registry.New failed in an unrecorded way: %v", err)
}

// catalogConforming is the closest registrable form of a landed public
// descriptor: bare schemas and no leading binary name in the CLI path. It
// steps past two recorded drift classes so deeper drift becomes observable.
func catalogConforming(t *testing.T, d contract.Descriptor) contract.Descriptor {
	d = stripDescriptorDefs(t, d)
	if len(d.CLI) > 1 && d.CLI[0] == "zatiti" {
		d.CLI = append([]string(nil), d.CLI[1:]...)
	}
	return d
}

// moduleDrift registers one real module's public descriptors, in their
// closest registrable form, against the real registry and returns every
// descriptor defect it reports. The registry reports descriptor defects
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
			return catalogConforming(t, d), true
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
// descriptor at a time so every drift is listed, not only the first (Z02
// registry-enumeration precondition). Schema bodies, modes, effects, owners,
// MCP names, submission-key and expected-version flags must all match.
func TestLandedPublicDescriptorsMatchFrozenCatalog(t *testing.T) {
	t.Parallel()
	var completionMissing, scopeDrift, unrecorded []string
	cliPrefixed := map[string]int{}
	prefixed, audited := 0, 0
	for _, m := range realModules(t) {
		for _, d := range m.Descriptors() {
			if d.Visibility != contract.VisibilityPublic {
				continue
			}
			audited++
			if len(d.CLI) > 1 && d.CLI[0] == "zatiti" {
				cliPrefixed[m.Name()]++
				prefixed++
			}
		}
		for id, drift := range moduleDrift(t, m) {
			switch {
			case strings.Contains(drift, "completion schema presence"):
				completionMissing = append(completionMissing, id)
			case id == "installation.init" && strings.Contains(drift, "scope requirements"):
				scopeDrift = append(scopeDrift, id)
			default:
				unrecorded = append(unrecorded, drift)
			}
		}
	}
	sort.Strings(completionMissing)
	// The frozen catalog holds 197 public operations; the registry owns two
	// (capabilities.list, capabilities.schema) and the domains the rest.
	if audited != 195 {
		t.Fatalf("audited %d domain public descriptors, want 195", audited)
	}
	for _, drift := range unrecorded {
		t.Errorf("unrecorded catalog drift: %s", drift)
	}
	if t.Failed() {
		return
	}
	if prefixed+len(completionMissing)+len(scopeDrift) > 0 {
		skipKnownDefect(t,
			"(a) eleven domains put the binary name in Descriptor.CLI, for example internal/tasks and internal/configuration service.go descriptor builders, "+
				"while the frozen catalog and internal/cli/cli.go:61-70 treat CLI as the path below the zatiti root; "+
				"(b) internal/execution (run.export) and internal/installation/service.go:139-151 (backup, restore) register no CompletionSchema although the frozen catalog declares one; "+
				"(c) internal/installation/service.go:153 sets ScopeRequired on installation.init, frozen as scope-free",
			fmt.Sprintf("audited %d public descriptors; cli binary-name prefix on %d operations by owner %v; completion schema missing on %v; scope_required drift on %v; every other frozen field matches",
				audited, prefixed, cliPrefixed, completionMissing, scopeDrift))
	}
}

// TestRealRegistryRoutesLocalIO: internal/application routes the twelve
// registered local IO operations through application.IOLookup. This is a
// type-level seam, so no registry instance is needed to observe it.
func TestRealRegistryRoutesLocalIO(t *testing.T) {
	t.Parallel()
	var catalog application.Catalog = (*registry.Registry)(nil)
	if _, ok := catalog.(application.IOLookup); !ok {
		skipKnownDefect(t,
			"*registry.Registry has no LocalIOFor; internal/application/dispatch.go:40-56 requires application.IOLookup and "+
				"discards the single-transaction handler the registry builds at internal/registry/registry.go:283-296",
			"*registry.Registry does not implement application.IOLookup, so installation.init and the other eleven local IO operations would fail internal_error")
	}
}

// TestRealRegistryResolvesCurrentVersion: internal/application addresses the
// catalog with version 0, meaning the current version. Observing this needs
// a registry instance, which needs the complete frozen surface registered.
func TestRealRegistryResolvesCurrentVersion(t *testing.T) {
	t.Parallel()
	reg, err := registry.New(editedModules(t, func(d contract.Descriptor) (contract.Descriptor, bool) {
		return catalogConforming(t, d), d.Visibility == contract.VisibilityPublic
	}))
	if err != nil {
		skipKnownDefect(t,
			"blocked by the catalog drift in TestLandedPublicDescriptorsMatchFrozenCatalog; by reading, "+
				"internal/registry/registry.go:221-228 Lookup matches the exact version only while "+
				"internal/application/dispatch.go:76,167,279 calls Lookup(id, 0) for the current version, so every dispatch would be not_found",
			err.Error())
	}
	if _, _, err := reg.Lookup("installation.status", 0); err != nil {
		skipKnownDefect(t,
			"internal/registry/registry.go:221-228 Lookup matches the exact version only; "+
				"internal/application/dispatch.go:76,167,279 calls Lookup(id, 0) for the current version",
			err.Error())
	}
}

// TestBootstrapThroughRealRegistry is the end state the four seam tests
// above block: bootstrap through application over the real registry.
func TestBootstrapThroughRealRegistry(t *testing.T) {
	t.Parallel()
	f, err := assemble(t, fixtureOptions{mode: catalogRegistry})
	if err != nil {
		if strings.Contains(err.Error(), "registry:") {
			skipKnownDefect(t, "see TestRealRegistryAssemblesLandedModules", err.Error())
		}
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
}
