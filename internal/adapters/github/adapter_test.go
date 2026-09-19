package github

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

func validateEvidence(t *testing.T, doc json.RawMessage) {
	t.Helper()
	schema, err := evidenceSchema()
	if err != nil {
		t.Fatalf("evidenceSchema: %v", err)
	}
	if err := contract.ValidateSchema(schema, doc); err != nil {
		t.Errorf("evidence does not validate against zatiti.github.evidence/v1: %v\ndoc: %s", err, doc)
	}
	// Revision 2: request_context is a staged locator matching exactly one
	// StagedOutput of purpose context, for every disposition.
	var ev wireGitHubEvidence
	if err := json.Unmarshal(doc, &ev); err != nil {
		t.Fatalf("unmarshal evidence: %v", err)
	}
	rc := ev.PhysicalCall.RequestContext
	matches, contexts := 0, 0
	for _, s := range ev.StagedOutputs {
		if s.Purpose == "context" {
			contexts++
			if s.StagingRef == rc.StagingRef && s.Digest == rc.Digest {
				matches++
			}
		}
	}
	if rc.Kind != "staged" || matches != 1 || contexts != 1 {
		t.Errorf("request_context %+v matches %d of %d context staged outputs, want a staged locator matching exactly 1 of 1", rc, matches, contexts)
	}
}

func TestNew_RequiresDependencies(t *testing.T) {
	profile := defaultProfileJSON(t)
	if _, err := New(contract.AdapterDependencies{Clock: newFakeClock()}, profile); err == nil {
		t.Error("expected an error when HTTP is nil")
	}
	if _, err := New(contract.AdapterDependencies{HTTP: &http.Client{}}, profile); err == nil {
		t.Error("expected an error when Clock is nil")
	}
	if _, err := New(contract.AdapterDependencies{HTTP: &http.Client{}, Clock: newFakeClock()}, []byte(`{}`)); err == nil {
		t.Error("expected an error for an invalid profile")
	}
}

func TestName(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP call")
		return nil, nil
	}), newFakeBlobStore(), defaultProfileJSON(t))
	if a.Name() != "github" {
		t.Errorf("Name() = %q", a.Name())
	}
}

func TestContract_ReturnsThreeSchemas(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP call")
		return nil, nil
	}), newFakeBlobStore(), defaultProfileJSON(t))
	var doc struct {
		Schema           string          `json:"schema"`
		ProfileSchema    json.RawMessage `json:"profile_schema"`
		ParametersSchema json.RawMessage `json:"parameters_schema"`
		EvidenceSchema   json.RawMessage `json:"evidence_schema"`
	}
	if err := json.Unmarshal(a.Contract(), &doc); err != nil {
		t.Fatalf("unmarshal Contract(): %v", err)
	}
	if doc.Schema != "zatiti.github.contract/v1" {
		t.Errorf("schema = %q", doc.Schema)
	}
	if len(doc.ProfileSchema) == 0 || len(doc.ParametersSchema) == 0 || len(doc.EvidenceSchema) == 0 {
		t.Error("expected all three sub-schemas to be populated")
	}
}

func TestInvoke_RejectsWrongAdapter(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP call")
		return nil, nil
	}), newFakeBlobStore(), defaultProfileJSON(t))
	d := testDispatch(t, wireReadRepository{Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindReadRepository, Resource: "metadata"})
	d.Adapter = "not-github"
	if _, err := a.Invoke(context.Background(), d); err == nil {
		t.Fatal("expected an error for a mismatched adapter name")
	}
}

func TestInvoke_ReadRepositoryMetadata_Success(t *testing.T) {
	transport := &countingTransport{fn: func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.String() != "https://api.github.com/repos/acme/widgets" {
			t.Errorf("url = %s", r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q", got)
		}
		return jsonResponse(200, `{"full_name":"acme/widgets","default_branch":"main"}`), nil
	}}
	a := newTestAdapter(t, transport, newFakeBlobStore(), defaultProfileJSON(t))

	d := testDispatch(t, wireReadRepository{Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindReadRepository, Resource: "metadata"})
	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Errorf("Disposition = %q", obs.Disposition)
	}
	if obs.ConfirmedAt == nil {
		t.Error("expected ConfirmedAt to be set")
	}
	if transport.count() != 1 {
		t.Errorf("expected exactly one physical call, got %d", transport.count())
	}
	validateEvidence(t, obs.Evidence)
}

