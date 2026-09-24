package mcpclient

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/zatiti/zatiti/internal/contract"
)

// schemaDefs is the embedded $defs catalog transcribed byte-for-byte from
// the frozen catalog (docs/implementation/adapter-schemas.json): every
// definition transitively reachable from MCPClientProfile,
// MCPClientParameters and MCPClientEvidence. withDefs injects it at
// composition time so no JSON round trip decays the frozen int64 bounds.
const schemaDefs = `{"$defs":{"ArtifactLocator":{"$schema":"https://json-schema.org/draft/2020-12/schema","oneOf":[{"additionalProperties":false,"properties":{"artifact":{"$ref":"#/$defs/ArtifactRef"},"kind":{"const":"artifact","type":"string"}},"required":["kind","artifact"],"type":"object"},{"additionalProperties":false,"properties":{"digest":{"$ref":"#/$defs/Digest"},"kind":{"const":"staged","type":"string"},"staging_ref":{"maxLength":256,"minLength":1,"type":"string"}},"required":["kind","staging_ref","digest"],"type":"object"}]},"ArtifactRef":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"digest":{"$ref":"#/$defs/Digest"},"id":{"$ref":"#/$defs/ID"}},"required":["id","digest"],"type":"object"},"CapabilityEvidence":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"adapter_version":{"maxLength":128,"minLength":1,"type":"string"},"artifact":{"$ref":"#/$defs/ArtifactRef"},"capabilities":{"items":{"maxLength":128,"minLength":1,"type":"string"},"maxItems":128,"minItems":0,"type":"array"},"limitations":{"items":{"maxLength":2048,"minLength":1,"type":"string"},"maxItems":128,"minItems":0,"type":"array"},"profile_digest":{"$ref":"#/$defs/Digest"},"protocol_revision":{"maxLength":128,"minLength":1,"type":"string"},"qualified_at":{"$ref":"#/$defs/UTC"},"source_revision":{"maxLength":128,"minLength":1,"type":"string"}},"required":["artifact","adapter_version","source_revision","protocol_revision","profile_digest","qualified_at","capabilities","limitations"],"type":"object"},"Classification":{"$schema":"https://json-schema.org/draft/2020-12/schema","enum":["public","internal","restricted"],"type":"string"},"Currency":{"$schema":"https://json-schema.org/draft/2020-12/schema","maxLength":3,"pattern":"^[A-Z]{3}$","type":"string"},"Digest":{"$schema":"https://json-schema.org/draft/2020-12/schema","maxLength":64,"pattern":"^[0-9a-f]{64}$","type":"string"},"HTTPSURL":{"$schema":"https://json-schema.org/draft/2020-12/schema","format":"uri","maxLength":4096,"pattern":"^https://","type":"string"},"ID":{"$schema":"https://json-schema.org/draft/2020-12/schema","format":"uuid","maxLength":36,"type":"string"},"InertSchema":{"$schema":"https://json-schema.org/draft/2020-12/schema","description":"A bounded JSON Schema 2020-12 document. Local references only; validators reject remote references, duplicate keys, excessive depth/node count and unsupported executable behavior. This is schema data, not authority.","maxProperties":256,"type":"object"},"MCPCallTool":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"arguments":{"$ref":"#/$defs/ToolArguments"},"classification":{"$ref":"#/$defs/Classification"},"input_schema":{"$ref":"#/$defs/InertSchema"},"input_schema_digest":{"$ref":"#/$defs/Digest"},"kind":{"const":"call_tool","type":"string"},"schema":{"const":"zatiti.mcp.action/v1","type":"string"},"session_handle":{"$ref":"#/$defs/MCPSessionHandle"},"tool":{"$ref":"#/$defs/MCPToolName"}},"required":["schema","kind","session_handle","tool","arguments","input_schema","input_schema_digest","classification"],"type":"object"},"MCPCloseSession":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"kind":{"const":"close_session","type":"string"},"schema":{"const":"zatiti.mcp.action/v1","type":"string"},"session_handle":{"$ref":"#/$defs/MCPSessionHandle"}},"required":["schema","kind","session_handle"],"type":"object"},"MCPContentSummary":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"audio":{"maximum":65536,"minimum":0,"type":"integer"},"embedded_resource":{"maximum":65536,"minimum":0,"type":"integer"},"image":{"maximum":65536,"minimum":0,"type":"integer"},"resource_link":{"maximum":65536,"minimum":0,"type":"integer"},"text":{"maximum":65536,"minimum":0,"type":"integer"}},"required":["text","image","audio","resource_link","embedded_resource"],"type":"object"},"MCPDiscoveredTool":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"annotations":{"$ref":"#/$defs/MCPToolAnnotations"},"description":{"maxLength":4096,"minLength":0,"type":"string"},"input_schema":{"$ref":"#/$defs/InertSchema"},"input_schema_digest":{"$ref":"#/$defs/Digest"},"name":{"$ref":"#/$defs/MCPToolName"},"output_schema":{"$ref":"#/$defs/InertSchema"},"title":{"maxLength":256,"minLength":0,"type":"string"}},"required":["name","input_schema","input_schema_digest"],"type":"object"},"MCPHandshakeExchange":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"http_status":{"maximum":599,"minimum":100,"type":"integer"},"message":{"enum":["server/discover","initialize","notifications/initialized"],"type":"string"},"request_sent":{"enum":["no","yes","unknown"],"type":"string"}},"required":["message","http_status","request_sent"],"type":"object"},"MCPListTools":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"cursor":{"maxLength":1024,"minLength":0,"type":"string"},"kind":{"const":"list_tools","type":"string"},"schema":{"const":"zatiti.mcp.action/v1","type":"string"},"session_handle":{"$ref":"#/$defs/MCPSessionHandle"}},"required":["schema","kind","session_handle"],"type":"object"},"MCPOpenSession":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"client_name":{"maxLength":128,"minLength":1,"type":"string"},"client_version":{"maxLength":128,"minLength":1,"type":"string"},"kind":{"const":"open_session","type":"string"},"schema":{"const":"zatiti.mcp.action/v1","type":"string"}},"required":["schema","kind","client_name","client_version"],"type":"object"},"MCPSessionHandle":{"$schema":"https://json-schema.org/draft/2020-12/schema","description":"Adapter-local opaque handle for one open MCP session (never the raw Mcp-Session-Id, which stays inside the adapter). Scoped to the connection that opened it; unknown after a controller restart, which is reported as prerequisite_missing, never re-derived.","maxLength":256,"minLength":1,"type":"string"},"MCPStdioTransport":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"args":{"items":{"maxLength":4096,"minLength":0,"type":"string"},"maxItems":64,"minItems":0,"type":"array"},"command":{"description":"Absolute path of an installation-authorized executable. Not qualified in v1: a profile naming this transport fails capability_unsupported until a coordinated revision qualifies subprocess custody (environment scrubbing, working directory, signal/timeout semantics).","maxLength":4096,"minLength":1,"type":"string"},"kind":{"const":"stdio","type":"string"}},"required":["kind","command","args"],"type":"object"},"MCPStreamableHTTPTransport":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"allow_private_endpoint":{"description":"Explicit installation-authorized binding to a loopback/private/link-local destination. False refuses such a resolved address before dialing and binds validated dial addresses to prevent DNS rebinding, exactly as httpread does.","type":"boolean"},"endpoint":{"$ref":"#/$defs/HTTPSURL"},"kind":{"const":"streamable_http","type":"string"},"max_redirects":{"const":0,"type":"integer"}},"required":["kind","endpoint","allow_private_endpoint","max_redirects"],"type":"object"},"MCPToolAnnotations":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"description":"Server-declared hints copied verbatim for operator review. They are untrusted data and never change effect classification, policy, review or allowlist decisions.","properties":{"destructive_hint":{"type":"boolean"},"idempotent_hint":{"type":"boolean"},"open_world_hint":{"type":"boolean"},"read_only_hint":{"type":"boolean"},"title":{"maxLength":256,"minLength":0,"type":"string"}},"required":[],"type":"object"},"MCPToolName":{"$schema":"https://json-schema.org/draft/2020-12/schema","maxLength":128,"pattern":"^[A-Za-z0-9_.-]{1,128}$","type":"string"},"Money":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"currency":{"$ref":"#/$defs/Currency"},"micro_units":{"maximum":9223372036854775807,"minimum":0,"type":"integer"}},"required":["currency","micro_units"],"type":"object"},"PhysicalCallEvidence":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"account_identity":{"maxLength":512,"minLength":1,"type":"string"},"attempt_id":{"$ref":"#/$defs/ID"},"capability_evidence":{"$ref":"#/$defs/ArtifactRef"},"confirmation":{"enum":["authoritative_success","authoritative_failure","provider_accepted","authoritative_nonexecution","unknown"],"type":"string"},"error_code":{"maxLength":128,"minLength":0,"type":"string"},"error_message":{"maxLength":2048,"minLength":0,"type":"string"},"finished_at":{"$ref":"#/$defs/UTC"},"http_status":{"maximum":599,"minimum":100,"type":"integer"},"operation_id":{"$ref":"#/$defs/ID"},"profile_digest":{"$ref":"#/$defs/Digest"},"provider_reference":{"maxLength":1024,"minLength":0,"type":"string"},"request_context":{"$ref":"#/$defs/ArtifactLocator"},"request_sent":{"enum":["no","yes","unknown"],"type":"string"},"requested_destination":{"maxLength":4096,"minLength":1,"type":"string"},"resolved_destination":{"maxLength":4096,"minLength":1,"type":"string"},"started_at":{"$ref":"#/$defs/UTC"}},"required":["operation_id","attempt_id","account_identity","requested_destination","resolved_destination","profile_digest","capability_evidence","started_at","finished_at","request_context","request_sent","confirmation"],"type":"object"},"ProviderUsage":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"accounting":{"$ref":"#/$defs/Usage"},"billing":{"enum":["observed","bounded_estimate","unknown","advisory","no_charge"],"type":"string"},"input_rate":{"$ref":"#/$defs/RationalRate"},"input_tokens":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"output_rate":{"$ref":"#/$defs/RationalRate"},"output_tokens":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"provider_usage_reference":{"maxLength":1024,"minLength":0,"type":"string"}},"required":["accounting","billing"],"type":"object"},"RationalRate":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"denominator_units":{"maximum":9223372036854775807,"minimum":1,"type":"integer"},"numerator_micro_units":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"unit":{"enum":["input_token","output_token","request","byte","second"],"type":"string"}},"required":["numerator_micro_units","denominator_units","unit"],"type":"object"},"StagedOutput":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"classification":{"$ref":"#/$defs/Classification"},"digest":{"$ref":"#/$defs/Digest"},"media_type":{"maxLength":256,"minLength":1,"type":"string"},"purpose":{"enum":["provider_response","model_text","context","tool_result","repository","public_source","memory","verification","backup","export"],"type":"string"},"size":{"maximum":268435456,"minimum":0,"type":"integer"},"staging_ref":{"maxLength":256,"minLength":1,"type":"string"}},"required":["staging_ref","digest","size","media_type","classification","purpose"],"type":"object"},"ToolArguments":{"$schema":"https://json-schema.org/draft/2020-12/schema","description":"Strictly validate against the exact pinned tool input schema before use. Unknown tool fields fail. This open container is never an executable grant.","maxProperties":256,"type":"object"},"UTC":{"$schema":"https://json-schema.org/draft/2020-12/schema","format":"date-time","maxLength":40,"pattern":"Z$","type":"string"},"Usage":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"advisory":{"type":"boolean"},"currency":{"$ref":"#/$defs/Currency"},"estimated":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"reserved":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"spent":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"unknown":{"maximum":9223372036854775807,"minimum":0,"type":"integer"}},"required":["currency","spent","reserved","estimated","unknown","advisory"],"type":"object"}}}`

