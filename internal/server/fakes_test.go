package server_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/application"
	"github.com/zatiti/zatiti/internal/contract"
)

// fakeClock is a trivial contract.Clock; nothing exercised through the
// server package depends on a specific time source.
type fakeClock struct{}

func (fakeClock) Now() time.Time { return time.Now() }

// fakeIDs mints real UUIDv4 identities through contract.NewID so command
// and event identities look exactly like production ones.
type fakeIDs struct{}

func (fakeIDs) New() contract.ID { return contract.NewID() }

// fakeUnit is the transaction-scoped contract.Unit handed to handlers in
// this test environment. No test handler in this package issues SQL, so the
// Reader/ExecContext methods only need to exist, never to work.
type fakeUnit struct {
	actor      contract.Actor
	scope      contract.Scope
	generation int64
	readOnly   bool
	db         *fakeDB
}

func (u *fakeUnit) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("fakeUnit: QueryContext is not supported")
}

func (u *fakeUnit) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }

func (u *fakeUnit) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("fakeUnit: ExecContext is not supported")
}

func (u *fakeUnit) Actor() contract.Actor { return u.actor }
func (u *fakeUnit) Scope() contract.Scope { return u.scope }
func (u *fakeUnit) Generation() int64     { return u.generation }
func (u *fakeUnit) ReadOnly() bool        { return u.readOnly }

func (u *fakeUnit) Emit(_ context.Context, event contract.Event) error {
	u.db.mu.Lock()
	defer u.db.mu.Unlock()
	event.Sequence = int64(len(u.db.events)) + 1
	u.db.events = append(u.db.events, event)
	return nil
}

// fakeDB is a minimal in-memory contract.Database. It exists solely so
// application.Application has something to open Read/Write snapshots
// against; it carries just enough state (the event log) for
// Application.installationID to discover whether bootstrap already ran, and
// it honors context cancellation the way a real driver would, so tests can
// prove the server decouples dispatch from client disconnection.
type fakeDB struct {
	mu         sync.Mutex
	events     []contract.Event
	generation int64
}

func (d *fakeDB) StartGeneration(context.Context) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.generation++
	return d.generation, nil
}

func (d *fakeDB) Generation(context.Context) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.generation, nil
}

func (d *fakeDB) Read(ctx context.Context, actor contract.Actor, scope contract.Scope, fn func(contract.Unit) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	u := &fakeUnit{actor: actor, scope: scope, generation: d.generation, readOnly: true, db: d}
	return fn(u)
}

func (d *fakeDB) Write(ctx context.Context, actor contract.Actor, scope contract.Scope, fn func(contract.Unit) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	u := &fakeUnit{actor: actor, scope: scope, generation: d.generation, db: d}
	return fn(u)
}

func (d *fakeDB) Migrate(context.Context, []contract.Migration) error { return nil }

