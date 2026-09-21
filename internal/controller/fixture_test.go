package controller

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/application"
	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// The fixture assembles the real application dispatcher over real storage in
// a temporary directory. Owners are exact local fakes: each persists through
// the transaction Unit it is handed (so rollback, commit and generation
// fencing are storage's, not simulated) and each validates its input and
// output against the frozen operation schemas embedded in this package's
// AGENTS.md.

// ---- frozen schemas ----

type spec struct {
	defs json.RawMessage
	in   map[string]json.RawMessage
	out  map[string]json.RawMessage
}

var (
	specOnce sync.Once
	specData *spec
	specErr  error
)

func loadSpec(t *testing.T) *spec {
	t.Helper()
	specOnce.Do(func() {
		f, err := os.Open("AGENTS.md")
		if err != nil {
			specErr = err
			return
		}
		defer func() { _ = f.Close() }()
		s := &spec{in: map[string]json.RawMessage{}, out: map[string]json.RawMessage{}}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 1<<20), 8<<20)
		var op, want string
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "### `"):
				op = strings.SplitN(strings.TrimPrefix(line, "### `"), "`", 2)[0]
			case line == "Input schema:":
				want = "in"
			case line == "Output data schema:":
				want = "out"
			case strings.HasPrefix(line, `{"$defs":{"Acceptance"`) && s.defs == nil:
				var doc struct {
					Defs json.RawMessage `json:"$defs"`
				}
				if err := json.Unmarshal([]byte(line), &doc); err != nil {
					specErr = err
					return
				}
				s.defs = doc.Defs
			case strings.HasPrefix(line, "{") && want != "" && op != "":
				if want == "in" {
					s.in[op] = json.RawMessage(line)
				} else {
					s.out[op] = json.RawMessage(line)
				}
				want = ""
			}
		}
		specErr = scanner.Err()
		specData = s
	})
	if specErr != nil {
		t.Fatalf("load frozen schemas: %v", specErr)
	}
	if len(specData.defs) == 0 || len(specData.in) < 20 {
		t.Fatalf("frozen schemas incomplete: %d inputs", len(specData.in))
	}
	return specData
}

func (s *spec) check(schema, instance json.RawMessage) error {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(schema, &doc); err != nil {
		return err
	}
	doc["$defs"] = s.defs
	full, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return contract.ValidateSchema(full, instance)
}

// ---- clock, ownership, database ----

type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []clockWaiter
	parked  chan struct{}
}

type clockWaiter struct {
	at time.Time
	ch chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{
		now:    time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		parked: make(chan struct{}, 1024),
	}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	ch := make(chan time.Time, 1)
	c.waiters = append(c.waiters, clockWaiter{at: c.now.Add(d), ch: ch})
	c.mu.Unlock()
	c.parked <- struct{}{}
	return ch
}

// Advance moves time and fires every due waiter.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var keep []clockWaiter
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			w.ch <- c.now
		} else {
			keep = append(keep, w)
		}
	}
	c.waiters = keep
	c.mu.Unlock()
}

// awaitParked blocks until the loop finished a tick and parked on After.
func (c *fakeClock) awaitParked(t *testing.T) {
	t.Helper()
	select {
	case <-c.parked:
	case <-time.After(30 * time.Second):
		t.Fatal("controller loop never parked")
	}
}

type fakeOwnership struct {
	mu   sync.Mutex
	held bool
	lost chan struct{}
}

func newOwnership() *fakeOwnership { return &fakeOwnership{held: true, lost: make(chan struct{})} }

func (o *fakeOwnership) Held() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.held
}

func (o *fakeOwnership) Lost() <-chan struct{} { return o.lost }

func (o *fakeOwnership) Close() error { o.lose(); return nil }

func (o *fakeOwnership) lose() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.held {
		o.held = false
		close(o.lost)
	}
}

var errAckLost = errors.New("acknowledgement lost after commit")

