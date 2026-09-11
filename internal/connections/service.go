package connections

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

// ownerName is the domain owner identity used in descriptors and events.
const ownerName = "connections"

// opMeta is the static registration record for one operation.
type opMeta struct {
	id              string
	visibility      string
	mode            string
	effect          string
	submission      bool
	expectedVersion bool
	callers         []string
	cli             string // "zatiti ..." CLI path; empty for internal operations
	completion      bool   // declares the eventual job result schema
}

// opMetas lists every owned operation. Internal operations come first, then
// the public catalog in schema order. Caller allowlists are the exact sets
// declared by the implementation assignment.
var opMetas = []opMeta{
	// Internal operations.
	{id: "_connections.activate", visibility: "internal", mode: "mutation", effect: "local",
		callers: []string{"configuration", "application"}},
	{id: "_connections.resolve", visibility: "internal", mode: "query", effect: "local",
		callers: []string{"effects", "execution", "memory", "skills", "configuration"}},
	{id: "_connections.validate", visibility: "internal", mode: "query", effect: "local",
		callers: []string{"configuration", "application"}},
	{id: "_connections.validation.record", visibility: "internal", mode: "mutation", effect: "local",
		callers: []string{"effects", "controller"}},

	// connection.*
	{id: "connection.archive", visibility: "public", mode: "mutation", effect: "local",
		submission: true, expectedVersion: true, cli: "zatiti connection archive"},
	{id: "connection.create", visibility: "public", mode: "mutation", effect: "local",
		submission: true, cli: "zatiti connection create"},
	{id: "connection.get", visibility: "public", mode: "query", effect: "local",
		cli: "zatiti connection get"},
	{id: "connection.list", visibility: "public", mode: "query", effect: "local",
		cli: "zatiti connection list"},
	{id: "connection.revoke", visibility: "public", mode: "mutation", effect: "local",
		submission: true, expectedVersion: true, cli: "zatiti connection revoke"},
	{id: "connection.rotate", visibility: "public", mode: "mutation", effect: "external_read",
		submission: true, expectedVersion: true, completion: true, cli: "zatiti connection rotate"},
	{id: "connection.setup.begin", visibility: "public", mode: "mutation", effect: "local",
		submission: true, expectedVersion: true, completion: true, cli: "zatiti connection setup begin"},
	{id: "connection.setup.cancel", visibility: "public", mode: "mutation", effect: "local",
		submission: true, expectedVersion: true, completion: true, cli: "zatiti connection setup cancel"},
	{id: "connection.setup.complete", visibility: "public", mode: "mutation", effect: "local",
		submission: true, expectedVersion: true, completion: true, cli: "zatiti connection setup complete"},
	{id: "connection.setup.status", visibility: "public", mode: "query", effect: "local",
		cli: "zatiti connection setup status"},
	{id: "connection.update", visibility: "public", mode: "mutation", effect: "local",
		submission: true, expectedVersion: true, cli: "zatiti connection update"},
	{id: "connection.validate", visibility: "public", mode: "mutation", effect: "external_read",
		submission: true, expectedVersion: true, completion: true, cli: "zatiti connection validate"},

	// tool.*
	{id: "tool.bind", visibility: "public", mode: "mutation", effect: "local",
		submission: true, cli: "zatiti tool bind"},
	{id: "tool.get", visibility: "public", mode: "query", effect: "local",
		cli: "zatiti tool get"},
	{id: "tool.list", visibility: "public", mode: "query", effect: "local",
		cli: "zatiti tool list"},
	{id: "tool.schema", visibility: "public", mode: "query", effect: "local",
		cli: "zatiti tool schema"},
	{id: "tool.unbind", visibility: "public", mode: "mutation", effect: "local",
		submission: true, cli: "zatiti tool unbind"},
}

// Service is the connections domain owner: trusted tool contracts,
// connection definitions, validation freshness and credential setup
// challenges.
type Service struct {
	clock       contract.Clock
	ids         contract.IDSource
	ports       contract.Ports
	secrets     contract.SecretStore
	catalog     map[string]contract.Descriptor
	descriptors []contract.Descriptor
}