func (d *fakeDB) Events(_ context.Context, after int64, limit int) ([]contract.Event, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []contract.Event
	for _, e := range d.events {
		if e.Sequence <= after {
			continue
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (d *fakeDB) Backup(context.Context, io.Writer) error {
	return errors.New("fakeDB: Backup is not supported")
}
func (d *fakeDB) Close() error { return nil }

// hasEvent reports whether an event of kind was committed, independent of
// whether any HTTP response ever reached a caller.
func (d *fakeDB) hasEvent(kind string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, e := range d.events {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

// fakeAuthenticator maps opaque bearer credentials and certificate SPKI
// fingerprints to actors, standing in for the identity owner. An
// unregistered credential or fingerprint is refused exactly as a revoked or
// unknown one would be in production.
type fakeAuthenticator struct {
	mu           sync.Mutex
	credentials  map[string]contract.Actor
	certificates map[contract.Digest]contract.Actor
}

func newFakeAuthenticator() *fakeAuthenticator {
	return &fakeAuthenticator{
		credentials:  make(map[string]contract.Actor),
		certificates: make(map[contract.Digest]contract.Actor),
	}
}

func (a *fakeAuthenticator) registerCredential(token string, actor contract.Actor) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.credentials[token] = actor
}

func (a *fakeAuthenticator) registerCertificate(fingerprint contract.Digest, actor contract.Actor) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.certificates[fingerprint] = actor
}

func (a *fakeAuthenticator) Authenticate(_ context.Context, _ contract.Reader, credential []byte) (contract.Actor, error) {
	if len(credential) == 0 {
		return contract.Actor{}, &contract.Fault{Code: contract.CodePermissionDenied, Message: "credential is required"}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	actor, ok := a.credentials[string(credential)]
	if !ok {
		return contract.Actor{}, &contract.Fault{Code: contract.CodePermissionDenied, Message: "credential is not recognized"}
	}
	return actor, nil
}

func (a *fakeAuthenticator) AuthenticateCertificate(_ context.Context, _ contract.Reader, fingerprint contract.Digest) (contract.Actor, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	actor, ok := a.certificates[fingerprint]
	if !ok {
		return contract.Actor{}, &contract.Fault{Code: contract.CodePermissionDenied, Message: "certificate is not recognized or has been revoked"}
	}
	return actor, nil
}

// fakeCatalog is a minimal application.Catalog plus the IOLookup capability
// application.New detects by type assertion. It carries only the
// descriptors and internal handlers this package's tests need to exercise
// the server's own transport behavior; it proves nothing about registry or
// application correctness, which those packages' own tests own.
type fakeCatalog struct {
	descriptors map[string]contract.Descriptor
	handlers    map[string]contract.Handler
	localIO     map[string]contract.LocalIO
	commands    *commandStore
}

func newFakeCatalog() *fakeCatalog {
	return &fakeCatalog{
		descriptors: make(map[string]contract.Descriptor),
		handlers:    make(map[string]contract.Handler),
		localIO:     make(map[string]contract.LocalIO),
		commands:    &commandStore{byKey: make(map[string]contract.Result)},
	}
}

// commandStore stands in for evidence's durable command retention: a
// submission key recorded once and looked up by "command.get", exactly the
// mechanism a client uses to recover a durable outcome after losing the
// original response to a disconnect. It is deliberately independent of
// application's own submission-key dedupe machinery (an evidence-owner
// concern); it exists only to let this package's tests exercise the
// transport's own handling of the recovery lookup.
type commandStore struct {
	mu    sync.Mutex
	byKey map[string]contract.Result
}

func (s *commandStore) record(key string, result contract.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byKey[key] = result
}

func (s *commandStore) lookup(key string) (contract.Result, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, ok := s.byKey[key]
	return result, ok
}

func (c *fakeCatalog) Lookup(id string, version int64) (contract.Descriptor, contract.Handler, error) {
	desc, ok := c.descriptors[id]
	if !ok {
		return contract.Descriptor{}, nil, fmt.Errorf("unknown operation %q", id)
	}
	if version != 0 && version != desc.Version {
		return contract.Descriptor{}, nil, fmt.Errorf("operation %q version %d not supported", id, version)
	}
	return desc, c.handlers[id], nil
}

func (c *fakeCatalog) Public() []contract.Descriptor {
	var out []contract.Descriptor
	for _, d := range c.descriptors {
		if d.Visibility == contract.VisibilityPublic {
			out = append(out, d)
		}
	}
	return out
}

func (c *fakeCatalog) LocalIOFor(operation string) (contract.LocalIO, bool) {
	io, ok := c.localIO[operation]
	return io, ok
}

// fakeInstallationIO is the contract.LocalIO fake behind installation.init.
// Finish emits the event that Application.installationID reads back to
// discover the minted installation, exactly as the real installation owner
// would inside its own migration-backed tables.
type fakeInstallationIO struct{}

func (fakeInstallationIO) Prepare(_ context.Context, u contract.Unit, invocation contract.Invocation) (contract.IOPlan, error) {
	return contract.IOPlan{
		ID:         contract.NewID(),
		Owner:      "installation",
		Invocation: invocation,
		Actor:      u.Actor(),
		Scope:      u.Scope(),
		Generation: u.Generation(),
		Prepared:   []byte(`{}`),
	}, nil
}

func (fakeInstallationIO) Perform(context.Context, contract.IOPlan) (contract.IOResult, error) {
	return contract.IOResult{Data: []byte(`{}`)}, nil
}

func (fakeInstallationIO) Finish(ctx context.Context, u contract.Unit, plan contract.IOPlan, _ contract.IOResult) (contract.Payload, error) {
	if err := u.Emit(ctx, contract.Event{
		ID:              contract.NewID(),
		At:              time.Now(),
		Scope:           plan.Scope,
		Kind:            "installation.initialized",
		ResourceID:      plan.Scope.InstallationID,
		ResourceVersion: 1,
		Data:            []byte(`{}`),
	}); err != nil {
		return contract.Payload{}, err
	}
	data := []byte(fmt.Sprintf(
		`{"resource":{"installation_id":%q,"generation":1,"paused":false,"maintenance":false,"initialized":true,"requirements":[],"version":1}}`,
		plan.Scope.InstallationID,
	))
	return contract.Payload{Status: contract.StatusCompleted, Data: data}, nil
}

// delayedInstallationIO wraps fakeInstallationIO with an artificial delay in
// Perform and a channel closed once Finish commits, so a test can close the
// client connection mid-flight and then observe, without sleeping and
// polling, the exact moment the mutation durably committed.
type delayedInstallationIO struct {
	fakeInstallationIO
	delay    time.Duration
	finished chan struct{}
}

func (d delayedInstallationIO) Perform(ctx context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	time.Sleep(d.delay)
	return d.fakeInstallationIO.Perform(ctx, plan)
}

func (d delayedInstallationIO) Finish(ctx context.Context, u contract.Unit, plan contract.IOPlan, result contract.IOResult) (contract.Payload, error) {
	payload, err := d.fakeInstallationIO.Finish(ctx, u, plan, result)
	close(d.finished)
	return payload, err
}

// installationInitInputSchema is the frozen installation.init input schema
// (internal/server/AGENTS.md, "installation.init v1").
const installationInitInputSchema = `{"type":"object","additionalProperties":false,"properties":{"credential_store":{"type":"string","enum":["os","headless"]},"owner_name":{"type":"string","maxLength":8192},"headless_key_ref":{"type":"string","maxLength":8192}},"required":["credential_store","owner_name"]}`

// commandGetInputSchema is the frozen command.get input with the shared
// Scope definition inlined: scope, submission_key, operation and
// operation_version are all required.
const commandGetInputSchema = `{"type":"object","additionalProperties":false,"properties":{"scope":{"type":"object","additionalProperties":false,"properties":{"installation_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"project_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"}},"required":["installation_id"]},"submission_key":{"type":"string","maxLength":8192},"operation":{"type":"string","maxLength":8192},"operation_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","submission_key","operation","operation_version"]}`

// pingInputSchema/pingOutputSchema back the "capabilities.list"-shaped test
// query operation: public, policy-exempt (see application.policyExempt),
// and free of any scope requirement so tests can exercise ordinary
// authenticated dispatch without also faking a policy owner.
const pingInputSchema = `{"type":"object","additionalProperties":false,"properties":{},"required":[]}`

// testEnv bundles a fully constructed, already-bootstrapped
// *application.Application with the fakes behind it, so tests can register
// credentials/certificates against a live authenticator and inspect the
// underlying event log directly.
type testEnv struct {
	app  *application.Application
	auth *fakeAuthenticator
	db   *fakeDB
	cat  *fakeCatalog

	// installation is the minted installation identity once bootstrap ran;
	// scoped inputs such as command.get name it.
	installation contract.ID
}

// newTestEnv builds an application wired to this package's fakes, with the
// "capabilities.list" query and "installation.init" bootstrap descriptors
// registered but the installation NOT yet initialized. Most tests want
// newBootstrappedEnv instead.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	return newTestEnvWithIO(t, fakeInstallationIO{})
}

// newTestEnvWithIO is newTestEnv with the installation.init LocalIO
// implementation swapped out, for tests that need to observe or delay
// bootstrap's local IO phases directly.
func newTestEnvWithIO(t *testing.T, installIO contract.LocalIO) *testEnv {
	t.Helper()
	cat := newFakeCatalog()
	cat.descriptors["installation.init"] = contract.Descriptor{
		ID: "installation.init", Version: 1, Owner: "installation",
		Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation,
		InputSchema:   []byte(installationInitInputSchema),
		SubmissionKey: false,
	}
	cat.localIO["installation.init"] = installIO

	cat.descriptors["capabilities.list"] = contract.Descriptor{
		ID: "capabilities.list", Version: 1, Owner: "registry",
		Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery,
		InputSchema:   []byte(pingInputSchema),
		SubmissionKey: false,
	}
	cat.handlers["capabilities.list"] = func(_ context.Context, u contract.Unit, _ contract.Invocation) (contract.Payload, error) {
		data := []byte(fmt.Sprintf(`{"items":[],"seen_principal":%q}`, u.Actor().PrincipalID))
		return contract.Payload{Status: contract.StatusCompleted, Data: data}, nil
	}

	cat.descriptors["command.get"] = contract.Descriptor{
		ID: "command.get", Version: 1, Owner: "evidence",
		Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery,
		InputSchema:   []byte(commandGetInputSchema),
		SubmissionKey: false,
		ScopeRequired: []string{"installation_id"},
	}
	cat.handlers["command.get"] = func(_ context.Context, _ contract.Unit, inv contract.Invocation) (contract.Payload, error) {
		var in struct {
			Scope            contract.Scope `json:"scope"`
			SubmissionKey    string         `json:"submission_key"`
			Operation        string         `json:"operation"`
			OperationVersion int64          `json:"operation_version"`
		}
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInvalidInput, Message: err.Error()}
		}
		result, ok := cat.commands.lookup(in.SubmissionKey)
		if !ok {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeNotFound, Message: "no command is bound to that submission key"}
		}
		data, err := json.Marshal(map[string]any{"resource": result})
		if err != nil {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: err.Error()}
		}
		return contract.Payload{Status: contract.StatusCompleted, Data: data}, nil
	}

	cat.descriptors["_policy.check"] = contract.Descriptor{
		ID: "_policy.check", Version: 1, Owner: "policy",
		Visibility: contract.VisibilityInternal, Mode: contract.ModeQuery,
		Callers: []string{"application"},
	}
	cat.handlers["_policy.check"] = func(context.Context, contract.Unit, contract.Invocation) (contract.Payload, error) {
		data := []byte(`{"resource":{"decision":"allow","reasons":[],"requirements":[]}}`)
		return contract.Payload{Status: contract.StatusCompleted, Data: data}, nil
	}

	cat.descriptors["_identity.authority"] = contract.Descriptor{
		ID: "_identity.authority", Version: 1, Owner: "identity",
		Visibility: contract.VisibilityInternal, Mode: contract.ModeQuery,
		Callers: []string{"application"},
	}
	cat.handlers["_identity.authority"] = func(_ context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
		var in struct {
			PrincipalID contract.ID    `json:"principal_id"`
			Scope       contract.Scope `json:"scope"`
		}
		_ = contract.DecodeStrict(inv.Input, &in)
		data := []byte(fmt.Sprintf(
			`{"resource":{"principal":{"id":%q,"version":1,"kind":%q,"name":"test","scope":{"installation_id":%q},"revoked":false},"grants":[],"restrictions":[]}}`,
			in.PrincipalID, u.Actor().Kind, in.Scope.InstallationID,
		))
		return contract.Payload{Status: contract.StatusCompleted, Data: data}, nil
	}

	db := &fakeDB{}
	auth := newFakeAuthenticator()
	app, err := application.New(db, cat, auth, fakeClock{}, fakeIDs{})
	if err != nil {
		t.Fatalf("application.New: %v", err)
	}
	return &testEnv{app: app, auth: auth, db: db, cat: cat}
}

