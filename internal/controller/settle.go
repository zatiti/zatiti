package controller

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

const recoveryEvidenceSchema = "zatiti.controller.recovery/v1"

// Obligation kinds.
const (
	obligationAdmission   = "admission"
	obligationRecord      = "record"
	obligationPublication = "publication"
	obligationDelivery    = "delivery"
	obligationJob         = "job"
)

// settle advances every journaled entry no worker currently holds. At start
// (recovering) every open entry belongs to a dead process; during a tick the
// pass retries steps whose write was lost. settle never invokes an adapter
// or a job runner: it only records what is already known, which for a claim
// without an observation is that the outcome is unknown.
func (c *Controller) settle(ctx context.Context, sess *session, recovering bool) {
	var pending map[contract.ID]wireOperation
	var pendingJobs map[contract.ID]wireJob
	for _, listed := range sess.journal.snapshot() {
		if c.held(listed.ID) {
			continue
		}
		// Re-read after the held check: a worker journals its final phase
		// before it lets go, so the fresh entry is never behind the worker.
		e, ok := sess.journal.get(listed.ID)
		if !ok || !e.open() {
			continue
		}
		if ctx.Err() != nil || c.abandoned.Load() {
			return
		}
		if !recovering && claimable(e, sess) {
			// Still awaiting its first conclusive claim; dispatch retries it.
			continue
		}
		switch {
		case e.Kind == kindEffect && e.Phase == phaseAdmitting:
			if pending == nil {
				pending = c.pendingOperations(ctx, sess)
			}
			c.settleAdmission(sess, e, pending)
		case e.Kind == kindEffect:
			c.settleEffect(ctx, sess, e, recovering)
		case e.Kind == kindJob && e.Phase == phaseAdmitted:
			if pendingJobs == nil {
				pendingJobs = c.pendingJobIndex(ctx, sess)
			}
			c.settleJobClaim(ctx, sess, e, pendingJobs)
		case e.Kind == kindJob && e.Phase == phaseClaimed && c.jobResumable(e):
			// Never touched here: settle must never invoke a runner.
			// resumeClaimedJobs (tick.go, ordinary flow) re-invokes it.
		case e.Kind == kindJob:
			c.settleJob(ctx, sess, e)
		case e.Kind == kindReconcile:
			c.settleReconcileEntry(ctx, sess, e)
		}
	}
}

// pendingOperations indexes the bounded pending scan by operation. A failed
// scan reads as nil, which settles nothing this pass.
func (c *Controller) pendingOperations(ctx context.Context, sess *session) map[contract.ID]wireOperation {
	var out operationsOutput
	if err := c.call(ctx, sess, "_effects.pending", limitInput{Limit: MaxBatch}, &out); err != nil {
		c.note(err)
		return nil
	}
	index := make(map[contract.ID]wireOperation, len(out.Operations))
	for _, op := range out.Operations {
		index[op.ID] = op
	}
	return index
}

// settleAdmission resolves an admit whose commit was never acknowledged. If
// the operation is still awaiting admission the admit never committed and
// the ordinary loop admits it again. Otherwise the admit may have created an
// attempt the controller cannot name: no allowed call lists a ready
// operation's attempt or its generation, so the entry is retained as a
// stranded admission instead of being guessed at.
func (c *Controller) settleAdmission(sess *session, e entry, pending map[contract.ID]wireOperation) {
	if pending == nil {
		return
	}
	if op, ok := pending[e.OperationID]; ok && admissible(op.State) {
		e.Phase = phaseDone
		c.journal(sess, e)
		return
	}
	f := prerequisiteMissing(
		"admission of operation %s was not acknowledged and the effects owner exposes no call that lists a ready operation's attempt; the attempt cannot be recorded as unsent",
		e.OperationID)
	e.Phase = phaseStranded
	e.Fault = f
	if c.journal(sess, e) {
		c.oblige(obligationAdmission, e.OperationID, f)
	}
}

func admissible(state string) bool {
	return state == opStatePrepared || state == opStateAwaitingReview
}

