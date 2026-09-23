package identity

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Identity's half of the P00-011 offline restore protocol.
//
// Two operations live here. _identity.revocations lets the installation
// owner enumerate this installation's current credential and grant
// revocations while it is paused, so installation.backup's manifest and
// installation.restore's recovery overlay can carry them at all -- before
// this operation existed, a revocation was simply absent from both, which
// is what internal/installation/restore_test.go's
// TestRestoreOverlayPreservesPostBackupUnknownEffectAcrossRewind doc comment
// named as unexercisable. _identity.restore.merge is the other end: after
// the controller has swapped the database file back to an older image, it
// re-applies those captured revocations to the rewound rows, so a
// credential or grant revoked AFTER the backup was taken does not come back
// to life just because the bytes that recorded the revocation were rewound.
//
// Three properties make that safe, and each is enforced below rather than
// assumed:
//
//   - Monotonic. The merge only ever sets revoked; it never clears one and
//     never creates a credential, grant or principal. A row the restored
//     image does not contain is skipped, not resurrected: the overlay
//     records that something was revoked, which is not authority to invent
//     the thing itself.
//   - Bounded by the capture point. An obligation whose own recorded_at is
//     later than the overlay's captured_at is refused, so a merge can never
//     back-date a revocation the overlay did not actually observe.
//   - Idempotent. Re-applying a revocation to an already-revoked row is a
//     no-op that appends no second identity_revocations row, so the
//     repeated MergeOverlay a crash between storage's overlay write and its
//     resume produces changes nothing.
//
// The caller check is deliberately its own: _identity.restore.merge is
// invoked by cmd/zatiti directly on this module (contract.Module.Handle)
// inside storage's paused restore-overlay transaction, because at that
// moment no *application.Application exists over the freshly reopened
// database to route an ordinary Internal call through -- the one this
// process had was built over the handle CommitRestore closed. That bypasses
// application dispatch's centralized caller allowlist, so this handler
// re-establishes the same fact for itself: the acting principal must be
// this installation's own live, unrevoked controller service principal.

// Obligation kinds identity owns inside a BackupManifest or RecoveryOverlay.
const (
	obligationRevokedCredential = "revoked_credential"
	obligationRevokedGrant      = "revoked_grant"
)

// Revocation entity kinds, as _identity.revocations reports them.
const (
	revocationKindCredential = "credential"
	revocationKindGrant      = "grant"
)

// revocationsInput is the _identity.revocations input.
type revocationsInput struct {
	Scope contract.Scope `json:"scope"`
}

// revocationOut is the Revocation resource shape.
type revocationOut struct {
	Kind        string           `json:"kind"`
	ID          contract.ID      `json:"id"`
	Version     contract.Version `json:"version"`
	PrincipalID contract.ID      `json:"principal_id"`
}

// revocationsOutput is the _identity.revocations output: a literal
// "revocations" array, not a "resource" wrapper.
type revocationsOutput struct {
	Revocations []revocationOut `json:"revocations"`
}

// recoveryObligation is the RecoveryObligation resource shape: one retained
// obligation exactly as installation captured and sealed it.
type recoveryObligation struct {
	ID              contract.ID          `json:"id"`
	Owner           string               `json:"owner"`
	Kind            string               `json:"kind"`
	ResourceID      contract.ID          `json:"resource_id"`
	ResourceVersion contract.Version     `json:"resource_version"`
	RecordArtifact  contract.ArtifactRef `json:"record_artifact"`
	RecordDigest    contract.Digest      `json:"record_digest"`
	State           string               `json:"state"`
	RecordedAt      string               `json:"recorded_at"`
}

// restoreMergeInput is the _identity.restore.merge input.
type restoreMergeInput struct {
	RestoreJobID     contract.ID          `json:"restore_job_id"`
	CapturedAt       string               `json:"captured_at"`
	SourceGeneration int64                `json:"source_generation"`
	Obligations      []recoveryObligation `json:"obligations"`
}