func TestInvoke_ReadRepository_RefAndCommitAndBlob(t *testing.T) {
	cases := []struct {
		name     string
		resource string
		branch   string
		sha      string
		wantPath string
	}{
		{"ref", "ref", "main", "", "/repos/acme/widgets/git/ref/heads/main"},
		{"commit", "commit", "", validSHA(), "/repos/acme/widgets/git/commits/" + validSHA()},
		{"tree", "tree", "", validSHA(), "/repos/acme/widgets/git/trees/" + validSHA()},
		{"blob", "blob", "", validSHA(), "/repos/acme/widgets/git/blobs/" + validSHA()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := &countingTransport{fn: func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != tc.wantPath {
					t.Errorf("path = %s, want %s", r.URL.Path, tc.wantPath)
				}
				return jsonResponse(200, `{"sha":"`+validSHA()+`","object":{"sha":"`+validSHA()+`"}}`), nil
			}}
			a := newTestAdapter(t, transport, newFakeBlobStore(), defaultProfileJSON(t))
			d := testDispatch(t, wireReadRepository{
				Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindReadRepository,
				Resource: tc.resource, Branch: tc.branch, SHA: tc.sha,
			})
			obs, err := a.Invoke(context.Background(), d)
			if err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			if obs.Disposition != contract.DispositionSucceeded {
				t.Errorf("Disposition = %q", obs.Disposition)
			}
			validateEvidence(t, obs.Evidence)
		})
	}
}

func TestInvoke_PermissionDenied_RepositoryNotAllowed(t *testing.T) {
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP call")
		return nil, nil
	}), newFakeBlobStore(), defaultProfileJSON(t))
	d := testDispatch(t, wireReadRepository{
		Schema: "zatiti.github.action/v1", Repository: wireRepository{Owner: "other", Name: "repo"},
		Kind: kindReadRepository, Resource: "metadata",
	})
	_, err := a.Invoke(context.Background(), d)
	f := mustFault(t, err)
	if f.Code != contract.CodePermissionDenied {
		t.Errorf("code = %q, want permission_denied", f.Code)
	}
}

func TestInvoke_CapabilityUnsupported_ActionNotAllowed(t *testing.T) {
	profile := buildProfileJSON(t, "https://api.github.com", []wireRepository{testRepo()}, []string{kindReadRepository}, permissiveAutomation())
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP call")
		return nil, nil
	}), newFakeBlobStore(), profile)
	blobs := newFakeBlobStore()
	d := testDispatch(t, wireCreateBranch{
		Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindCreateBranch,
		Branch: "feature/x", BaseSHA: validSHA(), ExpectedAbsent: "required",
		AutomationConstraints: restrictiveAutomation(),
		PreflightEvidence:     []wireArtifactRef{artifactRef(t, blobs, "e")},
	})
	_, err := a.Invoke(context.Background(), d)
	f := mustFault(t, err)
	if f.Code != contract.CodeCapabilityUnsupported {
		t.Errorf("code = %q, want capability_unsupported", f.Code)
	}
}

func TestInvoke_PermissionDenied_AutomationBoundViolation(t *testing.T) {
	profile := buildProfileJSON(t, "https://api.github.com", []wireRepository{testRepo()}, []string{kindMergePullRequest}, restrictiveAutomation())
	blobs := newFakeBlobStore()
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP call")
		return nil, nil
	}), blobs, profile)

	escalating := permissiveAutomation() // allow_automatic_merge:true, but profile is restrictive
	d := testDispatch(t, wireMergePullRequest{
		Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindMergePullRequest,
		PullRequestNumber: 1, ExpectedHeadSHA: validSHA(), ExpectedBaseSHA: validSHA(), MergeMethod: "squash",
		CommitTitle: artifactRef(t, blobs, "title"), CommitBody: artifactRef(t, blobs, "body"),
		AutomationConstraints: escalating,
		PreflightEvidence:     []wireArtifactRef{artifactRef(t, blobs, "e")},
	})
	_, err := a.Invoke(context.Background(), d)
	f := mustFault(t, err)
	if f.Code != contract.CodePermissionDenied {
		t.Errorf("code = %q, want permission_denied", f.Code)
	}
}

