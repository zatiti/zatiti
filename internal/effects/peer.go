package effects

import (
	"context"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Outgoing owner calls. Every peer operation is version 1 and runs inside
// the caller's transaction through Ports.Call; a peer fault passes through
// unchanged so callers see the exact refusal code. Only the thirteen
// allowlisted outgoing calls with a production path in this package are
// wrapped (see the final report for the two allowlisted calls with no
// specified effects flow).

// peerVersion is the contract version every peer descriptor serves.
const peerVersion int64 = 1

// callPeer invokes one peer operation and decodes its output body.
func callPeer[I, O any](ctx context.Context, s *Service, unit contract.Unit, op string, input I, output *O) error {
	if s.deps.Ports == nil {
		return prerequisiteMissing("peer operation %s is unavailable without a ports dependency", op)
	}
	raw, err := canonicalJSON(input)
	if err != nil {
		return err
	}
	payload, err := s.deps.Ports.Call(ctx, unit, contract.Invocation{Operation: op, Version: peerVersion, Input: raw})
	if err != nil {
		return fmt.Errorf("effects: call %s: %w", op, err)
	}
	if payload.Error != nil {
		return payload.Error
	}
	if payload.Status != contract.StatusCompleted {
		return &contract.Fault{Code: contract.CodeInternalError,
			Message: fmt.Sprintf("peer operation %s returned status %q", op, payload.Status)}
	}
	if err := contract.DecodeStrict(payload.Data, output); err != nil {
		return &contract.Fault{Code: contract.CodeInternalError,
			Message: fmt.Sprintf("peer operation %s returned malformed data: %v", op, err)}
	}
	return nil
}

// authority reads current principal revocation state and restrictions.
func (s *Service) authority(ctx context.Context, unit contract.Unit, in identityAuthorityInput) (authorityBody, error) {
	var out authorityBody
	if err := callPeer(ctx, s, unit, opIdentityAuthority, in, &out); err != nil {
		return authorityBody{}, err
	}
	return out, nil
}

// configurationSnapshot reads the current configuration revision for a scope.
func (s *Service) configurationSnapshot(ctx context.Context, unit contract.Unit, in configurationSnapshotInput) (snapshotBody, error) {
	var out snapshotBody
	if err := callPeer(ctx, s, unit, opConfigSnapshot, in, &out); err != nil {
		return snapshotBody{}, err
	}
	return out, nil
}

// policyCheck evaluates current authorization for one capability and action.
func (s *Service) policyCheck(ctx context.Context, unit contract.Unit, in policyCheckInput) (policyResultBody, error) {
	var out policyResultBody
	if err := callPeer(ctx, s, unit, opPolicyCheck, in, &out); err != nil {
		return policyResultBody{}, err
	}
	return out, nil
}

// reviewsEnsure creates or inspects the exact digest-bound review.
func (s *Service) reviewsEnsure(ctx context.Context, unit contract.Unit, in reviewsEnsureInput) (reviewResourceBody, error) {
	var out reviewResourceBody
	if err := callPeer(ctx, s, unit, opReviewsEnsure, in, &out); err != nil {
		return reviewResourceBody{}, err
	}
	return out, nil
}

// reviewsCheck rechecks the current exact decision for one action digest.
func (s *Service) reviewsCheck(ctx context.Context, unit contract.Unit, in reviewsCheckInput) (reviewsCheckBody, error) {
	var out reviewsCheckBody
	if err := callPeer(ctx, s, unit, opReviewsCheck, in, &out); err != nil {
		return reviewsCheckBody{}, err
	}
	return out, nil
}

// accountingInspect reads current intersected limits and honest usage.
func (s *Service) accountingInspect(ctx context.Context, unit contract.Unit, in accountingInspectInput) (inspectBody, error) {
	var out inspectBody
	if err := callPeer(ctx, s, unit, opAccountingInspect, in, &out); err != nil {
		return inspectBody{}, err
	}
	return out, nil
}

// accountingReserve reserves enforceable cost and concurrency atomically.
func (s *Service) accountingReserve(ctx context.Context, unit contract.Unit, in accountingReserveInput) (reservationResourceBody, error) {
	var out reservationResourceBody
	if err := callPeer(ctx, s, unit, opAccountingReserve, in, &out); err != nil {
		return reservationResourceBody{}, err
	}
	return out, nil
}

// accountingSettle settles observed cost or retains unknown reservation.
func (s *Service) accountingSettle(ctx context.Context, unit contract.Unit, in accountingSettleInput) (reservationResourceBody, error) {
	var out reservationResourceBody
	if err := callPeer(ctx, s, unit, opAccountingSettle, in, &out); err != nil {
		return reservationResourceBody{}, err
	}
	return out, nil
}

// connectionsResolve resolves the validated connection, credential reference
// and tool contract for one dispatch.
func (s *Service) connectionsResolve(ctx context.Context, unit contract.Unit, in connectionsResolveInput) (resolveBody, error) {
	var out resolveBody
	if err := callPeer(ctx, s, unit, opConnectionsResolve, in, &out); err != nil {
		return resolveBody{}, err
	}
	return out, nil
}

// connectionsValidationRecord records observed connection invalidation.
func (s *Service) connectionsValidationRecord(ctx context.Context, unit contract.Unit, in connectionsValidationRecordInput) (connectionResourceBody, error) {
	var out connectionResourceBody
	if err := callPeer(ctx, s, unit, opConnectionsValidationRecord, in, &out); err != nil {
		return connectionResourceBody{}, err
	}
	return out, nil
}

// tasksSnapshot returns the current pinned task contract and scope.
func (s *Service) tasksSnapshot(ctx context.Context, unit contract.Unit, in tasksSnapshotInput) (taskBody, error) {
	var out taskBody
	if err := callPeer(ctx, s, unit, opTasksSnapshot, in, &out); err != nil {
		return taskBody{}, err
	}
	return out, nil
}

// artifactsMetadata validates scope, digests and availability of pinned
// artifacts before disclosure or acceptance.
func (s *Service) artifactsMetadata(ctx context.Context, unit contract.Unit, in artifactsMetadataInput) (artifactsMetadataBody, error) {
	var out artifactsMetadataBody
	if err := callPeer(ctx, s, unit, opArtifactsMetadata, in, &out); err != nil {
		return artifactsMetadataBody{}, err
	}
	return out, nil
}

// executionJobCreate stores one durable execution job.
func (s *Service) executionJobCreate(ctx context.Context, unit contract.Unit, in executionJobCreateInput) (jobResourceBody, error) {
	var out jobResourceBody
	if err := callPeer(ctx, s, unit, opExecutionJobCreate, in, &out); err != nil {
		return jobResourceBody{}, err
	}
	return out, nil
}
