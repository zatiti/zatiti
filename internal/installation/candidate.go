package installation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/zatiti/zatiti/internal/contract"
)

// The two exported wrappers entrypoint assembly needs to complete the
// controller's restore handoff without ever reimplementing this package's
// bundle format.
//
// Both are deliberately unit-free. They take only contract.SecretStore and
// contract.BlobStore -- the same two dependencies performRestore itself uses
// for exactly this work, outside any contract.Unit -- because their caller
// is cmd/zatiti's controller.RestoreLifecycle, which runs while the live
// database is being replaced and has no transaction to run in. They open no
// database, hold no Service state and mutate nothing this package owns.
//
// They exist because the alternative was worse and was rejected on the
// record: cmd/zatiti holds its own contract.SecretStore/BlobStore, so it
// could technically re-derive the artifact header, the AES-256-GCM sealing
// and the frame layout itself -- producing a second implementation of
// secret-bearing AEAD framing that can silently drift from this one. See
// cmd/zatiti/restore.go's own package doc for the original refusal. Exposing
// a narrow, read-only wrapper around the existing unexported pipeline
// (readPublished -> decodeArtifact -> SecretStore.Get -> openBackupBundle /
// openRecoveryOverlay) keeps exactly one implementation of that framing.
//
// Neither wrapper widens what a caller may learn. ResolveBackupCandidate
// returns a decrypted database image staged as a local file plus the
// manifest claims storage.Restorable.PrepareRestore validates it against;
// OpenRecoveryOverlay returns the overlay's obligations. Neither returns key
// material, and neither resolves a key by name: both resolve it only by the
// opaque reference the artifact's own clear header carries, so a bundle
// whose key this installation's store does not hold simply fails.

// BackupCandidate is a decrypted backup image staged as a local file, plus
// the manifest's own claims about it. It is the installation-side form of
// controller.RestoreCandidate; entrypoint assembly converts between them,
// which keeps this package free of any storage import.
type BackupCandidate struct {
	// Path is the staged, decrypted candidate database file. The caller owns
	// it and removes it once storage has made its own copy.
	Path string
	// InstallationID is the installation the backup manifest is bound to.
	// openBackupBundle already refused a manifest bound elsewhere; this is
	// the verified value, not an unchecked claim.
	InstallationID contract.ID
	// DatabaseDigest and DatabaseSize describe exactly the framed image
	// bytes written to Path, as the manifest recorded them and as
	// openBackupBundle re-verified them.
	DatabaseDigest contract.Digest
	DatabaseSize   int64
	// SchemaVersions is the manifest's claimed set of applied owner
	// migrations. It is empty for every bundle this package has ever
	// produced: migration metadata is storage-private and no seam hands it
	// to this package (see performBackup's own note and the P31 handoff), so
	// the manifest lists none rather than guessing. A caller that needs a
	// schema claim must supply one it can actually justify and say so.
	SchemaVersions []SchemaVersion
	// BackupID is the backup job whose Perform sealed this bundle.
	BackupID contract.ID
	// Generation is the controller generation the manifest pinned.
	Generation int64
}

// SchemaVersion mirrors one entry of a manifest's
// database_schema_versions.
type SchemaVersion struct {
	Owner           string
	Version         int64
	MigrationDigest contract.Digest
}

// RecoveryOverlay is a decrypted, verified recovery overlay: what the
// installation owed at the moment the overlay was captured, ready to be
// handed to each owner's own restore.merge operation.
type RecoveryOverlay struct {
	// InstallationID is the installation the overlay is bound to, already
	// verified against the caller's own expectation.
	InstallationID contract.ID
	// CapturedAt is the instant the overlay was sealed, as an RFC3339Nano
	// UTC stamp. Every obligation's own recorded_at is at or before it, and
	// each owner's merge enforces that.
	CapturedAt string
	// SourceGeneration is the controller generation the overlay pinned.
	SourceGeneration int64
	// SourceDatabaseDigest is the consistent image digest of the
	// pre-restore database the overlay describes.
	SourceDatabaseDigest contract.Digest
	// Obligations are the retained obligations, in capture order.
	Obligations []Obligation
}

