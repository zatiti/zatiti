package controller

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

const maxBackoffTicks = 64

// tick is one scheduler pass. Every phase is bounded by the configured batch
// and rechecks admission before each new admission, so lost ownership or a
// stop request takes effect between two units of work, not between two
// ticks. tick reports an error only when this controller was displaced.
func (c *Controller) tick(ctx, workCtx context.Context, sess *session) error {
	defer c.ticked()
	if !c.admitting() {
		return nil
	}
	if c.restoring.Load() {
		// An exclusive restore handoff owns admission: generation just
		// advanced under this controller's own hand, not a rival's, so the
		// ordinary supersession check below must not fire, and no ordinary
		// admission call (wakes, ready scan, execution tick, turn work,
		// jobs, dispatch) runs for the rest of this lifetime. Only settle
		// (the final _installation.restore.record call once resumed) and
		// restoreWork (resuming the swap/merge itself) progress.
		c.settle(ctx, sess, false)
		c.restoreWork(ctx, workCtx, sess)
		return nil
	}
	replaced, err := c.superseded(ctx, sess)
	if err != nil {
		c.note(err)
		return nil
	}
	if replaced {
		return unavailable("a newer controller generation owns this installation; generation %d stopped admitting", sess.generation)
	}

	c.resumeClaimedJobs(ctx, workCtx, sess)
	c.settle(ctx, sess, false)
	c.restoreWork(ctx, workCtx, sess)
	if c.restoring.Load() {
		// A restore was just admitted (or an already-open entry this
		// settle pass surfaced was just resumed): admission closes at
		// once, before any ordinary work is admitted this same tick, never
		// deferred to the next one.
		return nil
	}
	c.wakes(ctx, sess)
	c.readyScan(ctx, sess)
	c.executionTick(ctx, sess)
	c.turnWork(ctx, workCtx, sess)
	waiting := c.jobs(ctx, workCtx, sess)
	c.driveReconciliation(ctx, workCtx, sess, waiting)
	c.dispatch(ctx, workCtx, sess, waiting)

	if err := sess.journal.compactIfDue(); err != nil {
		c.note(err)
	}
	return nil
}

func (c *Controller) ticked() {
	c.mu.Lock()
	c.status.Ticks++
	n := c.status.Ticks
	hook := c.afterTick
	c.mu.Unlock()
	if hook != nil {
		hook(n)
	}
}

func (c *Controller) batch() int64 { return int64(c.cfg.MaxDispatch) }

// wakes admits every due durable wake. Reading a wake admits nothing; the
// scheduling owner rechecks conditions and deduplicates the occurrence in
// the admitting transaction, so replaying a wake after a crash is safe and a
// refused wake simply stays due.
func (c *Controller) wakes(ctx context.Context, sess *session) {
	var due wakesOutput
	if err := c.call(ctx, sess, "_scheduling.wake.due", nowLimitInput{Now: c.now(), Limit: c.batch()}, &due); err != nil {
		c.note(err)
		return
	}
	for _, wake := range due.Wakes {
		if !c.admitting() {
			return
		}
		var out wakeAdmitOutput
		err := c.write(func() error {
			return c.call(ctx, sess, "_scheduling.wake.admit", wakeAdmitInput{Wake: wake}, &out)
		})
		if err != nil {
			c.note(err)
		}
	}
}

// readyScan is the bounded recovery scan of ready tasks that have no run.
// The controller is not an allowed caller of the enqueue operation, so the
// scan can only make a stranded task visible; it never invents a run.
func (c *Controller) readyScan(ctx context.Context, sess *session) {
	var out itemsOutput
	if err := c.call(ctx, sess, "_tasks.ready", limitInput{Limit: c.batch()}, &out); err != nil {
		c.note(err)
		return
	}
	c.count(func(s *Status) { s.ReadyWithoutRun = len(out.Items) })
}

// executionTick lets the execution owner expire leases and admit bounded
// owned work.
func (c *Controller) executionTick(ctx context.Context, sess *session) {
	if !c.admitting() {
		return
	}
	err := c.write(func() error {
		return c.call(ctx, sess, "_execution.tick", nowLimitInput{Now: c.now(), Limit: c.batch()}, nil)
	})
	if err != nil {
		c.note(err)
	}
}

