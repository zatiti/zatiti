package controller

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

const qualificationExpectedText = "ZATITI_MODEL_QUALIFIED"

type qualificationProbeParameters struct {
	ProfileDigest string `json:"profile_digest"`
	Provider      string `json:"provider"`
}

type qualificationCallbackEvidence struct {
	Kind         string `json:"kind"`
	PhysicalCall struct {
		OperationID   contract.ID `json:"operation_id"`
		ProfileDigest string      `json:"profile_digest"`
		RequestSent   string      `json:"request_sent"`
		Confirmation  string      `json:"confirmation"`
		FinishedAt    time.Time   `json:"finished_at"`
	} `json:"physical_call"`
	Output *struct {
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
	} `json:"output"`
	Qualification *struct {
		AdapterVersion   string   `json:"adapter_version"`
		SourceRevision   string   `json:"source_revision"`
		ProtocolRevision string   `json:"protocol_revision"`
		Capabilities     []string `json:"capabilities"`
		Limitations      []string `json:"limitations"`
	} `json:"qualification"`
}

type configurationQualificationRecordInput struct {
	JobID            contract.ID  `json:"job_id"`
	ExpectedVersion  int64        `json:"expected_version"`
	Generation       int64        `json:"generation"`
	OperationID      contract.ID  `json:"operation_id"`
	ProfileDigest    string       `json:"profile_digest"`
	Artifact         wireArtifact `json:"artifact"`
	QualifiedAt      time.Time    `json:"qualified_at"`
	AdapterVersion   string       `json:"adapter_version"`
	SourceRevision   string       `json:"source_revision"`
	ProtocolRevision string       `json:"protocol_revision"`
	Capabilities     []string     `json:"capabilities"`
	Limitations      []string     `json:"limitations"`
}

type qualificationJobOutput struct {
	Resource json.RawMessage `json:"resource"`
}

func (c *Controller) deliverQualification(ctx context.Context, sess *session, e *entry, observation contract.Observation) error {
	state, callback, evidenceIDs, err := c.qualificationResult(ctx, e, observation)
	if err != nil {
		return err
	}
	if evidenceIDs == nil {
		evidenceIDs = []contract.ID{}
	}
	result := json.RawMessage(`{"state":"` + state + `"}`)
	if state == jobStateSucceeded {
		var recorded qualificationJobOutput
		err = c.write(func() error {
			if err := c.call(ctx, sess, "_configuration.execution_profile.qualify.record", callback, &recorded); err != nil {
				return err
			}
			if len(recorded.Resource) == 0 {
				return internalFault("configuration qualification callback returned no qualified profile")
			}
			var marshalErr error
			result, marshalErr = json.Marshal(qualificationJobOutput{Resource: recorded.Resource})
			if marshalErr != nil {
				return internalFault("qualified profile result could not be encoded")
			}
			return c.call(ctx, sess, "_execution.job.record", jobRecordInput{
				JobID: e.Route.JobID, ExpectedVersion: e.Route.JobVersion,
				Generation: sess.generation, State: jobStateSucceeded, Result: result, EvidenceIDs: evidenceIDs,
			}, nil)
		})
		return err
	}
	return c.write(func() error {
		if err := c.call(ctx, sess, "_configuration.execution_profile.qualify.finish", map[string]any{
			"job_id":           e.Route.JobID,
			"expected_version": e.Route.JobVersion,
			"generation":       e.Generation,
			"operation_id":     e.OperationID,
			"profile_digest":   e.Route.ProfileDigest,
			"state":            state,
		}, nil); err != nil {
			return err
		}
		return c.call(ctx, sess, "_execution.job.record", jobRecordInput{
			JobID: e.Route.JobID, ExpectedVersion: e.Route.JobVersion,
			Generation: sess.generation, State: state, Result: result, EvidenceIDs: evidenceIDs,
		}, nil)
	})
}

