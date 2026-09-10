package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// maxSchemaDepth bounds schema traversal (including $ref recursion). Zatiti
// payloads never nest deeper; beyond this a schema is rejected instead of
// recursing forever.
const maxSchemaDepth = 256

// dialect2020_12 is the only schema dialect ValidateSchema accepts when the
// schema declares $schema. Schemas without $schema are validated as 2020-12.
const dialect2020_12 = "https://json-schema.org/draft/2020-12/schema"

// ValidateSchema validates the JSON instance against the JSON Schema
// (draft 2020-12 subset). Supported: types (with zatiti strict integers),
// enum/const, numeric bounds and multipleOf, string length/pattern/formats,
// array prefixItems/items/contains and collection limits, uniqueItems,
// object required/properties/patternProperties/additionalProperties/
// propertyNames/dependentRequired/dependentSchemas, if/then/else,
// allOf/anyOf/oneOf/not, and document-local $defs/$ref references.
//
// Strictness decisions (so validation agrees with DecodeStrict):
//
//   - duplicate keys are rejected in the schema AND the instance,
//   - type "integer" requires an integer literal within int64 range
//     (1.0 and 1e2 are not integers on this wire),
//   - remote or non-fragment $ref values are never fetched: they are
//     rejected,
//   - unknown schema keywords are rejected as unsupported instead of being
//     silently ignored; "x-" prefixed keys are inert extensions.
//
// The returned error is a *SchemaError wrapping ErrSchemaValidation (the
// instance failed), ErrSchemaMalformed (schema or instance is not valid
// JSON, or a $ref does not resolve) or ErrSchemaUnsupported (schema uses an
// unsupported construct).
func ValidateSchema(schema, instance json.RawMessage) error {
	sv, derr := strictParse(schema)
	if derr != nil {
		return &SchemaError{
			Message: "schema is not valid strict JSON: " + derr.Error(),
			Err:     errors.Join(ErrSchemaMalformed, error(derr)),
		}
	}
	iv, derr := strictParse(instance)
	if derr != nil {
		return derr // instance problems are decode problems
	}
	v := &schemaValidator{root: sv}
	return v.validate(iv, sv, "", "#", 0)
}

type schemaValidator struct {
	root any
}

var knownKeywords = map[string]bool{
	"$schema": true, "$id": true, "$ref": true, "$defs": true, "$comment": true,
	"allOf": true, "anyOf": true, "oneOf": true, "not": true,
	"if": true, "then": true, "else": true,
	"type": true, "enum": true, "const": true,
	"multipleOf": true, "maximum": true, "exclusiveMaximum": true,
	"minimum": true, "exclusiveMinimum": true,
	"maxLength": true, "minLength": true, "pattern": true, "format": true,
	"maxItems": true, "minItems": true, "uniqueItems": true,
	"contains": true, "maxContains": true, "minContains": true,
	"prefixItems": true, "items": true,
	"maxProperties": true, "minProperties": true, "required": true,
	"properties": true, "patternProperties": true, "additionalProperties": true,
	"propertyNames": true, "dependentRequired": true, "dependentSchemas": true,
	"title": true, "description": true, "default": true, "examples": true,
	"deprecated": true, "readOnly": true, "writeOnly": true,
}

func (v *schemaValidator) fail(keyword, schemaPath, instPtr, format string, args ...any) *SchemaError {
	return &SchemaError{
		Pointer:    instPtr,
		Keyword:    keyword,
		SchemaPath: schemaPath,
		Message:    fmt.Sprintf(format, args...),
		Err:        ErrSchemaValidation,
	}
}

func (v *schemaValidator) validate(instance, schema any, instPtr, schemaPath string, depth int) error {
	if depth > maxSchemaDepth {
		return &SchemaError{
			Pointer: instPtr, SchemaPath: schemaPath,
			Message: "schema nesting exceeds the supported depth",
			Err:     ErrSchemaUnsupported,
		}
	}
	switch s := schema.(type) {
	case bool:
		if !s {
			return v.fail("false", schemaPath, instPtr, "value rejected by false schema")
		}
		return nil
	case map[string]any:
		return v.validateObjectSchema(instance, s, instPtr, schemaPath, depth)
	default:
		return &SchemaError{
			Pointer: instPtr, SchemaPath: schemaPath,
			Message: "schema must be an object or a boolean",
			Err:     ErrSchemaMalformed,
		}
	}
}