// faultyDB loses the acknowledgement of a committed write on demand.
type faultyDB struct {
	contract.Database
	mu   sync.Mutex
	lose bool
}

func (d *faultyDB) armLostAck() {
	d.mu.Lock()
	d.lose = true
	d.mu.Unlock()
}

func (d *faultyDB) Write(ctx context.Context, actor contract.Actor, scope contract.Scope, fn func(contract.Unit) error) error {
	err := d.Database.Write(ctx, actor, scope, fn)
	d.mu.Lock()
	lose := d.lose
	d.lose = false
	d.mu.Unlock()
	if err == nil && lose {
		return errAckLost
	}
	return err
}

// ---- catalog and injection ----

type catalog struct {
	ops map[string]catalogOp
}

type catalogOp struct {
	desc contract.Descriptor
	h    contract.Handler
}

func (c *catalog) Lookup(id string, version int64) (contract.Descriptor, contract.Handler, error) {
	op, ok := c.ops[id]
	if !ok {
		return contract.Descriptor{}, nil, &contract.Fault{Code: contract.CodeNotFound, Message: "unknown operation " + id}
	}
	if version != 0 && version != op.desc.Version {
		return contract.Descriptor{}, nil, &contract.Fault{Code: contract.CodeCapabilityUnsupported, Message: "unsupported version"}
	}
	return op.desc, op.h, nil
}

func (c *catalog) Public() []contract.Descriptor { return nil }

type fakeAuth struct{}

func (fakeAuth) Authenticate(context.Context, contract.Reader, []byte) (contract.Actor, error) {
	return contract.Actor{}, &contract.Fault{Code: contract.CodePermissionDenied, Message: "no credentials in this fixture"}
}

func (fakeAuth) AuthenticateCertificate(context.Context, contract.Reader, contract.Digest) (contract.Actor, error) {
	return contract.Actor{}, &contract.Fault{Code: contract.CodePermissionDenied, Message: "no certificates in this fixture"}
}

type newIDs struct{}

func (newIDs) New() contract.ID { return contract.NewID() }

// injection alters one call of one operation.
type injection struct {
	// fail is returned before the handler runs; the transaction rolls back.
	fail error
	// loseAck commits the handler's work, then loses the acknowledgement.
	loseAck bool
	// crash freezes the controller's durable state at this boundary, as a
	// process death would: before the handler when fail is set, otherwise
	// after its commit.
	crash bool
	// sticky keeps the injection in place for every later call.
	sticky bool
	// before runs inside the transaction, ahead of the handler.
	before func()
}

// ---- fixture ----

type fx struct {
	t       *testing.T
	dir     string
	spec    *spec
	clock   *fakeClock
	install contract.ID
	actor   contract.Actor

	raw contract.Database
	db  *faultyDB
	app *application.Application

	mu       sync.Mutex
	calls    map[string]int
	limits   map[string]int64
	inject   map[string][]injection
	ctl      *Controller
	adapters map[string]contract.Adapter
	blobs    contract.BlobStore
	jobs     map[string]JobRunner
	verifier contract.Verifier
	operator contract.WorkerOperator
	ready    int
	paused   map[contract.ID]bool
}

func newFx(t *testing.T) *fx {
	t.Helper()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	f := &fx{
		t:        t,
		dir:      dir,
		spec:     loadSpec(t),
		clock:    newFakeClock(),
		install:  contract.NewID(),
		actor:    contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService},
		calls:    map[string]int{},
		limits:   map[string]int64{},
		inject:   map[string][]injection{},
		adapters: map[string]contract.Adapter{},
		paused:   map[contract.ID]bool{},
	}
	f.boot(true)
	t.Cleanup(func() { _ = f.raw.Close() })
	return f
}