func (c *Controller) qualificationResult(ctx context.Context, e *entry, observation contract.Observation) (string, configurationQualificationRecordInput, []contract.ID, error) {
	var ids []contract.ID
	var evidence qualificationCallbackEvidence
	var publishedArtifacts []wireArtifact
	if len(observation.Evidence) > 0 && json.Unmarshal(observation.Evidence, &evidence) == nil {
		var published struct {
			OutputArtifacts []wireArtifact `json:"output_artifacts"`
		}
		if json.Unmarshal(observation.Evidence, &published) == nil {
			publishedArtifacts = published.OutputArtifacts
			for _, artifact := range publishedArtifacts {
				if artifact.ID != "" {
					ids = append(ids, artifact.ID)
				}
			}
		}
	}
	if observation.Disposition == contract.DispositionUnknown || observation.Disposition == contract.DispositionAccepted {
		return jobStateOutcomeUnknown, configurationQualificationRecordInput{}, ids, nil
	}
	if observation.Disposition != contract.DispositionSucceeded || evidence.Kind != "qualification_probe" ||
		evidence.PhysicalCall.OperationID != e.OperationID || evidence.PhysicalCall.RequestSent != "yes" ||
		evidence.PhysicalCall.Confirmation != "authoritative_success" || evidence.Output == nil ||
		evidence.Output.FinishReason != "completed" || evidence.Output.Refusal != "" ||
		len(evidence.Output.TextOutputs) != 1 || len(evidence.Output.ToolProposals) != 0 || evidence.Qualification == nil {
		return jobStateFailed, configurationQualificationRecordInput{}, ids, nil
	}
	if e.Route.ProfileDigest == "" {
		return jobStateFailed, configurationQualificationRecordInput{}, ids, nil
	}
	protocol := map[string]string{"openai": "openai-openapi/2.3.0@ddface9b", "openrouter": "openrouter-responses/stateless-v1", "experiential": "experiential-responses/stateless-v1"}
	if evidence.PhysicalCall.ProfileDigest != e.Route.ProfileDigest || protocol[e.Route.Provider] == "" ||
		evidence.Qualification.ProtocolRevision != protocol[e.Route.Provider] ||
		evidence.Qualification.AdapterVersion != "responses-adapter/v2" ||
		evidence.Qualification.SourceRevision == "" ||
		!sameQualificationStrings(evidence.Qualification.Capabilities, []string{"responses.text_generation", "responses.single_request"}) {
		return jobStateFailed, configurationQualificationRecordInput{}, ids, nil
	}
	text := evidence.Output.TextOutputs[0]
	if text.Kind != "artifact" || text.Artifact.ID == "" || text.Artifact.Digest == "" {
		return jobStateFailed, configurationQualificationRecordInput{}, ids, nil
	}
	published := false
	for _, artifact := range publishedArtifacts {
		if artifact.ID == text.Artifact.ID && artifact.Digest == text.Artifact.Digest {
			published = true
			break
		}
	}
	if !published {
		return jobStateFailed, configurationQualificationRecordInput{}, ids, nil
	}
	c.mu.Lock()
	blobs := c.deps.Blobs
	c.mu.Unlock()
	if blobs == nil {
		return "", configurationQualificationRecordInput{}, ids, unavailable("qualification response artifact store is unavailable; callback will retry without repeating the provider request")
	}
	reader, err := blobs.Open(ctx, text.Artifact.Digest, 0, 256)
	if err != nil {
		return "", configurationQualificationRecordInput{}, ids, unavailable("qualification response artifact is temporarily unavailable; callback will retry without repeating the provider request")
	}
	content, readErr := io.ReadAll(io.LimitReader(reader, 257))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		return "", configurationQualificationRecordInput{}, ids, unavailable("qualification response artifact could not be read; callback will retry without repeating the provider request")
	}
	if len(content) > 256 || contract.Hash(content) != text.Artifact.Digest || strings.TrimSpace(string(content)) != qualificationExpectedText {
		return jobStateFailed, configurationQualificationRecordInput{}, ids, nil
	}
	if evidence.PhysicalCall.FinishedAt.IsZero() {
		return jobStateFailed, configurationQualificationRecordInput{}, ids, nil
	}
	callback := configurationQualificationRecordInput{
		JobID: e.Route.JobID, ExpectedVersion: e.Route.JobVersion, Generation: e.Generation,
		OperationID: e.OperationID, ProfileDigest: e.Route.ProfileDigest,
		Artifact:       wireArtifact{ID: text.Artifact.ID, Digest: text.Artifact.Digest},
		QualifiedAt:    evidence.PhysicalCall.FinishedAt,
		AdapterVersion: evidence.Qualification.AdapterVersion, SourceRevision: evidence.Qualification.SourceRevision,
		ProtocolRevision: evidence.Qualification.ProtocolRevision,
		Capabilities:     evidence.Qualification.Capabilities, Limitations: evidence.Qualification.Limitations,
	}
	return jobStateSucceeded, callback, appendUniqueID(ids, text.Artifact.ID), nil
}

func sameQualificationStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func appendUniqueID(ids []contract.ID, id contract.ID) []contract.ID {
	for _, old := range ids {
		if old == id {
			return ids
		}
	}
	return append(ids, id)
}
