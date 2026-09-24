package connections

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestMCPProbeOutputSchemaAcceptsRevisionFourEvidence(t *testing.T) {
	digest := strings.Repeat("0", 64)
	artifact := func(n int) map[string]any {
		return map[string]any{
			"id":     fmt.Sprintf("00000000-0000-4000-8000-%012d", n),
			"digest": digest,
		}
	}
	staged := func(ref string, purpose string) map[string]any {
		return map[string]any{
			"staging_ref": ref, "digest": digest, "size": 1,
			"media_type": "application/json", "classification": "public", "purpose": purpose,
		}
	}
	requestContext := map[string]any{"kind": "staged", "staging_ref": "request-context", "digest": digest}
	outputs := make([]any, 20)
	artifacts := make([]any, 20)
	for i := range outputs {
		purpose := "tool_result"
		ref := fmt.Sprintf("output-%d", i)
		if i == 0 {
			purpose, ref = "context", "request-context"
		}
		outputs[i] = staged(ref, purpose)
		artifacts[i] = artifact(201 + i)
	}
	exchanges := make([]any, 19)
	for i := range exchanges {
		kind, method := "main", "POST"
		rpcMethod := "tools/list"
		if i == 0 {
			kind, rpcMethod = "handshake", "server/discover"
		}
		exchanges[i] = map[string]any{
			"kind": kind, "method": method, "rpc_method": rpcMethod,
			"ordinal": i + 1, "request_context": requestContext, "request_sent": "yes",
		}
	}
	evidence := map[string]any{
		"schema": "zatiti.mcp.evidence/v1",
		"physical_call": map[string]any{
			"operation_id":     "00000000-0000-4000-8000-000000000101",
			"attempt_id":       "00000000-0000-4000-8000-000000000102",
			"account_identity": "test-account", "requested_destination": "https://example.test/mcp",
			"resolved_destination": "https://example.test/mcp", "profile_digest": digest,
			"capability_evidence": artifact(103), "started_at": "2026-09-24T20:00:00Z",
			"finished_at": "2026-09-24T20:00:01Z", "request_context": requestContext,
			"request_sent": "unknown", "confirmation": "unknown",
		},
		"kind": "open_session", "session_state": "unknown",
		"handshake":               []any{map[string]any{"message": "server/discover", "request_sent": "yes"}},
		"refused_server_requests": []any{},
		"usage": map[string]any{
			"accounting": map[string]any{
				"currency": "USD", "spent": 0, "reserved": 0, "estimated": 0, "unknown": 0, "advisory": false,
			},
			"billing": "no_charge",
		},
		"staged_outputs":   outputs,
		"output_artifacts": artifacts,
		"exchanges":        exchanges,
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatalf("marshal revision-four MCP evidence: %v", err)
	}
	env := newEnv(t)
	payload := env.mustOK("tool.schema", connGetIn{
		Scope: env.scope,
		ID:    contract.ID("0a000000-0000-4000-8000-0000000000c6"),
	})
	var out schemaOut
	env.decode(payload.Data, &out)
	if err := contract.ValidateSchema(out.OutputSchema, raw); err != nil {
		t.Fatalf("built-in mcp-probe output schema rejected revision-four adapter evidence: %v", err)
	}
}
