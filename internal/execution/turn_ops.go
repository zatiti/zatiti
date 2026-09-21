package execution

import (
	"context"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// The durable worker turn pipeline (revision 3): execution-owned turns for
// chat, task and responsibility triggers admitted once per unique source
// identity, a bounded/fair controller pending+claim boundary, versioned
// immutable context plans, durable per-step proposal records, the
// controller-driven internal report/verification counterpart of the public
// cooperative attempt path, and typed waiting reasons that wakes revalidate
// rather than blindly resume.

// Typed waiting reasons (WorkerTurn.waiting_reason).
const (
	waitingSetup         = "setup"
	waitingClarification = "clarification"
	waitingReview        = "review"
	waitingEffect        = "effect"
	waitingDependency    = "dependency"
	waitingBudget        = "budget"
	waitingRecovery      = "recovery"
)

// Default context plan bounds used when no tighter worker/task bound is
// configured. Context.prepare persists no bytes; these bound the eventual
// staged context the caller assembles from the plan's refs.
const (
	defaultContextByteBound  = 1 << 20 // 1 MiB
	defaultContextTokenBound = 100000
)

// defaultTurnLimits is the architecture default envelope (contracts.md
// "Effect and execution rules": one concurrent attempt/worker, 30
// minutes/attempt, 100 model steps, 24-hour root deadline) applied when a
// worker carries no configured limits of its own.
func defaultTurnLimits(now time.Time) wireLimits {
	return wireLimits{
		Currency:        "USD",
		SpendMicroUnits: 0,
		Concurrency:     1,
		ModelSteps:      100,
		ChildCount:      8,
		DelegationDepth: 3,
		AttemptSeconds:  1800,
		RootDeadline:    formatStamp(now.Add(24 * time.Hour)),
	}
}

// turnAdmitParams is admitTurn's input: the exact source/worker/scope
// identity plus optional pre-resolved binding fields a caller already holds
// (task.enqueue resolves configuration/limits/root itself in the same
// transaction it built the run in; a direct _execution.turn.admit call
// leaves them zero for admitTurn to resolve).
type turnAdmitParams struct {
	Source         wireTurnSource
	WorkerID       contract.ID
	Scope          contract.Scope
	RequesterID    contract.ID
	ConfigRevision contract.Version // 0 => resolved via _configuration.snapshot
	Limits         *wireLimits      // nil => resolved via _configuration.snapshot / defaults
	RootID         contract.ID      // "" => self-root
	TaskID         contract.ID
	RunID          contract.ID
}

// admitTurn is the shared engine behind _execution.turn.admit and the
// hosted-executor turn _execution.enqueue admits alongside a task's run.
// P00-001: re-admission for the same source identity returns the existing
// turn; a different source for a worker with an already-active decision
// stream is a safe-boundary injection into that stream, never a second
// concurrent one. Returns the resolved turn and whether it was newly
// created (a fresh admission, not a replay or an injection).
func (s *Service) admitTurn(ctx context.Context, unit contract.Unit, p turnAdmitParams, now time.Time) (*turnRow, bool, error) {
	switch p.Source.Kind {
	case "message", "task", "responsibility", "continuation":
	default:
		return nil, false, invalidInput("turn source kind %q is not recognized", p.Source.Kind)
	}
	if p.Source.SourceID == "" {
		return nil, false, invalidInput("turn source requires a source identity")
	}
	if p.Source.SourceVersion < 1 {
		return nil, false, invalidInput("turn source requires a source version")
	}
	if p.WorkerID == "" {
		return nil, false, invalidInput("turn admission requires a worker identity")
	}
	installation := p.Scope.InstallationID
	if installation == "" {
		return nil, false, invalidInput("turn admission requires an installation scope")
	}
	if err := checkInstallation(unit, installation); err != nil {
		return nil, false, err
	}

	// Exact identity replay: re-admission for the same source identity
	// returns the existing turn, never a second row.
	existing, err := findTurnBySource(ctx, unit, installation, p.Source.Kind, p.Source.SourceID, p.Source.SourceVersion, p.WorkerID)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, nil
	}

	// Safe-boundary injection: a different source arriving while the worker
	// already has a live (non-terminal) decision stream links into that
	// stream instead of starting a second concurrent one. One active
	// decision stream exists per worker lane; different eligible workers
	// may still run concurrently.
	active, err := findActiveTurnForWorker(ctx, unit, installation, p.WorkerID)
	if err != nil {
		return nil, false, err
	}
	if active != nil {
		return active, false, nil
	}

	// Pause blocks new admission only — an exact replay or an injection
	// into an already-active stream, both handled above, is never refused
	// merely because the worker was paused afterward.
	gate, err := loadGate(ctx, unit, p.WorkerID)
	if err != nil {
		return nil, false, err
	}
	if gate.Paused {
		return nil, false, conflict("worker admission is paused; resume the worker before a new turn is admitted")
	}

	gen := unit.Generation()
	if gen < 1 {
		return nil, false, prerequisiteMissing(
			"controller generation has not been started; a turn cannot bind generation %d", gen)
	}

	revision := p.ConfigRevision
	limits := p.Limits
	if revision == 0 || limits == nil {
		snapshot, err := s.callScopeSnapshot(ctx, unit, p.Scope)
		if err != nil {
			return nil, false, err
		}
		if revision == 0 {
			revision = snapshot.Revision
		}
		if limits == nil {
			if snapshot.Worker != nil && snapshot.Worker.Limits != nil {
				l := *snapshot.Worker.Limits
				limits = &l
			} else {
				l := defaultTurnLimits(now)
				limits = &l
			}
		}
	}

	id := s.newID()
	rootID := p.RootID
	if rootID == "" {
		rootID = id
	}
	t := &turnRow{
		ID:                    id,
		Version:               1,
		WorkerID:              p.WorkerID,
		PrincipalID:           p.WorkerID,
		InstallationID:        installation,
		OrganizationID:        p.Scope.OrganizationID,
		ProjectID:             p.Scope.ProjectID,
		TaskScopeID:           p.Scope.TaskID,
		Scope:                 p.Scope,
		Source:                p.Source,
		RequesterID:           p.RequesterID,
		ConfigurationRevision: revision,
		State:                 "pending",
		Generation:            gen,
		Limits:                *limits,
		RootID:                rootID,
		StepsUsed:             0,
		CreatedAt:             now,
		UpdatedAt:             now,
		TaskID:                p.TaskID,
		RunID:                 p.RunID,
	}
	if err := insertTurn(ctx, unit, t); err != nil {
		return nil, false, err
	}
	if err := emitTransition(ctx, unit, eventTurnAdmitted, t.ID, t.Version); err != nil {
		return nil, false, err
	}
	return t, true, nil
}

