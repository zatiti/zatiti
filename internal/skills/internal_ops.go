package skills

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/zatiti/zatiti/internal/contract"
)

// Internal compiler operations. _skills.validate reports the disposition of
// the owned candidate slice against the current snapshot; _skills.activate
// applies the exact sealed candidate slice inside the compiler transaction.
// Both handle only changes with kind "skill" — other kinds belong to their
// owners. Authorization comes from the old effective state and the caller
// allowlist, never from the candidate body.

// skillChange is one owned candidate change with its decoded definition.
type skillChange struct {
	Index      int
	Change     wireChange
	Definition wireSkill
	Row        *skillRow
}

// ownedChanges decodes the skill slice of a candidate: every change whose
// kind is "skill" must carry a definition that decodes and matches a stored
// version row exactly — the candidate is a sealed reference to what skills
// already validated, never a fresh definition.
func ownedChanges(ctx context.Context, unit contract.Unit, in *candidateInput) ([]skillChange, error) {
	installation := unit.Scope().InstallationID
	out := make([]skillChange, 0, len(in.Candidate.Changes))
	for i := range in.Candidate.Changes {
		c := in.Candidate.Changes[i]
		if c.Kind != "skill" {
			continue
		}
		var def wireSkill
		if err := contract.DecodeStrict(c.Definition, &def); err != nil {
			return nil, invalidInput("candidate change %d definition is not a valid skill document: %v", i, err)
		}
		row, err := fetchSkillVersion(ctx, unit, installation, c.ID, int64(def.Version))
		if err != nil {
			return nil, err
		}
		if row == nil {
			return nil, notFound("candidate change %d references skill %s version %d which does not exist", i, c.ID, def.Version)
		}
		if err := assertDefinitionMatches(ctx, unit, installation, row, &def, i); err != nil {
			return nil, err
		}
		out = append(out, skillChange{Index: i, Change: c, Definition: def, Row: row})
	}
	return out, nil
}

// assertDefinitionMatches enforces the exact-sealed-candidate fence: the
// candidate definition must equal its registered stored version in every
// owned field. A candidate that rewrites content, identity, requirements or
// dependency pins is rejected — activation may only transition state.
func assertDefinitionMatches(ctx context.Context, unit contract.Unit, installation contract.ID, row *skillRow, def *wireSkill, index int) error {
	mismatch := conflictFault("candidate change %d must match its registered concrete definition schemas", index)
	if def.Name != row.Name ||
		def.ContentDigest != row.ContentDigest ||
		def.InstructionArtifact.ID != row.InstructionArtifactID ||
		def.InstructionArtifact.Digest != row.InstructionDigest ||
		string(def.InputSchema) != nullableJSON(row.InputSchemaJSON) ||
		string(def.OutputSchema) != nullableJSON(row.OutputSchemaJSON) ||
		def.Source != row.Source ||
		def.License != row.License {
		return mismatch
	}
	requirements, err := decodeJSONColumn[[]string](row.RequirementsJSON)
	if err != nil {
		return faultWrap(internalError("stored requirements are malformed"), err)
	}
	if len(requirements) != len(def.Requirements) {
		return mismatch
	}
	for i := range requirements {
		if requirements[i] != def.Requirements[i] {
			return mismatch
		}
	}
	deps, err := skillDependencies(ctx, unit, installation, row.ID, row.Version)
	if err != nil {
		return err
	}
	if len(deps) != len(def.Dependencies) {
		return mismatch
	}
	for i := range deps {
		if deps[i].DepID != def.Dependencies[i].ID || deps[i].DepVersion != int64(def.Dependencies[i].Version) {
			return mismatch
		}
	}
	return nil
}

