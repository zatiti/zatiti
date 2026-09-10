package configuration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Effective-store access: point lookups, versioned writes and the draft,
// plan and revision lineage. Every query is installation-scoped; fetches
// return (nil, nil) when the row does not exist. Timestamps serialize as
// RFC3339Nano TEXT, matching the migration columns.

// Draft and plan lifecycle states.
const (
	draftOpen      = "open"
	draftDiscarded = "discarded"
	planSealed     = "sealed"
	planApplied    = "applied"
)

// installOf returns the authenticated installation of the caller's unit.
func (s *Service) installOf(_ context.Context, unit contract.Unit) contract.ID {
	return unit.Scope().InstallationID
}

// fetchOrgByID loads one organization row.
func fetchOrgByID(ctx context.Context, unit contract.Unit, install, id contract.ID) (*orgRow, error) {
	return scanOrg(unit.QueryRowContext(ctx,
		"SELECT id, version, installation_id, key, name, chief_id, parent_id, limits_json, extensions_json, state, created_at, updated_at "+
			"FROM configuration_organizations WHERE installation_id = ? AND id = ?", install, id))
}

// fetchOrgByKey loads one organization by its installation-scoped key.
func fetchOrgByKey(ctx context.Context, unit contract.Unit, install contract.ID, key string) (*orgRow, error) {
	return scanOrg(unit.QueryRowContext(ctx,
		"SELECT id, version, installation_id, key, name, chief_id, parent_id, limits_json, extensions_json, state, created_at, updated_at "+
			"FROM configuration_organizations WHERE installation_id = ? AND key = ?", install, key))
}

// fetchTeamByID loads one team row.
func fetchTeamByID(ctx context.Context, unit contract.Unit, install, id contract.ID) (*teamRow, error) {
	return scanTeam(unit.QueryRowContext(ctx,
		"SELECT id, version, installation_id, organization_id, key, name, worker_ids_json, extensions_json, state, created_at, updated_at "+
			"FROM configuration_teams WHERE installation_id = ? AND id = ?", install, id))
}

// fetchTeamByKey loads one team by its organization-scoped key.
func fetchTeamByKey(ctx context.Context, unit contract.Unit, install, org contract.ID, key string) (*teamRow, error) {
	return scanTeam(unit.QueryRowContext(ctx,
		"SELECT id, version, installation_id, organization_id, key, name, worker_ids_json, extensions_json, state, created_at, updated_at "+
			"FROM configuration_teams WHERE installation_id = ? AND organization_id = ? AND key = ?", install, org, key))
}

// fetchProjectByID loads one project row.
func fetchProjectByID(ctx context.Context, unit contract.Unit, install, id contract.ID) (*projectRow, error) {
	return scanProject(unit.QueryRowContext(ctx,
		"SELECT id, version, installation_id, organization_id, key, name, repositories_json, bindings_json, classification, limits_json, extensions_json, state, created_at, updated_at "+
			"FROM configuration_projects WHERE installation_id = ? AND id = ?", install, id))
}

// fetchProjectByKey loads one project by its organization-scoped key.
func fetchProjectByKey(ctx context.Context, unit contract.Unit, install, org contract.ID, key string) (*projectRow, error) {
	return scanProject(unit.QueryRowContext(ctx,
		"SELECT id, version, installation_id, organization_id, key, name, repositories_json, bindings_json, classification, limits_json, extensions_json, state, created_at, updated_at "+
			"FROM configuration_projects WHERE installation_id = ? AND organization_id = ? AND key = ?", install, org, key))
}

// fetchWorkerByID loads one worker row.
func fetchWorkerByID(ctx context.Context, unit contract.Unit, install, id contract.ID) (*workerRow, error) {
	return scanWorker(unit.QueryRowContext(ctx,
		"SELECT id, version, installation_id, organization_id, key, name, purpose, instructions, skill_versions_json, bindings_json, profile_json, limits_json, extensions_json, state, created_at, updated_at "+
			"FROM configuration_workers WHERE installation_id = ? AND id = ?", install, id))
}

