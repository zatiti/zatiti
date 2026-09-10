package configuration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

// Public operation handlers. Typed create/update/archive operations stage
// changes in drafts and never mutate effective configuration; queries read
// exact identities under the caller's installation; export/import move
// zatiti.organization/v1 portable definitions through the blob store. The
// compiler handlers live in compiler.go.

// ---------- shared input shapes ----------

// createIn is the wire input of every *.create operation with its
// kind-specific inline definition.
type createIn[D any] struct {
	Scope      wireScope    `json:"scope"`
	Definition D            `json:"definition"`
	DraftID    *contract.ID `json:"draft_id,omitempty"`
}

// updateIn is the wire input of every *.update operation.
type updateIn[D any] struct {
	Scope           wireScope    `json:"scope"`
	ID              contract.ID  `json:"id"`
	ExpectedVersion int64        `json:"expected_version"`
	Definition      D            `json:"definition"`
	DraftID         *contract.ID `json:"draft_id,omitempty"`
}

// archiveIn is the wire input of every *.archive operation.
type archiveIn struct {
	Scope           wireScope    `json:"scope"`
	ID              contract.ID  `json:"id"`
	ExpectedVersion int64        `json:"expected_version"`
	DraftID         *contract.ID `json:"draft_id,omitempty"`
}

// orgMoveIn is the wire input of organization.move.
type orgMoveIn struct {
	Scope           wireScope    `json:"scope"`
	ID              contract.ID  `json:"id"`
	ExpectedVersion int64        `json:"expected_version"`
	ParentID        contract.ID  `json:"parent_id"`
	DraftID         *contract.ID `json:"draft_id,omitempty"`
}

// workerMoveIn is the wire input of worker.move.
type workerMoveIn struct {
	Scope           wireScope    `json:"scope"`
	ID              contract.ID  `json:"id"`
	ExpectedVersion int64        `json:"expected_version"`
	OrganizationID  contract.ID  `json:"organization_id"`
	DraftID         *contract.ID `json:"draft_id,omitempty"`
}

// chiefReplaceIn is the wire input of organization.chief.replace.
type chiefReplaceIn struct {
	Scope           wireScope    `json:"scope"`
	ID              contract.ID  `json:"id"`
	ExpectedVersion int64        `json:"expected_version"`
	ChiefID         contract.ID  `json:"chief_id"`
	DraftID         *contract.ID `json:"draft_id,omitempty"`
}

// Kind-specific inline definitions, mirroring the embedded operation schemas
// exactly. None carries object identity; the handler allocates it.
type bindingDefIn struct {
	Scope        wireScope   `json:"scope"`
	Kind         string      `json:"kind"`
	TargetID     contract.ID `json:"target_id"`
	Permissions  []string    `json:"permissions"`
	SourceScope  *wireScope  `json:"source_scope,omitempty"`
	Destinations []string    `json:"destinations,omitempty"`
}

type profileDefIn struct {
	Executor            string      `json:"executor"`
	Model               string      `json:"model"`
	ConnectionID        contract.ID `json:"connection_id"`
	ProviderDestination string      `json:"provider_destination"`
	Capabilities        []string    `json:"capabilities"`
	CostBound           wireMoney   `json:"cost_bound"`
	Classification      string      `json:"classification"`
	ContextCapture      string      `json:"context_capture"`
}

type orgDefCreateIn struct {
	Key        string          `json:"key"`
	Name       string          `json:"name"`
	ParentID   *contract.ID    `json:"parent_id,omitempty"`
	Limits     *wireLimits     `json:"limits,omitempty"`
	Extensions json.RawMessage `json:"extensions,omitempty"`
}

type orgDefUpdateIn struct {
	Key        string          `json:"key"`
	Name       string          `json:"name"`
	ChiefID    contract.ID     `json:"chief_id"`
	ParentID   *contract.ID    `json:"parent_id,omitempty"`
	Limits     *wireLimits     `json:"limits,omitempty"`
	Extensions json.RawMessage `json:"extensions,omitempty"`
}

type chiefDefIn struct {
	Key           string                `json:"key"`
	Name          string                `json:"name"`
	Purpose       string                `json:"purpose"`
	Instructions  string                `json:"instructions"`
	SkillVersions []wireRef             `json:"skill_versions"`
	Bindings      []contract.ID         `json:"bindings"`
	Profile       *wireExecutionProfile `json:"profile"`
	Limits        *wireLimits           `json:"limits"`
	Extensions    json.RawMessage       `json:"extensions,omitempty"`
}

type teamDefIn struct {
	OrganizationID contract.ID     `json:"organization_id"`
	Key            string          `json:"key"`
	Name           string          `json:"name"`
	WorkerIDs      []contract.ID   `json:"worker_ids"`
	Extensions     json.RawMessage `json:"extensions,omitempty"`
}

type projectDefIn struct {
	OrganizationID contract.ID     `json:"organization_id"`
	Key            string          `json:"key"`
	Name           string          `json:"name"`
	Repositories   []string        `json:"repositories"`
	Bindings       []contract.ID   `json:"bindings"`
	Classification string          `json:"classification"`
	Limits         *wireLimits     `json:"limits,omitempty"`
	Extensions     json.RawMessage `json:"extensions,omitempty"`
}

type workerDefIn struct {
	OrganizationID contract.ID           `json:"organization_id"`
	Key            string                `json:"key"`
	Name           string                `json:"name"`
	Purpose        string                `json:"purpose"`
	Instructions   string                `json:"instructions"`
	SkillVersions  []wireRef             `json:"skill_versions"`
	Bindings       []contract.ID         `json:"bindings"`
	Profile        *wireExecutionProfile `json:"profile"`
	Limits         *wireLimits           `json:"limits"`
	Extensions     json.RawMessage       `json:"extensions,omitempty"`
}

// bootstrapIn is the wire input of _configuration.bootstrap.
type bootstrapIn struct {
	InstallationID contract.ID `json:"installation_id"`
	OwnerID        contract.ID `json:"owner_id"`
	OrganizationID contract.ID `json:"organization_id"`
	ChiefID        contract.ID `json:"chief_id"`
}

// snapshotIn is the wire input of _configuration.snapshot.
type snapshotIn struct {
	Scope wireScope `json:"scope"`
}

func stringsOrEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func idsOrEmpty(s []contract.ID) []contract.ID {
	if s == nil {
		return []contract.ID{}
	}
	return s
}

func refsOrEmpty(s []wireRef) []wireRef {
	if s == nil {
		return []wireRef{}
	}
	return s
}

// ---------- resource get ----------

