package responses

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/zatiti/zatiti/internal/contract"
)

// schemaDefs is the embedded $defs catalog transcribed byte-for-byte from
// the implementation assignment's frozen JSON Schema catalog: every shared
// primitive and Responses/context definition transitively referenced by the
// profile, parameters, evidence and context schemas below. withDefs injects
// it at composition time so no JSON round trip decays the frozen int64
// bounds.
const schemaDefs = `{"$defs":{"ArtifactLocator":{"$schema":"https://json-schema.org/draft/2020-12/schema","oneOf":[{"additionalProperties":false,"properties":{"artifact":{"$ref":"#/$defs/ArtifactRef"},"kind":{"const":"artifact","type":"string"}},"required":["kind","artifact"],"type":"object"},{"additionalProperties":false,"properties":{"digest":{"$ref":"#/$defs/Digest"},"kind":{"const":"staged","type":"string"},"staging_ref":{"maxLength":256,"minLength":1,"type":"string"}},"required":["kind","staging_ref","digest"],"type":"object"}]},"ArtifactRef":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"digest":{"$ref":"#/$defs/Digest"},"id":{"$ref":"#/$defs/ID"}},"required":["id","digest"],"type":"object"},"BoundEnforcement":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"classifications":{"items":{"$ref":"#/$defs/Classification"},"maxItems":3,"minItems":1,"type":"array"},"cost":{"enum":["enforced","advisory","unsupported"],"type":"string"},"disclosure":{"enum":["enforced","advisory","unsupported"],"type":"string"},"evidence":{"$ref":"#/$defs/CapabilityEvidence"},"maximum_cost":{"$ref":"#/$defs/Money"},"provider_destinations":{"items":{"$ref":"#/$defs/HTTPSURL"},"maxItems":64,"minItems":0,"type":"array"}},"required":["cost","disclosure","maximum_cost","provider_destinations","classifications","evidence"],"type":"object"},"CapabilityEvidence":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"adapter_version":{"maxLength":128,"minLength":1,"type":"string"},"artifact":{"$ref":"#/$defs/ArtifactRef"},"capabilities":{"items":{"maxLength":128,"minLength":1,"type":"string"},"maxItems":128,"minItems":0,"type":"array"},"limitations":{"items":{"maxLength":2048,"minLength":1,"type":"string"},"maxItems":128,"minItems":0,"type":"array"},"profile_digest":{"$ref":"#/$defs/Digest"},"protocol_revision":{"maxLength":128,"minLength":1,"type":"string"},"qualified_at":{"$ref":"#/$defs/UTC"},"source_revision":{"maxLength":128,"minLength":1,"type":"string"}},"required":["artifact","adapter_version","source_revision","protocol_revision","profile_digest","qualified_at","capabilities","limitations"],"type":"object"},"Classification":{"$schema":"https://json-schema.org/draft/2020-12/schema","enum":["public","internal","restricted"],"type":"string"},"ContextArtifactPart":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"artifact":{"$ref":"#/$defs/ArtifactRef"},"classification":{"$ref":"#/$defs/Classification"},"kind":{"const":"artifact","type":"string"},"label":{"maxLength":256,"minLength":0,"type":"string"},"media_type":{"maxLength":256,"minLength":1,"type":"string"}},"required":["kind","artifact","media_type","classification"],"type":"object"},"ContextMemoryExcerpt":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"brain_id":{"$ref":"#/$defs/ID"},"claim":{"$ref":"#/$defs/VersionRef"},"confidence":{"maximum":1000000,"minimum":0,"type":"integer"},"freshness":{"$ref":"#/$defs/UTC"},"kind":{"const":"memory_excerpt","type":"string"},"scope":{"$ref":"#/$defs/Scope"},"selected_context":{"$ref":"#/$defs/ArtifactRef"},"sources":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":256,"minItems":0,"type":"array"},"text":{"maxLength":262144,"minLength":0,"type":"string"}},"required":["kind","brain_id","claim","text","sources","confidence","freshness","scope","selected_context"],"type":"object"},"ContextMessage":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"created_at":{"$ref":"#/$defs/UTC"},"id":{"$ref":"#/$defs/ID"},"message_id":{"$ref":"#/$defs/ID"},"origin":{"enum":["effective_instruction","user_message","model_output","tool_result","memory_recall","agent_message","compaction"],"type":"string"},"parts":{"items":{"$ref":"#/$defs/ContextPart"},"maxItems":512,"minItems":1,"type":"array"},"role":{"enum":["system","developer","user","assistant","tool"],"type":"string"},"sender_id":{"$ref":"#/$defs/ID"},"source_artifacts":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":512,"minItems":0,"type":"array"}},"required":["id","role","origin","parts","source_artifacts"],"type":"object"},"ContextPart":{"$schema":"https://json-schema.org/draft/2020-12/schema","oneOf":[{"$ref":"#/$defs/ContextText"},{"$ref":"#/$defs/ContextArtifactPart"},{"$ref":"#/$defs/ContextToolCall"},{"$ref":"#/$defs/ContextToolResult"},{"$ref":"#/$defs/ContextMemoryExcerpt"}]},"ContextText":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"kind":{"const":"text","type":"string"},"text":{"maxLength":262144,"minLength":0,"type":"string"}},"required":["kind","text"],"type":"object"},"ContextToolCall":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"kind":{"const":"tool_call","type":"string"},"proposal":{"$ref":"#/$defs/ModelToolProposal"}},"required":["kind","proposal"],"type":"object"},"ContextToolDefinition":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"binding_id":{"$ref":"#/$defs/ID"},"description":{"maxLength":16384,"minLength":0,"type":"string"},"destinations":{"items":{"maxLength":4096,"minLength":1,"type":"string"},"maxItems":64,"minItems":0,"type":"array"},"effect":{"enum":["local","disclosure","external_read","external_mutation"],"type":"string"},"input_schema":{"$ref":"#/$defs/InertSchema"},"name":{"maxLength":128,"minLength":1,"type":"string"},"output_schema":{"$ref":"#/$defs/InertSchema"},"schema_digest":{"$ref":"#/$defs/Digest"},"tool":{"$ref":"#/$defs/VersionRef"}},"required":["tool","name","description","input_schema","output_schema","effect","destinations","binding_id","schema_digest"],"type":"object"},"ContextToolResult":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"artifact":{"$ref":"#/$defs/ArtifactRef"},"error_code":{"maxLength":128,"minLength":0,"type":"string"},"kind":{"const":"tool_result","type":"string"},"operation_id":{"$ref":"#/$defs/ID"},"proposal_id":{"maxLength":256,"minLength":1,"type":"string"},"status":{"enum":["completed","accepted","failed"],"type":"string"}},"required":["kind","proposal_id","operation_id","status","artifact"],"type":"object"},"Currency":{"$schema":"https://json-schema.org/draft/2020-12/schema","maxLength":3,"pattern":"^[A-Z]{3}$","type":"string"},"Digest":{"$schema":"https://json-schema.org/draft/2020-12/schema","maxLength":64,"pattern":"^[0-9a-f]{64}$","type":"string"},"HTTPSURL":{"$schema":"https://json-schema.org/draft/2020-12/schema","format":"uri","maxLength":4096,"pattern":"^https://","type":"string"},"ID":{"$schema":"https://json-schema.org/draft/2020-12/schema","format":"uuid","maxLength":36,"type":"string"},"InertSchema":{"$schema":"https://json-schema.org/draft/2020-12/schema","description":"A bounded JSON Schema 2020-12 document. Local references only; validators reject remote references, duplicate keys, excessive depth/node count and unsupported executable behavior. This is schema data, not authority.","maxProperties":256,"type":"object"},"ModelOutput":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"continuation_reference":{"maxLength":1024,"minLength":0,"type":"string"},"finish_reason":{"enum":["completed","tool_calls","length_limit","refused","interrupted","failed","unknown"],"type":"string"},"refusal":{"maxLength":8192,"minLength":0,"type":"string"},"request_context":{"$ref":"#/$defs/ArtifactLocator"},"response_id":{"maxLength":1024,"minLength":0,"type":"string"},"schema":{"const":"zatiti.model-output/v1","type":"string"},"text_outputs":{"items":{"$ref":"#/$defs/ArtifactLocator"},"maxItems":256,"minItems":0,"type":"array"},"tool_proposals":{"items":{"$ref":"#/$defs/ModelToolProposal"},"maxItems":256,"minItems":0,"type":"array"},"usage":{"$ref":"#/$defs/ProviderUsage"}},"required":["schema","response_id","request_context","finish_reason","text_outputs","tool_proposals","usage"],"type":"object"},"ModelToolProposal":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"explanation":{"maxLength":8192,"minLength":0,"type":"string"},"id":{"maxLength":256,"minLength":1,"type":"string"},"input":{"$ref":"#/$defs/ToolArguments"},"operation_id":{"maxLength":128,"minLength":1,"type":"string"},"operation_version":{"$ref":"#/$defs/Version"},"source_context":{"$ref":"#/$defs/ArtifactRef"},"tool":{"$ref":"#/$defs/VersionRef"}},"required":["id","tool","operation_id","operation_version","input","source_context"],"type":"object"},"Money":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"currency":{"$ref":"#/$defs/Currency"},"micro_units":{"maximum":9223372036854775807,"minimum":0,"type":"integer"}},"required":["currency","micro_units"],"type":"object"},"PhysicalCallEvidence":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"account_identity":{"maxLength":512,"minLength":1,"type":"string"},"attempt_id":{"$ref":"#/$defs/ID"},"capability_evidence":{"$ref":"#/$defs/ArtifactRef"},"confirmation":{"enum":["authoritative_success","authoritative_failure","provider_accepted","authoritative_nonexecution","unknown"],"type":"string"},"error_code":{"maxLength":128,"minLength":0,"type":"string"},"error_message":{"maxLength":2048,"minLength":0,"type":"string"},"finished_at":{"$ref":"#/$defs/UTC"},"http_status":{"maximum":599,"minimum":100,"type":"integer"},"operation_id":{"$ref":"#/$defs/ID"},"profile_digest":{"$ref":"#/$defs/Digest"},"provider_reference":{"maxLength":1024,"minLength":0,"type":"string"},"request_context":{"$ref":"#/$defs/ArtifactLocator"},"request_sent":{"enum":["no","yes","unknown"],"type":"string"},"requested_destination":{"maxLength":4096,"minLength":1,"type":"string"},"resolved_destination":{"maxLength":4096,"minLength":1,"type":"string"},"started_at":{"$ref":"#/$defs/UTC"}},"required":["operation_id","attempt_id","account_identity","requested_destination","resolved_destination","profile_digest","capability_evidence","started_at","finished_at","request_context","request_sent","confirmation"],"type":"object"},"ProviderUsage":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"accounting":{"$ref":"#/$defs/Usage"},"billing":{"enum":["observed","bounded_estimate","unknown","advisory","no_charge"],"type":"string"},"input_rate":{"$ref":"#/$defs/RationalRate"},"input_tokens":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"output_rate":{"$ref":"#/$defs/RationalRate"},"output_tokens":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"provider_usage_reference":{"maxLength":1024,"minLength":0,"type":"string"}},"required":["accounting","billing"],"type":"object"},"RationalRate":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"denominator_units":{"maximum":9223372036854775807,"minimum":1,"type":"integer"},"numerator_micro_units":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"unit":{"enum":["input_token","output_token","request","byte","second"],"type":"string"}},"required":["numerator_micro_units","denominator_units","unit"],"type":"object"},"Scope":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"installation_id":{"$ref":"#/$defs/ID"},"organization_id":{"$ref":"#/$defs/ID"},"project_id":{"$ref":"#/$defs/ID"},"task_id":{"$ref":"#/$defs/ID"},"worker_id":{"$ref":"#/$defs/ID"}},"required":["installation_id"],"type":"object"},"StagedOutput":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"classification":{"$ref":"#/$defs/Classification"},"digest":{"$ref":"#/$defs/Digest"},"media_type":{"maxLength":256,"minLength":1,"type":"string"},"purpose":{"enum":["provider_response","model_text","context","tool_result","repository","public_source","memory","verification","backup","export"],"type":"string"},"size":{"maximum":268435456,"minimum":0,"type":"integer"},"staging_ref":{"maxLength":256,"minLength":1,"type":"string"}},"required":["staging_ref","digest","size","media_type","classification","purpose"],"type":"object"},"ToolArguments":{"$schema":"https://json-schema.org/draft/2020-12/schema","description":"Strictly validate against the exact pinned tool input schema before use. Unknown tool fields fail. This open container is never an executable grant.","maxProperties":256,"type":"object"},"UTC":{"$schema":"https://json-schema.org/draft/2020-12/schema","format":"date-time","maxLength":40,"pattern":"Z$","type":"string"},"Usage":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"advisory":{"type":"boolean"},"currency":{"$ref":"#/$defs/Currency"},"estimated":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"reserved":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"spent":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"unknown":{"maximum":9223372036854775807,"minimum":0,"type":"integer"}},"required":["currency","spent","reserved","estimated","unknown","advisory"],"type":"object"},"Version":{"$schema":"https://json-schema.org/draft/2020-12/schema","maximum":9223372036854775807,"minimum":1,"type":"integer"},"VersionRef":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"id":{"$ref":"#/$defs/ID"},"version":{"$ref":"#/$defs/Version"}},"required":["id","version"],"type":"object"}}}`

