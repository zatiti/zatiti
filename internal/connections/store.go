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

// loadContractByName resolves a built-in tool contract by its stable name
// (connections_contracts_name_idx is unique), the connections-local
// provider-to-adapter selection key.
func (s *Service) loadContractByName(ctx context.Context, unit contract.Unit, name string) (contractRow, bool, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT id, version, name, input_schema, output_schema, effect, destinations_json,
		       credential_kind, cost_bound_json, timeout_seconds, idempotency,
		       key_retention_seconds, confirmation, reconciliation, adapter
		FROM connections_contracts WHERE name = ?`, name)
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

// pendingProbeRow is the callback-ownership record linking one outstanding
// connection.validate/connection.rotate job to the connection it probes.
// _connections.validation.record consumes it to complete the execution-side
// job once the observation lands (implementation assignment step 4).
type pendingProbeRow struct {
	ConnectionID contract.ID
	JobID        contract.ID
	JobVersion   int64
	Kind         string
}

// recordPendingProbe upserts the callback-ownership row for connectionID. A
// second validate/rotate issued before the first completes replaces the
// pointer: only the most recently admitted job is completed by a later
// observation, the same single-live-item discipline liveChallengeExists
// enforces for setup challenges.
func (s *Service) recordPendingProbe(ctx context.Context, unit contract.Unit, r pendingProbeRow, now time.Time) error {
	_, err := unit.ExecContext(ctx, `
		INSERT INTO connections_pending_probes (connection_id, job_id, job_version, kind, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(connection_id) DO UPDATE SET
			job_id = excluded.job_id, job_version = excluded.job_version,
			kind = excluded.kind, created_at = excluded.created_at`,
		string(r.ConnectionID), string(r.JobID), r.JobVersion, r.Kind, formatStamp(now))
	if err != nil {
		return fmt.Errorf("connections: record pending probe: %w", err)
	}
	return nil
}

// loadPendingProbe reads the callback-ownership row for connectionID, if any.
func (s *Service) loadPendingProbe(ctx context.Context, unit contract.Unit, connectionID contract.ID) (pendingProbeRow, bool, error) {
	var r pendingProbeRow
	err := unit.QueryRowContext(ctx, `
		SELECT connection_id, job_id, job_version, kind
		FROM connections_pending_probes WHERE connection_id = ?`, string(connectionID)).
		Scan(&r.ConnectionID, &r.JobID, &r.JobVersion, &r.Kind)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return pendingProbeRow{}, false, nil
		}
		return pendingProbeRow{}, false, fmt.Errorf("connections: load pending probe: %w", err)
	}
	return r, true, nil
}

// deletePendingProbe clears the callback-ownership row once its job has been
// completed, so a later observation for the same connection cannot replay a
// completion against a terminal job.
func (s *Service) deletePendingProbe(ctx context.Context, unit contract.Unit, connectionID contract.ID) error {
	if _, err := unit.ExecContext(ctx,
		`DELETE FROM connections_pending_probes WHERE connection_id = ?`, string(connectionID)); err != nil {
		return fmt.Errorf("connections: delete pending probe: %w", err)
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

// mcpToolRow is one recorded MCP discovered-tool catalog entry.
type mcpToolRow struct {
	ConnectionID         contract.ID
	Name                 string
	Title                string
	Description          string
	InputSchema          json.RawMessage
	InputSchemaDigest    string
	OutputSchema         json.RawMessage // nil when the server omitted it
	Annotations          json.RawMessage
	DiscoveredAt         time.Time
	DiscoveryOperationID contract.ID
	Stale                bool
}

func (r mcpToolRow) wire() wireMCPDiscoveredTool {
	out := wireMCPDiscoveredTool{
		Name:                 r.Name,
		Title:                r.Title,
		Description:          r.Description,
		InputSchema:          r.InputSchema,
		InputSchemaDigest:    r.InputSchemaDigest,
		DiscoveredAt:         r.DiscoveredAt,
		DiscoveryOperationID: r.DiscoveryOperationID,
	}
	if len(r.OutputSchema) > 0 && string(r.OutputSchema) != "null" {
		out.OutputSchema = r.OutputSchema
	}
	if len(r.Annotations) > 0 && string(r.Annotations) != "{}" && string(r.Annotations) != "null" {
		out.Annotations = r.Annotations
	}
	return out
}

// upsertMCPTool inserts or replaces one discovered tool row for the
// connection, clearing the stale bit so a re-advertised name is current.
func (s *Service) upsertMCPTool(ctx context.Context, unit contract.Unit, r mcpToolRow) error {
	outSchema := ""
	if len(r.OutputSchema) > 0 && string(r.OutputSchema) != "null" {
		outSchema = string(r.OutputSchema)
	}
	annotations := "{}"
	if len(r.Annotations) > 0 {
		annotations = string(r.Annotations)
	}
	_, err := unit.ExecContext(ctx, `
		INSERT INTO connections_mcp_tools
			(connection_id, name, title, description, input_schema, input_schema_digest,
			 output_schema, annotations_json, discovered_at, discovery_operation_id, stale)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
		ON CONFLICT(connection_id, name) DO UPDATE SET
			title = excluded.title,
			description = excluded.description,
			input_schema = excluded.input_schema,
			input_schema_digest = excluded.input_schema_digest,
			output_schema = excluded.output_schema,
			annotations_json = excluded.annotations_json,
			discovered_at = excluded.discovered_at,
			discovery_operation_id = excluded.discovery_operation_id,
			stale = 0`,
		string(r.ConnectionID), r.Name, r.Title, r.Description,
		string(r.InputSchema), r.InputSchemaDigest, nullIfEmpty(outSchema), annotations,
		formatStamp(r.DiscoveredAt), string(r.DiscoveryOperationID))
	if err != nil {
		return fmt.Errorf("connections: upsert mcp tool: %w", err)
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// markMCPToolsStaleExcept marks every tool for connectionID whose name is
// not in keep as stale. A complete catalog page (no next_cursor) uses this
// so tools the server no longer advertises stop resolving.
func (s *Service) markMCPToolsStaleExcept(ctx context.Context, unit contract.Unit, connectionID contract.ID, keep []string) (retErr error) {
	keepSet := make(map[string]struct{}, len(keep))
	for _, n := range keep {
		keepSet[n] = struct{}{}
	}
	rows, err := unit.QueryContext(ctx, `
		SELECT name FROM connections_mcp_tools WHERE connection_id = ? AND stale = 0`,
		string(connectionID))
	if err != nil {
		return fmt.Errorf("connections: list mcp tools for stale mark: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("connections: close mcp tool rows: %w", err)
		}
	}()
	var stale []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("connections: scan mcp tool name: %w", err)
		}
		if _, ok := keepSet[name]; !ok {
			stale = append(stale, name)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("connections: iterate mcp tools: %w", err)
	}
	for _, name := range stale {
		if _, err := unit.ExecContext(ctx, `
			UPDATE connections_mcp_tools SET stale = 1
			WHERE connection_id = ? AND name = ?`, string(connectionID), name); err != nil {
			return fmt.Errorf("connections: mark mcp tool stale: %w", err)
		}
	}
	return nil
}

// listMCPToolsPage returns a name-ordered page of every recorded tool for the
// connection, including stale rows so operators can inspect them.
func (s *Service) listMCPToolsPage(ctx context.Context, unit contract.Unit, connectionID contract.ID, offset, limit int) (result []mcpToolRow, retErr error) {
	rows, err := unit.QueryContext(ctx, `
		SELECT connection_id, name, title, description, input_schema, input_schema_digest,
			output_schema, annotations_json, discovered_at, discovery_operation_id, stale
		FROM connections_mcp_tools
		WHERE connection_id = ?
		ORDER BY name ASC
		LIMIT ? OFFSET ?`,
		string(connectionID), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("connections: list mcp tools: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("connections: close mcp tool rows: %w", err)
		}
	}()
	var out []mcpToolRow
	for rows.Next() {
		r, serr := scanMCPTool(rows.Scan)
		if serr != nil {
			return nil, serr
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("connections: iterate mcp tools: %w", err)
	}
	return out, nil
}

func scanMCPTool(scan func(dest ...any) error) (mcpToolRow, error) {
	var r mcpToolRow
	var outSchema sql.NullString
	var annotations, discoveredAt string
	var staleInt int
	var inputSchema string
	if err := scan(&r.ConnectionID, &r.Name, &r.Title, &r.Description, &inputSchema,
		&r.InputSchemaDigest, &outSchema, &annotations, &discoveredAt,
		&r.DiscoveryOperationID, &staleInt); err != nil {
		return mcpToolRow{}, err
	}
	r.InputSchema = json.RawMessage(inputSchema)
	if outSchema.Valid {
		r.OutputSchema = json.RawMessage(outSchema.String)
	}
	r.Annotations = json.RawMessage(annotations)
	var err error
	if r.DiscoveredAt, err = parseStamp(discoveredAt); err != nil {
		return mcpToolRow{}, fmt.Errorf("connections: parse mcp tool discovered_at: %w", err)
	}
	r.Stale = staleInt != 0
	return r, nil
}

// loadLatestSucceededSessionHandle returns the session_handle from the most
// recent succeeded validation observation evidence for connectionID. Used by
// connection.discover to build list_tools when the discover input itself
// carries no session_handle (a frozen-schema gap reported as a contract
// defect). An absent handle is ok=false, never an invented value.
func (s *Service) loadLatestSucceededSessionHandle(ctx context.Context, unit contract.Unit, connectionID contract.ID) (string, bool, error) {
	row := unit.QueryRowContext(ctx, `
		SELECT evidence_json FROM connections_validations
		WHERE connection_id = ? AND disposition = 'succeeded'
		ORDER BY confirmed_at DESC LIMIT 1`, string(connectionID))
	var evidence string
	if err := row.Scan(&evidence); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("connections: load latest validation evidence: %w", err)
	}
	var body struct {
		SessionHandle string `json:"session_handle"`
	}
	if err := json.Unmarshal([]byte(evidence), &body); err != nil {
		return "", false, nil
	}
	if body.SessionHandle == "" {
		return "", false, nil
	}
	return body.SessionHandle, true, nil
}