// handleGetResource is the shared *.get boundary: resolve the exact identity
// under current authorization, never disclose cross-scope data.
func handleGetResource(kind string) handlerFunc {
	return func(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
		in, err := decodeInto[getInput](s, kind+".get", inv.Input)
		if err != nil {
			return contract.Payload{}, err
		}
		if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
			return contract.Payload{}, err
		}
		install := in.Scope.InstallationID
		switch kind {
		case kindOrganization:
			row, err := fetchOrgByID(ctx, unit, install, in.ID)
			if err != nil {
				return contract.Payload{}, faultOf(err)
			}
			if row == nil {
				return contract.Payload{}, notFound("organization %s not found", in.ID)
			}
			return s.completed(map[string]any{"resource": orgDef(row)})
		case kindTeam:
			row, err := fetchTeamByID(ctx, unit, install, in.ID)
			if err != nil {
				return contract.Payload{}, faultOf(err)
			}
			if row == nil {
				return contract.Payload{}, notFound("team %s not found", in.ID)
			}
			return s.completed(map[string]any{"resource": teamDef(row)})
		case kindProject:
			row, err := fetchProjectByID(ctx, unit, install, in.ID)
			if err != nil {
				return contract.Payload{}, faultOf(err)
			}
			if row == nil {
				return contract.Payload{}, notFound("project %s not found", in.ID)
			}
			return s.completed(map[string]any{"resource": projectDef(row)})
		case kindWorker:
			row, err := fetchWorkerByID(ctx, unit, install, in.ID)
			if err != nil {
				return contract.Payload{}, faultOf(err)
			}
			if row == nil {
				return contract.Payload{}, notFound("worker %s not found", in.ID)
			}
			return s.completed(map[string]any{"resource": workerDef(row)})
		case kindBinding:
			row, err := fetchBindingByID(ctx, unit, install, in.ID)
			if err != nil {
				return contract.Payload{}, faultOf(err)
			}
			if row == nil {
				return contract.Payload{}, notFound("binding %s not found", in.ID)
			}
			return s.completed(map[string]any{"resource": bindingDef(row)})
		case kindExecutionProfile:
			row, err := fetchProfileByID(ctx, unit, install, in.ID)
			if err != nil {
				return contract.Payload{}, faultOf(err)
			}
			if row == nil {
				return contract.Payload{}, notFound("execution profile %s not found", in.ID)
			}
			return s.completed(map[string]any{"resource": profileDef(row)})
		}
		return contract.Payload{}, invalidInput("unsupported resource kind %s", kind)
	}
}

// ---------- resource list ----------

// resourceFilter compiles the exact-match filter for one resource list.
// supports marks the filter fields the resource accepts; anything else
// refuses invalid_input before any SQL runs. The installation scope is the
// first and unconditional predicate.
func resourceFilter(install contract.ID, f *wireListFilter, table string, supports map[string]bool) ([]string, []any, []string, error) {
	where := []string{"installation_id = ?"}
	args := []any{install}
	fp := []string{table}
	if f == nil {
		return where, args, fp, nil
	}
	if f.WorkerID != "" || f.TaskID != "" || f.Descendants != nil || f.NeedsYou != nil {
		return nil, nil, nil, invalidInput("filter fields worker_id, task_id, descendants and needs_you are not supported for %s", table)
	}
	if f.State != "" {
		where = append(where, "state = ?")
		args = append(args, f.State)
		fp = append(fp, "state="+f.State)
	}
	if f.Key != "" {
		if !supports["key"] {
			return nil, nil, nil, invalidInput("filter key is not supported for %s", table)
		}
		where = append(where, "key = ?")
		args = append(args, f.Key)
		fp = append(fp, "key="+f.Key)
	}
	if f.ParentID != "" {
		if !supports["parent_id"] {
			return nil, nil, nil, invalidInput("filter parent_id is not supported for %s", table)
		}
		where = append(where, "parent_id = ?")
		args = append(args, f.ParentID)
		fp = append(fp, "parent_id="+string(f.ParentID))
	}
	if f.OrganizationID != "" {
		if !supports["organization_id"] {
			return nil, nil, nil, invalidInput("filter organization_id is not supported for %s", table)
		}
		where = append(where, "organization_id = ?")
		args = append(args, f.OrganizationID)
		fp = append(fp, "org="+string(f.OrganizationID))
	}
	return where, args, fp, nil
}

// listSkeleton runs the paged query skeleton shared by every list operation:
// exact-match WHERE, stable ordering, limit+1 lookahead and authenticated
// next-page cursors.
func (s *Service) listSkeleton(ctx context.Context, unit contract.Unit, in *listInput, op, table, columns string,
	where []string, args []any, fingerprint []string, scan func(row rowScanner) (any, error)) ([]any, *string, error) {
	offset, err := decodeCursor(unit, op, in.Cursor, fingerprintOf(fingerprint...))
	if err != nil {
		return nil, nil, err
	}
	limit := limitOf(in.Limit)
	qargs := append(args, limit+1, offset)
	rows, err := unit.QueryContext(ctx,
		"SELECT "+columns+" FROM "+table+" WHERE "+strings.Join(where, " AND ")+
			" ORDER BY created_at, id LIMIT ? OFFSET ?", qargs...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]any, 0, limit)
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		next = nextCursor(unit, op, fingerprintOf(fingerprint...), offset+int64(limit))
	}
	return items, next, nil
}

// handleListResource is the shared *.list boundary: scope and filter before
// pagination, parameterized SQL only, cursors bound to principal and query.
func handleListResource(kind string) handlerFunc {
	return func(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
		in, err := decodeInto[listInput](s, kind+".list", inv.Input)
		if err != nil {
			return contract.Payload{}, err
		}
		if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
			return contract.Payload{}, err
		}
		install := in.Scope.InstallationID
		switch kind {
		case kindOrganization:
			where, args, fp, err := resourceFilter(install, in.Filter, "configuration_organizations",
				map[string]bool{"key": true, "parent_id": true})
			if err != nil {
				return contract.Payload{}, err
			}
			raws, next, err := s.listSkeleton(ctx, unit, in, inv.Operation, "configuration_organizations", orgColumns,
				where, args, fp, func(row rowScanner) (any, error) { return scanOrg(row) })
			if err != nil {
				return contract.Payload{}, err
			}
			items := make([]wireOrganization, 0, len(raws))
			for _, r := range raws {
				items = append(items, orgDef(r.(*orgRow)))
			}
			return s.listBody(items, next)
		case kindTeam:
			where, args, fp, err := resourceFilter(install, in.Filter, "configuration_teams",
				map[string]bool{"key": true, "organization_id": true})
			if err != nil {
				return contract.Payload{}, err
			}
			raws, next, err := s.listSkeleton(ctx, unit, in, inv.Operation, "configuration_teams", teamColumns,
				where, args, fp, func(row rowScanner) (any, error) { return scanTeam(row) })
			if err != nil {
				return contract.Payload{}, err
			}
			items := make([]wireTeam, 0, len(raws))
			for _, r := range raws {
				items = append(items, teamDef(r.(*teamRow)))
			}
			return s.listBody(items, next)
		case kindProject:
			where, args, fp, err := resourceFilter(install, in.Filter, "configuration_projects",
				map[string]bool{"key": true, "organization_id": true})
			if err != nil {
				return contract.Payload{}, err
			}
			raws, next, err := s.listSkeleton(ctx, unit, in, inv.Operation, "configuration_projects", projectColumns,
				where, args, fp, func(row rowScanner) (any, error) { return scanProject(row) })
			if err != nil {
				return contract.Payload{}, err
			}
			items := make([]wireProject, 0, len(raws))
			for _, r := range raws {
				items = append(items, projectDef(r.(*projectRow)))
			}
			return s.listBody(items, next)
		case kindWorker:
			where, args, fp, err := resourceFilter(install, in.Filter, "configuration_workers",
				map[string]bool{"key": true, "organization_id": true})
			if err != nil {
				return contract.Payload{}, err
			}
			raws, next, err := s.listSkeleton(ctx, unit, in, inv.Operation, "configuration_workers", workerColumns,
				where, args, fp, func(row rowScanner) (any, error) { return scanWorker(row) })
			if err != nil {
				return contract.Payload{}, err
			}
			items := make([]wireWorker, 0, len(raws))
			for _, r := range raws {
				items = append(items, workerDef(r.(*workerRow)))
			}
			return s.listBody(items, next)
		case kindBinding:
			where, args, fp, err := resourceFilter(install, in.Filter, "configuration_bindings", map[string]bool{})
			if err != nil {
				return contract.Payload{}, err
			}
			raws, next, err := s.listSkeleton(ctx, unit, in, inv.Operation, "configuration_bindings", bindingColumns,
				where, args, fp, func(row rowScanner) (any, error) { return scanBinding(row) })
			if err != nil {
				return contract.Payload{}, err
			}
			items := make([]wireBinding, 0, len(raws))
			for _, r := range raws {
				items = append(items, bindingDef(r.(*bindingRow)))
			}
			return s.listBody(items, next)
		case kindExecutionProfile:
			where, args, fp, err := resourceFilter(install, in.Filter, "configuration_execution_profiles", map[string]bool{})
			if err != nil {
				return contract.Payload{}, err
			}
			raws, next, err := s.listSkeleton(ctx, unit, in, inv.Operation, "configuration_execution_profiles", profileColumns,
				where, args, fp, func(row rowScanner) (any, error) { return scanProfile(row) })
			if err != nil {
				return contract.Payload{}, err
			}
			items := make([]wireExecutionProfile, 0, len(raws))
			for _, r := range raws {
				items = append(items, profileDef(r.(*profileRow)))
			}
			return s.listBody(items, next)
		}
		return contract.Payload{}, invalidInput("unsupported resource kind %s", kind)
	}
}