// validate handles _skills.validate: reports error diagnostics for every
// owned change that could not activate, without mutating anything.
func handleValidate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[candidateInput](s, "_skills.validate", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	installation := unit.Scope().InstallationID
	validation := wireValidation{
		Diagnostics:  []wireDiagnostic{},
		Requirements: []wireRequirement{},
		Dependencies: []wireRef{},
	}
	seen := map[string]bool{}
	for i := range in.Candidate.Changes {
		c := in.Candidate.Changes[i]
		if c.Kind != "skill" {
			continue
		}
		diag := func(code, message string) {
			validation.Diagnostics = append(validation.Diagnostics, wireDiagnostic{
				Path:     "candidate.changes[" + strconv.Itoa(i) + "]",
				Code:     code,
				Message:  message,
				Severity: "error",
			})
		}

		var def wireSkill
		if err := contract.DecodeStrict(c.Definition, &def); err != nil {
			diag("invalid_input", fmt.Sprintf("candidate definition is not a valid skill document: %v", err))
			continue
		}
		row, err := fetchSkillVersion(ctx, unit, installation, c.ID, int64(def.Version))
		if err != nil {
			return contract.Payload{}, err
		}
		if row == nil {
			diag("not_found", fmt.Sprintf("skill %s version %d does not exist", c.ID, def.Version))
			continue
		}
		key := string(c.ID) + "@" + strconv.FormatInt(int64(def.Version), 10)
		if seen[key] {
			diag("conflict", fmt.Sprintf("skill %s version %d appears more than once in the candidate slice", c.ID, def.Version))
			continue
		}
		seen[key] = true
		if err := assertDefinitionMatches(ctx, unit, installation, row, &def, i); err != nil {
			var f *contract.Fault
			message := err.Error()
			if asFault(err, &f) {
				message = f.Message
			}
			diag("conflict", message)
			continue
		}
		if err := validateTransition(ctx, unit, installation, row, &def, &c); err != nil {
			var f *contract.Fault
			message := err.Error()
			code := "invalid_input"
			if asFault(err, &f) {
				message = f.Message
				code = f.Code
			}
			diag(code, message)
			continue
		}

		// Collect the resolved dependency pins of this version so the
		// validation body reports the graph the activation relies on.
		deps, err := skillDependencies(ctx, unit, installation, c.ID, int64(def.Version))
		if err != nil {
			return contract.Payload{}, err
		}
		for _, d := range deps {
			validation.Dependencies = append(validation.Dependencies, wireRef{ID: d.DepID, Version: contract.Version(d.DepVersion)})
		}
	}

	// The combined dependency graph — candidate versions plus stored pins —
	// must remain acyclic.
	if err := rejectStoredCycles(ctx, unit, installation, in); err != nil {
		return contract.Payload{}, err
	}
	return s.completed(validationOutput{Resource: validation})
}

// validateTransition checks one owned change's action against the stored
// row and the current effective state. It is shared by validate (faults
// become diagnostics) and activate (faults reject the slice).
func validateTransition(ctx context.Context, unit contract.Unit, installation contract.ID, row *skillRow, def *wireSkill, c *wireChange) error {
	switch c.Action {
	case "create":
		if c.ExpectedVersion != 0 {
			return invalidInput("skill %s version %d create requires expected_version 0", c.ID, def.Version)
		}
		active, err := hasActiveVersion(ctx, unit, installation, c.ID)
		if err != nil {
			return err
		}
		if active {
			return conflictFault("skill %s already has an active version; create cannot supersede", c.ID)
		}
		if row.State != "draft" {
			return conflictFault("skill %s version %d is %s; create requires a draft version", c.ID, def.Version, row.State)
		}
		return nil
	case "update":
		if c.ExpectedVersion != def.Version {
			return invalidInput("skill %s update requires expected_version %d", c.ID, def.Version)
		}
		if row.State != "draft" {
			return conflictFault("skill %s version %d is %s; update requires a draft version", c.ID, def.Version, row.State)
		}
		active, err := hasActiveVersion(ctx, unit, installation, c.ID)
		if err != nil {
			return err
		}
		if !active {
			return conflictFault("skill %s has no active version to supersede; use create", c.ID)
		}
		return nil
	case "archive":
		if c.ExpectedVersion != def.Version {
			return invalidInput("skill %s archive requires expected_version %d", c.ID, def.Version)
		}
		if row.State == "archived" {
			return conflictFault("skill %s version %d is already archived", c.ID, def.Version)
		}
		return nil
	default:
		return invalidInput("skill change action %q is not supported", c.Action)
	}
}