// recoveryMergeOut is the RecoveryMerge resource shape: exactly which
// obligations this call folded in and which it deliberately left alone.
type recoveryMergeOut struct {
	RestoreJobID contract.ID   `json:"restore_job_id"`
	Owner        string        `json:"owner"`
	Applied      []contract.ID `json:"applied"`
	Skipped      []contract.ID `json:"skipped"`
}

// revocations implements _identity.revocations: every credential and grant
// this installation currently holds revoked. Listing a row IS the assertion
// that it is revoked; no secret material or store reference is returned.
func (s *Service) revocations(ctx context.Context, unit contract.Unit, in revocationsInput) (contract.Payload, error) {
	if err := s.requireLiveActor(ctx, unit, in.Scope); err != nil {
		return contract.Payload{}, err
	}
	out := revocationsOutput{Revocations: []revocationOut{}}
	install := string(unit.Scope().InstallationID)

	credRows, err := unit.QueryContext(ctx, `
		SELECT id, version, principal_id FROM identity_credentials
		WHERE installation_id = ? AND revoked = 1
		ORDER BY id`, install)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("identity: list revoked credentials: %w", err)
	}
	revoked, err := scanRevocations(credRows, revocationKindCredential)
	if err != nil {
		return contract.Payload{}, err
	}
	out.Revocations = append(out.Revocations, revoked...)

	grantRows, err := unit.QueryContext(ctx, `
		SELECT id, version, principal_id FROM identity_grants
		WHERE installation_id = ? AND revoked = 1
		ORDER BY id`, install)
	if err != nil {
		return contract.Payload{}, fmt.Errorf("identity: list revoked grants: %w", err)
	}
	revoked, err = scanRevocations(grantRows, revocationKindGrant)
	if err != nil {
		return contract.Payload{}, err
	}
	out.Revocations = append(out.Revocations, revoked...)
	return completed(out)
}

