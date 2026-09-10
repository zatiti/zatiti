package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/zatiti/zatiti/internal/contract"
)

// Operation IDs served by this module.
const (
	opActivate  = "_identity.activate"
	opAuthority = "_identity.authority"
	opBootstrap = "_identity.bootstrap"
	opPromote   = "_identity.promote"
	opRestrict  = "_identity.restrict"
	opValidate  = "_identity.validate"

	opCredProvision   = "credential.provision"
	opCredRevoke      = "credential.revoke"
	opGrantCreate     = "grant.create"
	opGrantGet        = "grant.get"
	opGrantList       = "grant.list"
	opGrantRevoke     = "grant.revoke"
	opGrantUpdate     = "grant.update"
	opPrincipalCreate = "principal.create"
	opPrincipalGet    = "principal.get"
	opPrincipalList   = "principal.list"
	opPrincipalRevoke = "principal.revoke"
	opPrincipalUpdate = "principal.update"
)

// descriptorVersion is the single contract version every descriptor serves.
const descriptorVersion int64 = 1

// Service implements contract.Module and contract.Authenticator for the
// identity domain. Construct it with New; all state is ready before the
// first call and no goroutines or peer queries run during construction.
type Service struct {
	deps     contract.Dependencies
	migs     []contract.Migration
	descs    []contract.Descriptor
	handlers map[string]contract.Handler
	schemas  map[string]wireSchema

	cursorMu  sync.Mutex
	cursorKey []byte
}

// New validates the injected dependencies and assembles the module's static
// contract. The secret store is optional at construction: credential
// provisioning and bootstrap return prerequisite_missing when it is absent.
func New(deps contract.Dependencies) (*Service, error) {
	if deps.Clock == nil {
		return nil, errors.New("identity: a clock dependency is required")
	}
	if deps.IDs == nil {
		return nil, errors.New("identity: an identity source is required")
	}
	s := &Service{
		deps:     deps,
		migs:     migrations(),
		handlers: make(map[string]contract.Handler, 18),
		schemas:  make(map[string]wireSchema, 18),
	}
	if err := s.assemble(); err != nil {
		return nil, err
	}
	return s, nil
}

// Name implements contract.Module.
func (s *Service) Name() string { return owner }

// Migrations implements contract.Module.
func (s *Service) Migrations() []contract.Migration {
	out := make([]contract.Migration, len(s.migs))
	copy(out, s.migs)
	return out
}

// Descriptors implements contract.Module.
func (s *Service) Descriptors() []contract.Descriptor {
	out := make([]contract.Descriptor, len(s.descs))
	copy(out, s.descs)
	return out
}

// Handle implements contract.Module with strict dispatch: the operation must
// be registered and its declared handler performs version checking, schema
// validation and strict decoding before execution. Handler errors carry a
// *contract.Fault and roll back the surrounding transaction.
func (s *Service) Handle(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	if inv.Operation == "" {
		return contract.Payload{}, invalidInput("operation is required")
	}
	h, ok := s.handlers[inv.Operation]
	if !ok {
		return contract.Payload{}, invalidInput("unknown operation %q", inv.Operation)
	}
	return h(ctx, unit, inv)
}