// dispatch admits and claims pending effect intents and hands each claimed
// dispatch to one worker. Operations awaiting confirmation or with an
// unknown outcome are listed by the owner but never touched here: only a
// separately admitted reconciliation may read them, and nothing resends.
func (c *Controller) dispatch(ctx, workCtx context.Context, sess *session, waiting map[contract.ID]wireJob) {
	c.resumeClaims(ctx, workCtx, sess)

	var pending operationsOutput
	if err := c.call(ctx, sess, "_effects.pending", limitInput{Limit: c.batch()}, &pending); err != nil {
		c.note(err)
		return
	}
	listed := make(map[contract.ID]struct{}, len(pending.Operations))
	for _, op := range pending.Operations {
		listed[op.ID] = struct{}{}
	}
	for id := range c.backoff {
		if _, ok := listed[id]; !ok {
			delete(c.backoff, id)
		}
	}
	journaled := map[contract.ID]struct{}{}
	for _, e := range sess.journal.snapshot() {
		if e.Kind == kindEffect && e.open() {
			journaled[e.OperationID] = struct{}{}
		}
	}
	for _, op := range pending.Operations {
		if !c.admitting() {
			return
		}
		if !admissible(op.State) {
			continue
		}
		if _, ok := journaled[op.ID]; ok {
			continue
		}
		if c.backingOff(op.ID) {
			continue
		}
		if !c.acquire() {
			return
		}
		if !c.admit(ctx, workCtx, sess, op, waiting) {
			c.free()
		}
	}
}

// admit runs the admit and claim transactions for one operation and starts
// its worker. It reports whether the worker now owns the slot.
func (c *Controller) admit(ctx, workCtx context.Context, sess *session, op wireOperation, waiting map[contract.ID]wireJob) bool {
	// A limited job scan is not proof that an explicit callback has no owner.
	// Leave the operation unadmitted until its durable linked job is visible.
	var callback wireCallbackRoute
	if len(op.CallbackRoute) > 0 && json.Unmarshal(op.CallbackRoute, &callback) == nil && callback.Kind == "connection" {
		job, found := waiting[op.ID]
		if !found || job.Owner != ownerConnections || (job.Operation != "connection.validate" && job.Operation != "connection.discover") {
			c.note(prerequisiteMissing("connection effect %s awaits its linked callback job", op.ID))
			return false
		}
	}

	e := entry{
		ID:          string(contract.NewID()),
		Kind:        kindEffect,
		Phase:       phaseAdmitting,
		Generation:  sess.generation,
		OperationID: op.ID,
	}
	if !c.journal(sess, e) {
		return false
	}
	var admitted operationOutput
	err := c.write(func() error {
		return c.call(ctx, sess, "_effects.admit", effectsAdmitInput{OperationID: op.ID, ExpectedVersion: op.Version}, &admitted)
	})
	if err != nil {
		c.note(err)
		if isFault(err) {
			// A fault rolled the admission back; the operation is as it was.
			e.Phase = phaseDone
			c.journal(sess, e)
			c.delay(op.ID)
		}
		// Anything else may have committed; the entry stays admitting and
		// the next settle pass resolves it against the owner's pending scan.
		return false
	}
	c.forgive(op.ID)
	result := admitted.Resource
	if result.State != opStateReady || len(result.AttemptIDs) == 0 {
		// Denied, expired or otherwise closed by the owner: no attempt exists.
		e.Phase = phaseDone
		c.journal(sess, e)
		return false
	}
	var action wireAction
	if len(result.Action) > 0 {
		if err := json.Unmarshal(result.Action, &action); err != nil {
			c.note(internalFault("admitted operation %s carries an action the controller cannot decode", op.ID))
		}
	}
	scope := action.Scope
	bound := action.CostBound
	e.Phase = phaseAdmitted
	e.AttemptID = result.AttemptIDs[len(result.AttemptIDs)-1]
	e.Scope = &scope
	e.Bound = &bound
	e.Route = c.routeFor(op.ID, result, action, waiting)
	if !c.journal(sess, e) {
		return false
	}
	return c.claim(ctx, workCtx, sess, e)
}

