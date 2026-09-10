package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// DecodeStrict decodes exactly one JSON value into v with zatiti wire
// strictness, agreeing with ValidateSchema on duplicate keys and integer
// precision:
//
//   - duplicate object keys are rejected at every level (never last-wins),
//   - unknown fields in struct targets are rejected,
//   - trailing data after the value is rejected,
//   - integer literals outside the int64 range are rejected before decoding,
//   - numbers in open targets decode to json.Number so exact integer values
//     survive (no float64 decay),
//   - input must be valid UTF-8 with no lone surrogate escapes.
//
// The returned error is a *DecodeError wrapping one of the Err* sentinels;
// type mismatches against typed targets keep the underlying
// *json.UnmarshalTypeError for errors.As.
func DecodeStrict(data []byte, v any) error {
	if v == nil {
		return &DecodeError{Err: errors.New("decode: nil target")}
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return &DecodeError{Err: ErrMalformedJSON}
	}
	// Structural pre-pass: UTF-8, surrogate escapes, duplicate keys,
	// trailing data and int64 range. The second pass performs the typed
	// strict decode against the target.
	if _, derr := strictParse(data); derr != nil {
		return derr
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return decodeErrorFrom(err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return &DecodeError{Err: ErrTrailingData}
	}
	return nil
}

// decodeErrorFrom classifies decoder errors: syntax as ErrMalformedJSON,
// unknown fields as ErrUnknownField, and type mismatches as the original
// *json.UnmarshalTypeError (path preserved from its Field).
func decodeErrorFrom(err error) error {
	var terr *json.UnmarshalTypeError
	if errors.As(err, &terr) {
		ptr := ""
		if terr.Field != "" {
			ptr = "/" + strings.ReplaceAll(terr.Field, ".", "/")
		}
		return &DecodeError{Pointer: ptr, Err: terr}
	}
	var serr *json.SyntaxError
	if errors.As(err, &serr) {
		return &DecodeError{Err: fmt.Errorf("%w: %s", ErrMalformedJSON, serr.Error())}
	}
	if field, ok := unknownFieldFrom(err); ok {
		return &DecodeError{
			Pointer: "/" + escapePointerToken(field),
			Err:     fmt.Errorf("%w: %q", ErrUnknownField, field),
		}
	}
	return &DecodeError{Err: err}
}

// unknownFieldFrom extracts the field name from the standard library's
// untyped unknown-field error ("json: unknown field \"x\"").
func unknownFieldFrom(err error) (string, bool) {
	const prefix = `json: unknown field "`
	msg := err.Error()
	i := strings.Index(msg, prefix)
	if i < 0 {
		return "", false
	}
	rest := msg[i+len(prefix):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return "", false
	}
	return rest[:j], true
}
