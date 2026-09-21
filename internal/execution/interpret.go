package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// P16: interpret a delivered model response into bounded, governed work.
// Model output is untrusted input: every decision below either matches one
// of the sealed local decision tools (contract.LocalDecisionTool*, never
// routed through an adapter) or a tool the turn's own last-committed
// context plan actually offered -- never a tool/connection/operation named
// only by the model's own text. A proposal outside that closure, or a
// proposal whose declared source_context does not match what was actually
// sent, is recorded refused: zero effects, a structured disposition, never
// a fabricated success or a fabricated failure of something else.
//
// Dispatch of the outgoing model_step effect itself (session handle
// lifecycle, calling _effects.prepare with the CallbackRoute) is out of this
// card's scope, exactly as P15 left it (context_build.go,
// buildResponsesModelStepAction's doc comment) -- this file is the receiving
// half: given a normalized ModelOutput already delivered through
// _execution.observation for a turn-linked hosted attempt, interpret it.

// wireModelOutput mirrors the frozen $defs/ModelOutput exactly (embedded in
// contextSchemaDefs, context_schema.go). This is untrusted model-adapter
// evidence: it is schema-validated before being decoded into this shape,
// and decoded strictly so an unknown field fails closed rather than being
// silently ignored.
type wireModelOutput struct {
	Schema                string                  `json:"schema"`
	ResponseID            string                  `json:"response_id"`
	RequestContext        wireArtifactLocator     `json:"request_context"`
	FinishReason          string                  `json:"finish_reason"`
	Refusal               string                  `json:"refusal,omitempty"`
	TextOutputs           []wireArtifactLocator   `json:"text_outputs"`
	ToolProposals         []wireModelToolProposal `json:"tool_proposals"`
	Usage                 json.RawMessage         `json:"usage"`
	ContinuationReference string                  `json:"continuation_reference,omitempty"`
}

// wireModelToolProposal mirrors the frozen $defs/ModelToolProposal exactly.
type wireModelToolProposal struct {
	ID               string           `json:"id"`
	Tool             wireRef          `json:"tool"`
	OperationID      string           `json:"operation_id"`
	OperationVersion contract.Version `json:"operation_version"`
	Input            json.RawMessage  `json:"input"`
	SourceContext    wireArtifactRef  `json:"source_context"`
	Explanation      string           `json:"explanation,omitempty"`
}

// normalizedProposal is execution's own private interpretation of one
// ModelToolProposal. ProposalRecord.normalized_proposal is declared inert
// JSON data in the frozen schema ("never executable authority"), so this
// shape is not itself a frozen $def -- it is P16's own record of what was
// decided, for the caller that later drives WorkerOperator/effects and for
// the next context build to explain a prior step.
type normalizedProposal struct {
	Kind string `json:"kind"` // reply | clarify | report_outputs | cycle_decision | local_operation | external_tool | refused

	Text     string                  `json:"text,omitempty"`     // reply
	Question string                  `json:"question,omitempty"` // clarify
	Bindings []reportBindingProposal `json:"bindings,omitempty"` // report_outputs

	Decision string `json:"decision,omitempty"`  // cycle_decision
	Reason   string `json:"reason,omitempty"`    // cycle_decision / refused explanation
	NextWake string `json:"next_wake,omitempty"` // cycle_decision

	Operation        string           `json:"operation,omitempty"`         // local_operation
	OperationVersion contract.Version `json:"operation_version,omitempty"` // local_operation
	Input            json.RawMessage  `json:"input,omitempty"`             // local_operation / external_tool

	ToolID       contract.ID      `json:"tool_id,omitempty"`       // external_tool
	ToolVersion  contract.Version `json:"tool_version,omitempty"`  // external_tool
	ConnectionID contract.ID      `json:"connection_id,omitempty"` // external_tool

	RefusalCode    string `json:"refusal_code,omitempty"`
	RefusalMessage string `json:"refusal_message,omitempty"`
}

