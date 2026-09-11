package policy

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
	opActivate   = "_policy.activate"
	opCheck      = "_policy.check"
	opInvalidate = "_policy.invalidate"
	opValidate   = "_policy.validate"

	opAutonomyDemote   = "autonomy.demote"
	opAutonomyEvaluate = "autonomy.evaluate"
	opAutonomyPropose  = "autonomy.propose"
	opAutonomyQualGet  = "autonomy.qualification.get"
	opAutonomyQualList = "autonomy.qualification.list"
	opAutonomyRestrict = "autonomy.restrict"
	opRuleArchive      = "autonomy.rule.archive"
	opRuleCreate       = "autonomy.rule.create"
	opRuleGet          = "autonomy.rule.get"
	opRuleList         = "autonomy.rule.list"
	opRuleUpdate       = "autonomy.rule.update"

	opPolicyArchive = "policy.archive"
	opPolicyCreate  = "policy.create"
	opPolicyExplain = "policy.explain"
	opPolicyGet     = "policy.get"
	opPolicyList    = "policy.list"
	opPolicyUpdate  = "policy.update"
)

// descriptorVersion is the single contract version every descriptor serves.
const descriptorVersion int64 = 1

// Service implements contract.Module for the policy domain. Construct it
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

// New validates the injected dependencies and assembles the module's static
// contract. The ports dependency is required: every authority decision and
// autonomy transition reads current peer state, and a policy module without
// ports could only invent answers.
func New(deps contract.Dependencies) (*Service, error) {
	if deps.Clock == nil {
		return nil, errors.New("policy: a clock dependency is required")
	}
	if deps.IDs == nil {
		return nil, errors.New("policy: an identity source is required")
	}
	if deps.Ports == nil {
		return nil, errors.New("policy: a ports dependency is required")
	}
	s := &Service{
		deps:     deps,
		migs:     migrations(),
		handlers: make(map[string]contract.Handler, 21),
		schemas:  make(map[string]wireSchema, 21),
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
			ID: opCheck, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityInternal, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			ScopeRequired: []string{"installation_id"},
			Callers: []string{
				"application", "configuration", "tasks", "execution", "effects",
				"memory", "messaging", "connections", "installation", "reviews", "accounting",
			},
		},
		{
			ID: opInvalidate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityInternal, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			ScopeRequired: []string{"installation_id"},
			Callers:       []string{"configuration", "skills", "connections", "execution", "tasks"},
		},
		{
			ID: opValidate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityInternal, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			ScopeRequired: []string{"installation_id"},
			Callers:       []string{"configuration", "application"},
		},
		{
			ID: opAutonomyDemote, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"autonomy", "demote"},
			MCP:             "zatiti_autonomy_demote",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opAutonomyEvaluate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"autonomy", "evaluate"},
			MCP:             "zatiti_autonomy_evaluate",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opAutonomyPropose, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:           []string{"autonomy", "propose"},
			MCP:           "zatiti_autonomy_propose",
			ScopeRequired: []string{"installation_id"},
			SubmissionKey: true,
		},
		{
			ID: opAutonomyQualGet, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI:           []string{"autonomy", "qualification", "get"},
			MCP:           "zatiti_autonomy_qualification_get",
			ScopeRequired: []string{"installation_id"},
		},
		{
			ID: opAutonomyQualList, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI:           []string{"autonomy", "qualification", "list"},
			MCP:           "zatiti_autonomy_qualification_list",
			ScopeRequired: []string{"installation_id"},
		},
		{
			ID: opAutonomyRestrict, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"autonomy", "restrict"},
			MCP:             "zatiti_autonomy_restrict",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opRuleArchive, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"autonomy", "rule", "archive"},
			MCP:             "zatiti_autonomy_rule_archive",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opRuleCreate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:           []string{"autonomy", "rule", "create"},
			MCP:           "zatiti_autonomy_rule_create",
			ScopeRequired: []string{"installation_id"},
			SubmissionKey: true,
		},
		{
			ID: opRuleGet, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI:           []string{"autonomy", "rule", "get"},
			MCP:           "zatiti_autonomy_rule_get",
			ScopeRequired: []string{"installation_id"},
		},
		{
			ID: opRuleList, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI:           []string{"autonomy", "rule", "list"},
			MCP:           "zatiti_autonomy_rule_list",
			ScopeRequired: []string{"installation_id"},
		},
		{
			ID: opRuleUpdate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"autonomy", "rule", "update"},
			MCP:             "zatiti_autonomy_rule_update",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opPolicyArchive, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"policy", "archive"},
			MCP:             "zatiti_policy_archive",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opPolicyCreate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:           []string{"policy", "create"},
			MCP:           "zatiti_policy_create",
			ScopeRequired: []string{"installation_id"},
			SubmissionKey: true,
		},
		{
			ID: opPolicyExplain, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI:           []string{"policy", "explain"},
			MCP:           "zatiti_policy_explain",
			ScopeRequired: []string{"installation_id"},
		},
		{
			ID: opPolicyGet, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI:           []string{"policy", "get"},
			MCP:           "zatiti_policy_get",
			ScopeRequired: []string{"installation_id"},
		},
		{
			ID: opPolicyList, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI:           []string{"policy", "list"},
			MCP:           "zatiti_policy_list",
			ScopeRequired: []string{"installation_id"},
		},
		{
			ID: opPolicyUpdate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"policy", "update"},
			MCP:             "zatiti_policy_update",
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
			return fmt.Errorf("policy: operation %s has no embedded schema", d.ID)
		}
		var err error
		d.InputSchema, err = mergeSchema(sch.Input)
		if err != nil {
			return fmt.Errorf("policy: operation %s input schema: %w", d.ID, err)
		}
		d.OutputSchema, err = mergeSchema(sch.Output)
		if err != nil {
			return fmt.Errorf("policy: operation %s output schema: %w", d.ID, err)
		}
		s.schemas[d.ID] = wireSchema{Input: string(d.InputSchema), Output: string(d.OutputSchema)}
		s.descs = append(s.descs, d)
	}

	s.handlers = map[string]contract.Handler{
		opActivate:         bind(s, opActivate, s.activate),
		opCheck:            bind(s, opCheck, s.check),
		opInvalidate:       bind(s, opInvalidate, s.invalidate),
		opValidate:         bind(s, opValidate, s.validate),
		opAutonomyDemote:   bind(s, opAutonomyDemote, s.autonomyDemote),
		opAutonomyEvaluate: bind(s, opAutonomyEvaluate, s.autonomyEvaluate),
		opAutonomyPropose:  bind(s, opAutonomyPropose, s.autonomyPropose),
		opAutonomyQualGet:  bind(s, opAutonomyQualGet, s.qualificationGet),
		opAutonomyQualList: bind(s, opAutonomyQualList, s.qualificationList),
		opAutonomyRestrict: bind(s, opAutonomyRestrict, s.autonomyRestrict),
		opRuleArchive:      bind(s, opRuleArchive, s.ruleArchive),
		opRuleCreate:       bind(s, opRuleCreate, s.ruleCreate),
		opRuleGet:          bind(s, opRuleGet, s.ruleGet),
		opRuleList:         bind(s, opRuleList, s.ruleList),
		opRuleUpdate:       bind(s, opRuleUpdate, s.ruleUpdate),
		opPolicyArchive:    bind(s, opPolicyArchive, s.policyArchive),
		opPolicyCreate:     bind(s, opPolicyCreate, s.policyCreate),
		opPolicyExplain:    bind(s, opPolicyExplain, s.explain),
		opPolicyGet:        bind(s, opPolicyGet, s.policyGet),
		opPolicyList:       bind(s, opPolicyList, s.policyList),
		opPolicyUpdate:     bind(s, opPolicyUpdate, s.policyUpdate),
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
		return contract.Payload{}, fmt.Errorf("policy: encode result: %w", err)
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: raw}, nil
}
