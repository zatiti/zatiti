package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/zatiti/zatiti/internal/contract"
)

// fakeUnit is the minimal transaction unit for handler-level tests.
type fakeUnit struct {
	actor  contract.Actor
	scope  contract.Scope
	ro     bool
	events []contract.Event
}

func (u *fakeUnit) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, fmt.Errorf("fakeUnit: queries are not implemented")
}

func (u *fakeUnit) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}

func (u *fakeUnit) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, fmt.Errorf("fakeUnit: exec is not implemented")
}

func (u *fakeUnit) Actor() contract.Actor { return u.actor }

func (u *fakeUnit) Scope() contract.Scope { return u.scope }

func (u *fakeUnit) Generation() int64 { return 1 }

func (u *fakeUnit) ReadOnly() bool { return u.ro }

func (u *fakeUnit) Emit(_ context.Context, event contract.Event) error {
	u.events = append(u.events, event)
	return nil
}

// fakeOwner is a module that owns descriptors and answers every invocation
// with a canned completed payload. It deliberately does not implement
// contract.LocalIO.
type fakeOwner struct {
	name        string
	descriptors []contract.Descriptor
	calls       []contract.Invocation
}

func newFakeOwner(name string, descriptors []contract.Descriptor) *fakeOwner {
	return &fakeOwner{name: name, descriptors: descriptors}
}

func (m *fakeOwner) Name() string { return m.name }

func (m *fakeOwner) Migrations() []contract.Migration { return nil }

func (m *fakeOwner) Descriptors() []contract.Descriptor { return m.descriptors }

func (m *fakeOwner) Handle(_ context.Context, _ contract.Unit, invocation contract.Invocation) (contract.Payload, error) {
	m.calls = append(m.calls, invocation)
	return contract.Payload{Status: contract.StatusCompleted, Data: json.RawMessage(`{}`)}, nil
}

// fakeIO records the LocalIO seam stages in call order.
type fakeIO struct {
	prepared  []contract.Invocation
	performed []contract.ID
	finished  []contract.ID
}

func (f *fakeIO) Prepare(_ context.Context, _ contract.Unit, invocation contract.Invocation) (contract.IOPlan, error) {
	f.prepared = append(f.prepared, invocation)
	return contract.IOPlan{Owner: invocation.Operation, Invocation: invocation}, nil
}

func (f *fakeIO) Perform(_ context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	f.performed = append(f.performed, plan.ID)
	return contract.IOResult{Data: json.RawMessage(`{}`)}, nil
}

func (f *fakeIO) Finish(_ context.Context, unit contract.Unit, plan contract.IOPlan, result contract.IOResult) (contract.Payload, error) {
	f.finished = append(f.finished, plan.ID)
	return contract.Payload{Status: contract.StatusCompleted, Data: result.Data}, nil
}

// fakeOwnerIO is a module that also implements contract.LocalIO.
type fakeOwnerIO struct {
	*fakeOwner
	io fakeIO
}

func newFakeOwnerIO(name string, descriptors []contract.Descriptor) *fakeOwnerIO {
	return &fakeOwnerIO{fakeOwner: newFakeOwner(name, descriptors)}
}

func (m *fakeOwnerIO) Prepare(ctx context.Context, unit contract.Unit, invocation contract.Invocation) (contract.IOPlan, error) {
	return m.io.Prepare(ctx, unit, invocation)
}

func (m *fakeOwnerIO) Perform(ctx context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	return m.io.Perform(ctx, plan)
}

func (m *fakeOwnerIO) Finish(ctx context.Context, unit contract.Unit, plan contract.IOPlan, result contract.IOResult) (contract.Payload, error) {
	return m.io.Finish(ctx, unit, plan, result)
}

// catalogModules builds one fake module per catalog owner. Owners with
// local-IO operations implement contract.LocalIO, as the frozen seam
// requires. The registry's own operations are excluded: New registers them
// itself.
func catalogModules() []contract.Module {
	cat, err := loadCatalog()
	if err != nil {
		panic(err) // catalog is compile-time frozen and catalog_test.go guards it
	}
	owners := map[string][]contract.Descriptor{}
	localOwners := map[string]bool{}
	for i := range cat.document.Operations {
		op := &cat.document.Operations[i]
		if op.Owner == "registry" {
			continue
		}
		owners[op.Owner] = append(owners[op.Owner], op.descriptor())
		if localIOOperations[op.ID] {
			localOwners[op.Owner] = true
		}
	}
	var names []string
	for name := range owners {
		names = append(names, name)
	}
	sort.Strings(names)
	modules := make([]contract.Module, 0, len(names))
	for _, name := range names {
		if localOwners[name] {
			modules = append(modules, newFakeOwnerIO(name, owners[name]))
		} else {
			modules = append(modules, newFakeOwner(name, owners[name]))
		}
	}
	return modules
}

// newFakeRegistry assembles a complete registry over the frozen catalog.
func newFakeRegistry() (*Registry, error) {
	return New(catalogModules())
}