type reportBindingProposal struct {
	Name     string          `json:"name"`
	Artifact wireArtifactRef `json:"artifact"`
}

// Refusal codes: a proposal outside the authorized closure is recorded
// refused with one of these, never silently dropped and never executed.
const (
	refusalUnauthorizedTool  = "unauthorized_tool"
	refusalMalformedProposal = "malformed_proposal"
	refusalStaleContext      = "stale_source_context"
	refusalUnsupportedCycle  = "cycle_decision_requires_responsibility"
)

// localDecisionToolID returns the kind name for a sealed local decision
// tool id, or "" if id does not name one. IDs are the same deterministic
// mint context_build.go's localDecisionTools uses, so a proposal's claimed
// tool.id is matched against the identical, independently recomputed set --
// never trusted merely because the model asserts a name.
func localDecisionToolID(id contract.ID) string {
	for _, name := range []string{
		contract.LocalDecisionToolReply, contract.LocalDecisionToolClarify,
		contract.LocalDecisionToolReportOutputs, contract.LocalDecisionToolCycleDecision,
	} {
		if uuidFromDigest(sha256Hex([]byte("zatiti.local-decision-tool/"+name))) == id {
			return name
		}
	}
	return ""
}

// interpretTurnObservation is the turn-aware extension of _execution.
// observation: given the normalized model evidence for a turn-linked hosted
// attempt's model_step, validate it strictly against the exact request that
// was dispatched, interpret each proposal into bounded governed work and
// advance the turn. It never trusts model text to select a tool,
// connection, credential or human-approval claim -- every accepted
// disposition resolves against execution's own already-authorized state
// (the turn's committed context plan, the sealed local decision tools).
func (s *Service) interpretTurnObservation(ctx context.Context, unit contract.Unit, turn *turnRow, a *attemptRow, in observationInput) (contract.Outcome[attemptBody], error) {
	if in.Observation.Disposition != "succeeded" && in.Observation.Disposition != "accepted" {
		// A failed/not_sent/unknown model_step never reaches interpretation:
		// it is the same unresolved-effect handling every other operation
		// kind already gets (obligation recorded, attempt fenced by the
		// caller's own recovery path). Interpreting content from a call that
		// was never confirmed to have run would fabricate a decision.
		return contract.Outcome[attemptBody]{}, conflict(
			"observation disposition %q carries no model output to interpret", in.Observation.Disposition)
	}

	schema, err := modelOutputSchema()
	if err != nil {
		return contract.Outcome[attemptBody]{}, fmt.Errorf("execution: load model output schema: %w", err)
	}
	if err := contract.ValidateSchema(schema, in.Observation.Evidence); err != nil {
		return contract.Outcome[attemptBody]{}, invalidInput(
			"malformed model output: %v", err)
	}
	var output wireModelOutput
	if err := contract.DecodeStrict(in.Observation.Evidence, &output); err != nil {
		return contract.Outcome[attemptBody]{}, invalidInput("malformed model output: %v", err)
	}
	if output.Schema != "zatiti.model-output/v1" {
		return contract.Outcome[attemptBody]{}, invalidInput(
			"model output schema %q is not the accepted zatiti.model-output/v1", output.Schema)
	}

	// The request_context binds this response to the exact context this
	// turn actually dispatched -- never to whatever the model claims. A
	// mismatch (stale, replayed against a superseded context, or simply
	// wrong) is a malformed/untrustworthy delivery, refused as a whole
	// rather than partially trusted.
	if turn.ContextArtifact == nil {
		return contract.Outcome[attemptBody]{}, conflict(
			"turn %s has no committed context to interpret a response against", turn.ID)
	}
	if output.RequestContext.Kind != "artifact" || output.RequestContext.Artifact == nil ||
		output.RequestContext.Artifact.ID != turn.ContextArtifact.ID ||
		output.RequestContext.Artifact.Digest != turn.ContextArtifact.Digest {
		return contract.Outcome[attemptBody]{}, invalidInput(
			"model output request_context does not match turn %s's committed context", turn.ID)
	}

	if len(output.ToolProposals) == 0 {
		// A hosted task-bound turn only ever makes governed progress through
		// one of the sealed decision tools or a bound product tool; bare
		// prose with no proposal is an incomplete response, not a decision
		// this pipeline can act on. Refusing it, rather than silently
		// treating it as "nothing happened" or guessing a decision, is the
		// bounded-failure behavior the card requires.
		return contract.Outcome[attemptBody]{}, invalidInput(
			"model output carries no tool proposals; a hosted turn cannot progress on text alone")
	}

	plan, err := latestCommittedContextPlan(ctx, unit, turn.ID)
	if err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if plan == nil {
		return contract.Outcome[attemptBody]{}, prerequisiteMissing(
			"turn %s has no committed context plan to authorize a proposal against", turn.ID)
	}

	stepIndex := turn.StepsUsed
	now := s.now()
	var lastDisposition stepDisposition
	sawNew := false
	anyCompleted := false
	anyPending := false

	for _, mp := range output.ToolProposals {
		if mp.ID == "" {
			return contract.Outcome[attemptBody]{}, invalidInput("malformed model output: a tool proposal carries no id")
		}
		existing, err := findProposalByKey(ctx, unit, turn.ID, stepIndex, mp.ID)
		if err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
		if existing != nil {
			// Idempotent replay: the identical (turn_id, step_index,
			// proposal_id) key already resolved. Never re-interpret and
			// never re-dispatch a second effect/local call for it -- this is
			// the duplicate-proposal defense (zero new effects on replay).
			var np normalizedProposal
			if err := json.Unmarshal(existing.NormalizedProposal, &np); err != nil {
				return contract.Outcome[attemptBody]{}, fmt.Errorf("execution: decode persisted proposal: %w", err)
			}
			lastDisposition = dispositionFor(np, existing.State)
			if lastDisposition.completed {
				anyCompleted = true
			} else {
				anyPending = true
			}
			continue
		}

		disp, err := s.interpretOneProposal(ctx, unit, turn, plan, stepIndex, mp, now)
		if err != nil {
			return contract.Outcome[attemptBody]{}, err
		}
		lastDisposition = disp
		sawNew = true
		if disp.completed {
			anyCompleted = true
		} else {
			anyPending = true
		}
	}

	// Steps used advances exactly once per delivery that recorded at least
	// one newly-interpreted, inline-recorded decision -- counted by whether
	// any proposal completed inline this delivery, never by which proposal
	// happened to be processed last. A local_operation/external_tool
	// proposal left prepared here has its own step counted later, exactly
	// once, when _execution.proposal.record finalizes it -- counting it
	// here too would double-charge the turn's bounded step budget.
	if sawNew && anyCompleted {
		turn.StepsUsed++
	}
	// A ModelOutput may carry more than one proposal. If any of them is
	// still awaiting an outside-unit action (a local_operation or
	// external_tool left "prepared"), the turn must stay reachable
	// (proposal_pending) regardless of what any OTHER proposal in the same
	// delivery decided -- a terminal disposition elsewhere in the same
	// batch (for example order [pending, reply]) must never leave a
	// dispatched effect orphaned by moving the turn to a state
	// work.pending/.claim no longer scans for it under.
	effective := lastDisposition
	if anyPending {
		effective = stepDisposition{kind: "mixed_pending"}
	}
	turn.State, turn.WaitingReason, turn.NextWake = nextTurnState(turn, effective, now)
	turn.UpdatedAt = now
	if err := updateTurn(ctx, unit, turn); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}
	if err := emitTransition(ctx, unit, eventProposalRecorded, turn.ID, turn.Version); err != nil {
		return contract.Outcome[attemptBody]{}, err
	}

	return completedOutcome(attemptBody{Resource: attemptOut(a)})
}

