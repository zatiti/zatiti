package configuration

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// The candidate digest.
//
// A plan's candidate_digest is the reviews owner's digest of the exact
// action "apply this plan": the SHA-256 (lowercase hex) of the canonical JSON
// (contract.Canonicalize: sorted keys, exact integers) of the frozen
// $defs/Action preview built by planReviewAction. That preview embeds the
// complete candidate — every staged change in staged order, the base
// revision, the plan and draft identities and the seal instant — so one
// value binds what every owner validates (_<owner>.validate), what policy
// gates (_policy.check candidate_digest), what the eligible owner reviews
// (_reviews.ensure preview, _reviews.check), what configuration.apply
// carries and what activation delivers (_<owner>.activate). It is a
// whole-plan digest, never a per-owner slice digest, and it is
// reconstructible from the sealed plan row alone.

// applyDestination names the action a plan review decides.
const applyDestination = "configuration.apply"

// planReviewWindow is the preview's own validity window after sealing.
// Apply is bounded by the configuration head, not by time; the window is
// preview information for the reviewer and matches the decision horizon
// policy attaches to every review requirement.
const planReviewWindow = 24 * time.Hour

// noObjectRef is the nil UUID at version 1: the frozen Action shape requires
// tool and connection references, and applying configuration names neither.
// The nil UUID is the well-known "no object" value, never a minted identity.
var noObjectRef = wireRef{ID: "00000000-0000-0000-0000-000000000000", Version: 1}

// noCurrency is ISO 4217's code for transactions involving no currency:
// applying configuration has no cost bound of its own.
const noCurrency = "XXX"

// reviewAction mirrors $defs/Action, the immutable preview a review binds.
type reviewAction struct {
	Scope                 wireScope         `json:"scope"`
	Tool                  wireRef           `json:"tool"`
	Connection            wireRef           `json:"connection"`
	AccountIdentity       string            `json:"account_identity"`
	Destination           string            `json:"destination"`
	Content               []wireArtifactRef `json:"content"`
	NotBefore             time.Time         `json:"not_before"`
	ExpiresAt             time.Time         `json:"expires_at"`
	Preconditions         json.RawMessage   `json:"preconditions"`
	ConfigurationRevision int64             `json:"configuration_revision"`
	Parameters            json.RawMessage   `json:"parameters"`
	CostBound             wireMoney         `json:"cost_bound"`
}

// reviewsEnsureInput is the _reviews.ensure input.
type reviewsEnsureInput struct {
	Scope       wireScope               `json:"scope"`
	Action      reviewAction            `json:"action"`
	Requirement wireDecisionRequirement `json:"requirement"`
}

// planScope is the scope a plan is sealed and reviewed under: the
// installation and, when the draft names one, the organization. Every
// review call for the plan uses exactly this scope so the preview's scope
// and the review's scope agree.
func planScope(plan *planRow) wireScope {
	return wireScope{InstallationID: plan.InstallationID, OrganizationID: plan.OrganizationID}
}

// planReviewAction builds the exact apply action of a sealed (or sealing)
// plan from its row and staged changes. Every field is taken from durable
// plan state, so apply rebuilds the same action and the same digest.
func planReviewAction(plan *planRow, changes []wireChange) (reviewAction, error) {
	staged := make([]json.RawMessage, 0, len(changes))
	for _, c := range changes {
		canon, err := canonicalizeChange(c)
		if err != nil {
			return reviewAction{}, err
		}
		staged = append(staged, json.RawMessage(canon))
	}
	parameters, err := json.Marshal(struct {
		PlanID       contract.ID       `json:"plan_id"`
		DraftID      contract.ID       `json:"draft_id"`
		BaseRevision int64             `json:"base_revision"`
		Changes      []json.RawMessage `json:"changes"`
	}{PlanID: plan.ID, DraftID: plan.DraftID, BaseRevision: plan.BaseRevision, Changes: staged})
	if err != nil {
		return reviewAction{}, internalError("review action encoding failed")
	}
	preconditions, err := json.Marshal(struct {
		BaseRevision int64 `json:"base_revision"`
	}{BaseRevision: plan.BaseRevision})
	if err != nil {
		return reviewAction{}, internalError("review action encoding failed")
	}
	sealedAt := plan.CreatedAt.UTC()
	return reviewAction{
		Scope:                 planScope(plan),
		Tool:                  noObjectRef,
		Connection:            noObjectRef,
		AccountIdentity:       string(plan.InstallationID),
		Destination:           applyDestination,
		Content:               []wireArtifactRef{},
		NotBefore:             sealedAt,
		ExpiresAt:             sealedAt.Add(planReviewWindow),
		Preconditions:         preconditions,
		ConfigurationRevision: plan.BaseRevision,
		Parameters:            parameters,
		CostBound:             wireMoney{Currency: noCurrency, MicroUnits: 0},
	}, nil
}

// reviewActionDigest is the reviews owner's digest of one action: SHA-256
// of its canonical JSON.
func reviewActionDigest(action reviewAction) (string, error) {
	raw, err := json.Marshal(action)
	if err != nil {
		return "", internalError("review action encoding failed")
	}
	canon, err := contract.Canonicalize(raw)
	if err != nil {
		return "", internalError("review action is not canonicalizable")
	}
	return string(contract.Hash(canon)), nil
}

// ensureDecisions creates (or inspects) the exact digest-bound review for
// every decision requirement policy attached to the plan, so an eligible
// owner can decide it through the review flow before apply. Each
// requirement carries the plan's candidate digest, which is the digest of
// the preview submitted here; the reviews owner refuses anything else.
func (s *Service) ensureDecisions(ctx context.Context, unit contract.Unit, plan *planRow, action reviewAction, decisions []wireDecisionRequirement) error {
	for _, requirement := range decisions {
		if requirement.ActionDigest != plan.CandidateDigest {
			return internalError("policy bound plan %s to decision digest %s, not its candidate digest", plan.ID, requirement.ActionDigest)
		}
		if _, err := s.callOwner(ctx, unit, "_reviews.ensure", reviewsEnsureInput{
			Scope:       planScope(plan),
			Action:      action,
			Requirement: requirement,
		}); err != nil {
			return err
		}
	}
	return nil
}
