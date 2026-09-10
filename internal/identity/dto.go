package identity

import (
	"encoding/json"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs mirror the brief's embedded schema definitions. JSON integer is
// int64, UUID is contract.ID, timestamps are time.Time, optional scalars are
// pointers, arrays are slices, and unknown fields are rejected on input.

// principalOut is the Principal resource shape.
type principalOut struct {
	ID      contract.ID      `json:"id"`
	Version contract.Version `json:"version"`
	Kind    string           `json:"kind"`
	Name    string           `json:"name"`
	Scope   contract.Scope   `json:"scope"`
	Revoked bool             `json:"revoked"`
}

// grantOut is the Grant resource shape.
type grantOut struct {
	ID            contract.ID      `json:"id"`
	Version       contract.Version `json:"version"`
	PrincipalID   contract.ID      `json:"principal_id"`
	Scope         contract.Scope   `json:"scope"`
	Capabilities  []string         `json:"capabilities"`
	Destinations  []string         `json:"destinations"`
	Denied        bool             `json:"denied"`
	ExpiresAt     *time.Time       `json:"expires_at,omitempty"`
	ParentGrantID *contract.ID     `json:"parent_grant_id,omitempty"`
}

// credentialOut is the Credential resource shape. It carries only the opaque
// store reference and permitted metadata; token digests and raw bytes are
// never returned.
type credentialOut struct {
	ID          contract.ID      `json:"id"`
	Version     contract.Version `json:"version"`
	PrincipalID contract.ID      `json:"principal_id"`
	StoreRef    string           `json:"store_ref"`
	Revoked     bool             `json:"revoked"`
	ExpiresAt   *time.Time       `json:"expires_at,omitempty"`
}

// authorityOut is the Authority resource shape.
type authorityOut struct {
	Principal    principalOut `json:"principal"`
	Grants       []grantOut   `json:"grants"`
	Restrictions []string     `json:"restrictions"`
}

// dispositionOut is the Disposition resource shape.
type dispositionOut struct {
	ID      contract.ID      `json:"id"`
	Version contract.Version `json:"version"`
	State   string           `json:"state"`
}

// refOut is the Ref shape (id plus version).
type refOut struct {
	ID      contract.ID      `json:"id"`
	Version contract.Version `json:"version"`
}

// diagnosticOut is the Diagnostic shape.
type diagnosticOut struct {
	Path     string `json:"path"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

// requirementOut is the Requirement shape.
type requirementOut struct {
	Code        string       `json:"code"`
	Message     string       `json:"message"`
	ResourceID  *contract.ID `json:"resource_id,omitempty"`
	ChallengeID *contract.ID `json:"challenge_id,omitempty"`
}

// validationOut is the Validation shape.
type validationOut struct {
	Diagnostics  []diagnosticOut  `json:"diagnostics"`
	Requirements []requirementOut `json:"requirements"`
	Dependencies []refOut         `json:"dependencies"`
}

// qualification is the Qualification shape bound into promotion activations.
type qualification struct {
	ID            contract.ID      `json:"id"`
	Version       contract.Version `json:"version"`
	WorkerID      contract.ID      `json:"worker_id"`
	Capability    string           `json:"capability"`
	Destinations  []string         `json:"destinations"`
	Rule          refOut           `json:"rule"`
	Model         string           `json:"model"`
	ToolVersions  []refOut         `json:"tool_versions"`
	SkillVersions []refOut         `json:"skill_versions"`
	EvidenceIDs   []contract.ID    `json:"evidence_ids"`
	WindowStart   time.Time        `json:"window_start"`
	WindowEnd     time.Time        `json:"window_end"`
	State         string           `json:"state"`
	Explanation   string           `json:"explanation"`
}

// Input DTOs. Strict decoding plus schema validation precede every handler.

type principalDefinition struct {
	Kind    string         `json:"kind"`
	Name    string         `json:"name"`
	Scope   contract.Scope `json:"scope"`
	Revoked bool           `json:"revoked"`
}

type principalCreateInput struct {
	Scope      contract.Scope      `json:"scope"`
	Definition principalDefinition `json:"definition"`
}

type principalGetInput struct {
	Scope contract.Scope `json:"scope"`
	ID    contract.ID    `json:"id"`
}

type principalUpdateInput struct {
	Scope           contract.Scope      `json:"scope"`
	ID              contract.ID         `json:"id"`
	ExpectedVersion contract.Version    `json:"expected_version"`
	Definition      principalDefinition `json:"definition"`
}

type principalRevokeInput struct {
	Scope           contract.Scope   `json:"scope"`
	ID              contract.ID      `json:"id"`
	ExpectedVersion contract.Version `json:"expected_version"`
}

type grantDefinition struct {
	PrincipalID   contract.ID    `json:"principal_id"`
	Scope         contract.Scope `json:"scope"`
	Capabilities  []string       `json:"capabilities"`
	Destinations  []string       `json:"destinations"`
	Denied        bool           `json:"denied"`
	ExpiresAt     *time.Time     `json:"expires_at,omitempty"`
	ParentGrantID *contract.ID   `json:"parent_grant_id,omitempty"`
}

type grantCreateInput struct {
	Scope      contract.Scope  `json:"scope"`
	Definition grantDefinition `json:"definition"`
}

type grantGetInput struct {
	Scope contract.Scope `json:"scope"`
	ID    contract.ID    `json:"id"`
}

type grantUpdateInput struct {
	Scope           contract.Scope   `json:"scope"`
	ID              contract.ID      `json:"id"`
	ExpectedVersion contract.Version `json:"expected_version"`
	Definition      grantDefinition  `json:"definition"`
}

type grantRevokeInput struct {
	Scope           contract.Scope   `json:"scope"`
	ID              contract.ID      `json:"id"`
	ExpectedVersion contract.Version `json:"expected_version"`
}

type credentialProvisionInput struct {
	Scope       contract.Scope `json:"scope"`
	PrincipalID contract.ID    `json:"principal_id"`
	StoreRef    string         `json:"store_ref"`
	ExpiresAt   *time.Time     `json:"expires_at,omitempty"`
}

type credentialRevokeInput struct {
	Scope           contract.Scope   `json:"scope"`
	ID              contract.ID      `json:"id"`
	ExpectedVersion contract.Version `json:"expected_version"`
}

// listFilter carries the shared structured list filter. Only a resource's
// supported fields may be set; anything else is invalid_input.
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

// Internal operation inputs.

type candidateInput struct {
	Candidate candidate `json:"candidate"`
}

type authorityInput struct {
	PrincipalID contract.ID    `json:"principal_id"`
	Scope       contract.Scope `json:"scope"`
}

type bootstrapInput struct {
	OwnerID        contract.ID `json:"owner_id"`
	CredentialID   contract.ID `json:"credential_id"`
	StoreRef       string      `json:"store_ref"`
	Name           string      `json:"name"`
	InstallationID contract.ID `json:"installation_id"`
}

type promoteInput struct {
	PrincipalID    contract.ID   `json:"principal_id"`
	Qualification  qualification `json:"qualification"`
	CeilingGrantID contract.ID   `json:"ceiling_grant_id"`
}

type restrictInput struct {
	PrincipalID contract.ID `json:"principal_id"`
	Capability  string      `json:"capability"`
	Reason      string      `json:"reason"`
}

// candidate is the shared compiler candidate envelope. Changes carry
// definition payloads for kinds owned by other packages; identity performs
// no live changes for them and owns no compiler-draftable kind.
type candidate struct {
	PlanID          contract.ID       `json:"plan_id"`
	BaseRevision    contract.Version  `json:"base_revision"`
	CandidateDigest string            `json:"candidate_digest"`
	Changes         []json.RawMessage `json:"changes"`
	Dependencies    []refOut          `json:"dependencies"`
}