func TestInvoke_CreateBranch_Success(t *testing.T) {
	blobs := newFakeBlobStore()
	transport := &countingTransport{fn: func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/repos/acme/widgets/git/refs" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body ghCreateRefBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Ref != "refs/heads/feature/x" || body.SHA != validSHA() {
			t.Errorf("body = %+v", body)
		}
		return jsonResponse(201, `{"ref":"refs/heads/feature/x","object":{"sha":"`+validSHA()+`"}}`), nil
	}}
	a := newTestAdapter(t, transport, blobs, defaultProfileJSON(t))
	d := testDispatch(t, wireCreateBranch{
		Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindCreateBranch,
		Branch: "feature/x", BaseSHA: validSHA(), ExpectedAbsent: "required",
		AutomationConstraints: permissiveAutomation(),
		PreflightEvidence:     []wireArtifactRef{artifactRef(t, blobs, "e")},
	})
	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Errorf("Disposition = %q", obs.Disposition)
	}
	validateEvidence(t, obs.Evidence)
}

func TestInvoke_PushCommit_Success(t *testing.T) {
	blobs := newFakeBlobStore()
	transport := &countingTransport{fn: func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %s, want PATCH", r.Method)
		}
		if r.URL.Path != "/repos/acme/widgets/git/refs/heads/feature/x" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body ghUpdateRefBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body.Force {
			t.Error("force must always be false")
		}
		return jsonResponse(200, `{"ref":"refs/heads/feature/x","object":{"sha":"`+body.SHA+`"}}`), nil
	}}
	a := newTestAdapter(t, transport, blobs, defaultProfileJSON(t))
	d := testDispatch(t, wirePushCommit{
		Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindPushCommit,
		Branch: "feature/x", ExpectedHeadSHA: validSHA(), PreparedCommitSHA: validSHA(), BaseSHA: validSHA(),
		Patch: artifactRef(t, blobs, "patch"), ContentArtifacts: []wireArtifactRef{}, Force: false,
		AutomationConstraints: permissiveAutomation(),
		PreflightEvidence:     []wireArtifactRef{artifactRef(t, blobs, "e")},
	})
	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Errorf("Disposition = %q", obs.Disposition)
	}
	validateEvidence(t, obs.Evidence)
}

func TestInvoke_OpenPullRequest_Success(t *testing.T) {
	blobs := newFakeBlobStore()
	title := artifactRef(t, blobs, "My PR title")
	body := artifactRef(t, blobs, "My PR body")
	transport := &countingTransport{fn: func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/repos/acme/widgets/pulls" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var b ghCreatePullBody
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if b.Title != "My PR title" || b.Body != "My PR body" || b.Head != "feature/x" || b.Base != "main" {
			t.Errorf("body = %+v", b)
		}
		return jsonResponse(201, `{"number":42,"html_url":"https://github.com/acme/widgets/pull/42","head":{"sha":"`+validSHA()+`"},"base":{"sha":"`+validSHA()+`"}}`), nil
	}}
	a := newTestAdapter(t, transport, blobs, defaultProfileJSON(t))
	d := testDispatch(t, wireOpenPullRequest{
		Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindOpenPullRequest,
		HeadBranch: "feature/x", HeadSHA: validSHA(), BaseBranch: "main", BaseSHA: validSHA(),
		Title: title, Body: body, Patch: artifactRef(t, blobs, "patch"), Draft: false,
		AutomationConstraints: permissiveAutomation(),
		PreflightEvidence:     []wireArtifactRef{artifactRef(t, blobs, "e")},
	})
	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Errorf("Disposition = %q", obs.Disposition)
	}
	if obs.ProviderReference != "https://github.com/acme/widgets/pull/42" {
		t.Errorf("ProviderReference = %q", obs.ProviderReference)
	}
	validateEvidence(t, obs.Evidence)
}

func TestInvoke_MergePullRequest_Success(t *testing.T) {
	blobs := newFakeBlobStore()
	transport := &countingTransport{fn: func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/repos/acme/widgets/pulls/7/merge" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var b ghMergePullBody
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if b.CommitMessage != "merge body text" {
			t.Errorf("commit_message = %q", b.CommitMessage)
		}
		return jsonResponse(200, `{"sha":"`+validSHA()+`","merged":true,"message":"merged"}`), nil
	}}
	a := newTestAdapter(t, transport, blobs, defaultProfileJSON(t))
	d := testDispatch(t, wireMergePullRequest{
		Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindMergePullRequest,
		PullRequestNumber: 7, ExpectedHeadSHA: validSHA(), ExpectedBaseSHA: validSHA(), MergeMethod: "squash",
		CommitTitle: artifactRef(t, blobs, "merge title text"), CommitBody: artifactRef(t, blobs, "merge body text"),
		AutomationConstraints: permissiveAutomation(),
		PreflightEvidence:     []wireArtifactRef{artifactRef(t, blobs, "e")},
	})
	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Errorf("Disposition = %q", obs.Disposition)
	}
	validateEvidence(t, obs.Evidence)
}

