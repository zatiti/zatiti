package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func mustSchema(t *testing.T, schema, instance string) error {
	t.Helper()
	return ValidateSchema(json.RawMessage(schema), json.RawMessage(instance))
}

func TestValidateSchemaTypes(t *testing.T) {
	t.Parallel()
	objectSchema := `{
		"type": "object",
		"additionalProperties": false,
		"required": ["name", "count"],
		"properties": {
			"name": {"type": "string", "minLength": 1},
			"count": {"type": "integer", "minimum": 0}
		}
	}`
	good := []struct{ name, instance string }{
		{"valid", `{"name":"a","count":0}`},
		{"max int64", `{"name":"a","count":9223372036854775807}`},
		{"nested whitespace", ` { "name" : "a" , "count" : 12 } `},
	}
	for _, tc := range good {
		if err := mustSchema(t, objectSchema, tc.instance); err != nil {
			t.Errorf("%s: ValidateSchema = %v, want nil", tc.name, err)
		}
	}
	bad := []struct{ name, instance string }{
		{"missing required", `{"name":"a"}`},
		{"unknown property", `{"name":"a","count":1,"extra":true}`},
		{"fractional integer", `{"name":"a","count":1.5}`},
		{"exponent integer", `{"name":"a","count":1e2}`},
		{"integer above int64", `{"name":"a","count":9223372036854775808}`},
		{"wrong type", `{"name":1,"count":1}`},
		{"empty string below minLength", `{"name":"","count":1}`},
		{"negative below minimum", `{"name":"a","count":-1}`},
		{"not an object", `[]`},
	}
	for _, tc := range bad {
		err := mustSchema(t, objectSchema, tc.instance)
		want := error(ErrSchemaValidation)
		if tc.name == "integer above int64" {
			// Same rejection decoding produces: instance decode problem.
			want = ErrIntegerOverflow
		}
		if !errors.Is(err, want) {
			t.Errorf("%s: ValidateSchema(%s) = %v, want %v", tc.name, tc.instance, err, want)
		}
	}
}

func TestValidateSchemaIntegerBounds(t *testing.T) {
	t.Parallel()
	schema := `{"type":"integer","minimum":1,"maximum":10,"multipleOf":2}`
	for _, good := range []string{"2", "10"} {
		if err := mustSchema(t, schema, good); err != nil {
			t.Errorf("%s should pass: %v", good, err)
		}
	}
	for _, bad := range []string{"0", "11", "3", "-5", "1.5", "2.0"} {
		if err := mustSchema(t, schema, bad); err == nil {
			t.Errorf("%s should fail integer bounds", bad)
		}
	}
	excl := `{"type":"integer","exclusiveMinimum":0,"exclusiveMaximum":3}`
	if err := mustSchema(t, excl, "2"); err != nil {
		t.Errorf("2 should pass exclusive bounds: %v", err)
	}
	if err := mustSchema(t, excl, "3"); err == nil {
		t.Errorf("3 should fail exclusiveMaximum")
	}
	if err := mustSchema(t, excl, "0"); err == nil {
		t.Errorf("0 should fail exclusiveMinimum")
	}
	// Out-of-int64 integers are rejected even without declared bounds; the
	// rejection is the same decode error decoding produces.
	if err := mustSchema(t, `{"type":"integer"}`, `9223372036854775808`); !errors.Is(err, ErrIntegerOverflow) {
		t.Errorf("oversized integer = %v, want ErrIntegerOverflow", err)
	}
}

func TestValidateSchemaOneOfAnyOfNot(t *testing.T) {
	t.Parallel()
	oneOf := `{"oneOf":[{"type":"string"},{"type":"integer"}]}`
	if err := mustSchema(t, oneOf, `"x"`); err != nil {
		t.Errorf("oneOf single match: %v", err)
	}
	if err := mustSchema(t, oneOf, `true`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("oneOf zero matches accepted: %v", err)
	}
	if err := mustSchema(t, `{"oneOf":[{"type":"number"},{"type":"integer"}]}`, `1`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("oneOf two matches accepted: %v", err)
	}
	if err := mustSchema(t, `{"anyOf":[{"type":"string"},{"type":"null"}]}`, `null`); err != nil {
		t.Errorf("anyOf match: %v", err)
	}
	if err := mustSchema(t, `{"anyOf":[{"type":"string"}]}`, `1`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("anyOf zero matches accepted: %v", err)
	}
	if err := mustSchema(t, `{"not":{"type":"null"}}`, `null`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("not accepted a null: %v", err)
	}
	if err := mustSchema(t, `{"allOf":[{"type":"number"},{"minimum":5}]}`, `5`); err != nil {
		t.Errorf("allOf valid rejected: %v", err)
	}
	if err := mustSchema(t, `{"allOf":[{"type":"number"},{"minimum":5}]}`, `4`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("allOf invalid accepted: %v", err)
	}
}