// handleTurnAdmit is the _execution.turn.admit boundary. For a message
// source, the turn admission and the message's durable processed marker
// commit in this same transaction — messaging's _messaging.processed is
// P10's own seam expecting execution to call through it exactly here.
func (s *Service) handleTurnAdmit(ctx context.Context, unit contract.Unit, in turnAdmitInput) (contract.Outcome[turnBody], error) {
	if in.RequesterID == "" {
		return contract.Outcome[turnBody]{}, invalidInput("turn admission requires a requester identity")
	}
	now := s.now()
	t, _, err := s.admitTurn(ctx, unit, turnAdmitParams{
		Source:      in.Source,
		WorkerID:    in.WorkerID,
		Scope:       in.Scope,
		RequesterID: in.RequesterID,
	}, now)
	if err != nil {
		return contract.Outcome[turnBody]{}, err
	}
	if in.Source.Kind == "message" {
		if _, err := s.callPeer(ctx, unit, peerMessagingProcessed, map[string]any{
			"message_id":   in.Source.SourceID,
			"recipient_id": in.WorkerID,
			"turn_id":      t.ID,
		}); err != nil {
			return contract.Outcome[turnBody]{}, err
		}
	}
	return completedOutcome(turnBody{Resource: turnOut(t)})
}

// handleWorkPending is the _execution.work.pending boundary: a bounded fair
// scan of typed claim/context/proposal/resume work, oldest admission first
// within each kind. A paused worker's pending turn is not surfaced for
// claim — pause blocks new admission without inference or spending.
func (s *Service) handleWorkPending(ctx context.Context, unit contract.Unit, in workPendingInput) (contract.Outcome[workPendingBody], error) {
	if in.Limit < 1 || in.Limit > 100 {
		return contract.Outcome[workPendingBody]{}, invalidInput("work.pending limit must be between 1 and 100")
	}
	installation := installationOf(unit)
	now := s.now()
	items := make([]wireWorkItem, 0, in.Limit)

	pending, err := listTurns(ctx, unit,
		[]string{"installation_id = ?", "state = 'pending'"}, []any{installation}, in.Limit)
	if err != nil {
		return contract.Outcome[workPendingBody]{}, err
	}
	for _, t := range pending {
		if int64(len(items)) >= in.Limit {
			return completedOutcome(workPendingBody{Items: items})
		}
		gate, err := loadGate(ctx, unit, t.WorkerID)
		if err != nil {
			return contract.Outcome[workPendingBody]{}, err
		}
		if gate.Paused {
			continue
		}
		items = append(items, workItemOut("claim", t))
	}

	remaining := in.Limit - int64(len(items))
	if remaining > 0 {
		waiting, err := listTurns(ctx, unit,
			[]string{"installation_id = ?", "state = 'waiting'", "next_wake != ''", "next_wake <= ?"},
			[]any{installation, formatStamp(now)}, remaining)
		if err != nil {
			return contract.Outcome[workPendingBody]{}, err
		}
		for _, t := range waiting {
			items = append(items, workItemOut("resume", t))
		}
	}

	remaining = in.Limit - int64(len(items))
	if remaining > 0 {
		claimed, err := listTurns(ctx, unit,
			[]string{"installation_id = ?", "state = 'claimed'"}, []any{installation}, remaining)
		if err != nil {
			return contract.Outcome[workPendingBody]{}, err
		}
		for _, t := range claimed {
			items = append(items, workItemOut("context", t))
		}
	}

	remaining = in.Limit - int64(len(items))
	if remaining > 0 {
		awaitingProposal, err := listTurns(ctx, unit,
			[]string{"installation_id = ?", "state = 'model_pending'"}, []any{installation}, remaining)
		if err != nil {
			return contract.Outcome[workPendingBody]{}, err
		}
		for _, t := range awaitingProposal {
			items = append(items, workItemOut("proposal", t))
		}
	}

	return completedOutcome(workPendingBody{Items: items})
}

