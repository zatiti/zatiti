// Package integration_test owns the real cross-package fixtures, atomicity,
// transport parity and end-to-end journeys. Everything here assembles landed
// production packages; nothing in this package imitates an unlanded one.
package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

// fixtureEpoch is the deterministic start of every fixture clock.
var fixtureEpoch = time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)

// stepClock is the injected deterministic clock. Every Now advances by one
// millisecond so stored timestamps order the way the calls did; Advance
// moves time for expiry cases.
type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func newStepClock() *stepClock { return &stepClock{now: fixtureEpoch} }

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Millisecond)
	return c.now
}

func (c *stepClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// uuidSource mints real UUIDv4 identities through the contract package.
type uuidSource struct{}

func (uuidSource) New() contract.ID { return contract.NewID() }

// recordingSecrets decorates the real platform secret store. It changes no
// behavior: it only remembers which opaque reference each key was custodied
// under, so the fixture can read the owner credential back the way the
// local trusted helper does. installation.Service.OwnerCredential is the
// production seam for the same lookup; switching the fixture to it is a
// slice-2 change.
type recordingSecrets struct {
	inner contract.SecretStore
	mu    sync.Mutex
	refs  map[string]string
}

func newRecordingSecrets(inner contract.SecretStore) *recordingSecrets {
	return &recordingSecrets{inner: inner, refs: map[string]string{}}
}

func (r *recordingSecrets) Put(ctx context.Context, key string, secret []byte) (string, error) {
	ref, err := r.inner.Put(ctx, key, secret)
	if err == nil {
		r.mu.Lock()
		r.refs[key] = ref
		r.mu.Unlock()
	}
	return ref, err
}

func (r *recordingSecrets) Get(ctx context.Context, ref string) ([]byte, error) {
	return r.inner.Get(ctx, ref)
}

func (r *recordingSecrets) Delete(ctx context.Context, ref string) error {
	return r.inner.Delete(ctx, ref)
}

func (r *recordingSecrets) refFor(key string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ref, ok := r.refs[key]
	return ref, ok
}

// catalogMode selects which operation catalog fronts the real modules.
type catalogMode int

const (
	// catalogRegistry is the production assembly: internal/registry.New over
	// the real modules. It is the target mode for every case in this package.
	catalogRegistry catalogMode = iota
	// catalogDirect fronts the same real modules with moduleCatalog. It
	// exists only while internal/registry cannot assemble the landed modules
	// (see TestRealRegistryAssemblesLandedModules); it invents no operation
	// behavior and must be deleted once the registry path is green.
	catalogDirect
)

// defaultCatalogMode is the single switch to flip to catalogRegistry once
// the registry defects recorded in registry_seam_test.go are fixed.
const defaultCatalogMode = catalogDirect

// moduleOrder is the explicit assembly and migration order.
var moduleOrder = []string{
	"identity", "configuration", "skills", "connections", "policy", "reviews",
	"accounting", "tasks", "scheduling", "messaging", "execution", "effects",
	"memory", "artifacts", "evidence", "installation",
}

// ownerKey is the secret-store key the fixture asks bootstrap to custody the
// owner credential under.
const ownerKey = "integration/owner"

// fixture is one really assembled installation: platform custody and lock,
// SQLite storage, all sixteen landed domain modules with real dependencies,
// an operation catalog and the application dispatcher, on temp directories.
type fixture struct {
	t        testing.TB
	stateDir string
	keyRef   string

	clock   *stepClock
	plat    *platform.Platform
	own     contract.Ownership
	secrets *recordingSecrets
	db      contract.Database
	modules []contract.Module
	catalog application.Catalog
	app     *application.Application

	generation int64

	// Set by bootstrap.
	installationID contract.ID
	owner          contract.Actor
}

// fixtureOptions narrows assembly for the seam tests.
type fixtureOptions struct {
	mode     catalogMode
	stateDir string // reuse an existing state directory (restart)
	keyRef   string
}

// buildModules constructs every landed domain module with real
// dependencies and owner-bound ports, in moduleOrder.
func buildModules(router *application.PortRouter, clock contract.Clock, secrets contract.SecretStore, blobs contract.BlobStore) ([]contract.Module, contract.Authenticator, error) {
	deps := func(owner string) contract.Dependencies {
		return contract.Dependencies{
			Clock: clock, IDs: uuidSource{}, Ports: router.For(owner),
			Secrets: secrets, Blobs: blobs,
		}
	}
	idn, err := identity.New(deps("identity"))
	if err != nil {
		return nil, nil, err
	}
	constructors := map[string]func(contract.Dependencies) (contract.Module, error){
		"configuration": func(d contract.Dependencies) (contract.Module, error) { return configuration.New(d) },
		"skills":        func(d contract.Dependencies) (contract.Module, error) { return skills.New(d) },
		"connections":   func(d contract.Dependencies) (contract.Module, error) { return connections.New(d) },
		"policy":        func(d contract.Dependencies) (contract.Module, error) { return policy.New(d) },
		"reviews":       func(d contract.Dependencies) (contract.Module, error) { return reviews.New(d) },
		"accounting":    func(d contract.Dependencies) (contract.Module, error) { return accounting.New(d) },
		"tasks":         func(d contract.Dependencies) (contract.Module, error) { return tasks.New(d) },
		"scheduling":    func(d contract.Dependencies) (contract.Module, error) { return scheduling.New(d) },
		"messaging":     func(d contract.Dependencies) (contract.Module, error) { return messaging.New(d) },
		"execution":     func(d contract.Dependencies) (contract.Module, error) { return execution.New(d) },
		"effects":       func(d contract.Dependencies) (contract.Module, error) { return effects.New(d) },
		"memory":        func(d contract.Dependencies) (contract.Module, error) { return memory.New(d) },
		"artifacts":     func(d contract.Dependencies) (contract.Module, error) { return artifacts.New(d) },
		"evidence":      func(d contract.Dependencies) (contract.Module, error) { return evidence.New(d) },
		"installation":  func(d contract.Dependencies) (contract.Module, error) { return installation.New(d) },
	}
	modules := []contract.Module{idn}
	for _, name := range moduleOrder[1:] {
		m, err := constructors[name](deps(name))
		if err != nil {
			return nil, nil, err
		}
		if m.Name() != name {
			return nil, nil, errors.New("module " + m.Name() + " does not carry its assembly name " + name)
		}
		modules = append(modules, m)
	}
	return modules, idn, nil
}

// writeMasterKey writes a synthetic 32-byte master key outside the state
// directory and returns its file: reference.
func writeMasterKey(t testing.TB) string {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(0xA0 + i)
	}
	path := filepath.Join(t.TempDir(), "master.key")
	if err := os.WriteFile(path, key, 0o600); err != nil {
		t.Fatalf("master key: %v", err)
	}
	return "file:" + path
}

