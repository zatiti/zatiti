package github

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/zatiti/zatiti/internal/contract"
)

// schemaDefs is the embedded $defs catalog transcribed byte-for-byte from
// the implementation assignment's frozen JSON Schema catalog: every shared
// primitive and GitHub-specific definition transitively referenced by the
// profile, parameters and evidence schemas below. withDefs injects it at
// composition time so no JSON round trip decays the frozen int64 bounds.
const schemaDefs = `{"$defs":{"ArtifactRef":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"digest":{"$ref":"#/$defs/Digest"},"id":{"$ref":"#/$defs/ID"}},"required":["id","digest"],"type":"object"},"AutomationConstraints":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"allow_automatic_merge":{"type":"boolean"},"allow_external_notifications":{"type":"boolean"},"allowed_deployment_environments":{"items":{"maxLength":256,"minLength":1,"type":"string"},"maxItems":128,"minItems":0,"type":"array"},"allowed_workflows":{"items":{"maxLength":512,"minLength":1,"type":"string"},"maxItems":256,"minItems":0,"type":"array"},"evidence":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":256,"minItems":0,"type":"array"},"unknown_automation":{"enum":["deny","review"],"type":"string"}},"required":["allowed_workflows","allowed_deployment_environments","allow_external_notifications","allow_automatic_merge","unknown_automation","evidence"],"type":"object"},"CapabilityEvidence":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"adapter_version":{"maxLength":128,"minLength":1,"type":"string"},"artifact":{"$ref":"#/$defs/ArtifactRef"},"capabilities":{"items":{"maxLength":128,"minLength":1,"type":"string"},"maxItems":128,"minItems":0,"type":"array"},"limitations":{"items":{"maxLength":2048,"minLength":1,"type":"string"},"maxItems":128,"minItems":0,"type":"array"},"profile_digest":{"$ref":"#/$defs/Digest"},"protocol_revision":{"maxLength":128,"minLength":1,"type":"string"},"qualified_at":{"$ref":"#/$defs/UTC"},"source_revision":{"maxLength":128,"minLength":1,"type":"string"}},"required":["artifact","adapter_version","source_revision","protocol_revision","profile_digest","qualified_at","capabilities","limitations"],"type":"object"},"Classification":{"$schema":"https://json-schema.org/draft/2020-12/schema","enum":["public","internal","restricted"],"type":"string"},"Currency":{"$schema":"https://json-schema.org/draft/2020-12/schema","maxLength":3,"pattern":"^[A-Z]{3}$","type":"string"},"Digest":{"$schema":"https://json-schema.org/draft/2020-12/schema","maxLength":64,"pattern":"^[0-9a-f]{64}$","type":"string"},"GitBranch":{"$schema":"https://json-schema.org/draft/2020-12/schema","description":"Validate as a safe Git branch/ref name; reject traversal, control characters and invalid Git ref syntax.","maxLength":1024,"minLength":1,"type":"string"},"GitHubCreateBranch":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"automation_constraints":{"$ref":"#/$defs/AutomationConstraints"},"base_sha":{"$ref":"#/$defs/GitSHA"},"branch":{"$ref":"#/$defs/GitBranch"},"expected_absent":{"const":"required","type":"string"},"kind":{"const":"create_branch","type":"string"},"preflight_evidence":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":64,"minItems":1,"type":"array"},"repository":{"$ref":"#/$defs/Repository"},"schema":{"const":"zatiti.github.action/v1","type":"string"}},"required":["schema","repository","kind","branch","base_sha","expected_absent","automation_constraints","preflight_evidence"],"type":"object"},"GitHubEvidence":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"automation_evidence":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":256,"minItems":0,"type":"array"},"branch":{"$ref":"#/$defs/GitBranch"},"commit_sha":{"$ref":"#/$defs/GitSHA"},"kind":{"enum":["read_repository","create_branch","push_commit","open_pull_request","merge_pull_request"],"type":"string"},"observed_base_sha":{"$ref":"#/$defs/GitSHA"},"observed_head_sha":{"$ref":"#/$defs/GitSHA"},"output_artifacts":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":256,"minItems":0,"type":"array"},"physical_call":{"$ref":"#/$defs/PhysicalCallEvidence"},"pull_request_number":{"maximum":9223372036854775807,"minimum":1,"type":"integer"},"pull_request_url":{"$ref":"#/$defs/HTTPSURL"},"repository":{"$ref":"#/$defs/Repository"},"schema":{"const":"zatiti.github.evidence/v1","type":"string"},"staged_outputs":{"items":{"$ref":"#/$defs/StagedOutput"},"maxItems":256,"minItems":0,"type":"array"},"usage":{"$ref":"#/$defs/ProviderUsage"}},"required":["schema","physical_call","kind","repository","usage","staged_outputs","output_artifacts"],"type":"object"},"GitHubIdempotencyProfile":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"equivalence_fields":{"items":{"maxLength":128,"minLength":1,"type":"string"},"maxItems":64,"minItems":0,"type":"array"},"evidence":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":64,"minItems":0,"type":"array"},"mode":{"enum":["none","qualified_key","authoritative_nonexecution"],"type":"string"},"retention_seconds":{"maximum":9223372036854775807,"minimum":0,"type":"integer"}},"required":["mode","retention_seconds","equivalence_fields","evidence"],"type":"object"},"GitHubMergePullRequest":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"automation_constraints":{"$ref":"#/$defs/AutomationConstraints"},"commit_body":{"$ref":"#/$defs/ArtifactRef"},"commit_title":{"$ref":"#/$defs/ArtifactRef"},"expected_base_sha":{"$ref":"#/$defs/GitSHA"},"expected_head_sha":{"$ref":"#/$defs/GitSHA"},"kind":{"const":"merge_pull_request","type":"string"},"merge_method":{"enum":["merge","squash","rebase"],"type":"string"},"preflight_evidence":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":64,"minItems":1,"type":"array"},"pull_request_number":{"maximum":9223372036854775807,"minimum":1,"type":"integer"},"repository":{"$ref":"#/$defs/Repository"},"schema":{"const":"zatiti.github.action/v1","type":"string"}},"required":["schema","repository","kind","pull_request_number","expected_head_sha","expected_base_sha","merge_method","commit_title","commit_body","automation_constraints","preflight_evidence"],"type":"object"},"GitHubOpenPullRequest":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"automation_constraints":{"$ref":"#/$defs/AutomationConstraints"},"base_branch":{"$ref":"#/$defs/GitBranch"},"base_sha":{"$ref":"#/$defs/GitSHA"},"body":{"$ref":"#/$defs/ArtifactRef"},"draft":{"type":"boolean"},"head_branch":{"$ref":"#/$defs/GitBranch"},"head_sha":{"$ref":"#/$defs/GitSHA"},"kind":{"const":"open_pull_request","type":"string"},"patch":{"$ref":"#/$defs/ArtifactRef"},"preflight_evidence":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":64,"minItems":1,"type":"array"},"repository":{"$ref":"#/$defs/Repository"},"schema":{"const":"zatiti.github.action/v1","type":"string"},"title":{"$ref":"#/$defs/ArtifactRef"}},"required":["schema","repository","kind","head_branch","head_sha","base_branch","base_sha","title","body","patch","draft","automation_constraints","preflight_evidence"],"type":"object"},"GitHubProfile":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"allowed_actions":{"items":{"enum":["read_repository","create_branch","push_commit","open_pull_request","merge_pull_request"],"type":"string"},"maxItems":5,"minItems":1,"type":"array"},"allowed_repositories":{"items":{"$ref":"#/$defs/Repository"},"maxItems":256,"minItems":1,"type":"array"},"api_base":{"$ref":"#/$defs/HTTPSURL"},"automation_constraints":{"$ref":"#/$defs/AutomationConstraints"},"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"},"idempotency_profile":{"$ref":"#/$defs/GitHubIdempotencyProfile"},"max_response_bytes":{"maximum":268435456,"minimum":1,"type":"integer"},"schema":{"const":"zatiti.github/v1","type":"string"},"timeout_seconds":{"maximum":1800,"minimum":1,"type":"integer"}},"required":["schema","api_base","allowed_repositories","allowed_actions","max_response_bytes","timeout_seconds","idempotency_profile","automation_constraints","capability_evidence"],"type":"object"},"GitHubPushCommit":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"automation_constraints":{"$ref":"#/$defs/AutomationConstraints"},"base_sha":{"$ref":"#/$defs/GitSHA"},"branch":{"$ref":"#/$defs/GitBranch"},"content_artifacts":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096,"minItems":0,"type":"array"},"expected_head_sha":{"$ref":"#/$defs/GitSHA"},"force":{"const":false,"type":"boolean"},"kind":{"const":"push_commit","type":"string"},"patch":{"$ref":"#/$defs/ArtifactRef"},"preflight_evidence":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":64,"minItems":1,"type":"array"},"prepared_commit_sha":{"$ref":"#/$defs/GitSHA"},"repository":{"$ref":"#/$defs/Repository"},"schema":{"const":"zatiti.github.action/v1","type":"string"}},"required":["schema","repository","kind","branch","expected_head_sha","prepared_commit_sha","base_sha","patch","content_artifacts","force","automation_constraints","preflight_evidence"],"type":"object"},"GitHubReadRepository":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"branch":{"$ref":"#/$defs/GitBranch"},"kind":{"const":"read_repository","type":"string"},"path":{"$ref":"#/$defs/RepositoryPath"},"preflight_for_operation_id":{"$ref":"#/$defs/ID"},"pull_request_number":{"maximum":9223372036854775807,"minimum":1,"type":"integer"},"repository":{"$ref":"#/$defs/Repository"},"resource":{"enum":["metadata","ref","commit","tree","blob","pull_request"],"type":"string"},"schema":{"const":"zatiti.github.action/v1","type":"string"},"sha":{"$ref":"#/$defs/GitSHA"}},"required":["schema","repository","kind","resource"],"type":"object"},"GitSHA":{"$schema":"https://json-schema.org/draft/2020-12/schema","maxLength":64,"pattern":"^([0-9a-f]{40}|[0-9a-f]{64})$","type":"string"},"HTTPSURL":{"$schema":"https://json-schema.org/draft/2020-12/schema","format":"uri","maxLength":4096,"pattern":"^https://","type":"string"},"ID":{"$schema":"https://json-schema.org/draft/2020-12/schema","format":"uuid","maxLength":36,"type":"string"},"PhysicalCallEvidence":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"account_identity":{"maxLength":512,"minLength":1,"type":"string"},"attempt_id":{"$ref":"#/$defs/ID"},"capability_evidence":{"$ref":"#/$defs/ArtifactRef"},"confirmation":{"enum":["authoritative_success","authoritative_failure","provider_accepted","authoritative_nonexecution","unknown"],"type":"string"},"error_code":{"maxLength":128,"minLength":0,"type":"string"},"error_message":{"maxLength":2048,"minLength":0,"type":"string"},"finished_at":{"$ref":"#/$defs/UTC"},"http_status":{"maximum":599,"minimum":100,"type":"integer"},"operation_id":{"$ref":"#/$defs/ID"},"profile_digest":{"$ref":"#/$defs/Digest"},"provider_reference":{"maxLength":1024,"minLength":0,"type":"string"},"request_context":{"$ref":"#/$defs/ArtifactRef"},"request_sent":{"enum":["no","yes","unknown"],"type":"string"},"requested_destination":{"maxLength":4096,"minLength":1,"type":"string"},"resolved_destination":{"maxLength":4096,"minLength":1,"type":"string"},"started_at":{"$ref":"#/$defs/UTC"}},"required":["operation_id","attempt_id","account_identity","requested_destination","resolved_destination","profile_digest","capability_evidence","started_at","finished_at","request_context","request_sent","confirmation"],"type":"object"},"ProviderUsage":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"accounting":{"$ref":"#/$defs/Usage"},"billing":{"enum":["observed","bounded_estimate","unknown","advisory","no_charge"],"type":"string"},"input_rate":{"$ref":"#/$defs/RationalRate"},"input_tokens":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"output_rate":{"$ref":"#/$defs/RationalRate"},"output_tokens":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"provider_usage_reference":{"maxLength":1024,"minLength":0,"type":"string"}},"required":["accounting","billing"],"type":"object"},"RationalRate":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"denominator_units":{"maximum":9223372036854775807,"minimum":1,"type":"integer"},"numerator_micro_units":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"unit":{"enum":["input_token","output_token","request","byte","second"],"type":"string"}},"required":["numerator_micro_units","denominator_units","unit"],"type":"object"},"Repository":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"name":{"pattern":"^[A-Za-z0-9_.-]{1,100}$","type":"string"},"owner":{"pattern":"^[A-Za-z0-9][A-Za-z0-9-]{0,99}$","type":"string"}},"required":["owner","name"],"type":"object"},"RepositoryPath":{"$schema":"https://json-schema.org/draft/2020-12/schema","description":"Normalized relative repository path; reject absolute paths, dot/dot-dot components, NUL, backslash ambiguity and path escapes.","maxLength":4096,"minLength":1,"type":"string"},"StagedOutput":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"classification":{"$ref":"#/$defs/Classification"},"digest":{"$ref":"#/$defs/Digest"},"media_type":{"maxLength":256,"minLength":1,"type":"string"},"purpose":{"enum":["provider_response","model_text","context","tool_result","repository","public_source","memory","verification","backup","export"],"type":"string"},"size":{"maximum":268435456,"minimum":0,"type":"integer"},"staging_ref":{"maxLength":256,"minLength":1,"type":"string"}},"required":["staging_ref","digest","size","media_type","classification","purpose"],"type":"object"},"UTC":{"$schema":"https://json-schema.org/draft/2020-12/schema","format":"date-time","maxLength":40,"pattern":"Z$","type":"string"},"Usage":{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"advisory":{"type":"boolean"},"currency":{"$ref":"#/$defs/Currency"},"estimated":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"reserved":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"spent":{"maximum":9223372036854775807,"minimum":0,"type":"integer"},"unknown":{"maximum":9223372036854775807,"minimum":0,"type":"integer"}},"required":["currency","spent","reserved","estimated","unknown","advisory"],"type":"object"}}}
`

