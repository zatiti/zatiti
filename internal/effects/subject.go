package effects

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// ResolveEffectSubject reads only effects-owned persisted authority. The
// application calls this capability after authenticating the controller.
func (s *Service) ResolveEffectSubject(ctx context.Context, unit contract.Unit, invocation contract.Invocation) (contract.Actor, contract.Scope, error) {
	var in struct {
		OperationID contract.ID `json:"operation_id"`
	}
	if err := json.Unmarshal(invocation.Input, &in); err != nil {
		return contract.Actor{}, contract.Scope{}, invalidInput("invalid effect subject reference")
	}
	if in.OperationID == "" {
		return contract.Actor{}, contract.Scope{}, invalidInput("effect subject requires operation_id")
	}
	o, err := loadOperation(ctx, unit, in.OperationID)
	if err != nil {
		return contract.Actor{}, contract.Scope{}, err
	}
	if o == nil {
		return contract.Actor{}, contract.Scope{}, notFound("operation %s does not exist", in.OperationID)
	}
	if err := requireRowInstallation(unit, o.InstallID); err != nil {
		return contract.Actor{}, contract.Scope{}, err
	}
	var actor contract.Actor
	if o.SubjectJSON == "" {
		return actor, contract.Scope{}, prerequisiteMissing("operation lacks a persisted authorization subject")
	}
	if err := json.Unmarshal([]byte(o.SubjectJSON), &actor); err != nil {
		return actor, contract.Scope{}, err
	}
	if actor.PrincipalID == "" {
		return actor, contract.Scope{}, prerequisiteMissing("operation lacks a persisted authorization principal")
	}
	return actor, contract.Scope{InstallationID: o.InstallID, OrganizationID: o.OrganizationID, ProjectID: o.ProjectID, WorkerID: o.WorkerID, TaskID: o.TaskID}, nil
}
