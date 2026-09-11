package accounting

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Budget definition operations. A budget binds by identity: the budget id
// equals the installation, organization, project or worker id it governs.
// _accounting.validate reports diagnostics without changing anything;
// _accounting.activate is the commitment point called by the configuration
// compiler inside its transaction; budget.propose stages an update through
// _configuration.stage; budget.get returns the effective intersected limits.

// validateBudgetChange checks one change against the current budget state
// and returns an error diagnostic, or nil when the change is applicable.
func (s *Service) validateBudgetChange(ctx context.Context, unit contract.Unit, change wireChange, path string) *wireDiagnostic {
	if change.Kind != changeKindBudget {
		return &wireDiagnostic{Path: path, Code: "unsupported_kind",
			Message: fmt.Sprintf("accounting validates budget changes only, got %q", change.Kind), Severity: "error"}
	}
	if err := contract.ValidateSchema(s.limitsSchema, change.Definition); err != nil {
		return &wireDiagnostic{Path: path + "/definition", Code: "invalid_definition",
			Message: fmt.Sprintf("definition does not match the Limits schema: %v", err), Severity: "error"}
	}
	var limits wireLimits
	if err := contract.DecodeStrict(change.Definition, &limits); err != nil {
		return &wireDiagnostic{Path: path + "/definition", Code: "invalid_definition",
			Message: fmt.Sprintf("definition decoding failed: %v", err), Severity: "error"}
	}
	if limits.Currency == unconfiguredCurrency && limits.SpendMicroUnits > 0 {
		return &wireDiagnostic{Path: path + "/definition", Code: "unconfigured_currency",
			Message: "paid execution requires an explicitly configured currency and a finite spend ceiling", Severity: "error"}
	}
	b, err := s.loadBudget(ctx, unit, s.installationOf(unit), change.ID)
	if err != nil {
		return &wireDiagnostic{Path: path, Code: "storage_error", Message: err.Error(), Severity: "error"}
	}
	switch change.Action {
	case changeActionCreate:
		if change.ExpectedVersion != 0 {
			return &wireDiagnostic{Path: path, Code: "create_expected_version",
				Message: "expected-version zero is create-only", Severity: "error"}
		}
		if b != nil {
			return &wireDiagnostic{Path: path, Code: "already_exists",
				Message: fmt.Sprintf("budget %s already exists; stage an update instead", change.ID), Severity: "error"}
		}
	case changeActionUpdate, changeActionArchive:
		if b == nil {
			return &wireDiagnostic{Path: path, Code: "missing",
				Message: fmt.Sprintf("no active budget %s to %s", change.ID, change.Action), Severity: "error"}
		}
		if change.ExpectedVersion != b.Version {
			return &wireDiagnostic{Path: path, Code: "stale_version",
				Message:  fmt.Sprintf("budget %s is at version %d, change expects %d", change.ID, b.Version, change.ExpectedVersion),
				Severity: "error"}
		}
	default:
		return &wireDiagnostic{Path: path, Code: "unsupported_action",
			Message:  fmt.Sprintf("budget changes support create, update and archive; %q deletes recorded obligations history", change.Action),
			Severity: "error"}
	}
	return nil
}

