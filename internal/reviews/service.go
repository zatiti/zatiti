package reviews

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
	opCheck    = "_reviews.check"
	opEnsure   = "_reviews.ensure"
	opDecide   = "review.decide"
	opDelegate = "review.delegate"
	opGet      = "review.get"
	opList     = "review.list"
)

// descriptorVersion is the single contract version every descriptor serves.
const descriptorVersion int64 = 1

// Service implements contract.Module for the reviews domain. Construct it
// with New; all state is ready before the first call and no goroutines or
// peer queries run during construction.
type Service struct {
	deps     contract.Dependencies
	migs     []contract.Migration
	descs    []contract.Descriptor
	handlers map[string]contract.Handler
	schemas  map[string]wireSchema

	cursorMu  sync.Mutex
	cursorKey []byte
}

// Compile-time proof that *Service implements the shared Module contract.
var _ contract.Module = (*Service)(nil)

// New validates the injected dependencies and assembles the module's static
// contract. Owner ports are required: eligibility rechecks consult
// _identity.authority through them.
func New(deps contract.Dependencies) (*Service, error) {
	if deps.Clock == nil {
		return nil, errors.New("reviews: a clock dependency is required")
	}
	if deps.IDs == nil {
		return nil, errors.New("reviews: an identity source is required")
	}
	if deps.Ports == nil {
		return nil, errors.New("reviews: owner ports are required")
	}
	s := &Service{
		deps:     deps,
		migs:     migrations(),
		handlers: make(map[string]contract.Handler, 6),
		schemas:  make(map[string]wireSchema, 6),
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

// assemble builds the descriptor table and binds every handler. Public
// descriptors mirror the frozen catalog exactly; the catalog entry is the
// source of truth for metadata.
func (s *Service) assemble() error {
	specs := []contract.Descriptor{
		{
			ID: opCheck, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityInternal, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			ScopeRequired: []string{"installation_id"},
			Callers:       []string{"effects", "configuration", "policy", "tasks"},
		},
		{
			ID: opEnsure, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityInternal, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			ScopeRequired: []string{"installation_id"},
			Callers:       []string{"effects", "configuration", "policy"},
		},
		{
			ID: opDecide, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"review", "decide"},
			MCP:             "zatiti_review_decide",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opDelegate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"review", "delegate"},
			MCP:             "zatiti_review_delegate",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opGet, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI:           []string{"review", "get"},
			MCP:           "zatiti_review_get",
			ScopeRequired: []string{"installation_id"},
		},
		{
			ID: opList, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI:           []string{"review", "list"},
			MCP:           "zatiti_review_list",
			ScopeRequired: []string{"installation_id"},
		},
	}

	// Merge the shared $defs document into every per-operation schema so
	// document-local $refs resolve against the exact brief definitions.
	for _, d := range specs {
		sch, ok := wireSchemas[d.ID]
		if !ok {
			return fmt.Errorf("reviews: operation %s has no embedded schema", d.ID)
		}
		var err error
		d.InputSchema, err = mergeSchema(sch.Input)
		if err != nil {
			return fmt.Errorf("reviews: operation %s input schema: %w", d.ID, err)
		}
		d.OutputSchema, err = mergeSchema(sch.Output)
		if err != nil {
			return fmt.Errorf("reviews: operation %s output schema: %w", d.ID, err)
		}
		s.schemas[d.ID] = wireSchema{Input: string(d.InputSchema), Output: string(d.OutputSchema)}
		s.descs = append(s.descs, d)
	}

	s.handlers = map[string]contract.Handler{
		opCheck:    bind(s, opCheck, s.check),
		opEnsure:   bind(s, opEnsure, s.ensure),
		opDecide:   bind(s, opDecide, s.decide),
		opDelegate: bind(s, opDelegate, s.delegate),
		opGet:      bind(s, opGet, s.get),
		opList:     bind(s, opList, s.list),
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
		return contract.Payload{}, fmt.Errorf("reviews: encode result: %w", err)
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: raw}, nil
}
