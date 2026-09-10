package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

// Decode error taxonomy. Every decode/validation failure wraps one of these
// sentinels, so callers branch with errors.Is and read the JSON pointer
// from the typed error.
var (
	ErrMalformedJSON     = errors.New("malformed JSON")
	ErrDuplicateKey      = errors.New("duplicate object key")
	ErrUnknownField      = errors.New("unknown field")
	ErrTrailingData      = errors.New("trailing data after JSON value")
	ErrIntegerOverflow   = errors.New("integer outside int64 range")
	ErrInvalidUTF8       = errors.New("input is not valid UTF-8")
	ErrLoneSurrogate     = errors.New("lone surrogate escape")
	ErrSchemaValidation  = errors.New("schema validation failed")
	ErrSchemaMalformed   = errors.New("schema is malformed")
	ErrSchemaUnsupported = errors.New("schema uses an unsupported construct")
)

// DecodeError locates a strict decode or schema failure. Pointer is the
// JSON pointer (RFC 6901) to the offending value; empty means the whole
// document. Err wraps the sentinel cause.
type DecodeError struct {
	Pointer string
	Err     error
}

func (e *DecodeError) Error() string {
	if e.Pointer == "" {
		return e.Err.Error()
	}
	return e.Err.Error() + " at " + e.Pointer
}

func (e *DecodeError) Unwrap() error { return e.Err }

func decodeErrf(pointer string, err error, format string, args ...any) *DecodeError {
	if format != "" {
		err = fmt.Errorf("%w: %s", err, fmt.Sprintf(format, args...))
	}
	return &DecodeError{Pointer: pointer, Err: err}
}

// SchemaError reports one schema validation failure with the instance
// pointer, the failing keyword and the schema-side path that rejected it.
type SchemaError struct {
	Pointer    string // JSON pointer into the instance
	Keyword    string // schema keyword that failed
	SchemaPath string // JSON pointer into the schema document
	Message    string
	Err        error // wrapped sentinel: ErrSchemaValidation, ErrSchemaMalformed or ErrSchemaUnsupported
}

func (e *SchemaError) Error() string {
	loc := ""
	if e.Pointer != "" {
		loc = " at " + e.Pointer
	}
	if e.SchemaPath != "" {
		loc += " (schema " + e.SchemaPath + ")"
	}
	return e.Message + loc
}

func (e *SchemaError) Unwrap() error { return e.Err }

// integerLiteral reports whether the JSON number literal n has no
// fractional part and no exponent, i.e. the exact shape zatiti maps to
// int64 on the wire.
func integerLiteral(n json.Number) bool {
	for i := 0; i < len(n); i++ {
		switch n[i] {
		case '.', 'e', 'E':
			return false
		}
	}
	return true
}

// parseInteger converts an integer-shaped literal to int64, rejecting
// values outside the int64 range.
func parseInteger(n json.Number) (int64, error) {
	v, err := strconv.ParseInt(string(n), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s", ErrIntegerOverflow, n)
	}
	return v, nil
}

// checkSurrogateEscapes rejects \u escapes that encode an unpaired Unicode
// surrogate (U+D800..U+DFFF). A high surrogate must be immediately followed
// by the matching low surrogate escape. Raw characters are unaffected.
func checkSurrogateEscapes(data []byte) error {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' || i+1 >= len(data) || data[i+1] != 'u' {
			continue
		}
		if i+6 > len(data) {
			return decodeErrf("", ErrLoneSurrogate, "truncated \\u escape")
		}
		r, ok := parseHex4(data[i+2 : i+6])
		if !ok {
			continue // invalid hex: the JSON parser reports it
		}
		if r < 0xD800 || r > 0xDFFF {
			i += 5
			continue
		}
		if r >= 0xDC00 {
			return decodeErrf("", ErrLoneSurrogate, "unpaired low surrogate \\u%04x", r)
		}
		// High surrogate: require an adjacent matching low surrogate escape.
		if i+12 > len(data) || data[i+6] != '\\' || data[i+7] != 'u' {
			return decodeErrf("", ErrLoneSurrogate, "unpaired high surrogate \\u%04x", r)
		}
		lo, ok := parseHex4(data[i+8 : i+12])
		if !ok || lo < 0xDC00 || lo > 0xDFFF {
			return decodeErrf("", ErrLoneSurrogate, "unpaired high surrogate \\u%04x", r)
		}
		i += 11
	}
	return nil
}