// applyBudgetChange applies one owned change at the commitment point. Every
// failure is a fault and rolls the surrounding compiler transaction back.
func (s *Service) applyBudgetChange(ctx context.Context, unit contract.Unit, change wireChange, now time.Time) (wireRef, error) {
	if change.Kind != changeKindBudget {
		return wireRef{}, invalidInput("accounting applies budget changes only, got %q", change.Kind)
	}
	if err := contract.ValidateSchema(s.limitsSchema, change.Definition); err != nil {
		return wireRef{}, invalidInput("change %s definition does not match the Limits schema: %v", change.ID, err)
	}
	var limits wireLimits
	if err := contract.DecodeStrict(change.Definition, &limits); err != nil {
		return wireRef{}, invalidInput("change %s definition decoding failed: %v", change.ID, err)
	}
	if limits.Currency == unconfiguredCurrency && limits.SpendMicroUnits > 0 {
		return wireRef{}, invalidInput("budget %s: paid execution requires an explicitly configured currency and a finite spend ceiling", change.ID)
	}
	limitsJSON, err := canonicalJSON(limits)
	if err != nil {
		return wireRef{}, err
	}
	install := s.installationOf(unit)
	b, err := s.loadBudget(ctx, unit, install, change.ID)
	if err != nil {
		return wireRef{}, err
	}
	version := change.ExpectedVersion
	switch change.Action {
	case changeActionCreate:
		if change.ExpectedVersion != 0 {
			return wireRef{}, invalidInput("budget %s: expected-version zero is create-only", change.ID)
		}
		if b != nil {
			return wireRef{}, conflict("budget %s already exists; stage an update instead", change.ID)
		}
		// A previously archived budget with the same identity re-activates
		// with a fresh version; identity is the governed object, not the row.
		// The reported version is read back, not assumed: the upsert's stored
		// version is one past the archived row's, not one.
		if _, err := unit.ExecContext(ctx, `INSERT INTO accounting_budgets (id, version, installation_id, limits_json, state, created_at, updated_at)
			VALUES (?, 1, ?, ?, 'active', ?, ?)
			ON CONFLICT (id) DO UPDATE SET version = accounting_budgets.version + 1, installation_id = excluded.installation_id,
				limits_json = excluded.limits_json, state = 'active', updated_at = excluded.updated_at`,
			string(change.ID), string(install), limitsJSON, formatStamp(now), formatStamp(now)); err != nil {
			return wireRef{}, err
		}
		if err := unit.QueryRowContext(ctx, `SELECT version FROM accounting_budgets WHERE id = ?`, string(change.ID)).Scan(&version); err != nil {
			return wireRef{}, err
		}
	case changeActionUpdate, changeActionArchive:
		if b == nil {
			return wireRef{}, notFound("no active budget %s to %s", change.ID, change.Action)
		}
		if change.ExpectedVersion != b.Version {
			return wireRef{}, staleVersion("budget %s is at version %d, change expects %d", change.ID, b.Version, change.ExpectedVersion)
		}
		state := "active"
		if change.Action == changeActionArchive {
			state = "archived"
		}
		if _, err := unit.ExecContext(ctx, `UPDATE accounting_budgets SET version = version + 1, limits_json = ?, state = ?, updated_at = ?
			WHERE id = ? AND installation_id = ? AND version = ?`,
			limitsJSON, state, formatStamp(now), string(change.ID), string(install), b.Version); err != nil {
			return wireRef{}, err
		}
		version = b.Version + 1
	default:
		return wireRef{}, invalidInput("budget changes support create, update and archive; %q deletes recorded obligations history", change.Action)
	}
	err = emitEvent(ctx, unit, eventBudgetActivated, change.ID, contract.Version(version), map[string]any{
		"action": change.Action,
	})
	if err != nil {
		return wireRef{}, err
	}
	return wireRef{ID: change.ID, Version: version}, nil
}

// handleValidate is the _accounting.validate boundary: report diagnostics for
// the owned candidate slice against current state, no live changes.
func (s *Service) handleValidate(ctx context.Context, unit contract.Unit, in candidateEnvelope) (contract.Outcome[validationResourceBody], error) {
	diagnostics := make([]wireDiagnostic, 0, len(in.Candidate.Changes))
	for i, change := range in.Candidate.Changes {
		if d := s.validateBudgetChange(ctx, unit, change, fmt.Sprintf("/changes/%d", i)); d != nil {
			diagnostics = append(diagnostics, *d)
		}
	}
	return completedOutcome(validationResourceBody{Resource: wireValidation{
		Diagnostics:  diagnostics,
		Requirements: []wireRequirement{},
		Dependencies: []wireRef{},
	}})
}

// handleActivate is the _accounting.activate commitment point: apply the
// owned exact sealed candidate slice inside the caller's compiler
// transaction. Every failure is a fault and rolls the plan back.
func (s *Service) handleActivate(ctx context.Context, unit contract.Unit, in candidateEnvelope) (contract.Outcome[versionsOutput], error) {
	versions := make([]wireRef, 0, len(in.Candidate.Changes))
	for _, change := range in.Candidate.Changes {
		ref, err := s.applyBudgetChange(ctx, unit, change, s.now())
		if err != nil {
			return contract.Outcome[versionsOutput]{}, err
		}
		versions = append(versions, ref)
	}
	return completedOutcome(versionsOutput{Versions: versions})
}