func TestValidateSchemaFormats(t *testing.T) {
	t.Parallel()
	good := []struct{ format, value string }{
		{"uuid", "00000000-0000-4000-8000-000000000001"},
		{"uuid", string(NewID())},
		{"date-time", "2026-09-10T12:34:56.789Z"},
		{"date", "2026-09-10"},
		{"time", "12:34:56Z"},
		{"email", "david@example.com"},
		{"hostname", "zatiti.example"},
		{"ipv4", "127.0.0.1"},
		{"ipv6", "::1"},
		{"uri", "https://example.com/x"},
		{"json-pointer", "/a/b"},
		{"json-pointer", ""},
		{"regex", "^[a-z]+$"},
	}
	for _, tc := range good {
		schema := `{"type":"string","format":"` + tc.format + `"}`
		if err := mustSchema(t, schema, `"`+tc.value+`"`); err != nil {
			t.Errorf("format %s value %s rejected: %v", tc.format, tc.value, err)
		}
	}
	bad := []struct{ format, value string }{
		{"uuid", "not-a-uuid"},
		{"uuid", "00000000000040008000000000000001"},
		{"date-time", "yesterday"},
		{"date", "2026-13-40"},
		{"email", "no-at-sign"},
		{"ipv4", "999.1.1.1"},
		{"json-pointer", "a/b"},
	}
	for _, tc := range bad {
		schema := `{"type":"string","format":"` + tc.format + `"}`
		if err := mustSchema(t, schema, `"`+tc.value+`"`); !errors.Is(err, ErrSchemaValidation) {
			t.Errorf("format %s accepted %q: %v", tc.format, tc.value, err)
		}
	}
	// Unknown formats are annotations in draft 2020-12: not asserted.
	if err := mustSchema(t, `{"type":"string","format":"tele-nope"}`, `"-1"`); err != nil {
		t.Errorf("unknown format must not assert: %v", err)
	}
}

func TestValidateSchemaRefs(t *testing.T) {
	t.Parallel()
	recursive := `{
		"$defs": {
			"node": {
				"type": "object",
				"additionalProperties": false,
				"required": ["name"],
				"properties": {
					"name": {"type": "string"},
					"next": {"$ref": "#/$defs/node"}
				}
			}
		},
		"$ref": "#/$defs/node"
	}`
	if err := mustSchema(t, recursive, `{"name":"a","next":{"name":"b","next":{"name":"c"}}}`); err != nil {
		t.Errorf("recursive local ref rejected: %v", err)
	}
	if err := mustSchema(t, recursive, `{"name":"a","next":{"name":"b","surprise":1}}`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("additionalProperties at ref depth accepted: %v", err)
	}
	rootRef := `{"$ref":"#"}`
	if err := mustSchema(t, rootRef, `{"anything":true}`); !errors.Is(err, ErrSchemaUnsupported) {
		t.Errorf("root self-ref = %v, want bounded ErrSchemaUnsupported (no hang)", err)
	}
	remote := `{"$ref":"https://example.com/schema.json"}`
	err := mustSchema(t, remote, `{}`)
	if !errors.Is(err, ErrSchemaUnsupported) {
		t.Fatalf("remote ref = %v, want ErrSchemaUnsupported (never fetch)", err)
	}
	if err := mustSchema(t, `{"$ref":"#/$defs/missing"}`, `{}`); !errors.Is(err, ErrSchemaMalformed) {
		t.Errorf("unresolvable ref = %v, want ErrSchemaMalformed", err)
	}
	// Pure $ref cycle with no instance progress hits the depth bound.
	cycle := `{"$defs":{"a":{"$ref":"#/$defs/a"}},"$ref":"#/$defs/a"}`
	if err := mustSchema(t, cycle, `"x"`); !errors.Is(err, ErrSchemaUnsupported) {
		t.Errorf("ref cycle = %v, want ErrSchemaUnsupported (depth bound)", err)
	}
}

