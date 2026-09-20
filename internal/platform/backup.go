package platform

import (
	"bytes"
	"context"
	"os"
	"path/filepath"

	"github.com/zatiti/zatiti/internal/contract"
)

// Portable encrypted backup custody: typed blob inventory/import and
// master-key transfer mechanics (P30). These are local, trusted-caller
// primitives -- not a public operation, never JSON on the wire -- that a
// restore driver calls directly through a concrete *Platform, mirroring how
// contract.DatabaseBackup wraps the opened database: entrypoint assembly
// owns wiring these into the frozen contract.SnapshotInventory /
// contract.RestoreCoordinator capability (internal/platform/AGENTS.md's
// "Local IO, authentication, jobs and verification seams" section), which
// operates on the whole installation (database + blobs) and is owned by
// internal/installation, not invented here. This file provides the
// blob/key half of that seam: internal/platform owns encrypted blob files
// and secret custody, never the database.

// BlobRecord is one published object's typed inventory entry for a backup
// manifest: the exact plaintext digest and byte count. It never carries a
// local path -- product interfaces never return local absolute paths.
type BlobRecord struct {
	Digest contract.Digest `json:"digest"`
	Size   int64           `json:"size"`
}

// Inventory lists every published (not staged) blob object this platform
// currently holds, for embedding in a backup manifest. It contains no
// secret material and no local path.
func (p *Platform) Inventory(ctx context.Context) ([]BlobRecord, error) {
	if err := p.ensureOpen(); err != nil {
		return nil, err
	}
	return p.blobs.inventory(ctx)
}

// ExportMasterKeyMaterial resolves the master key from cfg's configured
// reference and returns it as raw bytes, so a trusted local caller (backup
// tooling -- never a public operation, never a JSON response, never a log
// record) can provision an explicit, separate key artifact for a clean
// destination. This is the key-transfer mechanics current opaque
// references are insufficient for: a "secret:" reference names an entry in
// this machine's OS keychain, which does not exist and cannot be resolved
// on a different destination host, so portability requires actually
// exporting the material once, out of band, not carrying the reference
// forward. A "file:" reference needs no state directory at all -- it is
// already portable, and this simply re-reads it; a "secret:" reference
// needs cfg.StateDir to already be an initialized installation, because the
// keychain namespace is derived from that installation's own identity.
//
// The caller owns the returned slice and must zero it after use, and must
// never place it in a manifest, log record or API result: only the
// separate, explicit provisioning artifact it is written to.
func ExportMasterKeyMaterial(ctx context.Context, cfg Config) ([]byte, error) {
	if err := platformSupported(); err != nil {
		return nil, err
	}
	if cfg.MasterKeyRef == "" {
		return nil, errf(contractCodeInvalidInput, "master key reference is required")
	}
	src, err := parseMasterKeyRef(cfg.MasterKeyRef)
	if err != nil {
		return nil, err
	}
	if src.kind == masterSourceFile {
		return resolveMasterKey(src, nil)
	}
	if cfg.StateDir == "" || !filepath.IsAbs(cfg.StateDir) {
		return nil, errf(contractCodeInvalidInput, "state directory must be an initialized installation's absolute path to export a secret: master key")
	}
	instance, err := ensureInstanceID(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	impl, err := newKeychainSecrets(instance)
	if err != nil {
		return nil, err
	}
	return resolveMasterKey(src, &secretStore{impl: impl})
}

// ImportForeignBlob decrypts a foreign-encrypted blob envelope (the same
// on-disk envelope format Stage/Publish produce, but sealed under ANOTHER
// installation's master key, as recovered via ExportMasterKeyMaterial at
// the source and explicit secure provisioning to this process) and
// republishes its verified plaintext into THIS platform's object store
// under this installation's own derived key -- "encrypt under the
// destination custody policy": a restored installation stops depending on
// the original backup's key material the moment import completes.
//
// Full integrity (per-chunk AEAD authentication against the foreign key,
// then the authenticated plaintext digest and size) is established before
// a single plaintext byte reaches staging; a corrupt or truncated envelope
// fails artifact_fault and nothing is staged, let alone published. An
// envelope whose authenticated digest does not match expectedDigest is
// refused the same way, before any republish is attempted. Republishing
// then runs through the ordinary Stage/Publish pipeline unchanged, so the
// same staging isolation, symlink defenses (openPrivate's O_NOFOLLOW,
// mkdirPrivate's symlink refusal) and idempotent/conflicting-publish rules
// already proven for ordinary artifacts apply here without a second,
// separately-audited crypto or filesystem path: a symlinked destination or
// an attempt to overwrite an existing live object with different bytes is
// refused exactly as Stage/Publish already refuse it for any other caller.
//
// f is opened by the caller with OpenForeignBlobFile (a locally staged
// backup artifact, opened O_NOFOLLOW the same as every other state-tree
// read in this package) and stays open for the duration of the call;
// ImportForeignBlob never follows a path itself and never takes a bare
// path or io.Reader, so a symlinked or attacker-controlled path cannot
// reach it except through that same guarded open.
func (p *Platform) ImportForeignBlob(ctx context.Context, f *os.File, foreignMasterKey []byte, expectedDigest contract.Digest) error {
	if err := p.ensureOpen(); err != nil {
		return err
	}
	if !validDigest(string(expectedDigest)) {
		return errf(contractCodeInvalidInput, "expected artifact digest is malformed")
	}
	if len(foreignMasterKey) != 32 {
		return errf(contractCodeInvalidInput, "foreign master key must be 32 bytes")
	}
	foreignBlobKey, err := deriveSubkey(foreignMasterKey, hkdfInfoBlob)
	if err != nil {
		return err
	}
	defer zero(foreignBlobKey)
	gcm, err := newCipher(foreignBlobKey)
	if err != nil {
		return err
	}
	plain, meta, err := decryptForeignFile(f, gcm, p.maxBytes)
	if err != nil {
		return err
	}
	defer zero(plain)
	if meta.Digest != string(expectedDigest) {
		return artifactFault("imported artifact does not match the expected digest")
	}

	ref, digest, _, err := p.blobs.Stage(ctx, bytes.NewReader(plain), int64(len(plain)))
	if err != nil {
		return err
	}
	if digest != expectedDigest {
		_ = p.blobs.RemoveStaged(ctx, ref)
		return artifactFault("imported artifact digest changed on restaging")
	}
	return p.blobs.Publish(ctx, ref, digest)
}

// OpenForeignBlobFile opens path (a locally staged backup artifact file) for
// ImportForeignBlob, refusing a symlink at path itself rather than
// following it (the same O_NOFOLLOW discipline as every other state-tree
// read in this package); the caller closes it.
func OpenForeignBlobFile(path string) (*os.File, error) {
	f, err := openPrivate(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, errWrap(contractCodeControllerUnavailable, "backup artifact cannot be opened", err)
	}
	return f, nil
}
