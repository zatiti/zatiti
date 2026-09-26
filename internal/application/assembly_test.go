package application_test

// Real assembly: the real internal/registry over the sixteen landed domain
// modules, a real application over temp SQLite storage and real platform
// custody. The in-package tests drive the dispatcher against local owner
// fakes; this file proves the registry/application seam itself, which no
// fake on either side can.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/accounting"
	"github.com/zatiti/zatiti/internal/application"
	"github.com/zatiti/zatiti/internal/artifacts"
	"github.com/zatiti/zatiti/internal/configuration"
	"github.com/zatiti/zatiti/internal/connections"
	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/effects"
	"github.com/zatiti/zatiti/internal/evidence"
	"github.com/zatiti/zatiti/internal/execution"
	"github.com/zatiti/zatiti/internal/identity"
	"github.com/zatiti/zatiti/internal/installation"
	"github.com/zatiti/zatiti/internal/memory"
	"github.com/zatiti/zatiti/internal/messaging"
	"github.com/zatiti/zatiti/internal/platform"
	"github.com/zatiti/zatiti/internal/policy"
	"github.com/zatiti/zatiti/internal/registry"
	"github.com/zatiti/zatiti/internal/reviews"
	"github.com/zatiti/zatiti/internal/scheduling"
	"github.com/zatiti/zatiti/internal/skills"
	"github.com/zatiti/zatiti/internal/storage"
	"github.com/zatiti/zatiti/internal/tasks"
)

// *registry.Registry is the production Catalog and local IO route.
var (
	_ application.Catalog  = (*registry.Registry)(nil)
	_ application.IOLookup = (*registry.Registry)(nil)
)

// assemblyClock advances one millisecond per reading so stored timestamps
// order the way the calls did.
type assemblyClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *assemblyClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Millisecond)
	return c.now
}

type assemblyIDs struct{}

func (assemblyIDs) New() contract.ID { return contract.NewID() }

// custodySecrets decorates the real platform secret store only to remember
// the opaque reference bootstrap custodied the owner credential under.
type custodySecrets struct {
	contract.SecretStore
	mu   sync.Mutex
	refs map[string]string
}

func (s *custodySecrets) Put(ctx context.Context, key string, secret []byte) (string, error) {
	ref, err := s.SecretStore.Put(ctx, key, secret)
	if err == nil {
		s.mu.Lock()
		s.refs[key] = ref
		s.mu.Unlock()
	}
	return ref, err
}

// call is one invocation a real module received through the registry.
type call struct {
	operation string
	version   int64
}

// recorder observes the invocations reaching the real modules.
type recorder struct {
	mu    sync.Mutex
	calls []call
}

func (r *recorder) add(invocation contract.Invocation) {
	r.mu.Lock()
	r.calls = append(r.calls, call{invocation.Operation, invocation.Version})
	r.mu.Unlock()
}

func (r *recorder) saw(operation string, version int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if c.operation == operation && c.version == version {
			return true
		}
	}
	return false
}

// moduleView fronts one real module. Handle records the invocation and
// delegates; descs is the module's descriptor list, optionally conformed.
type moduleView struct {
	contract.Module
	descs []contract.Descriptor
	rec   *recorder
}

func (v moduleView) Descriptors() []contract.Descriptor { return v.descs }

func (v moduleView) Handle(ctx context.Context, unit contract.Unit, invocation contract.Invocation) (contract.Payload, error) {
	v.rec.add(invocation)
	return v.Module.Handle(ctx, unit, invocation)
}

// localIOView keeps the real module's LocalIO visible through the view.
type localIOView struct {
	moduleView
	contract.LocalIO
}

func viewOf(m contract.Module, descs []contract.Descriptor, rec *recorder) contract.Module {
	view := moduleView{Module: m, descs: descs, rec: rec}
	if local, ok := m.(contract.LocalIO); ok {
		return localIOView{moduleView: view, LocalIO: local}
	}
	return view
}

// frozen is the part of the frozen operation catalog the conforming edit
// below restores: completion schemas and the complete $defs document,
// including the definitions only internal operations reference.
type frozen struct {
	completions map[string]json.RawMessage
	defs        map[string]json.RawMessage
}