// schemaProfileBody is the zatiti.responses/v1 profile document schema
// (ResponsesProfile), transcribed verbatim from the frozen catalog.
const schemaProfileBody = `{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"},"connection_id":{"$ref":"#/$defs/ID"},"currency":{"$ref":"#/$defs/Currency"},"endpoint":{"$ref":"#/$defs/HTTPSURL"},"enforcement":{"$ref":"#/$defs/BoundEnforcement"},"input_rate":{"$ref":"#/$defs/RationalRate"},"max_input_tokens":{"maximum":10000000,"minimum":1,"type":"integer"},"max_output_tokens":{"maximum":1000000,"minimum":1,"type":"integer"},"max_response_bytes":{"maximum":268435456,"minimum":1,"type":"integer"},"model":{"maxLength":256,"minLength":1,"type":"string"},"output_rate":{"$ref":"#/$defs/RationalRate"},"schema":{"const":"zatiti.responses/v1","type":"string"},"timeout_seconds":{"maximum":1800,"minimum":1,"type":"integer"}},"required":["schema","endpoint","model","connection_id","max_input_tokens","max_output_tokens","max_response_bytes","timeout_seconds","currency","input_rate","output_rate","enforcement","capability_evidence"],"type":"object"}`

// schemaParametersBody is the zatiti.responses.action/v1 document schema
// (ResponsesParameters).
const schemaParametersBody = `{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"context_artifact":{"$ref":"#/$defs/ArtifactRef"},"continuation_reference":{"maxLength":1024,"minLength":0,"type":"string"},"kind":{"const":"model_step","type":"string"},"max_output_tokens":{"maximum":1000000,"minimum":1,"type":"integer"},"schema":{"const":"zatiti.responses.action/v1","type":"string"},"tool_contract_versions":{"items":{"$ref":"#/$defs/VersionRef"},"maxItems":256,"minItems":0,"type":"array"}},"required":["schema","kind","context_artifact","max_output_tokens","tool_contract_versions"],"type":"object"}`