func TestInvoke_NonTwoXX_Fails(t *testing.T) {
	transport := &countingTransport{fn: func(r *http.Request) (*http.Response, error) {
		return jsonResponse(422, `{"message":"Reference already exists"}`), nil
	}}
	a := newTestAdapter(t, transport, newFakeBlobStore(), defaultProfileJSON(t))
	blobs := newFakeBlobStore()
	d := testDispatch(t, wireCreateBranch{
		Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindCreateBranch,
		Branch: "feature/x", BaseSHA: validSHA(), ExpectedAbsent: "required",
		AutomationConstraints: permissiveAutomation(),
		PreflightEvidence:     []wireArtifactRef{artifactRef(t, blobs, "e")},
	})
	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke returned an error instead of an Observation: %v", err)
	}
	if obs.Disposition != contract.DispositionFailed {
		t.Errorf("Disposition = %q, want failed", obs.Disposition)
	}
	if obs.ConfirmedAt == nil {
		t.Error("a synchronous provider failure is authoritative; expected ConfirmedAt to be set")
	}
	validateEvidence(t, obs.Evidence)
}

// TestInvoke_NoHiddenRetry is the Z06 acceptance case: a transient
// mid-response transport failure produces exactly one physical call, never
// a hidden retry, with disposition unknown (the mutation might have taken
// effect).
func TestInvoke_NoHiddenRetry_TransientErrorIsUnknown(t *testing.T) {
	transport := &countingTransport{fn: func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("unexpected EOF reading response")
	}}
	a := newTestAdapter(t, transport, newFakeBlobStore(), defaultProfileJSON(t))
	blobs := newFakeBlobStore()
	d := testDispatch(t, wirePushCommit{
		Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindPushCommit,
		Branch: "feature/x", ExpectedHeadSHA: validSHA(), PreparedCommitSHA: validSHA(), BaseSHA: validSHA(),
		Patch: artifactRef(t, blobs, "patch"), ContentArtifacts: []wireArtifactRef{}, Force: false,
		AutomationConstraints: permissiveAutomation(),
		PreflightEvidence:     []wireArtifactRef{artifactRef(t, blobs, "e")},
	})
	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke returned an error instead of an Observation: %v", err)
	}
	if obs.Disposition != contract.DispositionUnknown {
		t.Errorf("Disposition = %q, want unknown", obs.Disposition)
	}
	if transport.count() != 1 {
		t.Fatalf("expected exactly one physical call, got %d", transport.count())
	}
	validateEvidence(t, obs.Evidence)
}

func TestInvoke_DialFailureIsNotSent(t *testing.T) {
	transport := &countingTransport{fn: func(r *http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	}}
	a := newTestAdapter(t, transport, newFakeBlobStore(), defaultProfileJSON(t))
	d := testDispatch(t, wireReadRepository{Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindReadRepository, Resource: "metadata"})
	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke returned an error instead of an Observation: %v", err)
	}
	if obs.Disposition != contract.DispositionNotSent {
		t.Errorf("Disposition = %q, want not_sent", obs.Disposition)
	}
	validateEvidence(t, obs.Evidence)
}

func TestInvoke_SecretIsRedacted(t *testing.T) {
	transport := &countingTransport{fn: func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("proxy denied request with header Authorization: Bearer " + testToken)
	}}
	a := newTestAdapter(t, transport, newFakeBlobStore(), defaultProfileJSON(t))
	d := testDispatch(t, wireReadRepository{Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindReadRepository, Resource: "metadata"})
	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke returned an error instead of an Observation: %v", err)
	}
	if strings.Contains(string(obs.Evidence), testToken) {
		t.Fatalf("evidence leaked the raw secret: %s", obs.Evidence)
	}
}

