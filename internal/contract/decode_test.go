package contract

import (
	"encoding/json"
	"errors"
	"testing"
)

type decodeTarget struct {
	Name    string          `json:"name"`
	Version int64           `json:"version"`
	Count   int64           `json:"count"`
	Extra   json.RawMessage `json:"extra,omitempty"`
}

func TestDecodeStrictAccepts(t *testing.T) {
	t.Parallel()
	var tgt decodeTarget
	err := DecodeStrict([]byte(`{"name":"widget","version":3,"count":-9,"extra":{"k":[1,2]}}`), &tgt)
	if err != nil {
		t.Fatalf("DecodeStrict: %v", err)
	}
	if tgt.Name != "widget" || tgt.Version != 3 || tgt.Count != -9 {
		t.Fatalf("decoded = %+v", tgt)
	}
	if string(tgt.Extra) != `{"k":[1,2]}` {
		t.Fatalf("raw extra = %s", tgt.Extra)
	}
}

func TestDecodeStrictRejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"unknown field", `{"name":"w","bogus":1}`, ErrUnknownField},
		{"duplicate key", `{"name":"a","name":"b"}`, ErrDuplicateKey},
		{"nested duplicate key", `{"extra":{"a":1,"a":2}}`, ErrDuplicateKey},
		{"integer overflow", `{"version":9223372036854775808}`, ErrIntegerOverflow},
		{"integer underflow", `{"count":-9223372036854775809}`, ErrIntegerOverflow},
		{"trailing data", `{"name":"a"} x`, ErrTrailingData},
		{"empty", ``, ErrMalformedJSON},
		{"lone surrogate", `{"name":"\ud800"}`, ErrLoneSurrogate},
		{"type mismatch", `{"version":"3"}`, nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var tgt decodeTarget
			err := DecodeStrict([]byte(tc.in), &tgt)
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("DecodeStrict(%s) error = %v, want %v", tc.in, err, tc.want)
				}
				return
			}
			var terr *json.UnmarshalTypeError
			if !errors.As(err, &terr) {
				t.Fatalf("DecodeStrict(%s) error = %v, want UnmarshalTypeError", tc.in, err)
			}
		})
	}
}

func TestDecodeStrictUnknownFieldErrorIsTyped(t *testing.T) {
	t.Parallel()
	var tgt decodeTarget
	err := DecodeStrict([]byte(`{"name":"w","bogus":true}`), &tgt)
	if !errors.Is(err, ErrUnknownField) {
		t.Fatalf("error = %v, want ErrUnknownField", err)
	}
	var de *DecodeError
	if !errors.As(err, &de) || de.Pointer != "/bogus" {
		t.Fatalf("pointer = %+v, want /bogus", de)
	}
}

func TestDecodeStrictOpenTargetKeepsExactIntegers(t *testing.T) {
	t.Parallel()
	var open map[string]any
	err := DecodeStrict([]byte(`{"v":123,"f":1.5}`), &open)
	if err != nil {
		t.Fatalf("DecodeStrict: %v", err)
	}
	n, ok := open["v"].(json.Number)
	if !ok || n.String() != "123" {
		t.Fatalf("open integer decoded as %T %v, want json.Number 123", open["v"], open["v"])
	}
	f, ok := open["f"].(json.Number)
	if !ok || f.String() != "1.5" {
		t.Fatalf("open decimal decoded as %T %v, want json.Number 1.5", open["f"], open["f"])
	}
}

func TestDecodeStrictRejectsOversizedIntegersInOpenTargets(t *testing.T) {
	t.Parallel()
	var open map[string]any
	err := DecodeStrict([]byte(`{"v":99999999999999999999}`), &open)
	if !errors.Is(err, ErrIntegerOverflow) {
		t.Fatalf("error = %v, want ErrIntegerOverflow (integer precision agreement)", err)
	}
}

func TestDecodeStrictNilTarget(t *testing.T) {
	t.Parallel()
	err := DecodeStrict([]byte(`{}`), nil)
	if err == nil {
		t.Fatalf("nil target accepted")
	}
}

func TestDecodeStrictAndSchemaAgreeOnDuplicateKeysAndIntegers(t *testing.T) {
	t.Parallel()
	schema := json.RawMessage(`{
		"type": "object",
		"additionalProperties": false,
		"required": ["version"],
		"properties": {"version": {"type": "integer"}}
	}`)

	// Duplicate keys: both reject.
	dup := []byte(`{"version":1,"version":2}`)
	var tgt decodeTarget
	if err := DecodeStrict(dup, &tgt); !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("DecodeStrict duplicate = %v, want ErrDuplicateKey", err)
	}
	if err := ValidateSchema(schema, json.RawMessage(dup)); !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("ValidateSchema duplicate = %v, want ErrDuplicateKey", err)
	}

	// Integer precision: a non-integer literal is rejected as type integer by
	// the schema and as an int64 target by decoding; an in-range integer is
	// accepted by both.
	frac := []byte(`{"version":1.5}`)
	if err := ValidateSchema(schema, json.RawMessage(frac)); !errors.Is(err, ErrSchemaValidation) {
		t.Fatalf("ValidateSchema 1.5 = %v, want type error", err)
	}
	if err := DecodeStrict(frac, &tgt); err == nil {
		t.Fatalf("DecodeStrict 1.5 into int64 succeeded")
	}
	whole := []byte(`{"version":7}`)
	if err := ValidateSchema(schema, json.RawMessage(whole)); err != nil {
		t.Fatalf("ValidateSchema 7 = %v, want nil", err)
	}
	if err := DecodeStrict(whole, &tgt); err != nil {
		t.Fatalf("DecodeStrict 7 = %v, want nil", err)
	}
}

func TestRequestEnvelopeRoundTrip(t *testing.T) {
	t.Parallel()
	req := Request{
		Schema:        SchemaRequest,
		SubmissionKey: "abc-123",
		Input:         json.RawMessage(`{"x":1}`),
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Request
	if err := DecodeStrict(raw, &back); err != nil {
		t.Fatalf("strict decode of own envelope: %v", err)
	}
	if back.Schema != SchemaRequest || back.SubmissionKey != "abc-123" || string(back.Input) != `{"x":1}` {
		t.Fatalf("round trip mismatch: %+v", back)
	}
	// Snake_case and omitempty behavior.
	empty, err := json.Marshal(Request{Schema: SchemaRequest, Input: json.RawMessage(`null`)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(empty) != `{"schema":"zatiti.request/v1","input":null}` {
		t.Fatalf("empty submission key must be omitted: %s", empty)
	}
}