// schemaEvidenceBody is the zatiti.responses.evidence/v1 document schema
// (ResponsesEvidence).
const schemaEvidenceBody = `{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"output":{"$ref":"#/$defs/ModelOutput"},"output_artifacts":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":256,"minItems":0,"type":"array"},"physical_call":{"$ref":"#/$defs/PhysicalCallEvidence"},"response_id":{"maxLength":1024,"minLength":0,"type":"string"},"schema":{"const":"zatiti.responses.evidence/v1","type":"string"},"staged_outputs":{"items":{"$ref":"#/$defs/StagedOutput"},"maxItems":256,"minItems":0,"type":"array"}},"required":["schema","physical_call","response_id","output","staged_outputs","output_artifacts"],"type":"object"}`

// schemaContextBody is the zatiti.context/v1 document schema
// (ContextArtifact): the persisted model-visible request context an action's
// context_artifact must contain.
const schemaContextBody = `{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"attempt_id":{"$ref":"#/$defs/ID"},"capture":{"enum":["complete","partial","advisory"],"type":"string"},"compaction":{"additionalProperties":false,"properties":{"compactor_profile":{"$ref":"#/$defs/VersionRef"},"source_contexts":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":256,"minItems":1,"type":"array"},"summary_artifact":{"$ref":"#/$defs/ArtifactRef"}},"required":["source_contexts","compactor_profile","summary_artifact"],"type":"object"},"configuration_revision":{"$ref":"#/$defs/Version"},"created_at":{"$ref":"#/$defs/UTC"},"execution_profile":{"$ref":"#/$defs/VersionRef"},"messages":{"items":{"$ref":"#/$defs/ContextMessage"},"maxItems":4096,"minItems":0,"type":"array"},"schema":{"const":"zatiti.context/v1","type":"string"},"scope":{"$ref":"#/$defs/Scope"},"skill_versions":{"items":{"$ref":"#/$defs/VersionRef"},"maxItems":256,"minItems":0,"type":"array"},"source_artifacts":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096,"minItems":0,"type":"array"},"tools":{"items":{"$ref":"#/$defs/ContextToolDefinition"},"maxItems":256,"minItems":0,"type":"array"},"worker":{"$ref":"#/$defs/VersionRef"}},"required":["schema","attempt_id","scope","configuration_revision","worker","execution_profile","skill_versions","messages","tools","source_artifacts","capture","created_at"],"type":"object"}`