// boot opens storage, assembles the application and advances the generation,
// exactly as the entrypoint does before constructing a controller.
func (f *fx) boot(first bool) {
	f.t.Helper()
	ctx := context.Background()
	raw, err := storage.Open(ctx, storage.Config{Path: filepath.Join(f.dir, "zatiti.db")})
	if err != nil {
		f.t.Fatalf("storage.Open: %v", err)
	}
	if err := raw.Migrate(ctx, fixtureMigrations()); err != nil {
		f.t.Fatalf("Migrate: %v", err)
	}
	f.raw = raw
	f.db = &faultyDB{Database: raw}
	app, err := application.New(f.db, f.catalog(), fakeAuth{}, f.clock, newIDs{})
	if err != nil {
		f.t.Fatalf("application.New: %v", err)
	}
	if err := application.NewPorts().Bind(app); err != nil {
		f.t.Fatalf("Bind: %v", err)
	}
	f.app = app
	if _, err := raw.StartGeneration(ctx); err != nil {
		f.t.Fatalf("StartGeneration: %v", err)
	}
	if first {
		f.exec(`INSERT INTO identity_principals (id, kind, revoked) VALUES (?, 'service', 0)`, string(f.actor.PrincipalID))
		err := raw.Write(ctx, f.actor, f.scope(), func(u contract.Unit) error {
			return u.Emit(ctx, contract.Event{Kind: "installation.installation.initialized", ResourceID: f.install, ResourceVersion: 1})
		})
		if err != nil {
			f.t.Fatalf("seed installation event: %v", err)
		}
	}
}

// restart is a process restart: the database is closed and reopened, the
// application reassembled and the generation advanced. Providers (adapters)
// and the state directory survive; the previous controller is dead.
func (f *fx) restart() {
	f.t.Helper()
	if f.ctl != nil {
		f.ctl.abandoned.Store(true)
		f.ctl.closed.Store(true)
		f.ctl.workers.Wait()
	}
	if err := f.raw.Close(); err != nil {
		f.t.Fatalf("close database: %v", err)
	}
	f.boot(false)
}

func (f *fx) scope() contract.Scope { return contract.Scope{InstallationID: f.install} }

func (f *fx) generation() int64 {
	f.t.Helper()
	g, err := f.raw.Generation(context.Background())
	if err != nil {
		f.t.Fatalf("Generation: %v", err)
	}
	return g
}

// controller constructs and attaches a controller over the current boot.
func (f *fx) controller(own contract.Ownership) *Controller {
	f.t.Helper()
	if own == nil {
		own = newOwnership()
	}
	c, err := New(Config{StateDir: f.dir, TickInterval: time.Second}, f.app, f.db, own, f.adapters, f.clock)
	if err != nil {
		f.t.Fatalf("New: %v", err)
	}
	if err := c.Attach(Collaborators{Identity: f.actor, Blobs: f.blobs, Jobs: f.jobs, Verifier: f.verifier, Operator: f.operator}); err != nil {
		f.t.Fatalf("Attach: %v", err)
	}
	f.mu.Lock()
	f.ctl = c
	f.mu.Unlock()
	return c
}

// started returns a controller whose startup recovery already ran, for tests
// that drive ticks by hand.
func (f *fx) started() (*Controller, *session) {
	f.t.Helper()
	c := f.controller(nil)
	sess, err := c.start(context.Background())
	if err != nil {
		f.t.Fatalf("start: %v", err)
	}
	f.t.Cleanup(func() { _ = sess.journal.close() })
	return c, sess
}

// pass runs one tick and waits for the work it started.
func (f *fx) pass(c *Controller, sess *session) error {
	ctx := context.Background()
	err := c.tick(ctx, ctx, sess)
	c.workers.Wait()
	return err
}

func (f *fx) exec(query string, args ...any) {
	f.t.Helper()
	ctx := context.Background()
	err := f.raw.Write(ctx, f.actor, f.scope(), func(u contract.Unit) error {
		_, err := u.ExecContext(ctx, query, args...)
		return err
	})
	if err != nil {
		f.t.Fatalf("exec %q: %v", query, err)
	}
}

