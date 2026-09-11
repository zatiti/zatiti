package connections

import (
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Unit tests for the owner-local storage helpers: stamp and string-array
// codecs and the cursor envelope binding actor and query identity.

func TestFormatParseStampRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
	}{
		{"zero-utc", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		{"nanoseconds", time.Date(2026, 9, 10, 12, 0, 0, 123456789, time.UTC)},
		{"non-utc-normalizes", time.Date(2026, 9, 10, 12, 0, 0, 0, time.FixedZone("X", 2*3600))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stored := formatStamp(tc.in)
			if !strings.HasSuffix(stored, "Z") {
				t.Fatalf("stored stamp %q is not UTC", stored)
			}
			parsed, err := parseStamp(stored)
			if err != nil {
				t.Fatalf("parseStamp: %v", err)
			}
			if !parsed.Equal(tc.in) {
				t.Fatalf("round trip lost the instant: got %s want %s", parsed, tc.in)
			}
		})
	}
}

func TestParseStampRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "not-a-stamp", "2026-13-40T99:00:00Z"} {
		if _, err := parseStamp(s); err == nil {
			t.Fatalf("parseStamp(%q) succeeded, want error", s)
		}
	}
}

func TestMarshalStringsNilEncodesAsEmptyArray(t *testing.T) {
	if got := marshalStrings(nil); got != "[]" {
		t.Fatalf("marshalStrings(nil) = %q, want []", got)
	}
	if got := marshalStrings([]string{}); got != "[]" {
		t.Fatalf("marshalStrings(empty) = %q, want []", got)
	}
	if got := marshalStrings([]string{"a", "b"}); got != `["a","b"]` {
		t.Fatalf("marshalStrings = %q", got)
	}
}

func TestScanStrings(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{"empty-column", "", []string{}, false},
		{"empty-array", "[]", []string{}, false},
		{"values", `["a","b"]`, []string{"a", "b"}, false},
		{"not-an-array", `{"x":1}`, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := scanStrings(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("scanStrings(%q) succeeded, want error", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("scanStrings(%q): %v", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("scanStrings(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("scanStrings(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestQueryIDBindsFilterAndLimit(t *testing.T) {
	filterA := listFilter{State: connStateValid}
	filterB := listFilter{State: connStateRevoked}
	first, second := queryID(filterA, 100), queryID(filterA, 100)
	if first != second {
		t.Fatalf("queryID is not deterministic for equal inputs")
	}
	if queryID(filterA, 100) == queryID(filterB, 100) {
		t.Fatalf("queryID ignores the filter")
	}
	if queryID(filterA, 100) == queryID(filterA, 50) {
		t.Fatalf("queryID ignores the limit")
	}
}

func TestCursorRoundTrip(t *testing.T) {
	actor := contract.ID("00000000-0000-4000-8000-000000000001")
	encoded, err := encodeCursor(cursorPayload{Offset: 7, QueryID: testDigest("q"), Actor: actor})
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}
	decoded, err := decodeCursor(encoded)
	if err != nil {
		t.Fatalf("decodeCursor: %v", err)
	}
	if decoded.Offset != 7 || decoded.QueryID != testDigest("q") || decoded.Actor != actor {
		t.Fatalf("cursor round trip mismatch: %+v", decoded)
	}
}

func TestDecodeCursorRefusesMalformed(t *testing.T) {
	for _, s := range []string{"", "not-base64!!!", "YWJj"} { // "abc" decodes but is not JSON
		_, err := decodeCursor(s)
		f, ok := err.(*contract.Fault)
		if !ok || f.Code != contract.CodeCursorExpired {
			t.Fatalf("decodeCursor(%q) = %v, want cursor_expired", s, err)
		}
	}
}
