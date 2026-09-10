package reviews

import (
	"context"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// The two internal operations. check is a pure recheck: it never mutates
// stored state, never trusts caller assertions, and returns eligible=false
// for every condition that is not a currently standing approval — false is
// not permission. ensure is the admission gate for effects: it records the
// exact action preview and requirement at creation and stays idempotent for
// a live review of the same digest.

// check implements _reviews.check. It resolves the latest review for the
// exact action digest and rechecks the decision's reviewer against current
// authority. Port failures propagate; only data conditions produce false.
func (s *Service) check(ctx context.Context, unit contract.Unit, in wireCheckInput) (contract.Payload, error) {
	if err := checkGate(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	review, err := fetchLatestReviewByDigest(ctx, unit, unit.Scope().InstallationID, in.ActionDigest)
	if err != nil {
		return contract.Payload{}, err
	}
	out := wireCheckOutput{Eligible: false}
	if review == nil {
		return completed(out)
	}
	switch review.State {
	case stateApproved:
		// An approval stands only while the requirement is unexpired and
		// its reviewer still satisfies kind, revocation and proposer
		// separation under current authority.
		req, err := review.requirement()
		if err != nil {
			return contract.Payload{}, err
		}
		if s.deps.Clock.Now().After(req.ExpiresAt) {
			return completed(out)
		}
		if review.DecisionID == nil {
			return contract.Payload{}, internalError("approved review %s has no recorded decision", review.ID)
		}
		decision, err := fetchDecisionByID(ctx, unit, unit.Scope().InstallationID, *review.DecisionID)
		if err != nil {
			return contract.Payload{}, err
		}
		if decision == nil {
			return contract.Payload{}, internalError("decision %s for review %s is missing", *review.DecisionID, review.ID)
		}
		if err := s.reviewerStanding(ctx, unit, *review, decision.ReviewerID); err != nil {
			// Permission faults here mean the reviewer lost standing: the
			// recheck answer is refusal, not an error. Port and transport
			// failures are not data answers and must propagate.
			if faultCode(err) == contract.CodePermissionDenied {
				return completed(out)
			}
			return contract.Payload{}, err
		}
		w := decision.wire()
		out.Eligible = true
		out.Decision = &w
		return completed(out)
	case stateRejected:
		// A rejection vetoes this exact digest permanently; the caller
		// sees which decision refused the action.
		if review.DecisionID == nil {
			return contract.Payload{}, internalError("rejected review %s has no recorded decision", review.ID)
		}
		decision, err := fetchDecisionByID(ctx, unit, unit.Scope().InstallationID, *review.DecisionID)
		if err != nil {
			return contract.Payload{}, err
		}
		if decision == nil {
			return contract.Payload{}, internalError("decision %s for review %s is missing", *review.DecisionID, review.ID)
		}
		w := decision.wire()
		out.Decision = &w
		return completed(out)
	default:
		// pending, expired and invalidated are all refusals. Lazy expiry
		// is deliberate: queries never mutate, so a pending review past
		// its expiry reads as not eligible here and is transitioned only
		// when a mutation path (ensure) touches it.
		return completed(out)
	}
}

// ensure implements _reviews.ensure. The requirement and preview are
// recorded exactly as submitted and never rewritten; re-ensuring the same
// digest returns the existing live review unchanged.
func (s *Service) ensure(ctx context.Context, unit contract.Unit, in wireEnsureInput) (contract.Payload, error) {
	if err := checkGate(unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	proposer := unit.Actor().PrincipalID
	if proposer == "" {
		return contract.Payload{}, permissionDenied("authentication is required")
	}
	digest, err := actionDigest(in.Action)
	if err != nil {
		return contract.Payload{}, err
	}
	if in.Requirement.ActionDigest != digest {
		return contract.Payload{}, invalidInput("requirement action_digest does not match the submitted action")
	}
	if in.Action.Scope != in.Scope {
		return contract.Payload{}, invalidInput("action scope does not match the requested scope")
	}
	now := s.deps.Clock.Now()
	if now.After(in.Requirement.ExpiresAt) {
		return contract.Payload{}, invalidInput("requirement has already expired at %s", formatStamp(in.Requirement.ExpiresAt))
	}

	install := unit.Scope().InstallationID
	existing, err := fetchLatestReviewByDigest(ctx, unit, install, digest)
	if err != nil {
		return contract.Payload{}, err
	}
	if existing != nil {
		switch existing.State {
		case statePending:
			req, err := existing.requirement()
			if err != nil {
				return contract.Payload{}, err
			}
			if now.After(req.ExpiresAt) {
				// Transition the stale pending review to expired, then
				// admit a fresh request below. The requirement is never
				// rewritten: expiry is the self-heal for wrongly light
				// requirements.
				if err := updateReview(ctx, unit, *existing, existing.Version+1, stateExpired, nil, now); err != nil {
					return contract.Payload{}, err
				}
				if err := emitTransition(ctx, unit, eventReviewExpired, existing.ID, contract.Version(existing.Version+1)); err != nil {
					return contract.Payload{}, err
				}
			} else {
				// A live pending review for this exact action exists;
				// re-ensure is idempotent and the stored requirement is
				// retained.
				w, err := existing.wire()
				if err != nil {
					return contract.Payload{}, err
				}
				return completed(resourceOut[wireReview]{Resource: w})
			}
		case stateApproved:
			req, err := existing.requirement()
			if err != nil {
				return contract.Payload{}, err
			}
			if !now.After(req.ExpiresAt) && existing.DecisionID != nil {
				// An approval still inside its expiry window stands;
				// reviewer standing is rechecked by check, not here:
				// ensure is a mutation path and must stay deterministic
				// against the stored requirement alone.
				w, err := existing.wire()
				if err != nil {
					return contract.Payload{}, err
				}
				return completed(resourceOut[wireReview]{Resource: w})
			}
			// A dead approval (expired window or corrupt state) admits a
			// fresh request; the approved review stays in history.
		case stateRejected:
			// Rejection is a permanent veto for this exact digest: no new
			// review supersedes it. The caller resolves the refusal
			// through check, which surfaces the decision.
			w, err := existing.wire()
			if err != nil {
				return contract.Payload{}, err
			}
			return completed(resourceOut[wireReview]{Resource: w})
		default:
			// expired and invalidated reviews admit a fresh request.
		}
	}

	created, err := s.createReview(ctx, unit, in.Scope, in.Action, in.Requirement, digest, proposer, now)
	if err != nil {
		return contract.Payload{}, err
	}
	w, err := created.wire()
	if err != nil {
		return contract.Payload{}, err
	}
	return completed(resourceOut[wireReview]{Resource: w})
}

// createReview records one new pending review with its eligible-reviewer
// snapshot and emits the request event.
func (s *Service) createReview(ctx context.Context, unit contract.Unit, scope contract.Scope, action wireAction, req wireRequirement, digest string, proposer contract.ID, now time.Time) (reviewRow, error) {
	id := s.deps.IDs.New()
	previewJSON, err := canonicalActionJSON(action)
	if err != nil {
		return reviewRow{}, err
	}
	requirementJSON, err := canonicalRequirementJSON(req)
	if err != nil {
		return reviewRow{}, err
	}
	r := reviewRow{
		ID:              id,
		Version:         1,
		Scope:           scope,
		ActionDigest:    digest,
		PreviewJSON:     previewJSON,
		RequirementJSON: requirementJSON,
		ProposerID:      proposer,
		State:           statePending,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := insertReview(ctx, unit, r, dedupe(req.EligiblePrincipals)); err != nil {
		return reviewRow{}, err
	}
	if err := emitTransition(ctx, unit, eventReviewRequested, id, 1); err != nil {
		return reviewRow{}, err
	}
	return r, nil
}

// dedupe preserves order while removing duplicate eligible principals, so
// the reviewer snapshot's primary key cannot be violated by a caller list.
func dedupe(ids []contract.ID) []contract.ID {
	seen := make(map[contract.ID]bool, len(ids))
	out := ids[:0:0]
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