// Selected columns per owned table, shared by point lookups and lists.
const (
	orgColumns     = "id, version, installation_id, key, name, chief_id, parent_id, limits_json, extensions_json, state, created_at, updated_at"
	teamColumns    = "id, version, installation_id, organization_id, key, name, worker_ids_json, extensions_json, state, created_at, updated_at"
	projectColumns = "id, version, installation_id, organization_id, key, name, repositories_json, bindings_json, classification, limits_json, extensions_json, state, created_at, updated_at"
	workerColumns  = "id, version, installation_id, organization_id, key, name, purpose, instructions, skill_versions_json, bindings_json, profile_json, limits_json, extensions_json, state, created_at, updated_at"
	bindingColumns = "id, version, installation_id, scope_json, kind, target_id, permissions_json, source_scope_json, destinations_json, state, created_at, updated_at"
	profileColumns = "id, version, installation_id, executor, model, connection_id, provider_destination, capabilities_json, cost_bound_json, classification, context_capture, state, created_at, updated_at"
)

// ---------- typed create / update / archive ----------

// createResource builds the {draft, resource} payload for a staged create:
// the draft with its appended changes plus the minted resource identity.
func (s *Service) createResourcePayload(draft *draftRow, resource any) (contract.Payload, error) {
	changes, err := draftChanges(draft.ChangesJSON)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	return s.completed(map[string]any{"draft": draftBody(draft, changes), "resource": resource})
}

// handleCreateResource stages a typed create: it allocates identity once,
// validates the composed full definition against the Change schema, and
// appends the change into an open draft. Effective state is untouched.
func handleCreateResource(kind string) handlerFunc {
	return func(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
		var change wireChange
		var resource any
		var scope wireScope
		var draftID *contract.ID
		switch kind {
		case kindBinding:
			in, err := decodeInto[createIn[bindingDefIn]](s, kind+".create", inv.Input)
			if err != nil {
				return contract.Payload{}, err
			}
			if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
				return contract.Payload{}, err
			}
			def := wireBinding{
				ID: s.ids.New(), Version: 1, Scope: in.Definition.Scope, Kind: in.Definition.Kind,
				TargetID: in.Definition.TargetID, Permissions: stringsOrEmpty(in.Definition.Permissions),
				SourceScope: in.Definition.SourceScope, Destinations: stringsOrEmpty(in.Definition.Destinations),
			}
			change = wireChange{Kind: kind, Action: actionCreate, ID: def.ID, Definition: rawDef(def)}
			resource, scope, draftID = def, in.Scope, in.DraftID
		case kindExecutionProfile:
			in, err := decodeInto[createIn[profileDefIn]](s, kind+".create", inv.Input)
			if err != nil {
				return contract.Payload{}, err
			}
			if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
				return contract.Payload{}, err
			}
			def := wireExecutionProfile{
				ID: s.ids.New(), Version: 1, Executor: in.Definition.Executor, Model: in.Definition.Model,
				ConnectionID: in.Definition.ConnectionID, ProviderDestination: in.Definition.ProviderDestination,
				Capabilities: stringsOrEmpty(in.Definition.Capabilities), CostBound: in.Definition.CostBound,
				Classification: in.Definition.Classification, ContextCapture: in.Definition.ContextCapture,
			}
			change = wireChange{Kind: kind, Action: actionCreate, ID: def.ID, Definition: rawDef(def)}
			resource, scope, draftID = def, in.Scope, in.DraftID
		case kindProject:
			in, err := decodeInto[createIn[projectDefIn]](s, kind+".create", inv.Input)
			if err != nil {
				return contract.Payload{}, err
			}
			if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
				return contract.Payload{}, err
			}
			def := wireProject{
				ID: s.ids.New(), Version: 1, OrganizationID: in.Definition.OrganizationID,
				Key: in.Definition.Key, Name: in.Definition.Name,
				Repositories: stringsOrEmpty(in.Definition.Repositories), Bindings: idsOrEmpty(in.Definition.Bindings),
				Classification: in.Definition.Classification, Limits: in.Definition.Limits,
				Extensions: in.Definition.Extensions,
			}
			change = wireChange{Kind: kind, Action: actionCreate, ID: def.ID, Definition: rawDef(def)}
			resource, scope, draftID = def, in.Scope, in.DraftID
		case kindTeam:
			in, err := decodeInto[createIn[teamDefIn]](s, kind+".create", inv.Input)
			if err != nil {
				return contract.Payload{}, err
			}
			if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
				return contract.Payload{}, err
			}
			def := wireTeam{
				ID: s.ids.New(), Version: 1, OrganizationID: in.Definition.OrganizationID,
				Key: in.Definition.Key, Name: in.Definition.Name,
				WorkerIDs: idsOrEmpty(in.Definition.WorkerIDs), Extensions: in.Definition.Extensions,
			}
			change = wireChange{Kind: kind, Action: actionCreate, ID: def.ID, Definition: rawDef(def)}
			resource, scope, draftID = def, in.Scope, in.DraftID
		case kindWorker:
			in, err := decodeInto[createIn[workerDefIn]](s, kind+".create", inv.Input)
			if err != nil {
				return contract.Payload{}, err
			}
			if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
				return contract.Payload{}, err
			}
			def := wireWorker{
				ID: s.ids.New(), Version: 1, OrganizationID: in.Definition.OrganizationID,
				Key: in.Definition.Key, Name: in.Definition.Name, Purpose: in.Definition.Purpose,
				Instructions: in.Definition.Instructions, SkillVersions: refsOrEmpty(in.Definition.SkillVersions),
				Bindings: idsOrEmpty(in.Definition.Bindings), Profile: in.Definition.Profile,
				Limits: in.Definition.Limits, Extensions: in.Definition.Extensions,
			}
			change = wireChange{Kind: kind, Action: actionCreate, ID: def.ID, Definition: rawDef(def)}
			resource, scope, draftID = def, in.Scope, in.DraftID
		default:
			return contract.Payload{}, invalidInput("unsupported resource kind %s", kind)
		}
		if err := validateChangeDef(change); err != nil {
			return contract.Payload{}, err
		}
		draft, err := s.appendChanges(ctx, unit, scope, draftID, []wireChange{change})
		if err != nil {
			return contract.Payload{}, err
		}
		return s.createResourcePayload(draft, resource)
	}
}

