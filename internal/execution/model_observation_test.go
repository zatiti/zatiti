package execution

import (
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestResponsesModelOutputBindsPhysicalRequestAndOperation(t *testing.T) {
	inputContext := wireArtifactRef{ID: "00000000-0000-4000-8000-000000000200", Digest: digestA}
	physicalContext := wireArtifactRef{ID: "00000000-0000-4000-8000-000000000104", Digest: digestA}
	operation := contract.ID("00000000-0000-4000-8000-000000000101")
	turn := &turnRow{ContextArtifact: &inputContext}
	var evidence wireResponsesEvidence
	if err := json.Unmarshal(buildPrepareSessionEvidence(t, "session-test"), &evidence); err != nil {
		t.Fatal(err)
	}
	evidence.Output = buildModelOutput(t, physicalContext, []wireModelToolProposal{})
	evidence.ResponseID = "response-test"
	raw := mustMarshal(t, evidence)
	if _, err := decodeTurnModelOutput(raw, operation, turn); err != nil {
		t.Fatalf("valid physical request evidence refused: %v", err)
	}
	if _, err := decodeTurnModelOutput(raw, contract.ID("00000000-0000-4000-8000-000000000999"), turn); err == nil {
		t.Fatal("evidence for another operation was accepted")
	}
	evidence.Output = buildModelOutput(t, inputContext, []wireModelToolProposal{})
	if _, err := decodeTurnModelOutput(mustMarshal(t, evidence), operation, turn); err == nil {
		t.Fatal("model output with a substituted physical request artifact was accepted")
	}
}
