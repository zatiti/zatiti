package skills

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Row types for the skills_ tables. JSON columns stay as strings and are
// (un)marshaled at the edges; version rows are immutable records whose state
// transitions are the only mutable field.

const timeLayout = time.RFC3339Nano

// skillRow is one immutable skill version.
type skillRow struct {
	ID                    contract.ID
	Version               int64
	InstallationID        contract.ID
	Name                  string
	Description           string
	State                 string // draft | active | archived
	ContentDigest         contract.Digest
	InstructionDigest     contract.Digest
	InstructionArtifactID contract.ID
	ManifestJSON          string
	RequirementsJSON      string
	DependenciesJSON      string
	InputSchemaJSON       string
	OutputSchemaJSON      string
	Source                string
	License               string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// evaluationRow is one sealed evaluation record. acceptance_json holds the
// self-authored expectations; observations_json and evidence_json hold the
// actual outcome and stay NULL until recorded downstream.
type evaluationRow struct {
	ID               contract.ID
	Version          int64
	InstallationID   contract.ID
	SkillID          contract.ID
	SkillVersion     int64
	State            string // pending | running | succeeded | failed | outcome_unknown | cancelled
	VerifierID       string
	VerifierVersion  string
	Mode             string
	AcceptanceJSON   string
	ProfileJSON      string
	LimitsJSON       string
	ObservationsJSON string
	EvidenceJSON     string
	JobID            contract.ID
	JobJSON          string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// dependencyRow is one resolved dependency pin of a skill version.
type dependencyRow struct {
	InstallationID contract.ID
	SkillID        contract.ID
	SkillVersion   int64
	DepName        string
	DepID          contract.ID
	DepVersion     int64
}

// rowScanner abstracts *sql.Row and *sql.Rows so point lookups and list
// scans share one decode path per table.
type rowScanner interface {
	Scan(dest ...any) error
}

// limitOf normalizes the list limit: default 50, maximum 200.
func limitOf(limit int64) int {
	switch {
	case limit <= 0:
		return 50
	case limit > 200:
		return 200
	default:
		return int(limit)
	}
}

// checkInstallation enforces the installation scope fence: the input scope
// must name an installation and match the transaction scope, so no operation
// reads or writes across installations. Additional organization/project/
// worker dimensions are enforced per operation against referenced resources,
// never inferred.
func checkInstallation(unit contract.Unit, scope wireScope) error {
	if scope.InstallationID == "" {
		return invalidInput("scope installation_id is required")
	}
	unitScope := unit.Scope()
	if unitScope.InstallationID != "" && unitScope.InstallationID != scope.InstallationID {
		return permissionDenied("scope installation does not match the transaction scope")
	}
	return nil
}

// marshalJSON encodes v, failing closed on encoding errors.
func marshalJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", internalError("json encoding failed")
	}
	return string(raw), nil
}

// decodeJSONColumn strict-decodes a stored JSON column.
func decodeJSONColumn[T any](raw string) (T, error) {
	var out T
	if raw == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return out, err
	}
	return out, nil
}

// scanSkill decodes one skills_versions row.
func scanSkill(row rowScanner) (*skillRow, error) {
	var r skillRow
	var created, updated string
	err := row.Scan(
		&r.ID, &r.Version, &r.InstallationID, &r.Name, &r.Description, &r.State,
		&r.ContentDigest, &r.InstructionDigest, &r.InstructionArtifactID,
		&r.ManifestJSON, &r.RequirementsJSON, &r.DependenciesJSON,
		&r.InputSchemaJSON, &r.OutputSchemaJSON, &r.Source, &r.License,
		&created, &updated,
	)
	if err != nil {
		return nil, err
	}
	if r.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, faultWrap(internalError("stored timestamp is malformed"), err)
	}
	if r.UpdatedAt, err = time.Parse(timeLayout, updated); err != nil {
		return nil, faultWrap(internalError("stored timestamp is malformed"), err)
	}
	return &r, nil
}

// scanEvaluation decodes one skills_evaluations row.
func scanEvaluation(row rowScanner) (*evaluationRow, error) {
	var r evaluationRow
	var created, updated string
	var jobID, observations, evidence, jobJSON sql.NullString
	err := row.Scan(
		&r.ID, &r.Version, &r.InstallationID, &r.SkillID, &r.SkillVersion, &r.State,
		&r.VerifierID, &r.VerifierVersion, &r.Mode,
		&r.AcceptanceJSON, &r.ProfileJSON, &r.LimitsJSON,
		&observations, &evidence, &jobID, &jobJSON,
		&created, &updated,
	)
	if err != nil {
		return nil, err
	}
	r.ObservationsJSON = observations.String
	r.EvidenceJSON = evidence.String
	r.JobID = contract.ID(jobID.String)
	r.JobJSON = jobJSON.String
	if r.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, faultWrap(internalError("stored timestamp is malformed"), err)
	}
	if r.UpdatedAt, err = time.Parse(timeLayout, updated); err != nil {
		return nil, faultWrap(internalError("stored timestamp is malformed"), err)
	}
	return &r, nil
}

