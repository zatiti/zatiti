package controller

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Reconciliation (P23 item 4): the effects owner already lists an operation
// as outcome_unknown or awaiting_confirmation in the same bounded
// _effects.pending scan dispatch (tick.go) reads every tick, but dispatch
// deliberately never touches such an operation itself (doc.go) -- only this
// file may. Reconciliation is a separately admitted, separately authorized
// and accounted bounded READ distinct from the original write
// (_effects.reconciliation.prepare/.record, R10-008/P00-007): it reaches
// Adapter.Reconcile, never Adapter.Invoke, through the exact same one-use
// _effects.claim every ordinary dispatch attempt consumes, and it never
// overwrites the original action or its history.
//
// This package's own no-double-dispatch discipline (the same one
// outstandingTurnEffects already enforces for worker-turn effects) applies
// here too: an operation with an outstanding, not-yet-recorded
// reconciliation attempt (an open kindReconcile journal entry) is never
// re-admitted for a second one while the first is in flight -- reconcile.go
// calls Adapter.Reconcile exactly once per admitted read, and the
// journal-open check is what stops a later tick from piling a second
// physical read on top before the first even settles.

const obligationReconciliation = "reconciliation"

// reconcileBackingOff, delayReconcile and forgiveReconcile pace repeated
// reconciliation of one persistently uncertain operation -- mirroring
// tick.go's own backoff/delay/forgive for admission refusals, but mutex-
// guarded because a reconciliation read settles on its own worker
// goroutine (finishReconcile), not only from the synchronous tick
// goroutine driveReconciliation itself runs on. Without this, an operation
// whose reconciliation keeps coming back non-authoritative would be
// re-admitted for a fresh bounded read on every single tick forever --
// exactly the runaway loop this card's required test rules out.
func (c *Controller) reconcileBackingOff(id contract.ID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.reconcileBackoff[id]
	if !ok {
		return false
	}
	return c.status.Ticks < b.until
}

func (c *Controller) delayReconcile(id contract.ID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.reconcileBackoff[id]
	b.failures++
	wait := int64(1) << min(b.failures, 6)
	if wait > maxBackoffTicks {
		wait = maxBackoffTicks
	}
	b.until = c.status.Ticks + wait
	c.reconcileBackoff[id] = b
}

func (c *Controller) forgiveReconcile(id contract.ID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.reconcileBackoff, id)
}

// driveReconciliation discovers operations whose uncertainty this
// controller may reconcile and admits exactly one bounded reconciliation
// read for each one that is not already outstanding.
func (c *Controller) driveReconciliation(ctx, workCtx context.Context, sess *session, waiting map[contract.ID]wireJob) {
	if !c.admitting() {
		return
	}
	var pending operationsOutput
	if err := c.call(ctx, sess, "_effects.pending", limitInput{Limit: c.batch()}, &pending); err != nil {
		c.note(err)
		return
	}
	journaled := map[contract.ID]struct{}{}
	for _, e := range sess.journal.snapshot() {
		if e.Kind == kindReconcile && e.open() {
			journaled[e.OperationID] = struct{}{}
		}
	}
	for _, op := range pending.Operations {
		if !c.admitting() {
			return
		}
		if op.State != opStateOutcomeUnknown && op.State != opStateAwaitingConfirmation {
			continue
		}
		if _, ok := journaled[op.ID]; ok {
			// Already being reconciled by an outstanding admitted read;
			// never a second physical effect piled on top of the first.
			continue
		}
		if c.reconcileBackingOff(op.ID) {
			continue
		}
		if !c.acquire() {
			return
		}
		if !c.admitReconciliation(ctx, workCtx, sess, op, waiting) {
			c.free()
		}
	}
}