// composeOnce lazily composes the four operation-shaped documents with the
// shared $defs catalog. Composition happens once; documents are immutable.
var (
	composeOnce         sync.Once
	composedProfile     json.RawMessage
	composedParameters  json.RawMessage
	composedEvidence    json.RawMessage
	composedContext     json.RawMessage
	composeSchemaErrVal error
)

func composeSchemas() error {
	composeOnce.Do(func() {
		// The catalog is spliced verbatim: decoding it into json.RawMessage
		// values round-trips no number through float64, so frozen integer
		// bounds like the int64 maximum survive composition exactly.
		var doc map[string]json.RawMessage
		if err := contract.DecodeStrict([]byte(schemaDefs), &doc); err != nil {
			composeSchemaErrVal = fmt.Errorf("responses: embedded $defs catalog is not valid JSON: %w", err)
			return
		}
		defs, ok := doc["$defs"]
		if !ok {
			composeSchemaErrVal = fmt.Errorf("responses: embedded $defs catalog has no $defs object")
			return
		}
		targets := []struct {
			body string
			dst  *json.RawMessage
		}{
			{schemaProfileBody, &composedProfile},
			{schemaParametersBody, &composedParameters},
			{schemaEvidenceBody, &composedEvidence},
			{schemaContextBody, &composedContext},
		}
		for _, t := range targets {
			composed, err := withDefs(t.body, defs)
			if err != nil {
				composeSchemaErrVal = err
				return
			}
			*t.dst = composed
		}
	})
	return composeSchemaErrVal
}

