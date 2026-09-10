package tasks

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

// opMeta is the static registration record for one operation.
type opMeta struct {
	id              string
	visibility      string
	mode            string
	submission      bool
	expectedVersion bool
	callers         []string
	cli             string // "zatiti ..." CLI path; empty for internal operations
}

// opMetas lists every owned operation: the four internal peer operations
// with their exact caller allowlists, then the public catalog.
var opMetas = []opMeta{
	// Internal operations.
	{id: "_tasks.create", visibility: "internal", mode: "mutation", submission: false,
		callers: []string{"scheduling", "messaging", "execution"}},
	{id: "_tasks.ready", visibility: "internal", mode: "query", submission: false,
		callers: []string{"execution", "controller"}},
	{id: "_tasks.snapshot", visibility: "internal", mode: "query", submission: false,
		callers: []string{"execution", "effects", "scheduling", "memory", "reviews", "policy"}},
	{id: "_tasks.transition", visibility: "internal", mode: "mutation", submission: false,
		callers: []string{"execution", "scheduling", "installation"}},

	// task.*
	{id: "task.accept", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti task accept"},
	{id: "task.assign", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti task assign"},
	{id: "task.cancel", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti task cancel"},
	{id: "task.create", visibility: "public", mode: "mutation", submission: true, cli: "zatiti task create"},
	{id: "task.delegate", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti task delegate"},
	{id: "task.dependencies", visibility: "public", mode: "query", cli: "zatiti task dependencies"},
	{id: "task.get", visibility: "public", mode: "query", cli: "zatiti task get"},
	{id: "task.list", visibility: "public", mode: "query", cli: "zatiti task list"},
	{id: "task.retry", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti task retry"},
	{id: "task.update", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti task update"},
}

// Service is the tasks domain owner: durable bounded tasks with pinned
// outcomes, immutable accepted verifier contracts, the dependency graph and
// narrowing delegation.
type Service struct {
	clock       contract.Clock
	ids         contract.IDSource
	ports       contract.Ports
	catalog     map[string]contract.Descriptor
	descriptors []contract.Descriptor
}

// Compile-time proof that *Service implements the shared Module contract.
var _ contract.Module = (*Service)(nil)

// New constructs the tasks owner. It never queries peers, touches storage
// or starts goroutines; all runtime coupling arrives through deps.
func New(deps contract.Dependencies) (*Service, error) {
	if deps.Clock == nil {
		return nil, contractFault("tasks requires a clock")
	}
	if deps.IDs == nil {
		return nil, contractFault("tasks requires an identity source")
	}
	if deps.Ports == nil {
		return nil, contractFault("tasks requires owner ports")
	}
	s := &Service{
		clock:   deps.Clock,
		ids:     deps.IDs,
		ports:   deps.Ports,
		catalog: make(map[string]contract.Descriptor, len(opMetas)),
	}
	s.descriptors = buildDescriptors(s.catalog)
	return s, nil
}

func contractFault(msg string) *contract.Fault {
	return &contract.Fault{Code: contract.CodeInternalError, Message: msg}
}

// Name implements contract.Module.
func (s *Service) Name() string { return ownerName }

// Migrations implements contract.Module.
func (s *Service) Migrations() []contract.Migration { return tasksMigrations() }

// Descriptors implements contract.Module: every owned operation with its
// composed schemas, transport bindings and caller allowlists.
func (s *Service) Descriptors() []contract.Descriptor { return s.descriptors }

// buildDescriptors composes the descriptor list and fills the lookup map.
func buildDescriptors(catalog map[string]contract.Descriptor) []contract.Descriptor {
	out := make([]contract.Descriptor, 0, len(opMetas))
	for _, m := range opMetas {
		d := contract.Descriptor{
			ID:              m.id,
			Version:         1,
			Owner:           ownerName,
			Visibility:      m.visibility,
			Mode:            m.mode,
			Effect:          "local",
			InputSchema:     inputSchema(m.id),
			OutputSchema:    outputSchema(m.id),
			ScopeRequired:   []string{"installation_id"},
			Callers:         m.callers,
			ExpectedVersion: m.expectedVersion,
			SubmissionKey:   m.submission,
		}
		if m.cli != "" {
			d.CLI = strings.Split(m.cli, " ")
		}
		if m.visibility == "public" {
			d.MCP = "zatiti_" + strings.ReplaceAll(m.id, ".", "_")
		}
		catalog[m.id] = d
		out = append(out, d)
	}
	return out
}

// handlerFunc executes one operation inside the caller's unit.
type handlerFunc func(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error)

// handlers is the strict dispatch table; every registered operation has one.
var handlers = map[string]handlerFunc{
	"_tasks.create":     handleTasksCreate,
	"_tasks.ready":      handleTasksReady,
	"_tasks.snapshot":   handleTasksSnapshot,
	"_tasks.transition": handleTasksTransition,

	"task.accept":       handleTaskAccept,
	"task.assign":       handleTaskAssign,
	"task.cancel":       handleTaskCancel,
	"task.create":       handleTaskCreate,
	"task.delegate":     handleTaskDelegate,
	"task.dependencies": handleTaskDependencies,
	"task.get":          handleTaskGet,
	"task.list":         handleTaskList,
	"task.retry":        handleTaskRetry,
	"task.update":       handleTaskUpdate,
}

// Handle implements contract.Module with strict dispatch: schema-validated
// strict decode, known operation only, mutations only in write transactions.
func (s *Service) Handle(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	d, ok := s.catalog[inv.Operation]
	if !ok {
		return contract.Payload{}, notFound("unknown operation %q", inv.Operation)
	}
	if inv.Version != d.Version {
		return contract.Payload{}, invalidInput("operation %s version %d does not match declared version %d",
			inv.Operation, inv.Version, d.Version)
	}
	if d.Mode == "mutation" && unit.ReadOnly() {
		return contract.Payload{}, invalidInput("operation %s is a mutation and requires a write transaction", inv.Operation)
	}
	h, ok := handlers[inv.Operation]
	if !ok {
		return contract.Payload{}, internalError("operation %s has no handler", inv.Operation)
	}
	return h(ctx, s, unit, inv)
}

// completed builds a completed payload carrying data.
func (s *Service) completed(data any) (contract.Payload, error) {
	raw, err := marshalData(data)
	if err != nil {
		return contract.Payload{}, err
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: raw}, nil
}

// decodeInto validates raw against the operation input schema, then strict-
// decodes it into T. Validation runs first so schema-level constraints
// (formats, bounds, additionalProperties) reject before typed decoding.
func decodeInto[T any](s *Service, op string, raw json.RawMessage) (*T, error) {
	schema := inputSchema(op)
	if err := contract.ValidateSchema(schema, raw); err != nil {
		return nil, invalidInput("input does not match the %s schema: %v", op, err)
	}
	var in T
	if err := contract.DecodeStrict(raw, &in); err != nil {
		return nil, invalidInput("input decoding failed for %s: %v", op, err)
	}
	return &in, nil
}
