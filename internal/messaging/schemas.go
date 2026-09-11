package messaging

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
	"_messaging.admit":          {schemaInMessagingAdmit, schemaOutMessage},
	"_messaging.bootstrap":      {schemaInMessagingBootstrap, schemaOutConversation},
	"_messaging.pending":        {schemaInMessagingPending, schemaOutMessages},
	"conversation.create":       {schemaInConversationCreate, schemaOutConversation},
	"conversation.get":          {schemaInConversationGet, schemaOutConversation},
	"conversation.list":         {schemaInConversationList, schemaOutConversations},
	"conversation.message.send": {schemaInConversationMessageSend, schemaOutMessage},
	"conversation.update":       {schemaInConversationUpdate, schemaOutConversation},
	"mailbox.ack":               {schemaInMailboxAck, schemaOutMessage},
	"mailbox.list":              {schemaInMailboxList, schemaOutMessages},
	"mailbox.send":              {schemaInMailboxSend, schemaOutMessage},
}

const schemaOutMessage = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Message"}},"required":["resource"]}`

const schemaOutMessages = `{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Message"},"maxItems":500}},"required":["items"]}`

const schemaOutConversation = `{"type":"object","additionalProperties":false,"properties":{"resource":{"$ref":"#/$defs/Conversation"}},"required":["resource"]}`

const schemaOutConversations = `{"type":"object","additionalProperties":false,"properties":{"items":{"type":"array","items":{"$ref":"#/$defs/Conversation"},"maxItems":500}},"required":["items"]}`

const schemaInMessagingAdmit = `{"type":"object","additionalProperties":false,"properties":{"message":{"$ref":"#/$defs/Message"}},"required":["message"]}`

const schemaInMessagingBootstrap = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"owner_id":{"type":"string","format":"uuid"},"chief_id":{"type":"string","format":"uuid"}},"required":["scope","owner_id","chief_id"]}`

const schemaInMessagingPending = `{"type":"object","additionalProperties":false,"properties":{"worker_id":{"type":"string","format":"uuid"},"limit":{"type":"integer","minimum":1,"maximum":100}},"required":["worker_id","limit"]}`

const schemaInConversationCreate = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"kind":{"type":"string","enum":["direct","group"]},"participant_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"title":{"type":"string","maxLength":8192}},"required":["scope","kind","participant_ids","title"]}`

const schemaInConversationGet = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"}},"required":["scope","id"]}`

const schemaInConversationList = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]}},"required":["scope"]}`

const schemaInConversationMessageSend = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"conversation_id":{"type":"string","format":"uuid"},"message_id":{"type":"string","format":"uuid"},"body":{"type":"string","maxLength":8192},"attachments":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"task_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096}},"required":["scope","conversation_id","message_id","body","attachments","task_ids"]}`

const schemaInConversationUpdate = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807},"participant_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096},"title":{"type":"string","maxLength":8192},"pinned":{"type":"boolean"}},"required":["scope","id","expected_version"]}`

const schemaInMailboxAck = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"message_id":{"type":"string","format":"uuid"},"recipient_id":{"type":"string","format":"uuid"},"expected_version":{"type":"integer","minimum":1,"maximum":9223372036854775807}},"required":["scope","message_id","recipient_id","expected_version"]}`

const schemaInMailboxList = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"cursor":{"type":"string","maxLength":8192},"limit":{"type":"integer","minimum":1,"maximum":200},"filter":{"type":"object","additionalProperties":false,"properties":{"state":{"type":"string","maxLength":8192},"key":{"type":"string","maxLength":8192},"parent_id":{"type":"string","format":"uuid"},"worker_id":{"type":"string","format":"uuid"},"task_id":{"type":"string","format":"uuid"},"organization_id":{"type":"string","format":"uuid"},"descendants":{"type":"boolean"},"needs_you":{"type":"boolean"}},"required":[]},"recipient_id":{"type":"string","format":"uuid"}},"required":["scope","recipient_id"]}`

const schemaInMailboxSend = `{"type":"object","additionalProperties":false,"properties":{"scope":{"$ref":"#/$defs/Scope"},"message_id":{"type":"string","format":"uuid"},"recipient_id":{"type":"string","format":"uuid"},"body":{"type":"string","maxLength":8192},"attachments":{"type":"array","items":{"$ref":"#/$defs/ArtifactRef"},"maxItems":4096},"task_ids":{"type":"array","items":{"type":"string","format":"uuid"},"maxItems":4096}},"required":["scope","message_id","recipient_id","body","attachments","task_ids"]}`

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
		panic(fmt.Sprintf("messaging: embedded schema definitions are invalid: %v", err))
	}
	defs, ok := doc["$defs"]
	if !ok {
		panic("messaging: embedded schema document has no $defs member")
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
