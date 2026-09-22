package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/controller"
	"github.com/zatiti/zatiti/internal/execution"
)

// P46 item 1: a full real-owner fixture with the actual controller, a
// Responses adapter pointed at a controlled provider, the real artifact
// store and the real trusted verifier -- fake only external services and
// time. controllerFixture extends the package's existing real-module
// fixture (fixture_test.go) with a genuinely running internal/controller:
// the real scheduler loop, the real WorkerOperator (application.Application
// itself, exactly as cmd/zatiti/serve.go wires it), the real trusted
// verifier (execution.NewVerifier) and every landed local job runner,
// discovered the same way cmd/zatiti's modules()/addJobRunner does. No
// RestoreLifecycle is attached: tests/integration has no more access to
// internal/installation's private bundle-decrypt code than
// internal/controller itself does (see cmd/zatiti/restore.go's
// restoreLifecycle{}), so a restore job this controller observes is
// honestly recorded failed with prerequisite_missing, exactly per
// Collaborators.RestoreLifecycle's own documented fallback -- see
// restore_controller_test.go.
type controllerFixture struct {
	*fixture
	ctl    *controller.Controller
	cancel context.CancelFunc
	done   chan error
}

// controllerIdentity resolves the "controller" service principal bootstrap
// created, the way cmd/zatiti's resolveControllerIdentity does: by reading
// the principal back, never by asserting an identity of its own.
func (f *fixture) controllerIdentity() contract.Actor {
	f.t.Helper()
	for _, p := range f.principals() {
		if p.Kind == contract.KindService && p.Name == "controller" {
			return contract.Actor{PrincipalID: p.ID, Kind: contract.KindService}
		}
	}
	f.t.Fatal("bootstrap created no controller service principal")
	return contract.Actor{}
}

// landedJobKind names one durable local job's owner and exact operation,
// mirroring cmd/zatiti/jobs.go's landedJobKinds table (the owner/operation
// pairs this tree's domain packages actually implement contract.
// LocalJobRunner for, confirmed there by reading each RunJob directly).
type landedJobKind struct{ owner, operation string }

var landedJobKinds = []landedJobKind{
	{"execution", "run_export"},
	{"execution", "claim_context"},
	{"skills", "skill.evaluate"},
	{"configuration", "organization.export"},
	{"configuration", "team.export"},
	{"configuration", "project.export"},
}

// jobRunnerShim adapts one owner's contract.LocalJobRunner to controller.
// JobRunner, exactly as cmd/zatiti/jobs.go's localJobShim does.
type jobRunnerShim struct{ runner contract.LocalJobRunner }

func (s jobRunnerShim) RunJob(ctx context.Context, job controller.Job) (controller.JobOutcome, error) {
	out, err := s.runner.RunJob(ctx, contract.JobWork{ID: job.ID, Owner: job.Owner, Operation: job.Operation, Input: job.Input})
	if err != nil {
		return controller.JobOutcome{}, err
	}
	reqs := make([]controller.Requirement, 0, len(out.Requirements))
	for _, r := range out.Requirements {
		req := controller.Requirement{Code: r.Code, Message: r.Message}
		if r.ResourceID != nil {
			req.ResourceID = *r.ResourceID
		}
		if r.ChallengeID != nil {
			req.ChallengeID = *r.ChallengeID
		}
		reqs = append(reqs, req)
	}
	return controller.JobOutcome{State: out.State, Result: out.Result, EvidenceIDs: out.EvidenceIDs, Requirements: reqs}, nil
}

// jobRunners builds the real Collaborators.Jobs map from this fixture's own
// already-constructed real modules, discovered generically by type
// assertion (contract.LocalJobRunner) exactly as cmd/zatiti/assembly.go's
// addJobRunner does, then filtered to the frozen landedJobKinds table
// exactly as cmd/zatiti/jobs.go's buildJobRunners does.
func (f *fixture) jobRunners() map[string]controller.JobRunner {
	f.t.Helper()
	owners := map[string]contract.LocalJobRunner{}
	for _, m := range f.modules {
		if r, ok := m.(contract.LocalJobRunner); ok {
			owners[m.Name()] = r
		}
	}
	jobs := map[string]controller.JobRunner{}
	for _, k := range landedJobKinds {
		if runner, ok := owners[k.owner]; ok {
			jobs[controller.JobKey(k.owner, k.operation)] = jobRunnerShim{runner: runner}
		}
	}
	return jobs
}

// controllerFixtureOptions configures newControllerFixture/attachController.
// The zero value is a controller with no adapters attached (a cooperative-
// only fixture: nothing it dispatches ever calls out).
type controllerFixtureOptions struct {
	adapters map[string]contract.Adapter
}

// newControllerFixture bootstraps an installation and attaches a real
// controller over it, wired with the real trusted verifier and the real
// worker operation executor (the application itself implements
// contract.WorkerOperator directly -- internal/application/worker.go,
// confirmed by cmd/zatiti/serve.go's own Collaborators.Operator: h.app
// comment), every landed local job runner and any adapters opts names. It
// runs the controller's scheduler loop on a fast real-wall-clock tick until
// the test ends.
func newControllerFixture(t testing.TB, opts controllerFixtureOptions) *controllerFixture {
	t.Helper()
	f := newBootstrappedFixture(t)
	return attachController(t, f, opts)
}

// attachController is newControllerFixture's body, factored out so a case
// that needs the underlying *fixture before the controller exists (for
// example to seed a hosted worker's configuration, or to drive a journey
// that starts over a served transport, before the scheduler starts driving
// it) can call newBootstrappedFixture itself first.
func attachController(t testing.TB, f *fixture, opts controllerFixtureOptions) *controllerFixture {
	t.Helper()
	adapters := opts.adapters
	if adapters == nil {
		adapters = map[string]contract.Adapter{}
	}
	ctl, err := controller.New(controller.Config{StateDir: f.stateDir, TickInterval: 15 * time.Millisecond}, f.app, f.db, f.own, adapters, f.clock)
	if err != nil {
		t.Fatalf("controller.New: %v", err)
	}
	verifier, err := execution.NewVerifier(contract.VerifierDependencies{Clock: f.clock, Blobs: f.plat.Blobs()})
	if err != nil {
		t.Fatalf("execution.NewVerifier: %v", err)
	}
	if err := ctl.Attach(controller.Collaborators{
		Identity: f.controllerIdentity(),
		Blobs:    f.plat.Blobs(),
		Jobs:     f.jobRunners(),
		Operator: f.app,
		Verifier: verifier,
		// RestoreLifecycle intentionally omitted: see this file's package
		// doc comment and restore_controller_test.go.
	}); err != nil {
		t.Fatalf("controller.Attach: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ctl.Run(ctx) }()
	cf := &controllerFixture{fixture: f, ctl: ctl, cancel: cancel, done: done}
	t.Cleanup(cf.stop)
	return cf
}

func (cf *controllerFixture) stop() {
	cf.cancel()
	select {
	case <-cf.done:
	case <-time.After(10 * time.Second):
		cf.t.Errorf("controller did not stop within 10s of cancellation")
	}
}

// waitFor polls cond in real wall-clock time (the controller's own
// scheduler loop runs on the real clock -- the fixture's deterministic
// stepClock only stamps persisted timestamps, see fixture_test.go) until it
// reports true, failing the test naming what it was waiting for if timeout
// elapses first. This is ordinary bounded Go-test polling of a background
// goroutine this same test process owns (ADR-style: ended by t.Fatalf, not
// an infinite loop), never a cross-process/cross-session queue poll.
func waitFor(t testing.TB, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