// admitReconciliation runs the prepare and claim transactions for one
// bounded reconciliation read and starts its worker. Both transactions have
// their own version/generation fence and no physical side effect of their
// own; only the adapter call that follows runs outside them.
func (c *Controller) admitReconciliation(ctx, workCtx context.Context, sess *session, op wireOperation, waiting map[contract.ID]wireJob) bool {
	e := entry{
		ID: string(contract.NewID()), Kind: kindReconcile, Phase: phaseAdmitting,
		Generation: sess.generation, OperationID: op.ID,
	}
	if !c.journal(sess, e) {
		return false
	}
	var prepared operationOutput
	err := c.write(func() error {
		return c.call(ctx, sess, "_effects.reconciliation.prepare", effectsAdmitInput{
			OperationID: op.ID, ExpectedVersion: op.Version,
		}, &prepared)
	})
	if err != nil {
		c.note(err)
		// The prepare call's own commit is unknowable from here, but the
		// operation's own row is untouched either way (admitReconciliationRead
		// only ever adds a new attempt to it) and stays visible in the next
		// _effects.pending scan; finishing this entry lets driveReconciliation
		// simply retry fresh rather than guess at an attempt id it never saw.
		e.Phase = phaseDone
		c.journal(sess, e)
		return false
	}
	result := prepared.Resource
	if len(result.AttemptIDs) == 0 {
		e.Phase = phaseDone
		c.journal(sess, e)
		return false
	}
	var action wireAction
	if len(result.Action) > 0 {
		if err := json.Unmarshal(result.Action, &action); err != nil {
			c.note(internalFault("reconciliation of operation %s carries an action the controller cannot decode", op.ID))
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
	var claimed dispatchOutput
	err = c.write(func() error {
		return c.call(ctx, sess, "_effects.claim", effectsClaimInput{
			OperationID: e.OperationID, AttemptID: e.AttemptID, Generation: sess.generation,
		}, &claimed)
	})
	if err != nil {
		c.note(err)
		e.Phase = phaseDone
		e.Fault = faultOf(err)
		c.journal(sess, e)
		return false
	}
	d := claimed.Resource
	adapter, ok := c.adapters[d.Adapter]
	if !ok {
		return c.unsentReconcile(ctx, sess, e, capabilityUnsupported("adapter %q is not registered with this controller", d.Adapter))
	}
	if !d.Deadline.IsZero() && !c.now().Before(d.Deadline) {
		return c.unsentReconcile(ctx, sess, e, conflictFault("reconciliation dispatch deadline of attempt %s passed before the adapter was invoked", e.AttemptID))
	}
	// Write-ahead: once this line is durable the adapter may have been
	// invoked, and recovery never resends it a second time.
	e.Phase = phaseClaimed
	if !c.journal(sess, e) {
		return false
	}
	c.hold(e.ID)
	c.workers.Add(1)
	go c.performReconcile(workCtx, sess, e, d, adapter)
	return true
}

// unsentReconcile settles a reconciliation attempt the adapter was never
// invoked for (an adapter this controller no longer has registered, or a
// claimed dispatch whose deadline already passed) -- mirroring tick.go's
// own unsent() for an ordinary dispatch attempt. The claim was already
// consumed, so this is still recorded through _effects.reconciliation.
// record rather than left as an orphaned, never-resolved claim.
func (c *Controller) unsentReconcile(ctx context.Context, sess *session, e entry, f *contract.Fault) bool {
	c.note(f)
	obs := c.synthesize(contract.DispositionNotSent, e.Bound, f.Message, "no")
	e.Observation = &obs
	e.Phase = phaseObserved
	if !c.journal(sess, e) {
		return false
	}
	c.settleReconcile(ctx, sess, e)
	return false
}

// performReconcile is the one physical Adapter.Reconcile call of one
// claimed reconciliation attempt. It runs outside every transaction,
// exactly once per claimed attempt.
func (c *Controller) performReconcile(workCtx context.Context, sess *session, e entry, d contract.Dispatch, adapter contract.Adapter) {
	defer c.workers.Done()
	defer c.free()
	defer c.release(e.ID)

	callCtx := workCtx
	if !d.Deadline.IsZero() {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(workCtx, d.Deadline.Sub(c.now()))
		defer cancel()
	}
	obs := c.observeReconcile(callCtx, adapter, d, e.Bound)
	e.Observation = &obs
	e.Phase = phaseObserved
	if !c.journal(sess, e) {
		return
	}
	c.settleReconcile(workCtx, sess, e)
}

// observeReconcile invokes Adapter.Reconcile exactly once and returns a
// schema-valid observation, using the same unestablished-on-panic/error
// fallback perform.go's observe already applies to an ordinary Invoke.
func (c *Controller) observeReconcile(ctx context.Context, adapter contract.Adapter, d contract.Dispatch, bound *wireMoney) (obs contract.Observation) {
	defer func() {
		if recover() != nil {
			obs = c.unestablished(bound, "adapter_panic", "the adapter panicked during reconciliation")
		}
	}()
	got, err := adapter.Reconcile(ctx, d)
	if err != nil {
		return c.unestablished(bound, "adapter_error", "the adapter returned an error instead of a reconciliation observation")
	}
	switch got.Disposition {
	case contract.DispositionSucceeded, contract.DispositionFailed, contract.DispositionAccepted,
		contract.DispositionUnknown, contract.DispositionNotSent:
	default:
		return c.unestablished(bound, "invalid_disposition", "the adapter returned an unsupported disposition")
	}
	if !jsonObject(got.Evidence) {
		got.Evidence = adapterReturn("missing_evidence", "the adapter returned no structured evidence")
	}
	if !validUsage(got.Usage) {
		got.Usage = c.synthesize(contract.DispositionUnknown, bound, "", "unknown").Usage
	}
	return got
}

// settleReconcile records the reconciliation observation through the
// dedicated owner call -- never the ordinary _effects.record, which the
// effects owner refuses for a reconciliation attempt -- and, only once that
// is durable, routes the merged result back to the original effect's owner
// exactly as an ordinary dispatch would (deliver.go). A non-authoritative
// disposition (unknown/not_sent) leaves the original operation's
// uncertainty untouched, matching the owner's own documented merge rule:
// nothing changed, so nothing is delivered, and a later tick's discovery
// may reconcile the same operation again.
func (c *Controller) settleReconcile(ctx context.Context, sess *session, e entry) {
	if e.Observation == nil {
		e.Phase = phaseRefused
		e.Fault = internalFault("journal entry for reconciliation of operation %s reached record without an observation", e.OperationID)
		c.journal(sess, e)
		return
	}
	err := c.write(func() error {
		return c.call(ctx, sess, "_effects.reconciliation.record", effectsRecordInput{
			OperationID: e.OperationID, AttemptID: e.AttemptID, Generation: e.Generation, Observation: *e.Observation,
		}, nil)
	})
	if err != nil {
		c.note(err)
		if transient(err) {
			return
		}
		f := faultOf(err)
		e.Phase = phaseRefused
		e.Fault = f
		if c.journal(sess, e) {
			c.oblige(obligationReconciliation, e.OperationID, f)
		}
		return
	}
	e.Phase = phaseRecorded
	if !c.journal(sess, e) {
		return
	}
	c.finishReconcile(ctx, sess, e)
}

// finishReconcile routes an authoritative reconciliation observation to the
// original effect's owner and marks the entry done. A non-authoritative one
// is simply finished: the original operation's own uncertainty was left
// untouched by the record call, so there is nothing new to deliver.
func (c *Controller) finishReconcile(ctx context.Context, sess *session, e entry) {
	switch e.Observation.Disposition {
	case contract.DispositionSucceeded, contract.DispositionFailed:
		c.forgiveReconcile(e.OperationID)
		c.deliver(ctx, sess, &e)
	default:
		// Still uncertain: back off before the next tick's discovery may
		// admit a fresh bounded read for the same operation again.
		c.delayReconcile(e.OperationID)
		e.Phase = phaseDone
		c.journal(sess, e)
	}
}

// settleReconcileEntry advances one kindReconcile journal entry no worker
// currently holds, from its durable phase. Called only from settle
// (recovery and lost-acknowledgement retry), never a fresh admission: it
// never calls Adapter.Reconcile a second time for an entry that already
// reached phaseClaimed -- a claim once made may have been invoked, so the
// only honest, non-duplicating resolution from here is an unknown
// reconciliation observation, recorded through the same dedicated owner
// call and, if that observation turns out authoritative on a later replay,
// still routed to the original owner exactly as a fresh one would be.
func (c *Controller) settleReconcileEntry(ctx context.Context, sess *session, e entry) {
	switch e.Phase {
	case phaseAdmitting:
		// The prepare call's own commit is unknowable; the operation's row
		// is untouched either way, so it simply resurfaces in the next
		// _effects.pending scan and driveReconciliation retries it fresh.
		e.Phase = phaseDone
		c.journal(sess, e)
		return
	case phaseAdmitted, phaseClaimed:
		obs := c.synthesize(contract.DispositionUnknown, e.Bound,
			"the controller stopped mid-reconciliation of operation "+string(e.OperationID), "unknown")
		e.Observation = &obs
		e.Phase = phaseObserved
		if !c.journal(sess, e) {
			return
		}
	}
	if e.Phase == phaseObserved {
		c.settleReconcile(ctx, sess, e)
		return
	}
	if e.Phase == phaseRecorded {
		c.finishReconcile(ctx, sess, e)
	}
}
