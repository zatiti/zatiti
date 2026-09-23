package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/controller"
	"github.com/zatiti/zatiti/internal/identity"
	"github.com/zatiti/zatiti/internal/installation"
	"github.com/zatiti/zatiti/internal/storage"
)

// This file is cmd/zatiti's half of the P00-011 six-step offline restore
// protocol: the entrypoint-supplied controller.RestoreLifecycle capability
// (internal/controller/restore.go) that only entrypoint assembly may
// implement, plus the outer per-process loop (serve.go's runServe) that
// reassembles Application/Controller/server over a freshly reopened
// database whenever a lifetime ends with controller.ErrRestoreHandoff.
//
// Both methods do real work. Until P50 they failed closed with
// prerequisite_missing, because three things genuinely did not exist: no
// operation exposed a pending restore job's backup-artifact reference to
// this package, no operation exposed the published recovery-overlay
// artifact, and no owner declared a merge operation for folding a
// RecoveryOverlay's obligations back into its own tables. All three exist
// now (_installation.restore.overlay, and _effects/_identity/_memory
// .restore.merge), and internal/installation exports the two unit-free
// wrappers around its own decrypt pipeline that keep this package from ever
// reimplementing the bundle's AEAD framing -- the duplication that was
// considered and rejected without sign-off, and is still rejected.
//
// Two structural points about how MergeOverlay calls the owners, because
// both look like shortcuts and are neither:
//
//  1. It calls contract.Module.Handle directly, not Application.Internal or
//     contract.Ports. This is not a preference. MergeOverlay runs inside
//     storage.Restorable.WriteRestoreOverlay, a raw write transaction opened
//     against the database CommitRestore just reopened. This lifetime's own
//     *application.Application was built over the handle CommitRestore
//     CLOSED; internal/application/dispatch.go's Internal and dispatchNested
//     both require a live, Application-managed dispatch chain over that dead
//     handle, and nothing repoints it. The fresh Application is only built
//     later, by reassembleAfterRestoreHandoff, after runRestore returns. So
//     there is no Application to route through at this instant -- the
//     registry's schema validation and each owner's own strict dispatch
//     still run, because they live in the Bind-produced handler this call
//     reaches, not in Application.
//
//  2. Because Application.Internal is bypassed, so is its centralized
//     caller-allowlist check. That check is re-established twice over: this
//     function resolves the installation's own controller service principal
//     through identity's exported ControllerPrincipal against the same
//     transaction and refuses unless the unit's actor IS that principal, and
//     each owner's merge handler independently re-verifies the actor for
//     itself before writing (identity to the full reserved-name level, over
//     its own tables; effects and memory to the service-principal level,
//     which is everything they can establish without reading a sibling
//     owner's tables). Identity is called first for exactly that reason: its
//     refusal rolls back the whole overlay transaction before effects or
//     memory has written anything.

// restoreLifecycle is the production controller.RestoreLifecycle. Its
// fields are the capabilities entrypoint assembly already holds directly:
// the platform's secret and blob stores, the live database (for the schema
// claim a candidate must be validated against), identity (for resolving the
// controller principal) and the constructed domain modules whose own merge
// operations fold the recovery overlay back in.
type restoreLifecycle struct {
	secrets        contract.SecretStore
	blobs          contract.BlobStore
	db             contract.Database
	identity       *identity.Service
	owners         map[string]contract.Module
	installationID contract.ID
}

var _ controller.RestoreLifecycle = restoreLifecycle{}

// mergeOwners is the fixed order the recovery overlay's owner slices are
// merged in. Identity is first deliberately: it performs the strictest
// caller check (the reserved controller principal name, over its own
// tables), so a wrong actor rolls the whole overlay transaction back before
// any other owner has written a row.
var mergeOwners = []string{"identity", "effects", "memory"}

// newRestoreLifecycle builds the production capability from an open
// installation handle.
func newRestoreLifecycle(h *installationHandle, installationID contract.ID) restoreLifecycle {
	return restoreLifecycle{
		secrets: h.plat.Secrets(), blobs: h.plat.Blobs(), db: h.db,
		identity: h.identity, owners: h.owners, installationID: installationID,
	}
}

// restoreJobInput is the part of installation.restore's own original input
// this capability reads back: the verified backup artifact the restore
// names. The controller journals that input at claim time and threads it
// here, because no operation projects it anywhere else. Decoding is
// deliberately lenient about other fields -- this is installation's input
// schema, not one this package owns.
type restoreJobInput struct {
	BackupArtifact contract.ArtifactRef `json:"backup_artifact"`
}