// stepDisposition is the outcome of interpreting one proposal, enough to
// decide the turn's next state without re-decoding normalizedProposal at
// the call site.
type stepDisposition struct {
	kind      string
	completed bool // this proposal was fully recorded inline (no outside-unit step pending)
	terminal  bool // reply/report_outputs/cycle_decision(done): the turn is done producing steps
	waiting   string
	nextWake  time.Time
}

func dispositionFor(np normalizedProposal, state string) stepDisposition {
	d := stepDisposition{kind: np.Kind, completed: state == "recorded"}
	switch np.Kind {
	case "reply":
		d.terminal = true
	case "clarify":
		d.waiting = waitingClarification
	case "report_outputs":
		d.terminal = true
	case "cycle_decision":
		switch np.Decision {
		case "done":
			d.terminal = true
		case "wait", "escalate":
			d.waiting = waitingReview
			if np.Decision == "wait" {
				d.waiting = ""
			}
			if t, err := parseStamp(np.NextWake); err == nil {
				d.nextWake = t
			}
		}
	}
	return d
}

// interpretOneProposal classifies and records one not-yet-seen
// ModelToolProposal. It never trusts the proposal's own claims about which
// tool/connection/operation it names beyond matching them against
// execution's own already-authorized closure (the sealed local decision
// tools, or the turn's committed context plan's own resolved tool
// components); anything else is recorded refused.
func (s *Service) interpretOneProposal(ctx context.Context, unit contract.Unit, turn *turnRow, plan *contextPlanRow, stepIndex int64, mp wireModelToolProposal, now time.Time) (stepDisposition, error) {
	base := &proposalRow{
		ID:                  s.newID(),
		InstallationID:      turn.InstallationID,
		TurnID:              turn.ID,
		StepIndex:           stepIndex,
		ProposalID:          mp.ID,
		SourceContextDigest: mp.SourceContext.Digest,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	// Anti-injection/anti-replay: the proposal must declare the exact
	// context digest this turn actually sent. A stale or fabricated
	// source_context (for example, one carried over by text that leaked
	// into a later fetched tool result and coaxed the model into repeating
	// or inventing a call) never gets past this check.
	if mp.SourceContext.Digest != turn.ContextArtifact.Digest {
		return s.recordRefused(ctx, unit, base, refusalStaleContext,
			"proposal source_context does not match the turn's committed context")
	}

	if kind := localDecisionToolID(mp.Tool.ID); kind != "" {
		return s.interpretLocalDecision(ctx, unit, turn, base, kind, mp, now)
	}

	comp := findToolComponent(plan, mp.Tool.ID, mp.Tool.Version)
	if comp == nil {
		// Attack shape: unauthorized connection/tool. The model named
		// something never offered in this exact step's committed context --
		// refused, zero effects, never resolved against a guessed identity.
		return s.recordRefused(ctx, unit, base, refusalUnauthorizedTool,
			"proposal names a tool not offered in the turn's committed context")
	}

	switch comp.Effect {
	case "local":
		return s.interpretLocalOperation(ctx, unit, base, mp)
	default:
		return s.interpretExternalTool(ctx, unit, turn, base, comp, mp, now)
	}
}

// findToolComponent locates the plan's own resolved tool component matching
// a proposal's claimed tool identity exactly -- the model may only select
// among what execution already offered, never name a different id/version.
func findToolComponent(plan *contextPlanRow, toolID contract.ID, toolVersion contract.Version) *contextComponent {
	for i := range plan.Recipe.Components {
		c := &plan.Recipe.Components[i]
		if c.Kind == "tool" && c.ToolID == toolID && c.ToolVersion == toolVersion {
			return c
		}
	}
	return nil
}

// recordRefused persists a terminal, zero-effect refusal: durable,
// inspectable, never executed and never confused with either a fabricated
// success or a fabricated failure of something else.
func (s *Service) recordRefused(ctx context.Context, unit contract.Unit, p *proposalRow, code, message string) (stepDisposition, error) {
	np := normalizedProposal{Kind: "refused", RefusalCode: code, RefusalMessage: message}
	raw, err := json.Marshal(np)
	if err != nil {
		return stepDisposition{}, err
	}
	p.NormalizedProposal = raw
	p.State = "recorded"
	if err := insertProposal(ctx, unit, p); err != nil {
		return stepDisposition{}, err
	}
	return stepDisposition{kind: "refused", completed: true}, nil
}

// interpretLocalDecision handles a proposal targeting one of the four sealed
// local decision tools. Their inputs are validated against the exact frozen
// schema before use; a proposal claiming one of these tool identities but
// carrying input that does not satisfy its schema is refused, never
// partially trusted.
func (s *Service) interpretLocalDecision(ctx context.Context, unit contract.Unit, turn *turnRow, base *proposalRow, kind string, mp wireModelToolProposal, now time.Time) (stepDisposition, error) {
	schema := decisionToolSchema(kind)
	if err := contract.ValidateSchema(schema, mp.Input); err != nil {
		return s.recordRefused(ctx, unit, base, refusalMalformedProposal,
			fmt.Sprintf("%s input does not satisfy its sealed schema: %v", kind, err))
	}

	switch kind {
	case contract.LocalDecisionToolReply:
		var decoded struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(mp.Input, &decoded); err != nil {
			return stepDisposition{}, err
		}
		np := normalizedProposal{Kind: "reply", Text: decoded.Text}
		return s.recordInline(ctx, unit, base, np)

	case contract.LocalDecisionToolClarify:
		var decoded struct {
			Question string `json:"question"`
		}
		if err := json.Unmarshal(mp.Input, &decoded); err != nil {
			return stepDisposition{}, err
		}
		np := normalizedProposal{Kind: "clarify", Question: decoded.Question}
		return s.recordInline(ctx, unit, base, np)

	case contract.LocalDecisionToolReportOutputs:
		var decoded struct {
			Bindings []reportBindingProposal `json:"bindings"`
		}
		if err := json.Unmarshal(mp.Input, &decoded); err != nil {
			return stepDisposition{}, err
		}
		return s.interpretReportOutputs(ctx, unit, turn, base, decoded.Bindings, now)

	case contract.LocalDecisionToolCycleDecision:
		var decoded struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
			NextWake string `json:"next_wake,omitempty"`
		}
		if err := json.Unmarshal(mp.Input, &decoded); err != nil {
			return stepDisposition{}, err
		}
		return s.interpretCycleDecision(ctx, unit, turn, base, decoded.Decision, decoded.Reason, decoded.NextWake, now)

	default:
		return s.recordRefused(ctx, unit, base, refusalMalformedProposal, "unrecognized sealed decision tool")
	}
}

