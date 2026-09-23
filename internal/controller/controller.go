package controller

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zatiti/zatiti/internal/application"
	"github.com/zatiti/zatiti/internal/contract"
)

const (
	// DefaultTickInterval is the shipped scheduler period.
	DefaultTickInterval = time.Second
	// MaxBatch is the largest per-tick batch any owner scan accepts.
	MaxBatch = 100
	// installationConcurrency is the contract default for concurrent
	// physical work in one installation. The controller has no allowed call
	// that reads a configured installation limit, so the default binds.
	installationConcurrency = 4

	operationVersion = 1
)

// Config configures one controller lifetime.
type Config struct {
	// StateDir is the absolute installation state directory. The controller
	// keeps its dispatch journal below it.
	StateDir string
	// TickInterval is the scheduler period; zero selects DefaultTickInterval.
	TickInterval time.Duration
	// MaxDispatch bounds every per-tick batch; zero selects MaxBatch.
	MaxDispatch int
}

// Collaborators are the trusted dependencies the frozen constructor has no
// parameter for. Assembly attaches them once, before Run. Identity is
// required; work that needs an absent optional collaborator stops at that
// exact point with prerequisite_missing and stays inspectable.
type Collaborators struct {
	// Identity is the explicitly provisioned service principal every
	// internal call runs under. It must be a current, unrevoked, unrestricted
	// service identity of this installation.
	Identity contract.Actor
	// Blobs publishes adapter staged outputs. Without it an observation that
	// carries staged outputs is recorded but its publication stays an open
	// obligation and its owner callback is withheld.
	Blobs contract.BlobStore
	// Jobs maps "owner/operation" to the runner of that durable job kind. A
	// pending job without a runner is never claimed.
	Jobs map[string]JobRunner
	// Operator drives a worker-authored local_operation proposal through the
	// real public-operation authorization boundary, under the worker's own
	// actor. Without it a "prepared" local_operation proposal is never
	// driven and stays an open obligation.
	Operator contract.WorkerOperator
	// Verifier independently establishes task acceptance against pinned
	// verifier code. Without it a claimed VerificationRequest is never
	// executed and stays an open obligation.
	Verifier contract.Verifier
	// RestoreLifecycle drives the two steps of an exclusive restore handoff
	// only entrypoint assembly can perform, because they need internal/
	// installation's own private backup-bundle/key/overlay code, which the
	// controller must not duplicate: staging a verified backup artifact
	// into a decrypted local candidate database file, and merging a
	// restore's captured RecoveryOverlay into every owner's own tables. A
	// restore job observed without it is recorded failed with
	// prerequisite_missing, never guessed at.
	RestoreLifecycle RestoreLifecycle
}

// Controller owns one controller lifetime: the scheduler loop, adapter
// dispatch, durable background work and recovery coordination.
type Controller struct {
	cfg      Config
	app      *application.Application
	db       contract.Database
	own      contract.Ownership
	adapters map[string]contract.Adapter
	clock    contract.Clock
	log      *slog.Logger

	mu     sync.Mutex
	deps   Collaborators
	state  int
	stopCh chan struct{}
	done   chan struct{}
	force  chan struct{}
	cancel context.CancelFunc
	status Status

	// closed stops admission; abandoned forbids every further write.
	closed    atomic.Bool
	abandoned atomic.Bool
	// restoring is true from the moment an exclusive restore handoff is
	// discovered or resumed; ordinary admission (wakes, ready scan,
	// execution tick, turn work, jobs, dispatch) stops for the rest of this
	// lifetime the instant it is set, since a lifetime that itself performs
	// the swap never admits again (see restore.go, ErrRestoreHandoff).
	restoring atomic.Bool
	// restoreHandoff is closed exactly once, by the restore goroutine that
	// itself performed a swap, to end Run without treating it as a fault or
	// a displacement.
	restoreHandoff chan struct{}
	// adopted is the freshly reopened database CommitRestore handed this
	// controller in exchange for closing the pre-restore one. It is the only
	// database handle this controller ever owns: the one Config/New was
	// constructed with belongs to entrypoint assembly and is never closed
	// here. Run closes this one when it ends, because nothing else can --
	// entrypoint's own handle field still names the pre-swap database, so
	// reassembly closes that and opens a third. Without this the process
	// keeps one extra live SQLite connection pool on the live file for
	// every restore it performs.
	adopted contract.Database
	// gate serializes the force-stop against write steps in flight, so no
	// worker writes after Stop has returned at its deadline.
	gate sync.RWMutex

	slots   chan struct{}
	workers sync.WaitGroup

	// busy and reported are guarded by mu; backoff belongs to the loop.
	busy     map[string]struct{}
	backoff  map[contract.ID]backoffState
	reported map[string]struct{}

	// reconcileBackoff paces repeated reconciliation of one persistently
	// uncertain operation. Unlike backoff (touched only from the tick
	// goroutine), a reconciliation read settles on its own worker goroutine,
	// so this one is guarded by mu (reconcile.go).
	reconcileBackoff map[contract.ID]backoffState

	// turnsMu guards the turn-routing indexes the turn-work phase refreshes
	// every tick from live state before dispatch runs: turnAttempts resolves
	// a worker_turn callback route's turn_id to the attempt _execution.
	// observation requires, and turnProposals resolves a prepared
	// external_tool effect's operation id to the proposal_id
	// _execution.proposal.record requires. Neither is durable across a
	// restart by design (see turns.go) -- the next tick's discovery phase
	// rebuilds both from the owner's own live state before any routed
	// delivery is attempted.
	turnsMu       sync.Mutex
	turnAttempts  map[contract.ID]contract.ID
	attemptTurns  map[contract.ID]turnRouteInfo
	turnProposals map[contract.ID]turnProposalRef

	// afterTick lets tests observe loop progress without sleeping.
	afterTick func(n int64)
}