// restoreMergeInput is the frozen _<owner>.restore.merge input.
type restoreMergeInput struct {
	RestoreJobID     contract.ID               `json:"restore_job_id"`
	CapturedAt       string                    `json:"captured_at"`
	SourceGeneration int64                     `json:"source_generation"`
	Obligations      []installation.Obligation `json:"obligations"`
}

// StageCandidate implements controller.RestoreLifecycle: it resolves the
// restore job's verified backup artifact into a decrypted candidate
// database file staged under dir.
//
// It never decrypts anything itself. installation.ResolveBackupCandidate
// runs the exact pipeline installation.restore's own Perform runs --
// readPublished, decodeArtifact, SecretStore.Get by the opaque reference in
// the bundle's own clear header, openBackupBundle -- so there is exactly one
// implementation of that framing in the tree.
func (l restoreLifecycle) StageCandidate(ctx context.Context, restoreJobID contract.ID, jobInput json.RawMessage, dir string) (controller.RestoreCandidate, error) {
	if l.secrets == nil || l.blobs == nil {
		return controller.RestoreCandidate{}, &contract.Fault{
			Code:    contract.CodePrerequisiteMissing,
			Message: "this process holds no secret or blob store; a backup candidate cannot be resolved here",
		}
	}
	var in restoreJobInput
	if err := json.Unmarshal(jobInput, &in); err != nil {
		return controller.RestoreCandidate{}, &contract.Fault{
			Code: contract.CodeInvalidInput,
			Message: "restore job " + string(restoreJobID) +
				"'s original input is not readable; its backup artifact reference cannot be resolved",
		}
	}
	if in.BackupArtifact.Digest == "" {
		return controller.RestoreCandidate{}, &contract.Fault{
			Code:    contract.CodeInvalidInput,
			Message: "restore job " + string(restoreJobID) + "'s original input names no backup artifact",
		}
	}
	// Size 0: a restore job's input names its verified backup by id and
	// digest only, and no operation the controller may call projects an
	// artifact's recorded size. ResolveBackupCandidate reads the whole
	// published object under its own bound and verifies the bytes hash to
	// this exact digest.
	candidate, err := installation.ResolveBackupCandidate(
		ctx, l.secrets, l.blobs, l.installationID, in.BackupArtifact, 0, dir)
	if err != nil {
		return controller.RestoreCandidate{}, err
	}

	versions, err := l.schemaClaim(ctx, candidate)
	if err != nil {
		return controller.RestoreCandidate{}, err
	}
	return controller.RestoreCandidate{
		Path:           candidate.Path,
		InstallationID: candidate.InstallationID,
		DatabaseDigest: candidate.DatabaseDigest,
		SchemaVersions: versions,
	}, nil
}

// schemaClaim resolves the applied-migration set storage.Restorable.
// PrepareRestore validates the staged image against.
//
// The manifest's own database_schema_versions is the intended source and is
// used whenever a bundle carries one. No bundle does today: migration
// metadata is storage-private, internal/installation has no seam that
// exposes it, and performBackup records an empty list rather than guessing
// (its own comment says so, and the P31 handoff names it). An empty claim
// cannot be passed through, because PrepareRestore compares it for exact
// equality against the staged image's real ledger and would refuse every
// genuine backup.
//
// So for a manifest that carries none, entrypoint assembly supplies the
// destination's own currently applied migrations -- a claim it can actually
// justify, from the database it already holds. The check PrepareRestore then
// performs is "the staged image's own migration ledger matches this running
// binary's exactly", which is a real compatibility gate: a backup taken
// under a different schema is refused, before anything destructive runs.
// This is strictly stronger than the empty claim, never weaker, and it is
// the honest ceiling until the manifest can carry the versions itself. That
// gap belongs to P00/integration, not to this file.
func (l restoreLifecycle) schemaClaim(ctx context.Context, candidate installation.BackupCandidate) ([]storage.SchemaVersion, error) {
	if len(candidate.SchemaVersions) > 0 {
		out := make([]storage.SchemaVersion, 0, len(candidate.SchemaVersions))
		for _, v := range candidate.SchemaVersions {
			out = append(out, storage.SchemaVersion{Owner: v.Owner, Version: v.Version, MigrationDigest: v.MigrationDigest})
		}
		return out, nil
	}
	restorable, ok := l.db.(storage.Restorable)
	if !ok {
		return nil, &contract.Fault{
			Code: contract.CodePrerequisiteMissing,
			Message: "the backup manifest carries no schema versions and this database cannot report its own; " +
				"the candidate's schema compatibility cannot be established",
		}
	}
	return restorable.SchemaVersions(ctx)
}