func parseHex4(b []byte) (int, bool) {
	v := 0
	for _, c := range b {
		switch {
		case '0' <= c && c <= '9':
			v = v<<4 | int(c-'0')
		case 'a' <= c && c <= 'f':
			v = v<<4 | int(c-'a'+10)
		case 'A' <= c && c <= 'F':
			v = v<<4 | int(c-'A'+10)
		default:
			return 0, false
		}
	}
	return v, true
}

// strictParse decodes exactly one JSON value from data with zatiti wire
// strictness: valid UTF-8, no lone surrogate escapes, no duplicate object
// keys at any level, no trailing data, numbers kept as exact json.Number
// literals. Objects decode to map[string]any, arrays to []any.
func strictParse(data []byte) (any, *DecodeError) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, decodeErrf("", ErrMalformedJSON, "empty input")
	}
	if !utf8.Valid(data) {
		return nil, decodeErrf("", ErrInvalidUTF8, "")
	}
	if err := checkSurrogateEscapes(data); err != nil {
		return nil, decodeErrf("", ErrLoneSurrogate, "")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, perr := decodeValue(dec)
	if perr != nil {
		return nil, perr
	}
	// Exactly one value: the next token must be EOF (whitespace allowed).
	if _, err := dec.Token(); err != io.EOF {
		return nil, decodeErrf("", ErrTrailingData, "")
	}
	return v, nil
}

// decodeValue builds one value from the decoder, rejecting duplicate keys.
func decodeValue(dec *json.Decoder) (any, *DecodeError) {
	tok, err := dec.Token()
	if err != nil {
		return nil, decodeErrf("", ErrMalformedJSON, "%v", err)
	}
	return decodeFromToken(dec, tok, "")
}

func decodeFromToken(dec *json.Decoder, tok json.Token, pointer string) (any, *DecodeError) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return decodeObject(dec, pointer)
		case '[':
			return decodeArray(dec, pointer)
		}
		return nil, decodeErrf(pointer, ErrMalformedJSON, "unexpected delimiter %v", t)
	default:
		if n, ok := tok.(json.Number); ok && integerLiteral(n) {
			// Integer precision agreement: every integer literal anywhere in
			// the document must fit int64, in every entry point.
			if _, err := parseInteger(n); err != nil {
				return nil, decodeErrf(pointer, ErrIntegerOverflow, "")
			}
		}
		return tok, nil
	}
}

func decodeObject(dec *json.Decoder, pointer string) (any, *DecodeError) {
	obj := make(map[string]any)
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, decodeErrf(pointer, ErrMalformedJSON, "%v", err)
		}
		if d, ok := tok.(json.Delim); ok && d == '}' {
			return obj, nil
		}
		key, ok := tok.(string)
		if !ok {
			return nil, decodeErrf(pointer, ErrMalformedJSON, "object key is not a string")
		}
		if _, dup := obj[key]; dup {
			return nil, decodeErrf(pointer+"/"+escapePointerToken(key), ErrDuplicateKey, "")
		}
		valTok, err := dec.Token()
		if err != nil {
			return nil, decodeErrf(pointer, ErrMalformedJSON, "%v", err)
		}
		val, perr := decodeFromToken(dec, valTok, pointer+"/"+escapePointerToken(key))
		if perr != nil {
			return nil, perr
		}
		obj[key] = val
	}
}

func decodeArray(dec *json.Decoder, pointer string) (any, *DecodeError) {
	arr := []any{}
	for dec.More() {
		val, perr := decodeValue(dec)
		if perr != nil {
			return nil, perr
		}
		arr = append(arr, val)
	}
	if _, err := dec.Token(); err != nil { // consume ']'
		return nil, decodeErrf(pointer, ErrMalformedJSON, "%v", err)
	}
	return arr, nil
}

// escapePointerToken applies RFC 6901 escaping to one pointer token.
func escapePointerToken(s string) string {
	if !bytes.ContainsAny([]byte(s), "~/") {
		return s
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '~':
			out = append(out, '~', '0')
		case '/':
			out = append(out, '~', '1')
		default:
			out = append(out, s[i])
		}
	}
	return string(out)
}