// resumeClaims retries claims whose transaction failed transiently. The
// owner's one-use claim decides: a claim that did commit conflicts on retry
// and settles as unsent-by-this-controller, which the owner keeps unknown.
func (c *Controller) resumeClaims(ctx, workCtx context.Context, sess *session) {
	for _, e := range sess.journal.snapshot() {
		if !claimable(e, sess) || c.held(e.ID) {
			continue
		}
		if !c.admitting() || !c.acquire() {
			return
		}
		if !c.claim(ctx, workCtx, sess, e) {
			c.free()
		}
	}
}

// claimable reports whether an admitted entry still awaits its first
// conclusive claim in this generation.
func claimable(e entry, sess *session) bool {
	return e.Kind == kindEffect && e.Phase == phaseAdmitted && e.Fault == nil && e.Generation == sess.generation
}

// claim consumes the attempt's one-use claim and starts the worker. Every
// conclusive failure before the adapter is invoked settles the attempt as
// not sent by this controller.
func (c *Controller) claim(ctx, workCtx context.Context, sess *session, e entry) bool {
	if !c.admitting() {
		// Admission closed between admit and claim. The unclaimed attempt
		// stays journaled and settles as not sent; nothing is dispatched.
		return false
	}
	var claimed dispatchOutput
	err := c.write(func() error {
		return c.call(ctx, sess, "_effects.claim", effectsClaimInput{
			OperationID: e.OperationID, AttemptID: e.AttemptID, Generation: sess.generation,
		}, &claimed)
	})
	if err != nil {
		c.note(err)
		if transient(err) {
			return false
		}
		c.unsent(ctx, sess, e, faultOf(err))
		return false
	}
	d := claimed.Resource
	if d.OperationID != e.OperationID || d.AttemptID != e.AttemptID || d.Generation != sess.generation {
		c.unsent(ctx, sess, e, internalFault("claimed dispatch does not match attempt %s of generation %d", e.AttemptID, sess.generation))
		return false
	}
	adapter, err := c.adapterForDispatch(d)
	if err != nil {
		c.unsent(ctx, sess, e, faultOf(err))
		return false
	}
	if !d.Deadline.IsZero() && !c.now().Before(d.Deadline) {
		c.unsent(ctx, sess, e, conflictFault("dispatch deadline of attempt %s passed before the adapter was invoked", e.AttemptID))
		return false
	}
	// Write-ahead: once this line is durable the adapter may have been
	// invoked, and recovery will never treat the attempt as unsent.
	e.Phase = phaseClaimed
	if !c.journal(sess, e) {
		return false
	}
	c.hold(e.ID)
	c.workers.Add(1)
	go c.perform(workCtx, sess, e, d, adapter)
	return true
}

// unsent settles an attempt the adapter was never invoked for.
func (c *Controller) unsent(ctx context.Context, sess *session, e entry, f *contract.Fault) {
	e.Fault = f
	if !c.journal(sess, e) {
		return
	}
	c.settleEffect(ctx, sess, e, false)
}

func (c *Controller) acquire() bool {
	select {
	case c.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (c *Controller) free() { <-c.slots }

func (c *Controller) backingOff(id contract.ID) bool {
	b, ok := c.backoff[id]
	if !ok {
		return false
	}
	c.mu.Lock()
	now := c.status.Ticks
	c.mu.Unlock()
	return now < b.until
}

// delay backs an operation off after a refused admission, so an operation
// awaiting review or budget is not re-admitted on every tick.
func (c *Controller) delay(id contract.ID) {
	b := c.backoff[id]
	b.failures++
	wait := int64(1) << min(b.failures, 6)
	if wait > maxBackoffTicks {
		wait = maxBackoffTicks
	}
	c.mu.Lock()
	b.until = c.status.Ticks + wait
	c.mu.Unlock()
	c.backoff[id] = b
}

func (c *Controller) forgive(id contract.ID) { delete(c.backoff, id) }
