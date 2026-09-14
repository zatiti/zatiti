package github

import (
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func validSHA() string { return "0123456789abcdef0123456789abcdef01234567" }

func artifactRef(t *testing.T, blobs *fakeBlobStore, content string) wireArtifactRef {
	t.Helper()
	d := blobs.put([]byte(content))
	return wireArtifactRef{ID: contract.NewID(), Digest: d}
}

func TestDecodeAction_ReadRepositoryResourceSelectors(t *testing.T) {
	base := func() wireReadRepository {
		return wireReadRepository{Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindReadRepository}
	}

	cases := []struct {
		name  string
		build func() wireReadRepository
		ok    bool
	}{
		{"metadata with no selector", func() wireReadRepository { r := base(); r.Resource = "metadata"; return r }, true},
		{"metadata with stray sha", func() wireReadRepository { r := base(); r.Resource = "metadata"; r.SHA = validSHA(); return r }, false},
		{"ref with branch", func() wireReadRepository { r := base(); r.Resource = "ref"; r.Branch = "main"; return r }, true},
		{"ref without branch", func() wireReadRepository { r := base(); r.Resource = "ref"; return r }, false},
		{"ref with sha instead of branch", func() wireReadRepository { r := base(); r.Resource = "ref"; r.SHA = validSHA(); return r }, false},
		{"commit with sha", func() wireReadRepository { r := base(); r.Resource = "commit"; r.SHA = validSHA(); return r }, true},
		{"commit without sha", func() wireReadRepository { r := base(); r.Resource = "commit"; return r }, false},
		{"tree with sha", func() wireReadRepository { r := base(); r.Resource = "tree"; r.SHA = validSHA(); return r }, true},
		{"blob with sha", func() wireReadRepository { r := base(); r.Resource = "blob"; r.SHA = validSHA(); return r }, true},
		{"pull_request with number", func() wireReadRepository { r := base(); r.Resource = "pull_request"; r.PullRequestNumber = 7; return r }, true},
		{"pull_request without number", func() wireReadRepository { r := base(); r.Resource = "pull_request"; return r }, false},
		{"path is never used in v1", func() wireReadRepository { r := base(); r.Resource = "metadata"; r.Path = "a/b"; return r }, false},
		{"invalid branch syntax", func() wireReadRepository { r := base(); r.Resource = "ref"; r.Branch = "bad..branch"; return r }, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.build())
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			_, err = decodeAction(raw)
			if tc.ok && err != nil {
				t.Errorf("expected success, got error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Errorf("expected an error, got nil")
			}
		})
	}
}

func TestDecodeAction_AllFiveKinds(t *testing.T) {
	blobs := newFakeBlobStore()
	title := artifactRef(t, blobs, "title text")
	body := artifactRef(t, blobs, "body text")
	patch := artifactRef(t, blobs, "patch text")
	preflight := []wireArtifactRef{artifactRef(t, blobs, "preflight evidence")}

	cases := []struct {
		name string
		v    any
		kind string
	}{
		{"read_repository", wireReadRepository{Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindReadRepository, Resource: "metadata"}, kindReadRepository},
		{"create_branch", wireCreateBranch{
			Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindCreateBranch,
			Branch: "feature/x", BaseSHA: validSHA(), ExpectedAbsent: "required",
			AutomationConstraints: restrictiveAutomation(), PreflightEvidence: preflight,
		}, kindCreateBranch},
		{"push_commit", wirePushCommit{
			Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindPushCommit,
			Branch: "feature/x", ExpectedHeadSHA: validSHA(), PreparedCommitSHA: validSHA(), BaseSHA: validSHA(),
			Patch: patch, ContentArtifacts: []wireArtifactRef{}, Force: false,
			AutomationConstraints: restrictiveAutomation(), PreflightEvidence: preflight,
		}, kindPushCommit},
		{"open_pull_request", wireOpenPullRequest{
			Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindOpenPullRequest,
			HeadBranch: "feature/x", HeadSHA: validSHA(), BaseBranch: "main", BaseSHA: validSHA(),
			Title: title, Body: body, Patch: patch, Draft: false,
			AutomationConstraints: restrictiveAutomation(), PreflightEvidence: preflight,
		}, kindOpenPullRequest},
		{"merge_pull_request", wireMergePullRequest{
			Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindMergePullRequest,
			PullRequestNumber: 3, ExpectedHeadSHA: validSHA(), ExpectedBaseSHA: validSHA(), MergeMethod: "squash",
			CommitTitle: title, CommitBody: body,
			AutomationConstraints: restrictiveAutomation(), PreflightEvidence: preflight,
		}, kindMergePullRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			act, err := decodeAction(raw)
			if err != nil {
				t.Fatalf("decodeAction: %v", err)
			}
			if act.Kind != tc.kind {
				t.Errorf("Kind = %q, want %q", act.Kind, tc.kind)
			}
			if act.Repository != testRepo() {
				t.Errorf("Repository = %+v", act.Repository)
			}
		})
	}
}

func TestDecodeAction_RejectsUnknownFields(t *testing.T) {
	raw := []byte(`{"schema":"zatiti.github.action/v1","repository":{"owner":"acme","name":"widgets"},"kind":"read_repository","resource":"metadata","unexpected_field":true}`)
	if _, err := decodeAction(raw); err == nil {
		t.Fatal("expected an error for an unknown field")
	}
}

func TestDecodeAction_RejectsPushCommitForce(t *testing.T) {
	raw := []byte(`{"schema":"zatiti.github.action/v1","repository":{"owner":"acme","name":"widgets"},"kind":"push_commit","branch":"main","expected_head_sha":"` + validSHA() + `","prepared_commit_sha":"` + validSHA() + `","base_sha":"` + validSHA() + `","patch":{"id":"11111111-1111-4111-8111-111111111111","digest":"` + string(contract.Hash([]byte("x"))) + `"},"content_artifacts":[],"force":true,"automation_constraints":{"allowed_workflows":[],"allowed_deployment_environments":[],"allow_external_notifications":false,"allow_automatic_merge":false,"unknown_automation":"deny","evidence":[]},"preflight_evidence":[{"id":"11111111-1111-4111-8111-111111111111","digest":"` + string(contract.Hash([]byte("x"))) + `"}]}`)
	if _, err := decodeAction(raw); err == nil {
		t.Fatal("expected force:true to be rejected by the frozen schema's const constraint")
	}
}
