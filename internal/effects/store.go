package effects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Storage rows and access helpers. All timestamps persist as UTC RFC3339Nano
// strings and all JSON documents persist in canonical form, matching the
// shared storage conventions. Actions and observations are append only;
// version-fenced updates drive operations and attempts.

// isNoRows reports whether err is the standard empty-scan result.
func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}

// formatStamp renders a UTC RFC3339Nano timestamp.
func formatStamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// parseStamp parses a stored UTC RFC3339Nano timestamp.
func parseStamp(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

// Logical operation states per R10-003.
const (
	opStatePrepared             = "prepared"
	opStateAwaitingReview       = "awaiting_review"
	opStateReady                = "ready"
	opStateExecuting            = "executing"
	opStateAwaitingConfirmation = "awaiting_confirmation"
	opStateOutcomeUnknown       = "outcome_unknown"
	opStateSucceeded            = "succeeded"
	opStateFailed               = "failed"
	opStateDenied               = "denied"
	opStateExpired              = "expired"
	opStateCancelled            = "cancelled"
)

// Attempt states: intent persisted, dispatch claim consumed, observation
// recorded. claimable marks the claimable set; unsent marks states where no
// earlier attempt can still take effect.
const (
	attemptStatePrepared = "prepared"
	attemptStateClaimed  = "claimed"
	attemptStateRecorded = "recorded"
)

// Attempt kinds. A dispatch attempt is the original mutation; a
// reconciliation attempt is a separately admitted bounded read linked to a
// prior dispatch attempt (R10-008, P00-007). Claiming a reconciliation
// attempt never advances the operation to executing, and only
// _effects.reconciliation.record may record its first observation.
const (
	attemptKindDispatch       = "dispatch"
	attemptKindReconciliation = "reconciliation"
)

// Observation kinds. The first observation of an attempt is physical;
// correction and dispute append contradictory late evidence without
// rewriting history.
const (
	obsKindPhysical   = "physical"
	obsKindCorrection = "correction"
	obsKindDispute    = "dispute"
)

// Obligation kinds tracked in effects_obligations.
const (
	oblReconcile = "reconcile"
	oblConfirm   = "confirm"
	oblDispute   = "dispute"
)

// Observation dispositions mirror the shared contract enum.
const (
	dispSucceeded = "succeeded"
	dispFailed    = "failed"
	dispAccepted  = "accepted"
	dispUnknown   = "unknown"
	dispNotSent   = "not_sent"
)

// operationRow is one logical effect: immutable action reference, state
// machine position and links to related operations.
type operationRow struct {
	ID                       contract.ID
	Version                  int64
	InstallID                contract.ID
	OrganizationID           contract.ID
	ProjectID                contract.ID
	WorkerID                 contract.ID
	TaskID                   contract.ID
	ActionID                 contract.ID
	ActionDigest             string
	SourceKey                string
	State                    string
	LinkedOperation          contract.ID
	Relationship             string
	JobID                    contract.ID
	AttemptCount             int64
	CallbackRouteJSON        string
	AdapterProfileJSON       string
	ProfileConnectionVersion int64
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

const operationColumns = `id, version, installation_id, organization_id, project_id, worker_id, task_id,
	action_id, action_digest, source_key, state, linked_operation_id, relationship, job_id,
	attempt_count, callback_route_json, adapter_profile_json, profile_connection_version, created_at, updated_at`

func scanOperation(scan func(dest ...any) error) (*operationRow, error) {
	var o operationRow
	var organization, project, worker, task, sourceKey, linked, relationship, jobID string
	var callbackRoute, adapterProfile string
	var created, updated string
	err := scan(&o.ID, &o.Version, &o.InstallID, &organization, &project, &worker, &task,
		&o.ActionID, &o.ActionDigest, &sourceKey, &o.State, &linked, &relationship, &jobID,
		&o.AttemptCount, &callbackRoute, &adapterProfile, &o.ProfileConnectionVersion, &created, &updated)
	if err != nil {
		return nil, err
	}
	o.OrganizationID = contract.ID(organization)
	o.ProjectID = contract.ID(project)
	o.WorkerID = contract.ID(worker)
	o.TaskID = contract.ID(task)
	o.SourceKey = sourceKey
	if linked != "" {
		o.LinkedOperation = contract.ID(linked)
	}
	if relationship != "" {
		o.Relationship = relationship
	}
	if jobID != "" {
		o.JobID = contract.ID(jobID)
	}
	o.CallbackRouteJSON = callbackRoute
	o.AdapterProfileJSON = adapterProfile
	var perr error
	o.CreatedAt, perr = parseStamp(created)
	if perr != nil {
		return nil, fmt.Errorf("effects: operation %s timestamp: %w", o.ID, perr)
	}
	o.UpdatedAt, perr = parseStamp(updated)
	if perr != nil {
		return nil, fmt.Errorf("effects: operation %s timestamp: %w", o.ID, perr)
	}
	return &o, nil
}

// loadOperation returns one operation by id, or nil when absent.
func loadOperation(ctx context.Context, unit contract.Unit, id contract.ID) (*operationRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+operationColumns+` FROM effects_operations WHERE id = ?`, string(id))
	o, err := scanOperation(row.Scan)
	if isNoRows(err) {
		return nil, nil
	}
	return o, err
}

// loadOperationBySource returns the operation replaying one submission key,
// or nil.
func loadOperationBySource(ctx context.Context, unit contract.Unit, install contract.ID, sourceKey string) (*operationRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+operationColumns+` FROM effects_operations
		WHERE installation_id = ? AND source_key = ?`, string(install), sourceKey)
	o, err := scanOperation(row.Scan)
	if isNoRows(err) {
		return nil, nil
	}
	return o, err
}

