package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Service assembly. Execution constructs as New(deps), registers every owned
// operation through the local bind (the frozen registry.Bind contract,
// mirrored locally because execution may only import the shared contract),
// and dispatches strictly. Ports are optional at construction: operations
// that need peers report prerequisite_missing at call time instead of failing
// construction, so narrow harnesses can construct the owner without peers.

// ownerName is the domain owner identity used in descriptors and events.
const ownerName = "execution"

// Operation identifiers and the peer operations execution calls.
const (
	opContext           = "_execution.context"
	opEnqueue           = "_execution.enqueue"
	opFence             = "_execution.fence"
	opJobClaim          = "_execution.job.claim"
	opJobCreate         = "_execution.job.create"
	opJobPending        = "_execution.job.pending"
	opJobRecord         = "_execution.job.record"
	opObservation       = "_execution.observation"
	opTick              = "_execution.tick"
	opVerificationRec   = "_execution.verification.record"
	opAttemptCancel     = "attempt.cancel"
	opAttemptCheckpoint = "attempt.checkpoint"
	opAttemptGet        = "attempt.get"
	opAttemptHeartbeat  = "attempt.heartbeat"
	opAttemptList       = "attempt.list"
	opAttemptRecovery   = "attempt.recovery"
	opAttemptReport     = "attempt.report"
	opJobGet            = "job.get"
	opRunCancel         = "run.cancel"
	opRunClaim          = "run.claim"
	opRunExport         = "run.export"
	opRunGet            = "run.get"
	opRunList           = "run.list"
	opRunRecovery       = "run.recovery"
	opWorkerPause       = "worker.pause"
	opWorkerResume      = "worker.resume"

	peerConfigSnapshot = "_configuration.snapshot"
	peerAccountReserve = "_accounting.reserve"
	peerAccountSettle  = "_accounting.settle"
	peerArtifactsMeta  = "_artifacts.metadata"
	peerEffectsPrepare = "_effects.prepare"
	peerTasksReady     = "_tasks.ready"
	peerTasksSnapshot  = "_tasks.snapshot"
	peerTasksTransit   = "_tasks.transition"
)

// opMeta is the static registration record for one operation.
type opMeta struct {
	id         string
	visibility string
	mode       string
	submission bool
	expected   bool // descriptor advertises optimistic version fencing
	callers    []string
	cli        string // "zatiti ..." CLI path; empty for internal operations
}

// opMetas lists every owned operation: internal first, then the public
// operations. Caller allowlists and transport bindings follow the
// implementation assignment exactly.
var opMetas = []opMeta{
	{id: opContext, visibility: "internal", mode: "mutation",
		callers: []string{"controller"}},
	{id: opEnqueue, visibility: "internal", mode: "mutation",
		callers: []string{"tasks", "scheduling", "application"}},
	{id: opFence, visibility: "internal", mode: "mutation",
		callers: []string{"controller", "installation"}},
	{id: opJobClaim, visibility: "internal", mode: "mutation",
		callers: []string{"controller", "application"}},
	{id: opJobCreate, visibility: "internal", mode: "mutation",
		callers: []string{"configuration", "skills", "connections", "memory",
			"artifacts", "installation", "execution", "effects", "application"}},
	{id: opJobPending, visibility: "internal", mode: "query",
		callers: []string{"controller"}},
	{id: opJobRecord, visibility: "internal", mode: "mutation",
		callers: []string{"controller", "application", "effects", "memory",
			"skills", "connections", "installation"}},
	{id: opObservation, visibility: "internal", mode: "mutation",
		callers: []string{"controller"}},
	{id: opTick, visibility: "internal", mode: "mutation",
		callers: []string{"controller"}},
	{id: opVerificationRec, visibility: "internal", mode: "mutation",
		callers: []string{"controller"}},

	{id: opAttemptCancel, visibility: "public", mode: "mutation", submission: true,
		expected: true, cli: "zatiti attempt cancel"},
	{id: opAttemptCheckpoint, visibility: "public", mode: "mutation", submission: true,
		expected: true, cli: "zatiti attempt checkpoint"},
	{id: opAttemptGet, visibility: "public", mode: "query", cli: "zatiti attempt get"},
	{id: opAttemptHeartbeat, visibility: "public", mode: "mutation", submission: true,
		expected: true, cli: "zatiti attempt heartbeat"},
	{id: opAttemptList, visibility: "public", mode: "query", cli: "zatiti attempt list"},
	{id: opAttemptRecovery, visibility: "public", mode: "query", cli: "zatiti attempt recovery"},
	{id: opAttemptReport, visibility: "public", mode: "mutation", submission: true,
		expected: true, cli: "zatiti attempt report"},
	{id: opJobGet, visibility: "public", mode: "query", cli: "zatiti job get"},
	{id: opRunCancel, visibility: "public", mode: "mutation", submission: true,
		expected: true, cli: "zatiti run cancel"},
	{id: opRunClaim, visibility: "public", mode: "mutation", submission: true,
		expected: true, cli: "zatiti run claim"},
	{id: opRunExport, visibility: "public", mode: "mutation", submission: true,
		cli: "zatiti run export"},
	{id: opRunGet, visibility: "public", mode: "query", cli: "zatiti run get"},
	{id: opRunList, visibility: "public", mode: "query", cli: "zatiti run list"},
	{id: opRunRecovery, visibility: "public", mode: "query", cli: "zatiti run recovery"},
	{id: opWorkerPause, visibility: "public", mode: "mutation", submission: true,
		expected: true, cli: "zatiti worker pause"},
	{id: opWorkerResume, visibility: "public", mode: "mutation", submission: true,
		expected: true, cli: "zatiti worker resume"},
}