// schemaProfileBody is the zatiti.mcp/v1 profile document schema
// (MCPClientProfile), transcribed verbatim from the frozen catalog.
const schemaProfileBody = `{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"allowed_tools":{"items":{"$ref":"#/$defs/MCPToolName"},"maxItems":256,"minItems":0,"type":"array"},"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"},"classifications":{"items":{"$ref":"#/$defs/Classification"},"maxItems":3,"minItems":1,"type":"array"},"credential_kind":{"description":"bearer sends the resolved credential bytes only in the Authorization header. Credential-in-path (for example /mcp/:apiKey) and OAuth authorization-code flows are refused as capability_unsupported: the first would place the secret in the staged request record and server logs, the second has no frozen token-endpoint shape (cmd/zatiti/helper.go).","enum":["bearer","none"],"type":"string"},"max_request_bytes":{"maximum":16777216,"minimum":1,"type":"integer"},"max_response_bytes":{"maximum":268435456,"minimum":1,"type":"integer"},"protocol_version":{"type":"string","enum":["2025-11-25","2026-07-28"],"description":"The protocol version the server must negotiate. The pinned go-sdk client (v1.7.0) always probes SEP-2575 server/discover first and falls back to the legacy initialize handshake; a negotiated version other than this value fails open_session as capability_unsupported."},"schema":{"const":"zatiti.mcp/v1","type":"string"},"timeout_seconds":{"maximum":1800,"minimum":1,"type":"integer"},"tool_call_cost":{"$ref":"#/$defs/Money"},"transport":{"oneOf":[{"$ref":"#/$defs/MCPStreamableHTTPTransport"},{"$ref":"#/$defs/MCPStdioTransport"}]}},"required":["schema","transport","protocol_version","credential_kind","allowed_tools","tool_call_cost","max_request_bytes","max_response_bytes","timeout_seconds","classifications","capability_evidence"],"type":"object"}`

