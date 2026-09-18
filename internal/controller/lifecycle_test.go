package controller

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/platform"
)

func run(ctx context.Context, c *Controller) <-chan error {
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	return done
}

func await(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(30 * time.Second):
		t.Fatal("controller did not return")
		return nil
	}
}

// Z01: a second controller against the same state directory is refused the
// exclusive lock, and the controller itself refuses ownership that is not
// held, so no second scheduler or writer can appear.
func TestDuplicateControllerIsRefused(t *testing.T) {
	f := newFx(t)
	keyDir := t.TempDir()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	keyPath := filepath.Join(keyDir, "master.key")
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	open := func() *platform.Platform {
		p, err := platform.Open(platform.Config{StateDir: f.dir, CredentialBackend: "headless", MasterKeyRef: "file:" + keyPath})
		if err != nil {
			t.Fatalf("platform.Open: %v", err)
		}
		t.Cleanup(func() { _ = p.Close() })
		return p
	}
	ctx := context.Background()
	held, err := open().Acquire(ctx)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	t.Cleanup(func() { _ = held.Close() })
	if _, err := open().Acquire(ctx); err == nil {
		t.Fatal("a second controller acquired the installation lock")
	}

	provider := f.adapter("synthetic")
	op := f.prepare("synthetic", nil)
	first, err := New(Config{StateDir: f.dir}, f.app, f.db, held, f.adapters, f.clock)
	if err != nil {
		t.Fatalf("New with held ownership: %v", err)
	}
	if err := first.Attach(Collaborators{Identity: f.actor}); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	// A controller handed ownership that is not held never schedules.
	released := newOwnership()
	released.lose()
	_, err = New(Config{StateDir: f.dir}, f.app, f.db, released, f.adapters, f.clock)
	wantFault(t, err, contract.CodeControllerUnavailable)

	// Only the original admits.
	done := run(ctx, first)
	f.clock.awaitParked(t)
	if err := first.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := await(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if provider.calls() != 1 || f.opState(op) != "succeeded" {
		t.Fatalf("original controller: calls %d state %s", provider.calls(), f.opState(op))
	}

	// Once the lock is released the same controller value cannot restart,
	// and a new one over the released ownership is refused.
	if err := held.Close(); err != nil {
		t.Fatalf("release: %v", err)
	}
	_, err = New(Config{StateDir: f.dir}, f.app, f.db, held, f.adapters, f.clock)
	wantFault(t, err, contract.CodeControllerUnavailable)
	wantFault(t, first.Run(ctx), contract.CodeConflict)
}

func TestNewAndAttachValidate(t *testing.T) {
	f := newFx(t)
	own := newOwnership()
	cases := []struct {
		name string
		make func() error
		code string
	}{
		{"nil application", func() error { _, err := New(Config{StateDir: f.dir}, nil, f.db, own, nil, f.clock); return err }, contract.CodeInvalidInput},
		{"nil database", func() error { _, err := New(Config{StateDir: f.dir}, f.app, nil, own, nil, f.clock); return err }, contract.CodeInvalidInput},
		{"nil ownership", func() error { _, err := New(Config{StateDir: f.dir}, f.app, f.db, nil, nil, f.clock); return err }, contract.CodeInvalidInput},
		{"nil clock", func() error { _, err := New(Config{StateDir: f.dir}, f.app, f.db, own, nil, nil); return err }, contract.CodeInvalidInput},
		{"no state dir", func() error { _, err := New(Config{}, f.app, f.db, own, nil, f.clock); return err }, contract.CodeInvalidInput},
		{"batch above owner maximum", func() error {
			_, err := New(Config{StateDir: f.dir, MaxDispatch: 101}, f.app, f.db, own, nil, f.clock)
			return err
		}, contract.CodeInvalidInput},
		{"negative tick", func() error {
			_, err := New(Config{StateDir: f.dir, TickInterval: -1}, f.app, f.db, own, nil, f.clock)
			return err
		}, contract.CodeInvalidInput},
		{"misnamed adapter", func() error {
			_, err := New(Config{StateDir: f.dir}, f.app, f.db, own,
				map[string]contract.Adapter{"other": &fakeAdapter{name: "synthetic", mu: make(chan struct{}, 1)}}, f.clock)
			return err
		}, contract.CodeInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { wantFault(t, tc.make(), tc.code) })
	}

	c, err := New(Config{StateDir: f.dir}, f.app, f.db, own, nil, f.clock)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.cfg.TickInterval != time.Second || c.cfg.MaxDispatch != 100 {
		t.Fatalf("defaults: %+v", c.cfg)
	}
	wantFault(t, c.Attach(Collaborators{Identity: contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindHuman}}), contract.CodeInvalidInput)
	wantFault(t, c.Attach(Collaborators{Identity: f.actor, Jobs: map[string]JobRunner{"a/b": nil}}), contract.CodeInvalidInput)
}

