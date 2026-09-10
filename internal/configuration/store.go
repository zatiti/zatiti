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

// Row types for the configuration_ tables. JSON columns stay as strings and
// are (un)marshaled at the edges; every row carries its optimistic version.

type orgRow struct {
	ID             contract.ID
	Version        int64
	InstallationID contract.ID
	Key            string
	Name           string
	ChiefID        contract.ID
	ParentID       contract.ID // empty when root
	LimitsJSON     string
	ExtensionsJSON string
	State          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type teamRow struct {
	ID             contract.ID
	Version        int64
	InstallationID contract.ID
	OrganizationID contract.ID
	Key            string
	Name           string
	WorkerIDsJSON  string
	ExtensionsJSON string
	State          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type projectRow struct {
	ID             contract.ID
	Version        int64
	InstallationID contract.ID
	OrganizationID contract.ID
	Key            string
	Name           string
	Repositories   []string
	BindingsJSON   string
	Classification string
	LimitsJSON     string
	ExtensionsJSON string
	State          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type workerRow struct {
	ID             contract.ID
	Version        int64
	InstallationID contract.ID
	OrganizationID contract.ID
	Key            string
	Name           string
	Purpose        string
	Instructions   string
	SkillVersions  []wireRef
	BindingsJSON   string
	ProfileJSON    string
	LimitsJSON     string
	ExtensionsJSON string
	State          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type bindingRow struct {
	ID              contract.ID
	Version         int64
	InstallationID  contract.ID
	ScopeJSON       string
	Kind            string
	TargetID        contract.ID
	Permissions     []string
	SourceScopeJSON string
	Destinations    []string
	State           string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type profileRow struct {
	ID                  contract.ID
	Version             int64
	InstallationID      contract.ID
	Executor            string
	Model               string
	ConnectionID        contract.ID
	ProviderDestination string
	Capabilities        []string
	CostBoundJSON       string
	Classification      string
	ContextCapture      string
	State               string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type draftRow struct {
	ID             contract.ID
	Version        int64
	InstallationID contract.ID
	OrganizationID contract.ID
	BaseRevision   int64
	ChangesJSON    string
	Diagnostics    []wireDiagnostic
	State          string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type planRow struct {
	ID              contract.ID
	Version         int64
	DraftID         contract.ID
	InstallationID  contract.ID
	OrganizationID  contract.ID
	BaseRevision    int64
	CandidateDigest string
	ChangesJSON     string
	Dependencies    []wireRef
	CompilerVersion string
	SchemaVersion   string
	AuthorityReqs   []wireRequirement
	Decisions       []wireDecisionRequirement
	Diagnostics     []wireDiagnostic
	Requirements    []wireRequirement
	State           string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type revisionRow struct {
	ID              contract.ID
	Version         int64
	InstallationID  contract.ID
	PlanID          contract.ID
	CandidateDigest string
	ActivatedAt     time.Time
}

// revisionObject is one owned object touched by a revision, with its
// before/after definitions for rollback planning.
type revisionObject struct {
	Kind       string
	ObjectID   contract.ID
	Action     string
	BeforeJSON string
	AfterJSON  string
}

const timeLayout = time.RFC3339Nano

// rowScanner abstracts *sql.Row and *sql.Rows so point lookups and list
// scans share one decode path per table.
type rowScanner interface {
	Scan(dest ...any) error
}

// normJSON collapses SQL NULL and the JSON null literal to the empty string
// so optional object columns round-trip as absent.
func normJSON(s string) string {
	if s == "" || s == "null" {
		return ""
	}
	return s
}

// jsonOrNull collapses empty and JSON-null marshaled values to SQL NULL.
func jsonOrNull(raw string) any {
	if normJSON(raw) == "" {
		return nil
	}
	return raw
}

// textOrNull collapses the empty string to SQL NULL.
func textOrNull(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func scanOrg(row rowScanner) (*orgRow, error) {
	var r orgRow
	var parent, limits, ext sql.NullString
	var created, updated string
	err := row.Scan(&r.ID, &r.Version, &r.InstallationID, &r.Key, &r.Name, &r.ChiefID,
		&parent, &limits, &ext, &r.State, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.ParentID = contract.ID(parent.String)
	r.LimitsJSON = normJSON(limits.String)
	r.ExtensionsJSON = normJSON(ext.String)
	if r.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, err
	}
	if r.UpdatedAt, err = time.Parse(timeLayout, updated); err != nil {
		return nil, err
	}
	return &r, nil
}

func scanTeam(row rowScanner) (*teamRow, error) {
	var r teamRow
	var ext sql.NullString
	var created, updated string
	err := row.Scan(&r.ID, &r.Version, &r.InstallationID, &r.OrganizationID, &r.Key, &r.Name,
		&r.WorkerIDsJSON, &ext, &r.State, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.ExtensionsJSON = normJSON(ext.String)
	if r.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, err
	}
	if r.UpdatedAt, err = time.Parse(timeLayout, updated); err != nil {
		return nil, err
	}
	return &r, nil
}

func scanProject(row rowScanner) (*projectRow, error) {
	var r projectRow
	var repositories, limits, ext sql.NullString
	var created, updated string
	err := row.Scan(&r.ID, &r.Version, &r.InstallationID, &r.OrganizationID, &r.Key, &r.Name,
		&repositories, &r.BindingsJSON, &r.Classification, &limits, &ext, &r.State, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if repositories.String != "" {
		if err := json.Unmarshal([]byte(repositories.String), &r.Repositories); err != nil {
			return nil, err
		}
	}
	r.LimitsJSON = normJSON(limits.String)
	r.ExtensionsJSON = normJSON(ext.String)
	if r.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, err
	}
	if r.UpdatedAt, err = time.Parse(timeLayout, updated); err != nil {
		return nil, err
	}
	return &r, nil
}

func scanWorker(row rowScanner) (*workerRow, error) {
	var r workerRow
	var skillVersions, profile, limits, ext sql.NullString
	var created, updated string
	err := row.Scan(&r.ID, &r.Version, &r.InstallationID, &r.OrganizationID, &r.Key, &r.Name,
		&r.Purpose, &r.Instructions, &skillVersions, &r.BindingsJSON, &profile, &limits, &ext,
		&r.State, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if skillVersions.String != "" {
		if err := json.Unmarshal([]byte(skillVersions.String), &r.SkillVersions); err != nil {
			return nil, err
		}
	}
	r.ProfileJSON = normJSON(profile.String)
	r.LimitsJSON = normJSON(limits.String)
	r.ExtensionsJSON = normJSON(ext.String)
	if r.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, err
	}
	if r.UpdatedAt, err = time.Parse(timeLayout, updated); err != nil {
		return nil, err
	}
	return &r, nil
}

func scanBinding(row rowScanner) (*bindingRow, error) {
	var r bindingRow
	var permissions, destinations, source sql.NullString
	var created, updated string
	err := row.Scan(&r.ID, &r.Version, &r.InstallationID, &r.ScopeJSON, &r.Kind, &r.TargetID,
		&permissions, &source, &destinations, &r.State, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if permissions.String != "" {
		if err := json.Unmarshal([]byte(permissions.String), &r.Permissions); err != nil {
			return nil, err
		}
	}
	if destinations.Valid {
		if err := json.Unmarshal([]byte(destinations.String), &r.Destinations); err != nil {
			return nil, err
		}
	}
	r.SourceScopeJSON = normJSON(source.String)
	if r.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, err
	}
	if r.UpdatedAt, err = time.Parse(timeLayout, updated); err != nil {
		return nil, err
	}
	return &r, nil
}

func scanProfile(row rowScanner) (*profileRow, error) {
	var r profileRow
	var capabilities sql.NullString
	var created, updated string
	err := row.Scan(&r.ID, &r.Version, &r.InstallationID, &r.Executor, &r.Model, &r.ConnectionID,
		&r.ProviderDestination, &capabilities, &r.CostBoundJSON, &r.Classification,
		&r.ContextCapture, &r.State, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if capabilities.String != "" {
		if err := json.Unmarshal([]byte(capabilities.String), &r.Capabilities); err != nil {
			return nil, err
		}
	}
	if r.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, err
	}
	if r.UpdatedAt, err = time.Parse(timeLayout, updated); err != nil {
		return nil, err
	}
	return &r, nil
}

func scanDraft(row rowScanner) (*draftRow, error) {
	var r draftRow
	var diagnostics string
	var created, updated string
	err := row.Scan(&r.ID, &r.Version, &r.InstallationID, &r.OrganizationID, &r.BaseRevision,
		&r.ChangesJSON, &diagnostics, &r.State, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if diagnostics != "" {
		if err := json.Unmarshal([]byte(diagnostics), &r.Diagnostics); err != nil {
			return nil, err
		}
	}
	if r.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, err
	}
	if r.UpdatedAt, err = time.Parse(timeLayout, updated); err != nil {
		return nil, err
	}
	return &r, nil
}

func scanPlan(row rowScanner) (*planRow, error) {
	var r planRow
	var dependencies, authority, decisions, diagnostics, requirements string
	var created, updated string
	err := row.Scan(&r.ID, &r.Version, &r.DraftID, &r.InstallationID, &r.OrganizationID,
		&r.BaseRevision, &r.CandidateDigest, &r.ChangesJSON, &dependencies, &r.CompilerVersion,
		&r.SchemaVersion, &authority, &decisions, &diagnostics, &requirements,
		&r.State, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(dependencies), &r.Dependencies); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(authority), &r.AuthorityReqs); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(decisions), &r.Decisions); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(diagnostics), &r.Diagnostics); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(requirements), &r.Requirements); err != nil {
		return nil, err
	}
	if r.CreatedAt, err = time.Parse(timeLayout, created); err != nil {
		return nil, err
	}
	if r.UpdatedAt, err = time.Parse(timeLayout, updated); err != nil {
		return nil, err
	}
	return &r, nil
}

// readHead returns the current configuration revision counter.
func readHead(ctx context.Context, unit contract.Unit) (int64, error) {
	var head int64
	err := unit.QueryRowContext(ctx, "SELECT revision FROM configuration_head WHERE id = 1").Scan(&head)
	if err != nil {
		return 0, err
	}
	return head, nil
}

func bumpHead(ctx context.Context, unit contract.Unit, next int64) error {
	_, err := unit.ExecContext(ctx, "UPDATE configuration_head SET revision = ? WHERE id = 1", next)
	return err
}

// draftChanges decodes the staged change list of a draft or plan.
func draftChanges(raw string) ([]wireChange, error) {
	if raw == "" {
		return nil, nil
	}
	var changes []wireChange
	if err := json.Unmarshal([]byte(raw), &changes); err != nil {
		return nil, err
	}
	return changes, nil
}

func marshalChanges(changes []wireChange) (string, error) {
	if len(changes) == 0 {
		return "[]", nil
	}
	raw, err := json.Marshal(changes)
	if err != nil {
		return "", internalError("change encoding failed")
	}
	return string(raw), nil
}

func marshalDiagnostics(diags []wireDiagnostic) string {
	if len(diags) == 0 {
		return "[]"
	}
	raw, err := json.Marshal(diags)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func marshalJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(raw)
}

// rawDef marshals a change definition into inert JSON bytes; definitions
// travel inside wireChange as opaque RawMessage.
func rawDef(def any) json.RawMessage {
	return json.RawMessage(marshalJSON(def))
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

// cursorPayload is the decoded pagination cursor. The signature binds the
// cursor to the principal, operation and query so a cursor minted for one
// query cannot be replayed against another.
type cursorPayload struct {
	Offset int64  `json:"offset"`
	Sig    string `json:"sig"`
}

// cursorSignature binds the cursor to the principal, installation, operation
// and exact query fingerprint, so a cursor minted for one filter or caller
// cannot be replayed against another.
func cursorSignature(unit contract.Unit, op, fingerprint string) string {
	sum := contract.Hash([]byte(strings.Join([]string{
		string(unit.Actor().PrincipalID), string(unit.Scope().InstallationID), op, fingerprint,
	}, "|")))
	return string(sum[:16])
}
