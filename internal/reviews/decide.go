package reviews

import (
	"context"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// review.decide records one immutable decision. Every gate rechecks current
// state — version, exact digest, expiry, principal kind, revocation,
// proposer separation and eligibility — and an agent credential can never
// satisfy a human-required review. A decision is permanent for its review;
// a rejection vetoes the exact action digest.

// eventDecidedData is the inert payload on reviews.review.decided.
type eventDecidedData struct {
	DecisionID contract.ID `json:"decision_id"`
	Decision   string      `json:"decision"`
	ReviewerID contract.ID `json:"reviewer_id"`
}

// decide implements review.decide.
func (s *Service) decide(ctx context.Context, unit contract.Unit, in wireDecideInput) (contract.Payload, error) {
	if err := checkGate(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	reviewer := unit.Actor().PrincipalID
	if reviewer == "" {
		return contract.Payload{}, permissionDenied("authentication is required")
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
	if in.ActionDigest != review.ActionDigest {
		return contract.Payload{}, invalidInput("action_digest does not match the review's exact action")
	}
	now := s.deps.Clock.Now()
	if err := s.decisionGate(ctx, unit, *review, reviewer, now); err != nil {
		return contract.Payload{}, err
	}
	req, err := review.requirement()
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.resolveEligibility(ctx, unit, *review, req, reviewer); err != nil {
		return contract.Payload{}, err
	}

	decision := decisionRow{
		ID:            s.deps.IDs.New(),
		ReviewID:      review.ID,
		ReviewVersion: review.Version,
		Install:       install,
		ActionDigest:  review.ActionDigest,
		ReviewerID:    reviewer,
		Decision:      in.Decision,
		DecidedAt:     now,
		Reason:        in.Reason,
		CreatedAt:     now,
	}
	newVersion := review.Version + 1
	newState := stateApproved
	if in.Decision == decideReject {
		newState = stateRejected
	}
	decisionID := decision.ID
	if err := updateReview(ctx, unit, *review, newVersion, newState, &decisionID, now); err != nil {
		return contract.Payload{}, err
	}
	if err := insertDecision(ctx, unit, decision); err != nil {
		return contract.Payload{}, err
	}
	if err := emitReviewEvent(ctx, unit, eventReviewDecided, review.ID, newVersion, eventDecidedData{
		DecisionID: decision.ID,
		Decision:   decision.Decision,
		ReviewerID: reviewer,
	}); err != nil {
		return contract.Payload{}, err
	}
	return completed(resourceOut[wireDecision]{Resource: decision.wire()})
}

// decisionGate enforces the state and authority conditions shared by decide
// and delegate: the review must be pending and unexpired, and the acting
// principal must hold current authority of the required kind, distinct from
// the proposer where separation is required. The requirement is decoded for
// the caller.
func (s *Service) decisionGate(ctx context.Context, unit contract.Unit, review reviewRow, actor contract.ID, now time.Time) error {
	switch review.State {
	case stateApproved, stateRejected:
		return conflict("review %s already has a recorded decision", review.ID)
	case stateExpired, stateInvalidated:
		return reviewRequired("review %s can no longer be decided; request a current review", review.ID)
	case statePending:
		// fall through to the expiry and authority checks
	default:
		return internalError("review %s has unknown state %q", review.ID, review.State)
	}
	req, err := review.requirement()
	if err != nil {
		return err
	}
	if now.After(req.ExpiresAt) {
		return reviewRequired("review %s expired at %s; request a current review", review.ID, formatStamp(req.ExpiresAt))
	}
	auth, err := s.loadAuthority(ctx, unit, actor, review.Scope)
	if err != nil {
		return err
	}
	if auth.Principal.Revoked {
		return permissionDenied("principal %s is revoked", actor)
	}
	if req.HumanRequired && auth.Principal.Kind != kindHuman {
		return permissionDenied("review %s requires a human reviewer; principal %s is a %s",
			review.ID, actor, auth.Principal.Kind)
	}
	if req.SeparateProposer && actor == review.ProposerID {
		return permissionDenied("review %s requires separation between proposer and reviewer", review.ID)
	}
	return nil
}

// resolveEligibility verifies that the acting principal may decide the
// review, either directly from the eligible snapshot or through a recorded
// delegation. Delegation is single-hop by construction: the delegator must
// itself be in the eligible class, and its current authority is rechecked
// so revoked or reclassified delegators stop lending authority.
func (s *Service) resolveEligibility(ctx context.Context, unit contract.Unit, review reviewRow, req wireRequirement, candidate contract.ID) error {
	for _, id := range req.EligiblePrincipals {
		if id == candidate {
			return nil
		}
	}
	delegation, err := fetchDelegationTo(ctx, unit, unit.Scope().InstallationID, review.ID, candidate)
	if err != nil {
		return err
	}
	if delegation == nil {
		return permissionDenied("principal %s is not eligible to decide review %s", candidate, review.ID)
	}
	eligible := false
	for _, id := range req.EligiblePrincipals {
		if id == delegation.DelegatorID {
			eligible = true
			break
		}
	}
	if !eligible {
		return permissionDenied("delegation to %s for review %s does not originate in the eligible class", candidate, review.ID)
	}
	auth, err := s.loadAuthority(ctx, unit, delegation.DelegatorID, review.Scope)
	if err != nil {
		return err
	}
	if auth.Principal.Revoked {
		return permissionDenied("delegator %s is revoked", delegation.DelegatorID)
	}
	if req.HumanRequired && auth.Principal.Kind != kindHuman {
		return permissionDenied("review %s requires a human reviewer; delegator %s is a %s",
			review.ID, delegation.DelegatorID, auth.Principal.Kind)
	}
	if req.SeparateProposer && delegation.DelegatorID == review.ProposerID {
		return permissionDenied("review %s requires separation between proposer and reviewer", review.ID)
	}
	return nil
}
