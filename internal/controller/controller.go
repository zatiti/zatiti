// internal/controller/controller.go
//
// Controller runtime root (T1.8): owns the long-running serve loop.
// Responsibilities: assert exclusive ownership (via state.Open),
// advance generation on start, run until signal, and shut down in a
// determined order (stop accepting, drain, release lock last).
// This root never serves MCP or CLI itself; those are transports the
// app root attaches.

package controller

import (
	"context"
	"fmt"
	"log"
	"time"

	"zatiti/internal/state"
	"zatiti/internal/worker"
)

// Controller is the long-running runtime.
type Controller struct {
	store      *state.Store
	generation uint64
	log        *log.Logger
	workerID   string
}

// Start acquires ownership and prepares the runtime. Generation is
// advanced here: any lease or review bound to an older generation
// becomes detectably stale from the first moment this controller runs.
func Start(ctx context.Context, dataDir string, logf *log.Logger) (*Controller, error) {
	store, err := state.Open(dataDir)
	if err != nil {
		return nil, fmt.Errorf("controller: acquire ownership: %w", err)
	}
	gen, err := store.AdvanceGeneration(ctx)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("controller: advance generation: %w", err)
	}
	return &Controller{
		store:      store,
		generation: gen,
		log:        logf,
		workerID:   fmt.Sprintf("controller-%d", gen),
	}, nil
}

// Generation reports the controller generation this run owns.
func (c *Controller) Generation() uint64 { return c.generation }

// Store grants transport roots read access to typed state services.
// Writers go through controller-owned methods, added by later tasks.
func (c *Controller) Store() *state.Store { return c.store }

// Run blocks until ctx is canceled, then releases ownership in order.
func (c *Controller) Run(ctx context.Context) error {
	c.log.Printf("controller: running at generation %d", c.generation)

	taskCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go c.runBackgroundWorker(taskCtx)

	<-ctx.Done()
	c.log.Printf("controller: shutting down")
	cancel() // stop worker heartbeats BEFORE closing the store
	return c.store.Close()
}

// runBackgroundWorker runs the scaffold's leased background task.
// Failures are logged, never fatal: a background purge failing must not
// take down the controller; the next TTL cycle retries.
func (c *Controller) runBackgroundWorker(ctx context.Context) {
	w := worker.New(c.store.DB(), c.generation, c.workerID, c.log)
	for {
		err := w.RunTask(ctx, "sessions/purge", func(ctx context.Context) error {
			// One purge per lease acquisition; the lease loop re-acquires.
			_, err := state.PurgeExpiredSessions(ctx, c.store.DB())
			return err
		})
		if err != nil && ctx.Err() == nil {
			c.log.Printf("background purge: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(state.LeaseTTL):
		}
	}
}