func (v *schemaValidator) validateObjectSchema(instance any, s map[string]any, instPtr, schemaPath string, depth int) error {
	for k := range s {
		if !knownKeywords[k] && !strings.HasPrefix(k, "x-") {
			return &SchemaError{
				Pointer: instPtr, Keyword: k, SchemaPath: schemaPath,
				Message: fmt.Sprintf("unsupported schema keyword %q", k),
				Err:     ErrSchemaUnsupported,
			}
		}
	}
	if dialect, ok := s["$schema"].(string); ok && dialect != dialect2020_12 {
		return &SchemaError{
			Keyword: "$schema", SchemaPath: schemaPath,
			Message: fmt.Sprintf("unsupported schema dialect %q; only draft 2020-12 is accepted", dialect),
			Err:     ErrSchemaUnsupported,
		}
	}
	if ref, ok := s["$ref"].(string); ok {
		resolved, rpath, rerr := v.resolveRef(ref, schemaPath)
		if rerr != nil {
			return rerr
		}
		if err := v.validate(instance, resolved, instPtr, rpath, depth+1); err != nil {
			return err
		}
	}

	if t, ok := s["type"]; ok {
		if err := v.checkType(t, instance, instPtr, schemaPath); err != nil {
			return err
		}
	}
	if _, ok := s["enum"]; ok {
		if err := v.checkEnum(s["enum"], instance, instPtr, schemaPath); err != nil {
			return err
		}
	}
	if _, ok := s["const"]; ok {
		if !deepEqualJSON(s["const"], instance) {
			return v.fail("const", schemaPath, instPtr, "value does not equal the constant")
		}
	}

	if err := v.checkNumeric(s, instance, instPtr, schemaPath); err != nil {
		return err
	}
	if err := v.checkString(s, instance, instPtr, schemaPath); err != nil {
		return err
	}
	if err := v.checkArray(s, instance, instPtr, schemaPath, depth); err != nil {
		return err
	}
	if err := v.checkObject(s, instance, instPtr, schemaPath, depth); err != nil {
		return err
	}
	if err := v.checkApplicators(s, instance, instPtr, schemaPath, depth); err != nil {
		return err
	}
	return v.checkConditional(s, instance, instPtr, schemaPath, depth)
}

// resolveRef resolves a document-local JSON pointer reference ("#", or
// "#/$defs/name", or any "#/..." pointer). Remote and non-fragment
// references are rejected: schemas never trigger fetches.
func (v *schemaValidator) resolveRef(ref, schemaPath string) (any, string, error) {
	if !strings.HasPrefix(ref, "#") {
		return nil, "", &SchemaError{
			Keyword: "$ref", SchemaPath: schemaPath,
			Message: fmt.Sprintf("$ref %q is not a document-local fragment; remote schema references are not fetched", ref),
			Err:     ErrSchemaUnsupported,
		}
	}
	pointer := strings.TrimPrefix(ref, "#")
	cur := v.root
	if pointer != "" {
		for _, raw := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
			tok := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
			switch node := cur.(type) {
			case map[string]any:
				next, ok := node[tok]
				if !ok {
					return nil, "", &SchemaError{
						Keyword: "$ref", SchemaPath: schemaPath,
						Message: fmt.Sprintf("$ref %q does not resolve in the schema document", ref),
						Err:     ErrSchemaMalformed,
					}
				}
				cur = next
			case []any:
				idx, err := strconv.Atoi(tok)
				if err != nil || idx < 0 || idx >= len(node) {
					return nil, "", &SchemaError{
						Keyword: "$ref", SchemaPath: schemaPath,
						Message: fmt.Sprintf("$ref %q does not resolve in the schema document", ref),
						Err:     ErrSchemaMalformed,
					}
				}
				cur = node[idx]
			default:
				return nil, "", &SchemaError{
					Keyword: "$ref", SchemaPath: schemaPath,
					Message: fmt.Sprintf("$ref %q does not resolve in the schema document", ref),
					Err:     ErrSchemaMalformed,
				}
			}
		}
	}
	return cur, schemaPath + "/$ref" + pointer, nil
}

func (v *schemaValidator) checkType(t any, instance any, instPtr, schemaPath string) error {
	matches := func(name string) bool {
		switch name {
		case "object":
			_, ok := instance.(map[string]any)
			return ok
		case "array":
			_, ok := instance.([]any)
			return ok
		case "string":
			_, ok := instance.(string)
			return ok
		case "boolean":
			_, ok := instance.(bool)
			return ok
		case "null":
			return instance == nil
		case "number":
			_, ok := instance.(json.Number)
			return ok
		case "integer":
			n, ok := instance.(json.Number)
			return ok && integerLiteral(n)
		default:
			return false
		}
	}
	names, ok := t.([]any)
	if !ok {
		name, isStr := t.(string)
		if !isStr {
			return &SchemaError{
				Keyword: "type", SchemaPath: schemaPath,
				Message: "type must be a string or an array of strings",
				Err:     ErrSchemaMalformed,
			}
		}
		names = []any{name}
	}
	for _, raw := range names {
		name, ok := raw.(string)
		if !ok {
			return &SchemaError{
				Keyword: "type", SchemaPath: schemaPath,
				Message: "type entries must be strings",
				Err:     ErrSchemaMalformed,
			}
		}
		if _, known := map[string]bool{"object": true, "array": true, "string": true,
			"boolean": true, "null": true, "number": true, "integer": true}[name]; !known {
			return &SchemaError{
				Keyword: "type", SchemaPath: schemaPath,
				Message: fmt.Sprintf("unsupported type %q", name),
				Err:     ErrSchemaUnsupported,
			}
		}
		if matches(name) {
			if name == "integer" {
				if n, isNum := instance.(json.Number); isNum {
					if _, err := parseInteger(n); err != nil {
						return v.fail("type", schemaPath, instPtr,
							"integer %s is outside the int64 wire range", n)
					}
				}
			}
			return nil
		}
	}
	return v.fail("type", schemaPath, instPtr, "expected type %s, got %s", typeNames(names), jsonTypeName(instance))
}

