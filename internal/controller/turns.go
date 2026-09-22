package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Turn-driving phase (P22, "the controller drives turns, contexts and
// model/tool work"): discovers worker-recipient messages, admits durable
// WorkerTurns, fairly claims turn/context/proposal work, stages and
// publishes context, and drives whatever P16's interpretation stage left
// "prepared" for an outside-unit caller -- a local_operation through the
// real contract.WorkerOperator, an external_tool through the existing
// generic effect pipeline (tick.go/deliver.go, extended below), and
// independent verification through the real contract.Verifier.
//
// One of the two out-of-authority contract gaps this phase originally
// found is now closed; the other remains and is reported as a controller
// obligation (never silently dropped, never worked around by inventing a
// seam this package has no authority to add):
//
//  1. CLOSED (execution-dispatch-model-step, the same-day P22 gap fix):
//     _effects.prepare's caller allowlist (internal/effects/service.go,
//     opMetas[opPrepare].callers) never included "controller", so nothing
//     this package called could itself dispatch a model_step (or
//     prepare_session) effect for a committed, model_pending turn. Closed
//     the way this file's own original comment named as more consistent
//     with prepareModelEffect/interpretExternalTool: execution's own
//     _execution.context.commit handler now chains internally to
//     _effects.prepare the moment it commits a turn to model_pending, no
//     allowlist change needed. A model_pending turn therefore now always
//     has an outstanding dispatched effect (visible via
//     outstandingTurnEffects) unless its plan carries no resolved
//     responses-adapter tool component (a worker with no hosted model
//     connection configured) -- stalledModelDispatch below only reports
//     that narrower remaining case, never a turn whose dispatch is
//     legitimately in flight.
//  2. A message/responsibility-triggered turn carries no attempt_id (only
//     a task-triggered turn gets one, via _execution.enqueue's automatic
//     turn admission); _execution.observation's frozen input schema
//     requires attempt_id. Now that gap 1 is closed, this is live: a bare
//     chat turn still has no schema-valid path to receive its own model
//     response, so execution's own dispatch deliberately never fires for
//     it (gated on AttemptID != "") rather than dispatching into a dead
//     end -- already flagged as an out-of-scope contract gap at P16's
//     landing (docs/roadmap.md, 2026-09-21).

// Controller obligation kinds this phase reports.
const (
	// obligationModelDispatch names a model_pending turn whose plan names no
	// resolved responses-adapter tool component, so nothing dispatched its
	// model_step effect (see stalledModelDispatch).
	obligationModelDispatch = "model_dispatch"
	// obligationProposal names a prepared local_operation proposal the
	// controller could not drive to completion.
	obligationProposal = "proposal"
	// obligationVerification names a claimed VerificationRequest the
	// controller could not execute.
	obligationVerification = "verification"
)

// contextDocumentSchema names the minimal, honest document the controller
// itself assembles and publishes as a turn's committed context artifact.
// _execution.context.commit's own handler (turn_ops.go, handleContextCommit)
// never reads or validates the staged bytes' content -- it only checks that
// the plan's pinned refs and configuration revision are still current and
// that the caller supplied a published (not merely staged) artifact -- so
// this is a legitimate, schema-honest choice given what the wire ContextPlan
// actually exposes (refs, byte/token bounds, configuration revision), not a
// reproduction of execution's own private zatiti.context/v1 transcript
// (context_build.go's contextRecipe/contextComponent, and the stageContext/
// buildResponsesModelStepAction functions that fold it into one, are
// unexported, keyed to execution's own unexported row types, and never
// wired to any operation or exported constructor a sibling package could
// call -- see the report for why reproducing that exact private format here
// would itself be inventing a seam, not implementing one).
const contextDocumentSchema = "zatiti.controller.context-plan/v1"

type contextDocument struct {
	Schema                string         `json:"schema"`
	TurnID                contract.ID    `json:"turn_id"`
	ConfigurationRevision int64          `json:"configuration_revision"`
	Refs                  []wireArtifact `json:"refs"`
}

// turnRouteInfo is what the controller remembers, between discovering a
// model_pending turn's own attempt and later delivering that model step's
// observation, to interpret the returned evidence and drive whatever it
// leaves "prepared". Rebuilt every tick from the owner's own live state
// (driveWorkItems) before it is ever consulted, so a fresh process never
// consults a stale entry.
type turnRouteInfo struct {
	TurnID    contract.ID
	StepIndex int64
	Version   int64
	WorkerID  contract.ID
	Scope     contract.Scope
}

// turnWork is the bounded, fair turn-driving tick phase. Every sub-phase
// processes its whole bounded batch, continuing past one item's refusal or
// failure, so a blocked item never starves a later, eligible one in the
// same batch.
func (c *Controller) turnWork(ctx, workCtx context.Context, sess *session) {
	if !c.admitting() {
		return
	}
	c.discoverMessages(ctx, sess)
	c.resumeContextStages(ctx, workCtx, sess)
	c.driveWorkItems(ctx, workCtx, sess)
	c.driveVerification(ctx, workCtx, sess)
}

// discoverMessages bounded-scans messages admitted and awaiting a durable
// worker turn (_messaging.ready) and admits -- or safely injects into an
// already-active turn for -- each recipient. This is a fallback discovery
// mechanism alongside _execution.work.pending's own "claim" scan, never the
// sole stranded-work response: a message's turn is also reachable the
// ordinary way once admitted, exactly like every other pending turn.
func (c *Controller) discoverMessages(ctx context.Context, sess *session) {
	var out messagesOutput
	if err := c.call(ctx, sess, "_messaging.ready", limitInput{Limit: c.batch()}, &out); err != nil {
		c.note(err)
		return
	}
	for _, m := range out.Items {
		for _, recipient := range m.RecipientIDs {
			if !c.admitting() {
				return
			}
			scope := m.Scope
			scope.WorkerID = recipient
			var admitted turnOutput
			err := c.write(func() error {
				return c.call(ctx, sess, "_execution.turn.admit", turnAdmitInput{
					Source: wireTurnSource{
						Kind: "message", SourceID: m.ID, SourceVersion: m.Version, RecipientWorkerID: recipient,
					},
					WorkerID: recipient, Scope: scope, RequesterID: m.SenderID,
				}, &admitted)
			})
			if err != nil {
				c.note(err)
			}
		}
	}
}

// driveWorkItems bounded-scans typed claim/context/proposal/resume work
// (_execution.work.pending) and advances each item as far as an allowed
// call and the confirmed contract gaps above permit.
func (c *Controller) driveWorkItems(ctx, workCtx context.Context, sess *session) {
	var out workItemsOutput
	if err := c.call(ctx, sess, "_execution.work.pending", limitInput{Limit: c.batch()}, &out); err != nil {
		c.note(err)
		return
	}
	// Refresh the turn-routing indexes from this tick's live scan before
	// dispatch() (called later this same tick, from tick.go) tries to route
	// any worker_turn-callback-routed effect delivery.
	c.turnsMu.Lock()
	for _, item := range out.Items {
		if item.Turn.AttemptID == "" {
			continue
		}
		c.turnAttempts[item.Turn.ID] = item.Turn.AttemptID
		c.attemptTurns[item.Turn.AttemptID] = turnRouteInfo{
			TurnID: item.Turn.ID, StepIndex: item.Turn.StepsUsed, Version: item.Turn.Version,
			WorkerID: item.Turn.WorkerID, Scope: item.Turn.Scope,
		}
	}
	c.turnsMu.Unlock()

	// A turn fenced out of model_pending (generation advanced while its
	// model_step was in flight, controller_ops.go's real handleFence) comes
	// back through work.claim's "waiting" branch as an ordinary "claimed"
	// turn -- context_pending, model_pending and proposal_pending are all
	// fenced the same way. Nothing in the frozen contract's own state
	// machine stops a resumed "claimed" turn from rebuilding a second
	// context and dispatching a second model_step while the first one's
	// outcome is still unresolved (outcome_unknown, awaiting a
	// reconciliation P23 has not landed yet). This package's own required
	// no-double-dispatch guarantee (P22.md) is stricter than that, so a
	// turn with an outstanding worker_turn-routed effect is never rebuilt
	// here -- it waits for that effect to resolve (or be reconciled, once
	// P23 exists) instead of piling a second physical call on top.
	outstanding := c.outstandingTurnEffects(ctx, sess)

	for _, item := range out.Items {
		if !c.admitting() {
			return
		}
		switch item.Kind {
		case workKindClaim, workKindResume:
			c.claimTurnWork(ctx, sess, item)
		case workKindContext:
			if outstanding[item.Turn.ID] {
				c.oblige(obligationModelDispatch, item.Turn.ID, prerequisiteMissing(
					"turn %s has an unresolved model_step effect outstanding; refusing to dispatch a second one until it resolves or is reconciled",
					item.Turn.ID))
				continue
			}
			c.advanceContext(ctx, workCtx, sess, item)
		case workKindProposal:
			if outstanding[item.Turn.ID] {
				// execution's own context.commit already dispatched this
				// turn's prepare_session/model_step effect (gap 1, now
				// closed); it is simply awaiting that effect's observation,
				// not stalled. Withdraw any obligation a prior tick raised
				// before that dispatch existed.
				c.resolve(obligationModelDispatch, item.Turn.ID)
				continue
			}
			c.stalledModelDispatch(sess, item.Turn)
		}
	}
}

// outstandingTurnEffects reads the bounded pending-effects scan and returns
// the set of turn ids naming a worker_turn callback route among them -- an
// admitted, claimed-or-awaiting-confirmation, or outcome_unknown physical
// call this package must never pile a duplicate on top of.
func (c *Controller) outstandingTurnEffects(ctx context.Context, sess *session) map[contract.ID]bool {
	var pending operationsOutput
	if err := c.call(ctx, sess, "_effects.pending", limitInput{Limit: c.batch()}, &pending); err != nil {
		c.note(err)
		return nil
	}
	out := make(map[contract.ID]bool, len(pending.Operations))
	for _, op := range pending.Operations {
		if len(op.CallbackRoute) == 0 {
			continue
		}
		var cb wireCallbackRoute
		if json.Unmarshal(op.CallbackRoute, &cb) == nil && cb.Kind == "worker_turn" && cb.TurnID != "" {
			out[cb.TurnID] = true
		}
	}
	return out
}

// claimTurnWork claims one pending or due-to-resume turn. _execution.work.
// claim is a pure database transaction with its own version/generation
// fence and no physical side effect, and a retry against the same key
// inspects and returns the same claim rather than creating a second one
// (turn_ops.go, handleWorkClaim) -- so a crash before this call ever
// commits leaves nothing to recover: the turn is simply reclaimed, once,
// the next time work.pending lists it.
func (c *Controller) claimTurnWork(ctx context.Context, sess *session, item wireWorkItem) {
	var claimed workClaimOutput
	err := c.write(func() error {
		return c.call(ctx, sess, "_execution.work.claim", workClaimInput{
			WorkID: item.Turn.ID, ExpectedVersion: item.Turn.Version, Generation: sess.generation,
		}, &claimed)
	})
	if err != nil {
		c.note(err)
		return
	}
	if claimed.Item.Turn.AttemptID != "" {
		c.turnsMu.Lock()
		c.turnAttempts[claimed.Item.Turn.ID] = claimed.Item.Turn.AttemptID
		c.turnsMu.Unlock()
	}
}

// advanceContext builds a fresh context plan for a freshly-claimed turn and
// stages/commits it. _execution.context.prepare transitions the turn to
// context_pending (turn_ops.go/context_build.go), a state _execution.work.
// pending's own scan does not list -- so unlike every other phase here, a
// turn stuck mid-way through this one is never rediscovered by re-scanning
// pending work; only this package's own journal remembers it, which is
// exactly what resumeContextStages (called every tick, before this scan) is
// for.
func (c *Controller) advanceContext(ctx, workCtx context.Context, sess *session, item wireWorkItem) {
	var prepared contextPlanOutput
	err := c.write(func() error {
		return c.call(ctx, sess, "_execution.context.prepare", contextPrepareInput{
			TurnID: item.Turn.ID, ExpectedVersion: item.Turn.Version, Generation: sess.generation,
		}, &prepared)
	})
	if err != nil {
		c.note(err)
		return
	}
	p := prepared.Resource
	scope := item.Turn.Scope
	e := entry{
		ID: string(contract.NewID()), Kind: kindTurn, Phase: phaseContextStaging,
		Generation: sess.generation, TurnID: item.Turn.ID, Plan: &p, Scope: &scope,
	}
	if !c.journal(sess, e) {
		return
	}
	c.stageAndCommit(ctx, workCtx, sess, e)
}

// resumeContextStages resumes every open kindTurn context entry this
// generation from its last durable phase, before this tick's ordinary work-
// item scan runs. A staged-but-uncommitted context (the process died after
// publishing the bytes and before the commit call was durable) resumes at
// the commit, reusing the already-published artifact rather than staging a
// second one for the same plan.
func (c *Controller) resumeContextStages(ctx, workCtx context.Context, sess *session) {
	for _, e := range sess.journal.snapshot() {
		if e.Kind != kindTurn || !e.open() || e.Generation != sess.generation {
			continue
		}
		if !c.admitting() {
			return
		}
		switch e.Phase {
		case phaseContextStaging:
			c.stageAndCommit(ctx, workCtx, sess, e)
		case phaseContextStaged:
			c.commitStagedContext(ctx, sess, e)
		}
	}
}

// stageAndCommit performs the one physical action in the turn-work phase --
// building, staging and publishing the context artifact's bytes outside any
// transaction -- durably journaling the resulting artifact before ever
// attempting the commit call, then commits.
func (c *Controller) stageAndCommit(ctx, workCtx context.Context, sess *session, e entry) {
	ref, ok := c.stageContextArtifact(workCtx, sess, e)
	if !ok {
		return
	}
	e.Phase = phaseContextStaged
	e.StagedArtifact = &ref
	if !c.journal(sess, e) {
		return
	}
	c.commitStagedContext(ctx, sess, e)
}

// stageContextArtifact builds, stages and publishes the context document
// outside any transaction, journaling nothing itself -- the caller journals
// the result once staging is durable.
func (c *Controller) stageContextArtifact(ctx context.Context, sess *session, e entry) (wireArtifact, bool) {
	c.mu.Lock()
	blobs := c.deps.Blobs
	c.mu.Unlock()
	if blobs == nil {
		f := prerequisiteMissing(
			"no blob store is attached; turn %s's context cannot be staged and published", e.TurnID)
		c.note(f)
		c.oblige(obligationPublication, e.TurnID, f)
		return wireArtifact{}, false
	}
	if e.Plan == nil {
		c.note(internalFault("journal entry for turn %s reached context staging without a plan", e.TurnID))
		return wireArtifact{}, false
	}
	doc, err := json.Marshal(contextDocument{
		Schema: contextDocumentSchema, TurnID: e.TurnID,
		ConfigurationRevision: e.Plan.ConfigurationRevision, Refs: nonNilArtifacts(e.Plan.Refs),
	})
	if err != nil {
		c.note(internalFault("turn %s's context document could not be encoded", e.TurnID))
		return wireArtifact{}, false
	}
	if int64(len(doc)) > e.Plan.ByteBound && e.Plan.ByteBound > 0 {
		f := capabilityUnsupported("turn %s's context document is %d bytes, exceeding the plan's %d byte bound",
			e.TurnID, len(doc), e.Plan.ByteBound)
		c.oblige(obligationPublication, e.TurnID, f)
		return wireArtifact{}, false
	}
	stagingRef, digest, size, err := blobs.Stage(ctx, bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		f := unavailable("turn %s's context could not be staged: %v", e.TurnID, err)
		c.note(f)
		c.oblige(obligationPublication, e.TurnID, f)
		return wireArtifact{}, false
	}
	_ = size
	if err := blobs.Publish(ctx, stagingRef, digest); err != nil {
		f := unavailable("turn %s's context could not be published: %v", e.TurnID, err)
		c.note(f)
		c.oblige(obligationPublication, e.TurnID, f)
		return wireArtifact{}, false
	}
	scope := sess.scope
	if e.Scope != nil {
		scope = *e.Scope
	}
	var published artifactOutput
	err = c.write(func() error {
		return c.call(ctx, sess, "_artifacts.publish", artifactsPublishInput{
			Scope: scope, Digest: digest, Size: int64(len(doc)), MediaType: "application/json",
			Classification: "internal", Encrypted: true,
		}, &published)
	})
	if err != nil {
		c.note(err)
		c.oblige(obligationPublication, e.TurnID, faultOf(err))
		return wireArtifact{}, false
	}
	c.resolve(obligationPublication, e.TurnID)
	return wireArtifact{ID: published.Resource.ID, Digest: published.Resource.Digest}, true
}

// commitStagedContext commits an already-published context artifact.
func (c *Controller) commitStagedContext(ctx context.Context, sess *session, e entry) {
	if e.Plan == nil || e.StagedArtifact == nil {
		c.note(internalFault("journal entry for turn %s reached context commit without a staged plan", e.TurnID))
		return
	}
	ref := *e.StagedArtifact
	err := c.write(func() error {
		return c.call(ctx, sess, "_execution.context.commit", contextCommitInput{
			PlanID: e.Plan.ID, ExpectedVersion: e.Plan.ExpectedVersion, Generation: e.Plan.Generation,
			StagedContext: wireArtifactLocator{Kind: "artifact", Artifact: &ref},
		}, nil)
	})
	if err != nil {
		c.note(err)
		if transient(err) {
			// The call may not have reached the owner at all (this
			// package's own crash, a lost acknowledgement): the entry
			// stays exactly as it was, staged artifact and all, so the
			// next tick's resumeContextStages retries this same commit
			// rather than re-staging a second artifact for the same plan.
			return
		}
		// A durable refusal (turn_ops.go, handleContextCommit: a stale
		// plan is discarded by the owner without spending or sending).
		// _execution.work.pending's own scan does not re-list a
		// context_pending turn, so this package has no rediscovery path
		// back to it without a query this card's frozen contract does not
		// expose (no "get turn by id") -- reported as an obligation rather
		// than guessed at, matching this card's "stalled turns" reporting
		// requirement.
		f := faultOf(err)
		e.Phase = phaseDone
		c.journal(sess, e)
		c.oblige(obligationPublication, e.TurnID, f)
		return
	}
	c.resolve(obligationPublication, e.TurnID)
	e.Phase = phaseDone
	c.journal(sess, e)
}

// stalledModelDispatch reports a model_pending turn the caller (turnWork,
// above) already confirmed has no outstanding dispatched effect: since gap
// 1 closed (see this file's header), execution's own context.commit
// dispatches prepare_session/model_step for every task-bound turn whose
// plan names a resolved responses-adapter tool, so reaching here means the
// plan names none -- a worker with no hosted model connection configured,
// which can never have a model_step dispatched for it until that binding
// exists.
func (c *Controller) stalledModelDispatch(sess *session, turn wireWorkerTurn) {
	f := prerequisiteMissing(
		"turn %s committed context and is model_pending with no outstanding dispatched effect: its plan names no "+
			"resolved responses-adapter tool component, so execution's own context.commit dispatch "+
			"(dispatchModelEffect) had nothing to dispatch -- bind a hosted model connection for this worker", turn.ID)
	c.note(f)
	c.oblige(obligationModelDispatch, turn.ID, f)
}

// observeTurnDelivery runs after a turn-linked model_step's observation is
// durably recorded (deliver.go, the ownerExecution case): it reads the same
// raw ModelOutput evidence the controller already holds -- never trusted
// for authorization, only to learn which proposal ids execution's own
// interpretation stage decided on -- and, for each one left "prepared" (a
// local_operation or external_tool the caller must finish outside any
// transaction), drives it.
func (c *Controller) observeTurnDelivery(ctx context.Context, sess *session, e *entry, normalized contract.Observation) {
	c.turnsMu.Lock()
	info, ok := c.attemptTurns[e.Route.AttemptID]
	c.turnsMu.Unlock()
	if !ok {
		return
	}
	var probe struct {
		ToolProposals []struct {
			ID string `json:"id"`
		} `json:"tool_proposals"`
	}
	if len(normalized.Evidence) == 0 || json.Unmarshal(normalized.Evidence, &probe) != nil {
		return
	}
	for _, tp := range probe.ToolProposals {
		if tp.ID == "" {
			continue
		}
		c.driveOneProposal(ctx, sess, info, tp.ID)
	}
}

// driveOneProposal fetches the authoritative, already-decided ProposalRecord
// for one proposal id (_execution.proposal.prepare is an idempotent lookup
// once execution has interpreted it) and, if it was left "prepared" for an
// outside-unit caller, drives it.
func (c *Controller) driveOneProposal(ctx context.Context, sess *session, info turnRouteInfo, proposalID string) {
	var prep proposalOutput
	err := c.write(func() error {
		return c.call(ctx, sess, "_execution.proposal.prepare", proposalPrepareInput{
			TurnID: info.TurnID, StepIndex: info.StepIndex, ProposalID: proposalID, ExpectedVersion: info.Version,
		}, &prep)
	})
	if err != nil {
		c.note(err)
		return
	}
	p := prep.Resource
	if p.State != "prepared" {
		// Recorded inline (reply/clarify/report_outputs/cycle_decision/
		// refused) by execution's own interpretation stage: nothing further
		// for this package to drive.
		return
	}
	var probe normalizedProposalProbe
	if json.Unmarshal(p.NormalizedProposal, &probe) != nil {
		return
	}
	switch probe.Kind {
	case proposalKindLocalOperation:
		c.driveLocalOperation(ctx, sess, info, p, probe)
	case proposalKindExternalTool:
		if p.EffectOperationID != "" {
			c.turnsMu.Lock()
			c.turnProposals[p.EffectOperationID] = turnProposalRef{
				TurnID: info.TurnID, StepIndex: info.StepIndex, ProposalID: proposalID, ExpectedVersion: info.Version,
			}
			c.turnsMu.Unlock()
		}
	}
}

// driveLocalOperation invokes a worker-authored local_operation proposal
// through the real contract.WorkerOperator, under the worker's own
// authenticated actor -- never a controller-privileged shortcut -- and
// reports the outcome back through _execution.proposal.record. The
// submission key is deterministic in the proposal id, so a retried call
// (this package restarting mid-way, or a duplicate delivery) replays the
// same authorized command instead of invoking the operation twice.
func (c *Controller) driveLocalOperation(ctx context.Context, sess *session, info turnRouteInfo, p wireProposalRecord, probe normalizedProposalProbe) {
	c.mu.Lock()
	operator := c.deps.Operator
	c.mu.Unlock()
	if operator == nil {
		f := prerequisiteMissing(
			"no worker operator is attached; turn %s's local_operation proposal %s cannot be driven", info.TurnID, p.ProposalID)
		c.oblige(obligationProposal, info.TurnID, f)
		return
	}
	result, err := operator.ExecuteWorker(ctx, contract.WorkerRequest{
		TurnID: info.TurnID, ProposalID: p.ProposalID, WorkerID: info.WorkerID, Scope: info.Scope,
		Operation: probe.Operation, Version: contract.Version(probe.OperationVersion), Input: probe.Input,
		SubmissionKey: fmt.Sprintf("worker-turn/%s/%s", info.TurnID, p.ProposalID),
	})
	if err != nil {
		f := faultOf(err)
		c.note(f)
		c.oblige(obligationProposal, info.TurnID, f)
		return
	}
	err = c.write(func() error {
		return c.call(ctx, sess, "_execution.proposal.record", proposalRecordInput{
			ProposalID: p.ProposalID, ExpectedVersion: info.Version, CommandID: result.CommandID,
		}, nil)
	})
	if err != nil {
		c.note(err)
		c.oblige(obligationProposal, info.TurnID, faultOf(err))
		return
	}
	c.resolve(obligationProposal, info.TurnID)
}

// driveVerification bounded-scans sealed VerificationRequest work
// (_execution.verification.pending), claims each and starts its worker
// outside any transaction and outside this tick's own goroutine, bounded by
// the same concurrency slots every other outside-Unit call shares: this
// package's own tick loop must never block waiting on the trusted verifier
// runner's execution (P23 item 1).
func (c *Controller) driveVerification(ctx, workCtx context.Context, sess *session) {
	var out verificationItemsOutput
	if err := c.call(ctx, sess, "_execution.verification.pending", limitInput{Limit: c.batch()}, &out); err != nil {
		c.note(err)
		return
	}
	for _, raw := range out.Items {
		if !c.admitting() {
			return
		}
		var probe verificationRequestProbe
		if json.Unmarshal(raw, &probe) != nil {
			continue
		}
		if !c.acquire() {
			return
		}
		if !c.claimVerification(ctx, workCtx, sess, probe) {
			c.free()
		}
	}
}

// claimVerification claims one sealed request under the trusted verifier
// identity only -- _execution.verification.claim never gives a worker-
// supplied runner a way to be substituted here -- and starts its worker.
// The claim itself is a pure database transaction with no physical side
// effect; only the verifier call and record that follow run outside it.
func (c *Controller) claimVerification(ctx, workCtx context.Context, sess *session, probe verificationRequestProbe) bool {
	var claimed verificationClaimOutput
	err := c.write(func() error {
		return c.call(ctx, sess, "_execution.verification.claim", verificationClaimInput{
			RequestID: probe.JobID, ExpectedVersion: 1, Generation: sess.generation,
		}, &claimed)
	})
	if err != nil {
		c.note(err)
		return false
	}
	c.workers.Add(1)
	go c.runVerification(workCtx, sess, probe, claimed.Request, claimed.AttemptVersion)
	return true
}

// runVerification executes one claimed verification outside any
// transaction through the real, exclusively attached contract.Verifier --
// c.deps.Verifier is never derived from Collaborators.Jobs or Operator, so
// no worker-authored runner can stand in for it -- then publishes the
// verifier's own staged request/verdict bytes as real artifacts BEFORE
// recording: the verifier's own recorded evidence must exist durably
// before anything downstream treats the task as verified.
//
// attemptVersion is the attempt's live version _execution.verification.
// claim read in its own claim transaction (the freshest read available,
// with the smallest staleness window before it is used below). It fences
// the attempt row itself, not the verification job: an attempt's version
// advances by exactly 1 on every claim/checkpoint/report, so it is never 1
// by the time verification runs for any attempt that did more than a bare
// claim -- a hardcoded ExpectedVersion: 1 here refused every realistic
// journey with stale_version.
func (c *Controller) runVerification(workCtx context.Context, sess *session, probe verificationRequestProbe, request json.RawMessage, attemptVersion int64) {
	defer c.workers.Done()
	defer c.free()

	attemptID := probe.AttemptID
	c.mu.Lock()
	verifier := c.deps.Verifier
	c.mu.Unlock()
	if verifier == nil {
		f := prerequisiteMissing("no verifier is attached; attempt %s's verification request cannot be executed", attemptID)
		c.oblige(obligationVerification, attemptID, f)
		return
	}
	result, err := c.verify(workCtx, verifier, request)
	if err != nil {
		f := unavailable("the verifier returned an error instead of a result: %v", err)
		c.note(f)
		c.oblige(obligationVerification, attemptID, f)
		return
	}
	published, ok := c.publishVerification(workCtx, sess, attemptID, probe.Scope, result.Document)
	if !ok {
		return
	}
	err = c.write(func() error {
		return c.call(workCtx, sess, "_execution.verification.record", verificationRecordInput{
			AttemptID: attemptID, ExpectedVersion: attemptVersion, Result: published,
		}, nil)
	})
	if err != nil {
		c.note(err)
		c.oblige(obligationVerification, attemptID, faultOf(err))
		return
	}
	c.resolve(obligationVerification, attemptID)
}

// verify calls the trusted verifier once, converting a panic into the same
// unestablished-result treatment perform.go's observe already gives a
// panicking adapter: a verifier that cannot say what it did never becomes a
// fabricated pass.
func (c *Controller) verify(ctx context.Context, verifier contract.Verifier, request json.RawMessage) (result contract.VerificationResult, err error) {
	defer func() {
		if recover() != nil {
			err = unavailable("the verifier panicked during Verify")
		}
	}()
	return verifier.Verify(ctx, contract.Verification{Request: request})
}

// publishVerification makes every staged output of a VerificationResult
// document (its own request/verdict bytes among them) a real artifact and
// returns the document with staged locators replaced by published
// references -- reusing the exact same generic staged-output publish
// discipline deliver.go's publish() already applies to an adapter
// observation, since $defs/VerificationResult shares the identical
// staged_outputs/output_artifacts shape. An unpublishable output blocks the
// record call and stays a visible obligation, never a silently incomplete
// verdict.
func (c *Controller) publishVerification(ctx context.Context, sess *session, attemptID contract.ID, scope contract.Scope, document json.RawMessage) (json.RawMessage, bool) {
	staged, err := stagedOutputs(document)
	if err != nil {
		c.oblige(obligationVerification, attemptID, invalidInput("verification result declares malformed staged outputs"))
		return nil, false
	}
	if f := checkLocators(document, staged); f != nil {
		c.oblige(obligationVerification, attemptID, f)
		return nil, false
	}
	if len(staged) == 0 {
		return document, true
	}
	c.mu.Lock()
	blobs := c.deps.Blobs
	c.mu.Unlock()
	if blobs == nil {
		f := prerequisiteMissing(
			"no blob store is attached; %d staged output(s) of attempt %s's verification cannot be published", len(staged), attemptID)
		c.note(f)
		c.oblige(obligationVerification, attemptID, f)
		return nil, false
	}
	if scope.InstallationID == "" {
		scope = sess.scope
	}
	published := make([]publishedOutput, 0, len(staged))
	for _, out := range staged {
		var artifact artifactOutput
		err := c.write(func() error {
			if err := blobs.Publish(ctx, out.StagingRef, out.Digest); err != nil {
				return err
			}
			return c.call(ctx, sess, "_artifacts.publish", artifactsPublishInput{
				Scope: scope, Digest: out.Digest, Size: out.Size, MediaType: out.MediaType,
				Classification: out.Classification, Encrypted: true,
			}, &artifact)
		})
		if err == nil && (artifact.Resource.ID == "" || artifact.Resource.Digest != out.Digest) {
			err = internalFault("artifacts owner published metadata that does not match the staged digest")
		}
		if err != nil {
			c.note(err)
			c.oblige(obligationVerification, attemptID, faultOf(err))
			return nil, false
		}
		published = append(published, publishedOutput{StagingRef: out.StagingRef, Digest: out.Digest, ArtifactID: artifact.Resource.ID})
	}
	normalized, err := normalizeEvidence(document, published)
	if err != nil {
		c.oblige(obligationVerification, attemptID, internalFault("published verification result cannot be normalized"))
		return nil, false
	}
	c.resolve(obligationVerification, attemptID)
	return normalized, true
}

func nonNilArtifacts(in []wireArtifact) []wireArtifact {
	if in == nil {
		return []wireArtifact{}
	}
	return in
}