// Service is the execution domain owner: runs pinning ready task/version
// configuration, leased attempts, durable jobs, independent verification and
// recovery dispositions.
type Service struct {
	deps        contract.Dependencies
	catalog     map[string]contract.Descriptor
	descriptors []contract.Descriptor
	handlers    map[string]contract.Handler

	cursorMu  sync.Mutex
	cursorKey []byte
}

// Compile-time proof that *Service implements the shared Module contract.
var _ contract.Module = (*Service)(nil)

// New constructs the execution owner. It never queries peers, touches
// storage or starts goroutines; all runtime coupling arrives through deps.
// Clock and identity source are required; ports are checked at call time.
func New(deps contract.Dependencies) (*Service, error) {
	if deps.Clock == nil {
		return nil, &contract.Fault{Code: contract.CodeInternalError, Message: "execution requires a clock"}
	}
	if deps.IDs == nil {
		return nil, &contract.Fault{Code: contract.CodeInternalError, Message: "execution requires an identity source"}
	}
	s := &Service{
		deps:     deps,
		catalog:  make(map[string]contract.Descriptor, len(opMetas)),
		handlers: make(map[string]contract.Handler, len(opMetas)),
	}
	if err := s.assemble(); err != nil {
		return nil, err
	}
	return s, nil
}

// now returns the deterministic current time from the dependency clock.
func (s *Service) now() time.Time { return s.deps.Clock.Now() }

// newID mints one identity from the dependency source.
func (s *Service) newID() contract.ID { return s.deps.IDs.New() }

// Name implements contract.Module.
func (s *Service) Name() string { return ownerName }

// Migrations implements contract.Module.
func (s *Service) Migrations() []contract.Migration { return migrations() }

// Descriptors implements contract.Module: every owned operation with its
// merged schemas, transport bindings and caller allowlists.
func (s *Service) Descriptors() []contract.Descriptor { return s.descriptors }

