package httpread

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/zatiti/zatiti/internal/contract"
)

// schemaDefs is the embedded $defs catalog transcribed byte-for-byte from
// the implementation assignment's frozen JSON Schema catalog: every shared
// primitive transitively referenced by the profile, parameters and evidence
// schemas below. withDefs injects it at composition time so no JSON round
// trip decays the frozen int64 bounds.
const schemaDefs = `{"$defs":{"ArtifactLocator":{"$schema":"https://json-schema.org/draft/2020-12/schema","oneOf":[{"additionalProperties":false,"properties":{"artifact":{"$ref":"#/$defs/ArtifactRef"},"kind":{"const":"artifact","type":"string"}},"required":["kind","artifact"],"type":"object"},{"additionalProperties":false,"properties":{"digest":{"$ref":"#/$defs/Digest"},"kind":{"const":"staged","type":"string"},"staging_ref":{"maxLength":256,"minLength":1,"type":"string"}},"required":["kind","staging_ref","digest"],"type":"object"}]},"ArtifactRef":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"digest":{"$ref":"#/$defs/Digest"},"id":{"$ref":"#/$defs/ID"}},"required":["id","digest"],"type":"object"},"CapabilityEvidence":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"adapter_version":{"maxLength":128,"minLength":1,"type":"string"},"artifact":{"$ref":"#/$defs/ArtifactRef"},"capabilities":{"items":{"maxLength":128,"minLength":1,"type":"string"},"maxItems":128,"minItems":0,"type":"array"},"limitations":{"items":{"maxLength":2048,"minLength":1,"type":"string"},"maxItems":128,"minItems":0,"type":"array"},"profile_digest":{"$ref":"#/$defs/Digest"},"protocol_revision":{"maxLength":128,"minLength":1,"type":"string"},"qualified_at":{"$ref":"#/$defs/UTC"},"source_revision":{"maxLength":128,"minLength":1,"type":"string"}},"required":["artifact","adapter_version","source_revision","protocol_revision","profile_digest","qualified_at","capabilities","limitations"],"type":"object"},"Classification":{"$schema":"https://json-schema.org/draft/2020-12/schema","enum":["public","internal","restricted"],"type":"string"},"Currency":{"$schema":"https://json-schema.org/draft/2020-12/schema","maxLength":3,"pattern":"^[A-Z]{3}$","type":"string"},"Digest":{"$schema":"https://json-schema.org/draft/2020-12/schema","maxLength":64,"pattern":"^[0-9a-f]{64}$","type":"string"},"HTTPOrigin":{"$schema":"https://json-schema.org/draft/2020-12/schema","format":"uri","maxLength":2048,"pattern":"^https?://[^/?#]+$","type":"string"},"HTTPReadHeader":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"name":{"enum":["Accept","Accept-Language","If-None-Match","If-Modified-Since","User-Agent"],"type":"string"},"value":{"maxLength":1024,"pattern":"^[^\r\n]*$","type":"string"}},"required":["name","value"],"type":"object"},"HTTPURL":{"$schema":"https://json-schema.org/draft/2020-12/schema","format":"uri","maxLength":4096,"pattern":"^https?://","type":"string"},"ID":{"$schema":"https://json-schema.org/draft/2020-12/schema","format":"uuid","maxLength":36,"type":"string"},"PhysicalCallEvidence":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"account_identity":{"maxLength":512,"minLength":1,"type":"string"},"attempt_id":{"$ref":"#/$defs/ID"},"capability_evidence":{"$ref":"#/$defs/ArtifactRef"},"confirmation":{"enum":["authoritative_success","authoritative_failure","provider_accepted","authoritative_nonexecution","unknown"],"type":"string"},"error_code":{"maxLength":128,"minLength":0,"type":"string"},"error_message":{"maxLength":2048,"minLength":0,"type":"string"},"finished_at":{"$ref":"#/$defs/UTC"},"http_status":{"maximum":599,"minimum":100,"type":"integer"},"operation_id":{"$ref":"#/$defs/ID"},"profile_digest":{"$ref":"#/$defs/Digest"},"provider_reference":{"maxLength":1024,"minLength":0,"type":"string"},"request_context":{"$ref":"#/$defs/ArtifactLocator"},"request_sent":{"enum":["no","yes","unknown"],"type":"string"},"requested_destination":{"maxLength":4096,"minLength":1,"type":"string"},"resolved_destination":{"maxLength":4096,"minLength":1,"type":"string"},"started_at":{"$ref":"#/$defs/UTC"}},"required":["operation_id","attempt_id","account_identity","requested_destination","resolved_destination","profile_digest","capability_evidence","started_at","finished_at","request_context","request_sent","confirmation"],"type":"object"},"ProviderUsage":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"accounting":{"$ref":"#/$defs/Usage"},"billing":{"enum":["observed","bounded_estimate","unknown","advisory","no_charge"],"type":"string"},"input_rate":{"$ref":"#/$defs/RationalRate"},"input_tokens":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"output_rate":{"$ref":"#/$defs/RationalRate"},"output_tokens":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"provider_usage_reference":{"maxLength":1024,"minLength":0,"type":"string"}},"required":["accounting","billing"],"type":"object"},"RationalRate":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"denominator_units":{"maximum":9223372036854775807,"minimum":1,"type":"integer"},"numerator_micro_units":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"unit":{"enum":["input_token","output_token","request","byte","second"],"type":"string"}},"required":["numerator_micro_units","denominator_units","unit"],"type":"object"},"StagedOutput":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"classification":{"$ref":"#/$defs/Classification"},"digest":{"$ref":"#/$defs/Digest"},"media_type":{"maxLength":256,"minLength":1,"type":"string"},"purpose":{"enum":["provider_response","model_text","context","tool_result","repository","public_source","memory","verification","backup","export"],"type":"string"},"size":{"maximum":268435456,"minimum":0,"type":"integer"},"staging_ref":{"maxLength":256,"minLength":1,"type":"string"}},"required":["staging_ref","digest","size","media_type","classification","purpose"],"type":"object"},"UTC":{"$schema":"https://json-schema.org/draft/2020-12/schema","format":"date-time","maxLength":40,"pattern":"Z$","type":"string"},"Usage":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"advisory":{"type":"boolean"},"currency":{"$ref":"#/$defs/Currency"},"estimated":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"reserved":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"spent":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"unknown":{"maximum":9223372036854775807,"minimum":0,"type":"integer"}},"required":["currency","spent","reserved","estimated","unknown","advisory"],"type":"object"}}}`

