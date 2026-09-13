package evidence

import (
	"encoding/json"
	"fmt"
)

// Wire schemas, embedded verbatim from the implementation assignment
// (internal/evidence/AGENTS.md). The shared $defs document resolves every
// document-local $ref of the operation schema bodies below; mergeSchema
// splices one body into a copy of the document so contract.ValidateSchema
// sees a complete schema.

// wireDefs is the exact $defs document from internal/evidence/AGENTS.md.
const wireDefs = `{"$defs":{"Command":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"principal_id":{"type":"string","format":"uuid"},"operation":{"type":"string","maxLength":8192},"operation_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"submission_key":{"type":"string","maxLength":8192},"request_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"},"status":{"type":"string","enum":["completed","accepted","failed"]},"data":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},"error_code":{"type":"string","maxLength":8192},"result":{"$ref":"#/$defs/Result"}},"required":["id","principal_id","operation","operation_version","submission_key","request_digest","status","data","result"]},"Event":{"type":"object","additionalProperties":false,"properties":{"id":{"type":"string","format":"uuid"},"sequence":{"type":"integer","minimum":1,"maximum":9223372036854775807},"at":{"type":"string","format":"date-time"},"scope":{"$ref":"#/$defs/Scope"},"kind":{"type":"string","maxLength":8192},"resource_id":{"type":"string","format":"uuid"},"resource_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"data":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["id","sequence","at","scope","kind","resource_id","resource_version","data"]},"Fault":{"type":"object","additionalProperties":false,"properties":{"code":{"type":"string","maxLength":8192},"message":{"type":"string","maxLength":8192},"retryable":{"type":"boolean"},"details":{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."}},"required":["code","message","retryable"]},"Result":{"type":"object","additionalProperties":false,"properties":{"schema":{"const":"zatiti.result/v1"},"command_id":{"type":"string","format":"uuid"},"status":{"type":"string","enum":["completed","accepted","failed"]},"data":{"anyOf":[{"type":"object","description":"Inert JSON data bounded by the enclosing size limit; never executable authority."},{"type":"null"}]},"error":{"anyOf":[{"$ref":"#/$defs/Fault"},{"type":"null"}]},"next_cursor":{"anyOf":[{"type":"string","maxLength":8192},{"type":"null"}]}},"required":["schema","command_id","status","data","error","next_cursor"]},"Scope":{"type":"object","additionalProperties":false,"properties":{"installation_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"project_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"}},"required":["installation_id"]}}}`

// Operation input/output schema bodies, embedded verbatim from the
// implementation assignment.
const (
	schemaCommandBeginIn   = `{"type":"object","additionalProperties":false,"properties":{"principal_id":{"type":"string","format":"uuid"},"operation":{"type":"string","maxLength":8192},"operation_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"submission_key":{"type":"string","maxLength":8192},"request_digest":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["principal_id","operation","operation_version","submission_key","request_digest"]}`
	schemaCommandBeginOut  = `{"type":"object","additionalProperties":false,"properties":{"command_id":{"type":"string","format":"uuid"},"existing":{"$ref":"#/$defs/Command"}},"required":["command_id"]}`
	schemaCommandFinishIn  = `{"type":"object","additionalProperties":false,"properties":{"command_id":{"type":"string","format":"uuid"},"result":{"$ref":"#/$defs/Result"}},"required":["command_id","result"]}`
	schemaCommandFinishOut = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Command"}},"required":["resource"]}`
	schemaSnapshotIn       = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"}},"required":["scope"]}`
	schemaSnapshotOut      = `{"type":"object","additionalProperties":false,"properties":{"last_sequence":{"type":"integer","minimum":0,"maximum":9223372036854775807}},"required":["last_sequence"]}`
	schemaCommandGetIn     = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"submission_key":{"type":"string","maxLength":8192},"operation":{"type":"string","maxLength":8192},"operation_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","submission_key","operation","operation_version"]}`
	schemaCommandGetOut    = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Command"}},"required":["resource"]}`
	schemaEventGetIn       = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}`
	schemaEventGetOut      = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Event"}},"required":["resource"]}`
	schemaEventListIn      = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":500},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}`
	schemaEventListOut     = `{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Event"},"maxItems":500}},"required":["items"]}`
)

// wireSchema holds one operation's merged input and output documents.
type wireSchema struct {
	Input  string
	Output string
}

// wireSchemas merges every owned operation body with the shared $defs
// document.
var wireSchemas = map[string]wireSchema{
	opCommandBegin:  {schemaCommandBeginIn, schemaCommandBeginOut},
	opCommandFinish: {schemaCommandFinishIn, schemaCommandFinishOut},
	opSnapshot:      {schemaSnapshotIn, schemaSnapshotOut},
	opCommandGet:    {schemaCommandGetIn, schemaCommandGetOut},
	opEventGet:      {schemaEventGetIn, schemaEventGetOut},
	opEventList:     {schemaEventListIn, schemaEventListOut},
}

// mergeSchema splices an operation schema body into the shared $defs
// document, producing one schema document whose document-local $refs resolve
// against the exact brief definitions.
func mergeSchema(body string) (json.RawMessage, error) {
	if len(body) < 2 || body[0] != '{' || body[len(body)-1] != '}' {
		return nil, fmt.Errorf("schema body is not a JSON object")
	}
	defsLen := len(wireDefs)
	if defsLen < 2 || wireDefs[defsLen-1] != '}' {
		return nil, fmt.Errorf("embedded $defs document is malformed")
	}
	merged := wireDefs[:defsLen-1] + "," + body[1:]
	return json.RawMessage(merged), nil
}
