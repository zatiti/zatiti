// internal/authz/authz_test.go
package authz

import (
	"context"
	"testing"

	"zatiti/internal/operations"
)

// decideWith uses the real policy table against a synthetic role,
// bypassing the DB by testing Decide's table logic through memberRole's
// outcome — for table purity we test the mapping directly.
func TestRoleCapabilityTable(t *testing.T) {
	cases := []struct {
		role  string
		mut   operations.Mutability
		allow bool
	}{
		{"owner", operations.ReadOnly, true},
		{"owner", operations.Mutating, true},
		{"owner", operations.Destructive, true},
		{"admin", operations.Destructive, false},
		{"admin", operations.Mutating, true},
		{"operator", operations.Mutating, true},
		{"operator", operations.Destructive, false},
		{"auditor", operations.ReadOnly, true},
		{"auditor", operations.Mutating, false},
	}
	for _, c := range cases {
		caps, ok := roleCapabilities[c.role]
		if !ok {
			t.Fatalf("role %q missing from policy table", c.role)
		}
		if caps[c.mut] != c.allow {
			t.Fatalf("role %q mutability %d: got %v, want %v", c.role, c.mut, caps[c.mut], c.allow)
		}
	}
}

func TestDecideFailClosedOnEmpty(t *testing.T) {
	// Constructing with a nil DB is safe: fail-closed branches must
	// fire before any query.
	a := &Authorizer{}
	op := &operations.Op{Name: "x.y", Mutability: operations.Mutating}
	dec, err := a.Decide(context.Background(), "", "org", op)
	if err != nil {
		t.Fatalf("Decide errored: %v", err)
	}
	if dec.Allow {
		t.Fatal("empty actor allowed")
	}
}
