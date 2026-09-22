package installation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Row structs mirror the installation_-prefixed tables.

type stateRow struct {
	ID          contract.ID
	Version     int64
	Paused      bool
	Maintenance bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type bootstrapIntentRow struct {
	ID              contract.ID
	InstallationID  contract.ID
	OwnerID         contract.ID
	CredentialID    contract.ID
	OwnerName       string
	CredentialStore string
	HeadlessKeyRef  string
	State           string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type jobRow struct {
	ID             contract.ID
	Version        int64
	InstallationID contract.ID
	Kind           string
	State          string
	Requirements   []wireRequirement
	Result         json.RawMessage
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type obligationRow struct {
	ID             contract.ID
	InstallationID contract.ID
	// RestoreJobID names the backup or restore job this obligation was
	// captured for (the column predates backup also using this table).
	RestoreJobID    contract.ID
	Owner           string
	Kind            string
	ResourceID      contract.ID
	ResourceVersion contract.Version
	RecordArtifact  wireArtifactRef
	RecordDigest    contract.Digest
	State           string
	RecordedAt      time.Time
}

func parseStamp(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("installation: decode stored timestamp: %w", err)
	}
	return t, nil
}

func formatStamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// expectOneRow requires exactly one row to have been affected, translating a
// zero-row outcome into a stale_version fault: the caller already loaded the
// row inside this same transaction, so zero rows only happens when a
// concurrent writer changed it first.
func expectOneRow(res sql.Result, kind string, id contract.ID) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("installation: rows affected for %s %s: %w", kind, id, err)
	}
	if n != 1 {
		return staleVersion("%s %s changed concurrently", kind, id)
	}
	return nil
}

// ---------- installation_state ----------