// Prerequisites the frozen constructor cannot carry fail the run by name
// instead of producing a controller that silently does nothing.
func TestRunRefusesMissingPrerequisites(t *testing.T) {
	t.Run("no service identity", func(t *testing.T) {
		f := newFx(t)
		c, err := New(Config{StateDir: f.dir}, f.app, f.db, newOwnership(), nil, f.clock)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		wantFault(t, c.Run(context.Background()), contract.CodePrerequisiteMissing)
		if f.called("_execution.fence") != 0 {
			t.Fatal("no owner call may be made without an identity")
		}
	})
	t.Run("unregistered identity is refused by the owner gate", func(t *testing.T) {
		f := newFx(t)
		c, err := New(Config{StateDir: f.dir}, f.app, f.db, newOwnership(), nil, f.clock)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		stranger := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService}
		if err := c.Attach(Collaborators{Identity: stranger}); err != nil {
			t.Fatalf("Attach: %v", err)
		}
		wantFault(t, c.Run(context.Background()), contract.CodePermissionDenied)
	})
	t.Run("revoked identity fails closed at the fence", func(t *testing.T) {
		f := newFx(t)
		f.exec(`UPDATE identity_principals SET revoked = 1`)
		c := f.controller(nil)
		wantFault(t, c.Run(context.Background()), contract.CodePermissionDenied)
		if f.called("_effects.pending") != 0 {
			t.Fatal("nothing may be scanned after a failed fence")
		}
	})
}

// Ownership.Lost stops admission immediately: between ticks the loop ends,
// and inside a tick no further unit is admitted after the loss.
func TestOwnershipLossStopsAdmissionImmediately(t *testing.T) {
	t.Run("between ticks", func(t *testing.T) {
		f := newFx(t)
		provider := f.adapter("synthetic")
		own := newOwnership()
		c := f.controller(own)
		done := run(context.Background(), c)
		f.clock.awaitParked(t)
		op := f.prepare("synthetic", nil)
		own.lose()
		wantFault(t, await(t, done), contract.CodeControllerUnavailable)
		if provider.calls() != 0 || f.opState(op) != "prepared" {
			t.Fatal("work was admitted after ownership was lost")
		}
		if c.Status().Admitting {
			t.Fatal("status still reports admitting")
		}
	})
	t.Run("inside a tick", func(t *testing.T) {
		f := newFx(t)
		provider := f.adapter("synthetic")
		first := f.prepare("synthetic", nil)
		second := f.prepare("synthetic", nil)
		own := newOwnership()
		c := f.controller(own)
		sess, err := c.start(context.Background())
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		t.Cleanup(func() { _ = sess.journal.close() })
		// Ownership is lost while the first admission is in its transaction.
		f.arm("_effects.admit", injection{before: own.lose})
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
		if got := f.called("_effects.admit"); got != 1 {
			t.Fatalf("admit called %d times; the second operation was admitted after the loss", got)
		}
		if provider.calls() != 0 {
			t.Fatalf("provider invoked %d times after ownership was lost", provider.calls())
		}
		if f.called("_effects.claim") != 0 {
			t.Fatal("a claim was consumed after ownership was lost")
		}
		if f.opState(second) != "prepared" {
			t.Fatalf("second operation is %s", f.opState(second))
		}
		_ = first
	})
}

