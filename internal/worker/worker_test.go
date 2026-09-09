// internal/worker/worker_test.go
package worker

import (
	"context"
	"errors"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"zatiti/internal/state"
)

type manualTicker struct {
	mu sync.Mutex
	ch chan time.Time
}

func (m *manualTicker) C() <-chan time.Time { return m.ch }
func (m *manualTicker) Stop()               {}
func (m *manualTicker) tick() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ch <- time.Now()
}

// swapTicker replaces newTicker for the test's duration.
func swapTicker(t *testing.T, mk func(time.Duration) ticker) {
	t.Helper()
	orig := newTicker
	newTicker = mk
	t.Cleanup(func() { newTicker = orig })
}

func TestRunTaskAbortsOnLeaseLoss(t *testing.T) {
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := state.Bootstrap(context.Background(), st, provStub{}); err != nil {
		t.Fatal(err)
	}
	gen, _ := st.Generation(context.Background())

	mt := &manualTicker{ch: make(chan time.Time, 8)}
	swapTicker(t, func(time.Duration) ticker { return mt })

	w := New(st.DB(), gen, "w1", log.New(os.Stderr, "", 0))
	started := make(chan struct{})
	workDone := make(chan struct{})
	errc := make(chan error, 1)
	go func() {
		errc <- w.RunTask(context.Background(), "task/x", func(ctx context.Context) error {
			close(started)
			<-ctx.Done() // work runs until the lease dies
			close(workDone)
			return ctx.Err()
		})
	}()
	<-started
	mt.tick() // successful heartbeat: work continues

	// Steal the lease underneath the worker (simulating expiry + takeover).
	if err := state.AcquireLease(context.Background(), st.DB(), "task/x", "w2", gen); err != nil {
		t.Fatalf("takeover: %v", err)
	}
	mt.tick() // heartbeat now fails → cancel → work observes ctx

	select {
	case <-workDone:
	case <-time.After(2 * time.Second):
		t.Fatal("work not aborted after lease loss")
	}
	if err := <-errc; err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("RunTask err = %v, want context.Canceled", err)
	}
}

type provStub struct{}

func (provStub) ProvisionOwner(ctx context.Context) (state.PrincipalRef, error) {
	return state.PrincipalRef{ID: "worker-test", PublicKey: []byte("k")}, nil
}