// Obligation is one retained RecoveryObligation, in the exact shape the
// frozen $defs/RecoveryObligation declares, so a caller can hand it to an
// owner's restore.merge without translating field names.
type Obligation struct {
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

// errNoStores names a caller that supplied neither of the two dependencies
// this work genuinely needs.
func errNoStores(what string) error {
	return prerequisiteMissing(
		"resolving %s requires both a secret store and a blob store; neither is optional here", what)
}

// maxArtifactBytes bounds an artifact read whose recorded size the caller
// does not have. It is the backup bound plus the bundle's own framing and
// sealing overhead, so a legal bundle always fits and a runaway object is
// refused instead of buffered.
const maxArtifactBytes = maxBackupImageBytes + (1 << 20)

// readArtifact reads one published artifact in full. size is the artifact's
// recorded byte size when the caller has it, and the read is bounded by it
// exactly as every other read in this package is.
//
// size <= 0 means the caller genuinely does not have it. That is not a
// shortcut: a restore job's own input names its verified backup by id and
// digest only, and no operation the controller may call projects an
// artifact's size (_artifacts.metadata's caller allowlist does not include
// the controller). In that case the object is read under an explicit
// maxArtifactBytes window -- an explicit positive bound, never a zero
// length, whose "read the whole remainder" meaning is one implementation's
// documented behavior and not part of the frozen contract.BlobStore
// interface -- and the bytes are hashed and checked against the reference
// here. That digest check is strictly stronger than the length comparison
// the sized path makes, and it also catches a truncation at the bound: an
// object larger than the window hashes to something else and is refused.
func readArtifact(ctx context.Context, blobs contract.BlobStore, digest contract.Digest, size int64) ([]byte, error) {
	if size > 0 {
		return readPublished(ctx, blobs, digest, size)
	}
	rc, err := blobs.Open(ctx, digest, 0, maxArtifactBytes)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, maxArtifactBytes))
	if err != nil {
		return nil, err
	}
	if digestOf(data) != digest {
		return nil, fmt.Errorf(
			"published artifact bytes do not hash to the referenced digest (read %d bytes)", len(data))
	}
	return data, nil
}

// ResolveBackupCandidate reads one published backup artifact, resolves its
// sealing key by the opaque reference the artifact's own clear header
// carries, decrypts and verifies the bundle against installationID, and
// writes the framed database image to a private file under destDir.
//
// It is the exact pipeline performRestore already runs before it exports a
// recovery overlay, with the one difference that it keeps the image instead
// of discarding it. Verification is not weakened anywhere: a bundle bound to
// another installation, a manifest that does not carry the required paused
// state, or a framed image that does not match the manifest's own digest and
// size all fail here exactly as they fail there.
//
// The returned file is the caller's to remove.
func ResolveBackupCandidate(ctx context.Context, secrets contract.SecretStore, blobs contract.BlobStore,
	installationID contract.ID, artifact contract.ArtifactRef, size int64, destDir string) (BackupCandidate, error) {
	if secrets == nil || blobs == nil {
		return BackupCandidate{}, errNoStores("a backup candidate")
	}
	if installationID == "" {
		return BackupCandidate{}, invalidInput("resolving a backup candidate requires the destination installation identity")
	}
	if artifact.Digest == "" {
		return BackupCandidate{}, invalidInput("backup artifact reference carries no digest")
	}
	if destDir == "" {
		return BackupCandidate{}, invalidInput("a staging directory is required")
	}
	raw, err := readArtifact(ctx, blobs, artifact.Digest, size)
	if err != nil {
		return BackupCandidate{}, artifactFault("backup artifact bytes are unavailable: %v", err)
	}
	keyRef, sealed, err := decodeArtifact(raw)
	if err != nil {
		return BackupCandidate{}, prerequisiteMissing("%v", err)
	}
	key, err := secrets.Get(ctx, keyRef)
	if err != nil || len(key) != 32 {
		return BackupCandidate{}, prerequisiteMissing(
			"the backup's key reference does not resolve in this installation's secret store; the bundle cannot be decrypted here")
	}
	manifest, image, err := openBackupBundle(key, sealed, installationID)
	if err != nil {
		if errors.Is(err, errBundleForeign) {
			return BackupCandidate{}, invalidInput("backup manifest belongs to a different installation")
		}
		return BackupCandidate{}, artifactFault("backup artifact failed integrity or authenticity verification: %v", err)
	}

	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return BackupCandidate{}, prerequisiteMissing("restore staging directory cannot be created")
	}
	path := filepath.Join(destDir, "candidate-"+string(artifact.ID)+".sqlite")
	// 0o600: the decrypted image is the whole installation in the clear.
	if err := os.WriteFile(path, image, 0o600); err != nil {
		return BackupCandidate{}, prerequisiteMissing("the decrypted candidate image cannot be staged locally")
	}

	versions := make([]SchemaVersion, 0, len(manifest.DatabaseSchemaVersions))
	for _, v := range manifest.DatabaseSchemaVersions {
		versions = append(versions, SchemaVersion(v))
	}
	return BackupCandidate{
		Path: path, InstallationID: manifest.InstallationID,
		DatabaseDigest: manifest.DatabaseDigest, DatabaseSize: manifest.DatabaseSize,
		SchemaVersions: versions, BackupID: manifest.BackupID, Generation: manifest.Generation,
	}, nil
}