// decisionToolSchema returns the exact frozen schema for one sealed local
// decision tool kind.
func decisionToolSchema(kind string) json.RawMessage {
	switch kind {
	case contract.LocalDecisionToolReply:
		return contract.ReplyProposalSchema
	case contract.LocalDecisionToolClarify:
		return contract.ClarifyProposalSchema
	case contract.LocalDecisionToolReportOutputs:
		return contract.ReportOutputsProposalSchema
	case contract.LocalDecisionToolCycleDecision:
		return contract.CycleDecisionProposalSchema
	default:
		return json.RawMessage(`{"not":{}}`) // matches nothing
	}
}

// recordInline records a proposal whose disposition is fully decided inside
// this same transaction (reply/clarify): no outside-unit action is
// required, so it is recorded immediately rather than left prepared for a
// caller to complete.
func (s *Service) recordInline(ctx context.Context, unit contract.Unit, p *proposalRow, np normalizedProposal) (stepDisposition, error) {
	raw, err := json.Marshal(np)
	if err != nil {
		return stepDisposition{}, err
	}
	p.NormalizedProposal = raw
	p.State = "recorded"
	if err := insertProposal(ctx, unit, p); err != nil {
		return stepDisposition{}, err
	}
	return dispositionFor(np, "recorded"), nil
}