func (f *fx) queryInt(query string, args ...any) int64 {
	f.t.Helper()
	ctx := context.Background()
	var n int64
	err := f.raw.Read(ctx, f.actor, f.scope(), func(u contract.Unit) error {
		return u.QueryRowContext(ctx, query, args...).Scan(&n)
	})
	if err != nil {
		f.t.Fatalf("query %q: %v", query, err)
	}
	return n
}

func (f *fx) queryString(query string, args ...any) string {
	f.t.Helper()
	ctx := context.Background()
	var s string
	err := f.raw.Read(ctx, f.actor, f.scope(), func(u contract.Unit) error {
		return u.QueryRowContext(ctx, query, args...).Scan(&s)
	})
	if err != nil {
		f.t.Fatalf("query %q: %v", query, err)
	}
	return s
}

func (f *fx) called(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[op]
}

func (f *fx) arm(op string, inj injection) {
	f.mu.Lock()
	f.inject[op] = append(f.inject[op], inj)
	f.mu.Unlock()
}

func (f *fx) disarm(op string) {
	f.mu.Lock()
	delete(f.inject, op)
	f.mu.Unlock()
}

func (f *fx) take(op string) *injection {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[op]++
	queue := f.inject[op]
	if len(queue) == 0 {
		return nil
	}
	inj := queue[0]
	if !inj.sticky {
		f.inject[op] = queue[1:]
	}
	return &inj
}

// crash freezes the current controller's durable state, as process death
// would: nothing it does afterwards reaches the journal or the database.
func (f *fx) crash() {
	f.mu.Lock()
	c := f.ctl
	f.mu.Unlock()
	if c != nil {
		c.abandoned.Store(true)
	}
}

type ownerFunc func(ctx context.Context, u contract.Unit, input json.RawMessage) (any, error)

// handler wraps one fake owner method with schema checks and injection.
func (f *fx) handler(op string, checkOutput bool, fn ownerFunc) contract.Handler {
	return func(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
		if err := f.spec.check(f.spec.in[op], inv.Input); err != nil {
			f.t.Errorf("%s input violates the frozen schema: %v\n%s", op, err, inv.Input)
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInvalidInput, Message: err.Error()}
		}
		var bounded struct {
			Limit int64 `json:"limit"`
		}
		if json.Unmarshal(inv.Input, &bounded) == nil && bounded.Limit > 0 {
			f.mu.Lock()
			f.limits[op] = bounded.Limit
			f.mu.Unlock()
		}
		inj := f.take(op)
		if inj != nil && inj.before != nil {
			inj.before()
		}
		if inj != nil && inj.fail != nil {
			if inj.crash {
				f.crash()
			}
			return contract.Payload{}, inj.fail
		}
		out, err := fn(ctx, u, inv.Input)
		if err != nil {
			return contract.Payload{}, err
		}
		data, err := json.Marshal(out)
		if err != nil {
			return contract.Payload{}, err
		}
		if checkOutput {
			if err := f.spec.check(f.spec.out[op], data); err != nil {
				f.t.Errorf("%s fake output violates the frozen schema: %v\n%s", op, err, data)
			}
		}
		if inj != nil && inj.loseAck {
			f.db.armLostAck()
		}
		if inj != nil && inj.crash {
			f.crash()
			f.db.armLostAck()
		}
		return contract.Payload{Status: contract.StatusCompleted, Data: data}, nil
	}
}