// insertOperation persists a new operation row.
func insertOperation(ctx context.Context, unit contract.Unit, o *operationRow) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO effects_operations (`+operationColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(o.ID), o.Version, string(o.InstallID), string(o.OrganizationID), string(o.ProjectID),
		string(o.WorkerID), string(o.TaskID), string(o.ActionID), o.ActionDigest, o.SourceKey,
		o.State, string(o.LinkedOperation), o.Relationship, string(o.JobID),
		o.AttemptCount, o.CallbackRouteJSON, o.AdapterProfileJSON, o.ProfileConnectionVersion,
		formatStamp(o.CreatedAt), formatStamp(o.UpdatedAt))
	return err
}

// updateOperationState transitions one operation with its new version and
// optional job link, fenced on the expected current version.
func updateOperationState(ctx context.Context, unit contract.Unit, o *operationRow, newState string, jobID contract.ID, now time.Time) error {
	res, err := unit.ExecContext(ctx, `UPDATE effects_operations
		SET version = version + 1, state = ?, job_id = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		newState, string(jobID), formatStamp(now), string(o.ID), o.Version)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return staleVersion("operation %s changed concurrently", o.ID)
	}
	o.Version++
	o.State = newState
	if jobID != "" {
		o.JobID = jobID
	}
	o.UpdatedAt = now
	return nil
}

// actionRow is one immutable action bound by its canonical digest.
type actionRow struct {
	ID         contract.ID
	Version    int64
	InstallID  contract.ID
	Digest     contract.Digest
	ActionJSON string
	CreatedAt  time.Time
}

// insertAction persists one immutable action. The digest is unique, so an
// identical action replays as the same row.
func insertAction(ctx context.Context, unit contract.Unit, a *actionRow) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO effects_actions
		(id, version, installation_id, digest, action_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		string(a.ID), a.Version, string(a.InstallID), string(a.Digest), a.ActionJSON, formatStamp(a.CreatedAt))
	return err
}