// assemble builds the descriptor list, binds one typed handler per
// operation and fills the lookup map.
func (s *Service) assemble() error {
	s.descriptors = make([]contract.Descriptor, 0, len(opMetas))
	for _, m := range opMetas {
		schemas, ok := opSchemas[m.id]
		if !ok {
			return fmt.Errorf("execution: operation %s has no wire schemas", m.id)
		}
		d := contract.Descriptor{
			ID:              m.id,
			Version:         1,
			Owner:           ownerName,
			Visibility:      m.visibility,
			Mode:            m.mode,
			Effect:          contract.EffectLocal,
			InputSchema:     json.RawMessage(schemas[0]),
			OutputSchema:    json.RawMessage(schemas[1]),
			ScopeRequired:   []string{"installation_id"},
			Callers:         m.callers,
			ExpectedVersion: m.expected,
			SubmissionKey:   m.submission,
		}
		if m.cli != "" {
			d.CLI = strings.Split(m.cli, " ")
		}
		if m.visibility == "public" {
			d.MCP = "zatiti_" + strings.ReplaceAll(m.id, ".", "_")
		}
		h, err := s.bindHandler(d)
		if err != nil {
			return fmt.Errorf("execution: bind %s: %w", m.id, err)
		}
		s.catalog[m.id] = d
		s.handlers[m.id] = h
		s.descriptors = append(s.descriptors, d)
	}
	return nil
}

// bindHandler attaches the typed handler of one operation to its descriptor
// through the local bind.
func (s *Service) bindHandler(d contract.Descriptor) (contract.Handler, error) {
	switch d.ID {
	case opContext:
		return bind(d, s.handleContext)
	case opEnqueue:
		return bind(d, s.handleEnqueue)
	case opFence:
		return bind(d, s.handleFence)
	case opJobClaim:
		return bind(d, s.handleJobClaim)
	case opJobCreate:
		return bind(d, s.handleJobCreate)
	case opJobPending:
		return bind(d, s.handleJobPending)
	case opJobRecord:
		return bind(d, s.handleJobRecord)
	case opObservation:
		return bind(d, s.handleObservation)
	case opTick:
		return bind(d, s.handleTick)
	case opVerificationRec:
		return bind(d, s.handleVerificationRecord)
	case opAttemptCancel:
		return bind(d, s.handleAttemptCancel)
	case opAttemptCheckpoint:
		return bind(d, s.handleAttemptCheckpoint)
	case opAttemptGet:
		return bind(d, s.handleAttemptGet)
	case opAttemptHeartbeat:
		return bind(d, s.handleAttemptHeartbeat)
	case opAttemptList:
		return bind(d, s.handleAttemptList)
	case opAttemptRecovery:
		return bind(d, s.handleAttemptRecovery)
	case opAttemptReport:
		return bind(d, s.handleAttemptReport)
	case opJobGet:
		return bind(d, s.handleJobGet)
	case opRunCancel:
		return bind(d, s.handleRunCancel)
	case opRunClaim:
		return bind(d, s.handleRunClaim)
	case opRunExport:
		return bind(d, s.handleRunExport)
	case opRunGet:
		return bind(d, s.handleRunGet)
	case opRunList:
		return bind(d, s.handleRunList)
	case opRunRecovery:
		return bind(d, s.handleRunRecovery)
	case opWorkerPause:
		return bind(d, s.handleWorkerPause)
	case opWorkerResume:
		return bind(d, s.handleWorkerResume)
	default:
		return nil, fmt.Errorf("no handler for operation %q", d.ID)
	}
}

// Handle implements contract.Module with strict dispatch: known operation
// only, declared version only, mutations only in write transactions.
func (s *Service) Handle(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	d, ok := s.catalog[inv.Operation]
	if !ok {
		return contract.Payload{}, notFound("unknown operation %q", inv.Operation)
	}
	if inv.Version != d.Version {
		return contract.Payload{}, invalidInput("operation %s version %d does not match declared version %d",
			inv.Operation, inv.Version, d.Version)
	}
	if d.Mode == contract.ModeMutation && unit.ReadOnly() {
		return contract.Payload{}, invalidInput("operation %s is a mutation and requires a write transaction", inv.Operation)
	}
	h, ok := s.handlers[inv.Operation]
	if !ok {
		return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError,
			Message: fmt.Sprintf("operation %s has no handler", inv.Operation)}
	}
	return h(ctx, unit, inv)
}