func (f *fx) catalog() *catalog {
	c := &catalog{ops: map[string]catalogOp{}}
	add := func(id, owner, mode string, callers []string, h contract.Handler) {
		c.ops[id] = catalogOp{h: h, desc: contract.Descriptor{
			ID: id, Version: 1, Owner: owner, Visibility: contract.VisibilityInternal, Mode: mode, Callers: callers,
		}}
	}
	ctl := []string{"controller"}
	add("_identity.authority", "identity", contract.ModeQuery, []string{"application"}, f.identityAuthority)
	for id, fn := range map[string]ownerFunc{
		"_effects.admit":                 f.effectsAdmit,
		"_effects.claim":                 f.effectsClaim,
		"_effects.record":                f.effectsRecord,
		"_execution.fence":               f.executionFence,
		"_execution.tick":                f.executionTick,
		"_execution.observation":         f.executionObservation,
		"_execution.job.claim":           f.jobClaim,
		"_execution.job.record":          f.jobRecord,
		"_scheduling.wake.admit":         f.wakeAdmit,
		"_memory.record":                 f.memoryRecord,
		"_connections.validation.record": f.validationRecord,
		"_artifacts.publish":             f.artifactsPublish,
		"_installation.restore.record":   f.restoreRecord,
		"_execution.turn.admit":          f.executionTurnAdmit,
		"_execution.work.claim":          f.executionWorkClaim,
		"_execution.context.prepare":     f.executionContextPrepare,
		"_execution.context.commit":      f.executionContextCommit,
		"_execution.proposal.prepare":    f.executionProposalPrepare,
		"_execution.proposal.record":     f.executionProposalRecord,
		"_execution.report":              f.executionReport,
		"_execution.verification.claim":  f.executionVerificationClaim,
		"_execution.verification.record": f.executionVerificationRecord,
	} {
		add(id, strings.SplitN(strings.TrimPrefix(id, "_"), ".", 2)[0], contract.ModeMutation, ctl, f.handler(id, true, fn))
	}
	for id, fn := range map[string]ownerFunc{
		"_effects.pending":                f.effectsPending,
		"_execution.job.pending":          f.jobPending,
		"_scheduling.wake.due":            f.wakeDue,
		"_messaging.ready":                f.messagingReady,
		"_execution.work.pending":         f.executionWorkPending,
		"_execution.verification.pending": f.executionVerificationPending,
	} {
		add(id, strings.SplitN(strings.TrimPrefix(id, "_"), ".", 2)[0], contract.ModeQuery, ctl, f.handler(id, true, fn))
	}
	// Ready tasks are only counted by the controller; the fake returns bare
	// identities, so its output is not held to the full Task schema.
	add("_tasks.ready", "tasks", contract.ModeQuery, ctl, f.handler("_tasks.ready", false, f.tasksReady))
	return c
}