// interpretReportOutputs binds only artifacts this turn itself already
// produced and recorded (a prior proposal's own result_artifact) -- never an
// arbitrary artifact id/digest the model merely names, which would let
// fabricated or unrelated content masquerade as a verified output. It then
// reuses the trusted internal report path (reportAttempt, the same helper
// _execution.report and the public attempt.report share) so a worker
// process exiting or "reporting" text never itself decides task success;
// independent verification still owns that.
func (s *Service) interpretReportOutputs(ctx context.Context, unit contract.Unit, turn *turnRow, base *proposalRow, bindings []reportBindingProposal, now time.Time) (stepDisposition, error) {
	if turn.AttemptID == "" {
		return s.recordRefused(ctx, unit, base, refusalMalformedProposal,
			"report_outputs requires a task-bound turn with a real attempt")
	}
	prior, err := listProposalsForTurn(ctx, unit, turn.ID)
	if err != nil {
		return stepDisposition{}, err
	}
	traced := make(map[contract.Digest]bool, len(prior))
	for _, p := range prior {
		if p.ResultArtifact != nil {
			traced[p.ResultArtifact.Digest] = true
		}
	}
	outputs := make([]wireArtifactRef, 0, len(bindings))
	for _, b := range bindings {
		if !traced[b.Artifact.Digest] {
			// Never let the model fabricate an output binding to an
			// artifact this turn did not itself actually produce and
			// record -- refuse the whole report rather than accept a
			// partially-trusted result.
			return s.recordRefused(ctx, unit, base, refusalMalformedProposal,
				fmt.Sprintf("report_outputs names artifact %s which this turn never recorded as a proposal result", b.Artifact.ID))
		}
		outputs = append(outputs, b.Artifact)
	}

	a, err := loadAttempt(ctx, unit, turn.AttemptID)
	if err != nil {
		return stepDisposition{}, err
	}
	observations, err := json.Marshal(map[string]any{"reported_via": "report_outputs_proposal", "proposal_id": base.ProposalID})
	if err != nil {
		return stepDisposition{}, err
	}
	if _, err := s.reportAttempt(ctx, unit, a, a.LeaseID, a.Generation, outputs, bindings, observations, wireUsage{Currency: turn.Limits.Currency}); err != nil {
		return stepDisposition{}, err
	}

	np := normalizedProposal{Kind: "report_outputs", Bindings: bindings}
	raw, err := json.Marshal(np)
	if err != nil {
		return stepDisposition{}, err
	}
	base.NormalizedProposal = raw
	base.State = "recorded"
	if err := insertProposal(ctx, unit, base); err != nil {
		return stepDisposition{}, err
	}
	return dispositionFor(np, "recorded"), nil
}