// schemaParametersBody is the zatiti.mcp.action/v1 document schema
// (MCPClientParameters): a kind-discriminated oneOf over the four action
// shapes.
const schemaParametersBody = `{"$schema":"https://json-schema.org/draft/2020-12/schema","oneOf":[{"$ref":"#/$defs/MCPOpenSession"},{"$ref":"#/$defs/MCPListTools"},{"$ref":"#/$defs/MCPCallTool"},{"$ref":"#/$defs/MCPCloseSession"}]}`

// schemaEvidenceBody is the zatiti.mcp.evidence/v1 document schema
// (MCPClientEvidence).
const schemaEvidenceBody = `{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"arguments_digest":{"$ref":"#/$defs/Digest"},"content_summary":{"$ref":"#/$defs/MCPContentSummary"},"handshake":{"items":{"$ref":"#/$defs/MCPHandshakeExchange"},"maxItems":3,"minItems":0,"type":"array"},"is_error":{"type":"boolean"},"kind":{"enum":["open_session","list_tools","call_tool","close_session"],"type":"string"},"next_cursor":{"maxLength":1024,"minLength":0,"type":"string"},"output_artifacts":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":2,"minItems":0,"type":"array"},"physical_call":{"$ref":"#/$defs/PhysicalCallEvidence"},"protocol_version":{"maxLength":64,"minLength":0,"type":"string"},"refused_server_requests":{"items":{"maxLength":128,"minLength":1,"type":"string"},"maxItems":32,"minItems":0,"type":"array"},"schema":{"const":"zatiti.mcp.evidence/v1","type":"string"},"server_capabilities":{"items":{"maxLength":64,"minLength":1,"type":"string"},"maxItems":32,"minItems":0,"type":"array"},"server_name":{"maxLength":256,"minLength":0,"type":"string"},"server_version":{"maxLength":128,"minLength":0,"type":"string"},"session_handle":{"$ref":"#/$defs/MCPSessionHandle"},"session_state":{"enum":["active","stateless","expired","closed","unknown"],"type":"string"},"staged_outputs":{"items":{"$ref":"#/$defs/StagedOutput"},"maxItems":2,"minItems":0,"type":"array"},"structured_content_digest":{"$ref":"#/$defs/Digest"},"tool":{"$ref":"#/$defs/MCPToolName"},"tools":{"items":{"$ref":"#/$defs/MCPDiscoveredTool"},"maxItems":256,"minItems":0,"type":"array"},"usage":{"$ref":"#/$defs/ProviderUsage"}},"required":["schema","physical_call","kind","session_state","refused_server_requests","usage","staged_outputs","output_artifacts"],"type":"object"}`

