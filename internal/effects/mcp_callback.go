package effects

import (
	"context"
	"encoding/json"
	"github.com/zatiti/zatiti/internal/contract"
)

type callbackEvidenceInput struct {
	OperationID contract.ID `json:"operation_id"`
	AttemptID   contract.ID `json:"attempt_id"`
}
type callbackEvidenceBody struct {
	Action      json.RawMessage `json:"action"`
	Observation wireObservation `json:"observation"`
	Generation  int64           `json:"generation"`
}

// A callback owner reads the recorded physical observation from its owner,
// rather than treating controller-delivered JSON as an attestation.
func (s *Service) handleCallbackEvidence(ctx context.Context, u contract.Unit, in callbackEvidenceInput) (contract.Outcome[callbackEvidenceBody], error) {
	o, err := loadOperation(ctx, u, in.OperationID)
	if err != nil {
		return contract.Outcome[callbackEvidenceBody]{}, err
	}
	if o == nil {
		return contract.Outcome[callbackEvidenceBody]{}, notFound("operation unavailable")
	}
	if err = requireRowInstallation(u, o.InstallID); err != nil {
		return contract.Outcome[callbackEvidenceBody]{}, err
	}
	a, err := loadAttempt(ctx, u, in.AttemptID)
	if err != nil {
		return contract.Outcome[callbackEvidenceBody]{}, err
	}
	if a == nil || a.OperationID != o.ID {
		return contract.Outcome[callbackEvidenceBody]{}, notFound("attempt unavailable")
	}
	records, err := listObservations(ctx, u, o.ID)
	if err != nil {
		return contract.Outcome[callbackEvidenceBody]{}, err
	}
	var selected *observationRow
	for _, r := range records {
		if r.AttemptID == a.ID && r.Kind == obsKindPhysical {
			if selected != nil {
				return contract.Outcome[callbackEvidenceBody]{}, conflict("multiple observations require reconciliation")
			}
			selected = r
		}
	}
	if selected == nil {
		return contract.Outcome[callbackEvidenceBody]{}, prerequisiteMissing("physical observation is not recorded")
	}
	stored, _, err := loadStoredAction(ctx, u, o)
	if err != nil {
		return contract.Outcome[callbackEvidenceBody]{}, err
	}
	obs := wireObservation{Disposition: selected.Disposition, Evidence: json.RawMessage(selected.EvidenceJSON), Usage: wireUsage{}}
	if err = json.Unmarshal([]byte(selected.UsageJSON), &obs.Usage); err != nil {
		return contract.Outcome[callbackEvidenceBody]{}, err
	}
	if selected.ProviderReference != "" {
		v := selected.ProviderReference
		obs.ProviderReference = v
	}
	if !selected.ConfirmedAt.IsZero() {
		v := selected.ConfirmedAt
		obs.ConfirmedAt = &v
	}
	return completedOutcome(callbackEvidenceBody{Action: json.RawMessage(stored.ActionJSON), Observation: obs, Generation: int64(a.Generation)})
}