// handleWorkClaim is the _execution.work.claim boundary: return the
// immutable work item plus a claim token/version, and, for a claim of a
// pending task-triggered turn bound to a hosted-executor worker, admit the
// queued run's attempt automatically through the same fenced rules as the
// public cooperative run.claim — card requirement P14/2. A cooperative-
// executor task never has a turn to begin with (see the _execution.enqueue
// hook below), so it is never reachable here.
func (s *Service) handleWorkClaim(ctx context.Context, unit contract.Unit, in workClaimInput) (contract.Outcome[workClaimBody], error) {
	t, err := loadTurn(ctx, unit, in.WorkID)
	if err != nil {
		return contract.Outcome[workClaimBody]{}, err
	}
	if t.InstallationID != installationOf(unit) {
		return contract.Outcome[workClaimBody]{}, permissionDenied("turn %s belongs to another installation", t.ID)
	}
	if unit.Generation() < 1 {
		return contract.Outcome[workClaimBody]{}, prerequisiteMissing(
			"controller generation has not been started; work cannot bind generation %d", in.Generation)
	}
	now := s.now()

	switch t.State {
	case "pending":
		if t.Version != in.ExpectedVersion {
			return contract.Outcome[workClaimBody]{}, staleVersion(
				"turn %s version %d does not match expected version %d", t.ID, t.Version, in.ExpectedVersion)
		}
		gate, err := loadGate(ctx, unit, t.WorkerID)
		if err != nil {
			return contract.Outcome[workClaimBody]{}, err
		}
		if gate.Paused {
			return contract.Outcome[workClaimBody]{}, conflict("worker admission is paused; resume the worker before claiming")
		}
		if t.Source.Kind == "task" && t.RunID != "" {
			if err := s.autoClaimHostedRun(ctx, unit, t, now); err != nil {
				return contract.Outcome[workClaimBody]{}, err
			}
		}
		t.LeaseID = s.newID()
		t.LeaseExpiresAt = now.Add(leaseDuration)
		t.Generation = in.Generation
		t.State = "claimed"
		t.UpdatedAt = now
		if err := updateTurn(ctx, unit, t); err != nil {
			return contract.Outcome[workClaimBody]{}, err
		}
		if err := emitTransition(ctx, unit, eventTurnClaimed, t.ID, t.Version); err != nil {
			return contract.Outcome[workClaimBody]{}, err
		}
		return completedOutcome(workClaimBody{Item: workItemOut("claim", t), ClaimToken: string(t.LeaseID), Version: t.Version})

	case "waiting":
		if t.Version != in.ExpectedVersion {
			return contract.Outcome[workClaimBody]{}, staleVersion(
				"turn %s version %d does not match expected version %d", t.ID, t.Version, in.ExpectedVersion)
		}
		if t.NextWake.IsZero() || t.NextWake.After(now) {
			return contract.Outcome[workClaimBody]{}, conflict("turn %s is not yet due to wake", t.ID)
		}
		ready, err := s.revalidateWaitingTurn(ctx, unit, t)
		if err != nil {
			return contract.Outcome[workClaimBody]{}, err
		}
		if !ready {
			// The wake revalidated current state and found it still
			// blocked: refuse the claim and keep the same typed reason.
			// A handler that returns a fault writes nothing — the poll
			// backoff that keeps a tight recheck loop from spinning is the
			// caller's own concern, not a mutation this refusal could make
			// stick anyway.
			return contract.Outcome[workClaimBody]{}, conflict(
				"turn %s wake revalidation is still blocked on %s", t.ID, t.WaitingReason)
		}
		t.LeaseID = s.newID()
		t.LeaseExpiresAt = now.Add(leaseDuration)
		t.Generation = in.Generation
		t.State = "claimed"
		t.WaitingReason = ""
		t.WaitingResourceID = ""
		t.NextWake = time.Time{}
		t.UpdatedAt = now
		if err := updateTurn(ctx, unit, t); err != nil {
			return contract.Outcome[workClaimBody]{}, err
		}
		if err := emitTransition(ctx, unit, eventTurnClaimed, t.ID, t.Version); err != nil {
			return contract.Outcome[workClaimBody]{}, err
		}
		return completedOutcome(workClaimBody{Item: workItemOut("resume", t), ClaimToken: string(t.LeaseID), Version: t.Version})

	case "claimed", "context_pending", "model_pending", "proposal_pending":
		// A retry against the same key inspects and returns the same claim
		// rather than creating a second one: the same generation replays.
		if t.Generation == in.Generation {
			kind := "context"
			switch t.State {
			case "model_pending", "proposal_pending":
				kind = "proposal"
			}
			return completedOutcome(workClaimBody{Item: workItemOut(kind, t), ClaimToken: string(t.LeaseID), Version: t.Version})
		}
		return contract.Outcome[workClaimBody]{}, conflict("turn %s is already claimed by generation %d", t.ID, t.Generation)

	default:
		return contract.Outcome[workClaimBody]{}, conflict("turn %s is %s and has no pending work", t.ID, t.State)
	}
}