// assemble follows the frozen entrypoint order: platform.Open -> Acquire ->
// storage.Open -> Migrate -> StartGeneration -> NewPorts -> modules ->
// catalog -> application.New -> Bind. It returns an error instead of failing
// the test so the seam tests can assert on assembly failures.
func assemble(t testing.TB, opts fixtureOptions) (*fixture, error) {
	t.Helper()
	ctx := context.Background()
	f := &fixture{t: t, clock: newStepClock(), stateDir: opts.stateDir, keyRef: opts.keyRef}
	if f.stateDir == "" {
		f.stateDir = filepath.Join(t.TempDir(), "state")
	}
	if f.keyRef == "" {
		f.keyRef = writeMasterKey(t)
	}
	t.Cleanup(f.close)

	plat, err := platform.Open(platform.Config{
		StateDir: f.stateDir, CredentialBackend: "headless", MasterKeyRef: f.keyRef,
	})
	if err != nil {
		return nil, err
	}
	f.plat = plat
	if f.own, err = plat.Acquire(ctx); err != nil {
		return nil, err
	}
	f.secrets = newRecordingSecrets(plat.Secrets())

	if f.db, err = storage.Open(ctx, storage.Config{Path: filepath.Join(plat.StateDir(), "zatiti.db")}); err != nil {
		return nil, err
	}

	router := application.NewPorts()
	modules, auth, err := buildModules(router, f.clock, f.secrets, plat.Blobs())
	if err != nil {
		return nil, err
	}
	f.modules = modules

	var migrations []contract.Migration
	for _, m := range modules {
		migrations = append(migrations, m.Migrations()...)
	}
	if err := f.db.Migrate(ctx, migrations); err != nil {
		return nil, err
	}
	if f.generation, err = f.db.StartGeneration(ctx); err != nil {
		return nil, err
	}

	switch opts.mode {
	case catalogRegistry:
		reg, err := registry.New(modules)
		if err != nil {
			return nil, err
		}
		f.catalog = reg
	default:
		cat, err := newModuleCatalog(modules)
		if err != nil {
			return nil, err
		}
		f.catalog = cat
	}

	if f.app, err = application.New(f.db, f.catalog, auth, f.clock, uuidSource{}); err != nil {
		return nil, err
	}
	if err := router.Bind(f.app); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *fixture) close() {
	if f.app != nil {
		f.app.Close()
	}
	if f.db != nil {
		_ = f.db.Close()
		f.db = nil
	}
	if f.own != nil {
		_ = f.own.Close()
		f.own = nil
	}
	if f.plat != nil {
		_ = f.plat.Close()
		f.plat = nil
	}
}

