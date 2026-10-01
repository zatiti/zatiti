package execution

import (
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// decodeTurnModelOutput accepts published adapter evidence and checks its
// exact operation reference and physical request artifact. The physical
// request record and the turn's input context are distinct artifacts.
func decodeTurnModelOutput(raw json.RawMessage, operation contract.ID, turn *turnRow) (wireModelOutput, error) {
	var envelope struct {
		Schema       string          `json:"schema"`
		Output       json.RawMessage `json:"output"`
		PhysicalCall struct {
			OperationID    contract.ID         `json:"operation_id"`
			RequestContext wireArtifactLocator `json:"request_context"`
		} `json:"physical_call"`
	}
	var output wireModelOutput
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return output, invalidInput("malformed model evidence: %v", err)
	}
	wrapped := envelope.Schema == "zatiti.responses.evidence/v1" || envelope.Schema == "zatiti.responses.evidence/v2"
	if wrapped {
		schema, err := responsesEvidenceSchema()
		if err != nil {
			return output, err
		}
		if err := contract.ValidateSchema(schema, raw); err != nil {
			return output, invalidInput("malformed Responses evidence: %v", err)
		}
		if envelope.PhysicalCall.OperationID != operation {
			return output, invalidInput("Responses evidence names a different operation")
		}
		raw = envelope.Output
	}
	schema, err := modelOutputSchema()
	if err != nil {
		return output, err
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return output, invalidInput("malformed model output: %v", err)
	}
	if err := contract.DecodeStrict(raw, &output); err != nil {
		return output, invalidInput("malformed model output: %v", err)
	}
	if turn.ContextArtifact == nil || output.RequestContext.Kind != "artifact" || output.RequestContext.Artifact == nil {
		return output, invalidInput("model output lacks a published request context")
	}
	expected := turn.ContextArtifact
	if wrapped {
		if envelope.PhysicalCall.RequestContext.Kind != "artifact" || envelope.PhysicalCall.RequestContext.Artifact == nil {
			return output, invalidInput("physical request context is not published")
		}
		expected = envelope.PhysicalCall.RequestContext.Artifact
	}
	if output.RequestContext.Artifact.ID != expected.ID || output.RequestContext.Artifact.Digest != expected.Digest {
		return output, invalidInput("model output request context does not match the authorized dispatch")
	}
	return output, nil
}