// interpretCycleDecision routes a responsibility-triggered turn's reasoning
// outcome through scheduling's own owner-backed cycle ledger. A turn with no
// responsibility source cannot name a responsibility_id to bind the cycle
// to, so the decision is refused rather than fabricated against the wrong
// owner.
func (s *Service) interpretCycleDecision(ctx context.Context, unit contract.Unit, turn *turnRow, base *proposalRow, decision, reason, nextWakeStr string, now time.Time) (stepDisposition, error) {
	if turn.Source.Kind != "responsibility" {
		return s.recordRefused(ctx, unit, base, refusalUnsupportedCycle,
			"cycle_decision requires a responsibility-triggered turn")
	}
	nextWake := now
	if nextWakeStr != "" {
		t, err := parseStamp(nextWakeStr)
		if err != nil {
			return s.recordRefused(ctx, unit, base, refusalMalformedProposal, "cycle_decision next_wake does not parse")
		}
		nextWake = t
	}
	cycleID := s.newID()
	if err := s.recordSchedulingCycle(ctx, unit, turn.Source.SourceID, turn.Source.SourceVersion,
		formatStamp(nextWake), []wireArtifactRef{}, []contract.ID{}, cycleID, turn.ID); err != nil {
		return stepDisposition{}, err
	}

	np := normalizedProposal{Kind: "cycle_decision", Decision: decision, Reason: reason, NextWake: formatStamp(nextWake)}
	raw, err := json.Marshal(np)
	if err != nil {
		return stepDisposition{}, err
	}
	base.NormalizedProposal = raw
	base.State = "recorded"
	if err := insertProposal(ctx, unit, base); err != nil {
		return stepDisposition{}, err
	}
	return dispositionFor(np, "recorded"), nil
}