// fetchWorkerByKey loads one worker by its organization-scoped key.
func fetchWorkerByKey(ctx context.Context, unit contract.Unit, install, org contract.ID, key string) (*workerRow, error) {
	return scanWorker(unit.QueryRowContext(ctx,
		"SELECT id, version, installation_id, organization_id, key, name, purpose, instructions, skill_versions_json, bindings_json, profile_json, limits_json, extensions_json, state, created_at, updated_at "+
			"FROM configuration_workers WHERE installation_id = ? AND organization_id = ? AND key = ?", install, org, key))
}

// fetchBindingByID loads one binding row.
func fetchBindingByID(ctx context.Context, unit contract.Unit, install, id contract.ID) (*bindingRow, error) {
	return scanBinding(unit.QueryRowContext(ctx,
		"SELECT id, version, installation_id, scope_json, kind, target_id, permissions_json, source_scope_json, destinations_json, state, created_at, updated_at "+
			"FROM configuration_bindings WHERE installation_id = ? AND id = ?", install, id))
}

// fetchProfileByID loads one execution-profile row.
func fetchProfileByID(ctx context.Context, unit contract.Unit, install, id contract.ID) (*profileRow, error) {
	return scanProfile(unit.QueryRowContext(ctx,
		"SELECT id, version, installation_id, executor, model, connection_id, provider_destination, capabilities_json, cost_bound_json, classification, context_capture, state, created_at, updated_at "+
			"FROM configuration_execution_profiles WHERE installation_id = ? AND id = ?", install, id))
}

// ---------- effective writes ----------

// insertOrg inserts a new organization at version 1.
func insertOrg(ctx context.Context, unit contract.Unit, r *orgRow) error {
	_, err := unit.ExecContext(ctx,
		"INSERT INTO configuration_organizations (id, version, installation_id, key, name, chief_id, parent_id, limits_json, extensions_json, state, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.ID, r.Version, r.InstallationID, r.Key, r.Name, r.ChiefID,
		textOrNull(string(r.ParentID)), jsonOrNull(r.LimitsJSON), jsonOrNull(r.ExtensionsJSON), r.State,
		r.CreatedAt.Format(timeLayout), r.UpdatedAt.Format(timeLayout))
	return err
}

// writeOrg rewrites an organization row after a version advance.
func writeOrg(ctx context.Context, unit contract.Unit, r *orgRow) error {
	_, err := unit.ExecContext(ctx,
		"UPDATE configuration_organizations SET version = ?, key = ?, name = ?, chief_id = ?, parent_id = ?, limits_json = ?, extensions_json = ?, state = ?, updated_at = ? WHERE id = ? AND installation_id = ?",
		r.Version, r.Key, r.Name, r.ChiefID, textOrNull(string(r.ParentID)),
		jsonOrNull(r.LimitsJSON), jsonOrNull(r.ExtensionsJSON), r.State,
		r.UpdatedAt.Format(timeLayout), r.ID, r.InstallationID)
	return err
}

// deleteOrg removes an organization row.
func deleteOrg(ctx context.Context, unit contract.Unit, id contract.ID) error {
	_, err := unit.ExecContext(ctx, "DELETE FROM configuration_organizations WHERE id = ?", id)
	return err
}

// insertTeam inserts a new team at version 1.
func insertTeam(ctx context.Context, unit contract.Unit, r *teamRow) error {
	_, err := unit.ExecContext(ctx,
		"INSERT INTO configuration_teams (id, version, installation_id, organization_id, key, name, worker_ids_json, extensions_json, state, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.ID, r.Version, r.InstallationID, r.OrganizationID, r.Key, r.Name,
		r.WorkerIDsJSON, jsonOrNull(r.ExtensionsJSON), r.State,
		r.CreatedAt.Format(timeLayout), r.UpdatedAt.Format(timeLayout))
	return err
}

