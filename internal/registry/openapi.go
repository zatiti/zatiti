package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

// OpenAPI emits the OpenAPI 3.1 description of the public surface. One POST
// path per public operation at /v1/operations/{operation_id}: the common
// request envelope as request body (with the operation's input schema),
// the common result envelope as responses (completed 200, explicit accepted
// 202 for mutations, failed default with the Fault schema), and every
// schema emitted locally under components.schemas — nothing is fetched and
// nothing is left dangling.
//
// The document is derived only from the runtime typed registry, never from
// another behavior authority, and is fully deterministic: the same registry
// produces byte-identical output on every call. Internal operations never
// appear.
func (r *Registry) OpenAPI() ([]byte, error) {
	schemas := make(map[string]any, len(r.catalog.sharedDefs)+2*len(r.publicList))
	for name, schema := range r.catalog.sharedDefs {
		rewritten, err := rewriteRefs(schema)
		if err != nil {
			return nil, fmt.Errorf("registry: shared schema %s: %w", name, err)
		}
		schemas[name] = rewritten
	}

	paths := make(map[string]any, len(r.publicList))
	for _, e := range r.publicList {
		d := e.descriptor
		input, err := rewriteRefBytes(d.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("registry: operation %s input schema: %w", d.ID, err)
		}
		output, err := rewriteRefBytes(d.OutputSchema)
		if err != nil {
			return nil, fmt.Errorf("registry: operation %s output schema: %w", d.ID, err)
		}
		// The result envelopes reference the operation's output component;
		// register it so no emitted reference dangles.
		schemas[componentName(d.ID, "Output")] = output
		paths["/v1/operations/"+d.ID] = pathItem(d, input, output)
	}

	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "Zatiti Local API",
			"version": fmt.Sprintf("1.%d.0", r.catalog.document.Revision),
			"description": "Typed local API of one Zatiti controller. Every operation is a POST to " +
				"/v1/operations/{operation_id} carrying the common request envelope and returning the " +
				"common result envelope; CLI tokens and MCP tool names are derived from the operation IDs.",
		},
		"servers": []any{map[string]any{"url": "/v1"}},
		"paths":   paths,
		"components": map[string]any{
			"schemas": schemas,
		},
	}
	return marshalDeterministic(doc)
}

// componentName maps an operation ID and role to a component schema name.
// Operation IDs are unique and dot-separated lowercase tokens, so the
// PascalCase concatenation is collision-free within one document.
func componentName(operationID, role string) string {
	parts := strings.Split(operationID, ".")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	b.WriteString("_")
	b.WriteString(role)
	return b.String()
}

// pathItem builds the POST path item for one public operation.
func pathItem(d contract.Descriptor, input, output map[string]any) map[string]any {
	responses := map[string]any{
		"200": map[string]any{
			"description": "Completed result envelope.",
			"content": map[string]any{
				"application/json": map[string]any{"schema": resultEnvelopeSchema(d, contract.StatusCompleted)},
			},
		},
		"default": map[string]any{
			"description": "Failed result envelope. The fault maps to the transport exit/status code; the envelope is always present.",
			"content": map[string]any{
				"application/json": map[string]any{"schema": resultEnvelopeSchema(d, contract.StatusFailed)},
			},
		},
	}
	if d.Mode == contract.ModeMutation {
		responses["202"] = map[string]any{
			"description": "Accepted result envelope identifying inspectable durable work.",
			"content": map[string]any{
				"application/json": map[string]any{"schema": resultEnvelopeSchema(d, contract.StatusAccepted)},
			},
		}
	}
	return map[string]any{
		"post": map[string]any{
			"operationId": d.ID,
			"tags":        []any{d.Owner},
			"description": "Effect: " + d.Effect + ".",
			"requestBody": map[string]any{
				"required": true,
				"content": map[string]any{
					"application/json": map[string]any{"schema": requestEnvelopeSchema(d, input)},
				},
			},
			"responses": responses,
		},
	}
}