// loadActionByDigest returns the action bound to one canonical digest, or
// nil when absent.
func loadActionByDigest(ctx context.Context, unit contract.Unit, install contract.ID, digest contract.Digest) (*actionRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT id, version, installation_id, digest, action_json, created_at
		FROM effects_actions WHERE installation_id = ? AND digest = ?`, string(install), string(digest))
	var a actionRow
	var created string
	err := row.Scan(&a.ID, &a.Version, &a.InstallID, &a.Digest, &a.ActionJSON, &created)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.CreatedAt, err = parseStamp(created)
	if err != nil {
		return nil, fmt.Errorf("effects: action %s timestamp: %w", a.ID, err)
	}
	return &a, nil
}

// attemptRow is one physical provider invocation. Kind separates the
// original dispatch attempt from a reconciliation bounded read; a
// reconciliation attempt's TargetAttemptID names the exact prior attempt it
// reconciles (R10-008, P00-007).
type attemptRow struct {
	ID                 contract.ID
	Version            int64
	OperationID        contract.ID
	AttemptNo          int64
	State              string
	Disposition        string
	Adapter            string
	CredentialRef      string
	Deadline           time.Time
	DispatchJSON       string
	ReservationID      contract.ID
	ReservationVersion int64
	ConnectionID       contract.ID
	ConnectionVersion  int64
	EvidenceJSON       string
	UsageJSON          string
	ProviderReference  string
	ConfirmedAt        time.Time
	Generation         int64
	Kind               string
	TargetAttemptID    contract.ID
	CreatedAt          time.Time
	RecordedAt         time.Time
}

const attemptColumns = `id, version, operation_id, attempt_no, state, disposition, adapter, credential_ref,
	deadline, dispatch_json, reservation_id, reservation_version, connection_id, connection_version,
	evidence_json, usage_json, provider_reference, confirmed_at, generation, kind, target_attempt_id,
	created_at, recorded_at`

func scanAttempt(scan func(dest ...any) error) (*attemptRow, error) {
	var a attemptRow
	var disposition, evidence, usage, providerRef, confirmed, recorded string
	var deadline, created string
	var kind, targetAttempt string
	err := scan(&a.ID, &a.Version, &a.OperationID, &a.AttemptNo, &a.State, &disposition, &a.Adapter,
		&a.CredentialRef, &deadline, &a.DispatchJSON, &a.ReservationID, &a.ReservationVersion,
		&a.ConnectionID, &a.ConnectionVersion, &evidence, &usage, &providerRef, &confirmed,
		&a.Generation, &kind, &targetAttempt, &created, &recorded)
	if err != nil {
		return nil, err
	}
	a.Disposition = disposition
	a.EvidenceJSON = evidence
	a.UsageJSON = usage
	a.ProviderReference = providerRef
	a.Kind = kind
	if targetAttempt != "" {
		a.TargetAttemptID = contract.ID(targetAttempt)
	}
	var perr error
	a.Deadline, perr = parseStamp(deadline)
	if perr != nil {
		return nil, fmt.Errorf("effects: attempt %s deadline: %w", a.ID, perr)
	}
	if confirmed != "" {
		a.ConfirmedAt, perr = parseStamp(confirmed)
		if perr != nil {
			return nil, fmt.Errorf("effects: attempt %s confirmation: %w", a.ID, perr)
		}
	}
	a.CreatedAt, perr = parseStamp(created)
	if perr != nil {
		return nil, fmt.Errorf("effects: attempt %s timestamp: %w", a.ID, perr)
	}
	if recorded != "" {
		a.RecordedAt, perr = parseStamp(recorded)
		if perr != nil {
			return nil, fmt.Errorf("effects: attempt %s record time: %w", a.ID, perr)
		}
	}
	return &a, nil
}

// loadAttempt returns one attempt by id, or nil when absent.
func loadAttempt(ctx context.Context, unit contract.Unit, id contract.ID) (*attemptRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT `+attemptColumns+` FROM effects_attempts WHERE id = ?`, string(id))
	a, err := scanAttempt(row.Scan)
	if isNoRows(err) {
		return nil, nil
	}
	return a, err
}