const (
	stateNew = iota
	stateRunning
	stateStopped
)

type backoffState struct {
	failures int
	until    int64
}

// session is the immutable per-Run context resolved at startup.
type session struct {
	actor      contract.Actor
	scope      contract.Scope
	generation int64
	journal    *journal
}

// Status is a point-in-time view of the controller for operators and tests.
type Status struct {
	// Generation is the controller generation this lifetime runs under.
	Generation int64
	// Admitting reports whether new work may still be admitted.
	Admitting bool
	// Ticks counts completed scheduler passes.
	Ticks int64
	// InFlight counts adapter calls and job runs currently outside a
	// transaction.
	InFlight int
	// Invocations counts physical adapter invocations started.
	Invocations int64
	// Recorded counts observations durably recorded by the effects owner.
	Recorded int64
	// Ambiguous counts abandoned claimed attempts recovered as unknown.
	Ambiguous int64
	// ReadyWithoutRun is the last bounded scan of ready tasks lacking a run.
	ReadyWithoutRun int
	// Obligations lists unresolved work the controller cannot finish alone.
	Obligations []Obligation
	// LastFault is the most recent fault a tick phase reported.
	LastFault *contract.Fault
}

// Obligation is one inspectable piece of unresolved work.
type Obligation struct {
	Kind       string
	ResourceID contract.ID
	Fault      contract.Fault
}

// New constructs a controller over an assembled application. The caller has
// already acquired the installation lock, migrated the database and advanced
// the generation; New takes no lock and advances nothing. It refuses
// ownership that is not held: a second controller never schedules.
func New(
	cfg Config,
	app *application.Application,
	db contract.Database,
	own contract.Ownership,
	adapters map[string]contract.Adapter,
	clock contract.Clock,
) (*Controller, error) {
	switch {
	case app == nil:
		return nil, invalidInput("controller requires an assembled application")
	case db == nil:
		return nil, invalidInput("controller requires a database")
	case own == nil:
		return nil, invalidInput("controller requires held installation ownership")
	case clock == nil:
		return nil, invalidInput("controller requires a clock")
	case cfg.StateDir == "":
		return nil, invalidInput("controller requires a state directory")
	case cfg.TickInterval < 0:
		return nil, invalidInput("tick interval must not be negative")
	case cfg.MaxDispatch < 0 || cfg.MaxDispatch > MaxBatch:
		return nil, invalidInput("max dispatch must be between 1 and %d", MaxBatch)
	}
	if !own.Held() {
		return nil, unavailable("installation ownership is not held; another controller owns this state directory")
	}
	if cfg.TickInterval == 0 {
		cfg.TickInterval = DefaultTickInterval
	}
	if cfg.MaxDispatch == 0 {
		cfg.MaxDispatch = MaxBatch
	}
	registered := make(map[string]contract.Adapter, len(adapters))
	for name, adapter := range adapters {
		if adapter == nil {
			return nil, invalidInput("adapter %q is nil", name)
		}
		if adapter.Name() != name {
			return nil, invalidInput("adapter registered as %q names itself %q", name, adapter.Name())
		}
		registered[name] = adapter
	}
	concurrency := installationConcurrency
	if cfg.MaxDispatch < concurrency {
		concurrency = cfg.MaxDispatch
	}
	return &Controller{
		cfg:              cfg,
		app:              app,
		db:               db,
		own:              own,
		adapters:         registered,
		clock:            clock,
		log:              slog.Default().With("component", "controller"),
		stopCh:           make(chan struct{}),
		done:             make(chan struct{}),
		force:            make(chan struct{}),
		restoreHandoff:   make(chan struct{}),
		slots:            make(chan struct{}, concurrency),
		busy:             map[string]struct{}{},
		backoff:          map[contract.ID]backoffState{},
		reported:         map[string]struct{}{},
		reconcileBackoff: map[contract.ID]backoffState{},
		turnAttempts:     map[contract.ID]contract.ID{},
		attemptTurns:     map[contract.ID]turnRouteInfo{},
		turnProposals:    map[contract.ID]turnProposalRef{},
	}, nil
}