// bind is the local mirror of the frozen registry.Bind contract: schema
// validation precedes execution, decoding is strict, the typed outcome is
// canonicalized and validated against the merged output schema, handler
// faults pass through and anything else becomes internal_error. Descriptor
// shape checks mirror the registry's; every public mutation requires a
// submission key and internal mutations do not.
func bind[I, O any](d contract.Descriptor, fn func(context.Context, contract.Unit, I) (contract.Outcome[O], error)) (contract.Handler, error) {
	if fn == nil {
		return nil, fmt.Errorf("operation %q requires a handler function", d.ID)
	}
	if err := validateDescriptorShape(&d); err != nil {
		return nil, err
	}
	mergedInput, err := mergeSchema(string(d.InputSchema))
	if err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	if err := checkSchemaDocument(mergedInput); err != nil {
		return nil, fmt.Errorf("input schema: %w", err)
	}
	mergedOutput, err := mergeSchema(string(d.OutputSchema))
	if err != nil {
		return nil, fmt.Errorf("output schema: %w", err)
	}
	if err := checkSchemaDocument(mergedOutput); err != nil {
		return nil, fmt.Errorf("output schema: %w", err)
	}
	query := d.Mode == contract.ModeQuery
	return func(ctx context.Context, unit contract.Unit, invocation contract.Invocation) (contract.Payload, error) {
		if invocation.Operation != d.ID {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError,
				Message: fmt.Sprintf("handler for operation %s received invocation for %q", d.ID, invocation.Operation)}
		}
		if invocation.Version != d.Version {
			return contract.Payload{}, notFound("operation %s version %d is not registered", d.ID, invocation.Version)
		}
		if err := contract.ValidateSchema(mergedInput, invocation.Input); err != nil {
			return contract.Payload{}, invalidInput("operation %s input rejected: %v", d.ID, err)
		}
		var input I
		if err := contract.DecodeStrict(invocation.Input, &input); err != nil {
			return contract.Payload{}, invalidInput("operation %s input rejected: %v", d.ID, err)
		}
		outcome, err := fn(ctx, unit, input)
		if err != nil {
			var f *contract.Fault
			if errors.As(err, &f) {
				return contract.Payload{}, f
			}
			wrapped := &contract.Fault{Code: contract.CodeInternalError,
				Message: fmt.Sprintf("operation %s failed", d.ID)}
			return contract.Payload{}, errors.Join(wrapped, err)
		}
		status := outcome.Status
		if status == "" {
			status = contract.StatusCompleted
		}
		if status != contract.StatusCompleted && status != contract.StatusAccepted {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError,
				Message: fmt.Sprintf("operation %s returned invalid status %q", d.ID, status)}
		}
		if query && status != contract.StatusCompleted {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError,
				Message: fmt.Sprintf("operation %s is a query and cannot return an accepted outcome", d.ID)}
		}
		data, err := json.Marshal(outcome.Data)
		if err != nil {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError,
				Message: fmt.Sprintf("operation %s output could not be marshaled: %v", d.ID, err)}
		}
		if data, err = contract.Canonicalize(data); err != nil {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError,
				Message: fmt.Sprintf("operation %s output is not canonical JSON: %v", d.ID, err)}
		}
		if err := contract.ValidateSchema(mergedOutput, data); err != nil {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError,
				Message: fmt.Sprintf("operation %s output does not match its declared schema: %v", d.ID, err)}
		}
		return contract.Payload{
			Status:     status,
			Data:       data,
			NextCursor: outcome.NextCursor,
		}, nil
	}, nil
}

