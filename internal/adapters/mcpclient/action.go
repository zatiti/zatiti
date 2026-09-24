package mcpclient

import (
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Action kinds qualified for v1. These are the only values the frozen
// zatiti.mcp.action/v1 oneOf schema accepts.
const (
	kindOpenSession  = "open_session"
	kindListTools    = "list_tools"
	kindCallTool     = "call_tool"
	kindCloseSession = "close_session"
)

// action is the decoded, kind-discriminated Dispatch.Action. Exactly one of
// the typed fields is non-nil, matching Kind.
type action struct {
	Kind         string
	OpenSession  *wireOpenSession
	ListTools    *wireListTools
	CallTool     *wireCallTool
	CloseSession *wireCloseSession
}

// decodeAction validates raw against the composed zatiti.mcp.action/v1
// oneOf schema and strict-decodes it into the variant named by its "kind"
// discriminator.
func decodeAction(raw json.RawMessage) (*action, error) {
	schema, err := parametersSchema()
	if err != nil {
		return nil, internalError("mcp parameters schema composition failed: %v", err)
	}
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return nil, invalidInput("mcp action does not match the zatiti.mcp.action/v1 schema: %v", err)
	}

	var peek wireActionKind
	if err := json.Unmarshal(raw, &peek); err != nil {
		return nil, invalidInput("mcp action kind could not be read: %v", err)
	}

	act := &action{Kind: peek.Kind}
	switch peek.Kind {
	case kindOpenSession:
		var w wireOpenSession
		if err := contract.DecodeStrict(raw, &w); err != nil {
			return nil, invalidInput("mcp open_session action decode failed: %v", err)
		}
		act.OpenSession = &w
	case kindListTools:
		var w wireListTools
		if err := contract.DecodeStrict(raw, &w); err != nil {
			return nil, invalidInput("mcp list_tools action decode failed: %v", err)
		}
		act.ListTools = &w
	case kindCallTool:
		var w wireCallTool
		if err := contract.DecodeStrict(raw, &w); err != nil {
			return nil, invalidInput("mcp call_tool action decode failed: %v", err)
		}
		act.CallTool = &w
	case kindCloseSession:
		var w wireCloseSession
		if err := contract.DecodeStrict(raw, &w); err != nil {
			return nil, invalidInput("mcp close_session action decode failed: %v", err)
		}
		act.CloseSession = &w
	default:
		// The oneOf schema already rejected any kind outside the four
		// qualified forms above; this is unreachable defense in depth.
		return nil, capabilityUnsupported("mcp action kind %q is not a qualified v1 capability", peek.Kind)
	}
	return act, nil
}