func TestInvoke_MaxResponseBytesBound(t *testing.T) {
	// buildProfileJSON's digest binds to the whole document, so a smaller
	// max_response_bytes bound is set by decoding, mutating and
	// recomputing the digest exactly as loadProfile itself would check it.
	base := buildProfileJSON(t, "https://api.github.com", []wireRepository{testRepo()}, []string{kindReadRepository}, permissiveAutomation())
	var w wireGitHubProfile
	if err := json.Unmarshal(base, &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	w.MaxResponseBytes = 16
	raw, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	digest, err := profileDigestWithoutCapabilityEvidence(raw)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	w.CapabilityEvidence.ProfileDigest = digest
	raw, err = json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	blobs := newFakeBlobStore()
	oversized := strings.Repeat("x", 4096)
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(200, oversized), nil
	})
	a := newTestAdapter(t, transport, blobs, raw)
	d := testDispatch(t, wireReadRepository{Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindReadRepository, Resource: "metadata"})
	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if obs.Disposition != contract.DispositionSucceeded {
		t.Errorf("Disposition = %q", obs.Disposition)
	}
	var ev wireGitHubEvidence
	if err := json.Unmarshal(obs.Evidence, &ev); err != nil {
		t.Fatalf("unmarshal evidence: %v", err)
	}
	// The request context is staged first, then the bounded response.
	if len(ev.StagedOutputs) != 2 || ev.StagedOutputs[1].Purpose != "provider_response" || ev.StagedOutputs[1].Size != 16 {
		t.Errorf("staged outputs = %+v, want the provider response bounded to 16", ev.StagedOutputs)
	}
}

func TestInvoke_RedirectIsNotFollowed(t *testing.T) {
	transport := &countingTransport{fn: func(r *http.Request) (*http.Response, error) {
		resp := jsonResponse(302, "")
		resp.Header.Set("Location", "https://evil.example/steal")
		return resp, nil
	}}
	a := newTestAdapter(t, transport, newFakeBlobStore(), defaultProfileJSON(t))
	d := testDispatch(t, wireReadRepository{Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindReadRepository, Resource: "metadata"})
	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke returned an error instead of an Observation: %v", err)
	}
	if obs.Disposition != contract.DispositionFailed {
		t.Errorf("Disposition = %q, want failed (a redirect is never followed)", obs.Disposition)
	}
	if transport.count() != 1 {
		t.Fatalf("expected exactly one physical call (no redirect follow-up), got %d", transport.count())
	}
}

// ---------- Reconcile ----------

func TestReconcile_PushCommit(t *testing.T) {
	cases := []struct {
		name       string
		refSHA     string
		wantStatus string
	}{
		{"matches prepared commit: succeeded", "PREPARED", contract.DispositionSucceeded},
		{"matches unchanged expected head: failed", "EXPECTED", contract.DispositionFailed},
		{"neither: unknown", "0000000000000000000000000000000000000000", contract.DispositionUnknown},
	}
	expectedHead := validSHA()
	prepared := "1111111111111111111111111111111111111111"

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sha := tc.refSHA
			switch sha {
			case "PREPARED":
				sha = prepared
			case "EXPECTED":
				sha = expectedHead
			}
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Errorf("reconcile must be a GET, got %s", r.Method)
				}
				if r.URL.Path != "/repos/acme/widgets/git/ref/heads/feature/x" {
					t.Errorf("path = %s", r.URL.Path)
				}
				return jsonResponse(200, `{"ref":"refs/heads/feature/x","object":{"sha":"`+sha+`"}}`), nil
			})
			blobs := newFakeBlobStore()
			a := newTestAdapter(t, transport, blobs, defaultProfileJSON(t))
			d := testDispatch(t, wirePushCommit{
				Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindPushCommit,
				Branch: "feature/x", ExpectedHeadSHA: expectedHead, PreparedCommitSHA: prepared, BaseSHA: validSHA(),
				Patch: artifactRef(t, blobs, "patch"), ContentArtifacts: []wireArtifactRef{}, Force: false,
				AutomationConstraints: permissiveAutomation(),
				PreflightEvidence:     []wireArtifactRef{artifactRef(t, blobs, "e")},
			})
			obs, err := a.Reconcile(context.Background(), d)
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if obs.Disposition != tc.wantStatus {
				t.Errorf("Disposition = %q, want %q", obs.Disposition, tc.wantStatus)
			}
			validateEvidence(t, obs.Evidence)
		})
	}
}

func TestReconcile_NotFoundIsUnknownNeverFailed(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResponse(404, `{"message":"Not Found"}`), nil
	})
	blobs := newFakeBlobStore()
	a := newTestAdapter(t, transport, blobs, defaultProfileJSON(t))
	d := testDispatch(t, wireCreateBranch{
		Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindCreateBranch,
		Branch: "feature/x", BaseSHA: validSHA(), ExpectedAbsent: "required",
		AutomationConstraints: permissiveAutomation(),
		PreflightEvidence:     []wireArtifactRef{artifactRef(t, blobs, "e")},
	})
	obs, err := a.Reconcile(context.Background(), d)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if obs.Disposition != contract.DispositionUnknown {
		t.Errorf("Disposition = %q, want unknown (GitHub not-found cannot prove nonexecution)", obs.Disposition)
	}
}