// Compile-time proof that *Service implements the shared Module contract and
// the local IO seam the registry routes connection.setup.begin/complete/cancel
// through.
var (
	_ contract.Module  = (*Service)(nil)
	_ contract.LocalIO = (*Service)(nil)
)

// New constructs the connections owner. It never queries peers, touches
// storage or starts goroutines; all runtime coupling arrives through deps.
func New(deps contract.Dependencies) (*Service, error) {
	if deps.Clock == nil {
		return nil, fault(contract.CodeInternalError, "connections requires a clock")
	}
	if deps.IDs == nil {
		return nil, fault(contract.CodeInternalError, "connections requires an identity source")
	}
	if deps.Ports == nil {
		return nil, fault(contract.CodeInternalError, "connections requires owner ports")
	}
	s := &Service{
		clock:   deps.Clock,
		ids:     deps.IDs,
		ports:   deps.Ports,
		secrets: deps.Secrets,
		catalog: make(map[string]contract.Descriptor, len(opMetas)),
	}
	s.descriptors = buildDescriptors(s.catalog)
	return s, nil
}

// Name implements contract.Module.
func (s *Service) Name() string { return ownerName }

// Migrations implements contract.Module.
func (s *Service) Migrations() []contract.Migration { return connectionsMigrations() }

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
			Effect:           m.effect,
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
// connection.setup.begin/complete/cancel are local IO operations: the
// registry routes them through Prepare/Perform/Finish and never reaches
// Handle, so their entries refuse with the routing explanation.
var handlers = map[string]handlerFunc{
	"_connections.activate":          handleActivate,
	"_connections.resolve":           handleResolve,
	"_connections.validate":          handleValidate,
	"_connections.validation.record": handleValidationRecord,

	"connection.archive":        handleArchive,
	"connection.create":         handleCreate,
	"connection.get":            handleGet,
	"connection.list":           handleList,
	"connection.revoke":         handleRevoke,
	"connection.rotate":         handleRotate,
	"connection.setup.begin":    handleLocalIORouted,
	"connection.setup.cancel":   handleLocalIORouted,
	"connection.setup.complete": handleLocalIORouted,
	"connection.setup.status":   handleSetupStatus,
	"connection.update":         handleUpdate,
	"connection.validate":       handleValidatePublic,

	"tool.bind":   handleToolBind,
	"tool.get":    handleToolGet,
	"tool.list":   handleToolList,
	"tool.schema": handleToolSchema,
	"tool.unbind": handleToolUnbind,
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

// handleLocalIORouted refuses direct dispatch of local IO operations. The
// registry routes connection.setup.begin/complete/cancel exclusively through
// the Prepare/Perform/Finish seam; a Handle call means a dispatch defect.
func handleLocalIORouted(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	return contract.Payload{}, internalError(
		"operation %s is a local IO mutation and routes only through Prepare/Perform/Finish", inv.Operation)
}

// completed builds a completed payload carrying data.
func (s *Service) completed(data any) (contract.Payload, error) {
	raw, err := marshalData(data)
	if err != nil {
		return contract.Payload{}, err
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: raw}, nil
}

// completedWithCursor builds a completed payload with an optional next-page
// cursor; a nil cursor omits the field entirely.
func (s *Service) completedWithCursor(data any, next *string) (contract.Payload, error) {
	raw, err := marshalData(data)
	if err != nil {
		return contract.Payload{}, err
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: raw, NextCursor: next}, nil
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

// checkInstallation refuses inputs whose scope escapes the transaction
// scope. Every referenced object is revalidated against the transaction
// scope; missing scope is never an implicit global.
func checkInstallation(unit contract.Unit, in wireScope) *contract.Fault {
	if in.InstallationID != unit.Scope().InstallationID {
		return invalidInput("request scope installation %s does not match the transaction scope", in.InstallationID)
	}
	return nil
}
