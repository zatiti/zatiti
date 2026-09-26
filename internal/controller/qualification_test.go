package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

type qualificationBlobStore struct {
	contract.BlobStore
	body []byte
}

func (b qualificationBlobStore) Open(_ context.Context, digest contract.Digest, offset, length int64) (io.ReadCloser, error) {
	if digest != contract.Hash(b.body) || offset != 0 || length != 256 {
		return nil, errors.New("blob not found")
	}
	return io.NopCloser(bytes.NewReader(b.body)), nil
}

func TestQualificationResultRequiresPublishedDigestBoundExactProviderReply(t *testing.T) {
	body := []byte("ZATITI_MODEL_QUALIFIED")
	artifactID := contract.NewID()
	digest := contract.Hash(body)
	operationID := contract.NewID()
	profileDigest := string(contract.Hash([]byte("candidate")))
	finished := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	var evidence qualificationCallbackEvidence
	evidence.Kind = "qualification_probe"
	evidence.PhysicalCall.OperationID = operationID
	evidence.PhysicalCall.ProfileDigest = profileDigest
	evidence.PhysicalCall.RequestSent = "yes"
	evidence.PhysicalCall.Confirmation = "authoritative_success"
	evidence.PhysicalCall.FinishedAt = finished
	evidence.Output = &struct {
		FinishReason string `json:"finish_reason"`
		Refusal      string `json:"refusal"`
		TextOutputs  []struct {
			Kind     string `json:"kind"`
			Artifact struct {
				ID     contract.ID     `json:"id"`
				Digest contract.Digest `json:"digest"`
			} `json:"artifact"`
		} `json:"text_outputs"`
		ToolProposals []json.RawMessage `json:"tool_proposals"`
	}{FinishReason: "completed"}
	evidence.Output.TextOutputs = []struct {
		Kind     string `json:"kind"`
		Artifact struct {
			ID     contract.ID     `json:"id"`
			Digest contract.Digest `json:"digest"`
		} `json:"artifact"`
	}{{Kind: "artifact", Artifact: struct {
		ID     contract.ID     `json:"id"`
		Digest contract.Digest `json:"digest"`
	}{ID: artifactID, Digest: digest}}}
	evidence.Qualification = &struct {
		AdapterVersion   string   `json:"adapter_version"`
		SourceRevision   string   `json:"source_revision"`
		ProtocolRevision string   `json:"protocol_revision"`
		Capabilities     []string `json:"capabilities"`
		Limitations      []string `json:"limitations"`
	}{AdapterVersion: "responses-adapter/v2", SourceRevision: "source-rev", ProtocolRevision: "openrouter-responses/stateless-v1", Capabilities: []string{"responses.text_generation", "responses.single_request"}}
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var evidenceDocument map[string]json.RawMessage
	if err := json.Unmarshal(evidenceJSON, &evidenceDocument); err != nil {
		t.Fatal(err)
	}
	published, _ := json.Marshal([]wireArtifact{{ID: artifactID, Digest: digest}})
	evidenceDocument["output_artifacts"] = published
	raw, _ := json.Marshal(evidenceDocument)
	c := &Controller{deps: Collaborators{Blobs: qualificationBlobStore{body: body}}}
	e := &entry{OperationID: operationID, Generation: 4, Route: &route{JobID: contract.NewID(), JobVersion: 2, ProfileDigest: profileDigest, Provider: "openrouter"}}
	obs := contract.Observation{Disposition: contract.DispositionSucceeded, Evidence: raw}
	state, callback, ids, err := c.qualificationResult(context.Background(), e, obs)
	if err != nil {
		t.Fatal(err)
	}
	if state != jobStateSucceeded || callback.Artifact.ID != artifactID || callback.Artifact.Digest != digest || len(ids) != 1 {
		t.Fatalf("qualification result state=%q callback=%+v evidenceIDs=%v evidence=%s", state, callback, ids, raw)
	}

	// The output must be among the effect's published artifacts, even if its
	// digest happens to be readable from the blob store.
	var unpublished map[string]any
	if err := json.Unmarshal(raw, &unpublished); err != nil {
		t.Fatal(err)
	}
	delete(unpublished, "output_artifacts")
	rawUnpublished, _ := json.Marshal(unpublished)
	obs.Evidence = rawUnpublished
	state, _, _, err = c.qualificationResult(context.Background(), e, obs)
	if err != nil || state != jobStateFailed {
		t.Fatalf("unpublished response accepted: state=%q err=%v", state, err)
	}
}

func TestQualificationUnknownOutcomeIsTerminalWithoutAnotherProviderCall(t *testing.T) {
	c := &Controller{}
	state, _, _, err := c.qualificationResult(context.Background(), &entry{}, contract.Observation{Disposition: contract.DispositionUnknown})
	if err != nil || state != jobStateOutcomeUnknown {
		t.Fatalf("state=%q err=%v", state, err)
	}
}

func TestQualificationEffectRoutesBackToTheExactPendingJob(t *testing.T) {
	operationID, jobID := contract.NewID(), contract.NewID()
	profileDigest := string(contract.Hash([]byte("candidate")))
	parameters, err := json.Marshal(qualificationProbeParameters{
		ProfileDigest: profileDigest,
		Provider:      "openrouter",
	})
	if err != nil {
		t.Fatal(err)
	}
	c := &Controller{}
	job := wireJob{
		ID: jobID, Version: 7, State: jobStatePending, Owner: configurationOwner,
		Operation: "execution_profile.qualify", OperationID: operationID,
	}
	got := c.routeFor(operationID, wireOperation{}, wireAction{Parameters: parameters}, map[contract.ID]wireJob{operationID: job})
	if got == nil || got.Owner != ownerQualification || got.JobID != jobID || got.JobVersion != 7 ||
		got.ProfileDigest != profileDigest || got.Provider != "openrouter" {
		t.Fatalf("qualification route = %+v, want exact pending qualification job and candidate", got)
	}
	job.State = jobStateOutcomeUnknown
	if route := c.routeFor(operationID, wireOperation{}, wireAction{Parameters: parameters}, map[contract.ID]wireJob{operationID: job}); route != nil && route.Owner == ownerQualification {
		t.Fatalf("terminal unknown qualification job was routed for another completion: %+v", route)
	}
}
