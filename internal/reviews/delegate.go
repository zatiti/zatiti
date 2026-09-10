package reviews

import (
	"context"

	"github.com/zatiti/zatiti/internal/contract"
)

// review.delegate records one single-hop delegation of review authority.
// Only a currently eligible reviewer may delegate, only to a principal the
// stored requirement admits — the eligible class, kind and proposer
// separation are never broadened — and a delegatee can never re-delegate,
// because the delegator must itself appear in the eligible snapshot.

// eventDelegatedData is the inert payload on reviews.review.delegated.
type eventDelegatedData struct {
	DelegationID contract.ID `json:"delegation_id"`
	DelegatorID  contract.ID `json:"delegator_id"`
	DelegateeID  contract.ID `json:"delegatee_id"`
}

// delegate implements review.delegate.
func (s *Service) delegate(ctx context.Context, unit contract.Unit, in wireDelegateInput) (contract.Payload, error) {
	if err := checkGate(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	delegator := unit.Actor().PrincipalID
	if delegator == "" {
		return contract.Payload{}, permissionDenied("authentication is required")
	}
	if in.PrincipalID == delegator {
		return contract.Payload{}, invalidInput("a reviewer cannot delegate review authority to themselves")
	}
	install := unit.Scope().InstallationID
	review, err := fetchReviewByID(ctx, unit, install, in.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	if review == nil {
		return contract.Payload{}, notFound("review %s not found", in.ID)
	}
	if in.ExpectedVersion != review.Version {
		return contract.Payload{}, staleVersion("review %s is at version %d; expected %d", review.ID, review.Version, in.ExpectedVersion)
	}
	now := s.deps.Clock.Now()
	if err := s.decisionGate(ctx, unit, *review, delegator, now); err != nil {
		return contract.Payload{}, err
	}
	req, err := review.requirement()
	if err != nil {
		return contract.Payload{}, err
	}
	eligible := false
	for _, id := range req.EligiblePrincipals {
		if id == delegator {
			eligible = true
			break
		}
	}
	if !eligible {
		return contract.Payload{}, permissionDenied("principal %s is not eligible to delegate review %s", delegator, review.ID)
	}

	// The delegatee must be a registered principal of the required kind
	// inside the review's scope; an unknown identifier is a caller error,
	// not an authorization grant.
	auth, err := s.loadPeerAuthority(ctx, unit, in.PrincipalID, review.Scope)
	if err != nil {
		if faultCode(err) == contract.CodeNotFound {
			return contract.Payload{}, invalidInput("principal %s is not registered in this installation", in.PrincipalID)
		}
		return contract.Payload{}, err
	}
	if auth.Principal.Revoked {
		return contract.Payload{}, permissionDenied("delegatee %s is revoked", in.PrincipalID)
	}
	if req.HumanRequired && auth.Principal.Kind != kindHuman {
		return contract.Payload{}, permissionDenied("review %s requires a human reviewer; delegatee %s is a %s",
			review.ID, in.PrincipalID, auth.Principal.Kind)
	}
	if req.SeparateProposer && in.PrincipalID == review.ProposerID {
		return contract.Payload{}, permissionDenied("review %s requires separation between proposer and reviewer", review.ID)
	}

	existing, err := fetchDelegation(ctx, unit, install, review.ID, delegator, in.PrincipalID)
	if err != nil {
		return contract.Payload{}, err
	}
	if existing != nil {
		// An identical delegation already stands; repeating it changes
		// nothing and must not consume a version.
		w, err := review.wire()
		if err != nil {
			return contract.Payload{}, err
		}
		return completed(resourceOut[wireReview]{Resource: w})
	}

	record := delegationRow{
		ID:            s.deps.IDs.New(),
		ReviewID:      review.ID,
		ReviewVersion: review.Version,
		Install:       install,
		DelegatorID:   delegator,
		DelegateeID:   in.PrincipalID,
		CreatedAt:     now,
	}
	newVersion := review.Version + 1
	if err := updateReview(ctx, unit, *review, newVersion, review.State, review.DecisionID, now); err != nil {
		return contract.Payload{}, err
	}
	if err := insertDelegation(ctx, unit, record); err != nil {
		return contract.Payload{}, err
	}
	if err := emitReviewEvent(ctx, unit, eventReviewDelegated, review.ID, newVersion, eventDelegatedData{
		DelegationID: record.ID,
		DelegatorID:  delegator,
		DelegateeID:  in.PrincipalID,
	}); err != nil {
		return contract.Payload{}, err
	}
	review.Version = newVersion
	review.UpdatedAt = now
	w, err := review.wire()
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(resourceOut[wireReview]{Resource: w})
}