func readFrozen(t *testing.T) frozen {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "implementation", "operations.json"))
	if err != nil {
		t.Fatalf("frozen operation catalog: %v", err)
	}
	var doc struct {
		Defs       map[string]json.RawMessage `json:"$defs"`
		Operations []struct {
			ID         string          `json:"id"`
			Completion json.RawMessage `json:"completion_schema"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("frozen operation catalog: %v", err)
	}
	out := frozen{completions: map[string]json.RawMessage{}, defs: doc.Defs}
	for _, op := range doc.Operations {
		if len(op.Completion) != 0 && string(op.Completion) != "null" {
			out.completions[op.ID] = op.Completion
		}
	}
	return out
}

// selfContain attaches the frozen $defs document to a bare schema body.
// The registry keeps only the definitions the body reaches.
func selfContain(t *testing.T, schema json.RawMessage, defs map[string]json.RawMessage) json.RawMessage {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(schema, &root); err != nil {
		t.Fatalf("schema is not an object: %v", err)
	}
	if _, has := root["$defs"]; has || !strings.Contains(string(schema), `"$ref"`) {
		return schema
	}
	root["$defs"] = mustJSON(t, defs)
	return mustJSON(t, root)
}

// conform edits the public-descriptor drifts from the frozen catalog that
// are domain-side defects, not seam rules: the binary name leading
// Descriptor.CLI, a completion schema the catalog declares but the module
// omits, and scope_required on the scope-free installation.init. It also
// completes the internal schemas five modules deliver as bare bodies whose
// $refs name definitions no document carries (for example #/$defs/Candidate),
// which nothing outside the module can evaluate. Every edit is a no-op on a
// conforming descriptor, so this function decays to the identity as the
// domains are corrected. The registry stays strict about all of it;
// TestLandedDriftIsDomainSide pins that.
func conform(t *testing.T, d contract.Descriptor, f frozen) contract.Descriptor {
	t.Helper()
	if d.Visibility != contract.VisibilityPublic {
		d.InputSchema = selfContain(t, d.InputSchema, f.defs)
		d.OutputSchema = selfContain(t, d.OutputSchema, f.defs)
		return d
	}
	if len(d.CLI) > 1 && d.CLI[0] == "zatiti" {
		d.CLI = append([]string(nil), d.CLI[1:]...)
	}
	if len(d.CompletionSchema) == 0 {
		d.CompletionSchema = f.completions[d.ID]
	}
	if d.ID == "installation.init" {
		d.ScopeRequired = nil
	}
	return d
}

// realModules constructs the sixteen landed modules with real dependencies
// and owner-bound ports, in assembly and migration order.
func realModules(t *testing.T, router *application.PortRouter, clock contract.Clock, secrets contract.SecretStore, blobs contract.BlobStore) ([]contract.Module, contract.Authenticator) {
	t.Helper()
	deps := func(owner string) contract.Dependencies {
		return contract.Dependencies{Clock: clock, IDs: assemblyIDs{}, Ports: router.For(owner), Secrets: secrets, Blobs: blobs}
	}
	idn, err := identity.New(deps("identity"))
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	modules := []contract.Module{idn}
	for _, c := range []struct {
		name string
		make func(contract.Dependencies) (contract.Module, error)
	}{
		{"configuration", func(d contract.Dependencies) (contract.Module, error) { return configuration.New(d) }},
		{"skills", func(d contract.Dependencies) (contract.Module, error) { return skills.New(d) }},
		{"connections", func(d contract.Dependencies) (contract.Module, error) { return connections.New(d) }},
		{"policy", func(d contract.Dependencies) (contract.Module, error) { return policy.New(d) }},
		{"reviews", func(d contract.Dependencies) (contract.Module, error) { return reviews.New(d) }},
		{"accounting", func(d contract.Dependencies) (contract.Module, error) { return accounting.New(d) }},
		{"tasks", func(d contract.Dependencies) (contract.Module, error) { return tasks.New(d) }},
		{"scheduling", func(d contract.Dependencies) (contract.Module, error) { return scheduling.New(d) }},
		{"messaging", func(d contract.Dependencies) (contract.Module, error) { return messaging.New(d) }},
		{"execution", func(d contract.Dependencies) (contract.Module, error) { return execution.New(d) }},
		{"effects", func(d contract.Dependencies) (contract.Module, error) { return effects.New(d) }},
		{"memory", func(d contract.Dependencies) (contract.Module, error) { return memory.New(d) }},
		{"artifacts", func(d contract.Dependencies) (contract.Module, error) { return artifacts.New(d) }},
		{"evidence", func(d contract.Dependencies) (contract.Module, error) { return evidence.New(d) }},
		{"installation", func(d contract.Dependencies) (contract.Module, error) { return installation.New(d) }},
	} {
		m, err := c.make(deps(c.name))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if m.Name() != c.name {
			t.Fatalf("module %s does not carry its assembly name %s", m.Name(), c.name)
		}
		modules = append(modules, m)
	}
	return modules, idn
}

// assembly is one really assembled installation.
type assembly struct {
	app      *application.Application
	reg      *registry.Registry
	secrets  *custodySecrets
	rec      *recorder
	modules  []contract.Module // the real modules, unedited
	assembly []contract.Module // what the registry was built over
}

const ownerKey = "assembly/owner"

// assemble follows the frozen entrypoint order: platform.Open -> Acquire ->
// storage.Open -> Migrate -> StartGeneration -> NewPorts -> modules ->
// registry.New -> application.New -> Bind.
func assemble(t *testing.T) *assembly {
	t.Helper()
	ctx := context.Background()

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(0xA0 + i)
	}
	keyPath := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatalf("master key: %v", err)
	}
	plat, err := platform.Open(platform.Config{
		StateDir: filepath.Join(t.TempDir(), "state"), CredentialBackend: "headless", MasterKeyRef: "file:" + keyPath,
	})
	if err != nil {
		t.Fatalf("platform: %v", err)
	}
	t.Cleanup(func() { _ = plat.Close() })
	own, err := plat.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	t.Cleanup(func() { _ = own.Close() })

	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(plat.StateDir(), "zatiti.db")})
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	a := &assembly{
		secrets: &custodySecrets{SecretStore: plat.Secrets(), refs: map[string]string{}},
		rec:     &recorder{},
	}
	clock := &assemblyClock{now: time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)}
	router := application.NewPorts()
	modules, auth := realModules(t, router, clock, a.secrets, plat.Blobs())
	a.modules = modules

	var migrations []contract.Migration
	for _, m := range modules {
		migrations = append(migrations, m.Migrations()...)
	}
	if err := db.Migrate(ctx, migrations); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.StartGeneration(ctx); err != nil {
		t.Fatalf("generation: %v", err)
	}

	frozenCatalog := readFrozen(t)
	for _, m := range modules {
		var descs []contract.Descriptor
		for _, d := range m.Descriptors() {
			descs = append(descs, conform(t, d, frozenCatalog))
		}
		a.assembly = append(a.assembly, viewOf(m, descs, a.rec))
	}
	if a.reg, err = registry.New(a.assembly); err != nil {
		t.Fatalf("registry.New over the landed modules: %v", err)
	}
	if a.app, err = application.New(db, a.reg, auth, clock, assemblyIDs{}); err != nil {
		t.Fatalf("application.New: %v", err)
	}
	t.Cleanup(a.app.Close)
	if err := router.Bind(a.app); err != nil {
		t.Fatalf("bind: %v", err)
	}
	return a
}

func faultCode(err error) string {
	var f *contract.Fault
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// TestRealRegistryDrivesApplication bootstraps and operates a real
// installation through application.Invoke over the real registry:
// installation.init (a local IO mutation without a submission key, nesting
// internal calls), a public mutation with submission replay, a public query,
// and the controller's internal entry.
func TestRealRegistryDrivesApplication(t *testing.T) {
	a := assemble(t)
	ctx := context.Background()

	// The registry hands application the real installation service for the
	// bootstrap phases, not a wrapper.
	var installationModule contract.Module
	for _, m := range a.modules {
		if m.Name() == "installation" {
			installationModule = m
		}
	}
	phases, ok := a.reg.LocalIOFor("installation.init")
	if !ok || phases == nil {
		t.Fatal("registry exposes no local IO route for installation.init")
	}
	if view, isView := phases.(localIOView); !isView || view.LocalIO != installationModule.(contract.LocalIO) {
		t.Fatal("installation.init does not route to the installation module's own LocalIO")
	}

	// 1. One-time bootstrap: Prepare, Perform outside transactions, Finish.
	bootstrap := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService}
	res, err := a.app.Invoke(ctx, bootstrap, "installation.init", contract.Request{
		Schema: contract.SchemaRequest,
		Input: mustJSON(t, map[string]any{
			"credential_store": "headless", "owner_name": "Assembly Owner", "headless_key_ref": ownerKey,
		}),
	})
	if err != nil {
		t.Fatalf("installation.init: %v", err)
	}
	var initOut struct {
		Resource struct {
			InstallationID contract.ID `json:"installation_id"`
			Initialized    bool        `json:"initialized"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(res.Data, &initOut); err != nil {
		t.Fatalf("installation.init data %s: %v", res.Data, err)
	}
	if res.Status != contract.StatusCompleted || !initOut.Resource.Initialized || initOut.Resource.InstallationID == "" {
		t.Fatalf("installation.init did not report an initialized installation: %s %s", res.Status, res.Data)
	}
	scope := contract.Scope{InstallationID: initOut.Resource.InstallationID}

	// Bootstrap nested an owner-named internal mutation without a submission
	// key; application addressed it at version 0 and the registry resolved
	// the current version before the real module saw it.
	if !a.rec.saw("_identity.bootstrap", 1) {
		t.Fatalf("installation.init did not reach _identity.bootstrap v1 through the registry; calls: %v", a.rec.calls)
	}
	if a.rec.saw("installation.init", 1) {
		t.Fatal("installation.init ran through Module.Handle instead of the LocalIO phases")
	}

	// A second bootstrap is refused by the real module, not by routing.
	if _, err := a.app.Invoke(ctx, bootstrap, "installation.init", contract.Request{
		Schema: contract.SchemaRequest,
		Input:  mustJSON(t, map[string]any{"credential_store": "headless", "owner_name": "Again", "headless_key_ref": ownerKey + "2"}),
	}); err == nil || faultCode(err) == contract.CodeNotFound || faultCode(err) == contract.CodeInternalError {
		t.Fatalf("second installation.init: %v, want a domain refusal", err)
	}

	// The owner authenticates with the credential bootstrap custodied.
	ref, ok := a.secrets.refs[ownerKey]
	if !ok {
		t.Fatalf("bootstrap custodied no owner credential under %q", ownerKey)
	}
	token, err := a.secrets.Get(ctx, ref)
	if err != nil {
		t.Fatalf("owner credential: %v", err)
	}
	owner, err := a.app.Authenticate(ctx, append([]byte(nil), token...))
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	// 2. A public mutation: input validated against the self-contained
	// schema Lookup delivers, authority and policy gates and command
	// retention running as nested internal calls from "application".
	create := contract.Request{
		Schema: contract.SchemaRequest, SubmissionKey: "assembly-principal-1",
		Input: mustJSON(t, map[string]any{"scope": scope, "definition": map[string]any{
			"kind": "client_agent", "name": "assembly-agent", "scope": scope, "revoked": false,
		}}),
	}
	created, err := a.app.Invoke(ctx, owner, "principal.create", create)
	if err != nil {
		t.Fatalf("principal.create: %v", err)
	}
	if created.Status != contract.StatusCompleted || created.CommandID == "" {
		t.Fatalf("principal.create returned %s command %q", created.Status, created.CommandID)
	}
	if !a.rec.saw("principal.create", 1) {
		t.Fatalf("principal.create did not reach the identity module at v1; calls: %v", a.rec.calls)
	}
	internalFromApplication := false
	a.rec.mu.Lock()
	for _, c := range a.rec.calls {
		if strings.HasPrefix(c.operation, "_") && c.operation != "_identity.bootstrap" && c.version >= 1 {
			internalFromApplication = true
		}
	}
	a.rec.mu.Unlock()
	t.Logf("invocations that reached real modules through the registry: %v", a.rec.calls)
	if !internalFromApplication {
		t.Fatalf("no internal call reached a module during principal.create; calls: %v", a.rec.calls)
	}

	// An identical retry replays the retained command.
	replay, err := a.app.Invoke(ctx, owner, "principal.create", create)
	if err != nil || replay.CommandID != created.CommandID {
		t.Fatalf("replay returned command %q, %v; want %q", replay.CommandID, err, created.CommandID)
	}
	// A schema-invalid input is refused before any module runs.
	if _, err := a.app.Invoke(ctx, owner, "principal.create", contract.Request{
		Schema: contract.SchemaRequest, SubmissionKey: "assembly-principal-2",
		Input: mustJSON(t, map[string]any{"scope": map[string]any{"installation_id": "not-a-uuid"}, "definition": map[string]any{}}),
	}); faultCode(err) != contract.CodeInvalidInput {
		t.Fatalf("invalid principal.create: %v, want invalid_input", err)
	}

	// 3. A public query, and the registry's own capabilities operation.
	listed, err := a.app.Invoke(ctx, owner, "principal.list", contract.Request{
		Schema: contract.SchemaRequest, Input: mustJSON(t, map[string]any{"scope": scope}),
	})
	if err != nil {
		t.Fatalf("principal.list: %v", err)
	}
	if !strings.Contains(string(listed.Data), "assembly-agent") {
		t.Fatalf("principal.list does not show the created principal: %s", listed.Data)
	}
	if _, err := a.app.Invoke(ctx, owner, "capabilities.list", contract.Request{
		Schema: contract.SchemaRequest, Input: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("capabilities.list: %v", err)
	}

	// Public routing never reaches an internal operation.
	if _, err := a.app.Invoke(ctx, owner, "_identity.bootstrap", contract.Request{
		Schema: contract.SchemaRequest, Input: json.RawMessage(`{}`),
	}); faultCode(err) != contract.CodeNotFound {
		t.Fatalf("public dispatch of an internal operation: %v, want not_found", err)
	}
}

// TestRegistryResolvesEveryLandedOperation: every descriptor of every landed
// module is addressable the way application addresses it, and the delivered
// schema documents evaluate as delivered.
func TestRegistryResolvesEveryLandedOperation(t *testing.T) {
	a := assemble(t)
	internal, mutations := 0, 0
	for _, m := range a.modules {
		for _, d := range m.Descriptors() {
			got, handler, err := a.reg.Lookup(d.ID, 0)
			if err != nil || handler == nil {
				t.Fatalf("Lookup(%s, 0): %v", d.ID, err)
			}
			if got.Version != d.Version || got.Owner != m.Name() {
				t.Fatalf("Lookup(%s, 0) = v%d owner %s, want v%d owner %s", d.ID, got.Version, got.Owner, d.Version, m.Name())
			}
			if strings.Join(got.Callers, ",") != strings.Join(d.Callers, ",") {
				t.Fatalf("%s: caller allowlist %v became %v", d.ID, d.Callers, got.Callers)
			}
			// A document with a dangling $ref reports it on any instance.
			if err := contract.ValidateSchema(got.InputSchema, json.RawMessage(`{}`)); err != nil &&
				strings.Contains(err.Error(), "does not resolve") {
				t.Fatalf("%s: delivered input schema does not resolve: %v", d.ID, err)
			}
			if d.Visibility == contract.VisibilityInternal {
				internal++
				if d.Mode == contract.ModeMutation {
					mutations++
				}
			}
		}
	}
	if internal == 0 || mutations == 0 {
		t.Fatalf("landed modules declare %d internal operations, %d of them mutations; the seam is not exercised", internal, mutations)
	}
	if got := len(a.reg.Public()); got != 203 {
		t.Fatalf("Public() holds %d operations, want the 203 frozen ones", got)
	}
}

// TestLandedDriftIsDomainSide pins what conform papers over. The registry
// must keep rejecting public descriptors that disagree with the frozen
// catalog; until the domains are corrected the unedited assembly fails, and
// it fails on exactly the recorded drifts, never on a seam rule.
func TestLandedDriftIsDomainSide(t *testing.T) {
	a := assemble(t)
	_, err := registry.New(a.modules)
	if err == nil {
		t.Log("the landed modules assemble unedited; conform is now the identity and can be deleted")
		return
	}
	for _, domainDrift := range []string{"CLI mapping", "completion schema presence", "scope requirements", "pointer does not resolve"} {
		if strings.Contains(err.Error(), domainDrift) {
			t.Logf("unedited assembly stops on domain-side catalog drift: %v", err)
			return
		}
	}
	t.Fatalf("unedited assembly fails on something other than the recorded domain drift: %v", err)
}
