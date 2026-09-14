package mcp

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/zatiti/zatiti/internal/contract"
)

// defsFile is the frozen shared $defs object from AGENTS.md's "Local schema
// definitions" section, copied verbatim (defs_test.go asserts it stays
// byte-identical to the committed contract). Every operation schema's
// "#/$defs/Name" reference resolves against exactly these definitions once
// they are merged into a tool's advertised schema document.
//
//go:embed defs.json
var defsFile []byte

var (
	sharedDefsOnce sync.Once
	sharedDefs     json.RawMessage
	sharedDefsErr  error
)

// loadSharedDefs parses the embedded defs.json exactly once and returns the
// inner $defs object. A failure here is a build-time asset defect, not a
// runtime condition; it is still returned as an error (never panicked)
// because this package is a library, not a program entrypoint.
func loadSharedDefs() (json.RawMessage, error) {
	sharedDefsOnce.Do(func() {
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal(defsFile, &wrapper); err != nil {
			sharedDefsErr = fmt.Errorf("mcp: embedded defs.json is not valid JSON: %w", err)
			return
		}
		defs, ok := wrapper["$defs"]
		if !ok {
			sharedDefsErr = fmt.Errorf("mcp: embedded defs.json does not hold a $defs object")
			return
		}
		sharedDefs = defs
	})
	return sharedDefs, sharedDefsErr
}

// submissionKeySchema is the frozen submission_key shape (wire conventions:
// 1..128 printable ASCII characters). It is added to a tool's input schema
// only when the descriptor itself requires a submission key.
const submissionKeySchema = `{"type":"string","minLength":1,"maxLength":128,"pattern":"^[ -~]{1,128}$"}`

// wrapInputSchema builds the tool's advertised input schema: the common
// request envelope restricted to the fields an MCP caller may set (never a
// credential profile, never the request schema constant, which this package
// fills itself), with "input" typed exactly as the operation's own input
// schema. defs is merged in at the document root so every "#/$defs/Name"
// reference inside the operation schema resolves.
func wrapInputSchema(d contract.Descriptor, defs json.RawMessage) (json.RawMessage, error) {
	if len(d.InputSchema) == 0 {
		return nil, fmt.Errorf("operation %s declares no input schema", d.ID)
	}
	properties := map[string]json.RawMessage{"input": d.InputSchema}
	required := []string{"input"}
	if d.SubmissionKey {
		properties["submission_key"] = json.RawMessage(submissionKeySchema)
		required = append(required, "submission_key")
	}
	doc := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
		"$defs":                defs,
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("operation %s: encoding input schema: %w", d.ID, err)
	}
	return out, nil
}

// Fixed shapes for the fields of the common result envelope other than
// "data", which wrapOutputSchema fills with the operation's own output
// schema (or null, for a failed result never carries typed data).
var (
	resultSchemaProperty     = json.RawMessage(`{"const":"zatiti.result/v1","type":"string"}`)
	resultCommandIDProperty  = json.RawMessage(`{"type":"string"}`)
	resultStatusProperty     = json.RawMessage(`{"type":"string","enum":["completed","accepted","failed"]}`)
	resultErrorProperty      = json.RawMessage(`{"anyOf":[{"$ref":"#/$defs/Fault"},{"type":"null"}]}`)
	resultNextCursorProperty = json.RawMessage(`{"anyOf":[{"type":"string"},{"type":"null"}]}`)
)

// wrapOutputSchema builds the tool's advertised output schema: the common
// result envelope with "data" typed as the operation's own output schema
// (nullable, since a failed result carries no data). defs is merged in at
// the document root for the same reason as wrapInputSchema.
func wrapOutputSchema(d contract.Descriptor, defs json.RawMessage) (json.RawMessage, error) {
	if len(d.OutputSchema) == 0 {
		return nil, fmt.Errorf("operation %s declares no output schema", d.ID)
	}
	dataSchema, err := json.Marshal(map[string]any{
		"anyOf": []json.RawMessage{d.OutputSchema, json.RawMessage(`{"type":"null"}`)},
	})
	if err != nil {
		return nil, fmt.Errorf("operation %s: encoding output data schema: %w", d.ID, err)
	}
	properties := map[string]json.RawMessage{
		"schema":      resultSchemaProperty,
		"command_id":  resultCommandIDProperty,
		"status":      resultStatusProperty,
		"data":        dataSchema,
		"error":       resultErrorProperty,
		"next_cursor": resultNextCursorProperty,
	}
	doc := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             []string{"schema", "command_id", "status", "data", "error", "next_cursor"},
		"$defs":                defs,
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("operation %s: encoding output schema: %w", d.ID, err)
	}
	return out, nil
}
