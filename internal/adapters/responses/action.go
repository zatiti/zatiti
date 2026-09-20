package responses

import (
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// The two action kinds the frozen zatiti.responses.action/v1 schema
// accepts, revision 3's kind-discriminated oneOf of
// ResponsesPrepareSessionParameters and ResponsesModelStepParameters (see
// "OpenAI Responses session preparation" in AGENTS.md, P00-009). Each
// Adapter.Invoke performs exactly one of the two physical calls the split
// requires, never both.
const (
	kindPrepareSession = "prepare_session"
	kindModelStep      = "model_step"
)

// decodeAction validates raw against the zatiti.responses.action/v1 schema
// (the oneOf that keeps a prepare_session action from carrying any
// model_step-only field, and a model_step action from omitting any of
// them), strict-decodes it, and rejects a tool_contract_versions list that
// pins the same tool twice: a tool closure with two versions of one tool
// cannot identify which contract a model proposal was made under.
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