// MergeOverlay implements controller.RestoreLifecycle: it opens the
// published, sealed recovery overlay and hands each owner its own slice of
// the obligations through that owner's own registered merge operation.
//
// Safe to repeat, as the interface requires: every owner keys its merge on
// the obligation's own id, so a second call after a crash between storage's
// overlay write and its resume folds nothing twice.
func (l restoreLifecycle) MergeOverlay(ctx context.Context, u contract.Unit, restoreJobID contract.ID, ref controller.RestoreOverlayRef) error {
	if l.secrets == nil || l.blobs == nil {
		return &contract.Fault{
			Code:    contract.CodePrerequisiteMissing,
			Message: "this process holds no secret or blob store; the recovery overlay cannot be opened here",
		}
	}
	if err := l.requireControllerActor(ctx, u); err != nil {
		return err
	}
	installationID := u.Scope().InstallationID
	if installationID == "" {
		installationID = l.installationID
	}
	overlay, err := installation.OpenRecoveryOverlay(ctx, l.secrets, l.blobs, installationID, ref.Artifact, ref.Size)
	if err != nil {
		return err
	}

	buckets := map[string][]installation.Obligation{}
	for _, ob := range overlay.Obligations {
		if _, known := installation.MergeOperation(ob.Owner); !known {
			// An obligation naming an owner with no merge operation cannot
			// be folded back by anyone. Refusing here is what keeps the
			// resume honest: it leaves storage paused with the obligation
			// still recorded, rather than silently dropping a liability.
			return &contract.Fault{
				Code: contract.CodePrerequisiteMissing,
				Message: "recovery overlay obligation " + string(ob.ID) + " names owner " + ob.Owner +
					", which declares no restore merge operation; this overlay cannot be merged",
			}
		}
		buckets[ob.Owner] = append(buckets[ob.Owner], ob)
	}

	for _, ownerName := range mergeOwners {
		operation, _ := installation.MergeOperation(ownerName)
		module := l.owners[ownerName]
		if module == nil {
			return &contract.Fault{
				Code:    contract.CodePrerequisiteMissing,
				Message: "no " + ownerName + " module is assembled in this process; " + operation + " cannot be called",
			}
		}
		obligations := buckets[ownerName]
		if obligations == nil {
			// An owner with nothing to merge is still called, with an empty
			// list: nothing to merge is not an error, and calling uniformly
			// keeps that path exercised rather than only reasoned about.
			obligations = []installation.Obligation{}
		}
		input, err := json.Marshal(restoreMergeInput{
			RestoreJobID: restoreJobID, CapturedAt: overlay.CapturedAt,
			SourceGeneration: overlay.SourceGeneration, Obligations: obligations,
		})
		if err != nil {
			return fmt.Errorf("encode %s input: %w", operation, err)
		}
		payload, err := module.Handle(ctx, u, contract.Invocation{Operation: operation, Version: 1, Input: input})
		if err != nil {
			return err
		}
		if payload.Error != nil {
			return payload.Error
		}
		if payload.Status != contract.StatusCompleted {
			return &contract.Fault{
				Code:    contract.CodeInternalError,
				Message: operation + " returned status " + payload.Status,
			}
		}
	}
	return nil
}

// requireControllerActor refuses unless the transaction's own actor is this
// installation's controller service principal, resolved from identity's own
// tables inside this same transaction. It is the centralized check
// Application.Internal would have made, re-established at the one seam that
// bypasses it; each owner's merge handler still repeats what it can for
// itself, so removing this one does not silently open the path.
func (l restoreLifecycle) requireControllerActor(ctx context.Context, u contract.Unit) error {
	if l.identity == nil {
		return &contract.Fault{
			Code:    contract.CodePrerequisiteMissing,
			Message: "no identity module is assembled in this process; the controller principal cannot be verified",
		}
	}
	expected, err := l.identity.ControllerPrincipal(ctx, u)
	if err != nil {
		return err
	}
	actor := u.Actor()
	if actor.PrincipalID != expected.PrincipalID || actor.Kind != contract.KindService {
		return &contract.Fault{
			Code:    contract.CodePermissionDenied,
			Message: "only this installation's controller service principal may merge a recovery overlay",
		}
	}
	return nil
}
