package skills

import (
	"encoding/json"
	"sync"

	"github.com/zatiti/zatiti/internal/contract"
)

// Operation input/output schema bodies, embedded verbatim from the
// implementation assignment. Each body resolves its #/$defs/... references
// against schemaDefs; withDefs composes the complete document at
// registration time so contract.ValidateSchema sees document-local defs.
const (
	schemaCandidateIn = `{"type":"object","additionalProperties":false,"properties":{"candidate":{"$ref":"#/$defs/Candidate"}},"required":["candidate"]}`
	schemaVersionsOut = `{"type":"object","additionalProperties":false,"properties":{"versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096}},"required":["versions"]}`
	schemaValidateOut = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Validation"}},"required":["resource"]}`

	schemaGetIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}`

	schemaListIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}`

	schemaArchiveIn  = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version"]}`
	schemaArchiveOut = `{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/Skill"}},"required":["draft","resource"]}`

	schemaEvaluateIn     = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"skill":{"$ref":"#/$defs/Ref"},"acceptance":{"$ref":"#/$defs/Acceptance"},"profile":{"$ref":"#/$defs/ExecutionProfile"},"limits":{"$ref":"#/$defs/Limits"}},"required":["scope","skill","acceptance","profile","limits"]}`
	schemaJobResourceOut = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Job"}},"required":["resource"]}`

	schemaImportIn  = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"artifact":{"$ref":"#/$defs/ArtifactRef"},"source":{"type":"string","maxLength":8192},"license":{"type":"string","maxLength":8192},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","artifact","source","license"]}`
	schemaImportOut = `{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Skill"}},"required":["resource"]},{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}]}`
)

// schema builders compose parameterized bodies over the shared defs. They are
// package-level vars (not consts) because composition needs function calls;
// the results feed operationSchemaBodies at init time.
var (
	schemaGetOut = func(resource string) string {
		return `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/` + resource + `"}},"required":["resource"]}`
	}
	schemaListOut = func(resource string) string {
		return `{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/` + resource + `"},"maxItems":500}},"required":["items"]}`
	}
)

// opSchema is one operation's declared input/output bodies.
type opSchemas struct {
	input  string
	output string
}

// operationSchemaBodies holds every operation catalog entry exactly as the
// assignment embeds it. Internal operations precede public ones.
var operationSchemaBodies = map[string]opSchemas{
	// Internal operations.
	"_skills.activate": {schemaCandidateIn, schemaVersionsOut},
	"_skills.validate": {schemaCandidateIn, schemaValidateOut},

	"skill.archive":           {schemaArchiveIn, schemaArchiveOut},
	"skill.evaluate":          {schemaEvaluateIn, schemaJobResourceOut},
	"skill.evaluation.status": {schemaGetIn, schemaJobResourceOut},
	"skill.get":               {schemaGetIn, schemaGetOut("Skill")},
	"skill.import":            {schemaImportIn, schemaImportOut},
	"skill.list":              {schemaListIn, schemaListOut("Skill")},
}

// completionBodies declares the eventual job result schema of every
// asynchronous operation, registered as the descriptor completion_schema.
var completionBodies = map[string]string{
	"skill.import":   `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Skill"}},"required":["resource"]}`,
	"skill.evaluate": `{"type":"object","additionalProperties":false,"properties":{"evaluation_id":{"type":"string","format":"uuid"},"passed":{"type":"boolean"},"evidence":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096}},"required":["evaluation_id","passed","evidence"]}`,
}

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
			panic("skills: embedded $defs catalog is not valid JSON: " + err.Error())
		}
		defs, ok := doc["$defs"]
		if !ok {
			panic("skills: embedded $defs catalog has no $defs object")
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
		panic("skills: operation schema is not valid JSON: " + err.Error())
	}
	if _, ok := doc["$defs"]; ok {
		panic("skills: operation schema already declares $defs: " + body)
	}
	doc["$defs"] = defs
	out, err := json.Marshal(doc)
	if err != nil {
		panic("skills: schema composition failed: " + err.Error())
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
