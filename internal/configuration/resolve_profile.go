package configuration

import (
	"context"

	"github.com/zatiti/zatiti/internal/contract"
)

type resolveExecutionProfileInput struct {
	Scope   wireScope `json:"scope"`
	Profile wireRef   `json:"profile"`
}

func handleResolveExecutionProfile(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[resolveExecutionProfileInput](s, inv.Operation, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	if in.Profile.ID == "" || in.Profile.Version < 1 {
		return contract.Payload{}, invalidInput("profile must be an exact positive version reference")
	}
	row, err := fetchProfileVersionByID(ctx, unit, in.Scope.InstallationID, in.Profile.ID, in.Profile.Version)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	// Historical versions stay resolvable after replacement/archive so an
	// already-prepared effect can recover against the exact configuration it
	// pinned. A row without adapter_profile still fails below, which keeps
	// legacy definitions from becoming executable by inference.
	if row == nil {
		return contract.Payload{}, notFound("execution profile version %s@%d not found", in.Profile.ID, in.Profile.Version)
	}
	profile := profileDef(row)
	if !adapterProfilePresent(profile.AdapterProfile) {
		return contract.Payload{}, fault(contract.CodePrerequisiteMissing, "execution profile %s@%d requires explicit provider profile resolution", in.Profile.ID, in.Profile.Version)
	}
	if err := validateEditableProfile(profile); err != nil {
		return contract.Payload{}, invalidInput("stored execution profile is invalid: %v", err)
	}
	return s.completed(map[string]any{"resource": profile})
}
