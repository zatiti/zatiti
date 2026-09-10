package contract

import (
	"errors"
	"strings"
	"testing"
)

func TestCanonicalizeGoldenVectors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"keys sorted", `{"b":2,"a":1}`, `{"a":1,"b":2}`},
		{"nested keys sorted", `{"z":[3,1,2],"a":{"d":4,"c":[true,null,"x"]}}`, `{"a":{"c":[true,null,"x"],"d":4},"z":[3,1,2]}`},
		{"int64 max", `{"v":9223372036854775807}`, `{"v":9223372036854775807}`},
		{"int64 min", `{"v":-9223372036854775808}`, `{"v":-9223372036854775808}`},
		{"negative zero integer normalized", `{"v":-0}`, `{"v":0}`},
		{"non-integer literal preserved", `{"v":1.50}`, `{"v":1.50}`},
		{"exponent literal preserved", `{"v":1e2}`, `{"v":1e2}`},
		{"escaped unicode normalized", `"caf\u00e9"`, `"café"`},
		{"unicode passthrough", `"日本語"`, `"日本語"`},
		{"required escapes only", `"a\"b\\c\nd\te"`, `"a\"b\\c\nd\te"`},
		{"control char escaped lowercase hex", "{\"a\":\"\\u000B\"}", "{\"a\":\"\\u000b\"}"},
		{"empty object", `{}`, `{}`},
		{"empty array", `[]`, `[]`},
		{"whitespace stripped", `  {  "a" : 1 }  `, `{"a":1}`},
		{"array order preserved", `[3,1,2]`, `[3,1,2]`},
		{"lone value true", `true`, `true`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Canonicalize([]byte(tc.in))
			if err != nil {
				t.Fatalf("Canonicalize(%s) returned error: %v", tc.in, err)
			}
			if string(got) != tc.want {
				t.Fatalf("Canonicalize(%s) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestCanonicalizeRejects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"duplicate key top level", `{"a":1,"a":2}`, ErrDuplicateKey},
		{"duplicate nested key", `{"x":{"a":1,"a":2}}`, ErrDuplicateKey},
		{"duplicate in array element", `[{"a":1,"a":2}]`, ErrDuplicateKey},
		{"integer overflow above int64", `{"v":9223372036854775808}`, ErrIntegerOverflow},
		{"integer underflow below int64", `{"v":-9223372036854775809}`, ErrIntegerOverflow},
		{"trailing value", `{"a":1} {"b":2}`, ErrTrailingData},
		{"trailing garbage", `{} x`, ErrTrailingData},
		{"empty input", ``, ErrMalformedJSON},
		{"whitespace only", `   `, ErrMalformedJSON},
		{"truncated", `{"a":`, ErrMalformedJSON},
		{"invalid utf-8", "\xff", ErrInvalidUTF8},
		{"lone high surrogate", `"\ud800"`, ErrLoneSurrogate},
		{"lone high surrogate before other escape", `"\ud800x"`, ErrLoneSurrogate},
		{"lone low surrogate", `"\udc00"`, ErrLoneSurrogate},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Canonicalize([]byte(tc.in))
			if !errors.Is(err, tc.want) {
				t.Fatalf("Canonicalize(%q) error = %v, want %v", tc.in, err, tc.want)
			}
		})
	}
}

func TestCanonicalizeValidSurrogatePair(t *testing.T) {
	t.Parallel()
	got, err := Canonicalize([]byte(`"😀"`))
	if err != nil {
		t.Fatalf("paired surrogate rejected: %v", err)
	}
	if string(got) != "\"😀\"" {
		t.Fatalf("canonical form = %s, want raw emoji", got)
	}
}

func TestCanonicalizeIdempotent(t *testing.T) {
	t.Parallel()
	in := []byte(`{"b":2,"a":{"y":[1,2],"x":null},"c":"café"}`)
	once, err := Canonicalize(in)
	if err != nil {
		t.Fatalf("first canonicalize: %v", err)
	}
	twice, err := Canonicalize(once)
	if err != nil {
		t.Fatalf("second canonicalize: %v", err)
	}
	if string(once) != string(twice) {
		t.Fatalf("canonicalization not idempotent: %s vs %s", once, twice)
	}
}

func TestCanonicalizeDuplicateKeyErrorCarriesPointer(t *testing.T) {
	t.Parallel()
	_, err := Canonicalize([]byte(`{"x":{"a":1,"a":2}}`))
	var de *DecodeError
	if !errors.As(err, &de) {
		t.Fatalf("error is not *DecodeError: %v", err)
	}
	if de.Pointer != "/x/a" {
		t.Fatalf("pointer = %q, want /x/a", de.Pointer)
	}
}

func TestCanonicalizeSetsSortsSchemaDeclaredSets(t *testing.T) {
	t.Parallel()
	schema := []byte(`{
		"type": "object",
		"required": ["tags", "keep"],
		"additionalProperties": false,
		"properties": {
			"tags": {"type": "array", "uniqueItems": true, "items": {"type": "string"}},
			"keep": {"type": "array", "items": {"type": "integer"}}
		}
	}`)
	data := []byte(`{"tags":["zulu","alpha","mike"],"keep":[3,1,2]}`)
	got, err := canonicalizeSets(data, schema)
	if err != nil {
		t.Fatalf("canonicalizeSets: %v", err)
	}
	want := `{"keep":[3,1,2],"tags":["alpha","mike","zulu"]}`
	if string(got) != want {
		t.Fatalf("canonicalizeSets = %s, want %s", got, want)
	}
	// Order-insensitive set members produce the same canonical bytes.
	got2, err := canonicalizeSets([]byte(`{"tags":["alpha","mike","zulu"],"keep":[3,1,2]}`), schema)
	if err != nil {
		t.Fatalf("canonicalizeSets second form: %v", err)
	}
	if string(got) != string(got2) {
		t.Fatalf("set forms differ: %s vs %s", got, got2)
	}
}

func TestHashExactBytes(t *testing.T) {
	t.Parallel()
	if got := Hash([]byte("abc")); got != Digest("ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad") {
		t.Fatalf("Hash(abc) = %s", got)
	}
	// Hash operates on exact bytes: whitespace changes the digest.
	if Hash([]byte(`{"a":1}`)) == Hash([]byte(`{ "a" : 1 }`)) {
		t.Fatalf("Hash treated differently-spelled bytes as equal")
	}
	// Callers canonicalize first: canonical forms hash equally.
	a, err := Canonicalize([]byte(`{"b":2,"a":1}`))
	if err != nil {
		t.Fatalf("canonicalize a: %v", err)
	}
	b, err := Canonicalize([]byte(`{"a":1,"b":2}`))
	if err != nil {
		t.Fatalf("canonicalize b: %v", err)
	}
	if Hash(a) != Hash(b) {
		t.Fatalf("canonical forms did not hash equally")
	}
	if got := Hash(nil); len(got) != 64 || strings.ToLower(string(got)) != string(got) {
		t.Fatalf("Hash(nil) = %s, want 64 lowercase hex chars", got)
	}
}

func TestSkillBytesPreservedByHash(t *testing.T) {
	t.Parallel()
	// Exact skill bytes: two archives differing by one byte hash differently.
	a := []byte("skill-archive-bytes-v1")
	b := []byte("skill-archive-bytes-v2")
	if Hash(a) == Hash(b) {
		t.Fatalf("distinct byte sequences hashed equally")
	}
	if Hash(a) != Hash([]byte("skill-archive-bytes-v1")) {
		t.Fatalf("Hash is not stable for identical bytes")
	}
}