// autoClaimHostedRun performs the automatic hosted claim of a task-triggered
// turn's queued run: the same fenced admitAttempt rules a human calling the
// public run.claim would exercise, driven by the controller instead. It
// mutates t.AttemptID on success; the caller persists t in the same
// transaction. A run that is no longer live (already claimed by a
// concurrent path, or terminal) has nothing left to auto-claim, and the
// turn still proceeds through its own pipeline.
func (s *Service) autoClaimHostedRun(ctx context.Context, unit contract.Unit, t *turnRow, now time.Time) error {
	r, err := loadRun(ctx, unit, t.RunID)
	if err != nil {
		return err
	}
	if !liveRunStates[r.State] {
		return nil
	}
	snapshot, err := s.callScopeSnapshot(ctx, unit, r.Scope)
	if err != nil {
		return err
	}
	if snapshot.Worker == nil || snapshot.Worker.Profile == nil || snapshot.Worker.Profile.Executor != "hosted" {
		// A turn is only ever admitted for a hosted-executor task (the
		// _execution.enqueue hook below); this should be unreachable.
		// Refusing is the safe failure mode: never auto-claim a cooperative
		// run as hosted.
		return conflict("turn %s names a task whose worker is not a hosted executor; refusing automatic claim", t.ID)
	}
	outcome, err := s.admitAttempt(ctx, unit, r, r.WorkerID, snapshot.Worker.Profile.Capabilities, now)
	if err != nil {
		return err
	}
	t.AttemptID = outcome.Data.Attempt.ID
	return nil
}

