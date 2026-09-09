// internal//registry.go
//
// T1.5: the single registry of controller operations. Both transports
// (CLI and MCP) derive their dispatch and tool listings from this
// registry — there is no second place where operation names or input
// shapes are declared. Registration is explicit and ordered.

package operations

import (
	"fmt"
	"sort"
)

// Mutability classifies whether an operation changes durable state.
type Mutability int

const (
	// ReadOnly never mutates durable state; safe to auto-approve.
	ReadOnly Mutability = iota
	// Mutating changes durable state; requires authorization per RFC §7.
	Mutating
	// Destructive is Mutating with irreversible effect; requires review
	// per RFC §7. Registry entries may not skip this classification.
	Destructive
)

// Op describes one controller operation.
type Op struct {
	// Name is the dotted canonical name, e.g. "organization.create".
	// It is also the CLI command path (spaces → spaces at dispatch).
	Name string
	// Summary is a one-line human description (usage + MCP tool desc).
	Summary string
	// Mutability drives authorization and review gating.
	Mutability Mutability
	// Fields declares the input fields in canonical order. Field order
	// is part of the interface: positional CLI args follow it.
	Fields []Field
}

// Field is one named input with a type tag. Freeform JSON inputs are
// rejected at registration: every field is typed and validated.
type Field struct {
	Name     string
	Type     FieldType
	Required bool
	Help     string
}

type FieldType int

const (
	TString FieldType = iota
	TUint
	TDuration
	TBool
)

// Registry holds declared operations. Immutable after Freeze.
type Registry struct {
	ops    map[string]*Op
	frozen bool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{ops: map[string]*Op{}}
}

// Register adds an operation. Duplicate names and empty names are
// construction-time failures, not runtime surprises.
func (r *Registry) Register(op Op) error {
	if r.frozen {
		return fmt.Errorf("operations: registry is frozen, cannot register %q", op.Name)
	}
	if op.Name == "" {
		return fmt.Errorf("operations: operation name is required")
	}
	if _, dup := r.ops[op.Name]; dup {
		return fmt.Errorf("operations: duplicate operation %q", op.Name)
	}
	if op.Mutability != ReadOnly && len(op.Fields) == 0 {
		// A mutating op with no fields is almost always a declaration
		// mistake (missing target selector); refuse early.
		return fmt.Errorf("operations: mutating operation %q declares no input fields", op.Name)
	}
	opCopy := op
	r.ops[op.Name] = &opCopy
	return nil
}

// Freeze seals the registry. Lookup-only access afterwards.
func (r *Registry) Freeze() { r.frozen = true }

// Lookup returns the operation by canonical name.
func (r *Registry) Lookup(name string) (*Op, bool) {
	op, ok := r.ops[name]
	return op, ok
}

// Names returns all operation names in sorted order (stable interface
// enumeration for help text and MCP tool listing).
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.ops))
	for name := range r.ops {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