// withDefs returns body with the $defs catalog injected at the document
// root, so every #/$defs/... reference resolves document-locally.
func withDefs(body string, defs json.RawMessage) (json.RawMessage, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return nil, fmt.Errorf("responses: operation schema is not valid JSON: %w", err)
	}
	if _, ok := doc["$defs"]; ok {
		return nil, fmt.Errorf("responses: operation schema already declares $defs: %s", body)
	}
	doc["$defs"] = defs
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("responses: schema composition failed: %w", err)
	}
	return json.RawMessage(out), nil
}

// profileSchema returns the composed zatiti.responses/v1 profile schema.
func profileSchema() (json.RawMessage, error) {
	if err := composeSchemas(); err != nil {
		return nil, err
	}
	return composedProfile, nil
}

// parametersSchema returns the composed zatiti.responses.action/v1 schema.
func parametersSchema() (json.RawMessage, error) {
	if err := composeSchemas(); err != nil {
		return nil, err
	}
	return composedParameters, nil
}

// evidenceSchema returns the composed zatiti.responses.evidence/v1 schema.
func evidenceSchema() (json.RawMessage, error) {
	if err := composeSchemas(); err != nil {
		return nil, err
	}
	return composedEvidence, nil
}

// contextSchema returns the composed zatiti.context/v1 schema.
func contextSchema() (json.RawMessage, error) {
	if err := composeSchemas(); err != nil {
		return nil, err
	}
	return composedContext, nil
}
