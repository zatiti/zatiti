package configuration

import (
	"context"
	"encoding/json"
	"slices"
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
	effect          string
	submission      bool
	expectedVersion bool
	callers         []string
	cli             string // CLI tokens below the root command, space separated; empty for internal operations
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
	// Revision 3: the export/import durable job ledger seam (P00-008). Prepare
	// persists the export plan through _execution.job.create before any bytes
	// stage; record publishes the job/artifact once bytes are staged outside
	// the transaction. Neither call is self-callable by configuration -- both
	// are invoked by the controller/application orchestrating the job.
	{id: "_configuration.export.prepare", visibility: "internal", mode: "mutation", submission: false,
		callers: []string{"controller", "application"}},
	{id: "_configuration.export.record", visibility: "internal", mode: "mutation", submission: false, expectedVersion: true,
		callers: []string{"controller"}},
	{id: "_configuration.execution_profile.resolve", visibility: "internal", mode: "query", submission: false,
		callers: []string{"execution", "effects"}},
	{id: "_configuration.execution_profile.qualification.resolve", visibility: "internal", mode: "query", submission: false,
		callers: []string{"effects"}},
	{id: "_configuration.execution_profile.qualify.record", visibility: "internal", mode: "mutation", submission: false, expectedVersion: true,
		callers: []string{"controller"}},
	{id: "_configuration.execution_profile.qualify.finish", visibility: "internal", mode: "mutation", submission: false, expectedVersion: true,
		callers: []string{"controller"}},
	{id: "_configuration.snapshot", visibility: "internal", mode: "query", submission: false,
		callers: internalCallersSnapshot},
	{id: "_configuration.stage", visibility: "internal", mode: "mutation", submission: false,
		callers: []string{"configuration", "skills", "connections", "policy", "accounting", "scheduling", "memory"}},
	{id: "_configuration.validate", visibility: "internal", mode: "query", submission: false,
		callers: []string{"configuration", "application"}},

	// binding.*
	{id: "binding.archive", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "binding archive"},
	{id: "binding.create", visibility: "public", mode: "mutation", submission: true, cli: "binding create"},
	{id: "binding.get", visibility: "public", mode: "query", cli: "binding get"},
	{id: "binding.list", visibility: "public", mode: "query", cli: "binding list"},
	{id: "binding.update", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "binding update"},

	// configuration.*
	{id: "configuration.apply", visibility: "public", mode: "mutation", submission: true, cli: "configuration apply"},
	{id: "configuration.draft.create", visibility: "public", mode: "mutation", submission: true, cli: "configuration draft create"},
	{id: "configuration.draft.discard", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "configuration draft discard"},
	{id: "configuration.draft.get", visibility: "public", mode: "query", cli: "configuration draft get"},
	{id: "configuration.draft.list", visibility: "public", mode: "query", cli: "configuration draft list"},
	{id: "configuration.draft.update", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "configuration draft update"},
	{id: "configuration.plan", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "configuration plan"},
	{id: "configuration.plan.get", visibility: "public", mode: "query", cli: "configuration plan get"},
	{id: "configuration.plan.list", visibility: "public", mode: "query", cli: "configuration plan list"},
	{id: "configuration.revision.get", visibility: "public", mode: "query", cli: "configuration revision get"},
	{id: "configuration.revision.list", visibility: "public", mode: "query", cli: "configuration revision list"},
	{id: "configuration.rollback.plan", visibility: "public", mode: "mutation", submission: true, cli: "configuration rollback plan"},

	// execution_profile.*
	{id: "execution_profile.archive", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "execution_profile archive"},
	{id: "execution_profile.create", visibility: "public", mode: "mutation", submission: true, cli: "execution_profile create"},
	{id: "execution_profile.qualify", visibility: "public", mode: "mutation", effect: "external_read", submission: true, completion: true, cli: "execution_profile qualify"},
	{id: "execution_profile.get", visibility: "public", mode: "query", cli: "execution_profile get"},
	{id: "execution_profile.list", visibility: "public", mode: "query", cli: "execution_profile list"},
	{id: "execution_profile.update", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "execution_profile update"},

	// organization.*
	{id: "organization.archive", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "organization archive"},
	{id: "organization.chief.replace", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "organization chief replace"},
	{id: "organization.create", visibility: "public", mode: "mutation", submission: true, cli: "organization create"},
	{id: "organization.export", visibility: "public", mode: "mutation", submission: true, completion: true, cli: "organization export"},
	{id: "organization.get", visibility: "public", mode: "query", cli: "organization get"},
	{id: "organization.import", visibility: "public", mode: "mutation", submission: true, cli: "organization import"},
	{id: "organization.list", visibility: "public", mode: "query", cli: "organization list"},
	{id: "organization.move", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "organization move"},
	{id: "organization.update", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "organization update"},

	// project.*
	{id: "project.archive", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "project archive"},
	{id: "project.create", visibility: "public", mode: "mutation", submission: true, cli: "project create"},
	{id: "project.export", visibility: "public", mode: "mutation", submission: true, completion: true, cli: "project export"},
	{id: "project.get", visibility: "public", mode: "query", cli: "project get"},
	{id: "project.import", visibility: "public", mode: "mutation", submission: true, cli: "project import"},
	{id: "project.list", visibility: "public", mode: "query", cli: "project list"},
	{id: "project.update", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "project update"},

	// team.*
	{id: "team.archive", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "team archive"},
	{id: "team.create", visibility: "public", mode: "mutation", submission: true, cli: "team create"},
	{id: "team.export", visibility: "public", mode: "mutation", submission: true, completion: true, cli: "team export"},
	{id: "team.get", visibility: "public", mode: "query", cli: "team get"},
	{id: "team.import", visibility: "public", mode: "mutation", submission: true, cli: "team import"},
	{id: "team.list", visibility: "public", mode: "query", cli: "team list"},
	{id: "team.update", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "team update"},

	// worker.*
	{id: "worker.archive", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "worker archive"},
	{id: "worker.create", visibility: "public", mode: "mutation", submission: true, cli: "worker create"},
	{id: "worker.get", visibility: "public", mode: "query", cli: "worker get"},
	{id: "worker.list", visibility: "public", mode: "query", cli: "worker list"},
	{id: "worker.move", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "worker move"},
	{id: "worker.update", visibility: "public", mode: "mutation", submission: true, expectedVersion: true, cli: "worker update"},
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

// Compile-time proof that *Service implements the shared Module contract
// and, for the revision-3 export job ledger (jobs.go), LocalJobRunner: the
// bounded IO organization.export/team.export/project.export and
// _configuration.export.prepare defer past their owning transaction.
var _ contract.Module = (*Service)(nil)
var _ contract.LocalJobRunner = (*Service)(nil)

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
		effect := m.effect
		if effect == "" {
			effect = "local"
		}
		d := contract.Descriptor{
			ID:               m.id,
			Version:          1,
			Owner:            ownerName,
			Visibility:       m.visibility,
			Mode:             m.mode,
			Effect:           effect,
			InputSchema:      inputSchema(m.id),
			OutputSchema:     outputSchema(m.id),
			CompletionSchema: nil,
			ScopeRequired:    scopeRequirement(inputSchema(m.id)),
			Callers:          m.callers,
			ExpectedVersion:  m.expectedVersion,
			SubmissionKey:    m.submission,
		}
		if scopeRequirementExempt[m.id] {
			d.ScopeRequired = nil
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

// scopeRequirementExempt names operations whose frozen catalog entry carries
// no scope_required despite an input schema that requires "scope": the
// export job ledger's scope names the RESOURCE'S home installation for the
// job the caller (controller/application) is preparing, not the enforced
// envelope of the calling principal, so it is not counted the way an
// ordinary public operation's scope is.
var scopeRequirementExempt = map[string]bool{
	"_configuration.export.prepare": true,
}

// handlerFunc executes one operation inside the caller's unit.
type handlerFunc func(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error)

// handlers is the strict dispatch table; every registered operation has one.
var handlers = map[string]handlerFunc{
	"_configuration.activate":                                handleActivate,
	"_configuration.bootstrap":                               handleBootstrap,
	"_configuration.export.prepare":                          handleExportPrepare,
	"_configuration.export.record":                           handleExportRecord,
	"_configuration.execution_profile.resolve":               handleResolveExecutionProfile,
	"_configuration.execution_profile.qualification.resolve": handleResolveExecutionProfileQualification,
	"_configuration.execution_profile.qualify.record":        handleRecordQualifiedExecutionProfile,
	"_configuration.execution_profile.qualify.finish":        handleFinishQualifiedExecutionProfile,
	"_configuration.snapshot":                                handleSnapshot,
	"_configuration.stage":                                   handleStage,
	"_configuration.validate":                                handleValidate,

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
	"execution_profile.qualify": handleQualifyExecutionProfile,
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

// accepted builds an accepted payload carrying data: the durable job now
// exists but its eventual result (job.get's Job.result, matching the
// operation's declared completion_schema) is not yet established.
func (s *Service) accepted(data any) (contract.Payload, error) {
	raw, err := marshalData(data)
	if err != nil {
		return contract.Payload{}, err
	}
	return contract.Payload{Status: contract.StatusAccepted, Data: raw}, nil
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
