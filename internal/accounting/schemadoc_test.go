package accounting

import (
	"encoding/json"
	"strings"
	"testing"
)

// Schema document tests prove that registration fails closed: every
// document-local $ref must resolve against $defs at any depth.

func TestCheckSchemaDocumentAcceptsResolvedRefs(t *testing.T) {
	doc := json.RawMessage(`{
		"$defs": {"ID": {"type": "string"}, "Pair": {"type": "array", "items": {"$ref": "#/$defs/ID"}}},
		"type": "object",
		"properties": {"id": {"$ref": "#/$defs/ID"}, "pair": {"$ref": "#/$defs/Pair"}}
	}`)
	if err := checkSchemaDocument(doc); err != nil {
		t.Fatalf("resolved refs rejected: %v", err)
	}
}

func TestCheckSchemaDocumentRejectsBrokenRefs(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "dangling ref in properties",
			doc:  `{"$defs": {"ID": {"type": "string"}}, "type": "object", "properties": {"id": {"$ref": "#/$defs/Missing"}}}`,
			want: "does not resolve",
		},
		{
			name: "dangling ref inside a list",
			doc:  `{"$defs": {"ID": {"type": "string"}}, "anyOf": [{"$ref": "#/$defs/ID"}, {"$ref": "#/$defs/Missing"}]}`,
			want: "does not resolve",
		},
		{
			name: "dangling ref inside $defs",
			doc:  `{"$defs": {"Pair": {"type": "array", "items": {"$ref": "#/$defs/Missing"}}}, "type": "object"}`,
			want: "does not resolve",
		},
		{
			name: "non-local ref",
			doc:  `{"$defs": {"ID": {"type": "string"}}, "type": "object", "properties": {"id": {"$ref": "https://example.invalid/schema.json#/$defs/ID"}}}`,
			want: "non-local",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSchemaDocument(json.RawMessage(tc.doc))
			if err == nil {
				t.Fatalf("broken schema document accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}
