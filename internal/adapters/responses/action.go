package responses

import (
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// kindModelStep is the only action kind the frozen
// zatiti.responses.action/v1 schema accepts.
const kindModelStep = "model_step"

// decodeAction validates raw against the zatiti.responses.action/v1 schema,
// strict-decodes it, and rejects a tool_contract_versions list that pins
// the same tool twice: a tool closure with two versions of one tool cannot
// identify which contract a model proposal was made under.
func decodeAction(raw json.RawMessage) (*wireResponsesParameters, error) {
	schema, err := parametersSchema()
	if err != nil {
		return nil, internalError("responses parameters schema composition failed: %v", err)
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return nil, invalidInput("responses action does not match the zatiti.responses.action/v1 schema: %v", err)
	}
	var w wireResponsesParameters
	if err := contract.DecodeStrict(raw, &w); err != nil {
		return nil, invalidInput("responses action decode failed: %v", err)
	}
	seen := make(map[contract.ID]bool, len(w.ToolContractVersions))
	for _, ref := range w.ToolContractVersions {
		if seen[ref.ID] {
			return nil, invalidInput("responses action tool_contract_versions pins tool %s more than once", ref.ID)
		}
		seen[ref.ID] = true
	}
	return &w, nil
}