// composeOnce lazily composes the three operation-shaped documents with the
// shared $defs catalog. Composition happens once; documents are immutable.
var (
	composeOnce         sync.Once
	composedProfile     json.RawMessage
	composedParameters  json.RawMessage
	composedEvidence    json.RawMessage
	composeSchemaErrVal error
)

func composeSchemas() error {
	composeOnce.Do(func() {
		// The catalog is spliced verbatim: decoding it into json.RawMessage
		// values round-trips no number through float64, so frozen integer
		// bounds like the int64 maximum survive composition exactly.
		var doc map[string]json.RawMessage
		if err := contract.DecodeStrict([]byte(schemaDefs), &doc); err != nil {
			composeSchemaErrVal = fmt.Errorf("mcpclient: embedded $defs catalog is not valid JSON: %w", err)
			return
		}
		defs, ok := doc["$defs"]
		if !ok {
			composeSchemaErrVal = fmt.Errorf("mcpclient: embedded $defs catalog has no $defs object")
			return
		}
		var err error
		if composedProfile, err = withDefs(schemaProfileBody, defs); err != nil {
			composeSchemaErrVal = err
			return
		}
		if composedParameters, err = withDefs(schemaParametersBody, defs); err != nil {
			composeSchemaErrVal = err
			return
		}
		if composedEvidence, err = withDefs(schemaEvidenceBody, defs); err != nil {
			composeSchemaErrVal = err
			return
		}
	})
	return composeSchemaErrVal
}

// withDefs returns body with the $defs catalog injected at the document
// root, so every #/$defs/... reference resolves document-locally.
func withDefs(body string, defs json.RawMessage) (json.RawMessage, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return nil, fmt.Errorf("mcpclient: operation schema is not valid JSON: %w", err)
	}
	if _, ok := doc["$defs"]; ok {
		return nil, fmt.Errorf("mcpclient: operation schema already declares $defs: %s", body)
	}
	doc["$defs"] = defs
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("mcpclient: schema composition failed: %w", err)
	}
	return json.RawMessage(out), nil
}

// profileSchema returns the composed zatiti.mcp/v1 profile schema.
func profileSchema() (json.RawMessage, error) {
	if err := composeSchemas(); err != nil {
		return nil, err
	}
	return composedProfile, nil
}

// parametersSchema returns the composed zatiti.mcp.action/v1 schema.
func parametersSchema() (json.RawMessage, error) {
	if err := composeSchemas(); err != nil {
		return nil, err
	}
	return composedParameters, nil
}

// evidenceSchema returns the composed zatiti.mcp.evidence/v1 schema.
func evidenceSchema() (json.RawMessage, error) {
	if err := composeSchemas(); err != nil {
		return nil, err
	}
	return composedEvidence, nil
}
