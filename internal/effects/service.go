package effects

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

// Service assembly. Effects constructs as New(deps), registers every owned
// operation through the local bind (the frozen registry.Bind contract,
// mirrored locally because effects may only import the shared contract), and
// dispatches strictly. Clock, identity source and ports are required:
// admission rechecks policy, reviews, budgets and connections through peers,
// and an effects module without them could only invent answers.

// ownerName is the domain owner identity used in descriptors and events.
const ownerName = "effects"

// Operation identifiers served by this module and the peer operations it
// calls.
const (
	opAdmit   = "_effects.admit"
	opClaim   = "_effects.claim"
	opPending = "_effects.pending"
	opPrepare = "_effects.prepare"
	opRecord  = "_effects.record"

	opCompensationPropose = "operation.compensation.propose"
	opGet                 = "operation.get"
	opList                = "operation.list"
	opPropose             = "operation.propose"
	opReconcile           = "operation.reconcile"
	opReplacementPropose  = "operation.replacement.propose"

	opIdentityAuthority           = "_identity.authority"
	opConfigSnapshot              = "_configuration.snapshot"
	opPolicyCheck                 = "_policy.check"
	opReviewsEnsure               = "_reviews.ensure"
	opReviewsCheck                = "_reviews.check"
	opAccountingInspect           = "_accounting.inspect"
	opAccountingReserve           = "_accounting.reserve"
	opAccountingSettle            = "_accounting.settle"
	opConnectionsResolve          = "_connections.resolve"
	opConnectionsValidationRecord = "_connections.validation.record"
	opTasksSnapshot               = "_tasks.snapshot"
	opArtifactsMetadata           = "_artifacts.metadata"
	opExecutionJobCreate          = "_execution.job.create"
)

// opMeta is the static registration record for one operation.
type opMeta struct {
	id         string
	visibility string
	mode       string
	effect     string
	submission bool
	expected   bool
	callers    []string
	cli        string // "zatiti ..." CLI path; empty for internal operations
}

// opMetas lists every owned operation: internal first, then the public
// operations. Caller allowlists and transport bindings follow the
// implementation assignment exactly.
var opMetas = []opMeta{
	{id: opAdmit, visibility: "internal", mode: "mutation",
		callers: []string{"controller", "execution", "memory", "skills", "connections"}},
	{id: opClaim, visibility: "internal", mode: "mutation",
		callers: []string{"controller"}},
	{id: opPending, visibility: "internal", mode: "query",
		callers: []string{"controller", "installation"}},
	{id: opPrepare, visibility: "internal", mode: "mutation",
		callers: []string{"execution", "memory", "connections", "skills", "installation"}},
	{id: opRecord, visibility: "internal", mode: "mutation",
		callers: []string{"controller"}},
	{id: opCompensationPropose, visibility: "public", mode: "mutation", submission: true, expected: true,
		cli: "zatiti operation compensation propose"},
	{id: opGet, visibility: "public", mode: "query", cli: "zatiti operation get"},
	{id: opList, visibility: "public", mode: "query", cli: "zatiti operation list"},
	{id: opPropose, visibility: "public", mode: "mutation", submission: true, cli: "zatiti operation propose"},
	{id: opReconcile, visibility: "public", mode: "mutation", submission: true, expected: true,
		effect: contract.EffectExternalRead, cli: "zatiti operation reconcile"},
	{id: opReplacementPropose, visibility: "public", mode: "mutation", submission: true, expected: true,
		cli: "zatiti operation replacement propose"},
}

// Service is the effects domain owner: immutable actions, logical
// operations, physical attempts, one-use dispatch claims and reconciliation
// history.
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

// New constructs the effects owner. It never queries peers, touches storage
// or starts goroutines; all runtime coupling arrives through deps.
func New(deps contract.Dependencies) (*Service, error) {
	if deps.Clock == nil {
		return nil, &contract.Fault{Code: contract.CodeInternalError, Message: "effects requires a clock"}
	}
	if deps.IDs == nil {
		return nil, &contract.Fault{Code: contract.CodeInternalError, Message: "effects requires an identity source"}
	}
	if deps.Ports == nil {
		return nil, &contract.Fault{Code: contract.CodeInternalError, Message: "effects requires a ports dependency"}
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
		schemas, ok := wireSchemas[m.id]
		if !ok {
			return fmt.Errorf("effects: operation %s has no wire schemas", m.id)
		}
		effect := contract.EffectLocal
		if m.effect != "" {
			effect = m.effect
		}
		d := contract.Descriptor{
			ID:              m.id,
			Version:         1,
			Owner:           ownerName,
			Visibility:      m.visibility,
			Mode:            m.mode,
			Effect:          effect,
			InputSchema:     json.RawMessage(schemas.Input),
			OutputSchema:    json.RawMessage(schemas.Output),
			ScopeRequired:   []string{"installation_id"},
			Callers:         m.callers,
			ExpectedVersion: m.expected,
			SubmissionKey:   m.submission,
		}
		if m.id == opReconcile {
			completion, err := mergeSchema(schemaReconcileResult)
			if err != nil {
				return fmt.Errorf("effects: operation %s completion schema: %w", m.id, err)
			}
			d.CompletionSchema = completion
		}
		if m.cli != "" {
			d.CLI = strings.Split(m.cli, " ")
		}
		if m.visibility == "public" {
			d.MCP = "zatiti_" + strings.ReplaceAll(m.id, ".", "_")
		}
		h, err := s.bindHandler(d)
		if err != nil {
			return fmt.Errorf("effects: bind %s: %w", m.id, err)
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
	case opAdmit:
		return bind(d, s.handleAdmit)
	case opClaim:
		return bind(d, s.handleClaim)
	case opPending:
		return bind(d, s.handlePending)
	case opPrepare:
		return bind(d, s.handlePrepare)
	case opRecord:
		return bind(d, s.handleRecord)
	case opCompensationPropose:
		return bind(d, s.handleCompensationPropose)
	case opGet:
		return bind(d, s.handleGet)
	case opList:
		return bind(d, s.handleList)
	case opPropose:
		return bind(d, s.handlePropose)
	case opReconcile:
		return bind(d, s.handleReconcile)
	case opReplacementPropose:
		return bind(d, s.handleReplacementPropose)
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
// shape checks mirror the registry's; internal mutations do not require a
// submission key because the implementation assignment fixes that boundary
// explicitly ("Submission key: not required at this internal/query/bootstrap
// boundary").
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
// checks. Internal mutations are exempt from the submission-key rule per the
// implementation assignment; public mutations require it.
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
func (s *Service) checkInstallation(unit contract.Unit, id contract.ID) error {
	if id == "" {
		return invalidInput("scope must name the installation")
	}
	if unit.Scope().InstallationID != id {
		return permissionDenied("scope installation %s does not match the caller's installation %s",
			id, unit.Scope().InstallationID)
	}
	return nil
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
		return nil, fmt.Errorf("effects: encode: %w", err)
	}
	canonical, err := contract.Canonicalize(raw)
	if err != nil {
		return nil, fmt.Errorf("effects: canonicalize: %w", err)
	}
	return canonical, nil
}