func TestReconcile_OpenPullRequest(t *testing.T) {
	blobs := newFakeBlobStore()
	head := validSHA()

	t.Run("found: succeeded", func(t *testing.T) {
		transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if !strings.Contains(r.URL.RawQuery, "head=acme:feature%2Fx") && !strings.Contains(r.URL.String(), "head=acme:feature/x") {
				t.Errorf("query = %s", r.URL.String())
			}
			return jsonResponse(200, `[{"number":9,"html_url":"https://github.com/acme/widgets/pull/9","head":{"sha":"`+head+`"},"base":{"sha":"`+validSHA()+`"}}]`), nil
		})
		a := newTestAdapter(t, transport, blobs, defaultProfileJSON(t))
		d := testDispatch(t, wireOpenPullRequest{
			Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindOpenPullRequest,
			HeadBranch: "feature/x", HeadSHA: head, BaseBranch: "main", BaseSHA: validSHA(),
			Title: artifactRef(t, blobs, "t"), Body: artifactRef(t, blobs, "b"), Patch: artifactRef(t, blobs, "p"), Draft: false,
			AutomationConstraints: permissiveAutomation(),
			PreflightEvidence:     []wireArtifactRef{artifactRef(t, blobs, "e")},
		})
		obs, err := a.Reconcile(context.Background(), d)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if obs.Disposition != contract.DispositionSucceeded {
			t.Errorf("Disposition = %q", obs.Disposition)
		}
	})

	t.Run("not found: unknown", func(t *testing.T) {
		transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return jsonResponse(200, `[]`), nil
		})
		a := newTestAdapter(t, transport, blobs, defaultProfileJSON(t))
		d := testDispatch(t, wireOpenPullRequest{
			Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindOpenPullRequest,
			HeadBranch: "feature/x", HeadSHA: head, BaseBranch: "main", BaseSHA: validSHA(),
			Title: artifactRef(t, blobs, "t"), Body: artifactRef(t, blobs, "b"), Patch: artifactRef(t, blobs, "p"), Draft: false,
			AutomationConstraints: permissiveAutomation(),
			PreflightEvidence:     []wireArtifactRef{artifactRef(t, blobs, "e")},
		})
		obs, err := a.Reconcile(context.Background(), d)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if obs.Disposition != contract.DispositionUnknown {
			t.Errorf("Disposition = %q, want unknown", obs.Disposition)
		}
	})
}

func TestReconcile_MergePullRequest(t *testing.T) {
	blobs := newFakeBlobStore()

	cases := []struct {
		name   string
		merged bool
		want   string
	}{
		{"merged: succeeded", true, contract.DispositionSucceeded},
		{"not merged: failed", false, contract.DispositionFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/repos/acme/widgets/pulls/5" {
					t.Errorf("path = %s", r.URL.Path)
				}
				merged := "false"
				if tc.merged {
					merged = "true"
				}
				return jsonResponse(200, `{"merged":`+merged+`}`), nil
			})
			a := newTestAdapter(t, transport, blobs, defaultProfileJSON(t))
			d := testDispatch(t, wireMergePullRequest{
				Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindMergePullRequest,
				PullRequestNumber: 5, ExpectedHeadSHA: validSHA(), ExpectedBaseSHA: validSHA(), MergeMethod: "squash",
				CommitTitle: artifactRef(t, blobs, "t"), CommitBody: artifactRef(t, blobs, "b"),
				AutomationConstraints: permissiveAutomation(),
				PreflightEvidence:     []wireArtifactRef{artifactRef(t, blobs, "e")},
			})
			obs, err := a.Reconcile(context.Background(), d)
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if obs.Disposition != tc.want {
				t.Errorf("Disposition = %q, want %q", obs.Disposition, tc.want)
			}
		})
	}
}

func TestCallContext_UsesEarlierDeadline(t *testing.T) {
	clock := newFakeClock()
	base := clock.Now()
	deadline := base.Add(time.Second)
	ctx, cancel := callContext(context.Background(), clock, time.Hour, deadline)
	defer cancel()
	got, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected a deadline")
	}
	if !got.Equal(deadline) {
		t.Errorf("deadline = %v, want the earlier dispatch deadline %v", got, deadline)
	}
}