// Attach supplies the collaborators the constructor cannot carry. It must be
// called before Run.
func (c *Controller) Attach(deps Collaborators) error {
	if deps.Identity.Kind != contract.KindService || deps.Identity.PrincipalID == "" {
		return invalidInput("controller identity must be a provisioned service principal")
	}
	jobs := make(map[string]JobRunner, len(deps.Jobs))
	for key, runner := range deps.Jobs {
		if runner == nil {
			return invalidInput("job runner %q is nil", key)
		}
		jobs[key] = runner
	}
	deps.Jobs = jobs
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != stateNew {
		return conflictFault("collaborators must be attached before the controller runs")
	}
	c.deps = deps
	return nil
}

// Status returns the current view.
func (c *Controller) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.status
	// An exclusive restore handoff closes ordinary admission even though
	// c.admitting() (governing effect/job claim eligibility, unrelated to
	// this reporting) does not itself know about it.
	out.Admitting = c.state == stateRunning && c.admitting() && !c.restoring.Load()
	out.Obligations = append([]Obligation(nil), c.status.Obligations...)
	if c.status.LastFault != nil {
		f := *c.status.LastFault
		out.LastFault = &f
	}
	return out
}

// Run owns the scheduler until ctx ends, Stop is called, ownership is lost
// or a newer generation supersedes this one. Cancelling ctx stops admission;
// work already outside a transaction keeps its own lifetime so that it can
// record what really happened. Run reports nil for an orderly end and a
// controller_unavailable fault when it was displaced.
func (c *Controller) Run(ctx context.Context) error {
	c.mu.Lock()
	if c.state != stateNew {
		c.mu.Unlock()
		return conflictFault("a controller runs exactly once")
	}
	c.state = stateRunning
	c.mu.Unlock()
	defer close(c.done)

	sess, err := c.start(ctx)
	if err != nil {
		c.closed.Store(true)
		c.markStopped()
		return err
	}
	defer func() { _ = sess.journal.close() }()

	// Work outside transactions outlives the admission context: a closing
	// client or a shutdown signal must not turn into a provider abort.
	workCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	defer cancel()

	var cause error
loop:
	for {
		if cause = c.tick(ctx, workCtx, sess); cause != nil {
			break
		}
		wait, release := c.wait(c.cfg.TickInterval)
		select {
		case <-ctx.Done():
			release()
			break loop
		case <-c.stopCh:
			release()
			break loop
		case <-c.own.Lost():
			release()
			cause = unavailable("installation ownership was lost; admission stopped")
			break loop
		case <-c.restoreHandoff:
			release()
			cause = ErrRestoreHandoff
			break loop
		case <-wait:
		}
	}
	c.closed.Store(true)
	if cause != nil {
		// Displaced: nothing more may be written, so in-flight calls have no
		// one to report to. Their claims stay journaled as ambiguity.
		c.abandon()
	}

	drained := make(chan struct{})
	go func() {
		c.workers.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-c.force:
	}
	// This lifetime is over, so the database it adopted from CommitRestore
	// has no further reader: the loop has ended and nothing reaches
	// c.database() again. Closing unconditionally (rather than only on the
	// drained branch) is deliberate, because abandon() above already closed
	// c.force for every non-nil cause -- including ErrRestoreHandoff -- so a
	// drained-only close would never run on exactly the path that adopts a
	// handle. It is also safe under a forced stop: database/sql's Close
	// closes idle connections immediately and closes an in-use one only when
	// its transaction returns it, so a worker still inside a transaction is
	// never torn mid-statement.
	c.releaseAdopted()
	c.markStopped()
	return cause
}