// scanRevocations drains one (id, version, principal_id) result set into
// revocation entries of one kind.
func scanRevocations(rows *sql.Rows, kind string) ([]revocationOut, error) {
	defer func() { _ = rows.Close() }()
	var out []revocationOut
	for rows.Next() {
		var (
			id, principalID string
			version         int64
		)
		if err := rows.Scan(&id, &version, &principalID); err != nil {
			return nil, fmt.Errorf("identity: scan revoked %s: %w", kind, err)
		}
		out = append(out, revocationOut{
			Kind: kind, ID: contract.ID(id),
			Version: contract.Version(version), PrincipalID: contract.ID(principalID),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity: list revoked %s: %w", kind, err)
	}
	return out, nil
}

// restoreMerge implements _identity.restore.merge. See this file's doc
// comment for the three properties it enforces and why its caller check is
// its own.
func (s *Service) restoreMerge(ctx context.Context, unit contract.Unit, in restoreMergeInput) (contract.Payload, error) {
	if err := s.requireControllerActor(ctx, unit); err != nil {
		return contract.Payload{}, err
	}
	capturedAt, err := time.Parse(time.RFC3339Nano, in.CapturedAt)
	if err != nil {
		return contract.Payload{}, invalidInput("recovery overlay capture timestamp is not a valid instant")
	}
	out := recoveryMergeOut{
		RestoreJobID: in.RestoreJobID, Owner: owner,
		Applied: []contract.ID{}, Skipped: []contract.ID{},
	}
	now := s.deps.Clock.Now()
	for _, ob := range in.Obligations {
		if ob.Owner != owner || (ob.Kind != obligationRevokedCredential && ob.Kind != obligationRevokedGrant) {
			// Another owner's slice of the same overlay; each owner merges
			// only what it owns.
			out.Skipped = append(out.Skipped, ob.ID)
			continue
		}
		recordedAt, err := time.Parse(time.RFC3339Nano, ob.RecordedAt)
		if err != nil {
			return contract.Payload{}, invalidInput("obligation %s carries an invalid recorded_at timestamp", ob.ID)
		}
		if recordedAt.After(capturedAt) {
			// Refused rather than skipped silently: an obligation the
			// overlay could not have observed must never be back-dated into
			// the rewound state.
			return contract.Payload{}, invalidInput(
				"obligation %s was recorded after the recovery overlay's own capture point; it cannot be merged", ob.ID)
		}
		applied, err := s.reapplyRevocation(ctx, unit, ob, now)
		if err != nil {
			return contract.Payload{}, err
		}
		if applied {
			out.Applied = append(out.Applied, ob.ID)
		} else {
			out.Skipped = append(out.Skipped, ob.ID)
		}
	}
	return completed(resourceOut[recoveryMergeOut]{Resource: out})
}

// reapplyRevocation re-applies one captured revocation to the rewound row.
// It reports whether it actually changed anything: an already-revoked row
// and a row the restored image does not contain at all are both left
// exactly as they are.
func (s *Service) reapplyRevocation(ctx context.Context, unit contract.Unit, ob recoveryObligation, now time.Time) (bool, error) {
	reason := "re-applied from recovery overlay obligation " + string(ob.ID)
	if ob.Kind == obligationRevokedCredential {
		c, found, err := s.loadCredential(ctx, unit, ob.ResourceID)
		if err != nil || !found || c.Revoked {
			return false, err
		}
		c.Revoked = true
		c.Version++
		c.UpdatedAt = now
		res, err := unit.ExecContext(ctx, `
			UPDATE identity_credentials
			SET version = ?, revoked = ?, updated_at = ?
			WHERE id = ? AND installation_id = ? AND version = ? AND revoked = 0`,
			c.Version, boolInt(c.Revoked), formatStamp(c.UpdatedAt),
			string(c.ID), string(unit.Scope().InstallationID), c.Version-1)
		if err != nil {
			return false, fmt.Errorf("identity: re-apply credential revocation: %w", err)
		}
		if err := expectOneRow(res, "credential", c.ID); err != nil {
			return false, err
		}
		if err := s.appendRevocation(ctx, unit, revocationKindCredential, c.ID, reason); err != nil {
			return false, err
		}
		return true, emitTransition(ctx, unit, eventCredRevoked, c.ID, contract.Version(c.Version))
	}

	g, found, err := s.loadGrant(ctx, unit, ob.ResourceID)
	if err != nil || !found || g.Revoked {
		return false, err
	}
	g.Revoked = true
	g.Version++
	g.UpdatedAt = now
	if err := s.updateGrantRevoked(ctx, unit, g); err != nil {
		return false, err
	}
	if err := s.appendRevocation(ctx, unit, revocationKindGrant, g.ID, reason); err != nil {
		return false, err
	}
	return true, emitTransition(ctx, unit, eventGrantRevoked, g.ID, contract.Version(g.Version))
}

// requireControllerActor is the defensive caller check every overlay merge
// performs for itself, because this call path deliberately bypasses
// application dispatch's centralized caller allowlist (see this file's doc
// comment). Identity can prove the whole fact locally -- these are its own
// tables -- so it does: the actor must be a registered, unrevoked service
// principal carrying this installation's reserved controller name.
func (s *Service) requireControllerActor(ctx context.Context, unit contract.Unit) error {
	actor := unit.Actor()
	if actor.PrincipalID == "" {
		return permissionDenied("authentication is required")
	}
	p, found, err := s.loadPrincipal(ctx, unit, actor.PrincipalID)
	if err != nil {
		return err
	}
	if !found {
		return permissionDenied("caller principal is not registered in this installation")
	}
	if p.Revoked {
		return permissionDenied("caller principal is revoked")
	}
	if p.Kind != contract.KindService || p.Name != ControllerPrincipalName {
		return permissionDenied(
			"only this installation's controller service principal may merge a recovery overlay")
	}
	return nil
}