// OpenRecoveryOverlay reads one published recovery-overlay artifact,
// resolves its sealing key by the opaque reference the artifact's own clear
// header carries, decrypts it and verifies its binding to installationID,
// returning the obligations it retained.
//
// It is the same path installation.restore's own Perform already takes to
// verify the overlay it just published (openPublishedOverlay), exported so
// the controller's merge step reads the identical bytes through the
// identical verification rather than a second implementation of it.
func OpenRecoveryOverlay(ctx context.Context, secrets contract.SecretStore, blobs contract.BlobStore,
	installationID contract.ID, artifact contract.ArtifactRef, size int64) (RecoveryOverlay, error) {
	if secrets == nil || blobs == nil {
		return RecoveryOverlay{}, errNoStores("a recovery overlay")
	}
	if installationID == "" {
		return RecoveryOverlay{}, invalidInput("resolving a recovery overlay requires the destination installation identity")
	}
	if artifact.Digest == "" {
		return RecoveryOverlay{}, invalidInput("recovery overlay reference carries no digest")
	}
	raw, err := readArtifact(ctx, blobs, artifact.Digest, size)
	if err != nil {
		return RecoveryOverlay{}, artifactFault("recovery overlay bytes are unavailable: %v", err)
	}
	keyRef, sealed, err := decodeArtifact(raw)
	if err != nil {
		return RecoveryOverlay{}, prerequisiteMissing("%v", err)
	}
	key, err := secrets.Get(ctx, keyRef)
	if err != nil || len(key) != 32 {
		return RecoveryOverlay{}, prerequisiteMissing(
			"the overlay's key reference does not resolve in this installation's secret store; it cannot be decrypted here")
	}
	doc, err := openRecoveryOverlay(key, sealed, installationID)
	if err != nil {
		if errors.Is(err, errBundleForeign) {
			return RecoveryOverlay{}, invalidInput("recovery overlay belongs to a different installation")
		}
		return RecoveryOverlay{}, artifactFault("recovery overlay failed integrity or authenticity verification: %v", err)
	}
	out := RecoveryOverlay{
		InstallationID: doc.InstallationID, CapturedAt: doc.CapturedAt,
		SourceGeneration: doc.SourceGeneration, SourceDatabaseDigest: doc.SourceDatabaseDigest,
		Obligations: make([]Obligation, 0, len(doc.Obligations)),
	}
	for _, o := range doc.Obligations {
		out.Obligations = append(out.Obligations, Obligation{
			ID: o.ID, Owner: o.Owner, Kind: o.Kind, ResourceID: o.ResourceID,
			ResourceVersion: o.ResourceVersion,
			RecordArtifact:  contract.ArtifactRef{ID: o.RecordArtifact.ID, Digest: o.RecordArtifact.Digest},
			RecordDigest:    o.RecordDigest, State: o.State, RecordedAt: o.RecordedAt,
		})
	}
	if out.CapturedAt == "" {
		return RecoveryOverlay{}, artifactFault("recovery overlay carries no capture timestamp")
	}
	return out, nil
}

// MergeOperation names the internal operation that folds one owner's slice
// of a recovery overlay back into that owner's own tables. It is exported so
// entrypoint assembly routes by the owner recorded on each obligation rather
// than by a hardcoded list that could drift from the obligation kinds this
// package actually produces.
func MergeOperation(owner string) (string, bool) {
	switch owner {
	case "effects", "identity", "memory":
		return "_" + owner + ".restore.merge", true
	}
	return "", false
}