// handleUpdateResource stages a typed update as a complete definition: the
// change carries the object identity, the caller's expected version and the
// projected post-apply version inside the definition. Omission never deletes.
func handleUpdateResource(kind string) handlerFunc {
	return func(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
		var change wireChange
		var resource any
		var scope wireScope
		var draftID *contract.ID
		switch kind {
		case kindBinding:
			in, err := decodeInto[updateIn[bindingDefIn]](s, kind+".update", inv.Input)
			if err != nil {
				return contract.Payload{}, err
			}
			if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
				return contract.Payload{}, err
			}
			def := wireBinding{
				ID: in.ID, Version: in.ExpectedVersion + 1, Scope: in.Definition.Scope, Kind: in.Definition.Kind,
				TargetID: in.Definition.TargetID, Permissions: stringsOrEmpty(in.Definition.Permissions),
				SourceScope: in.Definition.SourceScope, Destinations: stringsOrEmpty(in.Definition.Destinations),
			}
			change = wireChange{Kind: kind, Action: actionUpdate, ID: in.ID,
				ExpectedVersion: in.ExpectedVersion, Definition: rawDef(def)}
			resource, scope, draftID = def, in.Scope, in.DraftID
		case kindExecutionProfile:
			in, err := decodeInto[updateIn[profileDefIn]](s, kind+".update", inv.Input)
			if err != nil {
				return contract.Payload{}, err
			}
			if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
				return contract.Payload{}, err
			}
			def := wireExecutionProfile{
				ID: in.ID, Version: in.ExpectedVersion + 1, Executor: in.Definition.Executor,
				Model: in.Definition.Model, ConnectionID: in.Definition.ConnectionID,
				ProviderDestination: in.Definition.ProviderDestination,
				Capabilities:        stringsOrEmpty(in.Definition.Capabilities), CostBound: in.Definition.CostBound,
				Classification: in.Definition.Classification, ContextCapture: in.Definition.ContextCapture,
			}
			change = wireChange{Kind: kind, Action: actionUpdate, ID: in.ID,
				ExpectedVersion: in.ExpectedVersion, Definition: rawDef(def)}
			resource, scope, draftID = def, in.Scope, in.DraftID
		case kindOrganization:
			in, err := decodeInto[updateIn[orgDefUpdateIn]](s, kind+".update", inv.Input)
			if err != nil {
				return contract.Payload{}, err
			}
			if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
				return contract.Payload{}, err
			}
			def := wireOrganization{
				ID: in.ID, Version: in.ExpectedVersion + 1, Key: in.Definition.Key,
				Name: in.Definition.Name, ChiefID: in.Definition.ChiefID, ParentID: in.Definition.ParentID,
				Limits: in.Definition.Limits, Extensions: in.Definition.Extensions,
			}
			change = wireChange{Kind: kind, Action: actionUpdate, ID: in.ID,
				ExpectedVersion: in.ExpectedVersion, Definition: rawDef(def)}
			resource, scope, draftID = def, in.Scope, in.DraftID
		case kindProject:
			in, err := decodeInto[updateIn[projectDefIn]](s, kind+".update", inv.Input)
			if err != nil {
				return contract.Payload{}, err
			}
			if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
				return contract.Payload{}, err
			}
			def := wireProject{
				ID: in.ID, Version: in.ExpectedVersion + 1, OrganizationID: in.Definition.OrganizationID,
				Key: in.Definition.Key, Name: in.Definition.Name,
				Repositories: stringsOrEmpty(in.Definition.Repositories), Bindings: idsOrEmpty(in.Definition.Bindings),
				Classification: in.Definition.Classification, Limits: in.Definition.Limits,
				Extensions: in.Definition.Extensions,
			}
			change = wireChange{Kind: kind, Action: actionUpdate, ID: in.ID,
				ExpectedVersion: in.ExpectedVersion, Definition: rawDef(def)}
			resource, scope, draftID = def, in.Scope, in.DraftID
		case kindTeam:
			in, err := decodeInto[updateIn[teamDefIn]](s, kind+".update", inv.Input)
			if err != nil {
				return contract.Payload{}, err
			}
			if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
				return contract.Payload{}, err
			}
			def := wireTeam{
				ID: in.ID, Version: in.ExpectedVersion + 1, OrganizationID: in.Definition.OrganizationID,
				Key: in.Definition.Key, Name: in.Definition.Name,
				WorkerIDs: idsOrEmpty(in.Definition.WorkerIDs), Extensions: in.Definition.Extensions,
			}
			change = wireChange{Kind: kind, Action: actionUpdate, ID: in.ID,
				ExpectedVersion: in.ExpectedVersion, Definition: rawDef(def)}
			resource, scope, draftID = def, in.Scope, in.DraftID
		case kindWorker:
			in, err := decodeInto[updateIn[workerDefIn]](s, kind+".update", inv.Input)
			if err != nil {
				return contract.Payload{}, err
			}
			if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
				return contract.Payload{}, err
			}
			def := wireWorker{
				ID: in.ID, Version: in.ExpectedVersion + 1, OrganizationID: in.Definition.OrganizationID,
				Key: in.Definition.Key, Name: in.Definition.Name, Purpose: in.Definition.Purpose,
				Instructions: in.Definition.Instructions, SkillVersions: refsOrEmpty(in.Definition.SkillVersions),
				Bindings: idsOrEmpty(in.Definition.Bindings), Profile: in.Definition.Profile,
				Limits: in.Definition.Limits, Extensions: in.Definition.Extensions,
			}
			change = wireChange{Kind: kind, Action: actionUpdate, ID: in.ID,
				ExpectedVersion: in.ExpectedVersion, Definition: rawDef(def)}
			resource, scope, draftID = def, in.Scope, in.DraftID
		default:
			return contract.Payload{}, invalidInput("unsupported resource kind %s", kind)
		}
		if err := validateChangeDef(change); err != nil {
			return contract.Payload{}, err
		}
		draft, err := s.appendChanges(ctx, unit, scope, draftID, []wireChange{change})
		if err != nil {
			return contract.Payload{}, err
		}
		return s.createResourcePayload(draft, resource)
	}
}