// requestEnvelopeSchema is the common zatiti.request/v1 envelope bound to
// one operation's input schema. Mutations declare their submission key as
// required; the envelope never carries actor or authority material.
func requestEnvelopeSchema(d contract.Descriptor, input map[string]any) map[string]any {
	properties := map[string]any{
		"schema": map[string]any{"const": contract.SchemaRequest},
		"input":  input,
	}
	required := []any{"schema", "input"}
	if d.SubmissionKey {
		properties["submission_key"] = map[string]any{
			"type":      "string",
			"pattern":   "^[\x20-\x7e]{1,128}$",
			"maxLength": 128,
		}
		required = append(required, "submission_key")
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}

// resultEnvelopeSchema is the common zatiti.result/v1 envelope for one
// response status. Data carries the operation's output schema; a failed
// response carries no data. The next cursor lives in the envelope.
func resultEnvelopeSchema(d contract.Descriptor, status string) map[string]any {
	var dataSchema any
	if status == contract.StatusFailed {
		dataSchema = map[string]any{"type": "null"}
	} else {
		dataSchema = map[string]any{"anyOf": []any{
			map[string]any{"$ref": "#/components/schemas/" + componentName(d.ID, "Output")},
			map[string]any{"type": "null"},
		}}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"schema":      map[string]any{"const": contract.SchemaResult},
			"command_id":  map[string]any{"type": "string", "format": "uuid"},
			"status":      map[string]any{"const": status},
			"data":        dataSchema,
			"error":       map[string]any{"anyOf": []any{map[string]any{"$ref": "#/components/schemas/Fault"}, map[string]any{"type": "null"}}},
			"next_cursor": map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "null"}}},
		},
		"required": []any{"schema", "command_id", "status", "data", "error", "next_cursor"},
	}
}

// rewriteRefBytes rewrites document-local $refs of one operation schema
// from "#/$defs/Name" to "#/components/schemas/Name".
func rewriteRefBytes(schema json.RawMessage) (map[string]any, error) {
	var root any
	dec := json.NewDecoder(bytes.NewReader(schema))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("schema is not valid JSON: %w", err)
	}
	rewritten, err := rewriteRefs(root)
	if err != nil {
		return nil, err
	}
	obj, ok := rewritten.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("operation schema must be a JSON object")
	}
	return obj, nil
}

// rewriteRefs walks a decoded schema and rewrites every document-local
// $defs reference to the OpenAPI components location. Any other $ref form
// is rejected: the emitted document must be closed under its own schemas.
func rewriteRefs(node any) (any, error) {
	switch n := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(n))
		for key, value := range n {
			if key == "$ref" {
				s, ok := value.(string)
				if !ok {
					return nil, fmt.Errorf("$ref must be a string")
				}
				if strings.HasPrefix(s, "#/$defs/") {
					out[key] = "#/components/schemas/" + strings.TrimPrefix(s, "#/$defs/")
					continue
				}
				if s == "#" || strings.HasPrefix(s, "#/") {
					return nil, fmt.Errorf("$ref %q does not point at #/$defs and cannot be emitted", s)
				}
				return nil, fmt.Errorf("$ref %q is not a document-local fragment", s)
			}
			rewritten, err := rewriteRefs(value)
			if err != nil {
				return nil, err
			}
			out[key] = rewritten
		}
		return out, nil
	case []any:
		out := make([]any, len(n))
		for i, value := range n {
			rewritten, err := rewriteRefs(value)
			if err != nil {
				return nil, err
			}
			out[i] = rewritten
		}
		return out, nil
	default:
		return node, nil
	}
}

// marshalDeterministic renders the document as pretty-printed JSON with
// map keys in sorted order at every level: byte-identical for the same
// registry, independent of Go map iteration order.
func marshalDeterministic(doc any) ([]byte, error) {
	compact, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("registry: OpenAPI document could not be marshaled: %w", err)
	}
	var out bytes.Buffer
	if err := json.Indent(&out, compact, "", "  "); err != nil {
		return nil, fmt.Errorf("registry: OpenAPI document could not be rendered: %w", err)
	}
	out.WriteByte('\n')
	if out.Len() == 0 {
		return nil, io.ErrUnexpectedEOF
	}
	return out.Bytes(), nil
}
