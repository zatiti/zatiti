package connections

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Row types and storage access for the connections_ tables. All queries run
// inside the caller's Unit; no helper escapes its transaction.

// formatStamp renders a timestamp as RFC3339Nano UTC for storage.
func formatStamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// parseStamp reads a stored RFC3339 timestamp.
func parseStamp(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

// marshalStrings encodes a string slice for a JSON storage column. A nil
// slice encodes as an empty JSON array so round-trips are stable.
func marshalStrings(v []string) string {
	if v == nil {
		v = []string{}
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// scanStrings decodes a JSON string-array storage column.
func scanStrings(s string) ([]string, error) {
	if s == "" {
		return []string{}, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, fmt.Errorf("connections: decode string array: %w", err)
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// contractRow is one built-in trusted adapter contract.
type contractRow struct {
	ID                  contract.ID
	Version             int64
	Name                string
	InputSchema         json.RawMessage
	OutputSchema        json.RawMessage
	Effect              string
	Destinations        []string
	CredentialKind      string
	CostBound           wireMoney
	TimeoutSeconds      int64
	Idempotency         string
	KeyRetentionSeconds int64
	Confirmation        string
	Reconciliation      string
	Adapter             string
}

func (r contractRow) wire() wireTool {
	return wireTool(r)
}

func scanContract(scan func(dest ...any) error) (contractRow, error) {
	var r contractRow
	var destinations, costBound string
	var inputSchema, outputSchema string
	if err := scan(&r.ID, &r.Version, &r.Name, &inputSchema, &outputSchema, &r.Effect,
		&destinations, &r.CredentialKind, &costBound, &r.TimeoutSeconds, &r.Idempotency,
		&r.KeyRetentionSeconds, &r.Confirmation, &r.Reconciliation, &r.Adapter); err != nil {
		return contractRow{}, err
	}
	r.InputSchema = json.RawMessage(inputSchema)
	r.OutputSchema = json.RawMessage(outputSchema)
	var err error
	if r.Destinations, err = scanStrings(destinations); err != nil {
		return contractRow{}, err
	}
	if err := json.Unmarshal([]byte(costBound), &r.CostBound); err != nil {
		return contractRow{}, fmt.Errorf("connections: decode cost bound: %w", err)
	}
	return r, nil
}

func (s *Service) loadContract(ctx context.Context, unit contract.Unit, id contract.ID) (contractRow, bool, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT id, version, name, input_schema, output_schema, effect, destinations_json,
		       credential_kind, cost_bound_json, timeout_seconds, idempotency,
		       key_retention_seconds, confirmation, reconciliation, adapter
		FROM connections_contracts WHERE id = ?`, id)
	r, err := scanContract(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return contractRow{}, false, nil
		}
		return contractRow{}, false, err
	}
	return r, true, nil
}

func (s *Service) listContracts(ctx context.Context, unit contract.Unit, limit int) ([]contractRow, error) {
	rows, err := unit.QueryContext(ctx, `
		SELECT id, version, name, input_schema, output_schema, effect, destinations_json,
		       credential_kind, cost_bound_json, timeout_seconds, idempotency,
		       key_retention_seconds, confirmation, reconciliation, adapter
		FROM connections_contracts ORDER BY name LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("connections: list contracts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []contractRow
	for rows.Next() {
		r, err := scanContract(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("connections: iterate contracts: %w", err)
	}
	return out, nil
}

// connectionRow is one applied connection definition plus its private
// lifecycle state.
type connectionRow struct {
	ID              contract.ID
	Version         int64
	Scope           contract.Scope
	Provider        string
	AccountIdentity string
	CredentialRef   string
	Destinations    []string
	AllowedScopes   []string
	ValidationState string
	LifecycleState  string
	ValidatedAt     *time.Time
	ValidUntil      *time.Time
}

func scanConnection(scan func(dest ...any) error) (connectionRow, error) {
	var r connectionRow
	var scopeJSON, destinations, allowedScopes string
	var validatedAt, validUntil *string
	if err := scan(&r.ID, &r.Version, &scopeJSON, &r.Provider, &r.AccountIdentity,
		&r.CredentialRef, &destinations, &allowedScopes, &r.ValidationState,
		&r.LifecycleState, &validatedAt, &validUntil); err != nil {
		return connectionRow{}, err
	}
	var err error
	if err = json.Unmarshal([]byte(scopeJSON), &r.Scope); err != nil {
		return connectionRow{}, fmt.Errorf("connections: decode scope: %w", err)
	}
	if r.Destinations, err = scanStrings(destinations); err != nil {
		return connectionRow{}, err
	}
	if r.AllowedScopes, err = scanStrings(allowedScopes); err != nil {
		return connectionRow{}, err
	}
	parse := func(p *string) (*time.Time, error) {
		if p == nil {
			return nil, nil
		}
		t, err := parseStamp(*p)
		if err != nil {
			return nil, fmt.Errorf("connections: decode timestamp: %w", err)
		}
		return &t, nil
	}
	if r.ValidatedAt, err = parse(validatedAt); err != nil {
		return connectionRow{}, err
	}
	if r.ValidUntil, err = parse(validUntil); err != nil {
		return connectionRow{}, err
	}
	return r, nil
}

const connectionColumns = `id, version, scope_json, provider, account_identity, credential_ref,
	destinations_json, allowed_scopes_json, validation_state, lifecycle_state, validated_at, valid_until`

func (s *Service) loadConnection(ctx context.Context, unit contract.Unit, id contract.ID) (connectionRow, bool, error) {
	row := unit.QueryRowContext(ctx,
		`SELECT `+connectionColumns+` FROM connections_connections WHERE id = ?`, id)
	r, err := scanConnection(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return connectionRow{}, false, nil
		}
		return connectionRow{}, false, err
	}
	return r, true, nil
}

// listConnectionsPage reads the installation's connections ordered by id,
// skipping offset rows. Scope and structured filters apply in SQL before the
// limit.
func (s *Service) listConnectionsPage(ctx context.Context, unit contract.Unit, filter listFilter, offset, limit int) ([]connectionRow, error) {
	where, args := connectionFilterWhere(unit, filter)
	// SQL order is LIMIT ? OFFSET ?.
	args = append(args, limit, offset)
	rows, err := unit.QueryContext(ctx,
		`SELECT `+connectionColumns+` FROM connections_connections WHERE `+where+
			` ORDER BY id LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("connections: list connections: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []connectionRow
	for rows.Next() {
		r, err := scanConnection(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("connections: iterate connections: %w", err)
	}
	return out, nil
}

// challengeRow is one typed credential setup challenge, bound to the
// initiating principal, account and connection.
type challengeRow struct {
	ID                contract.ID
	Version           int64
	InstallationID    contract.ID
	ConnectionID      contract.ID
	ConnectionVersion int64
	Method            string
	PrincipalID       contract.ID
	AccountIdentity   string
	State             string
	ExpiresAt         time.Time
	ConsentURL        string
	HelperRef         string
	Requirements      []wireRequirement
}

func scanChallenge(scan func(dest ...any) error) (challengeRow, error) {
	var r challengeRow
	var expiresAt string
	var consentURL, helperRef, requirements *string
	if err := scan(&r.ID, &r.Version, &r.InstallationID, &r.ConnectionID, &r.ConnectionVersion,
		&r.Method, &r.PrincipalID, &r.AccountIdentity, &r.State, &expiresAt,
		&consentURL, &helperRef, &requirements); err != nil {
		return challengeRow{}, err
	}
	var err error
	if r.ExpiresAt, err = parseStamp(expiresAt); err != nil {
		return challengeRow{}, fmt.Errorf("connections: decode expiry: %w", err)
	}
	if consentURL != nil {
		r.ConsentURL = *consentURL
	}
	if helperRef != nil {
		r.HelperRef = *helperRef
	}
	if requirements != nil && *requirements != "" {
		if err := json.Unmarshal([]byte(*requirements), &r.Requirements); err != nil {
			return challengeRow{}, fmt.Errorf("connections: decode requirements: %w", err)
		}
	}
	return r, nil
}

const challengeColumns = `id, version, installation_id, connection_id, connection_version,
	method, principal_id, account_identity, state, expires_at, consent_url, helper_ref, requirements_json`

func (s *Service) loadChallenge(ctx context.Context, unit contract.Unit, id contract.ID) (challengeRow, bool, error) {
	row := unit.QueryRowContext(ctx,
		`SELECT `+challengeColumns+` FROM connections_challenges WHERE id = ?`, id)
	r, err := scanChallenge(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return challengeRow{}, false, nil
		}
		return challengeRow{}, false, err
	}
	return r, true, nil
}

// liveChallengeExists reports whether the connection already carries a
// challenge still awaiting its outcome (pending or external_action_required).
func (s *Service) liveChallengeExists(ctx context.Context, unit contract.Unit, connectionID contract.ID) (bool, error) {
	var n int64
	err := unit.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM connections_challenges
		WHERE connection_id = ? AND state IN ('pending', 'external_action_required')`,
		connectionID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("connections: check live challenge: %w", err)
	}
	return n > 0, nil
}

// observationRow is one recorded validation probe observation.
type observationRow struct {
	ID                contract.ID
	InstallationID    contract.ID
	ConnectionID      contract.ID
	ConnectionVersion int64
	Disposition       string
	ObservedAccount   string
	ObservedScopes    []string
	Evidence          json.RawMessage
	Usage             *wireUsage
	ProviderReference string
	ConfirmedAt       time.Time
}

func (s *Service) insertObservation(ctx context.Context, unit contract.Unit, o observationRow) error {
	usageJSON := ""
	if o.Usage != nil {
		raw, err := json.Marshal(o.Usage)
		if err != nil {
			return internalError("observation usage encoding failed")
		}
		usageJSON = string(raw)
	}
	evidence := "{}"
	if len(o.Evidence) > 0 {
		evidence = string(o.Evidence)
	}
	_, err := unit.ExecContext(ctx, `
		INSERT INTO connections_validations
			(id, installation_id, connection_id, connection_version, disposition,
			 observed_account, observed_scopes_json, evidence_json, usage_json,
			 provider_reference, confirmed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(o.ID), string(o.InstallationID), string(o.ConnectionID), o.ConnectionVersion,
		o.Disposition, o.ObservedAccount, marshalStrings(o.ObservedScopes), evidence,
		usageJSON, o.ProviderReference, formatStamp(o.ConfirmedAt))
	if err != nil {
		return fmt.Errorf("connections: insert observation: %w", err)
	}
	return nil
}

// appliedPlanRow is the activation replay fence: a plan id applied exactly
// once with its parameters and produced versions.
type appliedPlanRow struct {
	PlanID          contract.ID
	CandidateDigest string
	BaseRevision    int64
	Versions        []wireRef
	AppliedAt       time.Time
}

func (s *Service) loadAppliedPlan(ctx context.Context, unit contract.Unit, planID contract.ID) (appliedPlanRow, bool, error) {
	var r appliedPlanRow
	var versions string
	var appliedAt string
	err := unit.QueryRowContext(ctx, `
		SELECT plan_id, candidate_digest, base_revision, versions_json, applied_at
		FROM connections_applied_plans WHERE plan_id = ?`, planID).
		Scan(&r.PlanID, &r.CandidateDigest, &r.BaseRevision, &versions, &appliedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return appliedPlanRow{}, false, nil
		}
		return appliedPlanRow{}, false, err
	}
	if err := json.Unmarshal([]byte(versions), &r.Versions); err != nil {
		return appliedPlanRow{}, false, fmt.Errorf("connections: decode applied versions: %w", err)
	}
	t, err := parseStamp(appliedAt)
	if err != nil {
		return appliedPlanRow{}, false, fmt.Errorf("connections: decode applied timestamp: %w", err)
	}
	r.AppliedAt = t
	return r, true, nil
}

func (s *Service) insertAppliedPlan(ctx context.Context, unit contract.Unit, r appliedPlanRow) error {
	raw, err := json.Marshal(r.Versions)
	if err != nil {
		return internalError("applied versions encoding failed")
	}
	_, err = unit.ExecContext(ctx, `
		INSERT INTO connections_applied_plans (plan_id, candidate_digest, base_revision, versions_json, applied_at)
		VALUES (?, ?, ?, ?, ?)`,
		string(r.PlanID), r.CandidateDigest, r.BaseRevision, string(raw), formatStamp(r.AppliedAt))
	if err != nil {
		return fmt.Errorf("connections: insert applied plan: %w", err)
	}
	return nil
}

// emit appends one state-correlated event to the outbox in the same
// transaction. Data stays a small summary: never secrets or provider bodies.
func (s *Service) emit(ctx context.Context, unit contract.Unit, kind string, resourceID contract.ID, resourceVersion int64, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return internalError("event encoding failed")
	}
	return unit.Emit(ctx, contract.Event{
		ID:              s.ids.New(),
		At:              s.clock.Now(),
		Scope:           unit.Scope(),
		Kind:            kind,
		ResourceID:      resourceID,
		ResourceVersion: contract.Version(resourceVersion),
		Data:            raw,
	})
}

// Cursor encoding. A cursor binds the requesting principal and the exact
// query (filters plus limit) so a replayed or forwarded page cannot widen a
// snapshot.

type cursorPayload struct {
	Offset  int         `json:"offset"`
	QueryID string      `json:"q"`
	Actor   contract.ID `json:"actor"`
}

// queryID derives the query fingerprint for cursor binding.
func queryID(filter listFilter, limit int) string {
	raw, err := json.Marshal(struct {
		Filter listFilter `json:"filter"`
		Limit  int        `json:"limit"`
	}{filter, limit})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func encodeCursor(p cursorPayload) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", internalError("cursor encoding failed")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeCursor(s string) (cursorPayload, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return cursorPayload{}, cursorExpired("cursor is malformed")
	}
	var p cursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return cursorPayload{}, cursorExpired("cursor is malformed")
	}
	return p, nil
}
