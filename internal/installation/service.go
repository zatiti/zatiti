package installation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

// Operation identifiers.
const (
	opBackup             = "installation.backup"
	opDoctor             = "installation.doctor"
	opInit               = "installation.init"
	opJobGet             = "installation.job.get"
	opMaintenanceEnter   = "installation.maintenance.enter"
	opPause              = "installation.pause"
	opRestore            = "installation.restore"
	opResume             = "installation.resume"
	opStatus             = "installation.status"
	opVerifierList       = "installation.verifier.list"
	opRestoreRecordName  = "_installation.restore.record"
	opRestoreOverlayName = "_installation.restore.overlay"
)

// localIOOps is the frozen set of operations this package serves through
// Prepare/Perform/Finish instead of Handle.
var localIOOps = map[string]bool{
	opInit:    true,
	opBackup:  true,
	opRestore: true,
}

// descriptorVersion is the single contract version every descriptor serves.
const descriptorVersion int64 = 1

// opMeta is the static registration record for one operation.
type opMeta struct {
	id         string
	visibility string
	mode       string
	submission bool
	expected   bool
	callers    []string
	cli        []string // nil for internal operations
}

// opMetas lists every owned operation exactly as the implementation brief
// embeds it: the internal operation first, then the public catalog.
var opMetas = []opMeta{
	{id: opRestoreOverlayName, visibility: contract.VisibilityInternal, mode: contract.ModeQuery,
		callers: []string{"controller"}},
	{id: opRestoreRecordName, visibility: contract.VisibilityInternal, mode: contract.ModeMutation,
		callers: []string{"controller"}},

	{id: opBackup, visibility: contract.VisibilityPublic, mode: contract.ModeMutation,
		submission: true, cli: []string{"installation", "backup"}},
	{id: opDoctor, visibility: contract.VisibilityPublic, mode: contract.ModeQuery,
		cli: []string{"installation", "doctor"}},
	{id: opInit, visibility: contract.VisibilityPublic, mode: contract.ModeMutation,
		cli: []string{"init"}},
	{id: opJobGet, visibility: contract.VisibilityPublic, mode: contract.ModeQuery,
		cli: []string{"installation", "job", "get"}},
	{id: opMaintenanceEnter, visibility: contract.VisibilityPublic, mode: contract.ModeMutation,
		submission: true, expected: true, cli: []string{"installation", "maintenance", "enter"}},
	{id: opPause, visibility: contract.VisibilityPublic, mode: contract.ModeMutation,
		submission: true, expected: true, cli: []string{"installation", "pause"}},
	{id: opRestore, visibility: contract.VisibilityPublic, mode: contract.ModeMutation,
		submission: true, expected: true, cli: []string{"installation", "restore"}},
	{id: opResume, visibility: contract.VisibilityPublic, mode: contract.ModeMutation,
		submission: true, expected: true, cli: []string{"installation", "resume"}},
	{id: opStatus, visibility: contract.VisibilityPublic, mode: contract.ModeQuery,
		cli: []string{"installation", "status"}},
	{id: opVerifierList, visibility: contract.VisibilityPublic, mode: contract.ModeQuery,
		cli: []string{"installation", "verifier", "list"}},
}

// Service is the installation domain owner: bootstrap, restriction and
// maintenance state, health, encrypted backup and paused restore jobs.
type Service struct {
	deps        contract.Dependencies
	catalog     map[string]contract.Descriptor
	descriptors []contract.Descriptor
	// backup is the consistent-backup capability entrypoint assembly binds
	// with WithDatabaseBackup. It is called only from LocalIO.Perform, never
	// inside a Unit, and is never type-asserted to anything wider.
	backup contract.DatabaseBackup
}

// Option configures New beyond the common domain dependencies.
type Option func(*Service)

// WithDatabaseBackup binds the consistent SQLite backup capability. Without
// it installation.backup and installation.restore fail prerequisite_missing
// naming the capability; every other operation is unaffected and doctor
// reports the gap.
func WithDatabaseBackup(backup contract.DatabaseBackup) Option {
	return func(s *Service) { s.backup = backup }
}

// Compile-time proof that *Service implements the shared Module contract and
// the LocalIO seam for installation.init/backup/restore.
var (
	_ contract.Module  = (*Service)(nil)
	_ contract.LocalIO = (*Service)(nil)
)

