// internal/worker/worker.go
//
// Worker runtime: lease-guarded task execution. The worker is
// generation-aware (it receives the controller generation) and
// heartbeat-driven (renews inside LeaseTTL while work runs). Loss of
// lease aborts the work via context cancellation — the work function
// MUST respect ctx.

package worker

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"zatiti/internal/state"
)

// Worker runs leased tasks against a store.
type Worker struct {
	id         string
	generation uint64
	db         *sql.DB
	log        *log.Logger
}

// New builds a worker. workerID should be unique per process
// (hostname+pid or a UUID); the caller supplies it.
func New(db *sql.DB, generation uint64, workerID string, logf *log.Logger) *Worker {
	return &Worker{id: workerID, generation: generation, db: db, log: logf}
}

// RunTask claims resource and runs fn under lease guard. Returns
// state.ErrLeaseHeld if another live worker owns it. Blocks retrying
// the heartbeat until fn returns; fn receives a context that cancels
// if the lease is lost.
func (w *Worker) RunTask(ctx context.Context, resource string, fn func(ctx context.Context) error) error {
	if err := state.AcquireLease(ctx, w.db, resource, w.id, w.generation); err != nil {
		return err // ErrLeaseHeld or I/O
	}
	w.log.Printf("worker %s: acquired %s", w.id, resource)

	taskCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errc := make(chan error, 1)
	go func() {
		t := newTicker(state.LeaseTTL / 3)
		defer t.Stop()
		for {
			select {
			case <-taskCtx.Done():
				errc <- taskCtx.Err()
				return
			case <-t.C():
				if err := state.RenewLease(taskCtx, w.db, resource, w.id, w.generation); err != nil {
					w.log.Printf("worker %s: heartbeat failed: %v — aborting task", w.id, err)
					cancel() // lease lost: kill the work
				}
			}
		}
	}()

	workErr := fn(taskCtx)
	cancel()
	<-errc // heartbeat goroutine exits

	// Release only if we still hold it; loss is fine.
	if rerr := state.ReleaseLease(context.Background(), w.db, resource, w.id, w.generation); rerr != nil {
		w.log.Printf("worker %s: release %s: %v", w.id, resource, rerr)
	}
	if workErr != nil {
		return fmt.Errorf("worker: task %s: %w", resource, workErr)
	}
	if err := <-errc; err != nil && ctx.Err() == nil {
		return fmt.Errorf("worker: %s: %w", resource, err)
	}
	return nil
}

// ticker abstracts time.Ticker for test acceleration.
type ticker interface {
	C() <-chan time.Time
	Stop()
}

type realTicker struct{ *time.Ticker }

var newTicker = func(d time.Duration) ticker { return realTicker{time.NewTicker(d)} }

func (t realTicker) C() <-chan time.Time { return t.Ticker.C }
