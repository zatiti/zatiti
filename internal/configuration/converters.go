package configuration

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Definition converters: typed change definitions arrive as strict JSON and
// rows render back to the same wire shapes, so staged definitions, effective
// rows and revision lineage all round-trip through one set of types.

func decodeOrgDef(raw json.RawMessage) (wireOrganization, error) {
	var def wireOrganization
	if err := contract.DecodeStrict(raw, &def); err != nil {
		return def, invalidInput("organization definition decoding failed: %v", err)
	}
	return def, nil
}

// orgDef renders the effective organization row as its wire definition.
func orgDef(r *orgRow) wireOrganization {
	def := wireOrganization{
		ID: r.ID, Version: r.Version, Key: r.Key, Name: r.Name, ChiefID: r.ChiefID,
	}
	if r.ParentID != "" {
		parent := r.ParentID
		def.ParentID = &parent
	}
	if r.LimitsJSON != "" {
		var limits wireLimits
		if err := json.Unmarshal([]byte(r.LimitsJSON), &limits); err == nil {
			def.Limits = &limits
		}
	}
	if r.ExtensionsJSON != "" {
		def.Extensions = json.RawMessage(r.ExtensionsJSON)
	}
	return def
}

func decodeTeamDef(raw json.RawMessage) (wireTeam, error) {
	var def wireTeam
	if err := contract.DecodeStrict(raw, &def); err != nil {
		return def, invalidInput("team definition decoding failed: %v", err)
	}
	return def, nil
}

// teamDef renders the effective team row as its wire definition.
func teamDef(r *teamRow) wireTeam {
	def := wireTeam{
		ID: r.ID, Version: r.Version, OrganizationID: r.OrganizationID,
		Key: r.Key, Name: r.Name,
	}
	if r.WorkerIDsJSON != "" {
		var ids []contract.ID
		if err := json.Unmarshal([]byte(r.WorkerIDsJSON), &ids); err == nil {
			def.WorkerIDs = ids
		}
	}
	if def.WorkerIDs == nil {
		def.WorkerIDs = []contract.ID{}
	}
	if r.ExtensionsJSON != "" {
		def.Extensions = json.RawMessage(r.ExtensionsJSON)
	}
	return def
}

func decodeProjectDef(raw json.RawMessage) (wireProject, error) {
	var def wireProject
	if err := contract.DecodeStrict(raw, &def); err != nil {
		return def, invalidInput("project definition decoding failed: %v", err)
	}
	return def, nil
}

// projectDef renders the effective project row as its wire definition.
func projectDef(r *projectRow) wireProject {
	def := wireProject{
		ID: r.ID, Version: r.Version, OrganizationID: r.OrganizationID,
		Key: r.Key, Name: r.Name, Repositories: r.Repositories,
		Classification: r.Classification,
	}
	if r.BindingsJSON != "" {
		var ids []contract.ID
		if err := json.Unmarshal([]byte(r.BindingsJSON), &ids); err == nil {
			def.Bindings = ids
		}
	}
	if def.Bindings == nil {
		def.Bindings = []contract.ID{}
	}
	if len(def.Repositories) == 0 {
		def.Repositories = []string{}
	}
	if r.LimitsJSON != "" {
		var limits wireLimits
		if err := json.Unmarshal([]byte(r.LimitsJSON), &limits); err == nil {
			def.Limits = &limits
		}
	}
	if r.ExtensionsJSON != "" {
		def.Extensions = json.RawMessage(r.ExtensionsJSON)
	}
	return def
}

func decodeWorkerDef(raw json.RawMessage) (wireWorker, error) {
	var def wireWorker
	if err := contract.DecodeStrict(raw, &def); err != nil {
		return def, invalidInput("worker definition decoding failed: %v", err)
	}
	return def, nil
}

// workerDef renders the effective worker row as its wire definition.
func workerDef(r *workerRow) wireWorker {
	def := wireWorker{
		ID: r.ID, Version: r.Version, OrganizationID: r.OrganizationID,
		Key: r.Key, Name: r.Name, Purpose: r.Purpose, Instructions: r.Instructions,
		SkillVersions: r.SkillVersions,
	}
	if r.BindingsJSON != "" {
		var ids []contract.ID
		if err := json.Unmarshal([]byte(r.BindingsJSON), &ids); err == nil {
			def.Bindings = ids
		}
	}
	if def.Bindings == nil {
		def.Bindings = []contract.ID{}
	}
	if len(def.SkillVersions) == 0 {
		def.SkillVersions = []wireRef{}
	}
	if r.ProfileJSON != "" {
		var profile wireExecutionProfile
		if err := json.Unmarshal([]byte(r.ProfileJSON), &profile); err == nil {
			def.Profile = &profile
		}
	}
	if r.LimitsJSON != "" {
		var limits wireLimits
		if err := json.Unmarshal([]byte(r.LimitsJSON), &limits); err == nil {
			def.Limits = &limits
		}
	}
	if r.ExtensionsJSON != "" {
		def.Extensions = json.RawMessage(r.ExtensionsJSON)
	}
	return def
}