// handleBudgetGet is the budget.get boundary: the effective intersected
// installation, ancestor, project and worker limits for one scope.
func (s *Service) handleBudgetGet(ctx context.Context, unit contract.Unit, in scopeInput) (contract.Outcome[limitsOutput], error) {
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[limitsOutput]{}, err
	}
	levels, err := s.levelsForScope(ctx, unit, in.Scope)
	if err != nil {
		return contract.Outcome[limitsOutput]{}, err
	}
	limits, err := effectiveLimits(levels, time.Time{})
	if err != nil {
		return contract.Outcome[limitsOutput]{}, err
	}
	return completedOutcome(limitsOutput{Limits: *limits})
}

// budgetTarget resolves the budget a propose request addresses: the most
// specific named dimension wins, in worker, project, organization,
// installation order. The installation is always named, so the only failure
// is a scope with no dimensions at all.
func budgetTarget(scope wireScope) (contract.ID, error) {
	switch {
	case scope.WorkerID != "":
		return scope.WorkerID, nil
	case scope.ProjectID != "":
		return scope.ProjectID, nil
	case scope.OrganizationID != "":
		return scope.OrganizationID, nil
	case scope.InstallationID != "":
		return scope.InstallationID, nil
	default:
		return "", invalidInput("scope must name at least the installation")
	}
}

// handleBudgetPropose is the budget.propose boundary: stage the exact budget
// update under old authority through _configuration.stage. First creation
// flows through general configuration drafts, so propose addresses existing
// active budgets only.
func (s *Service) handleBudgetPropose(ctx context.Context, unit contract.Unit, in budgetProposeInput) (contract.Outcome[draftResourceBody], error) {
	if err := s.checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Outcome[draftResourceBody]{}, err
	}
	target, err := budgetTarget(in.Scope)
	if err != nil {
		return contract.Outcome[draftResourceBody]{}, err
	}
	var limits wireLimits
	if err := contract.DecodeStrict(in.Limits, &limits); err != nil {
		return contract.Outcome[draftResourceBody]{}, invalidInput("limits decoding failed: %v", err)
	}
	if limits.Currency == unconfiguredCurrency && limits.SpendMicroUnits > 0 {
		return contract.Outcome[draftResourceBody]{}, invalidInput("budget %s: paid execution requires an explicitly configured currency and a finite spend ceiling", target)
	}
	b, err := s.loadBudget(ctx, unit, in.Scope.InstallationID, target)
	if err != nil {
		return contract.Outcome[draftResourceBody]{}, err
	}
	if b == nil {
		return contract.Outcome[draftResourceBody]{}, notFound("no active budget for %s; create it through a configuration draft", target)
	}
	if in.ExpectedVersion != b.Version {
		return contract.Outcome[draftResourceBody]{}, staleVersion("budget %s is at version %d, propose expects %d", target, b.Version, in.ExpectedVersion)
	}
	definition, err := canonicalJSON(limits)
	if err != nil {
		return contract.Outcome[draftResourceBody]{}, err
	}
	change, err := json.Marshal(wireChange{
		Kind:            changeKindBudget,
		Action:          changeActionUpdate,
		ID:              target,
		ExpectedVersion: b.Version,
		Definition:      json.RawMessage(definition),
	})
	if err != nil {
		return contract.Outcome[draftResourceBody]{}, fmt.Errorf("accounting: encode budget change: %w", err)
	}
	raw, err := s.callConfiguration(ctx, unit, opConfigStage, stageInput{
		Scope:   in.Scope,
		Change:  json.RawMessage(change),
		DraftID: in.DraftID,
	})
	if err != nil {
		return contract.Outcome[draftResourceBody]{}, err
	}
	// The staged draft decodes strictly against the exact Draft mirror; the
	// typed body is re-marshaled and schema-validated by the bind boundary.
	var body draftResourceBody
	if err := contract.DecodeStrict(raw, &body); err != nil {
		return contract.Outcome[draftResourceBody]{}, fmt.Errorf("accounting: staged draft payload does not decode: %w", err)
	}
	return completedOutcome(body)
}