// settleEffect advances one effect entry from its durable phase.
func (c *Controller) settleEffect(ctx context.Context, sess *session, e entry, recovering bool) {
	switch e.Phase {
	case phaseAdmitted:
		// The adapter was never invoked: the claimed marker is written
		// before every invocation and this entry never reached it.
		reason := "the controller stopped before dispatching this attempt; the adapter was never invoked"
		if e.Fault != nil {
			reason = "the attempt was not dispatched (" + e.Fault.Code + "); the adapter was never invoked"
		}
		obs := c.synthesize(contract.DispositionNotSent, e.Bound, reason, "no")
		e.Observation = &obs
		e.Phase = phaseObserved
		if !c.journal(sess, e) {
			return
		}
	case phaseClaimed:
		// Claimed and possibly transmitted; nothing observed. Only evidence
		// can resolve it, and the controller has none.
		obs := c.synthesize(contract.DispositionUnknown, e.Bound,
			"the controller stopped after claiming this attempt and before an observation was durable", "unknown")
		e.Observation = &obs
		e.Phase = phaseObserved
		if !c.journal(sess, e) {
			return
		}
		if recovering {
			c.count(func(s *Status) { s.Ambiguous++ })
		}
	}
	if e.Phase == phaseObserved {
		if !c.record(ctx, sess, &e) {
			return
		}
	}
	if e.Phase == phaseRecorded {
		c.deliver(ctx, sess, &e)
	}
}

// record stores the journaled observation through the effects owner. The
// owner replays an identical observation, so repeating a record whose
// acknowledgement was lost is safe; repeating the provider call never is,
// and nothing here can reach an adapter.
func (c *Controller) record(ctx context.Context, sess *session, e *entry) bool {
	if e.Observation == nil {
		e.Phase = phaseRefused
		e.Fault = internalFault("journal entry for attempt %s reached record without an observation", e.AttemptID)
		c.journal(sess, *e)
		return false
	}
	err := c.write(func() error {
		return c.call(ctx, sess, "_effects.record", effectsRecordInput{
			OperationID: e.OperationID,
			AttemptID:   e.AttemptID,
			Generation:  e.Generation,
			Observation: *e.Observation,
		}, nil)
	})
	if err != nil {
		c.note(err)
		if transient(err) {
			return false
		}
		// A durable refusal: the observation stays in the journal as
		// evidence. It is never grounds for another provider call.
		f := faultOf(err)
		e.Phase = phaseRefused
		e.Fault = f
		if c.journal(sess, *e) {
			c.oblige(obligationRecord, e.AttemptID, f)
		}
		return false
	}
	e.Phase = phaseRecorded
	e.Fault = nil
	if !c.journal(sess, *e) {
		return false
	}
	c.count(func(s *Status) { s.Recorded++ })
	return true
}

// journal appends one entry state as a guarded write and reports whether it
// became durable.
func (c *Controller) journal(sess *session, e entry) bool {
	e.UpdatedAt = c.now()
	if err := c.write(func() error { return sess.journal.put(e) }); err != nil {
		c.note(err)
		return false
	}
	return true
}

// synthesize builds the observation the controller itself can vouch for. It
// never claims provider behavior: it states whether the adapter was invoked
// and leaves every charge it cannot rule out as unknown.
func (c *Controller) synthesize(disposition string, bound *wireMoney, reason, invoked string) contract.Observation {
	evidence, _ := json.Marshal(struct {
		Schema         string `json:"schema"`
		Reason         string `json:"reason"`
		AdapterInvoked string `json:"adapter_invoked"`
	}{recoveryEvidenceSchema, reason, invoked})
	usage := wireUsage{Currency: "XXX"}
	if bound != nil && validCurrency(bound.Currency) {
		usage.Currency = bound.Currency
		if invoked != "no" {
			usage.Unknown = bound.MicroUnits
			usage.Advisory = true
		}
	} else if invoked != "no" {
		usage.Advisory = true
	}
	raw, _ := json.Marshal(usage)
	return contract.Observation{Disposition: disposition, Evidence: evidence, Usage: raw}
}

func validCurrency(code string) bool {
	if len(code) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		if code[i] < 'A' || code[i] > 'Z' {
			return false
		}
	}
	return true
}

// held reports whether a worker currently owns the entry.
func (c *Controller) held(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.busy[id]
	return ok
}

func (c *Controller) hold(id string) {
	c.mu.Lock()
	c.busy[id] = struct{}{}
	c.status.InFlight++
	c.mu.Unlock()
}

func (c *Controller) release(id string) {
	c.mu.Lock()
	delete(c.busy, id)
	c.status.InFlight--
	c.mu.Unlock()
}
