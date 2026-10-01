package execution

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// ResolveEffectSubject binds execution preparation to the exact persisted
// turn, rather than treating the controller as the worker's principal.
func (s *Service) ResolveEffectSubject(ctx context.Context, unit contract.Unit, invocation contract.Invocation) (contract.Actor, contract.Scope, error) {
	if invocation.Operation == "_execution.context.prepare" || invocation.Operation == "_execution.context.commit" || invocation.Operation == "_execution.turn.observation" {
		var ref struct {
			TurnID contract.ID `json:"turn_id"`
			PlanID contract.ID `json:"plan_id"`
		}
		if err := json.Unmarshal(invocation.Input, &ref); err != nil {
			return contract.Actor{}, contract.Scope{}, invalidInput("invalid context subject reference")
		}
		if invocation.Operation == "_execution.context.commit" {
			plan, err := loadContextPlan(ctx, unit, ref.PlanID)
			if err != nil {
				return contract.Actor{}, contract.Scope{}, err
			}
			if plan == nil {
				return contract.Actor{}, contract.Scope{}, notFound("context plan does not exist")
			}
			ref.TurnID = plan.TurnID
		}
		turn, err := loadTurn(ctx, unit, ref.TurnID)
		if err != nil {
			return contract.Actor{}, contract.Scope{}, err
		}
		if turn == nil {
			return contract.Actor{}, contract.Scope{}, notFound("worker turn does not exist")
		}
		if turn.InstallationID != unit.Scope().InstallationID || turn.Generation != unit.Generation() || turn.PrincipalID == "" {
			return contract.Actor{}, contract.Scope{}, permissionDenied("context subject is stale or outside the installation")
		}
		return contract.Actor{PrincipalID: turn.PrincipalID, Kind: contract.KindWorker}, turn.Scope, nil
	}
	var in struct {
		Scope  contract.Scope `json:"scope"`
		Action struct {
			Scope contract.Scope `json:"scope"`
		} `json:"action"`
		CallbackRoute *struct {
			Kind      string      `json:"kind"`
			TurnID    contract.ID `json:"turn_id"`
			StepIndex *int64      `json:"step_index"`
		} `json:"callback_route"`
	}
	if err := json.Unmarshal(invocation.Input, &in); err != nil {
		return contract.Actor{}, contract.Scope{}, invalidInput("invalid effect preparation")
	}
	if in.CallbackRoute == nil || in.CallbackRoute.Kind != "worker_turn" || in.CallbackRoute.TurnID == "" || in.CallbackRoute.StepIndex == nil {
		return contract.Actor{}, contract.Scope{}, prerequisiteMissing("execution effect requires a persisted worker turn route")
	}
	t, err := loadTurn(ctx, unit, in.CallbackRoute.TurnID)
	if err != nil {
		return contract.Actor{}, contract.Scope{}, err
	}
	if t == nil {
		return contract.Actor{}, contract.Scope{}, notFound("worker turn does not exist")
	}
	if in.Scope != t.Scope || in.Action.Scope != t.Scope || t.InstallationID != unit.Scope().InstallationID || t.Generation != unit.Generation() || t.StepsUsed != *in.CallbackRoute.StepIndex || t.PrincipalID == "" {
		return contract.Actor{}, contract.Scope{}, permissionDenied("worker effect route is stale or outside the installation")
	}
	return contract.Actor{PrincipalID: t.PrincipalID, Kind: contract.KindWorker}, t.Scope, nil
}