func TestValidateSchemaStrictnessOfSchemaDocument(t *testing.T) {
	t.Parallel()
	if err := mustSchema(t, `{"typo":"x"}`, `{}`); !errors.Is(err, ErrSchemaUnsupported) {
		t.Errorf("unknown schema keyword accepted: %v", err)
	}
	if err := mustSchema(t, `{"$schema":"http://json-schema.org/draft-07/schema#","type":"string"}`, `"x"`); !errors.Is(err, ErrSchemaUnsupported) {
		t.Errorf("draft-07 dialect accepted: %v", err)
	}
	if err := mustSchema(t, `{"x-vendor":"inert","type":"string"}`, `"x"`); err != nil {
		t.Errorf("x- extension rejected: %v", err)
	}
	if err := mustSchema(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"string"}`, `"x"`); err != nil {
		t.Errorf("2020-12 dialect rejected: %v", err)
	}
	if err := mustSchema(t, `true`, `{}`); err != nil {
		t.Errorf("boolean true schema rejected: %v", err)
	}
	if err := mustSchema(t, `false`, `{}`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("false schema accepted: %v", err)
	}
	if err := mustSchema(t, `{"a":1,"a":2}`, `1`); !errors.Is(err, ErrDuplicateKey) {
		t.Errorf("duplicate key in schema accepted: %v", err)
	}
	if err := mustSchema(t, `{"type":"string"}`, `{bad`); !errors.Is(err, ErrMalformedJSON) {
		t.Errorf("malformed instance = %v, want ErrMalformedJSON", err)
	}
}

func TestValidateSchemaArrays(t *testing.T) {
	t.Parallel()
	unique := `{"type":"array","uniqueItems":true,"minItems":2,"maxItems":3}`
	if err := mustSchema(t, unique, `[1,2]`); err != nil {
		t.Errorf("unique array rejected: %v", err)
	}
	// Numeric equality: 1 and 1.0 are the same value.
	if err := mustSchema(t, unique, `[1,1.0]`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("numeric duplicates accepted: %v", err)
	}
	if err := mustSchema(t, unique, `[1]`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("below minItems accepted: %v", err)
	}
	if err := mustSchema(t, unique, `[1,2,3,4]`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("above maxItems accepted: %v", err)
	}
	tuple := `{"type":"array","prefixItems":[{"type":"string"},{"type":"integer"}],"items":{"type":"boolean"}}`
	if err := mustSchema(t, tuple, `["a",1,true,false]`); err != nil {
		t.Errorf("tuple+items rejected: %v", err)
	}
	if err := mustSchema(t, tuple, `["a",1,"not-bool"]`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("bad items tail accepted: %v", err)
	}
	if err := mustSchema(t, tuple, `[1,"a"]`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("bad prefix accepted: %v", err)
	}
	contains := `{"type":"array","contains":{"type":"integer"}}`
	if err := mustSchema(t, contains, `["a",1]`); err != nil {
		t.Errorf("contains match rejected: %v", err)
	}
	if err := mustSchema(t, contains, `["a"]`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("contains without match accepted: %v", err)
	}
}

func TestValidateSchemaObjects(t *testing.T) {
	t.Parallel()
	dep := `{
		"type": "object",
		"dependentRequired": {"credit_card": ["billing_address"]},
		"properties": {"credit_card": {"type":"string"}, "billing_address": {"type":"string"}}
	}`
	if err := mustSchema(t, dep, `{"credit_card":"x","billing_address":"y"}`); err != nil {
		t.Errorf("dependentRequired satisfied rejected: %v", err)
	}
	if err := mustSchema(t, dep, `{"credit_card":"x"}`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("dependentRequired violation accepted: %v", err)
	}
	names := `{"type":"object","propertyNames":{"type":"string","pattern":"^[a-z_]+$"}}`
	if err := mustSchema(t, names, `{"ok_name":1}`); err != nil {
		t.Errorf("propertyNames valid rejected: %v", err)
	}
	if err := mustSchema(t, names, `{"BadName":1}`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("propertyNames violation accepted: %v", err)
	}
	pattern := `{
		"type":"object",
		"patternProperties":{"^S_":{"type":"string"}},
		"additionalProperties":false
	}`
	if err := mustSchema(t, pattern, `{"S_x":"s"}`); err != nil {
		t.Errorf("patternProperties match rejected: %v", err)
	}
	if err := mustSchema(t, pattern, `{"S_x":1}`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("patternProperties type violation accepted: %v", err)
	}
}

func TestValidateSchemaEnumConstIf(t *testing.T) {
	t.Parallel()
	enum := `{"enum":["read","write",1,null]}`
	for _, v := range []string{`"read"`, `1`, `null`} {
		if err := mustSchema(t, enum, v); err != nil {
			t.Errorf("enum value %s rejected: %v", v, err)
		}
	}
	if err := mustSchema(t, enum, `"delete"`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("non-member accepted: %v", err)
	}
	// Enum numeric equality: 1.0 equals 1.
	if err := mustSchema(t, enum, `1.0`); err != nil {
		t.Errorf("numeric enum equality broken: %v", err)
	}
	ifThen := `{"if":{"type":"string"},"then":{"minLength":3},"else":{"type":"integer"}}`
	if err := mustSchema(t, ifThen, `"abc"`); err != nil {
		t.Errorf("if/then valid rejected: %v", err)
	}
	if err := mustSchema(t, ifThen, `"ab"`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("if/then violation accepted: %v", err)
	}
	if err := mustSchema(t, ifThen, `5`); err != nil {
		t.Errorf("if/else valid rejected: %v", err)
	}
	if err := mustSchema(t, ifThen, `5.5`); !errors.Is(err, ErrSchemaValidation) {
		t.Errorf("if/else violation accepted: %v", err)
	}
}

func TestValidateSchemaErrorCarriesPointerAndKeyword(t *testing.T) {
	t.Parallel()
	schema := `{
		"type": "object",
		"additionalProperties": false,
		"required": ["child"],
		"properties": {"child": {"type": "object", "required": ["v"], "properties": {"v": {"type":"integer"}}}}
	}`
	err := mustSchema(t, schema, `{"child":{"child":{}}}`)
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("error is not *SchemaError: %v", err)
	}
	if se.Keyword != "required" || se.Pointer != "/child" {
		t.Fatalf("pointer/keyword = %q/%q, want /child/required", se.Pointer, se.Keyword)
	}
	if !errors.Is(err, ErrSchemaValidation) {
		t.Fatalf("error does not wrap ErrSchemaValidation: %v", err)
	}
}

func TestValidateSchemaAdditionalPropertiesFalseDefault(t *testing.T) {
	t.Parallel()
	// Without additionalProperties, extra keys are allowed (JSON Schema
	// default); zatiti operation schemas declare it explicitly when strict.
	open := `{"type":"object","properties":{"a":{"type":"integer"}}}`
	if err := mustSchema(t, open, `{"a":1,"b":2}`); err != nil {
		t.Fatalf("JSON Schema default strictness broken: %v", err)
	}
	strict := `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":false}`
	err := mustSchema(t, strict, `{"a":"x","b":1}`)
	var se *SchemaError
	if !errors.As(err, &se) || se.Keyword != "additionalProperties" {
		t.Fatalf("strict object not enforced: %v", err)
	}
}

// TestValidateSchemaMemoizesParsedSchemaDocument asserts ValidateSchema's
// schema-side strictParse is actually cached across calls with a
// byte-identical schema document, not merely correct: every call site
// across the module reuses the same handler-registered schema on every
// invocation (see schemaCache's doc comment in schema.go), so a regression
// that silently drops the memoization -- e.g. a future edit that calls
// strictParse(schema) directly again instead of cachedStrictParse -- would
// reintroduce a real, previously-measured ~5.5ms-per-call cost across ~40
// call sites without failing any purely-functional test. This test fails
// on exactly that regression by counting schemaCache misses, and also
// fails if the cache were ever made to ignore its key (e.g. a bug that
// always hits), since a genuinely distinct schema document must still
// register its own miss.
//
// Deliberately not t.Parallel(): it reads a package-global counter shared
// by every schema-validating test, and Go only guarantees non-parallel
// tests never run concurrently with each other or with a parallel batch.
func TestValidateSchemaMemoizesParsedSchemaDocument(t *testing.T) {
	nonce := t.Name()
	schemaA := json.RawMessage(fmt.Sprintf(`{"type":"object","title":%q}`, nonce+"-a"))
	schemaB := json.RawMessage(fmt.Sprintf(`{"type":"string","title":%q}`, nonce+"-b"))
	instanceA := json.RawMessage(`{}`)
	instanceB := json.RawMessage(`"x"`)

	before := schemaCacheMisses.Load()
	if err := ValidateSchema(schemaA, instanceA); err != nil {
		t.Fatalf("ValidateSchema(schemaA): %v", err)
	}
	afterFirst := schemaCacheMisses.Load()
	if afterFirst != before+1 {
		t.Fatalf("first call to a never-seen schema: misses went %d -> %d, want exactly +1", before, afterFirst)
	}

	for i := 0; i < 5; i++ {
		if err := ValidateSchema(schemaA, instanceA); err != nil {
			t.Fatalf("repeat call %d to schemaA: %v", i, err)
		}
	}
	afterRepeats := schemaCacheMisses.Load()
	if afterRepeats != afterFirst {
		t.Fatalf("5 repeat calls with an identical schema document caused %d more cache misses, want 0 (memoization regressed)", afterRepeats-afterFirst)
	}

	// A genuinely different schema document must still register its own
	// miss: the cache must be keyed by content, never a constant hit.
	if err := ValidateSchema(schemaB, instanceB); err != nil {
		t.Fatalf("ValidateSchema(schemaB): %v", err)
	}
	afterDistinct := schemaCacheMisses.Load()
	if afterDistinct != afterRepeats+1 {
		t.Fatalf("a distinct schema document caused %d new misses, want exactly 1 (cache key too coarse)", afterDistinct-afterRepeats)
	}
}

// TestValidateSchemaCacheStaysBoundedUnderManyDistinctSchemas proves
// schemaCache cannot be grown without limit by a caller that controls the
// schema argument. Not every ValidateSchema call site passes fixed,
// developer-authored content: internal/execution/verifier.go and
// internal/skills/jobs.go both validate against want.Schema, which mirrors
// Adapter_InertSchema -- a field an ordinary task creator supplies on a
// task's Acceptance.expected_observations at task-creation time
// (Adapter_InertSchema's own frozen doc comment: "this is schema data, not
// authority"). A caller creating many tasks with distinct
// expected_observations[].schema values must not be able to grow
// schemaCache's memory without bound. This test fails if schemaCache ever
// regresses to an unbounded map (entry count would exceed
// schemaCacheCapacity) or to a broken cache that silently refuses to store
// anything (entry count would stay at 0, and the final LRU-recency check
// below would also fail).
//
// Deliberately not t.Parallel(): shares the package-global schemaCache with
// every other schema-validating test; see
// TestValidateSchemaMemoizesParsedSchemaDocument's identical reasoning.
func TestValidateSchemaCacheStaysBoundedUnderManyDistinctSchemas(t *testing.T) {
	nonce := t.Name()
	const n = schemaCacheCapacity * 3
	for i := 0; i < n; i++ {
		schema := json.RawMessage(fmt.Sprintf(`{"type":"object","title":%q}`, fmt.Sprintf("%s-%d", nonce, i)))
		if err := ValidateSchema(schema, json.RawMessage(`{}`)); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if got := schemaCache.len(); got > schemaCacheCapacity {
			t.Fatalf("call %d: schemaCache holds %d entries, want <= schemaCacheCapacity (%d): the size bound regressed", i, got, schemaCacheCapacity)
		}
	}
	if got := schemaCache.len(); got != schemaCacheCapacity {
		t.Fatalf("after %d distinct schema documents, schemaCache holds %d entries, want exactly %d (full, oldest evicted): the cache may have stopped storing entries", n, got, schemaCacheCapacity)
	}

	// The most recently used entry must still be a cache hit: a correct LRU
	// keeps recent entries and evicts old ones, rather than (for example)
	// evicting at random or clearing itself once full.
	lastSchema := json.RawMessage(fmt.Sprintf(`{"type":"object","title":%q}`, fmt.Sprintf("%s-%d", nonce, n-1)))
	before := schemaCacheMisses.Load()
	if err := ValidateSchema(lastSchema, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("re-validating the most recently used schema: %v", err)
	}
	if after := schemaCacheMisses.Load(); after != before {
		t.Fatalf("the most recently used schema was evicted (recorded a fresh miss); eviction is not least-recently-used")
	}
}
