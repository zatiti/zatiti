package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// stagedRequestRecord decodes the request record an observation's
// request_context names, reading it back from the blob store by digest.
func stagedRequestRecord(t *testing.T, blobs *fakeBlobStore, evidence json.RawMessage) (requestRecord, wireGitHubEvidence) {
	t.Helper()
	var ev wireGitHubEvidence
	if err := json.Unmarshal(evidence, &ev); err != nil {
		t.Fatalf("unmarshal evidence: %v", err)
	}
	blobs.mu.Lock()
	raw, ok := blobs.objects[ev.PhysicalCall.RequestContext.Digest]
	blobs.mu.Unlock()
	if !ok {
		t.Fatalf("request_context %+v names no staged bytes", ev.PhysicalCall.RequestContext)
	}
	var record requestRecord
	if err := contract.DecodeStrict(raw, &record); err != nil {
		t.Fatalf("decode request record: %v", err)
	}
	return record, ev
}

func createBranchDispatch(t *testing.T, blobs *fakeBlobStore) contract.Dispatch {
	t.Helper()
	return testDispatch(t, wireCreateBranch{
		Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindCreateBranch,
		Branch: "feature/x", BaseSHA: validSHA(), ExpectedAbsent: "required",
		AutomationConstraints: permissiveAutomation(),
		PreflightEvidence:     []wireArtifactRef{artifactRef(t, blobs, "e")},
	})
}

func TestInvoke_StagesExactSecretFreeRequestRecordBeforeSending(t *testing.T) {
	blobs := newFakeBlobStore()
	var sentBody []byte
	stagedBeforeSend := -1
	transport := &countingTransport{fn: func(r *http.Request) (*http.Response, error) {
		blobs.mu.Lock()
		stagedBeforeSend = blobs.staged
		blobs.mu.Unlock()
		var readErr error
		if sentBody, readErr = io.ReadAll(r.Body); readErr != nil {
			t.Errorf("read body: %v", readErr)
		}
		if r.Header.Get("Authorization") != "Bearer "+testToken || r.Header.Get("Accept") != "application/vnd.github+json" ||
			r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" || r.Header.Get("User-Agent") != userAgent || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request headers = %v", r.Header)
		}
		return jsonResponse(201, `{"ref":"refs/heads/feature/x","object":{"sha":"`+validSHA()+`"}}`), nil
	}}
	a := newTestAdapter(t, transport, blobs, defaultProfileJSON(t))
	obs, err := a.Invoke(context.Background(), createBranchDispatch(t, blobs))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if stagedBeforeSend != 1 {
		t.Fatalf("staged objects when the request was sent = %d, want the request record already staged", stagedBeforeSend)
	}
	validateEvidence(t, obs.Evidence)

	record, ev := stagedRequestRecord(t, blobs, obs.Evidence)
	if record.Schema != requestRecordSchema || record.Method != http.MethodPost || record.Destination != "https://api.github.com/repos/acme/widgets/git/refs" {
		t.Errorf("request record = %+v", record)
	}
	body, err := base64.StdEncoding.DecodeString(record.BodyBase64)
	if err != nil || string(body) != string(sentBody) || len(body) == 0 {
		t.Errorf("request record body %q is not the body as sent %q (%v)", body, sentBody, err)
	}
	if record.BodySize != int64(len(sentBody)) || record.BodyDigest != contract.Hash(sentBody) {
		t.Errorf("request record body size/digest = %d / %s", record.BodySize, record.BodyDigest)
	}
	var names []string
	for _, h := range record.Headers {
		names = append(names, h.Name)
	}
	if got := strings.Join(names, ","); got != "Accept,Content-Type,User-Agent,X-Github-Api-Version" {
		t.Errorf("recorded headers = %s; Authorization must be excluded and the rest sorted", got)
	}
	blobs.mu.Lock()
	for digest, data := range blobs.objects {
		if strings.Contains(string(data), testToken) {
			t.Errorf("staged object %s contains the credential", digest)
		}
	}
	blobs.mu.Unlock()

	// The placeholder is gone: request_context is not capability evidence.
	if ev.PhysicalCall.RequestContext.Digest == testCapabilityArtifact().Digest {
		t.Error("request_context reuses the capability evidence artifact")
	}
	first := ev.StagedOutputs[0]
	if first.Purpose != "context" || first.MediaType != "application/json" || first.Classification != "internal" {
		t.Errorf("staged request context = %+v", first)
	}
}