// handleArchive stages a typed archive of the current definition: explicit
// checks of retained work and obligations happen at plan and apply time; the
// staged change preserves the definition verbatim with the projected version.
func handleArchive(kind string) handlerFunc {
	return func(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
		in, err := decodeInto[archiveIn](s, kind+".archive", inv.Input)
		if err != nil {
			return contract.Payload{}, err
		}
		if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
			return contract.Payload{}, err
		}
		install := in.Scope.InstallationID
		var change wireChange
		var resource any
		switch kind {
		case kindOrganization:
			row, err := fetchOrgByID(ctx, unit, install, in.ID)
			if err != nil {
				return contract.Payload{}, faultOf(err)
			}
			if row == nil {
				return contract.Payload{}, notFound("organization %s not found", in.ID)
			}
			def := orgDef(row)
			def.Version = in.ExpectedVersion + 1
			change = wireChange{Kind: kind, Action: actionArchive, ID: in.ID,
				ExpectedVersion: in.ExpectedVersion, Definition: rawDef(def)}
			resource = def
		case kindTeam:
			row, err := fetchTeamByID(ctx, unit, install, in.ID)
			if err != nil {
				return contract.Payload{}, faultOf(err)
			}
			if row == nil {
				return contract.Payload{}, notFound("team %s not found", in.ID)
			}
			def := teamDef(row)
			def.Version = in.ExpectedVersion + 1
			change = wireChange{Kind: kind, Action: actionArchive, ID: in.ID,
				ExpectedVersion: in.ExpectedVersion, Definition: rawDef(def)}
			resource = def
		case kindProject:
			row, err := fetchProjectByID(ctx, unit, install, in.ID)
			if err != nil {
				return contract.Payload{}, faultOf(err)
			}
			if row == nil {
				return contract.Payload{}, notFound("project %s not found", in.ID)
			}
			def := projectDef(row)
			def.Version = in.ExpectedVersion + 1
			change = wireChange{Kind: kind, Action: actionArchive, ID: in.ID,
				ExpectedVersion: in.ExpectedVersion, Definition: rawDef(def)}
			resource = def
		case kindWorker:
			row, err := fetchWorkerByID(ctx, unit, install, in.ID)
			if err != nil {
				return contract.Payload{}, faultOf(err)
			}
			if row == nil {
				return contract.Payload{}, notFound("worker %s not found", in.ID)
			}
			def := workerDef(row)
			def.Version = in.ExpectedVersion + 1
			change = wireChange{Kind: kind, Action: actionArchive, ID: in.ID,
				ExpectedVersion: in.ExpectedVersion, Definition: rawDef(def)}
			resource = def
		case kindBinding:
			row, err := fetchBindingByID(ctx, unit, install, in.ID)
			if err != nil {
				return contract.Payload{}, faultOf(err)
			}
			if row == nil {
				return contract.Payload{}, notFound("binding %s not found", in.ID)
			}
			def := bindingDef(row)
			def.Version = in.ExpectedVersion + 1
			change = wireChange{Kind: kind, Action: actionArchive, ID: in.ID,
				ExpectedVersion: in.ExpectedVersion, Definition: rawDef(def)}
			resource = def
		case kindExecutionProfile:
			row, err := fetchProfileByID(ctx, unit, install, in.ID)
			if err != nil {
				return contract.Payload{}, faultOf(err)
			}
			if row == nil {
				return contract.Payload{}, notFound("execution profile %s not found", in.ID)
			}
			def := profileDef(row)
			def.Version = in.ExpectedVersion + 1
			change = wireChange{Kind: kind, Action: actionArchive, ID: in.ID,
				ExpectedVersion: in.ExpectedVersion, Definition: rawDef(def)}
			resource = def
		default:
			return contract.Payload{}, invalidInput("unsupported resource kind %s", kind)
		}
		if err := validateChangeDef(change); err != nil {
			return contract.Payload{}, err
		}
		draft, err := s.appendChanges(ctx, unit, in.Scope, in.DraftID, []wireChange{change})
		if err != nil {
			return contract.Payload{}, err
		}
		return s.createResourcePayload(draft, resource)
	}
}

// ---------- moves and chief replacement ----------

// stageResourceUpdate fetches the live row, projects the staged definition at
// the post-apply version and stages one update change. Moves recheck cycles
// and inherited authority at plan and apply time; staging itself never
// mutates effective state.
func (s *Service) stageResourceUpdate(ctx context.Context, unit contract.Unit, kind string,
	scope wireScope, id contract.ID, expectedVersion int64, mutate func(row any), draftID *contract.ID) (*draftRow, any, error) {
	install := scope.InstallationID
	switch kind {
	case kindOrganization:
		row, err := fetchOrgByID(ctx, unit, install, id)
		if err != nil {
			return nil, nil, err
		}
		if row == nil {
			return nil, nil, notFound("organization %s not found", id)
		}
		def := orgDef(row)
		mutate(&def)
		def.Version = expectedVersion + 1
		change := wireChange{Kind: kind, Action: actionUpdate, ID: id,
			ExpectedVersion: expectedVersion, Definition: rawDef(def)}
		if err := validateChangeDef(change); err != nil {
			return nil, nil, err
		}
		draft, err := s.appendChanges(ctx, unit, scope, draftID, []wireChange{change})
		return draft, def, err
	case kindWorker:
		row, err := fetchWorkerByID(ctx, unit, install, id)
		if err != nil {
			return nil, nil, err
		}
		if row == nil {
			return nil, nil, notFound("worker %s not found", id)
		}
		def := workerDef(row)
		mutate(&def)
		def.Version = expectedVersion + 1
		change := wireChange{Kind: kind, Action: actionUpdate, ID: id,
			ExpectedVersion: expectedVersion, Definition: rawDef(def)}
		if err := validateChangeDef(change); err != nil {
			return nil, nil, err
		}
		draft, err := s.appendChanges(ctx, unit, scope, draftID, []wireChange{change})
		return draft, def, err
	}
	return nil, nil, invalidInput("unsupported resource kind %s", kind)
}