// Stop ends admission at once and waits for work outside transactions to
// record its real outcome. If ctx ends first, Stop abandons that work: its
// claims are already journaled, the next generation records them as unknown,
// and no cancellation is ever recorded on the provider's behalf. Stop then
// reports outcome_unknown so the caller knows the shutdown was not clean.
func (c *Controller) Stop(ctx context.Context) error {
	c.closed.Store(true)
	c.mu.Lock()
	state := c.state
	if state == stateNew {
		c.state = stateStopped
	}
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
	c.mu.Unlock()
	if state == stateNew {
		return nil
	}
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
	}
	inFlight := c.abandon()
	<-c.done
	if inFlight == 0 {
		return nil
	}
	return &contract.Fault{
		Code: contract.CodeOutcomeUnknown,
		Message: "shutdown deadline passed with work still outside a transaction; " +
			"its outcome is retained as unknown for recovery",
	}
}

// abandon forbids further writes, releases Run and interrupts in-flight
// work. It returns how many units were still outside a transaction.
func (c *Controller) abandon() int {
	c.gate.Lock()
	c.abandoned.Store(true)
	c.gate.Unlock()
	c.mu.Lock()
	inFlight := c.status.InFlight
	cancel := c.cancel
	select {
	case <-c.force:
	default:
		close(c.force)
	}
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return inFlight
}

func (c *Controller) markStopped() {
	c.mu.Lock()
	c.state = stateStopped
	c.mu.Unlock()
}

// admitting reports whether new work may be admitted right now. Ownership
// is consulted on every admission, not once per tick.
func (c *Controller) admitting() bool {
	if c.closed.Load() || c.abandoned.Load() || !c.own.Held() {
		return false
	}
	select {
	case <-c.own.Lost():
		return false
	default:
		return true
	}
}

// write runs one durable step — journal appends and owner transactions —
// unless the controller was abandoned or lost ownership. A refused step
// reports controller_unavailable and leaves the journal as it was.
func (c *Controller) write(step func() error) error {
	c.gate.RLock()
	defer c.gate.RUnlock()
	if c.abandoned.Load() {
		return unavailable("controller was stopped before this step could be written")
	}
	if !c.own.Held() {
		return unavailable("installation ownership is not held; refusing to write")
	}
	return step()
}

// waiter is implemented by clocks that can also pace the loop. The contract
// clock only tells time; a deterministic test clock adds After.
type waiter interface {
	After(time.Duration) <-chan time.Time
}

func (c *Controller) wait(d time.Duration) (<-chan time.Time, func()) {
	if w, ok := c.clock.(waiter); ok {
		return w.After(d), func() {}
	}
	timer := time.NewTimer(d)
	return timer.C, func() { timer.Stop() }
}

func (c *Controller) now() time.Time { return c.clock.Now().UTC() }

// note records a tick-phase fault for Status.
func (c *Controller) note(err error) {
	if err == nil {
		return
	}
	f := *faultOf(err)
	c.mu.Lock()
	c.status.LastFault = &f
	c.mu.Unlock()
}

// oblige publishes one unresolved obligation and logs it once.
func (c *Controller) oblige(kind string, id contract.ID, f *contract.Fault) {
	key := kind + "/" + string(id)
	c.mu.Lock()
	_, seen := c.reported[key]
	if !seen {
		c.reported[key] = struct{}{}
		c.status.Obligations = append(c.status.Obligations, Obligation{Kind: kind, ResourceID: id, Fault: *f})
	}
	c.mu.Unlock()
	if !seen {
		c.log.Warn("unresolved controller obligation", "kind", kind, "resource_id", string(id), "code", f.Code)
	}
}

// resolve withdraws an obligation once the work completed.
func (c *Controller) resolve(kind string, id contract.ID) {
	key := kind + "/" + string(id)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.reported[key]; !ok {
		return
	}
	delete(c.reported, key)
	kept := c.status.Obligations[:0]
	for _, o := range c.status.Obligations {
		if o.Kind != kind || o.ResourceID != id {
			kept = append(kept, o)
		}
	}
	c.status.Obligations = kept
}

func (c *Controller) count(update func(*Status)) {
	c.mu.Lock()
	update(&c.status)
	c.mu.Unlock()
}