func fixtureMigrations() []contract.Migration {
	mig := func(owner, sql string) contract.Migration {
		return contract.Migration{Owner: owner, Version: 1, SQL: sql, SHA256: contract.Hash([]byte(sql))}
	}
	migV := func(owner string, version int64, sql string) contract.Migration {
		return contract.Migration{Owner: owner, Version: version, SQL: sql, SHA256: contract.Hash([]byte(sql))}
	}
	return []contract.Migration{
		mig("identity", `CREATE TABLE identity_principals (id TEXT PRIMARY KEY, kind TEXT NOT NULL, revoked INTEGER NOT NULL);`),
		mig("effects", `
CREATE TABLE effects_operations (seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, version INTEGER NOT NULL,
	state TEXT NOT NULL, action TEXT NOT NULL, adapter TEXT NOT NULL, admit_mode TEXT NOT NULL DEFAULT '');
CREATE TABLE effects_attempts (seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, operation_id TEXT NOT NULL,
	generation INTEGER NOT NULL, state TEXT NOT NULL, consumed INTEGER NOT NULL);
CREATE TABLE effects_observations (seq INTEGER PRIMARY KEY AUTOINCREMENT, operation_id TEXT NOT NULL, attempt_id TEXT NOT NULL,
	kind TEXT NOT NULL, disposition TEXT NOT NULL, evidence TEXT NOT NULL, usage TEXT NOT NULL);`),
		migV("effects", 2, `ALTER TABLE effects_operations ADD COLUMN callback_route TEXT NOT NULL DEFAULT '';`),
		mig("execution", `
CREATE TABLE execution_fences (seq INTEGER PRIMARY KEY AUTOINCREMENT, generation INTEGER NOT NULL, reason TEXT NOT NULL);
CREATE TABLE execution_ticks (seq INTEGER PRIMARY KEY AUTOINCREMENT, now TEXT NOT NULL);
CREATE TABLE execution_observations (operation_id TEXT PRIMARY KEY, attempt_id TEXT NOT NULL, disposition TEXT NOT NULL, evidence TEXT NOT NULL);
CREATE TABLE execution_jobs (seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, version INTEGER NOT NULL, state TEXT NOT NULL,
	owner TEXT NOT NULL, operation TEXT NOT NULL, operation_id TEXT NOT NULL DEFAULT '', input TEXT NOT NULL,
	claimed_generation INTEGER NOT NULL DEFAULT 0, result TEXT NOT NULL DEFAULT '');`),
		migV("execution", 2, turnsFixtureSchema),
		migV("execution", 3, `ALTER TABLE execution_turns ADD COLUMN next_wake TEXT NOT NULL DEFAULT '';`),
		mig("messaging", `
CREATE TABLE messaging_messages (seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, version INTEGER NOT NULL,
	sender_id TEXT NOT NULL, recipient_ids TEXT NOT NULL, scope TEXT NOT NULL, body TEXT NOT NULL,
	state TEXT NOT NULL, turn_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);`),
		mig("scheduling", `
CREATE TABLE scheduling_wakes (seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, source_id TEXT NOT NULL,
	occurrence_key TEXT NOT NULL, due_at TEXT NOT NULL, admitted INTEGER NOT NULL DEFAULT 0);
CREATE TABLE scheduling_cycles (occurrence_key TEXT PRIMARY KEY, wake_id TEXT NOT NULL);`),
		mig("memory", `CREATE TABLE memory_records (seq INTEGER PRIMARY KEY AUTOINCREMENT, operation_id TEXT NOT NULL, job_id TEXT NOT NULL,
	disposition TEXT NOT NULL, evidence TEXT NOT NULL);`),
		mig("connections", `CREATE TABLE connections_validations (seq INTEGER PRIMARY KEY AUTOINCREMENT, connection_id TEXT NOT NULL,
	expected_version INTEGER NOT NULL, disposition TEXT NOT NULL);`),
		mig("artifacts", `CREATE TABLE artifacts_items (id TEXT PRIMARY KEY, digest TEXT NOT NULL, size INTEGER NOT NULL,
	media_type TEXT NOT NULL, classification TEXT NOT NULL, scope TEXT NOT NULL);`),
		mig("installation", `CREATE TABLE installation_restores (seq INTEGER PRIMARY KEY AUTOINCREMENT, job_id TEXT NOT NULL,
	state TEXT NOT NULL, requirements TEXT NOT NULL);`),
	}
}

func fxFault(code, message string) error { return &contract.Fault{Code: code, Message: message} }

func decode(input json.RawMessage, v any) error {
	if err := contract.DecodeStrict(input, v); err != nil {
		return fxFault(contract.CodeInvalidInput, err.Error())
	}
	return nil
}

// ---- identity ----

func (f *fx) identityAuthority(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	var in struct {
		PrincipalID contract.ID    `json:"principal_id"`
		Scope       contract.Scope `json:"scope"`
	}
	if err := decode(inv.Input, &in); err != nil {
		return contract.Payload{}, err
	}
	var kind string
	var revoked int
	err := u.QueryRowContext(ctx, `SELECT kind, revoked FROM identity_principals WHERE id = ?`, string(in.PrincipalID)).Scan(&kind, &revoked)
	if err != nil {
		return contract.Payload{}, fxFault(contract.CodeNotFound, "principal is unknown")
	}
	data, _ := json.Marshal(map[string]any{"resource": map[string]any{
		"principal": map[string]any{
			"id": in.PrincipalID, "version": 1, "kind": kind, "name": "controller",
			"scope": in.Scope, "revoked": revoked == 1,
		},
		"grants": []any{}, "restrictions": []string{},
	}})
	return contract.Payload{Status: contract.StatusCompleted, Data: data}, nil
}
