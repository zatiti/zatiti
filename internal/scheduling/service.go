package scheduling

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
	opActivate    = "_scheduling.activate"
	opValidate    = "_scheduling.validate"
	opCycleRecord = "_scheduling.cycle.record"
	opWakeAdmit   = "_scheduling.wake.admit"
	opWakeDue     = "_scheduling.wake.due"

	opResponsibilityArchive = "responsibility.archive"
	opResponsibilityCreate  = "responsibility.create"
	opResponsibilityGet     = "responsibility.get"
	opResponsibilityList    = "responsibility.list"
	opResponsibilityPause   = "responsibility.pause"
	opResponsibilityResume  = "responsibility.resume"
	opResponsibilityUpdate  = "responsibility.update"

	opScheduleArchive = "schedule.archive"
	opScheduleCreate  = "schedule.create"
	opScheduleGet     = "schedule.get"
	opScheduleList    = "schedule.list"
	opSchedulePause   = "schedule.pause"
	opScheduleResume  = "schedule.resume"
	opScheduleUpdate  = "schedule.update"
)

// descriptorVersion is the single contract version every descriptor serves.
const descriptorVersion int64 = 1

// Service implements contract.Module for the scheduling domain. Construct it
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
// contract. The ports dependency is required: staging drafts, resolving
// worker dependencies, creating cycle tasks and enqueueing execution all
// read or call peers, and a scheduling module without ports could only
// invent answers.
func New(deps contract.Dependencies) (*Service, error) {
	if deps.Clock == nil {
		return nil, errors.New("scheduling: a clock dependency is required")
	}
	if deps.IDs == nil {
		return nil, errors.New("scheduling: an identity source is required")
	}
	if deps.Ports == nil {
		return nil, errors.New("scheduling: a ports dependency is required")
	}
	s := &Service{
		deps:     deps,
		migs:     migrations(),
		handlers: make(map[string]contract.Handler, 19),
		schemas:  make(map[string]wireSchema, 19),
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
			ID: opValidate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityInternal, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			ScopeRequired: []string{"installation_id"},
			Callers:       []string{"configuration", "application"},
		},
		{
			ID: opCycleRecord, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityInternal, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			ScopeRequired: []string{"installation_id"},
			Callers:       []string{"execution"},
		},
		{
			ID: opWakeAdmit, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityInternal, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			ScopeRequired: []string{"installation_id"},
			Callers:       []string{"controller"},
		},
		{
			ID: opWakeDue, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityInternal, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			ScopeRequired: []string{"installation_id"},
			Callers:       []string{"controller"},
		},
		{
			ID: opResponsibilityCreate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:           []string{"responsibility", "create"},
			MCP:           "zatiti_responsibility_create",
			ScopeRequired: []string{"installation_id"},
			SubmissionKey: true,
		},
		{
			ID: opResponsibilityGet, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI:           []string{"responsibility", "get"},
			MCP:           "zatiti_responsibility_get",
			ScopeRequired: []string{"installation_id"},
		},
		{
			ID: opResponsibilityList, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI:           []string{"responsibility", "list"},
			MCP:           "zatiti_responsibility_list",
			ScopeRequired: []string{"installation_id"},
		},
		{
			ID: opResponsibilityUpdate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"responsibility", "update"},
			MCP:             "zatiti_responsibility_update",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opResponsibilityArchive, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"responsibility", "archive"},
			MCP:             "zatiti_responsibility_archive",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opResponsibilityPause, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"responsibility", "pause"},
			MCP:             "zatiti_responsibility_pause",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opResponsibilityResume, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"responsibility", "resume"},
			MCP:             "zatiti_responsibility_resume",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opScheduleCreate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:           []string{"schedule", "create"},
			MCP:           "zatiti_schedule_create",
			ScopeRequired: []string{"installation_id"},
			SubmissionKey: true,
		},
		{
			ID: opScheduleGet, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI:           []string{"schedule", "get"},
			MCP:           "zatiti_schedule_get",
			ScopeRequired: []string{"installation_id"},
		},
		{
			ID: opScheduleList, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeQuery, Effect: contract.EffectLocal,
			CLI:           []string{"schedule", "list"},
			MCP:           "zatiti_schedule_list",
			ScopeRequired: []string{"installation_id"},
		},
		{
			ID: opScheduleUpdate, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"schedule", "update"},
			MCP:             "zatiti_schedule_update",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opScheduleArchive, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"schedule", "archive"},
			MCP:             "zatiti_schedule_archive",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opSchedulePause, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"schedule", "pause"},
			MCP:             "zatiti_schedule_pause",
			ScopeRequired:   []string{"installation_id"},
			SubmissionKey:   true,
			ExpectedVersion: true,
		},
		{
			ID: opScheduleResume, Version: descriptorVersion, Owner: owner,
			Visibility: contract.VisibilityPublic, Mode: contract.ModeMutation, Effect: contract.EffectLocal,
			CLI:             []string{"schedule", "resume"},
			MCP:             "zatiti_schedule_resume",
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
			return fmt.Errorf("scheduling: operation %s has no embedded schema", d.ID)
		}
		var err error
		d.InputSchema, err = mergeSchema(sch.Input)
		if err != nil {
			return fmt.Errorf("scheduling: operation %s input schema: %w", d.ID, err)
		}
		d.OutputSchema, err = mergeSchema(sch.Output)
		if err != nil {
			return fmt.Errorf("scheduling: operation %s output schema: %w", d.ID, err)
		}
		s.schemas[d.ID] = wireSchema{Input: string(d.InputSchema), Output: string(d.OutputSchema)}
		s.descs = append(s.descs, d)
	}

	s.handlers = map[string]contract.Handler{
		opActivate:              bind(s, opActivate, s.activate),
		opValidate:              bind(s, opValidate, s.validate),
		opCycleRecord:           bind(s, opCycleRecord, s.cycleRecord),
		opWakeAdmit:             bind(s, opWakeAdmit, s.wakeAdmit),
		opWakeDue:               bind(s, opWakeDue, s.wakeDue),
		opResponsibilityArchive: bind(s, opResponsibilityArchive, s.responsibilityArchive),
		opResponsibilityCreate:  bind(s, opResponsibilityCreate, s.responsibilityCreate),
		opResponsibilityGet:     bind(s, opResponsibilityGet, s.responsibilityGet),
		opResponsibilityList:    bind(s, opResponsibilityList, s.responsibilityList),
		opResponsibilityPause:   bind(s, opResponsibilityPause, s.responsibilityPause),
		opResponsibilityResume:  bind(s, opResponsibilityResume, s.responsibilityResume),
		opResponsibilityUpdate:  bind(s, opResponsibilityUpdate, s.responsibilityUpdate),
		opScheduleArchive:       bind(s, opScheduleArchive, s.scheduleArchive),
		opScheduleCreate:        bind(s, opScheduleCreate, s.scheduleCreate),
		opScheduleGet:           bind(s, opScheduleGet, s.scheduleGet),
		opScheduleList:          bind(s, opScheduleList, s.scheduleList),
		opSchedulePause:         bind(s, opSchedulePause, s.schedulePause),
		opScheduleResume:        bind(s, opScheduleResume, s.scheduleResume),
		opScheduleUpdate:        bind(s, opScheduleUpdate, s.scheduleUpdate),
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
		return contract.Payload{}, fmt.Errorf("scheduling: encode result: %w", err)
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: raw}, nil
}