// writeTeam rewrites a team row after a version advance.
func writeTeam(ctx context.Context, unit contract.Unit, r *teamRow) error {
	_, err := unit.ExecContext(ctx,
		"UPDATE configuration_teams SET version = ?, organization_id = ?, key = ?, name = ?, worker_ids_json = ?, extensions_json = ?, state = ?, updated_at = ? WHERE id = ? AND installation_id = ?",
		r.Version, r.OrganizationID, r.Key, r.Name, r.WorkerIDsJSON,
		jsonOrNull(r.ExtensionsJSON), r.State, r.UpdatedAt.Format(timeLayout), r.ID, r.InstallationID)
	return err
}

// deleteTeam removes a team row.
func deleteTeam(ctx context.Context, unit contract.Unit, id contract.ID) error {
	_, err := unit.ExecContext(ctx, "DELETE FROM configuration_teams WHERE id = ?", id)
	return err
}

// insertProject inserts a new project at version 1.
func insertProject(ctx context.Context, unit contract.Unit, r *projectRow) error {
	repositories := marshalJSON(r.Repositories)
	_, err := unit.ExecContext(ctx,
		"INSERT INTO configuration_projects (id, version, installation_id, organization_id, key, name, repositories_json, bindings_json, classification, limits_json, extensions_json, state, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.ID, r.Version, r.InstallationID, r.OrganizationID, r.Key, r.Name,
		jsonOrArray(repositories), r.BindingsJSON, r.Classification,
		jsonOrNull(r.LimitsJSON), jsonOrNull(r.ExtensionsJSON), r.State,
		r.CreatedAt.Format(timeLayout), r.UpdatedAt.Format(timeLayout))
	return err
}

// writeProject rewrites a project row after a version advance.
func writeProject(ctx context.Context, unit contract.Unit, r *projectRow) error {
	repositories := marshalJSON(r.Repositories)
	_, err := unit.ExecContext(ctx,
		"UPDATE configuration_projects SET version = ?, organization_id = ?, key = ?, name = ?, repositories_json = ?, bindings_json = ?, classification = ?, limits_json = ?, extensions_json = ?, state = ?, updated_at = ? WHERE id = ? AND installation_id = ?",
		r.Version, r.OrganizationID, r.Key, r.Name, jsonOrArray(repositories), r.BindingsJSON,
		r.Classification, jsonOrNull(r.LimitsJSON), jsonOrNull(r.ExtensionsJSON), r.State,
		r.UpdatedAt.Format(timeLayout), r.ID, r.InstallationID)
	return err
}

// deleteProject removes a project row.
func deleteProject(ctx context.Context, unit contract.Unit, id contract.ID) error {
	_, err := unit.ExecContext(ctx, "DELETE FROM configuration_projects WHERE id = ?", id)
	return err
}

// insertWorker inserts a new worker at version 1.
func insertWorker(ctx context.Context, unit contract.Unit, r *workerRow) error {
	skills := marshalJSON(r.SkillVersions)
	_, err := unit.ExecContext(ctx,
		"INSERT INTO configuration_workers (id, version, installation_id, organization_id, key, name, purpose, instructions, skill_versions_json, bindings_json, profile_json, limits_json, extensions_json, state, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.ID, r.Version, r.InstallationID, r.OrganizationID, r.Key, r.Name, r.Purpose, r.Instructions,
		jsonOrArray(skills), r.BindingsJSON, jsonOrNull(r.ProfileJSON), jsonOrNull(r.LimitsJSON),
		jsonOrNull(r.ExtensionsJSON), r.State,
		r.CreatedAt.Format(timeLayout), r.UpdatedAt.Format(timeLayout))
	return err
}

// writeWorker rewrites a worker row after a version advance.
func writeWorker(ctx context.Context, unit contract.Unit, r *workerRow) error {
	skills := marshalJSON(r.SkillVersions)
	_, err := unit.ExecContext(ctx,
		"UPDATE configuration_workers SET version = ?, organization_id = ?, key = ?, name = ?, purpose = ?, instructions = ?, skill_versions_json = ?, bindings_json = ?, profile_json = ?, limits_json = ?, extensions_json = ?, state = ?, updated_at = ? WHERE id = ? AND installation_id = ?",
		r.Version, r.OrganizationID, r.Key, r.Name, r.Purpose, r.Instructions,
		jsonOrArray(skills), r.BindingsJSON, jsonOrNull(r.ProfileJSON), jsonOrNull(r.LimitsJSON),
		jsonOrNull(r.ExtensionsJSON), r.State, r.UpdatedAt.Format(timeLayout), r.ID, r.InstallationID)
	return err
}

