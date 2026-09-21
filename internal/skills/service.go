package skills

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"github.com/zatiti/zatiti/internal/contract"
)

// ownerName is the domain owner identity used in descriptors and events.
const ownerName = "skills"

// opMeta is the static registration record for one operation.
type opMeta struct {
	id              string
	visibility      string
	mode            string
	submission      bool
	expectedVersion bool
	callers         []string
	cli             string // CLI tokens below the root command, space separated; empty for internal operations
	completion      bool   // declares the eventual result schema of an asynchronous operation
}

// opMetas lists every owned operation. Internal operations come first, then
// the public catalog in schema order.
var opMetas = []opMeta{
	// Internal operations: compiler-only activation and validation.
	{id: "_skills.activate", visibility: "internal", mode: "mutation", submission: false,
		callers: []string{"configuration", "application"}},
	{id: "_skills.validate", visibility: "internal", mode: "query", submission: false,
		callers: []string{"configuration", "application"}},
	// _skills.evaluation.record is the sole path that records published
	// verifier evidence against the exact immutable skill version an
	// evaluation names, routed through the durable execution job ledger
	// rather than a handler-local completion (revision 3).
	{id: "_skills.evaluation.record", visibility: "internal", mode: "mutation", submission: false,
		expectedVersion: true, callers: []string{"execution", "controller"}},

	// skill.*
	{id: "skill.archive", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "skill archive"},
	{id: "skill.evaluate", visibility: "public", mode: "mutation", submission: true, completion: true, cli: "skill evaluate"},
	{id: "skill.evaluation.status", visibility: "public", mode: "query", cli: "skill evaluation status"},
	{id: "skill.get", visibility: "public", mode: "query", cli: "skill get"},
	{id: "skill.import", visibility: "public", mode: "mutation", submission: true, completion: true, cli: "skill import"},
	{id: "skill.list", visibility: "public", mode: "query", cli: "skill list"},
}

// Service is the skills domain owner: immutable skill versions, safe import
// validation through the local IO phases and sealed evaluation jobs.
type Service struct {
	clock       contract.Clock
	ids         contract.IDSource
	ports       contract.Ports
	blobs       contract.BlobStore
	catalog     map[string]contract.Descriptor
	descriptors []contract.Descriptor

	// cursorMu guards the lazily minted cursor authentication key.
	cursorMu  sync.Mutex
	cursorKey []byte
}

// Compile-time proof that *Service implements the shared Module contract,
// the LocalIO seam for skill.import and the LocalJobRunner seam for the
// skill.evaluate durable job (RunJob, jobs.go).
var (
	_ contract.Module         = (*Service)(nil)
	_ contract.LocalIO        = (*Service)(nil)
	_ contract.LocalJobRunner = (*Service)(nil)
)

// New constructs the skills owner. It never queries peers, touches storage
// or starts goroutines; all runtime coupling arrives through deps.
func New(deps contract.Dependencies) (*Service, error) {
	if deps.Clock == nil {
		return nil, contractFault("skills requires a clock")
	}
	if deps.IDs == nil {
		return nil, contractFault("skills requires an identity source")
	}
	if deps.Ports == nil {
		return nil, contractFault("skills requires owner ports")
	}
	if deps.Blobs == nil {
		return nil, contractFault("skills requires a blob store")
	}
	s := &Service{
		clock:   deps.Clock,
		ids:     deps.IDs,
		ports:   deps.Ports,
		blobs:   deps.Blobs,
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
func (s *Service) Migrations() []contract.Migration { return skillsMigrations() }

// Descriptors implements contract.Module: every owned operation with its
// composed schemas, transport bindings and caller allowlists.
func (s *Service) Descriptors() []contract.Descriptor { return s.descriptors }

// buildDescriptors composes the descriptor list and fills the lookup map.
func buildDescriptors(catalog map[string]contract.Descriptor) []contract.Descriptor {
	out := make([]contract.Descriptor, 0, len(opMetas))
	for _, m := range opMetas {
		d := contract.Descriptor{
			ID:               m.id,
			Version:          1,
			Owner:            ownerName,
			Visibility:       m.visibility,
			Mode:             m.mode,
			Effect:           "local",
			InputSchema:      inputSchema(m.id),
			OutputSchema:     outputSchema(m.id),
			CompletionSchema: nil,
			ScopeRequired:    scopeRequirement(inputSchema(m.id)),
			Callers:          m.callers,
			ExpectedVersion:  m.expectedVersion,
			SubmissionKey:    m.submission,
		}
		if m.cli != "" {
			d.CLI = strings.Split(m.cli, " ")
		}
		if m.visibility == "public" {
			d.MCP = "zatiti_" + strings.ReplaceAll(m.id, ".", "_")
		}
		if m.completion {
			d.CompletionSchema = completionSchema(m.id)
		}
		catalog[m.id] = d
		out = append(out, d)
	}
	return out
}

// scopeRequirement derives the frozen catalog's scope requirement for an
// operation: "installation_id" exactly when the input schema requires a
// scope field, nothing otherwise.
func scopeRequirement(input json.RawMessage) []string {
	var probe struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(input, &probe); err == nil && slices.Contains(probe.Required, "scope") {
		return []string{"installation_id"}
	}
	return nil
}

// handlerFunc executes one operation inside the caller's unit.
type handlerFunc func(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error)

// handlers is the strict dispatch table. skill.import is absent: it is a
// registered local IO operation served through Prepare/Perform/Finish, and
// Handle refuses it rather than silently running the mutation phases here.
var handlers = map[string]handlerFunc{
	"_skills.activate":          handleActivate,
	"_skills.validate":          handleValidate,
	"_skills.evaluation.record": handleEvaluationRecord,

	"skill.archive":           handleArchive,
	"skill.evaluate":          handleEvaluate,
	"skill.evaluation.status": handleEvaluationStatus,
	"skill.get":               handleGet,
	"skill.list":              handleList,
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
	if inv.Operation == "skill.import" {
		return contract.Payload{}, internalError(
			"operation %s is a local IO operation and is served through Prepare/Perform/Finish", inv.Operation)
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