func typeNames(names []any) string {
	parts := make([]string, 0, len(names))
	for _, n := range names {
		if s, ok := n.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "|")
}

func jsonTypeName(v any) string {
	switch t := v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case nil:
		return "null"
	case json.Number:
		if integerLiteral(t) {
			return "integer"
		}
		return "number"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func (v *schemaValidator) checkEnum(enum any, instance any, instPtr, schemaPath string) error {
	options, ok := enum.([]any)
	if !ok {
		return &SchemaError{
			Keyword: "enum", SchemaPath: schemaPath,
			Message: "enum must be an array",
			Err:     ErrSchemaMalformed,
		}
	}
	for _, opt := range options {
		if deepEqualJSON(opt, instance) {
			return nil
		}
	}
	return v.fail("enum", schemaPath, instPtr, "value is not one of the enumerated options")
}

// deepEqualJSON compares two decoded values with numeric equality: JSON
// numbers compare exactly by value (1.0 equals 1), everything else by
// type and content.
func deepEqualJSON(a, b any) bool {
	an, aok := a.(json.Number)
	bn, bok := b.(json.Number)
	if aok && bok {
		av, aerr := ratOf(an)
		bv, berr := ratOf(bn)
		if aerr != nil || berr != nil {
			return string(an) == string(bn)
		}
		return av.Cmp(bv) == 0
	}
	if aok != bok {
		return false
	}
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, val := range av {
			bval, ok := bv[k]
			if !ok || !deepEqualJSON(val, bval) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !deepEqualJSON(av[i], bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

// ratOf parses an exact decimal from a JSON number literal. Exponent
// magnitudes beyond 10000 are rejected to bound work on untrusted schemas.
func ratOf(n json.Number) (*big.Rat, error) {
	s := string(n)
	if e := strings.IndexAny(s, "eE"); e >= 0 {
		if exp, err := strconv.Atoi(s[e+1:]); err == nil && (exp > 10000 || exp < -10000) {
			return nil, fmt.Errorf("exponent magnitude beyond the supported range")
		}
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("unparsable number %s", s)
	}
	return r, nil
}

func numLess(a, b *big.Rat) bool { return a.Cmp(b) < 0 }

func (v *schemaValidator) checkNumeric(s map[string]any, instance any, instPtr, schemaPath string) error {
	n, ok := instance.(json.Number)
	if !ok {
		return nil
	}
	val, err := ratOf(n)
	if err != nil {
		return v.fail("number", schemaPath, instPtr, "%s", err.Error())
	}
	if m, ok := s["multipleOf"]; ok {
		div, ok := m.(json.Number)
		if !ok {
			return &SchemaError{
				Keyword: "multipleOf", SchemaPath: schemaPath,
				Message: "multipleOf must be a number", Err: ErrSchemaMalformed,
			}
		}
		dv, err := ratOf(div)
		if err != nil || dv.Sign() == 0 {
			return &SchemaError{
				Keyword: "multipleOf", SchemaPath: schemaPath,
				Message: "multipleOf must be a nonzero number", Err: ErrSchemaMalformed,
			}
		}
		q := new(big.Rat).Quo(val, dv)
		if !q.IsInt() {
			return v.fail("multipleOf", schemaPath, instPtr, "value %s is not a multiple of %s", n, div)
		}
	}
	if b, ok := lookupNumber(s, "maximum"); ok {
		if numGreater(val, b) {
			return v.fail("maximum", schemaPath, instPtr, "value %s exceeds maximum %s", n, s["maximum"])
		}
	}
	if b, ok := lookupNumber(s, "exclusiveMaximum"); ok {
		if !numLess(val, b) {
			return v.fail("exclusiveMaximum", schemaPath, instPtr, "value %s must be less than %s", n, s["exclusiveMaximum"])
		}
	}
	if b, ok := lookupNumber(s, "minimum"); ok {
		if numLess(val, b) {
			return v.fail("minimum", schemaPath, instPtr, "value %s is below minimum %s", n, s["minimum"])
		}
	}
	if b, ok := lookupNumber(s, "exclusiveMinimum"); ok {
		if !numGreater(val, b) {
			return v.fail("exclusiveMinimum", schemaPath, instPtr, "value %s must be greater than %s", n, s["exclusiveMinimum"])
		}
	}
	return nil
}

func lookupNumber(s map[string]any, key string) (*big.Rat, bool) {
	raw, ok := s[key].(json.Number)
	if !ok {
		return nil, false
	}
	r, err := ratOf(raw)
	if err != nil {
		return nil, false
	}
	return r, true
}

func numGreater(a, b *big.Rat) bool { return a.Cmp(b) > 0 }