// revalidateWaitingTurn re-checks the conditions a woken turn was waiting
// on: pause is the one condition execution durably owns and can revalidate
// on its own; a task-linked turn additionally rechecks the task's current
// cancellation state. Deeper per-reason revalidation (clarification
// received, review decided, dependency resolved, budget replenished)
// belongs to the callers that own those domains and is not fabricated here.
func (s *Service) revalidateWaitingTurn(ctx context.Context, unit contract.Unit, t *turnRow) (bool, error) {
	gate, err := loadGate(ctx, unit, t.WorkerID)
	if err != nil {
		return false, err
	}
	if gate.Paused {
		return false, nil
	}
	if t.TaskID != "" {
		task, err := s.callTaskSnapshot(ctx, unit, t.Scope, t.TaskID)
		if err != nil {
			return false, err
		}
		if task.CancellationRequested {
			return false, nil
		}
	}
	return true, nil
}

// handleContextPrepare (the _execution.context.prepare boundary) now lives
// in context_build.go (P15): it resolves the real
// configuration/task/inbox/memory/tool references described in this
// package's mission, not merely the task's input list.

// handleContextCommit is the _execution.context.commit boundary: publish
// the plan and commit the pinned context after rechecking generation,
// referenced versions and current authority. A stale plan (the turn moved
// or the controller generation advanced since prepare) is discarded and
// refused; the caller rebuilds through context.prepare without spending or
// sending. A staged (unpublished) locator is an obligation, never a
// committed context.
func (s *Service) handleContextCommit(ctx context.Context, unit contract.Unit, in contextCommitInput) (contract.Outcome[turnBody], error) {
	plan, err := loadContextPlan(ctx, unit, in.PlanID)
	if err != nil {
		return contract.Outcome[turnBody]{}, err
	}
	if plan.InstallationID != installationOf(unit) {
		return contract.Outcome[turnBody]{}, permissionDenied("context plan %s belongs to another installation", plan.ID)
	}
	if plan.ExpectedVersion != in.ExpectedVersion || plan.Generation != in.Generation {
		return contract.Outcome[turnBody]{}, staleVersion(
			"context plan %s does not match the given version/generation", plan.ID)
	}
	if in.StagedContext.Kind != "artifact" || in.StagedContext.Artifact == nil {
		return contract.Outcome[turnBody]{}, artifactFault(
			"staged context is an unpublished obligation, not a committed context; publish it before commit")
	}

	t, err := loadTurn(ctx, unit, plan.TurnID)
	if err != nil {
		return contract.Outcome[turnBody]{}, err
	}
	if t.Version != plan.ExpectedVersion || unit.Generation() != plan.Generation {
		return contract.Outcome[turnBody]{}, staleVersion(
			"turn %s changed since the plan was built; discard and rebuild the context", t.ID)
	}
	if t.State != "context_pending" {
		return contract.Outcome[turnBody]{}, conflict("turn %s is %s and cannot commit context", t.ID, t.State)
	}
	now := s.now()

	// Validate pins: re-check the plan's referenced artifacts and pinned
	// configuration revision are still current before this context is
	// treated as an accepted model step, never trusting that nothing moved
	// between prepare and this commit.
	if plan.ConfigurationRevision != t.ConfigurationRevision {
		return contract.Outcome[turnBody]{}, staleVersion(
			"context plan %s pinned configuration revision %d, the turn now carries %d; discard and rebuild",
			plan.ID, plan.ConfigurationRevision, t.ConfigurationRevision)
	}
	if len(plan.Refs) > 0 {
		metas, err := s.callArtifactsMetadata(ctx, unit, t.Scope, plan.Refs)
		if err != nil {
			return contract.Outcome[turnBody]{}, err
		}
		available := make(map[contract.Digest]bool, len(metas))
		for _, m := range metas {
			available[m.Digest] = true
		}
		for _, ref := range plan.Refs {
			if !available[ref.Digest] {
				return contract.Outcome[turnBody]{}, artifactFault(
					"context plan %s pins artifact %s, which no longer resolves", plan.ID, ref.ID)
			}
		}
	}

	if err := commitContextPlan(ctx, unit, plan.ID); err != nil {
		return contract.Outcome[turnBody]{}, err
	}
	ref := *in.StagedContext.Artifact
	t.ContextArtifact = &ref
	t.State = "model_pending"
	t.UpdatedAt = now
	if err := updateTurn(ctx, unit, t); err != nil {
		return contract.Outcome[turnBody]{}, err
	}
	if err := emitTransition(ctx, unit, eventContextCommitted, t.ID, t.Version); err != nil {
		return contract.Outcome[turnBody]{}, err
	}
	// Publish lineage: record every pinned source that fed this committed
	// context so it stays inspectable after the source data itself moves
	// on -- the retained context artifact remains immutable and readable
	// regardless.
	if err := insertTurnContextLineage(ctx, unit, s.newID(), t.ID, plan.ID, "instruction", "", "", now); err != nil {
		return contract.Outcome[turnBody]{}, err
	}
	for _, ref := range plan.Refs {
		if err := insertTurnContextLineage(ctx, unit, s.newID(), t.ID, plan.ID, "history", string(ref.ID), ref.Digest, now); err != nil {
			return contract.Outcome[turnBody]{}, err
		}
	}
	return completedOutcome(turnBody{Resource: turnOut(t)})
}