// validateDescriptorShape mirrors the registry's bind-time descriptor
// checks. Internal mutations are exempt from the submission-key rule; public
// mutations require it.
func validateDescriptorShape(d *contract.Descriptor) error {
	if d.ID == "" || strings.Contains(d.ID, " ") {
		return fmt.Errorf("operation ID %q is not a dot-separated lowercase token sequence", d.ID)
	}
	if d.Version < 1 {
		return fmt.Errorf("operation version %d must be >= 1", d.Version)
	}
	if d.Owner == "" {
		return fmt.Errorf("owner %q is not a valid owner name", d.Owner)
	}
	switch d.Visibility {
	case contract.VisibilityPublic, contract.VisibilityInternal:
	default:
		return fmt.Errorf("visibility %q must be public or internal", d.Visibility)
	}
	switch d.Mode {
	case contract.ModeQuery, contract.ModeMutation:
	default:
		return fmt.Errorf("mode %q must be query or mutation", d.Mode)
	}
	switch d.Effect {
	case contract.EffectLocal, contract.EffectDisclosure,
		contract.EffectExternalRead, contract.EffectExternalMutation:
	default:
		return fmt.Errorf("effect %q must be local, disclosure, external_read or external_mutation", d.Effect)
	}
	if len(d.InputSchema) == 0 {
		return fmt.Errorf("operation %s: input schema is required", d.ID)
	}
	if len(d.OutputSchema) == 0 {
		return fmt.Errorf("operation %s: output schema is required", d.ID)
	}
	if d.Mode == contract.ModeQuery && d.SubmissionKey {
		return fmt.Errorf("query operation must not require a submission key")
	}
	if d.Mode == contract.ModeMutation && d.SubmissionKey && d.Visibility != contract.VisibilityPublic {
		return fmt.Errorf("only public mutations carry a submission key")
	}
	for _, caller := range d.Callers {
		if caller == "" {
			return fmt.Errorf("operation %s: caller names must not be empty", d.ID)
		}
	}
	return nil
}

// checkSchemaDocument verifies that a merged schema document parses and that
// every document-local $ref resolves against its $defs. Registration fails
// closed on a broken schema instead of failing on the wire.
func checkSchemaDocument(doc json.RawMessage) error {
	var parsed map[string]json.RawMessage
	if err := contract.DecodeStrict(doc, &parsed); err != nil {
		return fmt.Errorf("schema document does not parse: %w", err)
	}
	var defs map[string]json.RawMessage
	rawDefs, ok := parsed["$defs"]
	if !ok {
		return fmt.Errorf("schema document has no $defs")
	}
	if err := contract.DecodeStrict(rawDefs, &defs); err != nil {
		return fmt.Errorf("schema $defs do not parse: %w", err)
	}
	return resolveRefs(parsed, defs)
}

// resolveRefs walks one decoded schema node checking every local $ref.
func resolveRefs(node any, defs map[string]json.RawMessage) error {
	switch v := node.(type) {
	case map[string]any:
		if ref, ok := v["$ref"].(string); ok {
			if !strings.HasPrefix(ref, "#/$defs/") {
				return fmt.Errorf("non-local $ref %q is not supported", ref)
			}
			name := strings.TrimPrefix(ref, "#/$defs/")
			if _, ok := defs[name]; !ok {
				return fmt.Errorf("$ref %q does not resolve", ref)
			}
			return nil
		}
		for _, child := range v {
			if err := resolveRefs(child, defs); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := resolveRefs(child, defs); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkInstallation enforces that an operation addresses the caller's own
// installation. The installation id is required; anything else is a
// caller-scope violation.
func checkInstallation(unit contract.Unit, id contract.ID) error {
	if id == "" {
		return invalidInput("scope must name the installation")
	}
	if unit.Scope().InstallationID != id {
		return permissionDenied("scope installation %s does not match the caller's installation %s",
			id, unit.Scope().InstallationID)
	}
	return nil
}

// installationOf returns the caller's transaction installation.
func installationOf(unit contract.Unit) contract.ID {
	return unit.Scope().InstallationID
}

// completedOutcome wraps a typed data body as a completed outcome.
func completedOutcome[O any](data O) (contract.Outcome[O], error) {
	return contract.Outcome[O]{Status: contract.StatusCompleted, Data: data}, nil
}

// canonicalJSON marshals a value and canonicalizes the bytes, matching the
// shared storage and replay conventions.
func canonicalJSON(v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("execution: encode: %w", err)
	}
	canonical, err := contract.Canonicalize(raw)
	if err != nil {
		return nil, fmt.Errorf("execution: canonicalize: %w", err)
	}
	return canonical, nil
}
