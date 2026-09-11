package policy

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs. Types decoded from THIS package's own operation inputs are
// decoded strictly (unknown fields rejected) after schema validation; the
// structs below therefore cover exactly the fields the brief schemas allow.
// Types decoded from peer responses (peer*) are read tolerantly with
// encoding/json because the owning peer already validated its output against
// the shared schema before sending; only the fields this package consumes
// are declared.

// Change kind and action constants for compiler candidates.
const (
	kindPolicy       = "policy"
	kindAutonomyRule = "autonomy_rule"

	changeCreate  = "create"
	changeUpdate  = "update"
	changeArchive = "archive"
	changeDelete  = "delete"
)

// Qualification states.
const (
	stateProposed   = "proposed"
	stateQualified  = "qualified"
	stateRejected   = "rejected"
	stateRestricted = "restricted"
	stateExpired    = "expired"
)

// Decision values.
const (
	decisionAllow               = "allow"
	decisionDeny                = "deny"
	decisionReview              = "review"
	decisionPrerequisiteMissing = "prerequisite_missing"
)

// Principal kinds that may establish or change promotion rules. Enabling
// automation is an explicit administrative act; worker and client-agent
// principals propose within rules, they never write them.
func mayGovernAutonomy(kind string) bool {
	return kind == "human" || kind == "service"
}

// Shared reference shapes.

// wireRef is the shared Ref definition: an exact resource identity and
// version.
type wireRef struct {
	ID      contract.ID      `json:"id"`
	Version contract.Version `json:"version"`
}

// wireArtifactRef is the shared ArtifactRef definition.
type wireArtifactRef struct {
	ID     contract.ID `json:"id"`
	Digest string      `json:"digest"`
}

// wireMoney is the shared Money definition: int64 micro-units with checked
// arithmetic.
type wireMoney struct {
	Currency  string `json:"currency"`
	MicroUnit int64  `json:"micro_units"`
}