// handleProposalPrepare is the _execution.proposal.prepare boundary: return
// the typed proposal built only from persisted normalized model evidence.
// An identical repeat replays the existing record. Execution's own turn
// pipeline has no normalized model evidence to build a first proposal from
// until a model step actually dispatches and records one — an honest
// prerequisite_missing, never a fabricated placeholder.
func (s *Service) handleProposalPrepare(ctx context.Context, unit contract.Unit, in proposalPrepareInput) (contract.Outcome[proposalBody], error) {
	t, err := loadTurnForUpdate(ctx, unit, in.TurnID, in.ExpectedVersion)
	if err != nil {
		return contract.Outcome[proposalBody]{}, err
	}
	if t.InstallationID != installationOf(unit) {
		return contract.Outcome[proposalBody]{}, permissionDenied("turn %s belongs to another installation", t.ID)
	}
	existing, err := findProposalByKey(ctx, unit, t.ID, in.StepIndex, in.ProposalID)
	if err != nil {
		return contract.Outcome[proposalBody]{}, err
	}
	if existing != nil {
		return completedOutcome(proposalBody{Resource: proposalOut(existing)})
	}
	return contract.Outcome[proposalBody]{}, prerequisiteMissing(
		"no normalized model evidence is persisted for turn %s step %d proposal %s",
		t.ID, in.StepIndex, in.ProposalID)
}

