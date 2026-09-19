package installation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"runtime/debug"

	"github.com/zatiti/zatiti/internal/contract"
)

// Backup bundle format (zatiti.backup/v1 bundle framing). A backup artifact
// is one sealBundle-encrypted frame:
//
//	magic "ZTBK1\n"
//	uvarint manifest length, manifest JSON (BackupManifest)
//	uvarint image length, the consistent SQLite image (archive entry
//	  "state.sqlite", the manifest's database_archive_entry)
//
// The manifest's database_digest and database_size describe exactly the
// image bytes framed here, as hashed and counted while the DatabaseBackup
// capability streamed them. The recovery overlay artifact is the same
// framing with a RecoveryOverlay manifest and an empty image.

const (
	bundleMagic = "ZTBK1\n"
	// databaseArchiveEntry is the manifest's frozen database_archive_entry.
	databaseArchiveEntry = "state.sqlite"
	// maxBackupImageBytes bounds the database image a backup buffers before
	// sealing: the frozen per-artifact bound. A larger database fails the
	// backup by name rather than producing an artifact no store accepts.
	maxBackupImageBytes = 256 << 20
	// backupMediaType and overlayMediaType are the published artifacts'
	// media types; both are sealed frames, never plaintext.
	backupMediaType  = "application/vnd.zatiti.backup.v1+encrypted"
	overlayMediaType = "application/vnd.zatiti.recovery-overlay.v1+encrypted"
	backupClass      = "restricted"
)

// errImageTooLarge reports a database image beyond maxBackupImageBytes.
var errImageTooLarge = errors.New("database image exceeds the backup bound")

// errBundleForeign reports a bundle bound to another installation.
var errBundleForeign = errors.New("bundle manifest belongs to a different installation")

// hashingWriter counts and hashes every byte written through it.
type hashingWriter struct {
	w io.Writer
	h hash.Hash
	n int64
}

func newHashingWriter(w io.Writer) *hashingWriter {
	return &hashingWriter{w: w, h: sha256.New()}
}

func (hw *hashingWriter) Write(p []byte) (int, error) {
	n, err := hw.w.Write(p)
	hw.h.Write(p[:n])
	hw.n += int64(n)
	return n, err
}

func (hw *hashingWriter) digest() contract.Digest {
	return contract.Digest(hex.EncodeToString(hw.h.Sum(nil)))
}

// boundedBuffer refuses bytes past its limit so a runaway image never grows
// unbounded in memory.
type boundedBuffer struct {
	buf   bytes.Buffer
	limit int64
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if int64(b.buf.Len())+int64(len(p)) > b.limit {
		return 0, errImageTooLarge
	}
	return b.buf.Write(p)
}

// captureImage streams one consistent database image from the capability
// through a hashing, counting writer into a bounded buffer. A failure
// mid-stream returns the error and no image: nothing downstream can build a
// manifest from a partial copy.
func captureImage(ctx context.Context, backup contract.DatabaseBackup) ([]byte, contract.Digest, int64, error) {
	buf := &boundedBuffer{limit: maxBackupImageBytes}
	hw := newHashingWriter(buf)
	if err := backup.Backup(ctx, hw); err != nil {
		return nil, "", 0, err
	}
	if hw.n == 0 {
		return nil, "", 0, errors.New("database backup produced no bytes")
	}
	return buf.buf.Bytes(), hw.digest(), hw.n, nil
}

// digestImage streams one consistent database image through a hashing
// writer and discards the bytes: the recovery overlay needs only the
// digest of the current database.
func digestImage(ctx context.Context, backup contract.DatabaseBackup) (contract.Digest, int64, error) {
	hw := newHashingWriter(io.Discard)
	if err := backup.Backup(ctx, hw); err != nil {
		return "", 0, err
	}
	if hw.n == 0 {
		return "", 0, errors.New("database backup produced no bytes")
	}
	return hw.digest(), hw.n, nil
}

// encodeFrame builds the plaintext frame of a bundle.
func encodeFrame(manifest, image []byte) []byte {
	var lens [2 * binary.MaxVarintLen64]byte
	n := binary.PutUvarint(lens[:], uint64(len(manifest)))
	out := make([]byte, 0, len(bundleMagic)+n+len(manifest)+binary.MaxVarintLen64+len(image))
	out = append(out, bundleMagic...)
	out = append(out, lens[:n]...)
	out = append(out, manifest...)
	n = binary.PutUvarint(lens[:], uint64(len(image)))
	out = append(out, lens[:n]...)
	out = append(out, image...)
	return out
}

// decodeFrame splits a decrypted frame into its manifest and image.
func decodeFrame(frame []byte) (manifest, image []byte, err error) {
	if !bytes.HasPrefix(frame, []byte(bundleMagic)) {
		return nil, nil, errors.New("bundle frame carries no zatiti backup magic")
	}
	rest := frame[len(bundleMagic):]
	manifest, rest, err = readSection(rest, "manifest")
	if err != nil {
		return nil, nil, err
	}
	image, rest, err = readSection(rest, "image")
	if err != nil {
		return nil, nil, err
	}
	if len(rest) != 0 {
		return nil, nil, errors.New("bundle frame carries trailing bytes")
	}
	return manifest, image, nil
}

func readSection(in []byte, name string) (section, rest []byte, err error) {
	length, n := binary.Uvarint(in)
	if n <= 0 {
		return nil, nil, fmt.Errorf("bundle frame %s length is malformed", name)
	}
	in = in[n:]
	if length > uint64(len(in)) {
		return nil, nil, fmt.Errorf("bundle frame %s is truncated", name)
	}
	return in[:length], in[length:], nil
}

// digestOf hashes bytes the way every digest on the wire is expressed.
func digestOf(b []byte) contract.Digest {
	sum := sha256.Sum256(b)
	return contract.Digest(hex.EncodeToString(sum[:]))
}

// buildRevision reports the source revision and module version of the
// running binary for the manifest's provenance fields, or "unknown" when
// the build carries no such information. It never invents a revision.
func buildRevision() (sourceRevision, controllerVersion string) {
	sourceRevision, controllerVersion = "unknown", "unknown"
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return sourceRevision, controllerVersion
	}
	if info.Main.Version != "" {
		controllerVersion = info.Main.Version
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && setting.Value != "" {
			sourceRevision = setting.Value
		}
	}
	return sourceRevision, controllerVersion
}

// readPublished reads one published blob in full, bounded by size+1 so a
// blob larger than its recorded size is detected rather than buffered.
func readPublished(ctx context.Context, blobs contract.BlobStore, digest contract.Digest, size int64) ([]byte, error) {
	rc, err := blobs.Open(ctx, digest, 0, size)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != size {
		return nil, fmt.Errorf("published bundle is %d bytes, expected %d", len(data), size)
	}
	return data, nil
}

// stagePublished stages and publishes sealed bytes, returning the published
// digest and size. On any failure nothing stays staged.
func stagePublished(ctx context.Context, blobs contract.BlobStore, sealed []byte) (contract.Digest, int64, error) {
	ref, digest, size, err := blobs.Stage(ctx, bytes.NewReader(sealed), 0)
	if err != nil {
		return "", 0, err
	}
	if size != int64(len(sealed)) || digest != digestOf(sealed) {
		_ = blobs.RemoveStaged(ctx, ref)
		return "", 0, errors.New("staged bundle bytes do not match the sealed bundle")
	}
	if err := blobs.Publish(ctx, ref, digest); err != nil {
		_ = blobs.RemoveStaged(ctx, ref)
		return "", 0, err
	}
	return digest, size, nil
}