// interpretLocalOperation prepares (never itself executes) a worker-authored
// proposal to invoke an ordinary public operation under the worker's own
// authenticated actor. contract.WorkerOperator.ExecuteWorker must run
// outside any Unit (it re-enters full public-operation authorization in its
// own transaction), so execution can only stage the exact typed request here
// -- the caller performs it and reports the outcome back through
// _execution.proposal.record. Passing operation_id/version/input through
// unmodified is safe: WorkerOperator resolves the actor from the persisted
// turn/worker mapping and re-enters ordinary authorization under that
// worker's own scope, so an unlisted or unauthorized operation is refused
// there, never by this package fabricating controller privilege to force it
// through.
func (s *Service) interpretLocalOperation(ctx context.Context, unit contract.Unit, base *proposalRow, mp wireModelToolProposal) (stepDisposition, error) {
	if mp.OperationID == "" {
		return s.recordRefused(ctx, unit, base, refusalMalformedProposal, "local operation proposal names no operation")
	}
	np := normalizedProposal{
		Kind:             "local_operation",
		Operation:        mp.OperationID,
		OperationVersion: mp.OperationVersion,
		Input:            mp.Input,
	}
	raw, err := json.Marshal(np)
	if err != nil {
		return stepDisposition{}, err
	}
	base.NormalizedProposal = raw
	base.State = "prepared"
	if err := insertProposal(ctx, unit, base); err != nil {
		return stepDisposition{}, err
	}
	return stepDisposition{kind: "local_operation", completed: false}, nil
}

// interpretExternalTool resolves the proposal's matched tool/connection
// fresh -- from the plan's own pinned refs, never from the model's claim --
// validates the tool arguments against the exact bound input schema and
// prepares (never invokes) the effect. Effects.prepare only persists
// immutable admitted intent; the physical call and its confirmed outcome
// are awaited outside this transaction and reported back through
// _execution.proposal.record, so "accepted" is never treated as "confirmed
// succeeded" here.
func (s *Service) interpretExternalTool(ctx context.Context, unit contract.Unit, turn *turnRow, base *proposalRow, comp *contextComponent, mp wireModelToolProposal, now time.Time) (stepDisposition, error) {
	if err := contract.ValidateSchema(rawOrEmptyObject(comp.InputSchema), mp.Input); err != nil {
		return s.recordRefused(ctx, unit, base, refusalMalformedProposal,
			fmt.Sprintf("tool input does not satisfy its bound schema: %v", err))
	}
	conn, tool, err := s.callConnectionsResolve(ctx, unit, turn.Scope,
		wireRef{ID: comp.ConnectionID, Version: comp.ConnectionVersion},
		wireRef{ID: comp.ToolID, Version: comp.ToolVersion},
		firstOrEmpty(comp.Destinations))
	if err != nil {
		// A binding that resolved when the context was built and no longer
		// resolves now (revoked, changed) is refused -- never dispatched
		// against a guessed or stale identity.
		return s.recordRefused(ctx, unit, base, refusalUnauthorizedTool,
			fmt.Sprintf("tool/connection no longer resolves: %v", err))
	}

	expiresAt := turn.LeaseExpiresAt
	if expiresAt.IsZero() {
		expiresAt = now.Add(leaseDuration)
	}
	action := map[string]any{
		"scope":                  turn.Scope,
		"tool":                   map[string]any{"id": tool.ID, "version": tool.Version},
		"connection":             map[string]any{"id": conn.ID, "version": conn.Version},
		"account_identity":       conn.AccountIdentity,
		"destination":            firstOrEmpty(tool.Destinations),
		"content":                []any{},
		"not_before":             formatStamp(now),
		"expires_at":             formatStamp(expiresAt),
		"preconditions":          map[string]any{},
		"configuration_revision": turn.ConfigurationRevision,
		"parameters":             json.RawMessage(mp.Input),
		"cost_bound":             map[string]any{"currency": turn.Limits.Currency, "micro_units": 0},
	}
	sourceID := turn.AttemptID
	if sourceID == "" {
		sourceID = turn.ID
	}
	data, err := s.callPeer(ctx, unit, peerEffectsPrepare, map[string]any{
		"scope":     turn.Scope,
		"action":    action,
		"source_id": sourceID,
		"callback_route": map[string]any{
			"kind": "worker_turn", "turn_id": turn.ID, "step_index": base.StepIndex,
		},
	})
	if err != nil {
		return stepDisposition{}, err
	}
	op, err := decodeResource[struct {
		ID contract.ID `json:"id"`
	}]("effects operation", data)
	if err != nil {
		return stepDisposition{}, err
	}

	np := normalizedProposal{
		Kind: "external_tool", ToolID: tool.ID, ToolVersion: tool.Version,
		ConnectionID: conn.ID, Input: mp.Input,
	}
	raw, err := json.Marshal(np)
	if err != nil {
		return stepDisposition{}, err
	}
	base.NormalizedProposal = raw
	base.State = "prepared"
	base.EffectOperationID = op.ID
	if err := insertProposal(ctx, unit, base); err != nil {
		return stepDisposition{}, err
	}
	return stepDisposition{kind: "external_tool", completed: false}, nil
}