// wireDiagnostic is the shared Diagnostic definition.
type wireDiagnostic struct {
	Path     string `json:"path"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

// wireRequirement is the shared Requirement definition.
type wireRequirement struct {
	Code       string      `json:"code"`
	Message    string      `json:"message"`
	ResourceID contract.ID `json:"resource_id,omitempty"`
}

// Owned resource shapes.

// wireRule is the shared Rule definition: one standing policy rule. The
// conditions object is inert JSON on the wire and interpreted against the
// supported condition keys by the evaluation engine.
type wireRule struct {
	Capability    string          `json:"capability"`
	Effect        string          `json:"effect"`
	Destinations  []string        `json:"destinations"`
	Decision      string          `json:"decision"`
	HumanRequired bool            `json:"human_required"`
	Conditions    json.RawMessage `json:"conditions"`
}

// wirePolicy is the shared Policy definition: one standing policy document.
type wirePolicy struct {
	ID         contract.ID      `json:"id"`
	Version    contract.Version `json:"version"`
	Scope      contract.Scope   `json:"scope"`
	Rules      []wireRule       `json:"rules"`
	Extensions json.RawMessage  `json:"extensions,omitempty"`
}

// wirePromotionRule is the shared PromotionRule definition: one
// operator-approved earned-autonomy rule.
type wirePromotionRule struct {
	ID                     contract.ID      `json:"id"`
	Version                contract.Version `json:"version"`
	Scope                  contract.Scope   `json:"scope"`
	Capability             string           `json:"capability"`
	Destinations           []string         `json:"destinations"`
	RequiredEvidence       []string         `json:"required_evidence"`
	MinimumSuccesses       int64            `json:"minimum_successes"`
	EvidenceWindowSeconds  int64            `json:"evidence_window_seconds"`
	DisqualifyingEvents    []string         `json:"disqualifying_events"`
	CeilingGrantID         contract.ID      `json:"ceiling_grant_id"`
	HumanRequiredPreserved bool             `json:"human_required_preserved"`
}

// wireQualification is the shared Qualification definition: one worker's
// evidence-bound qualification for one capability.
type wireQualification struct {
	ID            contract.ID      `json:"id"`
	Version       contract.Version `json:"version"`
	WorkerID      contract.ID      `json:"worker_id"`
	Capability    string           `json:"capability"`
	Destinations  []string         `json:"destinations"`
	Rule          wireRef          `json:"rule"`
	Model         string           `json:"model"`
	ToolVersions  []wireRef        `json:"tool_versions"`
	SkillVersions []wireRef        `json:"skill_versions"`
	EvidenceIDs   []contract.ID    `json:"evidence_ids"`
	WindowStart   time.Time        `json:"window_start"`
	WindowEnd     time.Time        `json:"window_end"`
	State         string           `json:"state"`
	Explanation   string           `json:"explanation"`
}

// Decision shapes.

// wireDecisionRequirement is the shared DecisionRequirement definition.
type wireDecisionRequirement struct {
	ActionDigest       string        `json:"action_digest"`
	HumanRequired      bool          `json:"human_required"`
	EligiblePrincipals []contract.ID `json:"eligible_principals"`
	ExpiresAt          time.Time     `json:"expires_at"`
	SeparateProposer   bool          `json:"separate_proposer"`
}

// wirePolicyResult is the shared PolicyResult definition: the outcome of one
// authority intersection.
type wirePolicyResult struct {
	Decision     string                    `json:"decision"`
	Reasons      []string                  `json:"reasons"`
	Requirements []wireDecisionRequirement `json:"requirements"`
}

// Draft shape.

// wireDraft is the shared Draft definition. Changes are inert JSON objects
// on this package's boundary; only configuration interprets them.
type wireDraft struct {
	ID           contract.ID       `json:"id"`
	Version      contract.Version  `json:"version"`
	BaseRevision int64             `json:"base_revision"`
	Changes      []json.RawMessage `json:"changes"`
	Diagnostics  []wireDiagnostic  `json:"diagnostics"`
}

// Exact-action shape (shared Action definition).

// wireAction is the shared Action definition: the exact external action a
// check or explain evaluates. Every field is required by the schema.
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

// Compiler candidate shapes (shared Candidate and Change definitions).

// wireChange is one sealed candidate change.
type wireChange struct {
	Kind            string          `json:"kind"`
	Action          string          `json:"action"`
	ID              contract.ID     `json:"id"`
	ExpectedVersion int64           `json:"expected_version"`
	Definition      json.RawMessage `json:"definition"`
}

// candidateInput is the shared Candidate definition.
type candidateInput struct {
	PlanID          contract.ID  `json:"plan_id"`
	BaseRevision    int64        `json:"base_revision"`
	CandidateDigest string       `json:"candidate_digest"`
	Changes         []wireChange `json:"changes"`
	Dependencies    []wireRef    `json:"dependencies"`
}

// candidateEnvelope wraps the candidate the way the schema requires.
type candidateEnvelope struct {
	Candidate candidateInput `json:"candidate"`
}

// wireValidation is the shared Validation definition: one owner's candidate
// slice validation result.
type wireValidation struct {
	Diagnostics  []wireDiagnostic  `json:"diagnostics"`
	Requirements []wireRequirement `json:"requirements"`
	Dependencies []wireRef         `json:"dependencies"`
}

// Input DTOs for owned operations.

type checkInput struct {
	Scope           contract.Scope `json:"scope"`
	Capability      string         `json:"capability"`
	Action          *wireAction    `json:"action,omitempty"`
	CandidateDigest string         `json:"candidate_digest,omitempty"`
}

type explainInput struct {
	Scope  contract.Scope `json:"scope"`
	Action wireAction     `json:"action"`
}

type invalidateInput struct {
	ChangedDependencies []wireRef `json:"changed_dependencies"`
	Reason              string    `json:"reason"`
}

// policyDefinitionInput is policy.create/policy.update definition payload:
// scope, rules and optional inert extensions. id and version are assigned by
// this package when staging the complete definition.
type policyDefinitionInput struct {
	Scope      contract.Scope  `json:"scope"`
	Rules      []wireRule      `json:"rules"`
	Extensions json.RawMessage `json:"extensions,omitempty"`
}

type policyCreateInput struct {
	Scope      contract.Scope        `json:"scope"`
	Definition policyDefinitionInput `json:"definition"`
	DraftID    contract.ID           `json:"draft_id,omitempty"`
}

type policyUpdateInput struct {
	Scope           contract.Scope        `json:"scope"`
	ID              contract.ID           `json:"id"`
	ExpectedVersion contract.Version      `json:"expected_version"`
	Definition      policyDefinitionInput `json:"definition"`
	DraftID         contract.ID           `json:"draft_id,omitempty"`
}

type policyArchiveInput struct {
	Scope           contract.Scope   `json:"scope"`
	ID              contract.ID      `json:"id"`
	ExpectedVersion contract.Version `json:"expected_version"`
	DraftID         contract.ID      `json:"draft_id,omitempty"`
}

type policyGetInput struct {
	Scope contract.Scope `json:"scope"`
	ID    contract.ID    `json:"id"`
}

type qualificationGetInput struct {
	Scope contract.Scope `json:"scope"`
	ID    contract.ID    `json:"id"`
}

// listFilter is the shared structured list filter. Fields a resource does
// not carry are refused as invalid_input by the list handlers; pointer
// fields distinguish an absent filter from an empty one.
type listFilter struct {
	State          *string      `json:"state,omitempty"`
	Key            *string      `json:"key,omitempty"`
	ParentID       *contract.ID `json:"parent_id,omitempty"`
	WorkerID       *contract.ID `json:"worker_id,omitempty"`
	TaskID         *contract.ID `json:"task_id,omitempty"`
	OrganizationID *contract.ID `json:"organization_id,omitempty"`
	Descendants    *bool        `json:"descendants,omitempty"`
	NeedsYou       *bool        `json:"needs_you,omitempty"`
}

type listInput struct {
	Scope  contract.Scope `json:"scope"`
	Cursor *string        `json:"cursor,omitempty"`
	Limit  *int64         `json:"limit,omitempty"`
	Filter *listFilter    `json:"filter,omitempty"`
}

type autonomyProposeInput struct {
	Scope       contract.Scope `json:"scope"`
	WorkerID    contract.ID    `json:"worker_id"`
	Rule        wireRef        `json:"rule"`
	EvidenceIDs []contract.ID  `json:"evidence_ids"`
}

type autonomyEvaluateInput struct {
	Scope           contract.Scope   `json:"scope"`
	ID              contract.ID      `json:"id"`
	ExpectedVersion contract.Version `json:"expected_version"`
}

type autonomyRestrictInput struct {
	Scope           contract.Scope   `json:"scope"`
	ID              contract.ID      `json:"id"`
	ExpectedVersion contract.Version `json:"expected_version"`
	Reason          string           `json:"reason"`
	EvidenceIDs     []contract.ID    `json:"evidence_ids"`
}

// ruleDefinitionInput is autonomy.rule.create/update definition payload.
type ruleDefinitionInput struct {
	Scope                  contract.Scope `json:"scope"`
	Capability             string         `json:"capability"`
	Destinations           []string       `json:"destinations"`
	RequiredEvidence       []string       `json:"required_evidence"`
	MinimumSuccesses       int64          `json:"minimum_successes"`
	EvidenceWindowSeconds  int64          `json:"evidence_window_seconds"`
	DisqualifyingEvents    []string       `json:"disqualifying_events"`
	CeilingGrantID         contract.ID    `json:"ceiling_grant_id"`
	HumanRequiredPreserved bool           `json:"human_required_preserved"`
}

type ruleCreateInput struct {
	Scope      contract.Scope      `json:"scope"`
	Definition ruleDefinitionInput `json:"definition"`
	DraftID    contract.ID         `json:"draft_id,omitempty"`
}

type ruleUpdateInput struct {
	Scope           contract.Scope      `json:"scope"`
	ID              contract.ID         `json:"id"`
	ExpectedVersion contract.Version    `json:"expected_version"`
	Definition      ruleDefinitionInput `json:"definition"`
	DraftID         contract.ID         `json:"draft_id,omitempty"`
}

type ruleArchiveInput struct {
	Scope           contract.Scope   `json:"scope"`
	ID              contract.ID      `json:"id"`
	ExpectedVersion contract.Version `json:"expected_version"`
	DraftID         contract.ID      `json:"draft_id,omitempty"`
}

type ruleGetInput struct {
	Scope contract.Scope `json:"scope"`
	ID    contract.ID    `json:"id"`
}

// Output bodies for owned operations. Resource and items keys are literal.

type policyResultBody struct {
	Resource wirePolicyResult `json:"resource"`
}

type validationBody struct {
	Resource wireValidation `json:"resource"`
}

type versionsBody struct {
	Versions []wireRef `json:"versions"`
}

type qualificationIDsBody struct {
	QualificationIDs []contract.ID `json:"qualification_ids"`
}

type policyBody struct {
	Resource wirePolicy `json:"resource"`
}

type ruleBody struct {
	Resource wirePromotionRule `json:"resource"`
}

type qualificationBody struct {
	Resource wireQualification `json:"resource"`
}

type stagedPolicyBody struct {
	Draft    wireDraft  `json:"draft"`
	Resource wirePolicy `json:"resource"`
}

type stagedRuleBody struct {
	Draft    wireDraft         `json:"draft"`
	Resource wirePromotionRule `json:"resource"`
}

type explainBody struct {
	Decision     string                    `json:"decision"`
	Reasons      []string                  `json:"reasons"`
	Requirements []wireDecisionRequirement `json:"requirements"`
}

// Outgoing call input shapes (exact brief schemas).

type authorityCallInput struct {
	PrincipalID contract.ID    `json:"principal_id"`
	Scope       contract.Scope `json:"scope"`
}

type restrictCallInput struct {
	PrincipalID contract.ID `json:"principal_id"`
	Capability  string      `json:"capability"`
	Reason      string      `json:"reason"`
}

type promoteCallInput struct {
	PrincipalID    contract.ID       `json:"principal_id"`
	Qualification  wireQualification `json:"qualification"`
	CeilingGrantID contract.ID       `json:"ceiling_grant_id"`
}

type stageCallInput struct {
	Scope   contract.Scope `json:"scope"`
	Change  wireChange     `json:"change"`
	DraftID contract.ID    `json:"draft_id,omitempty"`
}

type scopeCallInput struct {
	Scope contract.Scope `json:"scope"`
}

type taskCallInput struct {
	Scope contract.Scope `json:"scope"`
	ID    contract.ID    `json:"id"`
}

type reviewsCheckCallInput struct {
	Scope        contract.Scope `json:"scope"`
	ActionDigest string         `json:"action_digest"`
}

// Peer response shapes. Decoded tolerantly; peers validate their own output
// against the shared schemas before sending.

type peerPrincipal struct {
	ID      contract.ID      `json:"id"`
	Version contract.Version `json:"version"`
	Kind    string           `json:"kind"`
	Name    string           `json:"name"`
	Scope   contract.Scope   `json:"scope"`
	Revoked bool             `json:"revoked"`
}

type peerGrant struct {
	ID            contract.ID      `json:"id"`
	Version       contract.Version `json:"version"`
	PrincipalID   contract.ID      `json:"principal_id"`
	Scope         contract.Scope   `json:"scope"`
	Capabilities  []string         `json:"capabilities"`
	Destinations  []string         `json:"destinations"`
	Denied        bool             `json:"denied"`
	ExpiresAt     *time.Time       `json:"expires_at,omitempty"`
	ParentGrantID contract.ID      `json:"parent_grant_id,omitempty"`
}

type authorityResource struct {
	Principal    peerPrincipal `json:"principal"`
	Grants       []peerGrant   `json:"grants"`
	Restrictions []string      `json:"restrictions"`
}

type authorityBody struct {
	Resource authorityResource `json:"resource"`
}

type peerDecision struct {
	Decision string `json:"decision"`
}

type reviewsCheckBody struct {
	Eligible bool          `json:"eligible"`
	Decision *peerDecision `json:"decision,omitempty"`
}

type peerTask struct {
	ID              contract.ID      `json:"id"`
	Version         contract.Version `json:"version"`
	Scope           contract.Scope   `json:"scope"`
	WorkerID        contract.ID      `json:"worker_id"`
	State           string           `json:"state"`
	RequiredOutputs []string         `json:"required_outputs,omitempty"`
}

type taskBody struct {
	Resource peerTask `json:"resource"`
}

type peerOrg struct {
	ID       contract.ID      `json:"id"`
	Version  contract.Version `json:"version"`
	Key      string           `json:"key"`
	ChiefID  contract.ID      `json:"chief_id"`
	ParentID contract.ID      `json:"parent_id,omitempty"`
}

type peerBinding struct {
	ID          contract.ID      `json:"id"`
	Version     contract.Version `json:"version"`
	Scope       contract.Scope   `json:"scope"`
	Kind        string           `json:"kind"`
	TargetID    contract.ID      `json:"target_id"`
	Permissions []string         `json:"permissions"`
}

type peerProject struct {
	ID             contract.ID `json:"id"`
	OrganizationID contract.ID `json:"organization_id"`
	Classification string      `json:"classification"`
}

// peerWorkerProfile carries the execution-profile fields qualification
// version derivation reads; decoded tolerantly from the workers snapshot.
type peerWorkerProfile struct {
	Model string `json:"model"`
}

type peerWorker struct {
	ID             contract.ID        `json:"id"`
	OrganizationID contract.ID        `json:"organization_id"`
	SkillVersions  []wireRef          `json:"skill_versions,omitempty"`
	Profile        *peerWorkerProfile `json:"profile,omitempty"`
}

type scopeSnapshot struct {
	Scope     contract.Scope `json:"scope"`
	Revision  int64          `json:"revision"`
	Ancestors []peerOrg      `json:"ancestors"`
	Bindings  []peerBinding  `json:"bindings"`
	Project   *peerProject   `json:"project,omitempty"`
	Worker    *peerWorker    `json:"worker,omitempty"`
}

type snapshotBody struct {
	Resource scopeSnapshot `json:"resource"`
}

type draftResource struct {
	Resource wireDraft `json:"resource"`
}

// Stored row shapes mirroring the policy_ tables.

type policyRow struct {
	ID         contract.ID
	Version    int64
	Scope      contract.Scope
	Rules      []wireRule
	Extensions json.RawMessage
	Archived   bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type promotionRuleRow struct {
	ID                     contract.ID
	Version                int64
	Scope                  contract.Scope
	Capability             string
	Destinations           []string
	RequiredEvidence       []string
	MinimumSuccesses       int64
	EvidenceWindowSeconds  int64
	DisqualifyingEvents    []string
	CeilingGrantID         contract.ID
	HumanRequiredPreserved bool
	Archived               bool
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type qualificationRow struct {
	ID            contract.ID
	Version       int64
	Scope         contract.Scope
	WorkerID      contract.ID
	Capability    string
	Destinations  []string
	RuleID        contract.ID
	RuleVersion   int64
	Model         string
	ToolVersions  []wireRef
	SkillVersions []wireRef
	EvidenceIDs   []contract.ID
	WindowStart   time.Time
	WindowEnd     time.Time
	State         string
	Explanation   string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type evidenceRow struct {
	QualificationID  contract.ID
	EvidenceID       contract.ID
	Kind             string
	FirstState       string
	FirstVersion     int64
	FirstAt          time.Time
	SucceededAt      *time.Time
	SucceededVersion *int64
}

// encodeStrings marshals a string list for a JSON text column.
func encodeStrings(v []string) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("policy: encode string list: %w", err)
	}
	return string(raw), nil
}

// decodeStrings parses a stored string list; NULL-free columns always hold
// canonical JSON arrays.
func decodeStrings(raw string) ([]string, error) {
	var v []string
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("policy: decode stored string list: %w", err)
	}
	if v == nil {
		v = []string{}
	}
	return v, nil
}

// encodeRefs marshals a Ref list for a JSON text column.
func encodeRefs(v []wireRef) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("policy: encode ref list: %w", err)
	}
	return string(raw), nil
}

// decodeRefs parses a stored Ref list.
func decodeRefs(raw string) ([]wireRef, error) {
	var v []wireRef
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("policy: decode stored ref list: %w", err)
	}
	if v == nil {
		v = []wireRef{}
	}
	return v, nil
}

// encodeIDs marshals an ID list for a JSON text column.
func encodeIDs(v []contract.ID) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("policy: encode id list: %w", err)
	}
	return string(raw), nil
}

// decodeIDs parses a stored ID list.
func decodeIDs(raw string) ([]contract.ID, error) {
	var v []contract.ID
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("policy: decode stored id list: %w", err)
	}
	if v == nil {
		v = []contract.ID{}
	}
	return v, nil
}

// encodeRules marshals a rule list for a JSON text column.
func encodeRules(v []wireRule) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("policy: encode rule list: %w", err)
	}
	return string(raw), nil
}

// decodeRules parses a stored rule list.
func decodeRules(raw string) ([]wireRule, error) {
	var v []wireRule
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("policy: decode stored rule list: %w", err)
	}
	if v == nil {
		v = []wireRule{}
	}
	return v, nil
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// Wire conversions.

func (r policyRow) wire() wirePolicy {
	return wirePolicy{
		ID:         r.ID,
		Version:    contract.Version(r.Version),
		Scope:      r.Scope,
		Rules:      r.Rules,
		Extensions: r.Extensions,
	}
}

func (r promotionRuleRow) wire() wirePromotionRule {
	return wirePromotionRule{
		ID:                     r.ID,
		Version:                contract.Version(r.Version),
		Scope:                  r.Scope,
		Capability:             r.Capability,
		Destinations:           r.Destinations,
		RequiredEvidence:       r.RequiredEvidence,
		MinimumSuccesses:       r.MinimumSuccesses,
		EvidenceWindowSeconds:  r.EvidenceWindowSeconds,
		DisqualifyingEvents:    r.DisqualifyingEvents,
		CeilingGrantID:         r.CeilingGrantID,
		HumanRequiredPreserved: r.HumanRequiredPreserved,
	}
}

func (r qualificationRow) wire() wireQualification {
	return wireQualification{
		ID:            r.ID,
		Version:       contract.Version(r.Version),
		WorkerID:      r.WorkerID,
		Capability:    r.Capability,
		Destinations:  r.Destinations,
		Rule:          wireRef{ID: r.RuleID, Version: contract.Version(r.RuleVersion)},
		Model:         r.Model,
		ToolVersions:  r.ToolVersions,
		SkillVersions: r.SkillVersions,
		EvidenceIDs:   r.EvidenceIDs,
		WindowStart:   r.WindowStart,
		WindowEnd:     r.WindowEnd,
		State:         r.State,
		Explanation:   r.Explanation,
	}
}