// Graceful stop versus unknown outcome.
func TestStopDrainsOrRetainsUnknown(t *testing.T) {
	t.Run("graceful stop records the real outcome", func(t *testing.T) {
		f := newFx(t)
		provider := f.adapter("synthetic")
		entered := make(chan struct{})
		release := make(chan struct{})
		provider.reply = func(context.Context, contract.Dispatch) (contract.Observation, error) {
			close(entered)
			<-release
			return succeeded(nil), nil
		}
		op := f.prepare("synthetic", nil)
		c := f.controller(nil)
		done := run(context.Background(), c)
		<-entered

		stopped := make(chan error, 1)
		go func() { stopped <- c.Stop(context.Background()) }()
		// Admission closes before the in-flight call finishes.
		<-c.stopCh
		if c.Status().Admitting {
			t.Fatal("Stop did not close admission at once")
		}
		late := f.prepare("synthetic", nil)
		close(release)
		if err := await(t, stopped); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if err := await(t, done); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := f.observations(op); !reflect.DeepEqual(got, []string{"physical:succeeded"}) {
			t.Fatalf("in-flight work was not recorded on graceful stop: %v", got)
		}
		if f.opState(late) != "prepared" || provider.calls() != 1 {
			t.Fatal("work was admitted after Stop")
		}
	})
	t.Run("deadline retains unknown and never invents cancellation", func(t *testing.T) {
		f := newFx(t)
		provider := f.adapter("synthetic")
		entered := make(chan struct{})
		provider.reply = func(ctx context.Context, _ contract.Dispatch) (contract.Observation, error) {
			close(entered)
			<-ctx.Done()
			// A provider-side cancellation claim the controller must not
			// record on the provider's behalf.
			return contract.Observation{Disposition: contract.DispositionNotSent,
				Evidence: []byte(`{"cancelled":true}`), Usage: []byte(fxUsage)}, nil
		}
		op := f.prepare("synthetic", nil)
		c := f.controller(nil)
		done := run(context.Background(), c)
		<-entered

		expired, cancel := context.WithCancel(context.Background())
		cancel()
		wantFault(t, c.Stop(expired), contract.CodeOutcomeUnknown)
		if err := await(t, done); err != nil {
			t.Fatalf("Run: %v", err)
		}
		c.workers.Wait()
		if got := f.observations(op); got != nil {
			t.Fatalf("an abandoned call wrote %v after the shutdown deadline", got)
		}
		if got := f.opState(op); got != "executing" {
			t.Fatalf("operation is %s; shutdown must not decide the outcome", got)
		}

		// The next generation records the ambiguity; the provider is not
		// called again.
		provider.reply = nil
		f.restart()
		next, sess := f.started()
		if err := f.pass(next, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
		if got := f.observations(op); !reflect.DeepEqual(got, []string{"physical:unknown"}) {
			t.Fatalf("observations %v", got)
		}
		if f.opState(op) != "outcome_unknown" || provider.calls() != 1 {
			t.Fatalf("state %s calls %d", f.opState(op), provider.calls())
		}
	})
	t.Run("stop before run", func(t *testing.T) {
		f := newFx(t)
		c := f.controller(nil)
		if err := c.Stop(context.Background()); err != nil {
			t.Fatalf("Stop: %v", err)
		}
		wantFault(t, c.Run(context.Background()), contract.CodeConflict)
	})
}

// Z21: the controller has no client in its lifetime. Work keeps running with
// no client connected, and ending the admission context — the most a closing
// client or a shutdown signal can do — does not abort a provider call that
// is already in flight; the call is recorded.
func TestWorkContinuesWhenTheClientCloses(t *testing.T) {
	f := newFx(t)
	provider := f.adapter("synthetic")
	entered := make(chan struct{})
	release := make(chan struct{})
	var callErr error
	provider.reply = func(ctx context.Context, _ contract.Dispatch) (contract.Observation, error) {
		close(entered)
		<-release
		callErr = ctx.Err()
		return succeeded(nil), nil
	}
	c := f.controller(nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := run(ctx, c)

	// Several scheduler passes with no client at all.
	for i := 0; i < 3; i++ {
		f.clock.awaitParked(t)
		f.clock.Advance(time.Second)
	}
	f.clock.awaitParked(t)
	if got := f.queryInt(`SELECT COUNT(*) FROM execution_ticks`); got < 4 {
		t.Fatalf("scheduler ran %d passes without a client, want at least 4", got)
	}

	op := f.prepare("synthetic", nil)
	f.clock.Advance(time.Second)
	<-entered
	cancel()
	close(release)
	if err := await(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if callErr != nil {
		t.Fatalf("the in-flight provider call saw %v when the admission context ended", callErr)
	}
	if got := f.observations(op); !reflect.DeepEqual(got, []string{"physical:succeeded"}) {
		t.Fatalf("observations %v", got)
	}
	if f.opState(op) != "succeeded" {
		t.Fatalf("operation is %s", f.opState(op))
	}
}

// The tick loop is paced only by the injected clock.
func TestLoopIsPacedByTheInjectedClock(t *testing.T) {
	f := newFx(t)
	c := f.controller(nil)
	ticks := make(chan int64, 16)
	c.afterTick = func(n int64) { ticks <- n }
	done := run(context.Background(), c)
	if n := <-ticks; n != 1 {
		t.Fatalf("first tick %d", n)
	}
	f.clock.awaitParked(t)
	// Less than the interval: no tick.
	f.clock.Advance(999 * time.Millisecond)
	select {
	case n := <-ticks:
		t.Fatalf("tick %d fired before the interval elapsed", n)
	default:
	}
	f.clock.Advance(time.Millisecond)
	if n := <-ticks; n != 2 {
		t.Fatalf("second tick %d", n)
	}
	now := f.queryString(`SELECT now FROM execution_ticks ORDER BY seq DESC LIMIT 1`)
	if want := f.clock.Now().Format(time.RFC3339Nano); now != want {
		t.Fatalf("owner saw now=%s, want the injected %s", now, want)
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := await(t, done); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
