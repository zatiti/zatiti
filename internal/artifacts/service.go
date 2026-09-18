package artifacts

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/zatiti/zatiti/internal/contract"
)

// ownerName is the domain owner identity used in descriptors and events.
const ownerName = "artifacts"

// Operation identifiers.
const (
	opMetadata = "_artifacts.metadata"
	opPublish  = "_artifacts.publish"

	opArtifactExport = "artifact.export"
	opArtifactGet    = "artifact.get"
	opArtifactList   = "artifact.list"
	opArtifactRead   = "artifact.read"
	opUploadBegin    = "artifact.upload.begin"
	opUploadCancel   = "artifact.upload.cancel"
	opUploadChunk    = "artifact.upload.chunk"
	opUploadFinish   = "artifact.upload.finish"
)

// localIOOps is the frozen set of operations this package serves through
// Prepare/Perform/Finish instead of Handle. It mirrors the shared contract's
// registered local IO set for artifacts.
var localIOOps = map[string]bool{
	opArtifactExport: true,
	opArtifactRead:   true,
	opUploadCancel:   true,
	opUploadChunk:    true,
	opUploadFinish:   true,
}

// opMeta is the static registration record for one operation.
type opMeta struct {
	id         string
	visibility string
	mode       string
	submission bool
	expected   bool // descriptor advertises optimistic version fencing
	callers    []string
	cli        string // CLI tokens below the root command, space separated; empty for internal operations
	completion bool   // declares the eventual result schema of an asynchronous operation
}

// opMetas lists every owned operation exactly as the implementation
// assignment embeds it. Internal operations come first, then the public
// catalog in schema order.
var opMetas = []opMeta{
	{id: opMetadata, visibility: "internal", mode: "query",
		callers: []string{"execution", "tasks", "effects", "memory", "messaging", "skills", "installation"}},
	{id: opPublish, visibility: "internal", mode: "mutation",
		callers: []string{"controller", "execution", "memory", "skills", "installation"}},

	{id: opArtifactExport, visibility: "public", mode: "mutation", submission: true,
		completion: true, cli: "artifact export"},
	{id: opArtifactGet, visibility: "public", mode: "query", cli: "artifact get"},
	{id: opArtifactList, visibility: "public", mode: "query", cli: "artifact list"},
	{id: opArtifactRead, visibility: "public", mode: "query", cli: "artifact read"},
	{id: opUploadBegin, visibility: "public", mode: "mutation", submission: true,
		cli: "artifact upload begin"},
	{id: opUploadCancel, visibility: "public", mode: "mutation", submission: true,
		expected: true, completion: true, cli: "artifact upload cancel"},
	{id: opUploadChunk, visibility: "public", mode: "mutation", submission: true,
		completion: true, cli: "artifact upload chunk"},
	{id: opUploadFinish, visibility: "public", mode: "mutation", submission: true,
		expected: true, completion: true, cli: "artifact upload finish"},
}

// Service is the artifacts domain owner: immutable artifact metadata,
// resumable bounded uploads and integrity-visible byte access.
type Service struct {
	deps        contract.Dependencies
	catalog     map[string]contract.Descriptor
	descriptors []contract.Descriptor

	// cursorMu guards the lazily minted cursor authentication key.
	cursorMu  sync.Mutex
	cursorKey []byte
}

// Compile-time proof that *Service implements the shared Module contract
// and the LocalIO seam for artifact.upload.chunk/finish/cancel, artifact.read
// and artifact.export.
var (
	_ contract.Module  = (*Service)(nil)
	_ contract.LocalIO = (*Service)(nil)
)

// New constructs the artifacts owner. It never queries peers, touches
// storage or starts goroutines; all runtime coupling arrives through deps.
// Clock, identity source and a blob store are required: every registered
// local IO operation needs bytes. Ports are checked at call time, since only
// artifact.export needs the outgoing _execution.job.create call.
func New(deps contract.Dependencies) (*Service, error) {
	if deps.Clock == nil {
		return nil, contractFault("artifacts requires a clock")
	}
	if deps.IDs == nil {
		return nil, contractFault("artifacts requires an identity source")
	}
	if deps.Blobs == nil {
		return nil, contractFault("artifacts requires a blob store")
	}
	s := &Service{
		deps:    deps,
		catalog: make(map[string]contract.Descriptor, len(opMetas)),
	}
	s.descriptors = s.buildDescriptors()
	return s, nil
}

func contractFault(msg string) *contract.Fault {
	return &contract.Fault{Code: contract.CodeInternalError, Message: msg}
}

// Name implements contract.Module.
func (s *Service) Name() string { return ownerName }

// Migrations implements contract.Module.
func (s *Service) Migrations() []contract.Migration { return migrations() }

// Descriptors implements contract.Module: every owned operation with its
// composed schemas, transport bindings and caller allowlists.
func (s *Service) Descriptors() []contract.Descriptor { return s.descriptors }

// buildDescriptors composes the descriptor list and fills the lookup map.
func (s *Service) buildDescriptors() []contract.Descriptor {
	out := make([]contract.Descriptor, 0, len(opMetas))
	for _, m := range opMetas {
		d := contract.Descriptor{
			ID:               m.id,
			Version:          1,
			Owner:            ownerName,
			Visibility:       m.visibility,
			Mode:             m.mode,
			Effect:           contract.EffectLocal,
			InputSchema:      inputSchema(m.id),
			OutputSchema:     outputSchema(m.id),
			CompletionSchema: nil,
			ScopeRequired:    []string{"installation_id"},
			Callers:          m.callers,
			ExpectedVersion:  m.expected,
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
		s.catalog[m.id] = d
		out = append(out, d)
	}
	return out
}

// handlerFunc executes one operation inside the caller's unit.
type handlerFunc func(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error)

// handlers is the strict dispatch table. The five registered local IO
// operations are absent: they are served through Prepare/Perform/Finish, and
// Handle refuses them rather than silently running their mutation phases
// here.
var handlers = map[string]handlerFunc{
	opMetadata: handleMetadata,
	opPublish:  handlePublish,

	opArtifactGet:  handleArtifactGet,
	opArtifactList: handleArtifactList,
	opUploadBegin:  handleUploadBegin,
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
	if d.Mode == contract.ModeMutation && unit.ReadOnly() {
		return contract.Payload{}, invalidInput("operation %s is a mutation and requires a write transaction", inv.Operation)
	}
	if localIOOps[inv.Operation] {
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
func decodeInto[T any](op string, raw json.RawMessage) (*T, error) {
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

// narrowScope enforces the optional scope dimensions the caller declared:
// any set field must equal the resource's stored dimension, so a scoped
// caller can neither widen its query nor leak across organization, project,
// worker or task boundaries.
func narrowScope(scope contract.Scope, organizationID, projectID, workerID, taskID contract.ID) error {
	if scope.OrganizationID != "" && scope.OrganizationID != organizationID {
		return permissionDenied("scope organization does not match the resource")
	}
	if scope.ProjectID != "" && scope.ProjectID != projectID {
		return permissionDenied("scope project does not match the resource")
	}
	if scope.WorkerID != "" && scope.WorkerID != workerID {
		return permissionDenied("scope worker does not match the resource")
	}
	if scope.TaskID != "" && scope.TaskID != taskID {
		return permissionDenied("scope task does not match the resource")
	}
	return nil
}
