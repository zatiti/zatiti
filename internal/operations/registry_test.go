// internal/operations/registry_test.go
package operations

import "testing"

func TestRegisterRejectsDuplicates(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Op{Name: "a.b", Mutability: ReadOnly}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := r.Register(Op{Name: "a.b", Mutability: ReadOnly}); err == nil {
		t.Fatal("want duplicate error, got nil")
	}
}

func TestMutatingRequiresFields(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Op{Name: "x.y", Mutability: Mutating}); err == nil {
		t.Fatal("want field requirement error, got nil")
	}
}

func TestFrozenRejectsRegistration(t *testing.T) {
	r := NewRegistry()
	r.Freeze()
	if err := r.Register(Op{Name: "late.op", Mutability: ReadOnly}); err == nil {
		t.Fatal("want frozen error, got nil")
	}
}

func TestNamesSorted(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(Op{Name: "b.a", Mutability: ReadOnly})
	_ = r.Register(Op{Name: "a.b", Mutability: ReadOnly})
	names := r.Names()
	if len(names) != 2 || names[0] != "a.b" || names[1] != "b.a" {
		t.Fatalf("names = %v, want sorted", names)
	}
}
