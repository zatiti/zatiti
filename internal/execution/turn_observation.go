package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// handleTurnObservation records the exact model-step callback named by the
// controller's durable callback route. It supports both task-bound turns and
// chief chat turns; a chat turn has no synthetic Attempt or task verifier.
func (s *Service) handleTurnObservation(ctx context.Context, unit contract.Unit, in turnObservationInput) (contract.Outcome[turnBody], error) {
	turn, err := loadTurn(ctx, unit, in.TurnID)
	if err != nil {
		return contract.Outcome[turnBody]{}, err
	}
	if turn.InstallationID != installationOf(unit) {
		return contract.Outcome[turnBody]{}, permissionDenied("turn %s belongs to another installation", turn.ID)
	}
	dispatch, err := loadTurnDispatchByRef(ctx, unit, turn.ID, string(in.OperationID))
	if err != nil {
		return contract.Outcome[turnBody]{}, err
	}
	if dispatch == nil || dispatch.Kind != "model_step" {
		return contract.Outcome[turnBody]{}, notFound("turn %s has no model-step dispatch for operation %s", turn.ID, in.OperationID)
	}
	if dispatch.State != "prepared" {
		return contract.Outcome[turnBody]{}, conflict("turn dispatch %s is already %s", dispatch.ID, dispatch.State)
	}
	if dispatch.StepIndex != in.StepIndex || turn.State != "model_pending" || in.StepIndex != turn.StepsUsed {
		return contract.Outcome[turnBody]{}, conflict("turn %s callback step %d is stale; current step is %d in state %s", turn.ID, in.StepIndex, turn.StepsUsed, turn.State)
	}
	now := s.now()
	if in.Observation.Disposition != "succeeded" && in.Observation.Disposition != "accepted" {
		if err := updateTurnDispatchState(ctx, unit, dispatch.ID, "failed"); err != nil {
			return contract.Outcome[turnBody]{}, err
		}
		turn.State = "waiting"
		turn.WaitingReason = waitingEffect
		if in.Observation.Disposition == "unknown" {
			turn.WaitingReason = waitingRecovery
		}
		turn.NextWake = time.Time{}
		turn.UpdatedAt = now
		if err := updateTurn(ctx, unit, turn); err != nil {
			return contract.Outcome[turnBody]{}, err
		}
		if err := emitTransition(ctx, unit, eventTurnWaiting, turn.ID, turn.Version); err != nil {
			return contract.Outcome[turnBody]{}, err
		}
		return completedOutcome(turnBody{Resource: turnOut(turn)})
	}

	schema, err := modelOutputSchema()
	if err != nil {
		return contract.Outcome[turnBody]{}, fmt.Errorf("execution: load model output schema: %w", err)
	}
	if err := contract.ValidateSchema(schema, in.Observation.Evidence); err != nil {
		return contract.Outcome[turnBody]{}, invalidInput("malformed model output: %v", err)
	}
	var output wireModelOutput
	if err := contract.DecodeStrict(in.Observation.Evidence, &output); err != nil {
		return contract.Outcome[turnBody]{}, invalidInput("malformed model output: %v", err)
	}
	if turn.ContextArtifact == nil || output.RequestContext.Kind != "artifact" || output.RequestContext.Artifact == nil ||
		output.RequestContext.Artifact.ID != turn.ContextArtifact.ID || output.RequestContext.Artifact.Digest != turn.ContextArtifact.Digest {
		return contract.Outcome[turnBody]{}, invalidInput("model output request_context does not match turn %s's committed context", turn.ID)
	}
	plan, err := latestCommittedContextPlan(ctx, unit, turn.ID)
	if err != nil {
		return contract.Outcome[turnBody]{}, err
	}
	if plan == nil {
		return contract.Outcome[turnBody]{}, prerequisiteMissing("turn %s has no committed context plan", turn.ID)
	}
	if err := updateTurnDispatchState(ctx, unit, dispatch.ID, "recorded"); err != nil {
		return contract.Outcome[turnBody]{}, err
	}
	if len(output.ToolProposals) == 0 {
		return contract.Outcome[turnBody]{}, invalidInput("model output carries no governed proposal")
	}
	step := turn.StepsUsed
	sawNew, anyCompleted, anyPending := false, false, false
	last := stepDisposition{}
	for _, proposal := range output.ToolProposals {
		if proposal.ID == "" {
			return contract.Outcome[turnBody]{}, invalidInput("model proposal carries no id")
		}
		existing, err := findProposalByKey(ctx, unit, turn.ID, step, proposal.ID)
		if err != nil {
			return contract.Outcome[turnBody]{}, err
		}
		if existing != nil {
			var normalized normalizedProposal
			if err := json.Unmarshal(existing.NormalizedProposal, &normalized); err != nil {
				return contract.Outcome[turnBody]{}, fmt.Errorf("execution: decode persisted proposal: %w", err)
			}
			last = dispositionFor(normalized, existing.State)
		} else {
			last, err = s.interpretOneProposal(ctx, unit, turn, plan, step, proposal, now)
			if err != nil {
				return contract.Outcome[turnBody]{}, err
			}
			sawNew = true
		}
		if last.completed {
			anyCompleted = true
		} else {
			anyPending = true
		}
	}
	if sawNew && anyCompleted {
		turn.StepsUsed++
	}
	if anyPending {
		last = stepDisposition{kind: "mixed_pending"}
	}
	turn.State, turn.WaitingReason, turn.NextWake = nextTurnState(turn, last, now)
	turn.UpdatedAt = now
	if err := updateTurn(ctx, unit, turn); err != nil {
		return contract.Outcome[turnBody]{}, err
	}
	if err := emitTransition(ctx, unit, eventProposalRecorded, turn.ID, turn.Version); err != nil {
		return contract.Outcome[turnBody]{}, err
	}
	return completedOutcome(turnBody{Resource: turnOut(turn)})
}
