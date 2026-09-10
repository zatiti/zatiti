package platform

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/zatiti/zatiti/internal/contract"
)

const (
	stagingRefPrefix = "st1:"
	// diskReserveBytes is the headroom staging refuses to go below: disk
	// pressure rejects new artifact-producing admissions but never touches
	// referenced or unknown-obligation data.
	diskReserveBytes int64 = 512 << 20
)

// blobStore is the encrypted, content-addressed artifact store. Files are
// 0600 inside the 0700 state tree; every object is an authenticated
// envelope under the blob subkey of the master key.
type blobStore struct {
	stateDir  string
	stagedDir string
	objects   string
	max       int64
	key       []byte

	mu     sync.Mutex
	zeroed bool // keys wiped by Platform.Close: the store fails closed

	// probeFreeSpace is swappable in tests to make disk-pressure refusal
	// deterministic.
	probeFreeSpace func(dir string) (uint64, error)
}

func (bs *blobStore) zeroKeys() {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	zero(bs.key)
	bs.zeroed = true
}

// closed fails the call once keys are wiped; a closed store never serves
// decrypted artifact bytes.
func (bs *blobStore) closed() error {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if bs.zeroed {
		return errf(contractCodeControllerUnavailable, "platform is closed")
	}
	return nil
}

func newBlobStore(stateDir string, max int64, key []byte) (*blobStore, error) {
	return &blobStore{
		stateDir:       stateDir,
		stagedDir:      filepath.Join(stateDir, dirNameBlobs, dirNameStaged),
		objects:        filepath.Join(stateDir, dirNameBlobs, dirNameObjects),
		max:            max,
		key:            key,
		probeFreeSpace: defaultProbeFreeSpace,
	}, nil
}

func stagingRef(id string) string { return stagingRefPrefix + id }

func parseStagingRef(ref string) (string, error) {
	if len(ref) != len(stagingRefPrefix)+32 || ref[:len(stagingRefPrefix)] != stagingRefPrefix || !isHex(ref[len(stagingRefPrefix):], 32) {
		return "", errf(contractCodeNotFound, "staging identity is unknown")
	}
	return ref[len(stagingRefPrefix):], nil
}

func (bs *blobStore) objectPath(digest contract.Digest) string {
	return filepath.Join(bs.objects, string(digest)[:2], string(digest))
}

// Stage streams r into an encrypted envelope and returns the opaque staging
// identity, the SHA-256 digest of the exact plaintext bytes and the byte
// count. size is an upper bound on accepted bytes (0 means the platform
// bound only). Staged bytes are encrypted as they arrive; an interrupted
// stage leaves nothing behind.
func (bs *blobStore) Stage(ctx context.Context, r io.Reader, limit int64) (string, contract.Digest, int64, error) {
	if err := bs.closed(); err != nil {
		return "", "", 0, err
	}
	if limit < 0 {
		return "", "", 0, errf(contractCodeInvalidInput, "staging size bound must not be negative")
	}
	capBytes := bs.max
	if limit > 0 && limit < capBytes {
		capBytes = limit
	}
	if err := bs.checkPressure(); err != nil {
		return "", "", 0, err
	}
	gcm, err := newCipher(bs.key)
	if err != nil {
		return "", "", 0, err
	}
	id := randHex(16)
	tmpPath := filepath.Join(bs.stagedDir, ".tmp-"+id)
	f, err := openPrivate(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePrivate)
	if err != nil {
		return "", "", 0, errWrap(contractCodeControllerUnavailable, "staging file cannot be created", err)
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := writeEnvelopeHeader(f); err != nil {
		return "", "", 0, errWrap(contractCodeControllerUnavailable, "staging file cannot be written", err)
	}
	h := sha256.New()
	buf := make([]byte, blobChunkSize)
	var total int64
	var idx uint64
	pos := chunksStartOffset()
	var writeErr error
	for {
		if ctx.Err() != nil {
			return "", "", 0, errWrap(contractCodeControllerUnavailable, "staging was cancelled", ctx.Err())
		}
		n, rerr := io.ReadFull(r, buf)
		if n > 0 {
			if total+int64(n) > capBytes {
				return "", "", 0, artifactFault("artifact exceeds the configured size limit")
			}
			idx++
			sealed, serr := sealWith(gcm, buf[:n], chunkAAD(idx))
			if serr != nil {
				return "", "", 0, serr
			}
			header := putU32be(uint32(len(sealed)))
			if _, werr := f.WriteAt(header, pos); werr != nil {
				writeErr = werr
				break
			}
			if _, werr := f.WriteAt(sealed, pos+int64(len(header))); werr != nil {
				writeErr = werr
				break
			}
			pos += int64(len(header)) + int64(len(sealed))
			h.Write(buf[:n])
			total += int64(n)
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
				break
			}
			// A stream that fails because the caller cancelled reports the
			// cancellation, not an input fault.
			if errors.Is(rerr, context.Canceled) || errors.Is(rerr, context.DeadlineExceeded) {
				return "", "", 0, errWrap(contractCodeControllerUnavailable, "staging was cancelled", rerr)
			}
			return "", "", 0, errWrap(contractCodeArtifactFaultAlias, "staging input failed", rerr)
		}
	}
	if writeErr != nil {
		return "", "", 0, errWrap(contractCodeControllerUnavailable, "staging file cannot be written", writeErr)
	}
	meta := blobMeta{Chunk: blobChunkSize, Digest: hex.EncodeToString(h.Sum(nil)), Size: total}
	if err := writeEnvelopeMeta(f, gcm, meta); err != nil {
		return "", "", 0, errWrap(contractCodeControllerUnavailable, "staging file cannot be finalized", err)
	}
	if err := f.Sync(); err != nil {
		return "", "", 0, errWrap(contractCodeControllerUnavailable, "staging file cannot be flushed", err)
	}
	if err := f.Close(); err != nil {
		return "", "", 0, errWrap(contractCodeControllerUnavailable, "staging file cannot be closed", err)
	}
	finalPath := filepath.Join(bs.stagedDir, id)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return "", "", 0, errWrap(contractCodeControllerUnavailable, "staging file cannot be published", err)
	}
	ok = true
	if err := fsyncDir(bs.stagedDir); err != nil {
		return "", "", 0, err
	}
	return stagingRef(id), contract.Digest(meta.Digest), total, nil
}

