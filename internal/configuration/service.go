package configuration

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

// ownerName is the domain owner identity used in descriptors and events.
const ownerName = "configuration"

// opMeta is the static registration record for one operation.
type opMeta struct {
	id              string
	visibility      string
	mode            string
	submission      bool
	expectedVersion bool
	callers         []string
	cli             string // "zatiti ..." CLI path; empty for internal operations
	completion      bool   // declares the eventual export artifact result schema
}

var internalCallersSnapshot = []string{
	"application", "policy", "tasks", "execution", "effects", "memory",
	"reviews", "accounting", "scheduling", "messaging", "connections", "installation",
}

// opMetas lists every owned operation. Internal operations come first, then
// the public catalog in schema order.
var opMetas = []opMeta{
	// Internal operations.
	{id: "_configuration.activate", visibility: "internal", mode: "mutation", submission: false,
		callers: []string{"configuration", "application"}},
	{id: "_configuration.bootstrap", visibility: "internal", mode: "mutation", submission: false,
		callers: []string{"installation"}},
	{id: "_configuration.snapshot", visibility: "internal", mode: "query", submission: false,
		callers: internalCallersSnapshot},
	{id: "_configuration.stage", visibility: "internal", mode: "mutation", submission: false,
		callers: []string{"configuration", "skills", "connections", "policy", "accounting", "scheduling", "memory"}},
	{id: "_configuration.validate", visibility: "internal", mode: "query", submission: false,
		callers: []string{"configuration", "application"}},

	// binding.*
	{id: "binding.archive", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti binding archive"},
	{id: "binding.create", visibility: "public", mode: "mutation", submission: true, cli: "zatiti binding create"},
	{id: "binding.get", visibility: "public", mode: "query", cli: "zatiti binding get"},
	{id: "binding.list", visibility: "public", mode: "query", cli: "zatiti binding list"},
	{id: "binding.update", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti binding update"},

	// configuration.*
	{id: "configuration.apply", visibility: "public", mode: "mutation", submission: true, cli: "zatiti configuration apply"},
	{id: "configuration.draft.create", visibility: "public", mode: "mutation", submission: true, cli: "zatiti configuration draft create"},
	{id: "configuration.draft.discard", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti configuration draft discard"},
	{id: "configuration.draft.get", visibility: "public", mode: "query", cli: "zatiti configuration draft get"},
	{id: "configuration.draft.list", visibility: "public", mode: "query", cli: "zatiti configuration draft list"},
	{id: "configuration.draft.update", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti configuration draft update"},
	{id: "configuration.plan", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti configuration plan"},
	{id: "configuration.plan.get", visibility: "public", mode: "query", cli: "zatiti configuration plan get"},
	{id: "configuration.plan.list", visibility: "public", mode: "query", cli: "zatiti configuration plan list"},
	{id: "configuration.revision.get", visibility: "public", mode: "query", cli: "zatiti configuration revision get"},
	{id: "configuration.revision.list", visibility: "public", mode: "query", cli: "zatiti configuration revision list"},
	{id: "configuration.rollback.plan", visibility: "public", mode: "mutation", submission: true, cli: "zatiti configuration rollback plan"},

	// execution_profile.*
	{id: "execution_profile.archive", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti execution_profile archive"},
	{id: "execution_profile.create", visibility: "public", mode: "mutation", submission: true, cli: "zatiti execution_profile create"},
	{id: "execution_profile.get", visibility: "public", mode: "query", cli: "zatiti execution_profile get"},
	{id: "execution_profile.list", visibility: "public", mode: "query", cli: "zatiti execution_profile list"},
	{id: "execution_profile.update", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti execution_profile update"},

	// organization.*
	{id: "organization.archive", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti organization archive"},
	{id: "organization.chief.replace", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti organization chief replace"},
	{id: "organization.create", visibility: "public", mode: "mutation", submission: true, cli: "zatiti organization create"},
	{id: "organization.export", visibility: "public", mode: "mutation", submission: true, completion: true, cli: "zatiti organization export"},
	{id: "organization.get", visibility: "public", mode: "query", cli: "zatiti organization get"},
	{id: "organization.import", visibility: "public", mode: "mutation", submission: true, cli: "zatiti organization import"},
	{id: "organization.list", visibility: "public", mode: "query", cli: "zatiti organization list"},
	{id: "organization.move", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti organization move"},
	{id: "organization.update", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti organization update"},

	// project.*
	{id: "project.archive", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti project archive"},
	{id: "project.create", visibility: "public", mode: "mutation", submission: true, cli: "zatiti project create"},
	{id: "project.export", visibility: "public", mode: "mutation", submission: true, completion: true, cli: "zatiti project export"},
	{id: "project.get", visibility: "public", mode: "query", cli: "zatiti project get"},
	{id: "project.import", visibility: "public", mode: "mutation", submission: true, cli: "zatiti project import"},
	{id: "project.list", visibility: "public", mode: "query", cli: "zatiti project list"},
	{id: "project.update", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti project update"},

	// team.*
	{id: "team.archive", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti team archive"},
	{id: "team.create", visibility: "public", mode: "mutation", submission: true, cli: "zatiti team create"},
	{id: "team.export", visibility: "public", mode: "mutation", submission: true, completion: true, cli: "zatiti team export"},
	{id: "team.get", visibility: "public", mode: "query", cli: "zatiti team get"},
	{id: "team.import", visibility: "public", mode: "mutation", submission: true, cli: "zatiti team import"},
	{id: "team.list", visibility: "public", mode: "query", cli: "zatiti team list"},
	{id: "team.update", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti team update"},

	// worker.*
	{id: "worker.archive", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti worker archive"},
	{id: "worker.create", visibility: "public", mode: "mutation", submission: true, cli: "zatiti worker create"},
	{id: "worker.get", visibility: "public", mode: "query", cli: "zatiti worker get"},
	{id: "worker.list", visibility: "public", mode: "query", cli: "zatiti worker list"},
	{id: "worker.move", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti worker move"},
	{id: "worker.update", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "zatiti worker update"},
}

// Service is the configuration domain owner: organizations, chiefs, teams,
// projects, worker definitions, bindings, drafts, plans, revisions and the
// sole definition compiler.
type Service struct {
	clock       contract.Clock
	ids         contract.IDSource
	ports       contract.Ports
	blobs       contract.BlobStore
	catalog     map[string]contract.Descriptor
	descriptors []contract.Descriptor
}

// Compile-time proof that *Service implements the shared Module contract.
var _ contract.Module = (*Service)(nil)

// New constructs the configuration owner. It never queries peers, touches
// storage or starts goroutines; all runtime coupling arrives through deps.
func New(deps contract.Dependencies) (*Service, error) {
	if deps.Clock == nil {
		return nil, contractFault("configuration requires a clock")
	}
	if deps.IDs == nil {
		return nil, contractFault("configuration requires an identity source")
	}
	if deps.Ports == nil {
		return nil, contractFault("configuration requires owner ports")
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
func (s *Service) Migrations() []contract.Migration { return configurationMigrations() }

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
			ScopeRequired:    []string{"installation_id"},
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

// handlerFunc executes one operation inside the caller's unit.
type handlerFunc func(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error)

// handlers is the strict dispatch table; every registered operation has one.
var handlers = map[string]handlerFunc{
	"_configuration.activate":  handleActivate,
	"_configuration.bootstrap": handleBootstrap,
	"_configuration.snapshot":  handleSnapshot,
	"_configuration.stage":     handleStage,
	"_configuration.validate":  handleValidate,

	"binding.archive": handleArchive("binding"),
	"binding.create":  handleCreateResource("binding"),
	"binding.get":     handleGetResource("binding"),
	"binding.list":    handleListResource("binding"),
	"binding.update":  handleUpdateResource("binding"),

	"configuration.apply":         handleApply,
	"configuration.draft.create":  handleDraftCreate,
	"configuration.draft.discard": handleDraftDiscard,
	"configuration.draft.get":     handleDraftGet,
	"configuration.draft.list":    handleListDrafts,
	"configuration.draft.update":  handleDraftUpdate,
	"configuration.plan":          handlePlan,
	"configuration.plan.get":      handlePlanGet,
	"configuration.plan.list":     handleListPlans,
	"configuration.revision.get":  handleRevisionGet,
	"configuration.revision.list": handleListRevisions,
	"configuration.rollback.plan": handleRollbackPlan,

	"execution_profile.archive": handleArchive("execution_profile"),
	"execution_profile.create":  handleCreateResource("execution_profile"),
	"execution_profile.get":     handleGetResource("execution_profile"),
	"execution_profile.list":    handleListResource("execution_profile"),
	"execution_profile.update":  handleUpdateResource("execution_profile"),

	"organization.archive":       handleArchive("organization"),
	"organization.chief.replace": handleChiefReplace,
	"organization.create":        handleOrganizationCreate,
	"organization.export":        handleExport("organization"),
	"organization.get":           handleGetResource("organization"),
	"organization.import":        handleImport("organization"),
	"organization.list":          handleListResource("organization"),
	"organization.move":          handleMoveOrganization,
	"organization.update":        handleUpdateResource("organization"),

	"project.archive": handleArchive("project"),
	"project.create":  handleCreateResource("project"),
	"project.export":  handleExport("project"),
	"project.get":     handleGetResource("project"),
	"project.import":  handleImport("project"),
	"project.list":    handleListResource("project"),
	"project.update":  handleUpdateResource("project"),

	"team.archive": handleArchive("team"),
	"team.create":  handleCreateResource("team"),
	"team.export":  handleExport("team"),
	"team.get":     handleGetResource("team"),
	"team.import":  handleImport("team"),
	"team.list":    handleListResource("team"),
	"team.update":  handleUpdateResource("team"),

	"worker.archive": handleArchive("worker"),
	"worker.create":  handleCreateResource("worker"),
	"worker.get":     handleGetResource("worker"),
	"worker.list":    handleListResource("worker"),
	"worker.move":    handleMoveWorker,
	"worker.update":  handleUpdateResource("worker"),
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