// deleteWorker removes a worker row.
func deleteWorker(ctx context.Context, unit contract.Unit, id contract.ID) error {
	_, err := unit.ExecContext(ctx, "DELETE FROM configuration_workers WHERE id = ?", id)
	return err
}

// insertBinding inserts a new binding at version 1.
func insertBinding(ctx context.Context, unit contract.Unit, r *bindingRow) error {
	permissions := marshalJSON(r.Permissions)
	destinations := marshalJSON(r.Destinations)
	_, err := unit.ExecContext(ctx,
		"INSERT INTO configuration_bindings (id, version, installation_id, scope_json, kind, target_id, permissions_json, source_scope_json, destinations_json, state, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.ID, r.Version, r.InstallationID, r.ScopeJSON, r.Kind, r.TargetID,
		jsonOrArray(permissions), jsonOrNull(r.SourceScopeJSON), jsonOrArray(destinations), r.State,
		r.CreatedAt.Format(timeLayout), r.UpdatedAt.Format(timeLayout))
	return err
}

// writeBinding rewrites a binding row after a version advance.
func writeBinding(ctx context.Context, unit contract.Unit, r *bindingRow) error {
	permissions := marshalJSON(r.Permissions)
	destinations := marshalJSON(r.Destinations)
	_, err := unit.ExecContext(ctx,
		"UPDATE configuration_bindings SET version = ?, scope_json = ?, kind = ?, target_id = ?, permissions_json = ?, source_scope_json = ?, destinations_json = ?, state = ?, updated_at = ? WHERE id = ? AND installation_id = ?",
		r.Version, r.ScopeJSON, r.Kind, r.TargetID, jsonOrArray(permissions),
		jsonOrNull(r.SourceScopeJSON), jsonOrArray(destinations), r.State,
		r.UpdatedAt.Format(timeLayout), r.ID, r.InstallationID)
	return err
}

// deleteBinding removes a binding row.
func deleteBinding(ctx context.Context, unit contract.Unit, id contract.ID) error {
	_, err := unit.ExecContext(ctx, "DELETE FROM configuration_bindings WHERE id = ?", id)
	return err
}

// insertProfile inserts a new execution profile at version 1.
func insertProfile(ctx context.Context, unit contract.Unit, r *profileRow) error {
	capabilities := marshalJSON(r.Capabilities)
	_, err := unit.ExecContext(ctx,
		"INSERT INTO configuration_execution_profiles (id, version, installation_id, executor, model, connection_id, provider_destination, capabilities_json, cost_bound_json, classification, context_capture, state, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.ID, r.Version, r.InstallationID, r.Executor, r.Model, r.ConnectionID, r.ProviderDestination,
		jsonOrArray(capabilities), r.CostBoundJSON, r.Classification, r.ContextCapture, r.State,
		r.CreatedAt.Format(timeLayout), r.UpdatedAt.Format(timeLayout))
	return err
}

// writeProfile rewrites an execution-profile row after a version advance.
func writeProfile(ctx context.Context, unit contract.Unit, r *profileRow) error {
	capabilities := marshalJSON(r.Capabilities)
	_, err := unit.ExecContext(ctx,
		"UPDATE configuration_execution_profiles SET version = ?, executor = ?, model = ?, connection_id = ?, provider_destination = ?, capabilities_json = ?, cost_bound_json = ?, classification = ?, context_capture = ?, state = ?, updated_at = ? WHERE id = ? AND installation_id = ?",
		r.Version, r.Executor, r.Model, r.ConnectionID, r.ProviderDestination,
		jsonOrArray(capabilities), r.CostBoundJSON, r.Classification, r.ContextCapture, r.State,
		r.UpdatedAt.Format(timeLayout), r.ID, r.InstallationID)
	return err
}

