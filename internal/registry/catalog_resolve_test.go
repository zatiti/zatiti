package registry

import (
	"encoding/json"
	"testing"
)

func TestResolveSchemaRejectsDuplicateKeysAcrossSplitValidation(t *testing.T) {
	catalog := mustCatalog(t)
	cases := map[string]string{
		"top-level":         `{"type":"object","type":"array"}`,
		"operation body":    `{"type":"object","properties":{"value":{"type":"string","type":"integer"}}}`,
		"definitions":       `{"type":"object","$defs":{"Value":{"type":"string"},"Value":{"type":"integer"}}}`,
		"nested definition": `{"type":"object","$defs":{"Value":{"type":"object","properties":{"name":{"type":"string","type":"integer"}}}}}`,
	}
	for name, schema := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := catalog.resolveSchema(json.RawMessage(schema)); err == nil {
				t.Fatal("duplicate JSON key was accepted")
			}
		})
	}
}