// Publish verifies the staged envelope against the expected digest, then
// atomically moves it into the content-addressed store and flushes the
// directory entries. Publishing the same digest again succeeds without
// duplicating data; a mismatch or tampered staging file is refused.
func (bs *blobStore) Publish(ctx context.Context, stagingRefStr string, digest contract.Digest) error {
	if err := bs.closed(); err != nil {
		return err
	}
	if !validDigest(string(digest)) {
		return errf(contractCodeInvalidInput, "artifact digest is malformed")
	}
	id, err := parseStagingRef(stagingRefStr)
	if err != nil {
		return err
	}
	stagedPath := filepath.Join(bs.stagedDir, id)
	objectPath := bs.objectPath(digest)

	if _, statErr := os.Lstat(objectPath); statErr == nil {
		// Idempotent republication: verify the existing object, then drop
		// the redundant staging file.
		if err := bs.verifyObject(objectPath, digest); err != nil {
			return err
		}
		return removeFile(stagedPath)
	}

	f, err := openPrivate(stagedPath, os.O_RDONLY, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return artifactFault("staging identity is unknown or already consumed")
		}
		return errWrap(contractCodeControllerUnavailable, "staged artifact cannot be opened", err)
	}
	gcm, gerr := newCipher(bs.key)
	if gerr != nil {
		_ = f.Close()
		return gerr
	}
	meta, verr := verifyEnvelope(f, gcm)
	_ = f.Close()
	if verr != nil {
		return verr
	}
	if meta.Digest != string(digest) {
		return artifactFault("staged bytes do not match the published digest")
	}
	if err := mkdirPrivate(filepath.Join(bs.objects, string(digest)[:2])); err != nil {
		return err
	}
	if err := os.Rename(stagedPath, objectPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			// Raced with an identical publication; verify and finish.
			if err := bs.verifyObject(objectPath, digest); err != nil {
				return err
			}
			return removeFile(stagedPath)
		}
		return errWrap(contractCodeControllerUnavailable, "artifact cannot be published", err)
	}
	if err := fsyncDir(filepath.Join(bs.objects, string(digest)[:2])); err != nil {
		return err
	}
	return fsyncDir(bs.stagedDir)
}

// verifyObject runs the full-integrity pass on a stored object and checks
// its digest matches the requested one.
func (bs *blobStore) verifyObject(path string, digest contract.Digest) error {
	f, err := openPrivate(path, os.O_RDONLY, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return artifactFault("artifact bytes are unavailable")
		}
		return errWrap(contractCodeControllerUnavailable, "artifact bytes cannot be opened", err)
	}
	defer func() { _ = f.Close() }()
	gcm, gerr := newCipher(bs.key)
	if gerr != nil {
		return gerr
	}
	meta, verr := verifyEnvelope(f, gcm)
	if verr != nil {
		return verr
	}
	if meta.Digest != string(digest) {
		return artifactFault("artifact bytes do not match the referenced digest")
	}
	return nil
}