// deleteProfile removes an execution-profile row.
func deleteProfile(ctx context.Context, unit contract.Unit, id contract.ID) error {
	_, err := unit.ExecContext(ctx, "DELETE FROM configuration_execution_profiles WHERE id = ?", id)
	return err
}

// jsonOrArray keeps the JSON null literal out of NOT NULL array columns:
// a nil slice serializes as "null", which these columns store as "[]".
func jsonOrArray(raw string) any {
	if normJSON(raw) == "" {
		return "[]"
	}
	return raw
}

// ---------- drafts ----------

// insertDraft persists a new draft row including its diagnostics.
func insertDraft(ctx context.Context, unit contract.Unit, r *draftRow) error {
	_, err := unit.ExecContext(ctx,
		"INSERT INTO configuration_drafts (id, version, installation_id, organization_id, base_revision, changes_json, diagnostics_json, state, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.ID, r.Version, r.InstallationID, r.OrganizationID, r.BaseRevision,
		r.ChangesJSON, marshalDiagnostics(r.Diagnostics), r.State,
		r.CreatedAt.Format(timeLayout), r.UpdatedAt.Format(timeLayout))
	return err
}

// loadDraft loads one draft row.
func loadDraft(ctx context.Context, unit contract.Unit, install, id contract.ID) (*draftRow, error) {
	return scanDraft(unit.QueryRowContext(ctx,
		"SELECT id, version, installation_id, organization_id, base_revision, changes_json, diagnostics_json, state, created_at, updated_at "+
			"FROM configuration_drafts WHERE installation_id = ? AND id = ?", install, id))
}

// updateDraftChanges persists appended changes with a version advance.
func updateDraftChanges(ctx context.Context, unit contract.Unit, r *draftRow) error {
	_, err := unit.ExecContext(ctx,
		"UPDATE configuration_drafts SET changes_json = ?, version = ?, updated_at = ? WHERE id = ? AND installation_id = ?",
		r.ChangesJSON, r.Version, r.UpdatedAt.Format(timeLayout), r.ID, r.InstallationID)
	return err
}

// updateDraftState persists a lifecycle transition.
func updateDraftState(ctx context.Context, unit contract.Unit, r *draftRow) error {
	_, err := unit.ExecContext(ctx,
		"UPDATE configuration_drafts SET state = ?, version = ?, updated_at = ? WHERE id = ? AND installation_id = ?",
		r.State, r.Version, r.UpdatedAt.Format(timeLayout), r.ID, r.InstallationID)
	return err
}

// fingerprintOf derives the cursor query fingerprint from the applied filters.
func fingerprintOf(parts ...string) string {
	return strings.Join(parts, "|")
}