// scanDependency decodes one skills_dependencies row.
func scanDependency(row rowScanner) (*dependencyRow, error) {
	var r dependencyRow
	err := row.Scan(&r.InstallationID, &r.SkillID, &r.SkillVersion, &r.DepName, &r.DepID, &r.DepVersion)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// querySkills runs a skills_versions query and decodes every row.
func querySkills(ctx context.Context, unit contract.Unit, query string, args ...any) ([]*skillRow, error) {
	rows, err := unit.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, faultWrap(internalError("skill query failed"), err)
	}
	defer func() { _ = rows.Close() }()
	var out []*skillRow
	for rows.Next() {
		r, err := scanSkill(rows)
		if err != nil {
			return nil, faultWrap(internalError("skill row decode failed"), err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, faultWrap(internalError("skill query failed"), err)
	}
	return out, nil
}

// fetchSkillByID loads the latest version row of one skill identity.
func fetchSkillByID(ctx context.Context, unit contract.Unit, installation contract.ID, id contract.ID) (*skillRow, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT id, version, installation_id, name, description, state,
		       content_digest, instruction_digest, instruction_artifact_id,
		       manifest_json, requirements_json, dependencies_json,
		       input_schema_json, output_schema_json, source, license,
		       created_at, updated_at
		FROM skills_versions
		WHERE installation_id = ? AND id = ?
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

// fetchSkillVersion loads one exact version row.
func fetchSkillVersion(ctx context.Context, unit contract.Unit, installation contract.ID, id contract.ID, version int64) (*skillRow, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT id, version, installation_id, name, description, state,
		       content_digest, instruction_digest, instruction_artifact_id,
		       manifest_json, requirements_json, dependencies_json,
		       input_schema_json, output_schema_json, source, license,
		       created_at, updated_at
		FROM skills_versions
		WHERE installation_id = ? AND id = ? AND version = ?`,
		string(installation), string(id), version)
	r, err := scanSkill(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, faultWrap(internalError("skill lookup failed"), err)
	}
	return r, nil
}

// fetchSkillByName loads the latest version row carrying a skill name.
func fetchSkillByName(ctx context.Context, unit contract.Unit, installation contract.ID, name string) (*skillRow, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT id, version, installation_id, name, description, state,
		       content_digest, instruction_digest, instruction_artifact_id,
		       manifest_json, requirements_json, dependencies_json,
		       input_schema_json, output_schema_json, source, license,
		       created_at, updated_at
		FROM skills_versions
		WHERE installation_id = ? AND name = ?
		ORDER BY version DESC LIMIT 1`, string(installation), name)
	r, err := scanSkill(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, faultWrap(internalError("skill lookup failed"), err)
	}
	return r, nil
}

// fetchSkillByNameVersion loads one exact version row carrying a skill
// name, in any state.
func fetchSkillByNameVersion(ctx context.Context, unit contract.Unit, installation contract.ID, name string, version int64) (*skillRow, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT id, version, installation_id, name, description, state,
		       content_digest, instruction_digest, instruction_artifact_id,
		       manifest_json, requirements_json, dependencies_json,
		       input_schema_json, output_schema_json, source, license,
		       created_at, updated_at
		FROM skills_versions
		WHERE installation_id = ? AND name = ? AND version = ?`, string(installation), name, version)
	r, err := scanSkill(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, faultWrap(internalError("skill lookup failed"), err)
	}
	return r, nil
}

// fetchActiveSkillByName loads the latest active version row carrying a
// skill name.
func fetchActiveSkillByName(ctx context.Context, unit contract.Unit, installation contract.ID, name string) (*skillRow, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT id, version, installation_id, name, description, state,
		       content_digest, instruction_digest, instruction_artifact_id,
		       manifest_json, requirements_json, dependencies_json,
		       input_schema_json, output_schema_json, source, license,
		       created_at, updated_at
		FROM skills_versions
		WHERE installation_id = ? AND name = ? AND state = 'active'
		ORDER BY version DESC LIMIT 1`, string(installation), name)
	r, err := scanSkill(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, faultWrap(internalError("skill lookup failed"), err)
	}
	return r, nil
}

// fetchEvaluation loads one evaluation row.
func fetchEvaluation(ctx context.Context, unit contract.Unit, installation contract.ID, id contract.ID) (*evaluationRow, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT id, version, installation_id, skill_id, skill_version, state,
		       verifier_id, verifier_version, mode,
		       acceptance_json, profile_json, limits_json,
		       observations_json, evidence_json, job_id, job_json,
		       created_at, updated_at
		FROM skills_evaluations
		WHERE installation_id = ? AND id = ?`, string(installation), string(id))
	r, err := scanEvaluation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, faultWrap(internalError("evaluation lookup failed"), err)
	}
	return r, nil
}

// skillDependencies loads the resolved dependency pins of one version.
func skillDependencies(ctx context.Context, unit contract.Unit, installation contract.ID, id contract.ID, version int64) ([]*dependencyRow, error) {
	rows, err := unit.QueryContext(ctx, `
		SELECT installation_id, skill_id, skill_version, dep_name, dep_id, dep_version
		FROM skills_dependencies
		WHERE installation_id = ? AND skill_id = ? AND skill_version = ?
		ORDER BY dep_name`, string(installation), string(id), version)
	if err != nil {
		return nil, faultWrap(internalError("dependency query failed"), err)
	}
	defer func() { _ = rows.Close() }()
	var out []*dependencyRow
	for rows.Next() {
		d, err := scanDependency(rows)
		if err != nil {
			return nil, faultWrap(internalError("dependency row decode failed"), err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, faultWrap(internalError("dependency query failed"), err)
	}
	return out, nil
}

// evaluationRefs loads the evaluation identities recorded for one version.
func evaluationRefs(ctx context.Context, unit contract.Unit, installation contract.ID, id contract.ID, version int64) ([]contract.ID, error) {
	rows, err := unit.QueryContext(ctx, `
		SELECT id FROM skills_evaluations
		WHERE installation_id = ? AND skill_id = ? AND skill_version = ?
		ORDER BY created_at, id`, string(installation), string(id), version)
	if err != nil {
		return nil, faultWrap(internalError("evaluation query failed"), err)
	}
	defer func() { _ = rows.Close() }()
	var out []contract.ID
	for rows.Next() {
		var id contract.ID
		if err := rows.Scan(&id); err != nil {
			return nil, faultWrap(internalError("evaluation row decode failed"), err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, faultWrap(internalError("evaluation query failed"), err)
	}
	return out, nil
}

// insertDependency writes one resolved dependency pin.
func insertDependency(ctx context.Context, unit contract.Unit, d dependencyRow) error {
	_, err := unit.ExecContext(ctx, `
		INSERT INTO skills_dependencies
			(installation_id, skill_id, skill_version, dep_name, dep_id, dep_version)
		VALUES (?, ?, ?, ?, ?, ?)`,
		string(d.InstallationID), string(d.SkillID), d.SkillVersion, d.DepName,
		string(d.DepID), d.DepVersion)
	if err != nil {
		return faultWrap(internalError("dependency persistence failed"), err)
	}
	return nil
}

// wireSkillOf builds the wire Skill resource for a stored version. The
// dependency list comes from the normalized table so the wire body always
// reflects the resolved pins, and evaluation references are joined live.
func wireSkillOf(ctx context.Context, unit contract.Unit, r *skillRow) (wireSkill, error) {
	deps, err := skillDependencies(ctx, unit, r.InstallationID, r.ID, r.Version)
	if err != nil {
		return wireSkill{}, err
	}
	refs := make([]wireRef, 0, len(deps))
	for _, d := range deps {
		refs = append(refs, wireRef{ID: d.DepID, Version: contract.Version(d.DepVersion)})
	}
	evals, err := evaluationRefs(ctx, unit, r.InstallationID, r.ID, r.Version)
	if err != nil {
		return wireSkill{}, err
	}
	if evals == nil {
		evals = []contract.ID{}
	}
	requirements, err := decodeJSONColumn[[]string](r.RequirementsJSON)
	if err != nil {
		return wireSkill{}, faultWrap(internalError("stored requirements are malformed"), err)
	}
	if requirements == nil {
		requirements = []string{}
	}
	return wireSkill{
		ID:                  r.ID,
		Version:             contract.Version(r.Version),
		Name:                r.Name,
		InstructionArtifact: wireArtifactRef{ID: r.InstructionArtifactID, Digest: r.InstructionDigest},
		ContentDigest:       r.ContentDigest,
		InputSchema:         json.RawMessage(nullableJSON(r.InputSchemaJSON)),
		OutputSchema:        json.RawMessage(nullableJSON(r.OutputSchemaJSON)),
		Requirements:        requirements,
		Dependencies:        refs,
		Source:              r.Source,
		License:             r.License,
		EvaluationRefs:      evals,
		Diagnostics:         []wireDiagnostic{},
	}, nil
}

// nullableJSON normalizes an empty stored JSON column to the JSON empty
// object so required wire object fields always carry a value.
func nullableJSON(raw string) string {
	if raw == "" {
		return "{}"
	}
	return raw
}