// handleProposalRecord is the _execution.proposal.record boundary: record
// the next durable turn disposition from a committed command or effect
// outcome. An identical repeat for the same proposal replays; a differing
// repeat is submission_conflict, never a silent overwrite. Recording
// advances the turn's step counter and, once the turn's own model-step
// bound is reached, holds it waiting on a typed budget reason rather than
// letting the caller keep dispatching — the same bound a bounded chief
// conversation before any task exists is held to.
func (s *Service) handleProposalRecord(ctx context.Context, unit contract.Unit, in proposalRecordInput) (contract.Outcome[turnBody], error) {
	p, err := findProposalByID(ctx, unit, installationOf(unit), in.ProposalID)
	if err != nil {
		return contract.Outcome[turnBody]{}, err
	}
	if p == nil {
		return contract.Outcome[turnBody]{}, notFound("proposal %s does not exist", in.ProposalID)
	}
	t, err := loadTurn(ctx, unit, p.TurnID)
	if err != nil {
		return contract.Outcome[turnBody]{}, err
	}

	if p.State == "recorded" {
		// An identical repeat replays the recorded disposition regardless
		// of the caller's now-stale expected_version snapshot: recording
		// already happened and moved the turn once, so a lost-ack retry
		// carries the version the turn had *before* that — nothing new
		// mutates here, so there is nothing to fence against. A differing
		// repeat is refused without ever touching the turn.
		if p.CommandID == in.CommandID && p.EffectOperationID == in.EffectOperationID &&
			artifactRefEqual(p.ResultArtifact, in.ResultArtifact) {
			return completedOutcome(turnBody{Resource: turnOut(t)})
		}
		return contract.Outcome[turnBody]{}, submissionConflict(
			"proposal %s is already recorded with a different result", in.ProposalID)
	}

	if t.Version != in.ExpectedVersion {
		return contract.Outcome[turnBody]{}, staleVersion(
			"turn %s version %d does not match expected version %d", t.ID, t.Version, in.ExpectedVersion)
	}

	now := s.now()
	p.State = "recorded"
	p.CommandID = in.CommandID
	p.EffectOperationID = in.EffectOperationID
	p.ResultArtifact = in.ResultArtifact
	p.UpdatedAt = now
	if err := updateProposal(ctx, unit, p); err != nil {
		return contract.Outcome[turnBody]{}, err
	}
	if err := emitTransition(ctx, unit, eventProposalRecorded, t.ID, t.Version); err != nil {
		return contract.Outcome[turnBody]{}, err
	}

	t.StepsUsed++
	if p.EffectOperationID != "" {
		t.LastObservationID = p.EffectOperationID
	}
	// P16: a local_operation or external_tool proposal is the only kind
	// _execution.observation's interpretation stage ever leaves "prepared"
	// for a caller to finish outside any Unit (WorkerOperator.ExecuteWorker,
	// or an awaited effect) and then report back here. Recording it means
	// that outside-unit action already concluded, so the turn returns to
	// claimed, ready for another context.prepare/dispatch loop -- never left
	// unconditionally "proposal_pending" with nothing left to advance it.
	// Every other kind (reply/clarify/report_outputs/cycle_decision/refused)
	// is recorded inline by that same interpretation stage and never reaches
	// this handler in production; a direct test call (as P14's own fixtures
	// do) keeps the original unconditional transition unchanged.
	t.State = "proposal_pending"
	if kind := normalizedProposalKind(p.NormalizedProposal); kind == "local_operation" || kind == "external_tool" {
		t.State = "claimed"
	}
	t.UpdatedAt = now
	if t.Limits.ModelSteps > 0 && t.StepsUsed >= t.Limits.ModelSteps {
		t.State = "waiting"
		t.WaitingReason = waitingBudget
		t.NextWake = time.Time{}
	}
	if err := updateTurn(ctx, unit, t); err != nil {
		return contract.Outcome[turnBody]{}, err
	}
	return completedOutcome(turnBody{Resource: turnOut(t)})
}

// artifactRefEqual reports whether two optional artifact refs name the same
// artifact.
func artifactRefEqual(a, b *wireArtifactRef) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID && a.Digest == b.Digest
}

// handleExecutionReport is the _execution.report boundary: the same
// report/verification transition as the public cooperative attempt.report,
// driven by the controller on behalf of the current worker subject bound to
// this turn/attempt/lease/generation. Process exit/report never directly
// succeeds the task.
func (s *Service) handleExecutionReport(ctx context.Context, unit contract.Unit, in reportInput) (contract.Outcome[attemptBody], error) {
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	a, err := loadAttemptForUpdate(ctx, unit, in.AttemptID, in.ExpectedVersion)
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if err := narrowAttemptScope(in.Scope, a); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	return s.reportAttempt(ctx, unit, a, in.LeaseID, in.Generation, in.Outputs, in.Observations, in.Usage)
}

