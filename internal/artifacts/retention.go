package artifacts

import (
	"context"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Abandoned-staging cleanup (AGENTS.md step 4: "Provide bounded
// enumerations/pins for backups and exports, integrity verification and
// abandoned-staging cleanup with recovery-safe retention").
//
// Chunk bytes are published under their own content digest as soon as they
// arrive (localio.go performChunk), so an abandoned upload never leaves
// unpublished staged bytes behind on the happy path; the only bookkeeping
// left dangling is the *upload row itself*, which stays reported "open"
// forever unless something transitions it. A late explicit
// artifact.upload.cancel already recognizes this and records "expired"
// instead of "cancelled" (finishCancel), but nothing reclaims an upload
// whose caller never calls cancel at all. ReclaimExpiredUploads is that
// sweep: a Go-level maintenance primitive a future scheduled caller drives
// (the frozen operation catalog names no "artifact staging sweep"
// operation; docs/implementation/operations.json's ten artifacts entries
// are exhaustive), kept here so the transition it performs is real,
// tested, package-owned behavior rather than an invented cross-package
// operation.
//
// Retention stays recovery-safe by construction: the only rows touched are
// state = 'open' AND already past expires_at. A row still inside its
// expiry window, or already terminal (finished/cancelled/expired), is left
// exactly as it stands. Reclaiming never touches artifacts_metadata or
// blob bytes -- published content is retained by BlobStore's own,
// separately owned lifecycle.

// defaultReclaimLimit bounds one sweep call when the caller passes limit<=0.
const defaultReclaimLimit = 200

// eventUploadExpired marks the sweep-driven expiry transition, distinct
// from a caller-initiated eventUploadCancelled so an event consumer can
// tell an abandoned upload from an actively cancelled one.
const eventUploadExpired = "artifacts.upload.expired"

// ReclaimExpiredUploads transitions every upload that is still "open" past
// its expiry to "expired", bounded by limit (defaultReclaimLimit when
// limit<=0), and returns how many it reclaimed. It runs inside the
// caller's own write Unit like any other mutation in this package: no
// network/model call, no filesystem streaming, no goroutine.
func (s *Service) ReclaimExpiredUploads(ctx context.Context, unit contract.Unit, limit int64) (int, error) {
	if limit <= 0 {
		limit = defaultReclaimLimit
	}
	now := s.deps.Clock.Now()
	abandoned, err := listExpiredOpenUploads(ctx, unit, now, limit)
	if err != nil {
		return 0, err
	}
	reclaimed := 0
	for _, u := range abandoned {
		u.State = "expired"
		u.UpdatedAt = now
		if err := updateUpload(ctx, unit, u); err != nil {
			return reclaimed, err
		}
		data, err := marshalData(u.wire())
		if err != nil {
			return reclaimed, err
		}
		if err := emitTransition(ctx, unit, eventUploadExpired, u.ID, u.Version, data); err != nil {
			return reclaimed, err
		}
		reclaimed++
	}
	return reclaimed, nil
}

// listExpiredOpenUploads reads every "open" upload already past expiry, up
// to limit, oldest expiry first. Rows are fully drained and the statement
// closed before the caller issues any further read or write on the same
// Unit, matching listArtifacts/listChunks in store.go.
func listExpiredOpenUploads(ctx context.Context, unit contract.Unit, now time.Time, limit int64) ([]*uploadRow, error) {
	rows, err := unit.QueryContext(ctx, `SELECT `+uploadColumns+` FROM artifacts_uploads
		WHERE state = 'open' AND expires_at <= ? ORDER BY expires_at LIMIT ?`,
		formatStamp(now), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*uploadRow
	for rows.Next() {
		u, err := scanUpload(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