// listAttempts returns every attempt of one operation in dispatch order.
func listAttempts(ctx context.Context, unit contract.Unit, operationID contract.ID) ([]*attemptRow, error) {
	rows, err := unit.QueryContext(ctx, `SELECT `+attemptColumns+` FROM effects_attempts
		WHERE operation_id = ? ORDER BY attempt_no`, string(operationID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*attemptRow
	for rows.Next() {
		a, err := scanAttempt(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// lastDispatchAttempt returns the most recent dispatch-kind (original
// mutation) attempt of one operation, ignoring reconciliation reads, or nil
// when the operation has never been dispatched. Reconciliation links to this
// exact prior attempt rather than re-resolving a connection/tool.
func lastDispatchAttempt(attempts []*attemptRow) *attemptRow {
	var last *attemptRow
	for _, a := range attempts {
		if a.Kind == attemptKindReconciliation {
			continue
		}
		last = a
	}
	return last
}

// insertAttempt persists a new attempt with its dispatch intent. Kind
// defaults to attemptKindDispatch when unset, so every existing caller that
// builds an attemptRow without naming Kind keeps writing ordinary dispatch
// attempts.
func insertAttempt(ctx context.Context, unit contract.Unit, a *attemptRow) error {
	kind := a.Kind
	if kind == "" {
		kind = attemptKindDispatch
	}
	_, err := unit.ExecContext(ctx, `INSERT INTO effects_attempts (`+attemptColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(a.ID), a.Version, string(a.OperationID), a.AttemptNo, a.State, a.Disposition,
		a.Adapter, a.CredentialRef, formatStamp(a.Deadline), a.DispatchJSON,
		string(a.ReservationID), a.ReservationVersion, string(a.ConnectionID), a.ConnectionVersion,
		a.EvidenceJSON, a.UsageJSON, a.ProviderReference, formatStamp(a.ConfirmedAt),
		a.Generation, kind, string(a.TargetAttemptID), formatStamp(a.CreatedAt), formatStamp(a.RecordedAt))
	return err
}

// recordAttempt transitions one attempt to recorded with its observation,
// fenced on the expected current version.
func recordAttempt(ctx context.Context, unit contract.Unit, a *attemptRow, disposition, evidenceJSON, usageJSON, providerRef string, confirmedAt time.Time, now time.Time) error {
	res, err := unit.ExecContext(ctx, `UPDATE effects_attempts
		SET version = version + 1, state = ?, disposition = ?, evidence_json = ?, usage_json = ?,
			provider_reference = ?, confirmed_at = ?, recorded_at = ?
		WHERE id = ? AND version = ?`,
		attemptStateRecorded, disposition, evidenceJSON, usageJSON, providerRef,
		formatStamp(confirmedAt), formatStamp(now), string(a.ID), a.Version)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return staleVersion("attempt %s changed concurrently", a.ID)
	}
	a.Version++
	a.State = attemptStateRecorded
	a.Disposition = disposition
	a.EvidenceJSON = evidenceJSON
	a.UsageJSON = usageJSON
	a.ProviderReference = providerRef
	a.ConfirmedAt = confirmedAt
	a.RecordedAt = now
	return nil
}

// updateAttemptState transitions one attempt's dispatch state, fenced on its
// expected current version.
func updateAttemptState(ctx context.Context, unit contract.Unit, a *attemptRow, newState string) error {
	res, err := unit.ExecContext(ctx, `UPDATE effects_attempts
		SET version = version + 1, state = ?
		WHERE id = ? AND version = ?`, newState, string(a.ID), a.Version)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return staleVersion("attempt %s changed concurrently", a.ID)
	}
	a.Version++
	a.State = newState
	return nil
}

// bumpAttemptCount increments the denormalized attempt counter of one
// operation inside the caller's transaction.
func bumpAttemptCount(ctx context.Context, unit contract.Unit, id contract.ID) error {
	_, err := unit.ExecContext(ctx,
		`UPDATE effects_operations SET attempt_count = attempt_count + 1 WHERE id = ?`, string(id))
	return err
}

// claimRow is the one-use attempt-bound dispatch claim.
type claimRow struct {
	AttemptID  contract.ID
	Generation int64
	ExpiresAt  time.Time
	Consumed   bool
	CreatedAt  time.Time
}

// insertClaim persists the claim created at admit time.
func insertClaim(ctx context.Context, unit contract.Unit, c *claimRow) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO effects_claims
		(attempt_id, generation, expires_at, consumed, created_at)
		VALUES (?, ?, ?, 0, ?)`,
		string(c.AttemptID), c.Generation, formatStamp(c.ExpiresAt), formatStamp(c.CreatedAt))
	return err
}

// loadClaim returns the claim of one attempt, or nil when absent.
func loadClaim(ctx context.Context, unit contract.Unit, attemptID contract.ID) (*claimRow, error) {
	row := unit.QueryRowContext(ctx, `SELECT attempt_id, generation, expires_at, consumed, created_at
		FROM effects_claims WHERE attempt_id = ?`, string(attemptID))
	var c claimRow
	var expires, created string
	var consumed int
	err := row.Scan(&c.AttemptID, &c.Generation, &expires, &consumed, &created)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.ExpiresAt, err = parseStamp(expires)
	if err != nil {
		return nil, fmt.Errorf("effects: claim %s expiry: %w", c.AttemptID, err)
	}
	c.CreatedAt, err = parseStamp(created)
	if err != nil {
		return nil, fmt.Errorf("effects: claim %s timestamp: %w", c.AttemptID, err)
	}
	c.Consumed = consumed != 0
	return &c, nil
}

// consumeClaim consumes the one-use claim, fenced on the unconsumed state.
// A second consume attempt or an already-consumed claim yields conflict.
func consumeClaim(ctx context.Context, unit contract.Unit, attemptID contract.ID, now time.Time) error {
	res, err := unit.ExecContext(ctx, `UPDATE effects_claims
		SET consumed = 1 WHERE attempt_id = ? AND consumed = 0 AND expires_at > ?`,
		string(attemptID), formatStamp(now))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return conflict("dispatch claim for attempt %s is consumed, expired or absent", attemptID)
	}
	return nil
}

// observationRow is one append-only recorded observation.
type observationRow struct {
	ID                contract.ID
	OperationID       contract.ID
	AttemptID         contract.ID
	Kind              string
	Disposition       string
	EvidenceJSON      string
	UsageJSON         string
	ProviderReference string
	ConfirmedAt       time.Time
	RecordedAt        time.Time
}

// insertObservation appends one observation in the same transaction.
func insertObservation(ctx context.Context, unit contract.Unit, o *observationRow) error {
	_, err := unit.ExecContext(ctx, `INSERT INTO effects_observations
		(id, operation_id, attempt_id, kind, disposition, evidence_json, usage_json, provider_reference, confirmed_at, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(o.ID), string(o.OperationID), string(o.AttemptID), o.Kind, o.Disposition,
		o.EvidenceJSON, o.UsageJSON, o.ProviderReference, formatStamp(o.ConfirmedAt), formatStamp(o.RecordedAt))
	return err
}

// listObservations returns every recorded observation of one operation in
// record order.
func listObservations(ctx context.Context, unit contract.Unit, operationID contract.ID) ([]*observationRow, error) {
	rows, err := unit.QueryContext(ctx, `SELECT id, operation_id, attempt_id, kind, disposition, evidence_json,
		usage_json, provider_reference, confirmed_at, recorded_at
		FROM effects_observations WHERE operation_id = ? ORDER BY recorded_at, id`, string(operationID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*observationRow
	for rows.Next() {
		var o observationRow
		var attempt, evidence, usage, providerRef, confirmed, recorded string
		err := rows.Scan(&o.ID, &o.OperationID, &attempt, &o.Kind, &o.Disposition, &evidence, &usage, &providerRef, &confirmed, &recorded)
		if err != nil {
			return nil, err
		}
		if attempt != "" {
			o.AttemptID = contract.ID(attempt)
		}
		o.EvidenceJSON = evidence
		o.UsageJSON = usage
		o.ProviderReference = providerRef
		if confirmed != "" {
			o.ConfirmedAt, err = parseStamp(confirmed)
			if err != nil {
				return nil, fmt.Errorf("effects: observation %s confirmation: %w", o.ID, err)
			}
		}
		if recorded != "" {
			o.RecordedAt, err = parseStamp(recorded)
			if err != nil {
				return nil, fmt.Errorf("effects: observation %s record time: %w", o.ID, err)
			}
		}
		out = append(out, &o)
	}
	return out, rows.Err()
}

// obligationRow is one open recovery obligation.
type obligationRow struct {
	ID          contract.ID
	OperationID contract.ID
	Kind        string
	State       string
	DetailJSON  string
	CreatedAt   time.Time
	ResolvedAt  time.Time
}

// insertObligation opens one obligation.
func insertObligation(ctx context.Context, unit contract.Unit, o *obligationRow) error {
	var resolvedAt any
	if !o.ResolvedAt.IsZero() {
		resolvedAt = formatStamp(o.ResolvedAt)
	}
	_, err := unit.ExecContext(ctx, `INSERT INTO effects_obligations
		(id, operation_id, kind, state, detail_json, created_at, resolved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(o.ID), string(o.OperationID), o.Kind, o.State, o.DetailJSON,
		formatStamp(o.CreatedAt), resolvedAt)
	return err
}

// listOpenObligationsForOperation returns the unresolved obligations of one
// operation in opening order.
func listOpenObligationsForOperation(ctx context.Context, unit contract.Unit, operationID contract.ID) ([]*obligationRow, error) {
	rows, err := unit.QueryContext(ctx, `SELECT id, operation_id, kind, state, detail_json, created_at, resolved_at
		FROM effects_obligations
		WHERE operation_id = ? AND resolved_at IS NULL
		ORDER BY created_at, id`, string(operationID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*obligationRow
	for rows.Next() {
		var ob obligationRow
		var detail, created string
		var resolved *string
		if err := rows.Scan(&ob.ID, &ob.OperationID, &ob.Kind, &ob.State, &detail, &created, &resolved); err != nil {
			return nil, err
		}
		ob.DetailJSON = detail
		if created != "" {
			c, err := parseStamp(created)
			if err != nil {
				return nil, fmt.Errorf("effects: obligation %s creation: %w", ob.ID, err)
			}
			ob.CreatedAt = c
		}
		if resolved != nil && *resolved != "" {
			r, err := parseStamp(*resolved)
			if err != nil {
				return nil, fmt.Errorf("effects: obligation %s resolution: %w", ob.ID, err)
			}
			ob.ResolvedAt = r
		}
		out = append(out, &ob)
	}
	return out, rows.Err()
}

// pendingDispatchableStates are operations the controller can act on right
// now: staged operations awaiting admission, admitted operations whose
// one-use claim is unconsumed and claimed operations whose observation is
// still owed. Ready and executing are listed so a restarted controller can
// name every admitted-but-unfinished attempt (attempt_ids) and settle it
// from its own journal: unclaimed as not sent, claimed as unknown, never as
// ready for resend.
var pendingDispatchableStates = []string{opStatePrepared, opStateReady, opStateExecuting}

// pendingBlockedStates are operations awaiting an outcome the controller
// does not yet control: accepted calls awaiting confirmation and uncertain
// operations awaiting reconciliation.
var pendingBlockedStates = []string{opStateAwaitingConfirmation, opStateOutcomeUnknown}

// listPendingOperations returns the controller-actionable operations of one
// installation. Dispatchable operations are scanned before blocked ones so
// any number of unresolved (awaiting_confirmation/outcome_unknown)
// operations beyond the batch limit can never hide newer dispatchable work:
// the dispatchable scan runs its own bounded query, unaffected by how many
// blocked rows exist, and only the remaining budget after it is spent on
// blocked operations.
func listPendingOperations(ctx context.Context, unit contract.Unit, install contract.ID, limit int64) ([]*operationRow, error) {
	dispatchable, err := queryOperationsByStates(ctx, unit, install, pendingDispatchableStates, limit)
	if err != nil {
		return nil, err
	}
	out := dispatchable
	if remaining := limit - int64(len(dispatchable)); remaining > 0 {
		blocked, err := queryOperationsByStates(ctx, unit, install, pendingBlockedStates, remaining)
		if err != nil {
			return nil, err
		}
		out = append(out, blocked...)
	}
	return out, nil
}

// queryOperationsByStates returns up to limit operations of one installation
// in one of the named states, in submission order.
func queryOperationsByStates(ctx context.Context, unit contract.Unit, install contract.ID, states []string, limit int64) ([]*operationRow, error) {
	if limit <= 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(states))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(states)+2)
	args = append(args, string(install))
	for _, state := range states {
		args = append(args, state)
	}
	args = append(args, limit)
	rows, err := unit.QueryContext(ctx, `SELECT `+operationColumns+` FROM effects_operations
		WHERE installation_id = ? AND state IN (`+placeholders+`)
		ORDER BY created_at, id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*operationRow
	for rows.Next() {
		o, err := scanOperation(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// resolveObligation closes one obligation, fenced on its open state.
func resolveObligation(ctx context.Context, unit contract.Unit, id contract.ID, now time.Time) error {
	res, err := unit.ExecContext(ctx, `UPDATE effects_obligations
		SET state = 'resolved', resolved_at = ? WHERE id = ? AND resolved_at IS NULL`, formatStamp(now), string(id))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return conflict("obligation %s is already resolved", id)
	}
	return nil
}