// handleVerificationPending is the _execution.verification.pending
// boundary: a bounded scan of sealed VerificationRequest work. No worker
// can claim verifier authority through this read-only scan.
func (s *Service) handleVerificationPending(ctx context.Context, unit contract.Unit, in verificationPendingInput) (contract.Outcome[verificationPendingBody], error) {
	if in.Limit < 1 || in.Limit > 100 {
		return contract.Outcome[verificationPendingBody]{}, invalidInput("verification.pending limit must be between 1 and 100")
	}
	rows, err := listPendingVerificationJobs(ctx, unit, installationOf(unit), in.Limit)
	if err != nil {
		return contract.Outcome[verificationPendingBody]{}, err
	}
	items := make([]wireVerificationRequest, 0, len(rows))
	for _, v := range rows {
		var req wireVerificationRequest
		if err := decodeJSON(string(v.Request), &req); err != nil {
			return contract.Outcome[verificationPendingBody]{}, err
		}
		items = append(items, req)
	}
	return completedOutcome(verificationPendingBody{Items: items})
}

// handleVerificationClaim is the _execution.verification.claim boundary: an
// exact version+generation claim of one sealed VerificationRequest for the
// trusted verifier identity only. A lost claim acknowledgement resolves
// through this same generation-bound token, never by assuming unclaimed.
func (s *Service) handleVerificationClaim(ctx context.Context, unit contract.Unit, in verificationClaimInput) (contract.Outcome[verificationClaimBody], error) {
	v, err := loadVerificationJobByID(ctx, unit, in.RequestID)
	if err != nil {
		return contract.Outcome[verificationClaimBody]{}, err
	}
	if v.InstallationID != installationOf(unit) {
		return contract.Outcome[verificationClaimBody]{}, permissionDenied(
			"verification request %s belongs to another installation", v.ID)
	}
	if v.Version != in.ExpectedVersion {
		return contract.Outcome[verificationClaimBody]{}, staleVersion(
			"verification request %s version %d does not match expected version %d", v.ID, v.Version, in.ExpectedVersion)
	}
	if v.State != "pending" {
		return contract.Outcome[verificationClaimBody]{}, conflict(
			"verification request %s is %s and cannot be claimed", v.ID, v.State)
	}
	if unit.Generation() < 1 {
		return contract.Outcome[verificationClaimBody]{}, prerequisiteMissing(
			"controller generation has not been started; a claim cannot bind generation %d", in.Generation)
	}
	existing, err := loadVerificationClaim(ctx, unit, v.ID)
	if err != nil {
		return contract.Outcome[verificationClaimBody]{}, err
	}
	now := s.now()
	var token string
	switch {
	case existing != nil && existing.ClaimedGeneration == in.Generation:
		// Lost claim acknowledgement resolves through this same
		// generation-bound token, never by assuming unclaimed.
		token = existing.ClaimToken
	case existing != nil:
		return contract.Outcome[verificationClaimBody]{}, conflict(
			"verification request %s is already claimed by generation %d", v.ID, existing.ClaimedGeneration)
	default:
		token = string(s.newID())
		if err := setVerificationClaim(ctx, unit, &verificationClaimRow{
			RequestID: v.ID, ClaimedGeneration: in.Generation, ClaimToken: token, ClaimedAt: now,
		}); err != nil {
			return contract.Outcome[verificationClaimBody]{}, err
		}
		if err := emitTransition(ctx, unit, eventVerificationClaimed, v.ID, v.Version); err != nil {
			return contract.Outcome[verificationClaimBody]{}, err
		}
	}
	var req wireVerificationRequest
	if err := decodeJSON(string(v.Request), &req); err != nil {
		return contract.Outcome[verificationClaimBody]{}, err
	}
	return completedOutcome(verificationClaimBody{Request: req, ClaimToken: token})
}