// Open verifies the full integrity of the referenced object and then returns
// a reader over the requested bounded window. Corrupt or missing bytes fail
// with artifact_fault before any byte is returned.
func (bs *blobStore) Open(ctx context.Context, digest contract.Digest, offset, length int64) (io.ReadCloser, error) {
	if err := bs.closed(); err != nil {
		return nil, err
	}
	if !validDigest(string(digest)) {
		return nil, errf(contractCodeInvalidInput, "artifact digest is malformed")
	}
	if offset < 0 || length < 0 {
		return nil, errf(contractCodeInvalidInput, "artifact read range must not be negative")
	}
	objectPath := bs.objectPath(digest)
	f, err := openPrivate(objectPath, os.O_RDONLY, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, artifactFault("artifact bytes are unavailable")
		}
		return nil, errWrap(contractCodeControllerUnavailable, "artifact bytes cannot be opened", err)
	}
	gcm, gerr := newCipher(bs.key)
	if gerr != nil {
		_ = f.Close()
		return nil, gerr
	}
	meta, verr := verifyEnvelope(f, gcm)
	_ = f.Close()
	if verr != nil {
		return nil, verr
	}
	if meta.Digest != string(digest) {
		return nil, artifactFault("artifact bytes do not match the referenced digest")
	}
	// Full integrity is established; the window is bounded by the object.
	// A zero length selects the whole remainder from offset.
	if offset >= meta.Size {
		if offset > meta.Size {
			return nil, artifactFault("artifact read range is outside the referenced artifact")
		}
		return io.NopCloser(&emptyReader{}), nil
	}
	end := meta.Size
	if length > 0 {
		end = offset + length
		if end > meta.Size || end < 0 { // end < 0 guards offset+length overflow
			end = meta.Size
		}
	}
	f, err = openPrivate(objectPath, os.O_RDONLY, 0)
	if err != nil {
		return nil, errWrap(contractCodeControllerUnavailable, "artifact bytes cannot be opened", err)
	}
	return &blobReader{
		f:      f,
		gcm:    gcm,
		offset: offset,
		end:    end,
	}, nil
}

// blobReader streams the decrypted window [offset, end) after the full
// object has already passed integrity verification in Open.
type blobReader struct {
	f       *os.File
	gcm     cipherAEAD
	offset  int64
	end     int64
	started bool
	cur     []byte // decrypted chunk bytes not yet delivered
	pos     int64  // file position of the next chunk header
	idx     uint64 // next chunk index
}

func (br *blobReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if br.offset >= br.end {
		return 0, io.EOF
	}
	if !br.started {
		br.started = true
		br.pos = chunksStartOffset()
		br.idx = 0
		// Skip whole chunks before the window start.
		skip := uint64(br.offset / blobChunkSize)
		for i := uint64(1); i <= skip; i++ {
			var lenBuf [4]byte
			if _, err := br.f.ReadAt(lenBuf[:], br.pos); err != nil {
				return 0, artifactFault("artifact bytes failed integrity verification")
			}
			ctLen := int64(binary.BigEndian.Uint32(lenBuf[:]))
			br.pos += 4 + int64(ctLen)
			br.idx = i
		}
	}
	for len(br.cur) == 0 {
		if br.offset >= br.end {
			return 0, io.EOF
		}
		br.idx++
		plain, next, err := readChunkAt(br.f, br.gcm, br.pos, br.idx, int(blobChunkSize))
		if err != nil {
			return 0, err
		}
		br.pos = next
		skipInto := br.offset - int64(br.idx-1)*blobChunkSize
		if skipInto > 0 {
			if skipInto > int64(len(plain)) {
				return 0, artifactFault("artifact bytes failed integrity verification")
			}
			plain = plain[skipInto:]
		}
		br.cur = plain
	}
	// The window bound caps every read: callers may pass buffers larger
	// than the remaining window.
	remaining := br.end - br.offset
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n := copy(p, br.cur)
	br.cur = br.cur[n:]
	br.offset += int64(n)
	if br.offset >= br.end {
		// Window satisfied; any leftover decrypted chunk bytes beyond br.end
		// are discarded by the offset check on subsequent reads.
		return n, io.EOF
	}
	return n, nil
}

func (br *blobReader) Close() error {
	if br.f == nil {
		return nil
	}
	err := br.f.Close()
	br.f = nil
	return err
}

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

// RemoveStaged discards a staged object that was never published. It is
// idempotent: removing an unknown or already-consumed staging identity
// succeeds.
func (bs *blobStore) RemoveStaged(ctx context.Context, ref string) error {
	if err := bs.closed(); err != nil {
		return err
	}
	id, err := parseStagingRef(ref)
	if err != nil {
		return err
	}
	return removeFile(filepath.Join(bs.stagedDir, id))
}

// checkPressure refuses new artifact-producing admissions when free disk
// space is below the reserve. Fail-closed: an unreadable filesystem is
// treated as pressure, never as room.
func (bs *blobStore) checkPressure() error {
	free, err := bs.probeFreeSpace(bs.stagedDir)
	if err != nil {
		return &Error{
			Code:      contractCodeControllerUnavailable,
			Message:   "disk space cannot be determined; artifact staging is paused",
			Retryable: true,
			cause:     err,
		}
	}
	if int64(free) < diskReserveBytes {
		return &Error{
			Code:      contractCodeControllerUnavailable,
			Message:   "insufficient disk space; artifact staging is paused",
			Retryable: true,
		}
	}
	return nil
}