// schemaProfileBody is the zatiti.github/v1 profile document schema
// (GitHubProfile), transcribed verbatim from the frozen catalog.
const schemaProfileBody = `{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"allowed_actions":{"items":{"enum":["read_repository","create_branch","push_commit","open_pull_request","merge_pull_request"],"type":"string"},"maxItems":5,"minItems":1,"type":"array"},"allowed_repositories":{"items":{"$ref":"#/$defs/Repository"},"maxItems":256,"minItems":1,"type":"array"},"api_base":{"$ref":"#/$defs/HTTPSURL"},"automation_constraints":{"$ref":"#/$defs/AutomationConstraints"},"capability_evidence":{"$ref":"#/$defs/CapabilityEvidence"},"idempotency_profile":{"$ref":"#/$defs/GitHubIdempotencyProfile"},"max_response_bytes":{"maximum":268435456,"minimum":1,"type":"integer"},"schema":{"const":"zatiti.github/v1","type":"string"},"timeout_seconds":{"maximum":1800,"minimum":1,"type":"integer"}},"required":["schema","api_base","allowed_repositories","allowed_actions","max_response_bytes","timeout_seconds","idempotency_profile","automation_constraints","capability_evidence"],"type":"object"}`

// schemaParametersBody is the zatiti.github.action/v1 discriminated union
// (GitHubParameters): exactly one of the five action kinds.
const schemaParametersBody = `{"$schema":"https://json-schema.org/draft/2020-12/schema","oneOf":[{"$ref":"#/$defs/GitHubReadRepository"},{"$ref":"#/$defs/GitHubCreateBranch"},{"$ref":"#/$defs/GitHubPushCommit"},{"$ref":"#/$defs/GitHubOpenPullRequest"},{"$ref":"#/$defs/GitHubMergePullRequest"}]}`