// New constructs the installation owner. It never queries peers, touches
// storage or starts goroutines; all runtime coupling arrives through deps.
// Clock and an identity source are always required. Secrets is required for
// bootstrap to custody the owner credential; Ports is required for every
// operation that composes peer owners (bootstrap, maintenance, backup,
// restore) but is checked at call time so a Ports-less construction still
// serves doctor/status/job.get against purely local state. Options bind the
// capabilities the common Dependencies deliberately do not carry.
func New(deps contract.Dependencies, opts ...Option) (*Service, error) {
	if deps.Clock == nil {
		return nil, internalError("installation requires a clock")
	}
	if deps.IDs == nil {
		return nil, internalError("installation requires an identity source")
	}
	s := &Service{
		deps:    deps,
		catalog: make(map[string]contract.Descriptor, len(opMetas)),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	descs, err := s.buildDescriptors()
	if err != nil {
		return nil, err
	}
	s.descriptors = descs
	return s, nil
}

// Name implements contract.Module.
func (s *Service) Name() string { return owner }

// Migrations implements contract.Module.
func (s *Service) Migrations() []contract.Migration { return migrations() }

// Descriptors implements contract.Module.
func (s *Service) Descriptors() []contract.Descriptor { return s.descriptors }

// buildDescriptors composes the descriptor list, merging each operation's
// embedded schema body into the shared $defs document so document-local
// $refs resolve, and fills the lookup catalog.
func (s *Service) buildDescriptors() ([]contract.Descriptor, error) {
	out := make([]contract.Descriptor, 0, len(opMetas))
	for _, m := range opMetas {
		sch, ok := wireSchemas[m.id]
		if !ok {
			return nil, fmt.Errorf("installation: operation %s has no embedded schema", m.id)
		}
		inputSchema, err := mergeSchema(sch.Input)
		if err != nil {
			return nil, fmt.Errorf("installation: operation %s input schema: %w", m.id, err)
		}
		outputSchema, err := mergeSchema(sch.Output)
		if err != nil {
			return nil, fmt.Errorf("installation: operation %s output schema: %w", m.id, err)
		}
		d := contract.Descriptor{
			ID:              m.id,
			Version:         descriptorVersion,
			Owner:           owner,
			Visibility:      m.visibility,
			Mode:            m.mode,
			Effect:          contract.EffectLocal,
			InputSchema:     inputSchema,
			OutputSchema:    outputSchema,
			Callers:         m.callers,
			ExpectedVersion: m.expected,
			SubmissionKey:   m.submission,
		}
		if completion, ok := completionSchemas[m.id]; ok {
			merged, err := mergeSchema(completion)
			if err != nil {
				return nil, fmt.Errorf("installation: operation %s completion schema: %w", m.id, err)
			}
			d.CompletionSchema = merged
		}
		if m.visibility == contract.VisibilityPublic {
			// installation.init runs before any installation exists, so the
			// frozen catalog keeps it scope-free.
			if m.id != opInit {
				d.ScopeRequired = []string{"installation_id"}
			}
			d.CLI = m.cli
			d.MCP = "zatiti_" + strings.ReplaceAll(m.id, ".", "_")
		}
		s.catalog[m.id] = d
		out = append(out, d)
	}
	return out, nil
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

// handlerFunc executes one operation inside the caller's unit.
type handlerFunc func(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error)

// handlers is the strict dispatch table. installation.init/backup/restore
// are absent: they are served through Prepare/Perform/Finish, and Handle
// refuses them rather than silently running their mutation phases here.
var handlers = map[string]handlerFunc{
	opRestoreOverlayName: handleRestoreOverlay,
	opRestoreRecordName:  handleRestoreRecord,
	opDoctor:             handleStatus,
	opStatus:             handleStatus,
	opJobGet:             handleJobGet,
	opMaintenanceEnter:   handleMaintenanceEnter,
	opPause:              handlePause,
	opResume:             handleResume,
	opVerifierList:       handleVerifierList,
}

// Handle implements contract.Module with strict dispatch: the operation must
// be registered, its version must match, mutations require a write
// transaction, and its embedded schema validates the input before strict
// decoding.
func (s *Service) Handle(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	d, ok := s.catalog[inv.Operation]
	if !ok {
		return contract.Payload{}, invalidInput("unknown operation %q", inv.Operation)
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
	if err := contract.ValidateSchema(d.InputSchema, inv.Input); err != nil {
		return contract.Payload{}, invalidInput("operation %s input rejected: %v", inv.Operation, err)
	}
	h, ok := handlers[inv.Operation]
	if !ok {
		return contract.Payload{}, internalError("operation %s has no handler", inv.Operation)
	}
	return h(ctx, s, unit, inv)
}

// decodeInto validates raw against the operation's merged input schema, then
// strict-decodes it into T.
func decodeInto[T any](s *Service, op string, raw json.RawMessage) (T, error) {
	var zero T
	d, ok := s.catalog[op]
	if !ok {
		return zero, internalError("operation %s is not registered", op)
	}
	if err := contract.ValidateSchema(d.InputSchema, raw); err != nil {
		return zero, invalidInput("input does not match the %s schema: %v", op, err)
	}
	var in T
	if err := contract.DecodeStrict(raw, &in); err != nil {
		return zero, invalidInput("input decoding failed for %s: %v", op, err)
	}
	return in, nil
}

// completed marshals a successful result body into a completed payload.
func completed(data any) (contract.Payload, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("installation: encode result: %w", err)
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: raw}, nil
}

// checkInstallation enforces that an operation addresses the caller's own
// installation.
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