func TestInvoke_ReadRecordsAnEmptyBody(t *testing.T) {
	blobs := newFakeBlobStore()
	a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"full_name":"acme/widgets"}`), nil
	}), blobs, defaultProfileJSON(t))
	d := testDispatch(t, wireReadRepository{Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindReadRepository, Resource: "metadata"})
	obs, err := a.Invoke(context.Background(), d)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	record, _ := stagedRequestRecord(t, blobs, obs.Evidence)
	if record.Method != http.MethodGet || record.BodySize != 0 || record.BodyBase64 != "" || record.BodyDigest != contract.Hash(nil) {
		t.Errorf("request record = %+v", record)
	}
	for _, h := range record.Headers {
		if h.Name == "Content-Type" {
			t.Error("a bodiless request recorded a Content-Type header")
		}
	}
}

func TestInvoke_RequestContextStagingFailureSendsNothing(t *testing.T) {
	cases := []struct {
		name  string
		blobs func() contract.BlobStore
		code  string
	}{
		{"stage fails", func() contract.BlobStore {
			b := newFakeBlobStore()
			b.stageErr = errors.New("disk full at /private/path")
			return b
		}, contract.CodeInternalError},
		{"no blob store", func() contract.BlobStore { return nil }, contract.CodePrerequisiteMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			transport := &countingTransport{fn: func(*http.Request) (*http.Response, error) {
				return jsonResponse(200, `{}`), nil
			}}
			a := newTestAdapter(t, transport, tc.blobs(), defaultProfileJSON(t))
			d := testDispatch(t, wireReadRepository{Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindReadRepository, Resource: "metadata"})
			obs, err := a.Invoke(context.Background(), d)
			f := mustFault(t, err)
			if f.Code != tc.code || strings.Contains(f.Message, "/private/path") {
				t.Errorf("fault = %s: %s, want %s without store internals", f.Code, f.Message, tc.code)
			}
			if obs.Disposition != "" || transport.count() != 0 {
				t.Errorf("disposition %q, physical calls %d; nothing may be sent without a staged request context", obs.Disposition, transport.count())
			}
		})
	}
}

// A not_sent or unknown attempt keeps its staged request context, and
// Reconcile is its own physical call with its own request context.
func TestRequestContextIsKeptForEveryDisposition(t *testing.T) {
	cases := []struct {
		name        string
		err         error
		disposition string
	}{
		{"not_sent", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, contract.DispositionNotSent},
		{"unknown", errors.New("connection reset by peer"), contract.DispositionUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blobs := newFakeBlobStore()
			a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, tc.err }), blobs, defaultProfileJSON(t))
			obs, err := a.Invoke(context.Background(), createBranchDispatch(t, blobs))
			if err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			if obs.Disposition != tc.disposition {
				t.Errorf("Disposition = %q, want %q", obs.Disposition, tc.disposition)
			}
			validateEvidence(t, obs.Evidence)
			record, ev := stagedRequestRecord(t, blobs, obs.Evidence)
			if record.Method != http.MethodPost || len(ev.StagedOutputs) != 1 {
				t.Errorf("record %+v, staged outputs %+v", record, ev.StagedOutputs)
			}
		})
	}

	t.Run("reconcile", func(t *testing.T) {
		blobs := newFakeBlobStore()
		a := newTestAdapter(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, `{"ref":"refs/heads/feature/x","object":{"sha":"`+validSHA()+`"}}`), nil
		}), blobs, defaultProfileJSON(t))
		obs, err := a.Reconcile(context.Background(), createBranchDispatch(t, blobs))
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		validateEvidence(t, obs.Evidence)
		record, _ := stagedRequestRecord(t, blobs, obs.Evidence)
		if record.Method != http.MethodGet || record.BodySize != 0 {
			t.Errorf("reconcile request record = %+v, want its own bodiless read", record)
		}
	})
}