// assemble builds the descriptor table and binds every handler.
func (s *Service) assemble() error {
	specs := []contract.Descriptor{
		{
			ID: opActivate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityInternal, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			ScopeRequired: []string{"installation_id"},
			Callers:       []string{"configuration", "application"},
		},
		{
			ID: opAuthority, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityInternal, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			ScopeRequired: []string{"installation_id"},
			Callers:       []string{"application", "policy", "reviews", "configuration", "execution", "effects"},
		},
		{
			ID: opBootstrap, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityInternal, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			ScopeRequired: []string{"installation_id"},
			Callers:       []string{"installation"},
		},
		{
			ID: opPromote, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityInternal, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			ScopeRequired: []string{"installation_id"},
			Callers:       []string{"policy"},
		},
		{
			ID: opRestrict, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityInternal, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			ScopeRequired: []string{"installation_id"},
			Callers:       []string{"policy", "installation"},
		},
		{
			ID: opValidate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityInternal, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			ScopeRequired: []string{"installation_id"},
			Callers:       []string{"configuration", "application"},
		},
		{
			ID: opCredProvision, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:           []string{"credential", "provision"},
			MCP:           "zatiti_credential_provision",
			ScopeRequired: []string{"installation_id"},
			SubmissionKey: true,
		},
		{
			ID: opCredRevoke, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"credential", "revoke"},
			MCP:             "zatiti_credential_revoke",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opGrantCreate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:           []string{"grant", "create"},
			MCP:           "zatiti_grant_create",
			ScopeRequired: []string{"installation_id"},
			SubmissionKey: true,
		},
		{
			ID: opGrantGet, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI:           []string{"grant", "get"},
			MCP:           "zatiti_grant_get",
			ScopeRequired: []string{"installation_id"},
		},
		{
			ID: opGrantList, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI:           []string{"grant", "list"},
			MCP:           "zatiti_grant_list",
			ScopeRequired: []string{"installation_id"},
		},
		{
			ID: opGrantRevoke, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"grant", "revoke"},
			MCP:             "zatiti_grant_revoke",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opGrantUpdate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"grant", "update"},
			MCP:             "zatiti_grant_update",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opPrincipalCreate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:           []string{"principal", "create"},
			MCP:           "zatiti_principal_create",
			ScopeRequired: []string{"installation_id"},
			SubmissionKey: true,
		},
		{
			ID: opPrincipalGet, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI:           []string{"principal", "get"},
			MCP:           "zatiti_principal_get",
			ScopeRequired: []string{"installation_id"},
		},
		{
			ID: opPrincipalList, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI:           []string{"principal", "list"},
			MCP:           "zatiti_principal_list",
			ScopeRequired: []string{"installation_id"},
		},
		{
			ID: opPrincipalRevoke, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"principal", "revoke"},
			MCP:             "zatiti_principal_revoke",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opPrincipalUpdate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"principal", "update"},
			MCP:             "zatiti_principal_update",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
	}

	// Merge the shared $defs document into every per-operation schema so
	// document-local $refs resolve against the exact brief definitions.
	for _, d := range specs {
		sch, ok := wireSchemas[d.ID]
		if !ok {
			return fmt.Errorf("identity: operation %s has no embedded schema", d.ID)
		}
		var err error
		d.InputSchema, err = mergeSchema(sch.Input)
		if err != nil {
			return fmt.Errorf("identity: operation %s input schema: %w", d.ID, err)
		}
		d.OutputSchema, err = mergeSchema(sch.Output)
		if err != nil {
			return fmt.Errorf("identity: operation %s output schema: %w", d.ID, err)
		}
		s.schemas[d.ID] = wireSchema{Input: string(d.InputSchema), Output: string(d.OutputSchema)}
		s.descs = append(s.descs, d)
	}

	s.handlers = map[string]contract.Handler{
		opActivate:        bind(s, opActivate, s.activate),
		opAuthority:       bind(s, opAuthority, s.authority),
		opBootstrap:       bind(s, opBootstrap, s.bootstrap),
		opPromote:         bind(s, opPromote, s.promote),
		opRestrict:        bind(s, opRestrict, s.restrict),
		opValidate:        bind(s, opValidate, s.validate),
		opCredProvision:   bind(s, opCredProvision, s.credentialProvision),
		opCredRevoke:      bind(s, opCredRevoke, s.credentialRevoke),
		opGrantCreate:     bind(s, opGrantCreate, s.grantCreate),
		opGrantGet:        bind(s, opGrantGet, s.grantGet),
		opGrantList:       bind(s, opGrantList, s.grantList),
		opGrantRevoke:     bind(s, opGrantRevoke, s.grantRevoke),
		opGrantUpdate:     bind(s, opGrantUpdate, s.grantUpdate),
		opPrincipalCreate: bind(s, opPrincipalCreate, s.principalCreate),
		opPrincipalGet:    bind(s, opPrincipalGet, s.principalGet),
		opPrincipalList:   bind(s, opPrincipalList, s.principalList),
		opPrincipalRevoke: bind(s, opPrincipalRevoke, s.principalRevoke),
		opPrincipalUpdate: bind(s, opPrincipalUpdate, s.principalUpdate),
	}
	return nil
}

// mergeSchema splices an operation schema body into the shared $defs
// document, producing one schema document whose document-local $refs resolve
// against the exact brief definitions.
func mergeSchema(body string) (json.RawMessage, error) {
	if len(body) < 2 || body[0] != '{' || body[len(body)-1] != '}' {
		return nil, fmt.Errorf("schema body is not a JSON object")
	}
	defsLen := len(wireDefs)
	if defsLen < 2 || wireDefs[defsLen-1] != '}' {
		return nil, fmt.Errorf("embedded $defs document is malformed")
	}
	merged := wireDefs[:defsLen-1] + "," + body[1:]
	return json.RawMessage(merged), nil
}

// bind adapts a typed handler to the contract.Handler signature with strict
// dispatch: version pinning, read/write mode checking, input schema
// validation and strict decoding all run before the handler body.
func bind[I any](s *Service, op string, fn func(context.Context, contract.Unit, I) (contract.Payload, error)) contract.Handler {
	return func(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
		if inv.Version != descriptorVersion {
			return contract.Payload{}, invalidInput(
				"operation %s version %d is unsupported; this module serves v%d",
				inv.Operation, inv.Version, descriptorVersion)
		}
		if d := s.descriptor(op); d != nil && d.Mode == contract.ModeMutation && unit.ReadOnly() {
			return contract.Payload{}, invalidInput(
				"operation %s is a mutation and cannot run on a read snapshot", op)
		}
		sch := s.schemas[op]
		if err := contract.ValidateSchema(json.RawMessage(sch.Input), inv.Input); err != nil {
			return contract.Payload{}, invalidInput("operation %s input rejected: %v", op, err)
		}
		var in I
		if err := contract.DecodeStrict(inv.Input, &in); err != nil {
			return contract.Payload{}, invalidInput("operation %s input rejected: %v", op, err)
		}
		return fn(ctx, unit, in)
	}
}

// descriptor returns the descriptor for op, or nil when absent.
func (s *Service) descriptor(op string) *contract.Descriptor {
	for i := range s.descs {
		if s.descs[i].ID == op {
			return &s.descs[i]
		}
	}
	return nil
}

// completed marshals a successful result body into a completed payload.
func completed(data any) (contract.Payload, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("identity: encode result: %w", err)
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: raw}, nil
}
