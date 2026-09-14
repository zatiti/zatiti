package github

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// githubProfile is the decoded, validated zatiti.github/v1 adapter profile,
// with repository/action allowlists indexed for O(1) lookup at Invoke time.
type githubProfile struct {
	APIBase             string
	AllowedRepositories map[string]bool // "owner/name"
	AllowedActions      map[string]bool
	MaxResponseBytes    int64
	Timeout             time.Duration
	Automation          wireAutomationConstraints
	CapabilityEvidence  wireCapabilityEvidence
	Idempotency         wireGitHubIdempotencyProfile
	Digest              contract.Digest
}

// loadProfile validates raw against the zatiti.github/v1 schema, strict
// decodes it, and checks that capability_evidence.profile_digest binds this
// exact profile: the digest of the canonical profile with the
// capability_evidence field itself omitted. A profile whose evidence does
// not bind to its own bytes is self-referential and grants no authority.
func loadProfile(raw json.RawMessage) (*githubProfile, error) {
	schema, err := profileSchema()
	if err != nil {
		return nil, internalError("github profile schema composition failed: %v", err)
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return nil, invalidInput("github profile does not match the zatiti.github/v1 schema: %v", err)
	}
	var w wireGitHubProfile
	if err := contract.DecodeStrict(raw, &w); err != nil {
		return nil, invalidInput("github profile decode failed: %v", err)
	}

	digest, err := profileDigestWithoutCapabilityEvidence(raw)
	if err != nil {
		return nil, invalidInput("github profile canonicalization failed: %v", err)
	}
	if digest != w.CapabilityEvidence.ProfileDigest {
		return nil, invalidInput(
			"github profile capability_evidence.profile_digest %q does not bind this exact profile (computed %q); self-referential capability evidence grants no authority",
			w.CapabilityEvidence.ProfileDigest, digest)
	}

	repos := make(map[string]bool, len(w.AllowedRepositories))
	for _, r := range w.AllowedRepositories {
		repos[r.slug()] = true
	}
	actions := make(map[string]bool, len(w.AllowedActions))
	for _, a := range w.AllowedActions {
		actions[a] = true
	}

	return &githubProfile{
		APIBase:             w.APIBase,
		AllowedRepositories: repos,
		AllowedActions:      actions,
		MaxResponseBytes:    w.MaxResponseBytes,
		Timeout:             time.Duration(w.TimeoutSeconds) * time.Second,
		Automation:          w.AutomationConstraints,
		CapabilityEvidence:  w.CapabilityEvidence,
		Idempotency:         w.IdempotencyProfile,
		Digest:              digest,
	}, nil
}

// profileDigestWithoutCapabilityEvidence computes the canonical-JSON SHA-256
// digest of raw with its top-level capability_evidence field removed.
func profileDigestWithoutCapabilityEvidence(raw json.RawMessage) (contract.Digest, error) {
	var doc map[string]json.RawMessage
	if err := contract.DecodeStrict(raw, &doc); err != nil {
		return "", err
	}
	delete(doc, "capability_evidence")
	stripped, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshal stripped profile: %w", err)
	}
	canon, err := contract.Canonicalize(stripped)
	if err != nil {
		return "", fmt.Errorf("canonicalize stripped profile: %w", err)
	}
	return contract.Hash(canon), nil
}

func (p *githubProfile) allowsRepository(r wireRepository) bool {
	return p.AllowedRepositories[r.slug()]
}

func (p *githubProfile) allowsAction(kind string) bool {
	return p.AllowedActions[kind]
}

// automationWithinBounds reports an error unless a's declared automation
// constraints stay within the profile's configured ceiling: an action
// cannot claim a broader automatic-merge/notification license, or name a
// workflow/deployment environment the profile does not also permit.
func (p *githubProfile) automationWithinBounds(a wireAutomationConstraints) error {
	if a.AllowAutomaticMerge && !p.Automation.AllowAutomaticMerge {
		return fmt.Errorf("action requests allow_automatic_merge but the profile does not permit it")
	}
	if a.AllowExternalNotifications && !p.Automation.AllowExternalNotifications {
		return fmt.Errorf("action requests allow_external_notifications but the profile does not permit it")
	}
	allowedWorkflows := toSet(p.Automation.AllowedWorkflows)
	for _, w := range a.AllowedWorkflows {
		if !allowedWorkflows[w] {
			return fmt.Errorf("action allowed_workflows includes %q, which the profile does not permit", w)
		}
	}
	allowedEnvs := toSet(p.Automation.AllowedDeploymentEnvironments)
	for _, e := range a.AllowedDeploymentEnvironments {
		if !allowedEnvs[e] {
			return fmt.Errorf("action allowed_deployment_environments includes %q, which the profile does not permit", e)
		}
	}
	return nil
}

func toSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, it := range items {
		s[it] = true
	}
	return s
}
