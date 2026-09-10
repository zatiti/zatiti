package reviews

import (
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs. Field names and shapes mirror the embedded $defs exactly; the
// registry validates every output against those schemas, so a drift here is
// an internal fault, not a silent contract change.

// wireRef mirrors $defs/Ref.
type wireRef struct {
	ID      contract.ID `json:"id"`
	Version int64       `json:"version"`
}

// wireArtifactRef mirrors $defs/ArtifactRef.
type wireArtifactRef struct {
	ID     contract.ID `json:"id"`
	Digest string      `json:"digest"`
}

// wireMoney mirrors $defs/Money.
type wireMoney struct {
	Currency   string `json:"currency"`
	MicroUnits int64  `json:"micro_units"`
}

// wireAction mirrors $defs/Action. Preconditions and parameters are inert
// JSON objects: the schema bounds them and no code executes them.
type wireAction struct {
	Scope                 contract.Scope    `json:"scope"`
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

// wireRequirement mirrors $defs/DecisionRequirement.
type wireRequirement struct {
	ActionDigest       string        `json:"action_digest"`
	HumanRequired      bool          `json:"human_required"`
	EligiblePrincipals []contract.ID `json:"eligible_principals"`
	ExpiresAt          time.Time     `json:"expires_at"`
	SeparateProposer   bool          `json:"separate_proposer"`
}

// wireDecision mirrors $defs/Decision.
type wireDecision struct {
	ID            contract.ID `json:"id"`
	ReviewID      contract.ID `json:"review_id"`
	ReviewVersion int64       `json:"review_version"`
	ActionDigest  string      `json:"action_digest"`
	ReviewerID    contract.ID `json:"reviewer_id"`
	Decision      string      `json:"decision"`
	At            time.Time   `json:"at"`
	Reason        string      `json:"reason"`
}

// wireReview mirrors $defs/Review. decision_id is absent until a decision
// is recorded.
type wireReview struct {
	ID           contract.ID     `json:"id"`
	Version      int64           `json:"version"`
	Scope        contract.Scope  `json:"scope"`
	ActionDigest string          `json:"action_digest"`
	Preview      wireAction      `json:"preview"`
	Requirement  wireRequirement `json:"requirement"`
	ProposerID   contract.ID     `json:"proposer_id"`
	State        string          `json:"state"`
	DecisionID   *contract.ID    `json:"decision_id,omitempty"`
}

// Review lifecycle states.
const (
	statePending     = "pending"
	stateApproved    = "approved"
	stateRejected    = "rejected"
	stateExpired     = "expired"
	stateInvalidated = "invalidated"
)

// Decision values.
const (
	decideApprove = "approve"
	decideReject  = "reject"
)

// Principal kinds.
const (
	kindHuman = "human"
)

// wireCheckInput is the _reviews.check input.
type wireCheckInput struct {
	Scope        contract.Scope `json:"scope"`
	ActionDigest string         `json:"action_digest"`
}

// wireCheckOutput is the _reviews.check output. A false eligible value is
// never permission; callers must treat it as refusal.
type wireCheckOutput struct {
	Eligible bool          `json:"eligible"`
	Decision *wireDecision `json:"decision,omitempty"`
}

// wireEnsureInput is the _reviews.ensure input.
type wireEnsureInput struct {
	Scope       contract.Scope  `json:"scope"`
	Action      wireAction      `json:"action"`
	Requirement wireRequirement `json:"requirement"`
}

// wireDecideInput is the review.decide input.
type wireDecideInput struct {
	Scope           contract.Scope `json:"scope"`
	ID              contract.ID    `json:"id"`
	ExpectedVersion int64          `json:"expected_version"`
	ActionDigest    string         `json:"action_digest"`
	Decision        string         `json:"decision"`
	Reason          string         `json:"reason"`
}

// wireDelegateInput is the review.delegate input.
type wireDelegateInput struct {
	Scope           contract.Scope `json:"scope"`
	ID              contract.ID    `json:"id"`
	ExpectedVersion int64          `json:"expected_version"`
	PrincipalID     contract.ID    `json:"principal_id"`
}

// wireGetInput is the review.get input.
type wireGetInput struct {
	Scope contract.Scope `json:"scope"`
	ID    contract.ID    `json:"id"`
}

// wireListFilter mirrors the shared list filter shape. Reviews support only
// state and needs_you; any other present field is refused invalid_input.
type wireListFilter struct {
	State          *string      `json:"state,omitempty"`
	Key            *string      `json:"key,omitempty"`
	ParentID       *contract.ID `json:"parent_id,omitempty"`
	WorkerID       *contract.ID `json:"worker_id,omitempty"`
	TaskID         *contract.ID `json:"task_id,omitempty"`
	OrganizationID *contract.ID `json:"organization_id,omitempty"`
	Descendants    *bool        `json:"descendants,omitempty"`
	NeedsYou       *bool        `json:"needs_you,omitempty"`
}

// wireListInput is the review.list input.
type wireListInput struct {
	Scope  contract.Scope  `json:"scope"`
	Cursor *string         `json:"cursor,omitempty"`
	Limit  *int64          `json:"limit,omitempty"`
	Filter *wireListFilter `json:"filter,omitempty"`
}

// wireAuthorityPrincipal is the principal projection inside the
// _identity.authority output.
type wireAuthorityPrincipal struct {
	ID      contract.ID    `json:"id"`
	Version int64          `json:"version"`
	Kind    string         `json:"kind"`
	Name    string         `json:"name"`
	Scope   contract.Scope `json:"scope"`
	Revoked bool           `json:"revoked"`
}

// wireAuthority is the _identity.authority output resource. Grants and
// restrictions are opaque here: reviews rechecks principal kind and
// revocation, and never mints authority from grant contents.
type wireAuthority struct {
	Principal    wireAuthorityPrincipal `json:"principal"`
	Grants       []json.RawMessage      `json:"grants"`
	Restrictions []string               `json:"restrictions"`
}

// wireAuthorityInput is the _identity.authority input.
type wireAuthorityInput struct {
	PrincipalID contract.ID    `json:"principal_id"`
	Scope       contract.Scope `json:"scope"`
}

// resourceOut wraps single-resource outputs.
type resourceOut[T any] struct {
	Resource T `json:"resource"`
}
