package tasks

import (
	"encoding/json"
	"fmt"
)

// Per-operation input and output schema bodies. The embedded $defs document
// (schema_defs.go) carries the frozen local definitions; each operation
// schema is its exact body from the implementation contract composed onto
// those definitions. Composition is bytewise splicing of the $defs member
// into the operation schema so the wire document is deterministic.

// opSchemas holds the input/output schema bodies for every owned operation.
var opSchemas = map[string][2]string{
	"_tasks.create":     {schemaInTasksCreate, schemaOutResource},
	"_tasks.ready":      {schemaInTasksReady, schemaOutItems},
	"_tasks.snapshot":   {schemaInScopeID, schemaOutResource},
	"_tasks.transition": {schemaInTasksTransition, schemaOutResource},

	"task.accept":       {schemaInTaskAccept, schemaOutResource},
	"task.assign":       {schemaInTaskAssign, schemaOutResource},
	"task.cancel":       {schemaInTaskCancel, schemaOutResource},
	"task.create":       {schemaInTaskCreate, schemaOutResource},
	"task.delegate":     {schemaInTaskDelegate, schemaOutResource},
	"task.dependencies": {schemaInScopeID, schemaOutDependencies},
	"task.get":          {schemaInScopeID, schemaOutResource},
	"task.list":         {schemaInTaskList, schemaOutItems},
	"task.retry":        {schemaInTaskRetry, schemaOutResource},
	"task.update":       {schemaInTaskUpdate, schemaOutResource},
}

const schemaOutResource = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Task"}},"required":["resource"]}`

const schemaOutItems = `{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Task"},"maxItems":500}},"required":["items"]}`

const schemaOutDependencies = `{"type":"object","additionalProperties":false,"properties":{"dependencies":{"type":"array","items":{"$ref":"#/$defs/Task"},"maxItems":4096}},"required":["dependencies"]}`

const schemaInScopeID = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}`

const schemaInTasksCreate = `{"type":"object","additionalProperties":false,"properties":{"task":{"$ref":"#/$defs/Task"},"source_id":{"type":"string","format":"uuid"},"occurrence_key":{"type":"string","maxLength":8192}},"required":["task","source_id","occurrence_key"]}`

const schemaInTasksReady = `{"type":"object","additionalProperties":false,"properties":{"limit":{"type":"integer","minimum":1,"maximum":100}},"required":["limit"]}`

const schemaInTasksTransition = `{"type":"object","additionalProperties":false,"properties":{"task_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"state":{"type":"string","enum":["draft","ready","running","waiting","verifying","succeeded","failed","cancelled"]},"evidence_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"waiting_reason":{"type":"string","maxLength":8192},"manual":{"type":"boolean"}},"required":["task_id","expected_version","state","evidence_ids"]}`

const schemaInTaskAccept = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"decision":{"type":"string","enum":["accept","reject"]},"evidence_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"reason":{"type":"string","maxLength":8192}},"required":["scope","id","expected_version","decision","evidence_ids","reason"]}`

const schemaInTaskAssign = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"worker_id":{"type":"string","format":"uuid"}},"required":["scope","id","expected_version","worker_id"]}`

const schemaInTaskCancel = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"reason":{"type":"string","maxLength":8192}},"required":["scope","id","expected_version","reason"]}`

const taskDefinitionSchema = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"owner_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"outcome":{"type":"string","maxLength":8192},"inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"required_outputs":{"type":"array","items":{"type":"string","maxLength":8192},"maxItems":4096},"acceptance":{"$ref":"#/$defs/Acceptance"},"limits":{"$ref":"#/$defs/Limits"},"dependencies":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"parent_id":{"type":"string","format":"uuid"},"root_id":{"type":"string","format":"uuid"},"waiting_reason":{"type":"string","maxLength":8192},"cancellation_requested":{"type":"boolean"},"manual_acceptance":{"type":"boolean"}},"required":["scope","owner_id","worker_id","outcome","inputs","required_outputs","acceptance","limits","dependencies"]}`

const schemaInTaskCreate = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"definition":` + taskDefinitionSchema + `},"required":["scope","definition"]}`

const schemaInTaskDelegate = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"child":` + taskDefinitionSchema + `},"required":["scope","id","expected_version","child"]}`

const schemaInTaskList = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}`

const schemaInTaskRetry = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"reason":{"type":"string","maxLength":8192}},"required":["scope","id","expected_version","reason"]}`

const schemaInTaskUpdate = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"inputs":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"outcome":{"type":"string","maxLength":8192}},"required":["scope","id","expected_version","inputs"]}`

// withDefs composes an operation schema body with the embedded $defs
// document. Both bodies are exact JSON objects; the splice inserts the
// $defs member after the opening brace.
func withDefs(body string) json.RawMessage {
	return json.RawMessage(`{"$defs":` + defsMember + `,` + body[1:])
}

// defsMember is the raw "$defs" object extracted from the embedded document
// (schema_defs.go holds the full document; this is its top-level member).
var defsMember = mustDefsMember()

// mustDefsMember extracts the "$defs":{...} member from schemaDefs at
// package init, failing loudly if the embedded document is malformed.
func mustDefsMember() string {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(schemaDefs), &doc); err != nil {
		panic(fmt.Sprintf("tasks: embedded schema definitions are invalid: %v", err))
	}
	defs, ok := doc["$defs"]
	if !ok {
		panic("tasks: embedded schema document has no $defs member")
	}
	return string(defs)
}

// schemaCache holds composed schemas, filled once at package init so
// concurrent dispatch only reads.
var schemaCache = map[string]json.RawMessage{}

func init() {
	for op, pair := range opSchemas {
		schemaCache["in:"+op] = withDefs(pair[0])
		schemaCache["out:"+op] = withDefs(pair[1])
	}
}

// inputSchema returns the composed input schema for an operation, or nil
// when the operation is unknown.
func inputSchema(op string) json.RawMessage {
	return schemaCache["in:"+op]
}

// outputSchema returns the composed output schema for an operation, or nil
// when the operation is unknown.
func outputSchema(op string) json.RawMessage {
	return schemaCache["out:"+op]
}
