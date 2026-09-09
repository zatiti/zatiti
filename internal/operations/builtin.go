// internal/operations/builtin.go
//
// Declared operations, frozen at construction. Registration order is
// irrelevant; Names() is canonical.

package operations

// NewBuiltin returns the registry of implemented operations.
// Implemented: organization.list (read), organization.create (mutating).
func NewBuiltin() (*Registry, error) {
	r := NewRegistry()

	if err := r.Register(Op{
		Name:       "organization.list",
		Summary:    "list organizations visible to the caller",
		Mutability: ReadOnly,
	}); err != nil {
		return nil, err
	}
	if err := r.Register(Op{
		Name:       "organization.create",
		Summary:    "create a new organization",
		Mutability: Mutating,
		Fields: []Field{
			{Name: "name", Type: TString, Required: true, Help: "unique organization name"},
		},
	}); err != nil {
		return nil, err
	}

	if err := r.Register(Op{
		Name:       "audit.list",
		Summary:    "list audit journal entries for an organization",
		Mutability: ReadOnly,
		Fields: []Field{
			{Name: "org", Type: TString, Required: false, Help: "organization name (defaults to root)"},
			{Name: "limit", Type: TUint, Required: false, Help: "max entries (default 100)"},
		},
	}); err != nil {
		return nil, err
	}
	if err := r.Register(Op{
		Name:       "principal.create",
		Summary:    "register an operator principal for the root organization",
		Mutability: Mutating,
		Fields: []Field{
			{Name: "name", Type: TString, Required: true, Help: "operator label"},
			{Name: "role", Type: TString, Required: true, Help: "operator|admin|auditor"},
		},
	}); err != nil {
		return nil, err
	}
	if err := r.Register(Op{
		Name:       "credential.issue",
		Summary:    "issue a credential for a pending principal (owner only)",
		Mutability: Mutating,
		Fields: []Field{
			{Name: "principal", Type: TString, Required: true, Help: "principal ID or label"},
		},
	}); err != nil {
		return nil, err
	}
	if err := r.Register(Op{
		Name:       "organization.purge",
		Summary:    "irreversibly delete an organization and its memberships (via review)",
		Mutability: Destructive,
		Fields: []Field{
			{Name: "org", Type: TString, Required: true, Help: "organization name to purge"},
		},
	}); err != nil {
		return nil, err
	}
	if err := r.Register(Op{
		Name:       "review.submit",
		Summary:    "open a review proposal for a destructive operation",
		Mutability: Mutating,
		Fields: []Field{
			{Name: "op", Type: TString, Required: true, Help: "destructive operation name"},
			{Name: "org", Type: TString, Required: true, Help: "target organization name"},
		},
	}); err != nil {
		return nil, err
	}
	if err := r.Register(Op{
		Name:       "review.approve",
		Summary:    "approve a pending review (2 distinct approvals required; proposer excluded)",
		Mutability: Mutating,
		Fields: []Field{
			{Name: "review", Type: TString, Required: true, Help: "review ID"},
		},
	}); err != nil {
		return nil, err
	}
	if err := r.Register(Op{
		Name:       "review.execute",
		Summary:    "execute a review that has met quorum",
		Mutability: Mutating,
		Fields: []Field{
			{Name: "review", Type: TString, Required: true, Help: "review ID"},
		},
	}); err != nil {
		return nil, err
	}

	r.Freeze()
	return r, nil
}