// newFixture assembles in the default catalog mode and fails the test on any
// assembly error.
func newFixture(t testing.TB) *fixture {
	t.Helper()
	f, err := assemble(t, fixtureOptions{mode: defaultCatalogMode})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	return f
}

// newBootstrappedFixture assembles and runs installation.init through the
// real application, the way the local bootstrap route does.
func newBootstrappedFixture(t testing.TB) *fixture {
	t.Helper()
	f := newFixture(t)
	f.bootstrap()
	return f
}

// bootstrapActor mirrors internal/server: the one-time local bootstrap has
// no principal yet, so a throwaway service actor satisfies Invoke.
func bootstrapActor() contract.Actor {
	return contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService}
}

// initInput is the installation.init input the fixture uses everywhere.
func initInput() json.RawMessage {
	return mustJSON(map[string]any{
		"credential_store": "headless",
		"owner_name":       "Integration Owner",
		"headless_key_ref": ownerKey,
	})
}

func (f *fixture) bootstrap() {
	f.t.Helper()
	res, err := f.app.Invoke(context.Background(), bootstrapActor(), "installation.init",
		contract.Request{Schema: contract.SchemaRequest, Input: initInput()})
	if err != nil {
		f.t.Fatalf("installation.init: %v", err)
	}
	var out struct {
		Resource struct {
			InstallationID contract.ID `json:"installation_id"`
			Initialized    bool        `json:"initialized"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		f.t.Fatalf("installation.init data: %v", err)
	}
	if !out.Resource.Initialized || out.Resource.InstallationID == "" {
		f.t.Fatalf("installation.init did not report an initialized installation: %s", res.Data)
	}
	f.installationID = out.Resource.InstallationID
	f.owner = f.authenticate(f.ownerToken())
}

// ownerToken reads the custodied owner credential bytes the way a trusted
// local helper would: from platform custody, never from a product result.
func (f *fixture) ownerToken() []byte {
	f.t.Helper()
	ref, ok := f.secrets.refFor(ownerKey)
	if !ok {
		f.t.Fatalf("bootstrap custodied no owner credential under %q", ownerKey)
	}
	token, err := f.secrets.Get(context.Background(), ref)
	if err != nil {
		f.t.Fatalf("owner credential: %v", err)
	}
	return token
}

func (f *fixture) authenticate(token []byte) contract.Actor {
	f.t.Helper()
	actor, err := f.app.Authenticate(context.Background(), append([]byte(nil), token...))
	if err != nil {
		f.t.Fatalf("authenticate: %v", err)
	}
	return actor
}

// scope returns the installation-wide scope.
func (f *fixture) scope() contract.Scope { return contract.Scope{InstallationID: f.installationID} }

// invoke runs one public operation as actor. A mutation needs key.
func (f *fixture) invoke(actor contract.Actor, op, key string, input any) (contract.Result, error) {
	f.t.Helper()
	return f.app.Invoke(context.Background(), actor, op, contract.Request{
		Schema: contract.SchemaRequest, SubmissionKey: key, Input: mustJSON(input),
	})
}

// must runs invoke and fails the test on any fault.
func (f *fixture) must(actor contract.Actor, op, key string, input any) contract.Result {
	f.t.Helper()
	res, err := f.invoke(actor, op, key, input)
	if err != nil {
		f.t.Fatalf("%s: %v", op, err)
	}
	return res
}

func mustJSON(v any) json.RawMessage {
	if raw, ok := v.(json.RawMessage); ok {
		return raw
	}
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

// faultCode extracts the contract fault code from an error, or "".
func faultCode(err error) string {
	var f *contract.Fault
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}

// decode strictly decodes result data into out.
func decode(t testing.TB, data json.RawMessage, out any) {
	t.Helper()
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
}
