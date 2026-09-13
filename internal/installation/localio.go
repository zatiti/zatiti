package installation

import (
	"context"

	"github.com/zatiti/zatiti/internal/contract"
)

// contract.LocalIO seam for installation.init/backup/restore. The registry
// routes exactly these operations through Prepare/Perform/Finish; Handle
// refuses them.

// Prepare implements contract.LocalIO.
func (s *Service) Prepare(ctx context.Context, unit contract.Unit, invocation contract.Invocation) (contract.IOPlan, error) {
	switch invocation.Operation {
	case opInit:
		return s.prepareInit(ctx, unit, invocation)
	case opBackup:
		return s.prepareBackup(ctx, unit, invocation)
	case opRestore:
		return s.prepareRestore(ctx, unit, invocation)
	default:
		return contract.IOPlan{}, internalError(
			"operation %s does not route through the installation local IO seam", invocation.Operation)
	}
}

// Perform implements contract.LocalIO. It runs outside transactions with no
// Unit at all.
func (s *Service) Perform(ctx context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	switch plan.Invocation.Operation {
	case opInit:
		return s.performInit(ctx, plan)
	case opBackup:
		return s.performBackup(ctx, plan)
	case opRestore:
		return s.performRestore(ctx, plan)
	default:
		return contract.IOResult{}, internalError(
			"operation %s does not route through the installation local IO seam", plan.Invocation.Operation)
	}
}

// Finish implements contract.LocalIO. It revalidates and commits the
// terminal disposition inside the completion transaction.
func (s *Service) Finish(ctx context.Context, unit contract.Unit, plan contract.IOPlan, result contract.IOResult) (contract.Payload, error) {
	if plan.Owner != owner {
		return contract.Payload{}, permissionDenied("local IO plan does not belong to the installation owner")
	}
	switch plan.Invocation.Operation {
	case opInit:
		return s.finishInit(ctx, unit, plan, result)
	case opBackup:
		return s.finishBackup(ctx, unit, plan, result)
	case opRestore:
		return s.finishRestore(ctx, unit, plan, result)
	default:
		return contract.Payload{}, internalError(
			"operation %s does not route through the installation local IO seam", plan.Invocation.Operation)
	}
}