// normalizedProposalKind reads only the "kind" discriminator from a
// persisted normalized_proposal document, tolerating any other shape (the
// field is inert JSON data, never executable authority) -- an empty or
// undecodable kind reads as "".
func normalizedProposalKind(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var probe struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ""
	}
	return probe.Kind
}

// firstOrEmpty returns the first element of a string slice, or "".
func firstOrEmpty(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// nextTurnState computes the turn's next state/waiting-reason/next-wake from
// the last interpreted proposal's disposition, replacing the unconditional
// "always prepare another model step" behavior with a real decision: a
// terminal decision (reply/report_outputs/cycle_decision done) completes the
// turn; clarify/cycle_decision wait or escalate park it waiting; anything
// else (a local/external proposal left prepared for an outside-unit step,
// or a refused proposal that leaves room to try something else) returns it
// to claimed so the caller may loop through context.prepare again. The
// turn's own model-step bound is enforced last and overrides every other
// outcome.
func nextTurnState(turn *turnRow, d stepDisposition, now time.Time) (state, waitingReason string, nextWake time.Time) {
	switch {
	case d.terminal:
		state = "completed"
		if d.kind == "report_outputs" {
			state = "reporting"
		}
	case d.waiting != "" || !d.nextWake.IsZero():
		state = "waiting"
		waitingReason = d.waiting
		nextWake = d.nextWake
	case !d.completed:
		// A local_operation/external_tool proposal is left prepared,
		// awaiting an outside-unit action and a later proposal.record: the
		// turn stays in its committed-context state so a stray duplicate
		// observation cannot advance it twice, but is still reachable by
		// work.claim's existing retry-same-key replay.
		state = "proposal_pending"
	default:
		state = "claimed"
	}
	if turn.Limits.ModelSteps > 0 && turn.StepsUsed >= turn.Limits.ModelSteps {
		// The bounded model-step ceiling is an absolute architectural limit
		// (contracts.md "Effect and execution rules": 100 model steps by
		// default) -- it overrides any decision reached on the step that hit
		// it, matching _execution.proposal.record's own established
		// precedence for the identical bound.
		state = "waiting"
		waitingReason = waitingBudget
		nextWake = time.Time{}
	}
	return state, waitingReason, nextWake
}
