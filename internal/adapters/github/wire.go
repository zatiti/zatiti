package github

import (
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs for the GitHub adapter's profile, action and evidence schemas.
// JSON is snake_case; unknown fields are rejected at the decode boundary.
// Schema validation (contract.ValidateSchema) runs before every strict
// decode below, so these shapes only need to agree with the schema on
// field names and types, not re-enforce bounds ValidateSchema already
// checked.

// wireRepository addresses one GitHub repository by owner and name.
type wireRepository struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

func (r wireRepository) slug() string { return r.Owner + "/" + r.Name }

// wireArtifactRef addresses one artifact by identity and content digest.
type wireArtifactRef struct {
	ID     contract.ID     `json:"id"`
	Digest contract.Digest `json:"digest"`
}

// wireAutomationConstraints bounds downstream automation a GitHub write may
// trigger (workflow runs, deployments, notifications, automatic merge).
type wireAutomationConstraints struct {
	AllowedWorkflows              []string          `json:"allowed_workflows"`
	AllowedDeploymentEnvironments []string          `json:"allowed_deployment_environments"`
	AllowExternalNotifications    bool              `json:"allow_external_notifications"`
	AllowAutomaticMerge           bool              `json:"allow_automatic_merge"`
	UnknownAutomation             string            `json:"unknown_automation"`
	Evidence                      []wireArtifactRef `json:"evidence"`
}

// wireGitHubIdempotencyProfile declares what safe-retry window, if any, the
// operator has qualified for this GitHub account. The adapter itself never
// retries a mutation; this profile is read-only context it carries through
// to evidence for the effects/execution layer to act on.
type wireGitHubIdempotencyProfile struct {
	Mode              string            `json:"mode"`
	RetentionSeconds  int64             `json:"retention_seconds"`
	EquivalenceFields []string          `json:"equivalence_fields"`
	Evidence          []wireArtifactRef `json:"evidence"`
}

// wireCapabilityEvidence records a qualification result: which adapter
// build, against which protocol revision, was qualified for a given profile
// digest and what it can/cannot do.
type wireCapabilityEvidence struct {
	Artifact         wireArtifactRef `json:"artifact"`
	AdapterVersion   string          `json:"adapter_version"`
	SourceRevision   string          `json:"source_revision"`
	ProtocolRevision string          `json:"protocol_revision"`
	ProfileDigest    contract.Digest `json:"profile_digest"`
	QualifiedAt      time.Time       `json:"qualified_at"`
	Capabilities     []string        `json:"capabilities"`
	Limitations      []string        `json:"limitations"`
}

// wireGitHubProfile is the decoded zatiti.github/v1 adapter profile.
type wireGitHubProfile struct {
	Schema                string                       `json:"schema"`
	APIBase               string                       `json:"api_base"`
	AllowedRepositories   []wireRepository             `json:"allowed_repositories"`
	AllowedActions        []string                     `json:"allowed_actions"`
	MaxResponseBytes      int64                        `json:"max_response_bytes"`
	TimeoutSeconds        int64                        `json:"timeout_seconds"`
	IdempotencyProfile    wireGitHubIdempotencyProfile `json:"idempotency_profile"`
	AutomationConstraints wireAutomationConstraints    `json:"automation_constraints"`
	CapabilityEvidence    wireCapabilityEvidence       `json:"capability_evidence"`
}

// ---------- action (Dispatch.Action) bodies, kind-discriminated ----------

type wireReadRepository struct {
	Schema                  string         `json:"schema"`
	Repository              wireRepository `json:"repository"`
	Kind                    string         `json:"kind"`
	Resource                string         `json:"resource"`
	Branch                  string         `json:"branch,omitempty"`
	SHA                     string         `json:"sha,omitempty"`
	Path                    string         `json:"path,omitempty"`
	PullRequestNumber       int64          `json:"pull_request_number,omitempty"`
	PreflightForOperationID contract.ID    `json:"preflight_for_operation_id,omitempty"`
}

type wireCreateBranch struct {
	Schema                string                    `json:"schema"`
	Repository            wireRepository            `json:"repository"`
	Kind                  string                    `json:"kind"`
	Branch                string                    `json:"branch"`
	BaseSHA               string                    `json:"base_sha"`
	ExpectedAbsent        string                    `json:"expected_absent"`
	AutomationConstraints wireAutomationConstraints `json:"automation_constraints"`
	PreflightEvidence     []wireArtifactRef         `json:"preflight_evidence"`
}

type wirePushCommit struct {
	Schema                string                    `json:"schema"`
	Repository            wireRepository            `json:"repository"`
	Kind                  string                    `json:"kind"`
	Branch                string                    `json:"branch"`
	ExpectedHeadSHA       string                    `json:"expected_head_sha"`
	PreparedCommitSHA     string                    `json:"prepared_commit_sha"`
	BaseSHA               string                    `json:"base_sha"`
	Patch                 wireArtifactRef           `json:"patch"`
	ContentArtifacts      []wireArtifactRef         `json:"content_artifacts"`
	Force                 bool                      `json:"force"`
	AutomationConstraints wireAutomationConstraints `json:"automation_constraints"`
	PreflightEvidence     []wireArtifactRef         `json:"preflight_evidence"`
}

type wireOpenPullRequest struct {
	Schema                string                    `json:"schema"`
	Repository            wireRepository            `json:"repository"`
	Kind                  string                    `json:"kind"`
	HeadBranch            string                    `json:"head_branch"`
	HeadSHA               string                    `json:"head_sha"`
	BaseBranch            string                    `json:"base_branch"`
	BaseSHA               string                    `json:"base_sha"`
	Title                 wireArtifactRef           `json:"title"`
	Body                  wireArtifactRef           `json:"body"`
	Patch                 wireArtifactRef           `json:"patch"`
	Draft                 bool                      `json:"draft"`
	AutomationConstraints wireAutomationConstraints `json:"automation_constraints"`
	PreflightEvidence     []wireArtifactRef         `json:"preflight_evidence"`
}

type wireMergePullRequest struct {
	Schema                string                    `json:"schema"`
	Repository            wireRepository            `json:"repository"`
	Kind                  string                    `json:"kind"`
	PullRequestNumber     int64                     `json:"pull_request_number"`
	ExpectedHeadSHA       string                    `json:"expected_head_sha"`
	ExpectedBaseSHA       string                    `json:"expected_base_sha"`
	MergeMethod           string                    `json:"merge_method"`
	CommitTitle           wireArtifactRef           `json:"commit_title"`
	CommitBody            wireArtifactRef           `json:"commit_body"`
	AutomationConstraints wireAutomationConstraints `json:"automation_constraints"`
	PreflightEvidence     []wireArtifactRef         `json:"preflight_evidence"`
}

// ---------- evidence (Observation.Evidence) body ----------

type wirePhysicalCallEvidence struct {
	OperationID          contract.ID       `json:"operation_id"`
	AttemptID            contract.ID       `json:"attempt_id"`
	AccountIdentity      string            `json:"account_identity"`
	RequestedDestination string            `json:"requested_destination"`
	ResolvedDestination  string            `json:"resolved_destination"`
	ProfileDigest        contract.Digest   `json:"profile_digest"`
	CapabilityEvidence   wireArtifactRef   `json:"capability_evidence"`
	StartedAt            time.Time         `json:"started_at"`
	FinishedAt           time.Time         `json:"finished_at"`
	RequestContext       wireStagedLocator `json:"request_context"`
	RequestSent          string            `json:"request_sent"`
	Confirmation         string            `json:"confirmation"`
	HTTPStatus           int64             `json:"http_status,omitempty"`
	ProviderReference    string            `json:"provider_reference,omitempty"`
	ErrorCode            string            `json:"error_code,omitempty"`
	ErrorMessage         string            `json:"error_message,omitempty"`
}

type wireRationalRate struct {
	NumeratorMicroUnits int64  `json:"numerator_micro_units"`
	DenominatorUnits    int64  `json:"denominator_units"`
	Unit                string `json:"unit"`
}

type wireUsage struct {
	Currency  string `json:"currency"`
	Spent     int64  `json:"spent"`
	Reserved  int64  `json:"reserved"`
	Estimated int64  `json:"estimated"`
	Unknown   int64  `json:"unknown"`
	Advisory  bool   `json:"advisory"`
}

type wireProviderUsage struct {
	Accounting             wireUsage         `json:"accounting"`
	Billing                string            `json:"billing"`
	InputTokens            *int64            `json:"input_tokens,omitempty"`
	OutputTokens           *int64            `json:"output_tokens,omitempty"`
	InputRate              *wireRationalRate `json:"input_rate,omitempty"`
	OutputRate             *wireRationalRate `json:"output_rate,omitempty"`
	ProviderUsageReference string            `json:"provider_usage_reference,omitempty"`
}

type wireStagedOutput struct {
	StagingRef     string          `json:"staging_ref"`
	Digest         contract.Digest `json:"digest"`
	Size           int64           `json:"size"`
	MediaType      string          `json:"media_type"`
	Classification string          `json:"classification"`
	Purpose        string          `json:"purpose"`
}

// wireStagedLocator is the "staged" variant of ArtifactLocator, the only
// variant this adapter returns: it cannot mint artifact IDs, and the
// controller substitutes the artifact variant after publication.
type wireStagedLocator struct {
	Kind       string          `json:"kind"`
	StagingRef string          `json:"staging_ref"`
	Digest     contract.Digest `json:"digest"`
}

type wireGitHubEvidence struct {
	Schema             string                   `json:"schema"`
	PhysicalCall       wirePhysicalCallEvidence `json:"physical_call"`
	Kind               string                   `json:"kind"`
	Repository         wireRepository           `json:"repository"`
	Usage              wireProviderUsage        `json:"usage"`
	StagedOutputs      []wireStagedOutput       `json:"staged_outputs"`
	OutputArtifacts    []wireArtifactRef        `json:"output_artifacts"`
	Branch             string                   `json:"branch,omitempty"`
	ObservedHeadSHA    string                   `json:"observed_head_sha,omitempty"`
	ObservedBaseSHA    string                   `json:"observed_base_sha,omitempty"`
	CommitSHA          string                   `json:"commit_sha,omitempty"`
	PullRequestNumber  int64                    `json:"pull_request_number,omitempty"`
	PullRequestURL     string                   `json:"pull_request_url,omitempty"`
	AutomationEvidence []wireArtifactRef        `json:"automation_evidence,omitempty"`
}