// activate handles _skills.activate: applies the owned exact sealed
// candidate slice inside the compiler transaction. Every owned change is
// checked first, then every transition is applied, so the slice lands all
// or nothing within the caller's transaction.
func handleActivate(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[candidateInput](s, "_skills.activate", inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	installation := unit.Scope().InstallationID
	changes, err := ownedChanges(ctx, unit, in)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := rejectStoredCycles(ctx, unit, installation, in); err != nil {
		return contract.Payload{}, err
	}

	// Check every transition before applying any.
	type activation struct {
		change    skillChange
		supersede *skillRow
	}
	plans := make([]activation, 0, len(changes))
	for _, ch := range changes {
		if err := validateTransition(ctx, unit, installation, ch.Row, &ch.Definition, &ch.Change); err != nil {
			return contract.Payload{}, err
		}
		if ch.Change.Action == "update" {
			old, err := fetchActiveVersion(ctx, unit, installation, ch.Change.ID)
			if err != nil {
				return contract.Payload{}, err
			}
			plans = append(plans, activation{change: ch, supersede: old})
			continue
		}
		plans = append(plans, activation{change: ch})
	}

	// Apply. A superseded active version is archived and its policy
	// qualifications invalidated; creation invalidates nothing — there is
	// nothing it replaces.
	now := s.clock.Now().UTC().Format(timeLayout)
	versions := make([]wireRef, 0, len(plans))
	for _, p := range plans {
		switch p.change.Change.Action {
		case "create", "update":
			if p.supersede != nil {
				if err := setState(ctx, unit, now, installation, p.supersede.ID, p.supersede.Version, "archived"); err != nil {
					return contract.Payload{}, err
				}
			}
			if err := setState(ctx, unit, now, installation, p.change.Row.ID, p.change.Row.Version, "active"); err != nil {
				return contract.Payload{}, err
			}
			if err := s.emitSkillState(ctx, unit, "skills.skill.activated", p.change.Row.ID, p.change.Row.Version, p.change.Row.Name); err != nil {
				return contract.Payload{}, err
			}
			if p.supersede != nil {
				if _, err := s.callOwner(ctx, unit, "_policy.invalidate", map[string]any{
					"changed_dependencies": []wireRef{{ID: p.supersede.ID, Version: contract.Version(p.supersede.Version)}},
					"reason": fmt.Sprintf("skill %s version %d superseded by activation",
						p.supersede.Name, p.supersede.Version),
				}); err != nil {
					return contract.Payload{}, err
				}
			}
		case "archive":
			wasActive := p.change.Row.State == "active"
			if err := setState(ctx, unit, now, installation, p.change.Row.ID, p.change.Row.Version, "archived"); err != nil {
				return contract.Payload{}, err
			}
			if err := s.emitSkillState(ctx, unit, "skills.skill.archived", p.change.Row.ID, p.change.Row.Version, p.change.Row.Name); err != nil {
				return contract.Payload{}, err
			}
			if wasActive {
				if _, err := s.callOwner(ctx, unit, "_policy.invalidate", map[string]any{
					"changed_dependencies": []wireRef{{ID: p.change.Row.ID, Version: contract.Version(p.change.Row.Version)}},
					"reason": fmt.Sprintf("skill %s version %d archived",
						p.change.Row.Name, p.change.Row.Version),
				}); err != nil {
					return contract.Payload{}, err
				}
			}
		}
		versions = append(versions, wireRef{ID: p.change.Row.ID, Version: contract.Version(p.change.Row.Version)})
	}
	return s.completed(versionsOutput{Versions: versions})
}

// setState transitions one immutable version row's state — the only mutable
// column. The UPDATE pins the identity so the transition applies exactly
// once inside the transaction.
func setState(ctx context.Context, unit contract.Unit, now string, installation contract.ID, id contract.ID, version int64, state string) error {
	result, err := unit.ExecContext(ctx, `
		UPDATE skills_versions
		SET state = ?, updated_at = ?
		WHERE installation_id = ? AND id = ? AND version = ?`,
		state, now, string(installation), string(id), version)
	if err != nil {
		return faultWrap(internalError("skill state transition failed"), err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return faultWrap(internalError("skill state transition failed"), err)
	}
	if affected != 1 {
		return conflictFault("skill %s version %d state transition did not apply", id, version)
	}
	return nil
}

// hasActiveVersion reports whether any active version of the identity exists.
func hasActiveVersion(ctx context.Context, unit contract.Unit, installation contract.ID, id contract.ID) (bool, error) {
	row, err := fetchActiveVersion(ctx, unit, installation, id)
	if err != nil {
		return false, err
	}
	return row != nil, nil
}

// fetchActiveVersion loads the active version row of one skill identity.
func fetchActiveVersion(ctx context.Context, unit contract.Unit, installation contract.ID, id contract.ID) (*skillRow, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT id, version, installation_id, name, description, state,
		       content_digest, instruction_digest, instruction_artifact_id,
		       manifest_json, requirements_json, dependencies_json,
		       input_schema_json, output_schema_json, source, license,
		       created_at, updated_at
		FROM skills_versions
		WHERE installation_id = ? AND id = ? AND state = 'active'
		ORDER BY version DESC LIMIT 1`, string(installation), string(id))
	r, err := scanSkill(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, faultWrap(internalError("skill lookup failed"), err)
	}
	return r, nil
}

// rejectStoredCycles walks the stored dependency graph reachable from the
// candidate's owned versions and rejects any cycle the slice would leave
// behind. The candidate never changes edges — import resolved and
// cycle-checked them — so a cycle here means the stored graph is already
// inconsistent; refusing the slice fails closed.
func rejectStoredCycles(ctx context.Context, unit contract.Unit, installation contract.ID, in *candidateInput) error {
	type node struct {
		id      contract.ID
		version int64
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	colors := map[node]int{}

	var visit func(n node) error
	visit = func(n node) error {
		colors[n] = gray
		deps, err := skillDependencies(ctx, unit, installation, n.id, n.version)
		if err != nil {
			return err
		}
		for _, d := range deps {
			next := node{id: d.DepID, version: d.DepVersion}
			if next == n || colors[next] == gray {
				return invalidInput("skill dependency cycle through %s version %d", d.DepName, d.DepVersion)
			}
			if colors[next] != white {
				continue
			}
			if err := visit(next); err != nil {
				return err
			}
		}
		colors[n] = black
		return nil
	}

	for i := range in.Candidate.Changes {
		c := in.Candidate.Changes[i]
		if c.Kind != "skill" {
			continue
		}
		var def wireSkill
		if err := json.Unmarshal(c.Definition, &def); err != nil {
			continue // reported by the per-change checks
		}
		n := node{id: c.ID, version: int64(def.Version)}
		if colors[n] == white {
			if err := visit(n); err != nil {
				return err
			}
		}
	}
	return nil
}