// bootstrap runs installation.init directly against the application (not
// through the server) so tests that need an initialized installation don't
// also have to exercise the bootstrap HTTP path themselves.
func (e *testEnv) bootstrap(t *testing.T) {
	t.Helper()
	actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService}
	res, err := e.app.Invoke(context.Background(), actor, "installation.init", contract.Request{
		Schema: contract.SchemaRequest,
		Input:  []byte(`{"credential_store":"os","owner_name":"test-owner"}`),
	})
	if err != nil {
		t.Fatalf("bootstrap installation.init: %v", err)
	}
	if res.Status != contract.StatusCompleted {
		t.Fatalf("bootstrap installation.init status = %q, want completed", res.Status)
	}
	var data struct {
		Resource struct {
			InstallationID contract.ID `json:"installation_id"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(res.Data, &data); err != nil || data.Resource.InstallationID == "" {
		t.Fatalf("bootstrap installation.init data %s: %v", res.Data, err)
	}
	e.installation = data.Resource.InstallationID
}

// newBootstrappedEnv is newTestEnv plus a completed bootstrap: the common
// starting point for tests exercising ordinary (post-bootstrap) dispatch.
func newBootstrappedEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newTestEnv(t)
	e.bootstrap(t)
	return e
}

// testCA is a self-signed certificate authority minted fresh for one test,
// never checked-in key material.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating CA key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          randomSerial(t),
		Subject:               pkix.Name{CommonName: "zatiti-server-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating CA certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing CA certificate: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testCA{cert: cert, key: key, pool: pool}
}

// leaf mints a certificate signed by the CA, for use as either the server's
// or a client's TLS credential.
func (ca *testCA) leaf(t *testing.T, commonName string, server bool) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating leaf key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: randomSerial(t),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if server {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		template.DNSNames = []string{"localhost"}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("creating leaf certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing leaf certificate: %v", err)
	}
	tlsCert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: cert}
	return tlsCert, cert
}

func randomSerial(t *testing.T) *big.Int {
	t.Helper()
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		t.Fatalf("generating certificate serial: %v", err)
	}
	return serial
}

// shortSocketPath returns a fresh temporary socket path short enough for
// AF_UNIX's sun_path bound (104 bytes on darwin, 108 on Linux). t.TempDir()
// embeds the full test name and is routinely too long for that bound, so
// this mints its own short-named directory instead.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "zsrv")
	if err != nil {
		t.Fatalf("creating temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// fingerprintOf mirrors the server's own certificate fingerprint
// computation so tests can register the exact digest the server will
// compute for a generated leaf certificate.
func fingerprintOf(cert *x509.Certificate) contract.Digest {
	return contract.Hash(cert.RawSubjectPublicKeyInfo)
}