// listDraftRows pages drafts for configuration.draft.list. Supported filters:
// state and organization_id; everything else refuses invalid_input before any
// SQL runs.
func listDraftRows(ctx context.Context, unit contract.Unit, in *listInput, op string) ([]*draftRow, *string, error) {
	where := []string{"installation_id = ?"}
	args := []any{in.Scope.InstallationID}
	fingerprint := []string{"draft"}
	if f := in.Filter; f != nil {
		if f.Key != "" || f.ParentID != "" || f.WorkerID != "" || f.TaskID != "" ||
			f.Descendants != nil || f.NeedsYou != nil {
			return nil, nil, invalidInput("filter fields key, parent_id, worker_id, task_id, descendants and needs_you are not supported for drafts")
		}
		if f.State != "" {
			if f.State != draftOpen && f.State != draftDiscarded {
				return nil, nil, invalidInput("filter state %q is not a draft state", f.State)
			}
			where = append(where, "state = ?")
			args = append(args, f.State)
			fingerprint = append(fingerprint, "state="+f.State)
		}
		if f.OrganizationID != "" {
			where = append(where, "organization_id = ?")
			args = append(args, f.OrganizationID)
			fingerprint = append(fingerprint, "org="+string(f.OrganizationID))
		}
	}
	offset, err := decodeCursor(unit, op, in.Cursor, fingerprintOf(fingerprint...))
	if err != nil {
		return nil, nil, err
	}
	limit := limitOf(in.Limit)
	args = append(args, limit+1, offset)
	rows, err := unit.QueryContext(ctx,
		"SELECT id, version, installation_id, organization_id, base_revision, changes_json, diagnostics_json, state, created_at, updated_at "+
			"FROM configuration_drafts WHERE "+strings.Join(where, " AND ")+
			" ORDER BY created_at, id LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*draftRow, 0, limit)
	for rows.Next() {
		r, err := scanDraft(rows)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *string
	if len(out) > limit {
		out = out[:limit]
		next = nextCursor(unit, op, fingerprintOf(fingerprint...), offset+int64(limit))
	}
	return out, next, nil
}

// ---------- plans ----------

// insertPlan persists a sealed plan with its lineage columns.
func insertPlan(ctx context.Context, unit contract.Unit, r *planRow) error {
	dependencies, err := marshalRefs(r.Dependencies)
	if err != nil {
		return err
	}
	authority, err := marshalRequirements(r.AuthorityReqs)
	if err != nil {
		return err
	}
	decisions, err := marshalDecisionReqs(r.Decisions)
	if err != nil {
		return err
	}
	diagnostics := marshalDiagnostics(r.Diagnostics)
	requirements, err := marshalRequirements(r.Requirements)
	if err != nil {
		return err
	}
	_, err = unit.ExecContext(ctx,
		"INSERT INTO configuration_plans (id, version, draft_id, installation_id, organization_id, base_revision, candidate_digest, changes_json, dependencies_json, compiler_version, schema_version, authority_requirements_json, decisions_json, diagnostics_json, requirements_json, state, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.ID, r.Version, r.DraftID, r.InstallationID, r.OrganizationID, r.BaseRevision,
		r.CandidateDigest, r.ChangesJSON, dependencies, r.CompilerVersion, r.SchemaVersion,
		authority, decisions, diagnostics, requirements, r.State,
		r.CreatedAt.Format(timeLayout), r.UpdatedAt.Format(timeLayout))
	return err
}

// loadPlan loads one plan row.
func loadPlan(ctx context.Context, unit contract.Unit, install, id contract.ID) (*planRow, error) {
	return scanPlan(unit.QueryRowContext(ctx,
		"SELECT id, version, draft_id, installation_id, organization_id, base_revision, candidate_digest, changes_json, dependencies_json, compiler_version, schema_version, authority_requirements_json, decisions_json, diagnostics_json, requirements_json, state, created_at, updated_at "+
			"FROM configuration_plans WHERE installation_id = ? AND id = ?", install, id))
}

// markPlanApplied seals the plan as applied.
func markPlanApplied(ctx context.Context, unit contract.Unit, id contract.ID, at time.Time) error {
	_, err := unit.ExecContext(ctx,
		"UPDATE configuration_plans SET state = ?, version = version + 1, updated_at = ? WHERE id = ?",
		planApplied, at.Format(timeLayout), id)
	return err
}

// listPlanRows pages plans for configuration.plan.list. Supported filters:
// state and organization_id.
func listPlanRows(ctx context.Context, unit contract.Unit, in *listInput, op string) ([]*planRow, *string, error) {
	where := []string{"installation_id = ?"}
	args := []any{in.Scope.InstallationID}
	fingerprint := []string{"plan"}
	if f := in.Filter; f != nil {
		if f.Key != "" || f.ParentID != "" || f.WorkerID != "" || f.TaskID != "" ||
			f.Descendants != nil || f.NeedsYou != nil {
			return nil, nil, invalidInput("filter fields key, parent_id, worker_id, task_id, descendants and needs_you are not supported for plans")
		}
		if f.State != "" {
			if f.State != planSealed && f.State != planApplied {
				return nil, nil, invalidInput("filter state %q is not a plan state", f.State)
			}
			where = append(where, "state = ?")
			args = append(args, f.State)
			fingerprint = append(fingerprint, "state="+f.State)
		}
		if f.OrganizationID != "" {
			where = append(where, "organization_id = ?")
			args = append(args, f.OrganizationID)
			fingerprint = append(fingerprint, "org="+string(f.OrganizationID))
		}
	}
	offset, err := decodeCursor(unit, op, in.Cursor, fingerprintOf(fingerprint...))
	if err != nil {
		return nil, nil, err
	}
	limit := limitOf(in.Limit)
	args = append(args, limit+1, offset)
	rows, err := unit.QueryContext(ctx,
		"SELECT id, version, draft_id, installation_id, organization_id, base_revision, candidate_digest, changes_json, dependencies_json, compiler_version, schema_version, authority_requirements_json, decisions_json, diagnostics_json, requirements_json, state, created_at, updated_at "+
			"FROM configuration_plans WHERE "+strings.Join(where, " AND ")+
			" ORDER BY created_at, id LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*planRow, 0, limit)
	for rows.Next() {
		r, err := scanPlan(rows)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *string
	if len(out) > limit {
		out = out[:limit]
		next = nextCursor(unit, op, fingerprintOf(fingerprint...), offset+int64(limit))
	}
	return out, next, nil
}

// ---------- revisions ----------

// insertRevision records the revision row with its object lineage.
func insertRevision(ctx context.Context, unit contract.Unit, r *revisionRow, objects []revisionObject) error {
	_, err := unit.ExecContext(ctx,
		"INSERT INTO configuration_revisions (id, version, installation_id, plan_id, candidate_digest, activated_at) VALUES (?, ?, ?, ?, ?, ?)",
		r.ID, r.Version, r.InstallationID, r.PlanID, r.CandidateDigest, r.ActivatedAt.Format(timeLayout))
	if err != nil {
		return err
	}
	for _, o := range objects {
		if _, err := unit.ExecContext(ctx,
			"INSERT INTO configuration_revision_objects (revision_id, kind, object_id, action, before_json, after_json) VALUES (?, ?, ?, ?, ?, ?)",
			r.ID, o.Kind, o.ObjectID, o.Action, textOrNull(o.BeforeJSON), textOrNull(o.AfterJSON)); err != nil {
			return err
		}
	}
	return err
}

// scanRevision scans one revision row.
func scanRevision(row rowScanner) (*revisionRow, error) {
	var r revisionRow
	var activated string
	err := row.Scan(&r.ID, &r.Version, &r.InstallationID, &r.PlanID, &r.CandidateDigest, &activated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r.ActivatedAt, err = time.Parse(timeLayout, activated); err != nil {
		return nil, err
	}
	return &r, nil
}

// loadRevision loads one revision row.
func loadRevision(ctx context.Context, unit contract.Unit, install, id contract.ID) (*revisionRow, error) {
	return scanRevision(unit.QueryRowContext(ctx,
		"SELECT id, version, installation_id, plan_id, candidate_digest, activated_at "+
			"FROM configuration_revisions WHERE installation_id = ? AND id = ?", install, id))
}

// loadRevisionByPlan loads the revision recorded for one plan.
func loadRevisionByPlan(ctx context.Context, unit contract.Unit, install, planID contract.ID) (*revisionRow, error) {
	return scanRevision(unit.QueryRowContext(ctx,
		"SELECT id, version, installation_id, plan_id, candidate_digest, activated_at "+
			"FROM configuration_revisions WHERE installation_id = ? AND plan_id = ?", install, planID))
}

// loadRevisionObjects loads the recorded lineage of one revision.
func loadRevisionObjects(ctx context.Context, unit contract.Unit, revID contract.ID) ([]revisionObject, error) {
	rows, err := unit.QueryContext(ctx,
		"SELECT kind, object_id, action, before_json, after_json "+
			"FROM configuration_revision_objects WHERE revision_id = ? ORDER BY kind, object_id", revID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]revisionObject, 0, 8)
	for rows.Next() {
		var o revisionObject
		var before, after sql.NullString
		if err := rows.Scan(&o.Kind, &o.ObjectID, &o.Action, &before, &after); err != nil {
			return nil, err
		}
		o.BeforeJSON = before.String
		o.AfterJSON = after.String
		out = append(out, o)
	}
	return out, rows.Err()
}

// listRevisionRows pages revisions for configuration.revision.list. Revisions
// model no filter fields.
func listRevisionRows(ctx context.Context, unit contract.Unit, in *listInput, op string) ([]*revisionRow, *string, error) {
	fingerprint := []string{"revision"}
	if f := in.Filter; f != nil {
		if f.State != "" || f.Key != "" || f.ParentID != "" || f.WorkerID != "" || f.TaskID != "" ||
			f.OrganizationID != "" || f.Descendants != nil || f.NeedsYou != nil {
			return nil, nil, invalidInput("revisions support no list filters")
		}
	}
	offset, err := decodeCursor(unit, op, in.Cursor, fingerprintOf(fingerprint...))
	if err != nil {
		return nil, nil, err
	}
	limit := limitOf(in.Limit)
	rows, err := unit.QueryContext(ctx,
		"SELECT id, version, installation_id, plan_id, candidate_digest, activated_at "+
			"FROM configuration_revisions WHERE installation_id = ? ORDER BY activated_at, id LIMIT ? OFFSET ?",
		in.Scope.InstallationID, limit+1, offset)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*revisionRow, 0, limit)
	for rows.Next() {
		var r revisionRow
		var activated string
		if err := rows.Scan(&r.ID, &r.Version, &r.InstallationID, &r.PlanID, &r.CandidateDigest, &activated); err != nil {
			return nil, nil, err
		}
		if r.ActivatedAt, err = time.Parse(timeLayout, activated); err != nil {
			return nil, nil, err
		}
		out = append(out, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *string
	if len(out) > limit {
		out = out[:limit]
		next = nextCursor(unit, op, fingerprintOf(fingerprint...), offset+int64(limit))
	}
	return out, next, nil
}

// ---------- pagination ----------

// decodeCursor decodes and authenticates an opaque list cursor. Unknown or
// tampered cursors fail with cursor_expired before any rows are read.
func decodeCursor(unit contract.Unit, op, cursor, fingerprint string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}
	var p cursorPayload
	if err := json.Unmarshal([]byte(cursor), &p); err != nil {
		return 0, cursorExpired("list cursor is not decodable")
	}
	if p.Offset < 0 {
		return 0, cursorExpired("list cursor is not valid")
	}
	if p.Sig != cursorSignature(unit, op, fingerprint) {
		return 0, cursorExpired("list cursor was issued for a different principal, scope or query")
	}
	return p.Offset, nil
}

// nextCursor mints the next-page cursor when the page was full.
func nextCursor(unit contract.Unit, op, fingerprint string, offset int64) *string {
	raw, err := json.Marshal(cursorPayload{Offset: offset, Sig: cursorSignature(unit, op, fingerprint)})
	if err != nil {
		return nil
	}
	s := string(raw)
	return &s
}

// ---------- shared JSON marshaling for lineage columns ----------

func marshalRefs(refs []wireRef) (string, error) {
	if refs == nil {
		return "[]", nil
	}
	raw, err := json.Marshal(refs)
	if err != nil {
		return "", internalError("dependency encoding failed")
	}
	return string(raw), nil
}

func marshalRequirements(reqs []wireRequirement) (string, error) {
	if reqs == nil {
		return "[]", nil
	}
	raw, err := json.Marshal(reqs)
	if err != nil {
		return "", internalError("requirement encoding failed")
	}
	return string(raw), nil
}

func marshalDecisionReqs(reqs []wireDecisionRequirement) (string, error) {
	if reqs == nil {
		return "[]", nil
	}
	raw, err := json.Marshal(reqs)
	if err != nil {
		return "", internalError("decision encoding failed")
	}
	return string(raw), nil
}
