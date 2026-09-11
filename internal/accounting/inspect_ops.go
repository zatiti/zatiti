package accounting

import (
	"context"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Inspection. The limit views report the intersected effective limits; the
// usage views report the most specific named dimension's position, never an
// invented zero for an unnamed scope. Estimated usage is advisory: it cannot
// back an enforceable hard cap and is flagged as such.

// usageForScope resolves the honest usage for one scope: the position of the
// most specific named dimension (worker, project, base organization,
// installation). A scope naming a dimension beyond the installation is
// validated against the configuration snapshot first, so a nonexistent
// object fails the query instead of reporting a phantom zero.
func (s *Service) usageForScope(ctx context.Context, unit contract.Unit, scope wireScope) (wireUsage, error) {
	if _, err := s.levelsForScope(ctx, unit, scope); err != nil {
		return wireUsage{}, err
	}
	lookup := []struct {
		ref  contract.ID
		kind positionKind
	}{
		{scope.WorkerID, posWorker},
		{scope.ProjectID, posProject},
		{scope.OrganizationID, posOrganization},
		{scope.InstallationID, posInstallation},
	}
	for _, l := range lookup {
		if l.ref == "" {
			continue
		}
		p, err := loadPosition(ctx, unit, l.kind, l.ref)
		if err != nil {
			return wireUsage{}, err
		}
		if p != nil {
			return wireUsage{
				Currency:  p.Currency,
				Spent:     p.Spent,
				Reserved:  p.Reserved,
				Estimated: p.Estimated,
				Unknown:   p.Unknown,
				Advisory:  p.Estimated > 0,
			}, nil
		}
	}
	// No position anywhere in the chain: nothing has ever been reserved
	// against these dimensions. The currency stays unconfigured until a
	// reservation or a position pins it.
	return wireUsage{Currency: unconfiguredCurrency, Advisory: false}, nil
}

// handleInspect is the _accounting.inspect boundary: intersected limits plus
// honest usage for admission and doctor.
func (s *Service) handleInspect(ctx context.Context, unit contract.Unit, in scopeInput) (contract.Outcome[inspectOutput], error) {
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[inspectOutput]{}, err
	}
	levels, err := s.levelsForScope(ctx, unit, in.Scope)
	if err != nil {
		return contract.Outcome[inspectOutput]{}, err
	}
	limits, err := effectiveLimits(levels, time.Time{})
	if err != nil {
		return contract.Outcome[inspectOutput]{}, err
	}
	usage, err := s.usageForScope(ctx, unit, in.Scope)
	if err != nil {
		return contract.Outcome[inspectOutput]{}, err
	}
	return completedOutcome(inspectOutput{Limits: *limits, Usage: usage})
}

// handleUsageGet is the usage.get boundary.
func (s *Service) handleUsageGet(ctx context.Context, unit contract.Unit, in scopeInput) (contract.Outcome[usageResourceBody], error) {
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[usageResourceBody]{}, err
	}
	usage, err := s.usageForScope(ctx, unit, in.Scope)
	if err != nil {
		return contract.Outcome[usageResourceBody]{}, err
	}
	return completedOutcome(usageResourceBody{Resource: usage})
}