// handleMoveOrganization stages reparenting under a new parent; ancestry
// cycles are refused at validation and re-proven at apply.
func handleMoveOrganization(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[orgMoveIn](s, "organization.move", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	draft, _, err := s.stageResourceUpdate(ctx, unit, kindOrganization, in.Scope, in.ID, in.ExpectedVersion,
		func(row any) { row.(*wireOrganization).ParentID = &in.ParentID }, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return s.draftResourceBody(draft)
}

// handleMoveWorker stages a worker's move to a new home organization.
func handleMoveWorker(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[workerMoveIn](s, "worker.move", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	draft, _, err := s.stageResourceUpdate(ctx, unit, kindWorker, in.Scope, in.ID, in.ExpectedVersion,
		func(row any) { row.(*wireWorker).OrganizationID = in.OrganizationID }, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return s.draftResourceBody(draft)
}

// handleChiefReplace stages the explicit atomic chief replacement; the
// organization keeps its identity, memory, obligations and history.
func handleChiefReplace(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[chiefReplaceIn](s, "organization.chief.replace", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	draft, _, err := s.stageResourceUpdate(ctx, unit, kindOrganization, in.Scope, in.ID, in.ExpectedVersion,
		func(row any) { row.(*wireOrganization).ChiefID = in.ChiefID }, in.DraftID)
	if err != nil {
		return contract.Payload{}, err
	}
	return s.draftResourceBody(draft)
}

// ---------- organization.create ----------

// handleOrganizationCreate allocates the organization and its designated
// chief together and stages both creates in one bundle so the atomic
// chief-creation invariant holds at apply.
func handleOrganizationCreate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	// organization.create reuses createIn's definition field plus chief; the
	// composed input schema carries both, so decode through the wider shape.
	wide, err := decodeInto[struct {
		Scope      wireScope      `json:"scope"`
		Definition orgDefCreateIn `json:"definition"`
		DraftID    *contract.ID   `json:"draft_id,omitempty"`
		Chief      chiefDefIn     `json:"chief"`
	}](s, "organization.create", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.checkInstallation(unit, wide.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	orgID := s.ids.New()
	chiefID := s.ids.New()
	orgDef := wireOrganization{
		ID: orgID, Version: 1, Key: wide.Definition.Key, Name: wide.Definition.Name,
		ChiefID: chiefID, ParentID: wide.Definition.ParentID, Limits: wide.Definition.Limits,
		Extensions: wide.Definition.Extensions,
	}
	chiefDef := wireWorker{
		ID: chiefID, Version: 1, OrganizationID: orgID, Key: wide.Chief.Key, Name: wide.Chief.Name,
		Purpose: wide.Chief.Purpose, Instructions: wide.Chief.Instructions,
		SkillVersions: refsOrEmpty(wide.Chief.SkillVersions), Bindings: idsOrEmpty(wide.Chief.Bindings),
		Profile: wide.Chief.Profile, Limits: wide.Chief.Limits, Extensions: wide.Chief.Extensions,
	}
	orgChange := wireChange{Kind: kindOrganization, Action: actionCreate, ID: orgID, Definition: rawDef(orgDef)}
	chiefChange := wireChange{Kind: kindWorker, Action: actionCreate, ID: chiefID, Definition: rawDef(chiefDef)}
	if err := validateChangeDef(orgChange); err != nil {
		return contract.Payload{}, err
	}
	if err := validateChangeDef(chiefChange); err != nil {
		return contract.Payload{}, err
	}
	draft, err := s.appendChanges(ctx, unit, wide.Scope, wide.DraftID, []wireChange{orgChange, chiefChange})
	if err != nil {
		return contract.Payload{}, err
	}
	return s.createResourcePayload(draft, orgDef)
}

// ---------- bootstrap ----------

// handleBootstrap atomically initializes the root organization and its
// minimal non-executable chief: null profile and limits mean the chief can
// never run paid work until an operator configures a provider and limits.
// Bootstrap initializes the head to revision 1; the root is not rollbackable
// through revision lineage.
func handleBootstrap(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[bootstrapIn](s, "_configuration.bootstrap", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.checkInstallation(unit, in.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	var n int
	if err := unit.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM configuration_organizations WHERE installation_id = ?", in.InstallationID).Scan(&n); err != nil {
		return contract.Payload{}, err
	}
	if n > 0 {
		return contract.Payload{}, conflictFault("installation %s is already bootstrapped", in.InstallationID)
	}
	now := s.clock.Now().UTC()
	org := &orgRow{
		ID: in.OrganizationID, Version: 1, InstallationID: in.InstallationID,
		Key: "personal", Name: "Personal", ChiefID: in.ChiefID,
		State: stateActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := insertOrg(ctx, unit, org); err != nil {
		return contract.Payload{}, err
	}
	chief := &workerRow{
		ID: in.ChiefID, Version: 1, InstallationID: in.InstallationID,
		OrganizationID: in.OrganizationID, Key: "chief", Name: "Chief",
		Purpose:       "Personal chief of the root organization",
		SkillVersions: []wireRef{}, BindingsJSON: "[]",
		State: stateActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := insertWorker(ctx, unit, chief); err != nil {
		return contract.Payload{}, err
	}
	if err := bumpHead(ctx, unit, 1); err != nil {
		return contract.Payload{}, err
	}
	return s.completed(map[string]any{"organization": orgDef(org), "chief": workerDef(chief)})
}

// ---------- snapshot ----------

// handleSnapshot reads current ancestry, effective bindings, worker/project
// and revision for one scope. No automatic descendant private data access.
func handleSnapshot(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[snapshotIn](s, "_configuration.snapshot", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	install := in.Scope.InstallationID
	revision, err := readHead(ctx, unit)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	var worker *wireWorker
	if in.Scope.WorkerID != "" {
		row, err := fetchWorkerByID(ctx, unit, install, in.Scope.WorkerID)
		if err != nil {
			return contract.Payload{}, faultOf(err)
		}
		if row != nil {
			def := workerDef(row)
			worker = &def
		}
	}
	var project *wireProject
	if in.Scope.ProjectID != "" {
		row, err := fetchProjectByID(ctx, unit, install, in.Scope.ProjectID)
		if err != nil {
			return contract.Payload{}, faultOf(err)
		}
		if row != nil {
			def := projectDef(row)
			project = &def
		}
	}
	// The ancestry base is the most specific organization in the scope.
	baseOrg := in.Scope.OrganizationID
	if baseOrg == "" && project != nil {
		baseOrg = project.OrganizationID
	}
	if baseOrg == "" && worker != nil {
		baseOrg = worker.OrganizationID
	}
	ancestors, err := s.organizationAncestry(ctx, unit, install, baseOrg)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	bindings, err := s.effectiveBindings(ctx, unit, install, in.Scope, ancestors)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	return s.completed(map[string]any{"resource": wireScopeSnapshot{
		Scope:     in.Scope,
		Revision:  revision,
		Ancestors: ancestors,
		Bindings:  bindings,
		Worker:    worker,
		Project:   project,
	}})
}

// organizationAncestry walks the parent chain from the base organization to
// the root, nearest first. A missing ancestor ends the walk without failing:
// the snapshot reports the reachable ancestry.
func (s *Service) organizationAncestry(ctx context.Context, unit contract.Unit, install, orgID contract.ID) ([]wireOrganization, error) {
	chain := make([]wireOrganization, 0, 4)
	cur := orgID
	seen := make(map[contract.ID]bool)
	for cur != "" && !seen[cur] {
		seen[cur] = true
		row, err := fetchOrgByID(ctx, unit, install, cur)
		if err != nil {
			return nil, err
		}
		if row == nil {
			break
		}
		chain = append(chain, orgDef(row))
		cur = row.ParentID
	}
	return chain, nil
}

// effectiveBindings returns the active bindings whose scope is satisfied by
// the requested scope: installation always, organization empty or anywhere in
// the ancestry chain, project/worker/task empty or exact.
func (s *Service) effectiveBindings(ctx context.Context, unit contract.Unit, install contract.ID, scope wireScope, ancestors []wireOrganization) ([]wireBinding, error) {
	rows, err := unit.QueryContext(ctx,
		"SELECT "+bindingColumns+" FROM configuration_bindings WHERE installation_id = ? AND state = ?",
		install, stateActive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	orgIDs := make(map[contract.ID]bool, len(ancestors))
	for _, a := range ancestors {
		orgIDs[a.ID] = true
	}
	out := make([]wireBinding, 0, 4)
	for rows.Next() {
		row, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		var bScope wireScope
		if row.ScopeJSON != "" {
			if err := json.Unmarshal([]byte(row.ScopeJSON), &bScope); err != nil {
				return nil, err
			}
		}
		if bScope.InstallationID != "" && bScope.InstallationID != install {
			continue
		}
		if bScope.OrganizationID != "" && !orgIDs[bScope.OrganizationID] {
			continue
		}
		if bScope.ProjectID != "" && bScope.ProjectID != scope.ProjectID {
			continue
		}
		if bScope.WorkerID != "" && bScope.WorkerID != scope.WorkerID {
			continue
		}
		if bScope.TaskID != "" && bScope.TaskID != scope.TaskID {
			continue
		}
		out = append(out, bindingDef(row))
	}
	return out, rows.Err()
}

// ---------- export and import ----------

// exportBundle is the zatiti.organization/v1 portable definition bundle.
// Definitions are exported as stored: no secrets, credentials or runtime
// history ever live in configuration rows, so none can leak; opaque
// credential references (connection ids) travel verbatim and require
// explicit destination rebinding on import.
type exportBundle struct {
	Format       string                 `json:"format"`
	Organization *wireOrganization      `json:"organization,omitempty"`
	Teams        []wireTeam             `json:"teams,omitempty"`
	Projects     []wireProject          `json:"projects,omitempty"`
	Workers      []wireWorker           `json:"workers,omitempty"`
	Bindings     []wireBinding          `json:"bindings,omitempty"`
	Profiles     []wireExecutionProfile `json:"execution_profiles,omitempty"`
}

const exportFormat = "zatiti.organization/v1"

// handleExport creates the bounded artifact job for one resource. The bundle
// is canonical JSON staged and published through the blob store; the job
// carries the result artifact reference for job.get completion.
func handleExport(kind string) handlerFunc {
	return func(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
		in, err := decodeInto[getInput](s, kind+".export", inv.Input)
		if err != nil {
			return contract.Payload{}, err
		}
		if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
			return contract.Payload{}, err
		}
		if s.blobs == nil {
			return contract.Payload{}, internalError("export requires a blob store")
		}
		install := in.Scope.InstallationID
		bundle := &exportBundle{Format: exportFormat}
		switch kind {
		case kindOrganization:
			row, err := fetchOrgByID(ctx, unit, install, in.ID)
			if err != nil {
				return contract.Payload{}, faultOf(err)
			}
			if row == nil {
				return contract.Payload{}, notFound("organization %s not found", in.ID)
			}
			bundle.Organization = ptrOf(orgDef(row))
			if err := s.exportOrganizationScope(ctx, unit, install, in.ID, bundle); err != nil {
				return contract.Payload{}, err
			}
		case kindTeam:
			row, err := fetchTeamByID(ctx, unit, install, in.ID)
			if err != nil {
				return contract.Payload{}, faultOf(err)
			}
			if row == nil {
				return contract.Payload{}, notFound("team %s not found", in.ID)
			}
			bundle.Teams = []wireTeam{teamDef(row)}
		case kindProject:
			row, err := fetchProjectByID(ctx, unit, install, in.ID)
			if err != nil {
				return contract.Payload{}, faultOf(err)
			}
			if row == nil {
				return contract.Payload{}, notFound("project %s not found", in.ID)
			}
			bundle.Projects = []wireProject{projectDef(row)}
		default:
			return contract.Payload{}, invalidInput("unsupported export kind %s", kind)
		}
		raw, err := json.Marshal(bundle)
		if err != nil {
			return contract.Payload{}, internalError("export bundle encoding failed")
		}
		canon, err := contract.Canonicalize(raw)
		if err != nil {
			return contract.Payload{}, invalidInput("export bundle is not canonicalizable: %v", err)
		}
		stagingRef, digest, _, err := s.blobs.Stage(ctx, bytes.NewReader(canon), int64(len(canon)))
		if err != nil {
			return contract.Payload{}, err
		}
		if err := s.blobs.Publish(ctx, stagingRef, digest); err != nil {
			return contract.Payload{}, err
		}
		job := wireJob{
			ID:           s.ids.New(),
			Version:      1,
			Kind:         "export",
			State:        "succeeded",
			Requirements: []wireRequirement{},
			ResultArtifact: &wireArtifactRef{
				ID:     s.ids.New(),
				Digest: digest,
			},
			Owner:     ownerName,
			Operation: kind + ".export",
		}
		return s.completed(map[string]any{"job": job})
	}
}

// exportOrganizationScope fills the bundle with every active team, project,
// worker, binding and profile scoped to the organization.
func (s *Service) exportOrganizationScope(ctx context.Context, unit contract.Unit, install, orgID contract.ID, bundle *exportBundle) error {
	teams, err := unit.QueryContext(ctx,
		"SELECT "+teamColumns+" FROM configuration_teams WHERE installation_id = ? AND organization_id = ? AND state = ? ORDER BY created_at, id",
		install, orgID, stateActive)
	if err != nil {
		return err
	}
	defer func() { _ = teams.Close() }()
	for teams.Next() {
		row, err := scanTeam(teams)
		if err != nil {
			return err
		}
		bundle.Teams = append(bundle.Teams, teamDef(row))
	}
	if err := teams.Err(); err != nil {
		return err
	}
	projects, err := unit.QueryContext(ctx,
		"SELECT "+projectColumns+" FROM configuration_projects WHERE installation_id = ? AND organization_id = ? AND state = ? ORDER BY created_at, id",
		install, orgID, stateActive)
	if err != nil {
		return err
	}
	defer func() { _ = projects.Close() }()
	for projects.Next() {
		row, err := scanProject(projects)
		if err != nil {
			return err
		}
		bundle.Projects = append(bundle.Projects, projectDef(row))
	}
	if err := projects.Err(); err != nil {
		return err
	}
	workers, err := unit.QueryContext(ctx,
		"SELECT "+workerColumns+" FROM configuration_workers WHERE installation_id = ? AND organization_id = ? AND state = ? ORDER BY created_at, id",
		install, orgID, stateActive)
	if err != nil {
		return err
	}
	defer func() { _ = workers.Close() }()
	for workers.Next() {
		row, err := scanWorker(workers)
		if err != nil {
			return err
		}
		bundle.Workers = append(bundle.Workers, workerDef(row))
	}
	if err := workers.Err(); err != nil {
		return err
	}
	return s.exportOrgBindings(ctx, unit, install, orgID, bundle)
}

// exportOrgScopeRows scans org-scoped bindings and the profiles embedded in
// the bundle's workers.
func (s *Service) exportOrgBindings(ctx context.Context, unit contract.Unit, install, orgID contract.ID, bundle *exportBundle) error {
	rows, err := unit.QueryContext(ctx,
		"SELECT "+bindingColumns+" FROM configuration_bindings WHERE installation_id = ? AND state = ? ORDER BY created_at, id",
		install, stateActive)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		row, err := scanBinding(rows)
		if err != nil {
			return err
		}
		var scope wireScope
		if row.ScopeJSON != "" {
			if err := json.Unmarshal([]byte(row.ScopeJSON), &scope); err != nil {
				return err
			}
		}
		if scope.OrganizationID == orgID {
			bundle.Bindings = append(bundle.Bindings, bindingDef(row))
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, w := range bundle.Workers {
		if w.Profile != nil {
			bundle.Profiles = append(bundle.Profiles, *w.Profile)
		}
	}
	return nil
}

func ptrOf[T any](v T) *T { return &v }

// importRebinding is one opaque-credential-ref rebinding instruction.
type importRebinding struct {
	SourceRef      string `json:"source_ref"`
	DestinationRef string `json:"destination_ref"`
}

// handleImport validates the portable bundle (schema, ids, references,
// rebinding, canonical digest), applies rebinding to opaque credential refs
// and stages a draft of create changes. Cross-installation import never
// transfers live authority: every definition stages as a fresh create that
// must pass full validation.
func handleImport(kind string) handlerFunc {
	return func(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
		in, err := decodeInto[struct {
			Scope      wireScope         `json:"scope"`
			Artifact   wireArtifactRef   `json:"artifact"`
			Rebindings []importRebinding `json:"rebindings"`
			DraftID    *contract.ID      `json:"draft_id,omitempty"`
		}](s, kind+".import", inv.Input)
		if err != nil {
			return contract.Payload{}, err
		}
		if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
			return contract.Payload{}, err
		}
		if s.blobs == nil {
			return contract.Payload{}, internalError("import requires a blob store")
		}
		rc, err := s.blobs.Open(ctx, in.Artifact.Digest, 0, -1)
		if err != nil {
			return contract.Payload{}, faultOf(err)
		}
		defer func() { _ = rc.Close() }()
		raw, err := io.ReadAll(rc)
		if err != nil {
			return contract.Payload{}, faultOf(err)
		}
		if contract.Hash(raw) != in.Artifact.Digest {
			return contract.Payload{}, conflictFault("artifact content does not match its digest")
		}
		var bundle exportBundle
		if err := contract.DecodeStrict(raw, &bundle); err != nil {
			return contract.Payload{}, invalidInput("artifact is not a %s bundle: %v", exportFormat, err)
		}
		if bundle.Format != exportFormat {
			return contract.Payload{}, invalidInput("artifact format %q is not %s", bundle.Format, exportFormat)
		}
		rebind := make(map[string]string, len(in.Rebindings))
		for _, r := range in.Rebindings {
			rebind[r.SourceRef] = r.DestinationRef
		}
		changes, diagnostics, err := bundle.changes(kind, rebind)
		if err != nil {
			return contract.Payload{}, err
		}
		for i := range changes {
			if err := validateChangeDef(changes[i]); err != nil {
				return contract.Payload{}, err
			}
		}
		draft, err := s.appendChanges(ctx, unit, in.Scope, in.DraftID, changes)
		if err != nil {
			return contract.Payload{}, err
		}
		outChanges, err := draftChanges(draft.ChangesJSON)
		if err != nil {
			return contract.Payload{}, faultOf(err)
		}
		return s.completed(map[string]any{
			"draft":       draftBody(draft, outChanges),
			"diagnostics": diagnostics,
		})
	}
}

// bundleChanges converts the portable bundle into staged create changes for
// the requested resource kind, applying opaque-credential rebinding. An
// opaque ref without an explicit destination rebinding aborts staging: the
// caller must bind every credential reference before import.
func (b *exportBundle) changes(kind string, rebind map[string]string) ([]wireChange, []wireDiagnostic, error) {
	diagnostics := []wireDiagnostic{}
	mk := func(kindName string, def any) wireChange {
		raw := rawDef(def)
		var id struct {
			ID contract.ID `json:"id"`
		}
		_ = json.Unmarshal(raw, &id)
		return wireChange{Kind: kindName, Action: actionCreate, ID: id.ID, Definition: raw}
	}
	rebound := func(source string) (string, bool) {
		dest, ok := rebind[source]
		if !ok {
			return "", false
		}
		diagnostics = append(diagnostics, infoDiagnostic("connection_id", "rebound",
			"opaque credential reference "+source+" rebound to "+dest))
		return dest, true
	}
	var changes []wireChange
	switch kind {
	case kindOrganization:
		if b.Organization != nil {
			changes = append(changes, mk(kindOrganization, *b.Organization))
		}
		for i := range b.Teams {
			changes = append(changes, mk(kindTeam, b.Teams[i]))
		}
		for i := range b.Projects {
			changes = append(changes, mk(kindProject, b.Projects[i]))
		}
		for i := range b.Workers {
			if err := rebindWorkerCredentialRefs(&b.Workers[i], rebound); err != nil {
				return nil, nil, err
			}
			changes = append(changes, mk(kindWorker, b.Workers[i]))
		}
		for i := range b.Bindings {
			changes = append(changes, mk(kindBinding, b.Bindings[i]))
		}
	case kindTeam:
		for i := range b.Teams {
			changes = append(changes, mk(kindTeam, b.Teams[i]))
		}
		for i := range b.Workers {
			if err := rebindWorkerCredentialRefs(&b.Workers[i], rebound); err != nil {
				return nil, nil, err
			}
			changes = append(changes, mk(kindWorker, b.Workers[i]))
		}
	case kindProject:
		for i := range b.Projects {
			changes = append(changes, mk(kindProject, b.Projects[i]))
		}
	}
	if len(changes) == 0 {
		return nil, nil, invalidInput("the artifact carries no definitions for %s", kind)
	}
	return changes, diagnostics, nil
}

// rebindWorkerCredentialRefs applies destination rebinding to the opaque
// credential refs inside an embedded profile; a connection id absent from
// the rebinding map refuses the import. The destination reference replaces
// the source one in the staged definition.
func rebindWorkerCredentialRefs(w *wireWorker, rebound func(string) (string, bool)) error {
	if w.Profile == nil || string(w.Profile.ConnectionID) == "" {
		return nil
	}
	dest, ok := rebound(string(w.Profile.ConnectionID))
	if !ok {
		return invalidInput("opaque credential reference %s requires explicit destination rebinding",
			w.Profile.ConnectionID)
	}
	w.Profile.ConnectionID = contract.ID(dest)
	return nil
}

// ---------- plan and revision reads ----------

// handlePlanGet implements configuration.plan.get.
func handlePlanGet(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[getInput](s, "configuration.plan.get", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	plan, err := loadPlan(ctx, unit, in.Scope.InstallationID, in.ID)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if plan == nil {
		return contract.Payload{}, notFound("plan %s not found", in.ID)
	}
	return s.planBody(plan)
}

// handleListPlans implements configuration.plan.list.
func handleListPlans(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[listInput](s, "configuration.plan.list", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	rows, next, err := listPlanRows(ctx, unit, in, inv.Operation)
	if err != nil {
		return contract.Payload{}, err
	}
	items := make([]wirePlan, 0, len(rows))
	for _, p := range rows {
		changes, err := draftChanges(p.ChangesJSON)
		if err != nil {
			return contract.Payload{}, faultOf(err)
		}
		raws := make([]json.RawMessage, 0, len(changes))
		for _, c := range changes {
			raws = append(raws, marshalChangeRaw(c))
		}
		items = append(items, wirePlan{
			ID:                    p.ID,
			Version:               p.Version,
			DraftID:               p.DraftID,
			BaseRevision:          p.BaseRevision,
			CandidateDigest:       p.CandidateDigest,
			Changes:               raws,
			Dependencies:          p.Dependencies,
			CompilerVersion:       p.CompilerVersion,
			SchemaVersion:         p.SchemaVersion,
			AuthorityRequirements: p.AuthorityReqs,
			Decisions:             p.Decisions,
			Diagnostics:           p.Diagnostics,
			Requirements:          p.Requirements,
		})
	}
	return s.listBody(items, next)
}

// handleRevisionGet implements configuration.revision.get.
func handleRevisionGet(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[getInput](s, "configuration.revision.get", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	rev, err := loadRevision(ctx, unit, in.Scope.InstallationID, in.ID)
	if err != nil {
		return contract.Payload{}, faultOf(err)
	}
	if rev == nil {
		return contract.Payload{}, notFound("revision %s not found", in.ID)
	}
	return s.revisionBody(rev)
}

// handleListRevisions implements configuration.revision.list.
func handleListRevisions(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[listInput](s, "configuration.revision.list", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	rows, next, err := listRevisionRows(ctx, unit, in, inv.Operation)
	if err != nil {
		return contract.Payload{}, err
	}
	items := make([]wireRevision, 0, len(rows))
	for _, r := range rows {
		items = append(items, wireRevision{
			ID:              r.ID,
			Version:         r.Version,
			PlanID:          r.PlanID,
			CandidateDigest: r.CandidateDigest,
			ActivatedAt:     r.ActivatedAt,
		})
	}
	return s.listBody(items, next)
}
