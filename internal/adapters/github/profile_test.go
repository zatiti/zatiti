package github

import (
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestLoadProfile_Valid(t *testing.T) {
	raw := defaultProfileJSON(t)
	p, err := loadProfile(raw)
	if err != nil {
		t.Fatalf("loadProfile: %v", err)
	}
	if p.APIBase != "https://api.github.com" {
		t.Errorf("APIBase = %q", p.APIBase)
	}
	if !p.allowsRepository(testRepo()) {
		t.Errorf("expected repository to be allowed")
	}
	if p.allowsRepository(wireRepository{Owner: "other", Name: "repo"}) {
		t.Errorf("unexpected repository allowed")
	}
	if !p.allowsAction(kindReadRepository) {
		t.Errorf("expected read_repository to be allowed")
	}
	if p.allowsAction("delete_repository") {
		t.Errorf("unexpected action allowed")
	}
}

func TestLoadProfile_RejectsSchemaViolation(t *testing.T) {
	// max_response_bytes below the schema's minimum of 1.
	raw := []byte(`{"schema":"zatiti.github/v1","api_base":"https://api.github.com","allowed_repositories":[{"owner":"acme","name":"widgets"}],"allowed_actions":["read_repository"],"max_response_bytes":0,"timeout_seconds":30,"idempotency_profile":{"mode":"none","retention_seconds":0,"equivalence_fields":[],"evidence":[]},"automation_constraints":{"allowed_workflows":[],"allowed_deployment_environments":[],"allow_external_notifications":false,"allow_automatic_merge":false,"unknown_automation":"deny","evidence":[]},"capability_evidence":{"artifact":{"id":"11111111-1111-4111-8111-111111111111","digest":"0000000000000000000000000000000000000000000000000000000000000"},"adapter_version":"v","source_revision":"v","protocol_revision":"v","profile_digest":"0000000000000000000000000000000000000000000000000000000000000","qualified_at":"2026-01-01T00:00:00Z","capabilities":[],"limitations":[]}}`)
	_, err := loadProfile(raw)
	if err == nil {
		t.Fatal("expected schema validation error")
	}
	f := mustFault(t, err)
	if f.Code != contract.CodeInvalidInput {
		t.Errorf("code = %q, want invalid_input", f.Code)
	}
}

func TestLoadProfile_RejectsSelfReferentialCapabilityEvidence(t *testing.T) {
	raw := defaultProfileJSON(t)
	var w wireGitHubProfile
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Tamper with the profile after the digest was computed: the profile
	// digest bound into capability_evidence no longer matches.
	w.MaxResponseBytes = w.MaxResponseBytes + 1
	tampered, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = loadProfile(tampered)
	if err == nil {
		t.Fatal("expected capability_evidence digest mismatch to be rejected")
	}
	f := mustFault(t, err)
	if f.Code != contract.CodeInvalidInput {
		t.Errorf("code = %q, want invalid_input", f.Code)
	}
}

func TestAutomationWithinBounds(t *testing.T) {
	raw := buildProfileJSON(t, "https://api.github.com", []wireRepository{testRepo()},
		[]string{kindPushCommit}, restrictiveAutomation())
	p, err := loadProfile(raw)
	if err != nil {
		t.Fatalf("loadProfile: %v", err)
	}

	cases := []struct {
		name string
		a    wireAutomationConstraints
		ok   bool
	}{
		{"within bounds", restrictiveAutomation(), true},
		{"escalates automatic merge", wireAutomationConstraints{AllowAutomaticMerge: true, UnknownAutomation: "deny", AllowedWorkflows: []string{}, AllowedDeploymentEnvironments: []string{}, Evidence: []wireArtifactRef{}}, false},
		{"escalates notifications", wireAutomationConstraints{AllowExternalNotifications: true, UnknownAutomation: "deny", AllowedWorkflows: []string{}, AllowedDeploymentEnvironments: []string{}, Evidence: []wireArtifactRef{}}, false},
		{"names a workflow the profile forbids", wireAutomationConstraints{AllowedWorkflows: []string{"deploy.yml"}, UnknownAutomation: "deny", AllowedDeploymentEnvironments: []string{}, Evidence: []wireArtifactRef{}}, false},
		{"names an environment the profile forbids", wireAutomationConstraints{AllowedDeploymentEnvironments: []string{"prod"}, UnknownAutomation: "deny", AllowedWorkflows: []string{}, Evidence: []wireArtifactRef{}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := p.automationWithinBounds(tc.a)
			if tc.ok && err != nil {
				t.Errorf("expected within bounds, got error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Errorf("expected an out-of-bounds error, got nil")
			}
		})
	}
}
