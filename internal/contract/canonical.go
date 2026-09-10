package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// Canonicalize converts one JSON value to versioned canonical JSON bytes:
// object keys sorted (byte order of the decoded key strings), duplicate
// keys rejected, exact integer values preserved (int64 range; integers are
// re-emitted in minimal form), non-integer numbers preserved as exact
// literal bytes, semantic array order preserved, and strings re-escaped
// minimally. The output is deterministic: semantically equal inputs produce
// equal bytes.
//
// Canonicalize validates structure, not membership: unknown-field rejection
// happens against a schema (ValidateSchema) or a DTO (DecodeStrict). Arrays
// keep their order; sorting schema-declared sets is a schema-aware concern
// (see canonicalizeSets).
func Canonicalize(data []byte) ([]byte, error) {
	v, derr := strictParse(data)
	if derr != nil {
		return nil, derr
	}
	var buf bytes.Buffer
	if err := encodeCanonicalValue(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// canonicalizeSets canonicalizes data like Canonicalize and additionally
// sorts every array the schema declares as a set (uniqueItems: true) by the
// canonical bytes of each element — the stable identity of a JSON value.
// Array order stays semantic everywhere else. This is the schema-aware form
// used where a canonical input hash must treat set members as orderless.
func canonicalizeSets(data, schema []byte) ([]byte, error) {
	v, derr := strictParse(data)
	if derr != nil {
		return nil, derr
	}
	sv, serr := strictParse(schema)
	if serr != nil {
		return nil, decodeErrf("", ErrSchemaMalformed, "schema: %s", serr.Error())
	}
	var buf bytes.Buffer
	if err := encodeCanonicalSets(&buf, v, sv, map[string]bool{}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeCanonicalSets(buf *bytes.Buffer, v, schema any, refs map[string]bool) error {
	// Only arrays under a uniqueItems schema are reordered; everything else
	// follows the ordinary canonical encoder.
	if arr, ok := v.([]any); ok {
		if isUniqueItemsSchema(schema) {
			sorted := make([]any, len(arr))
			copy(sorted, arr)
			sort.SliceStable(sorted, func(i, j int) bool {
				return lessStableIdentity(sorted[i], sorted[j])
			})
			arr = sorted
		}
		itemSchema := schemaItems(schema)
		buf.WriteByte('[')
		for i, el := range arr {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeCanonicalSets(buf, el, itemSchema, refs); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	}
	if obj, ok := v.(map[string]any); ok {
		props := schemaProperties(schema)
		addProps := schemaAdditionalProperties(schema)
		buf.WriteByte('{')
		keys := sortedKeys(obj)
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeString(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			sub := props[k]
			if sub == nil {
				sub = addProps
			}
			if err := encodeCanonicalSets(buf, obj[k], sub, refs); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	}
	return encodeScalar(buf, v)
}

func lessStableIdentity(a, b any) bool {
	var ab, bb bytes.Buffer
	// Element encoding cannot fail here: values came from a parsed document.
	_ = encodeCanonicalValue(&ab, a)
	_ = encodeCanonicalValue(&bb, b)
	return bytes.Compare(ab.Bytes(), bb.Bytes()) < 0
}

func isUniqueItemsSchema(schema any) bool {
	m, ok := schema.(map[string]any)
	if !ok {
		return false
	}
	u, ok := m["uniqueItems"].(bool)
	return ok && u
}

func schemaProperties(schema any) map[string]any {
	m, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	p, _ := m["properties"].(map[string]any)
	return p
}

func schemaAdditionalProperties(schema any) any {
	m, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	if ap, present := m["additionalProperties"]; present {
		return ap
	}
	return nil
}

func schemaItems(schema any) any {
	m, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	return m["items"]
}

// Canonicalize writes through encodeCanonicalValue; sets-aware encoding
// above shares the scalar/object leaf behavior via these helpers only where
// structure differs.

func encodeCanonicalValue(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case map[string]any:
		buf.WriteByte('{')
		keys := sortedKeys(t)
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeString(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := encodeCanonicalValue(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, el := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeCanonicalValue(buf, el); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	default:
		return encodeScalar(buf, v)
	}
	return nil
}

func encodeScalar(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		return encodeString(buf, t)
	case json.Number:
		return encodeNumber(buf, t)
	default:
		return fmt.Errorf("canonicalize: unexpected value type %T", v)
	}
	return nil
}

// encodeNumber re-emits integer literals in minimal int64 form and rejects
// integers outside the int64 range; non-integer literals pass through with
// their exact original spelling.
func encodeNumber(buf *bytes.Buffer, n json.Number) error {
	if integerLiteral(n) {
		v, err := parseInteger(n)
		if err != nil {
			return &DecodeError{Pointer: "", Err: err}
		}
		buf.WriteString(strconv.FormatInt(v, 10))
		return nil
	}
	buf.WriteString(string(n))
	return nil
}

// encodeString writes s with minimal JSON escaping: only quote, backslash
// and the required control escapes; every other character is emitted as
// UTF-8. U+2028 and U+2029 stay unescaped.
func encodeString(buf *bytes.Buffer, s string) error {
	buf.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			buf.WriteString(`\"`)
		case c == '\\':
			buf.WriteString(`\\`)
		case c == '\b':
			buf.WriteString(`\b`)
		case c == '\f':
			buf.WriteString(`\f`)
		case c == '\n':
			buf.WriteString(`\n`)
		case c == '\r':
			buf.WriteString(`\r`)
		case c == '\t':
			buf.WriteString(`\t`)
		case c < 0x20:
			fmt.Fprintf(buf, `\u%04x`, c)
		default:
			buf.WriteByte(c)
		}
	}
	buf.WriteByte('"')
	return nil
}

func sortedKeys(obj map[string]any) []string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