func decodeBindingDef(raw json.RawMessage) (wireBinding, error) {
	var def wireBinding
	if err := contract.DecodeStrict(raw, &def); err != nil {
		return def, invalidInput("binding definition decoding failed: %v", err)
	}
	return def, nil
}

// bindingDef renders the effective binding row as its wire definition.
func bindingDef(r *bindingRow) wireBinding {
	def := wireBinding{
		ID: r.ID, Version: r.Version, Kind: r.Kind, TargetID: r.TargetID,
		Permissions: r.Permissions, Destinations: r.Destinations,
	}
	if r.ScopeJSON != "" {
		var scope wireScope
		if err := json.Unmarshal([]byte(r.ScopeJSON), &scope); err == nil {
			def.Scope = scope
		}
	}
	if r.SourceScopeJSON != "" {
		var source wireScope
		if err := json.Unmarshal([]byte(r.SourceScopeJSON), &source); err == nil {
			def.SourceScope = &source
		}
	}
	if def.Permissions == nil {
		def.Permissions = []string{}
	}
	if len(def.Destinations) == 0 {
		def.Destinations = []string{}
	}
	return def
}

func decodeProfileDef(raw json.RawMessage) (wireExecutionProfile, error) {
	var def wireExecutionProfile
	if err := contract.DecodeStrict(raw, &def); err != nil {
		return def, invalidInput("execution profile definition decoding failed: %v", err)
	}
	return def, nil
}

// profileDef renders the effective execution-profile row as its wire
// definition.
func profileDef(r *profileRow) wireExecutionProfile {
	def := wireExecutionProfile{
		ID: r.ID, Version: r.Version, Executor: r.Executor, Model: r.Model,
		ConnectionID: r.ConnectionID, ProviderDestination: r.ProviderDestination,
		Capabilities: r.Capabilities, Classification: r.Classification,
		ContextCapture: r.ContextCapture,
	}
	if r.CostBoundJSON != "" {
		var money wireMoney
		if err := json.Unmarshal([]byte(r.CostBoundJSON), &money); err == nil {
			def.CostBound = money
		}
	}
	if def.Capabilities == nil {
		def.Capabilities = []string{}
	}
	return def
}

// profileOrNull serializes an optional profile for the nullable
// profile_json column; absent serializes to SQL NULL.
func profileOrNull(p *wireExecutionProfile) string {
	if p == nil {
		return ""
	}
	return marshalJSON(p)
}

// limitsOrNull serializes optional limits for the nullable limits_json
// column; absent serializes to SQL NULL.
func limitsOrNull(l *wireLimits) string {
	if l == nil {
		return ""
	}
	return marshalJSON(l)
}

// ---------- draft staging ----------

// appendChanges appends changes into an open draft, creating the draft on
// demand when no draft id was supplied. The new draft seals against the
// current head; the caller's transaction covers creation and append.
func (s *Service) appendChanges(ctx context.Context, unit contract.Unit, scope wireScope, draftID *contract.ID, changes []wireChange) (*draftRow, error) {
	var draft *draftRow
	if draftID != nil && *draftID != "" {
		var err error
		draft, err = loadDraft(ctx, unit, scope.InstallationID, *draftID)
		if err != nil {
			return nil, faultOf(err)
		}
		if draft == nil {
			return nil, notFound("draft %s not found", *draftID)
		}
		if draft.State != draftOpen {
			return nil, conflictFault("draft %s is %s and no longer accepts changes", draft.ID, draft.State)
		}
	} else {
		head, err := readHead(ctx, unit)
		if err != nil {
			return nil, faultOf(err)
		}
		now := s.clock.Now().UTC()
		draft = &draftRow{
			ID:             s.ids.New(),
			Version:        1,
			InstallationID: scope.InstallationID,
			OrganizationID: scope.OrganizationID,
			BaseRevision:   head,
			ChangesJSON:    "[]",
			State:          draftOpen,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := insertDraft(ctx, unit, draft); err != nil {
			return nil, err
		}
	}
	if err := s.appendDraftChanges(ctx, unit, draft, changes); err != nil {
		return nil, err
	}
	return draft, nil
}

// appendDraftChanges appends already schema-validated changes to the draft's
// staged list and advances the draft version. Complete typed definitions
// only; omission never deletes.
func (s *Service) appendDraftChanges(ctx context.Context, unit contract.Unit, draft *draftRow, changes []wireChange) error {
	existing, err := draftChanges(draft.ChangesJSON)
	if err != nil {
		return faultOf(err)
	}
	existing = append(existing, changes...)
	raw, err := marshalChanges(existing)
	if err != nil {
		return err
	}
	draft.ChangesJSON = raw
	draft.Version++
	draft.UpdatedAt = s.clock.Now().UTC()
	return updateDraftChanges(ctx, unit, draft)
}