func loadState(ctx context.Context, r contract.Reader) (*stateRow, error) {
	row := r.QueryRowContext(ctx, `
		SELECT id, version, paused, maintenance, created_at, updated_at
		FROM installation_state LIMIT 1`)
	var (
		s                    stateRow
		pausedInt, maintInt  int64
		createdAt, updatedAt string
	)
	err := row.Scan(&s.ID, &s.Version, &pausedInt, &maintInt, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("installation: load state: %w", err)
	}
	s.Paused = pausedInt == 1
	s.Maintenance = maintInt == 1
	if s.CreatedAt, err = parseStamp(createdAt); err != nil {
		return nil, err
	}
	if s.UpdatedAt, err = parseStamp(updatedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

func insertState(ctx context.Context, unit contract.Unit, s stateRow) error {
	_, err := unit.ExecContext(ctx, `
		INSERT INTO installation_state (id, version, paused, maintenance, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		string(s.ID), s.Version, boolInt(s.Paused), boolInt(s.Maintenance),
		formatStamp(s.CreatedAt), formatStamp(s.UpdatedAt))
	if err != nil {
		return fmt.Errorf("installation: insert state: %w", err)
	}
	return nil
}

func updateState(ctx context.Context, unit contract.Unit, s stateRow, previousVersion int64) error {
	res, err := unit.ExecContext(ctx, `
		UPDATE installation_state
		SET version = ?, paused = ?, maintenance = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		s.Version, boolInt(s.Paused), boolInt(s.Maintenance), formatStamp(s.UpdatedAt),
		string(s.ID), previousVersion)
	if err != nil {
		return fmt.Errorf("installation: update state: %w", err)
	}
	return expectOneRow(res, "installation", s.ID)
}

// ---------- installation_bootstrap_intents ----------

func insertBootstrapIntent(ctx context.Context, unit contract.Unit, r bootstrapIntentRow) error {
	_, err := unit.ExecContext(ctx, `
		INSERT INTO installation_bootstrap_intents
			(id, installation_id, owner_id, credential_id, owner_name,
			 credential_store, headless_key_ref, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(r.ID), string(r.InstallationID), string(r.OwnerID), string(r.CredentialID),
		r.OwnerName, r.CredentialStore, r.HeadlessKeyRef, r.State,
		formatStamp(r.CreatedAt), formatStamp(r.UpdatedAt))
	if err != nil {
		return fmt.Errorf("installation: insert bootstrap intent: %w", err)
	}
	return nil
}

// abandonPendingBootstrapIntents marks every still-pending intent abandoned:
// the opaque, tokenless crash-recovery cleanup a fresh init attempt performs
// before creating its own intent. It never touches the raw secret the
// trusted helper may have already custodied for an abandoned intent; that
// unreferenced material is out of this package's reach, by the same design
// that leaves unreferenced staged artifact bytes for separate reclamation.
func abandonPendingBootstrapIntents(ctx context.Context, unit contract.Unit, now time.Time) (int64, error) {
	res, err := unit.ExecContext(ctx, `
		UPDATE installation_bootstrap_intents
		SET state = 'abandoned', updated_at = ?
		WHERE state = 'pending'`, formatStamp(now))
	if err != nil {
		return 0, fmt.Errorf("installation: abandon pending bootstrap intents: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("installation: abandon pending bootstrap intents: %w", err)
	}
	return n, nil
}

func markBootstrapIntent(ctx context.Context, unit contract.Unit, id contract.ID, state string, now time.Time) error {
	_, err := unit.ExecContext(ctx, `
		UPDATE installation_bootstrap_intents SET state = ?, updated_at = ? WHERE id = ?`,
		state, formatStamp(now), string(id))
	if err != nil {
		return fmt.Errorf("installation: mark bootstrap intent %s %s: %w", id, state, err)
	}
	return nil
}

// ---------- installation_jobs ----------

func encodeRequirements(reqs []wireRequirement) (string, error) {
	if reqs == nil {
		reqs = []wireRequirement{}
	}
	raw, err := json.Marshal(reqs)
	if err != nil {
		return "", fmt.Errorf("installation: encode requirements: %w", err)
	}
	return string(raw), nil
}

func decodeRequirements(raw string) ([]wireRequirement, error) {
	var reqs []wireRequirement
	if err := json.Unmarshal([]byte(raw), &reqs); err != nil {
		return nil, fmt.Errorf("installation: decode stored requirements: %w", err)
	}
	if reqs == nil {
		reqs = []wireRequirement{}
	}
	return reqs, nil
}

func insertJob(ctx context.Context, unit contract.Unit, j jobRow) error {
	reqsJSON, err := encodeRequirements(j.Requirements)
	if err != nil {
		return err
	}
	result := j.Result
	if len(result) == 0 {
		result = json.RawMessage(`{}`)
	}
	_, err = unit.ExecContext(ctx, `
		INSERT INTO installation_jobs
			(id, version, installation_id, kind, state, requirements_json, result_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(j.ID), j.Version, string(j.InstallationID), j.Kind, j.State,
		reqsJSON, string(result), formatStamp(j.CreatedAt), formatStamp(j.UpdatedAt))
	if err != nil {
		return fmt.Errorf("installation: insert job: %w", err)
	}
	return nil
}

func updateJob(ctx context.Context, unit contract.Unit, j jobRow, previousVersion int64) error {
	reqsJSON, err := encodeRequirements(j.Requirements)
	if err != nil {
		return err
	}
	result := j.Result
	if len(result) == 0 {
		result = json.RawMessage(`{}`)
	}
	res, err := unit.ExecContext(ctx, `
		UPDATE installation_jobs
		SET version = ?, state = ?, requirements_json = ?, result_json = ?, updated_at = ?
		WHERE id = ? AND version = ?`,
		j.Version, j.State, reqsJSON, string(result), formatStamp(j.UpdatedAt),
		string(j.ID), previousVersion)
	if err != nil {
		return fmt.Errorf("installation: update job: %w", err)
	}
	return expectOneRow(res, "job", j.ID)
}

func loadJob(ctx context.Context, r contract.Reader, id contract.ID) (*jobRow, error) {
	row := r.QueryRowContext(ctx, `
		SELECT id, version, installation_id, kind, state, requirements_json, result_json, created_at, updated_at
		FROM installation_jobs WHERE id = ?`, string(id))
	var (
		j                    jobRow
		reqsJSON, resultJSON string
		createdAt, updatedAt string
	)
	err := row.Scan(&j.ID, &j.Version, &j.InstallationID, &j.Kind, &j.State,
		&reqsJSON, &resultJSON, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("installation: load job: %w", err)
	}
	if j.Requirements, err = decodeRequirements(reqsJSON); err != nil {
		return nil, err
	}
	j.Result = json.RawMessage(resultJSON)
	if j.CreatedAt, err = parseStamp(createdAt); err != nil {
		return nil, err
	}
	if j.UpdatedAt, err = parseStamp(updatedAt); err != nil {
		return nil, err
	}
	return &j, nil
}

func (j jobRow) wire() wireJob {
	return wireJob{
		ID:           j.ID,
		Version:      contract.Version(j.Version),
		Kind:         j.Kind,
		State:        j.State,
		Requirements: j.Requirements,
		Owner:        owner,
		Operation:    "installation." + j.Kind,
		Result:       j.Result,
	}
}

// ---------- installation_recovery_obligations ----------

func insertObligation(ctx context.Context, unit contract.Unit, o obligationRow) error {
	artifactJSON, err := json.Marshal(o.RecordArtifact)
	if err != nil {
		return fmt.Errorf("installation: encode obligation artifact: %w", err)
	}
	_, err = unit.ExecContext(ctx, `
		INSERT INTO installation_recovery_obligations
			(id, installation_id, restore_job_id, owner, kind, resource_id, resource_version,
			 record_artifact_json, record_digest, state, recorded_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(o.ID), string(o.InstallationID), string(o.RestoreJobID), o.Owner, o.Kind,
		string(o.ResourceID), int64(o.ResourceVersion), string(artifactJSON), string(o.RecordDigest),
		o.State, formatStamp(o.RecordedAt))
	if err != nil {
		return fmt.Errorf("installation: insert recovery obligation: %w", err)
	}
	return nil
}

func listObligationsByJob(ctx context.Context, r contract.Reader, jobID contract.ID) ([]obligationRow, error) {
	rows, err := r.QueryContext(ctx, `
		SELECT id, installation_id, restore_job_id, owner, kind, resource_id, resource_version,
		       record_artifact_json, record_digest, state, recorded_at
		FROM installation_recovery_obligations WHERE restore_job_id = ? ORDER BY recorded_at ASC`,
		string(jobID))
	if err != nil {
		return nil, fmt.Errorf("installation: list recovery obligations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []obligationRow
	for rows.Next() {
		var (
			o                  obligationRow
			resourceVersionInt int64
			artifactJSON       string
			recordedAt         string
		)
		if err := rows.Scan(&o.ID, &o.InstallationID, &o.RestoreJobID, &o.Owner, &o.Kind,
			&o.ResourceID, &resourceVersionInt, &artifactJSON, &o.RecordDigest, &o.State, &recordedAt); err != nil {
			return nil, fmt.Errorf("installation: scan recovery obligation: %w", err)
		}
		o.ResourceVersion = contract.Version(resourceVersionInt)
		if err := json.Unmarshal([]byte(artifactJSON), &o.RecordArtifact); err != nil {
			return nil, fmt.Errorf("installation: decode recovery obligation artifact: %w", err)
		}
		if o.RecordedAt, err = parseStamp(recordedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("installation: list recovery obligations: %w", err)
	}
	return out, nil
}

// ---------- installation_backup_keys ----------

// loadBackupKeyRef returns the recorded backup key reference, or "" when
// no backup has custodied a key yet.
func loadBackupKeyRef(ctx context.Context, r contract.Reader, installationID contract.ID) (string, error) {
	var ref string
	err := r.QueryRowContext(ctx, `SELECT store_ref FROM installation_backup_keys WHERE installation_id = ?`,
		string(installationID)).Scan(&ref)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("installation: load backup key reference: %w", err)
	}
	return ref, nil
}

// recordBackupKeyRef records or replaces the backup key reference.
func recordBackupKeyRef(ctx context.Context, unit contract.Unit, installationID contract.ID, ref string, now time.Time) error {
	_, err := unit.ExecContext(ctx, `
		INSERT INTO installation_backup_keys (installation_id, store_ref, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(installation_id) DO UPDATE SET store_ref = excluded.store_ref, updated_at = excluded.updated_at`,
		string(installationID), ref, formatStamp(now), formatStamp(now))
	if err != nil {
		return fmt.Errorf("installation: record backup key reference: %w", err)
	}
	return nil
}