// schemaProfileBody is the zatiti.httpread/v1 profile document schema
// (HTTPReadProfile), transcribed verbatim from the frozen catalog.
const schemaProfileBody = `{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"allowed_media_types":{"items":{"maxLength":256,"minLength":1,"type":"string"},"maxItems":128,"minItems":1,"type":"array"},"allowed_origins":{"items":{"$ref":"#/$defs/HTTPOrigin"},"maxItems":256,"minItems":1,"type":"array"},"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"},"max_bytes":{"maximum":268435456,"minimum":1,"type":"integer"},"max_redirects":{"const":0,"type":"integer"},"schema":{"const":"zatiti.httpread/v1","type":"string"},"timeout_seconds":{"maximum":1800,"minimum":1,"type":"integer"}},"required":["schema","allowed_origins","max_bytes","timeout_seconds","max_redirects","allowed_media_types","capability_evidence"],"type":"object"}`

// schemaParametersBody is the zatiti.httpread.action/v1 document schema
// (HTTPReadParameters).
const schemaParametersBody = `{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"expected_media_type":{"maxLength":256,"minLength":1,"type":"string"},"headers":{"items":{"$ref":"#/$defs/HTTPReadHeader"},"maxItems":16,"minItems":0,"type":"array"},"kind":{"const":"read","type":"string"},"method":{"const":"GET","type":"string"},"schema":{"const":"zatiti.httpread.action/v1","type":"string"},"url":{"$ref":"#/$defs/HTTPURL"}},"required":["schema","kind","url","method","headers","expected_media_type"],"type":"object"}`

// schemaEvidenceBody is the zatiti.httpread.evidence/v1 document schema
// (HTTPReadEvidence).
const schemaEvidenceBody = `{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"content_digest":{"$ref":"#/$defs/Digest"},"content_size":{"maximum":268435456,"minimum":0,"type":"integer"},"etag":{"maxLength":1024,"minLength":0,"type":"string"},"freshness":{"$ref":"#/$defs/UTC"},"last_modified":{"maxLength":128,"minLength":0,"type":"string"},"media_type":{"maxLength":256,"minLength":0,"type":"string"},"output_artifacts":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":2,"minItems":0,"type":"array"},"physical_call":{"$ref":"#/$defs/PhysicalCallEvidence"},"redirect_location":{"$ref":"#/$defs/HTTPURL"},"requested_url":{"$ref":"#/$defs/HTTPURL"},"resolved_url":{"$ref":"#/$defs/HTTPURL"},"schema":{"const":"zatiti.httpread.evidence/v1","type":"string"},"staged_outputs":{"items":{"$ref":"#/$defs/StagedOutput"},"maxItems":2,"minItems":0,"type":"array"},"status":{"maximum":599,"minimum":100,"type":"integer"},"usage":{"$ref":"#/$defs/ProviderUsage"},"validated_dial_addresses":{"items":{"maxLength":128,"minLength":1,"type":"string"},"maxItems":32,"minItems":0,"type":"array"}},"required":["schema","physical_call","requested_url","resolved_url","validated_dial_addresses","status","media_type","freshness","usage","staged_outputs","output_artifacts"],"type":"object"}`

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
			composeSchemaErrVal = fmt.Errorf("httpread: embedded $defs catalog is not valid JSON: %w", err)
			return
		}
		defs, ok := doc["$defs"]
		if !ok {
			composeSchemaErrVal = fmt.Errorf("httpread: embedded $defs catalog has no $defs object")
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
		return nil, fmt.Errorf("httpread: operation schema is not valid JSON: %w", err)
	}
	if _, ok := doc["$defs"]; ok {
		return nil, fmt.Errorf("httpread: operation schema already declares $defs: %s", body)
	}
	doc["$defs"] = defs
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("httpread: schema composition failed: %w", err)
	}
	return json.RawMessage(out), nil
}

// profileSchema returns the composed zatiti.httpread/v1 profile schema.
func profileSchema() (json.RawMessage, error) {
	if err := composeSchemas(); err != nil {
		return nil, err
	}
	return composedProfile, nil
}

// parametersSchema returns the composed zatiti.httpread.action/v1 schema.
func parametersSchema() (json.RawMessage, error) {
	if err := composeSchemas(); err != nil {
		return nil, err
	}
	return composedParameters, nil
}

// evidenceSchema returns the composed zatiti.httpread.evidence/v1 schema.
func evidenceSchema() (json.RawMessage, error) {
	if err := composeSchemas(); err != nil {
		return nil, err
	}
	return composedEvidence, nil
}