// schemaEvidenceBody is the zatiti.github.evidence/v1 document schema
// (GitHubEvidence).
const schemaEvidenceBody = `{"$schema":"https://json-schema.org/draft/2020-12/schema","additionalProperties":false,"properties":{"automation_evidence":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":256,"minItems":0,"type":"array"},"branch":{"$ref":"#/$defs/GitBranch"},"commit_sha":{"$ref":"#/$defs/GitSHA"},"kind":{"enum":["read_repository","create_branch","push_commit","open_pull_request","merge_pull_request"],"type":"string"},"observed_base_sha":{"$ref":"#/$defs/GitSHA"},"observed_head_sha":{"$ref":"#/$defs/GitSHA"},"output_artifacts":{"items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":256,"minItems":0,"type":"array"},"physical_call":{"$ref":"#/$defs/PhysicalCallEvidence"},"pull_request_number":{"maximum":9223372036854775807,"minimum":1,"type":"integer"},"pull_request_url":{"$ref":"#/$defs/HTTPSURL"},"repository":{"$ref":"#/$defs/Repository"},"schema":{"const":"zatiti.github.evidence/v1","type":"string"},"staged_outputs":{"items":{"$ref":"#/$defs/StagedOutput"},"maxItems":256,"minItems":0,"type":"array"},"usage":{"$ref":"#/$defs/ProviderUsage"}},"required":["schema","physical_call","kind","repository","usage","staged_outputs","output_artifacts"],"type":"object"}`

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
			composeSchemaErrVal = fmt.Errorf("github: embedded $defs catalog is not valid JSON: %w", err)
			return
		}
		defs, ok := doc["$defs"]
		if !ok {
			composeSchemaErrVal = fmt.Errorf("github: embedded $defs catalog has no $defs object")
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
		return nil, fmt.Errorf("github: operation schema is not valid JSON: %w", err)
	}
	if _, ok := doc["$defs"]; ok {
		return nil, fmt.Errorf("github: operation schema already declares $defs: %s", body)
	}
	doc["$defs"] = defs
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("github: schema composition failed: %w", err)
	}
	return json.RawMessage(out), nil
}

// profileSchema returns the composed zatiti.github/v1 profile schema.
func profileSchema() (json.RawMessage, error) {
	if err := composeSchemas(); err != nil {
		return nil, err
	}
	return composedProfile, nil
}

// parametersSchema returns the composed zatiti.github.action/v1 schema.
func parametersSchema() (json.RawMessage, error) {
	if err := composeSchemas(); err != nil {
		return nil, err
	}
	return composedParameters, nil
}

// evidenceSchema returns the composed zatiti.github.evidence/v1 schema.
func evidenceSchema() (json.RawMessage, error) {
	if err := composeSchemas(); err != nil {
		return nil, err
	}
	return composedEvidence, nil
}
