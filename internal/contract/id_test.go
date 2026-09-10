package contract

import (
	"strings"
	"testing"
)

func TestNewIDFormat(t *testing.T) {
	t.Parallel()
	seen := make(map[ID]bool, 256)
	for i := 0; i < 256; i++ {
		id := NewID()
		if !validID(id) {
			t.Fatalf("NewID() = %q, not a valid UUID", id)
		}
		s := string(id)
		if s != strings.ToLower(s) {
			t.Fatalf("NewID() = %q, want lowercase", id)
		}
		if s[14] != '4' {
			t.Fatalf("NewID() = %q, want version 4 in position 14", id)
		}
		switch s[19] {
		case '8', '9', 'a', 'b':
		default:
			t.Fatalf("NewID() = %q, want RFC 4122 variant in position 19", id)
		}
		if seen[id] {
			t.Fatalf("NewID() repeated %q within one process", id)
		}
		seen[id] = true
	}
}

func TestValidID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   ID
		want bool
	}{
		{"00000000-0000-4000-8000-000000000001", true},
		{ID(NewID()), true},
		{"00000000-0000-4000-8000-000000000001X", false},
		{"00000000-0000-4000-8000-00000000000", false},
		{"00000000_0000_4000_8000_000000000001", false},
		{"00000000-0000-4000-8000-00000000000g", false},
		{"00000000-0000-4000-8000-000000000001", true},
		{"{00000000-0000-4000-8000-000000000001}", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := validID(tc.in); got != tc.want {
			t.Errorf("validID(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestValidDigest(t *testing.T) {
	t.Parallel()
	good := Hash([]byte("abc"))
	if !validDigest(good) {
		t.Fatalf("Hash output %q is not a valid digest", good)
	}
	bad := []Digest{
		"",
		Digest(good[:63]),
		Digest(strings.ToUpper(string(good))),
		Digest(strings.Repeat("g", 64)),
		good + "0",
	}
	for _, d := range bad {
		if validDigest(d) {
			t.Errorf("validDigest(%q) = true, want false", d)
		}
	}
	if validDigest(Digest(NewID())) {
		t.Errorf("a UUID is not a digest")
	}
}

func TestFaultError(t *testing.T) {
	t.Parallel()
	var nilFault *Fault
	if nilFault.Error() != "<nil>" {
		t.Fatalf("nil fault Error() = %q", nilFault.Error())
	}
	f := &Fault{Code: CodeInvalidInput, Message: "scope is required"}
	if f.Error() != "invalid_input: scope is required" {
		t.Fatalf("Error() = %q", f.Error())
	}
	bare := &Fault{Code: CodeInternalError}
	if bare.Error() != "internal_error" {
		t.Fatalf("Error() = %q", bare.Error())
	}
	var _ error = (*Fault)(nil)
}
