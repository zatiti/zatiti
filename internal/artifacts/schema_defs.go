package artifacts

import (
	"encoding/json"
	"sync"

	"github.com/zatiti/zatiti/internal/contract"
)

// schemaDefs is the embedded $defs catalog of the implementation
// assignment, spliced byte-for-byte below. Operation schemas resolve their
// #/$defs/... references against this catalog; withDefs injects it at
// composition time so no JSON round trip decays the frozen integer bounds.
const schemaDefs = `{"$defs":{"Action":{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"tool":{"$ref":"#/$defs/Ref"},"connection":{"$ref":"#/$defs/Ref"},"account_identity":{"type":"string","maxLength":8192},"destination":{"type":"string","maxLength":8192},"content":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"not_before":{"type":"string","format":"date-time"},"expires_at":{"type":"string","format":"date-time"},"preconditions":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"configuration_revision":{"type":"integer","minimum":1,"maximum":9223372036854775807},"parameters":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"cost_bound":{"$ref":"#/$defs/Money"}},"required":["scope","tool","connection","account_identity","destination","content","not_before","expires_at","preconditions","configuration_revision","parameters","cost_bound"]},"Artifact":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"size":{"type":"integer","minimum":0,"maximum":9223372036854775807},"media_type":{"type":"string","maxLength":8192},"classification":{"type":"string","enum":["internal","public","restricted"]},"encrypted":{"type":"boolean"},"state":{"type":"string","enum":["available","fault"]},"created_at":{"type":"string","format":"date-time"}},"required":["id","version","scope","digest","size","media_type","classification","encrypted","state","created_at"]},"ArtifactRef":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["id","digest"]},"Disposition":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"state":{"type":"string","maxLength":8192},"job":{"$ref":"#/$defs/Job"},"operation":{"$ref":"#/$defs/Operation"},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096}},"required":["id","version","state"]},"Job":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"kind":{"type":"string","maxLength":8192},"state":{"type":"string","enum":["pending","running","succeeded","failed","outcome_unknown","cancelled"]},"requirements":{"type":"array","items":{"$ref":"#/$defs/Requirement"},"maxItems":4096},"result_artifact":{"$ref":"#/$defs/ArtifactRef"},"operation_id":{"type":"string","format":"uuid"},"owner":{"type":"string","maxLength":8192},"operation":{"type":"string","maxLength":8192},"result":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","version","kind","state","requirements","owner","operation"]},"Money":{"type":"object","additionalProperties":false,"properties":{"currency":{"type":"string","pattern":"^[A-Z]{3}$"},"micro_units":{"type":"integer","minimum":0,"maximum":9223372036854775807}},"required":["currency","micro_units"]},"Operation":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"action":{"$ref":"#/$defs/Action"},"action_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"state":{"type":"string","enum":["prepared","awaiting_review","ready","executing","awaiting_confirmation","outcome_unknown","succeeded","failed","denied","expired","cancelled"]},"attempt_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"linked_operation_id":{"type":"string","format":"uuid"},"relationship":{"type":"string","enum":["retry","reconciliation","compensation","replacement"]}},"required":["id","version","action","action_digest","state","attempt_ids"]},"Ref":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["id","version"]},"Requirement":{"type":"object","additionalProperties":false,"properties":{"code":{"type":"string","maxLength":8192},"message":{"type":"string","maxLength":8192},"resource_id":{"type":"string","format":"uuid"},"challenge_id":{"type":"string","format":"uuid"}},"required":["code","message"]},"Scope":{"type":"object","additionalProperties":false,"properties":{"installation_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"project_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"}},"required":["installation_id"]},"Upload":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"scope":{"$ref":"#/$defs/Scope"},"expected_size":{"type":"integer","minimum":0,"maximum":9223372036854775807},"expected_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"received_size":{"type":"integer","minimum":0,"maximum":9223372036854775807},"expires_at":{"type":"string","format":"date-time"},"state":{"type":"string","enum":["open","finished","cancelled","expired"]}},"required":["id","version","scope","expected_size","expected_digest","received_size","expires_at","state"]}}}
`

// composedSchemas lazily composes every operation schema document with the
// shared $defs catalog. Composition happens once; documents are immutable.
var (
	composeOnce        sync.Once
	composedIn         map[string]json.RawMessage
	composedOut        map[string]json.RawMessage
	composedCompletion map[string]json.RawMessage
)

func composeSchemas() {
	composeOnce.Do(func() {
		// The catalog is spliced verbatim: decoding it into any would
		// round-trip every number through float64 and corrupt integer
		// boundaries such as the int64 maximum on expected_version.
		var doc map[string]json.RawMessage
		if err := contract.DecodeStrict([]byte(schemaDefs), &doc); err != nil {
			panic("artifacts: embedded $defs catalog is not valid JSON: " + err.Error())
		}
		defs, ok := doc["$defs"]
		if !ok {
			panic("artifacts: embedded $defs catalog has no $defs object")
		}
		composedIn = make(map[string]json.RawMessage, len(operationSchemaBodies))
		composedOut = make(map[string]json.RawMessage, len(operationSchemaBodies))
		for id, bodies := range operationSchemaBodies {
			composedIn[id] = withDefs(bodies.input, defs)
			composedOut[id] = withDefs(bodies.output, defs)
		}
		composedCompletion = make(map[string]json.RawMessage, len(completionBodies))
		for id, body := range completionBodies {
			composedCompletion[id] = withDefs(body, defs)
		}
	})
}

// withDefs returns body with the $defs catalog injected at the document
// root, so every #/$defs/... reference resolves document-locally. The
// catalog rides as raw JSON: encoding/json splices json.RawMessage values
// byte-for-byte, so schema integers survive composition exactly.
func withDefs(body string, defs json.RawMessage) json.RawMessage {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		panic("artifacts: operation schema is not valid JSON: " + err.Error())
	}
	if _, ok := doc["$defs"]; ok {
		panic("artifacts: operation schema already declares $defs: " + body)
	}
	doc["$defs"] = defs
	out, err := json.Marshal(doc)
	if err != nil {
		panic("artifacts: schema composition failed: " + err.Error())
	}
	return json.RawMessage(out)
}

// inputSchema returns the composed input schema document for op.
func inputSchema(op string) json.RawMessage {
	composeSchemas()
	return composedIn[op]
}

// outputSchema returns the composed output schema document for op.
func outputSchema(op string) json.RawMessage {
	composeSchemas()
	return composedOut[op]
}

// completionSchema returns the composed eventual result schema for an
// asynchronous operation, or nil.
func completionSchema(op string) json.RawMessage {
	composeSchemas()
	return composedCompletion[op]
}
