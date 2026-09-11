package connections

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
	schemaValidateOut = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Validation"}},"required":["resource"]}`
	schemaActivateOut = `{"type":"object","additionalProperties":false,"properties":{"versions":{"type":"array","items":{"$ref":"#/$defs/Ref"},"maxItems":4096}},"required":["versions"]}`

	schemaGetIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}`

	schemaListIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}`

	schemaArchiveIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version"]}`

	schemaRevokeIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","id","expected_version"]}`

	schemaRotateIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"store_ref":{"type":"string","maxLength":8192}},"required":["scope","id","expected_version","store_ref"]}`

	schemaSetupBeginIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"connection_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"method":{"type":"string","enum":["browser","store_reference"]}},"required":["scope","connection_id","expected_version","method"]}`

	schemaSetupCancelIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"challenge_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","challenge_id","expected_version"]}`

	schemaSetupCompleteIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"challenge_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"helper_ref":{"type":"string","maxLength":8192}},"required":["scope","challenge_id","expected_version","helper_ref"]}`

	schemaSetupStatusIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"challenge_id":{"type":"string","format":"uuid"}},"required":["scope","challenge_id"]}`

	schemaValidationRecordIn = `{"type":"object","additionalProperties":false,"properties":{"connection_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"observation":{"$ref":"#/$defs/Observation"}},"required":["connection_id","expected_version","observation"]}`

	schemaConnectionDef = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"provider":{"type":"string","maxLength":8192},"account_identity":{"type":"string","maxLength":8192},"credential_ref":{"type":"string","maxLength":8192},"destinations":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"allowed_scopes":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096}},"required":["scope","provider","account_identity","credential_ref","destinations","allowed_scopes"]}`

	schemaBindIn = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"binding":{"$ref":"#/$defs/Binding"},"draft_id":{"type":"string","format":"uuid"}},"required":["scope","binding"]}`

	schemaToolSchemaOut = `{"type":"object","additionalProperties":false,"properties":{"input_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"output_schema":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["input_schema","output_schema"]}`

	schemaSetupOut = `{"oneOf":[{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Challenge"}},"required":["resource"]},{"type":"object","additionalProperties":false,"properties":{"job":{"$ref":"#/$defs/Job"}},"required":["job"]}]}`
)

// schema builders compose parameterized bodies over the shared defs.
var (
	schemaGetOut = func(resource string) string {
		return `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/` + resource + `"}},"required":["resource"]}`
	}
	schemaListOut = func(resource string) string {
		return `{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/` + resource + `"},"maxItems":500}},"required":["items"]}`
	}
	schemaCreateIn = func(def string) string {
		return `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":` + def + `,"draft_id":{"type":"string","format":"uuid"}},"required":["scope","definition"]}`
	}
	schemaCreateOut = func(resource string) string {
		return `{"type":"object","additionalProperties":false,"properties":{"draft":{"$ref":"#/$defs/Draft"},"resource":{"$ref":"#/$defs/` + resource + `"}},"required":["draft","resource"]}`
	}
	schemaUpdateIn = func(def string) string {
		return `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"definition":` + def + `,"draft_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","definition"]}`
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
	"_connections.activate":          {schemaCandidateIn, schemaActivateOut},
	"_connections.resolve":           {`{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"connection":{"$ref":"#/$defs/Ref"},"tool":{"$ref":"#/$defs/Ref"},"destination":{"type":"string","maxLength":8192}},"required":["scope","connection","tool","destination"]}`, `{"type":"object","additionalProperties":false,"properties":{"connection":{"$ref":"#/$defs/Connection"},"tool":{"$ref":"#/$defs/Tool"}},"required":["connection","tool"]}`},
	"_connections.validate":          {schemaCandidateIn, schemaValidateOut},
	"_connections.validation.record": {schemaValidationRecordIn, schemaGetOut("Connection")},

	// connection.*
	"connection.archive": {schemaArchiveIn, schemaCreateOut("Connection")},
	"connection.create":  {schemaCreateIn(schemaConnectionDef), schemaCreateOut("Connection")},
	"connection.get":     {schemaGetIn, schemaGetOut("Connection")},
	"connection.list":    {schemaListIn, schemaListOut("Connection")},
	"connection.revoke":  {schemaRevokeIn, schemaGetOut("Disposition")},
	"connection.rotate":  {schemaRotateIn, schemaGetOut("Job")},
	"connection.setup.begin": {
		schemaSetupBeginIn,
		schemaSetupOut,
	},
	"connection.setup.cancel":   {schemaSetupCancelIn, schemaSetupOut},
	"connection.setup.complete": {schemaSetupCompleteIn, schemaSetupOut},
	"connection.setup.status":   {schemaSetupStatusIn, schemaGetOut("Challenge")},
	"connection.update":         {schemaUpdateIn(schemaConnectionDef), schemaCreateOut("Connection")},
	"connection.validate":       {schemaRevokeIn, schemaGetOut("Job")},

	// tool.*
	"tool.bind":   {schemaBindIn, schemaGetOut("Draft")},
	"tool.get":    {schemaGetIn, schemaGetOut("Tool")},
	"tool.list":   {schemaListIn, schemaListOut("Tool")},
	"tool.schema": {schemaGetIn, schemaToolSchemaOut},
	"tool.unbind": {schemaBindIn, schemaGetOut("Draft")},
}

// completionOps maps the asynchronous operations to their eventual job result
// schema, declared separately from the immediate response.
var completionOps = map[string]string{
	"connection.rotate":         `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Connection"}},"required":["resource"]}`,
	"connection.validate":       `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Connection"}},"required":["resource"]}`,
	"connection.setup.begin":    `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Challenge"}},"required":["resource"]}`,
	"connection.setup.cancel":   `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Challenge"}},"required":["resource"]}`,
	"connection.setup.complete": `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Challenge"}},"required":["resource"]}`,
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
		// boundaries such as the int64 maximum.
		var doc map[string]json.RawMessage
		if err := contract.DecodeStrict([]byte(schemaDefs), &doc); err != nil {
			panic("connections: embedded $defs catalog is not valid JSON: " + err.Error())
		}
		defs, ok := doc["$defs"]
		if !ok {
			panic("connections: embedded $defs catalog has no $defs object")
		}
		composedIn = make(map[string]json.RawMessage, len(operationSchemaBodies))
		composedOut = make(map[string]json.RawMessage, len(operationSchemaBodies))
		for id, bodies := range operationSchemaBodies {
			composedIn[id] = withDefs(bodies.input, defs)
			composedOut[id] = withDefs(bodies.output, defs)
		}
		composedCompletion = make(map[string]json.RawMessage, len(completionOps))
		for id, body := range completionOps {
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
		panic("connections: operation schema is not valid JSON: " + err.Error())
	}
	if _, ok := doc["$defs"]; ok {
		panic("connections: operation schema already declares $defs: " + body)
	}
	doc["$defs"] = defs
	out, err := json.Marshal(doc)
	if err != nil {
		panic("connections: schema composition failed: " + err.Error())
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
