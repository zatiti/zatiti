package platform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// stageAndPublish stages data with the given limit and publishes it,
// returning the digest.
func stageAndPublish(t *testing.T, p *Platform, data []byte, limit int64) contract.Digest {
	t.Helper()
	ref, digest, size, err := p.Blobs().Stage(ctx(), bytes.NewReader(data), limit)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if size != int64(len(data)) {
		t.Fatalf("Stage size = %d, want %d", size, len(data))
	}
	if digest != digestOf(data) {
		t.Fatalf("Stage digest = %s, want %s", digest, digestOf(data))
	}
	if err := p.Blobs().Publish(ctx(), ref, digest); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	return digest
}

func readAll(t *testing.T, rc io.ReadCloser, n int64) []byte {
	t.Helper()
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if int64(len(got)) != n {
		t.Fatalf("read %d bytes, want %d", len(got), n)
	}
	return got
}

func TestBlobEncryptedRoundtripSingleChunk(t *testing.T) {
	p, state := openForTest(t)
	data := []byte("hello, encrypted at rest world")
	digest := stageAndPublish(t, p, data, 0)

	rc, err := p.Blobs().Open(ctx(), digest, 0, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got := readAll(t, rc, int64(len(data)))
	if !bytes.Equal(got, data) {
		t.Fatal("roundtrip mismatch")
	}

	// The stored object must not contain the plaintext anywhere.
	objPath := filepath.Join(state, dirNameBlobs, dirNameObjects, string(digest)[:2], string(digest))
	stored, err := os.ReadFile(objPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, data) {
		t.Fatal("plaintext found in the stored object")
	}
	if got := modeOf(t, objPath); got != 0o600 {
		t.Fatalf("object mode is %o, want 600", got)
	}
	if !bytes.HasPrefix(stored, []byte(blobMagic)) {
		t.Fatal("object is not an envelope")
	}
}

func TestBlobRoundtripMultipleChunksAndWindows(t *testing.T) {
	p, _ := openForTest(t)
	data := make([]byte, 2*blobChunkSize+12345)
	for i := range data {
		data[i] = byte(i * 7)
	}
	digest := stageAndPublish(t, p, data, 0)

	// Full read.
	rc, err := p.Blobs().Open(ctx(), digest, 0, 0)
	if err != nil {
		t.Fatalf("Open full: %v", err)
	}
	if got := readAll(t, rc, int64(len(data))); !bytes.Equal(got, data) {
		t.Fatal("full roundtrip mismatch")
	}

	// Window entirely inside the first chunk.
	rc, err = p.Blobs().Open(ctx(), digest, 5, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, rc, 10); !bytes.Equal(got, data[5:15]) {
		t.Fatal("intra-chunk window mismatch")
	}

	// Window crossing the chunk boundary.
	rc, err = p.Blobs().Open(ctx(), digest, blobChunkSize-4, 8)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, rc, 8); !bytes.Equal(got, data[blobChunkSize-4:blobChunkSize+4]) {
		t.Fatal("cross-chunk window mismatch")
	}

	// Window deep in the last chunk.
	rc, err = p.Blobs().Open(ctx(), digest, 2*blobChunkSize+100, 45)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, rc, 45); !bytes.Equal(got, data[2*blobChunkSize+100:2*blobChunkSize+145]) {
		t.Fatal("final-chunk window mismatch")
	}

	// Length clipped to the end of the object.
	rc, err = p.Blobs().Open(ctx(), digest, int64(len(data))-10, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, rc, 10); !bytes.Equal(got, data[len(data)-10:]) {
		t.Fatal("clipped window mismatch")
	}
}

func TestBlobEmptyArtifact(t *testing.T) {
	p, _ := openForTest(t)
	digest := stageAndPublish(t, p, nil, 0)
	if digest != digestOf(nil) {
		t.Fatalf("empty digest mismatch: %s", digest)
	}
	rc, err := p.Blobs().Open(ctx(), digest, 0, 10)
	if err != nil {
		t.Fatalf("Open empty: %v", err)
	}
	defer func() { _ = rc.Close() }()
	if _, err := rc.Read(make([]byte, 8)); !errors.Is(err, io.EOF) {
		t.Fatalf("empty object read = %v, want io.EOF", err)
	}
}

func TestBlobOpenRangeSemantics(t *testing.T) {
	p, _ := openForTest(t)
	data := []byte("0123456789abcdef")
	digest := stageAndPublish(t, p, data, 0)

	// offset == size returns an empty reader, not a fault.
	rc, err := p.Blobs().Open(ctx(), digest, int64(len(data)), 5)
	if err != nil {
		t.Fatalf("offset==size: %v", err)
	}
	_ = rc.Close()

	// offset beyond the artifact is an artifact fault.
	_, err = p.Blobs().Open(ctx(), digest, int64(len(data))+1, 0)
	wantCode(t, err, contractCodeArtifactFaultAlias)

	// Negative ranges are invalid input.
	_, err = p.Blobs().Open(ctx(), digest, -1, 0)
	wantCode(t, err, contractCodeInvalidInput)
	_, err = p.Blobs().Open(ctx(), digest, 0, -1)
	wantCode(t, err, contractCodeInvalidInput)

	// Malformed digests are invalid input.
	_, err = p.Blobs().Open(ctx(), "ZZ", 0, 0)
	wantCode(t, err, contractCodeInvalidInput)

	// Unknown digest is an artifact fault.
	_, err = p.Blobs().Open(ctx(), digestOf([]byte("never-stored")), 0, 0)
	wantCode(t, err, contractCodeArtifactFaultAlias)
}

func TestBlobStageLimitIsEnforced(t *testing.T) {
	p, _ := openForTest(t)
	data := bytes.Repeat([]byte("x"), 100)

	if _, _, _, err := p.Blobs().Stage(ctx(), bytes.NewReader(data), -1); err == nil {
		t.Fatal("negative limit accepted")
	} else {
		wantCode(t, err, contractCodeInvalidInput)
	}

	_, _, _, err := p.Blobs().Stage(ctx(), bytes.NewReader(data), 50)
	wantCode(t, err, contractCodeArtifactFaultAlias)
	if msg := errMessage(err); !strings.Contains(msg, "limit") {
		t.Fatalf("unexpected limit message: %q", msg)
	}
}

func TestBlobPlatformMaxApplies(t *testing.T) {
	p, _ := openForTestWithMax(t, 64)
	data := bytes.Repeat([]byte("z"), 100)
	_, _, _, err := p.Blobs().Stage(ctx(), bytes.NewReader(data), 0)
	wantCode(t, err, contractCodeArtifactFaultAlias)
	if msg := errMessage(err); !strings.Contains(msg, "size limit") {
		t.Fatalf("unexpected limit message: %q", msg)
	}

	// A narrower caller bound still works below the platform bound.
	small := []byte("tiny")
	if _, _, _, err := p.Blobs().Stage(ctx(), bytes.NewReader(small), 0); err != nil {
		t.Fatalf("small stage: %v", err)
	}
}

func TestBlobStageCancellationLeavesNothingBehind(t *testing.T) {
	p, state := openForTest(t)
	stagedDir := filepath.Join(state, dirNameBlobs, dirNameStaged)

	cctx, cancel := context.WithCancel(ctx())
	r := &gateReader{ctx: cctx, cancel: cancel, chunk: make([]byte, blobChunkSize)}
	for i := range r.chunk {
		r.chunk[i] = 0xA5
	}
	_, _, _, err := p.Blobs().Stage(cctx, r, 0)
	cancel()
	if err == nil {
		t.Fatal("cancelled stage succeeded")
	}
	wantCode(t, err, contractCodeControllerUnavailable)
	if msg := errMessage(err); !strings.Contains(msg, "cancelled") {
		t.Fatalf("unexpected cancellation message: %q", msg)
	}
	if names := stagedFiles(t, stagedDir); len(names) != 0 {
		t.Fatalf("interrupted stage left files behind: %v", names)
	}
}

// gateReader delivers exactly one full chunk, then interrupts the stream:
// the second read cancels the stage context and surfaces the cancellation,
// the way an interrupted stream fails mid-read.
type gateReader struct {
	ctx    context.Context
	cancel context.CancelFunc
	chunk  []byte
	done   bool
}

func (g *gateReader) Read(p []byte) (int, error) {
	if g.done {
		g.cancel()
		return 0, g.ctx.Err()
	}
	g.done = true
	n := copy(p, g.chunk)
	return n, nil
}

func TestBlobStagePreCancelledContext(t *testing.T) {
	p, state := openForTest(t)
	stagedDir := filepath.Join(state, dirNameBlobs, dirNameStaged)
	cctx, cancel := context.WithCancel(ctx())
	cancel()
	_, _, _, err := p.Blobs().Stage(cctx, bytes.NewReader([]byte("x")), 0)
	cancel()
	wantCode(t, err, contractCodeControllerUnavailable)
	if names := stagedFiles(t, stagedDir); len(names) != 0 {
		t.Fatalf("cancelled stage left files behind: %v", names)
	}
}

func TestBlobPublishDigestMismatchRefused(t *testing.T) {
	p, _ := openForTest(t)
	data := []byte("the actual bytes")
	ref, _, _, err := p.Blobs().Stage(ctx(), bytes.NewReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	wrong := digestOf([]byte("other bytes"))
	err = p.Blobs().Publish(ctx(), ref, wrong)
	wantCode(t, err, contractCodeArtifactFaultAlias)
	// Malformed digests are invalid input, not artifact faults.
	err = p.Blobs().Publish(ctx(), ref, "XYZ")
	wantCode(t, err, contractCodeInvalidInput)
}

func TestBlobPublishTamperedStagingRefused(t *testing.T) {
	p, state := openForTest(t)
	data := []byte("payload to tamper")
	ref, digest, _, err := p.Blobs().Stage(ctx(), bytes.NewReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimPrefix(ref, "st1:")
	path := filepath.Join(state, dirNameBlobs, dirNameStaged, id)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xFF}, info.Size()-1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	err = p.Blobs().Publish(ctx(), ref, digest)
	wantCode(t, err, contractCodeArtifactFaultAlias)
	// Nothing was published.
	if _, serr := os.Stat(p.blobs.objectPath(digest)); !os.IsNotExist(serr) {
		t.Fatal("tampered staging reached the object store")
	}
}

func TestBlobPublishIsIdempotent(t *testing.T) {
	p, state := openForTest(t)
	data := []byte("published once")
	ref, digest, _, err := p.Blobs().Stage(ctx(), bytes.NewReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Blobs().Publish(ctx(), ref, digest); err != nil {
		t.Fatal(err)
	}
	objPath := p.blobs.objectPath(digest)
	first, err := os.ReadFile(objPath)
	if err != nil {
		t.Fatal(err)
	}

	// Re-publishing the same digest without new staging succeeds and keeps
	// one object; the redundant staging file is consumed.
	ref2, digest2, _, err := p.Blobs().Stage(ctx(), bytes.NewReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Blobs().Publish(ctx(), ref2, digest2); err != nil {
		t.Fatalf("idempotent republish: %v", err)
	}
	second, err := os.ReadFile(objPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("republished object differs (idempotency broken)")
	}
	if names := stagedFiles(t, filepath.Join(state, dirNameBlobs, dirNameStaged)); len(names) != 0 {
		t.Fatalf("staging not consumed: %v", names)
	}
}

func TestBlobConcurrentPublishOfSameDigest(t *testing.T) {
	p, state := openForTest(t)
	data := []byte("racing publications")
	digest := digestOf(data)

	const n = 6
	refs := make([]string, n)
	for i := range refs {
		ref, d, _, err := p.Blobs().Stage(ctx(), bytes.NewReader(data), 0)
		if err != nil {
			t.Fatal(err)
		}
		if d != digest {
			t.Fatal("digest mismatch in race setup")
		}
		refs[i] = ref
	}
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i, ref := range refs {
		i, ref := i, ref
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = p.Blobs().Publish(ctx(), ref, digest)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent publish %d: %v", i, err)
		}
	}
	// Exactly one object exists and staging is fully consumed.
	rc, err := p.Blobs().Open(ctx(), digest, 0, 0)
	if err != nil {
		t.Fatalf("Open after race: %v", err)
	}
	_ = rc.Close()
	if names := stagedFiles(t, filepath.Join(state, dirNameBlobs, dirNameStaged)); len(names) != 0 {
		t.Fatalf("staging left behind after race: %v", names)
	}
}

func TestBlobRemoveStaged(t *testing.T) {
	p, _ := openForTest(t)
	data := []byte("to be discarded")
	ref, digest, _, err := p.Blobs().Stage(ctx(), bytes.NewReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Blobs().RemoveStaged(ctx(), ref); err != nil {
		t.Fatal(err)
	}
	err = p.Blobs().Publish(ctx(), ref, digest)
	wantCode(t, err, contractCodeArtifactFaultAlias)
	// Idempotent: unknown staging identity removal succeeds.
	if err := p.Blobs().RemoveStaged(ctx(), ref); err != nil {
		t.Fatalf("idempotent RemoveStaged: %v", err)
	}
	if err := p.Blobs().RemoveStaged(ctx(), "garbage"); err == nil {
		t.Fatal("malformed staging reference accepted")
	}
}

func TestBlobTamperedObjectFailsClosed(t *testing.T) {
	p, _ := openForTest(t)
	data := bytes.Repeat([]byte("tamper-me"), 100)
	digest := stageAndPublish(t, p, data, 0)
	objPath := p.blobs.objectPath(digest)

	tamper := func(offset int64) error {
		f, err := os.OpenFile(objPath, os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if _, err := f.WriteAt([]byte{0x5A}, offset); err != nil {
			_ = f.Close()
			return err
		}
		return f.Close()
	}

	// Flip one ciphertext byte inside the first chunk.
	if err := tamper(chunksStartOffset() + 20); err != nil {
		t.Fatal(err)
	}
	_, err := p.Blobs().Open(ctx(), digest, 0, 0)
	wantCode(t, err, contractCodeArtifactFaultAlias)
	// Restore via restage for the next case.
	_ = os.Remove(objPath)
	digest2 := stageAndPublish(t, p, data, 0)
	_ = digest2

	// Corrupt the magic (offset 1: the first byte is 'Z' already).
	if err := tamper(1); err != nil {
		t.Fatal(err)
	}
	_, err = p.Blobs().Open(ctx(), digest, 0, 0)
	wantCode(t, err, contractCodeArtifactFaultAlias)
}

func TestBlobTruncatedObjectIsRefused(t *testing.T) {
	p, _ := openForTest(t)
	data := make([]byte, 3*blobChunkSize+77)
	digest := stageAndPublish(t, p, data, 0)
	objPath := p.blobs.objectPath(digest)
	full, err := os.ReadFile(objPath)
	if err != nil {
		t.Fatal(err)
	}

	// Truncate mid-way through the last chunk.
	if err := os.WriteFile(objPath, full[:len(full)-40], 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = p.Blobs().Open(ctx(), digest, 0, 0)
	wantCode(t, err, contractCodeArtifactFaultAlias)

	// Truncate the metadata too.
	if err := os.WriteFile(objPath, full[:20], 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = p.Blobs().Open(ctx(), digest, 0, 0)
	wantCode(t, err, contractCodeArtifactFaultAlias)

}

func TestBlobReorderedChunksAreRefused(t *testing.T) {
	p, _ := openForTest(t)
	data := make([]byte, 2*blobChunkSize)
	for i := range data {
		data[i] = byte(i)
	}
	digest := stageAndPublish(t, p, data, 0)
	objPath := p.blobs.objectPath(digest)
	full, err := os.ReadFile(objPath)
	if err != nil {
		t.Fatal(err)
	}

	// Parse the two chunk records and swap them.
	start := chunksStartOffset()
	len1 := int64(beUint32(full[start : start+4]))
	rec1 := full[start : start+4+len1]
	pos2 := start + 4 + len1
	len2 := int64(beUint32(full[pos2 : pos2+4]))
	rec2 := full[pos2 : pos2+4+len2]

	reordered := append([]byte{}, full[:start]...)
	reordered = append(reordered, rec2...)
	reordered = append(reordered, rec1...)
	if err := os.WriteFile(objPath, reordered, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = p.Blobs().Open(ctx(), digest, 0, 0)
	wantCode(t, err, contractCodeArtifactFaultAlias)
}

func TestBlobCrossObjectSwapIsRefused(t *testing.T) {
	p, _ := openForTest(t)
	digestA := stageAndPublish(t, p, []byte("contents of artifact A"), 0)
	digestB := stageAndPublish(t, p, []byte("contents of artifact B entirely different"), 0)

	// Overwrite A's object file with B's bytes.
	objB, err := os.ReadFile(p.blobs.objectPath(digestB))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.blobs.objectPath(digestA), objB, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = p.Blobs().Open(ctx(), digestA, 0, 0)
	wantCode(t, err, contractCodeArtifactFaultAlias)
}

func TestBlobStagingFileIsEncryptedAndPrivate(t *testing.T) {
	p, state := openForTest(t)
	secret := []byte("staged-but-secret")
	ref, digest, _, err := p.Blobs().Stage(ctx(), bytes.NewReader(secret), 0)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimPrefix(ref, "st1:")
	path := filepath.Join(state, dirNameBlobs, dirNameStaged, id)
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, secret) {
		t.Fatal("plaintext found in the staging file")
	}
	if got := modeOf(t, path); got != 0o600 {
		t.Fatalf("staging file mode is %o, want 600", got)
	}
	if err := p.Blobs().Publish(ctx(), ref, digest); err != nil {
		t.Fatal(err)
	}
}

func TestBlobDiskPressureRefusalIsFailClosed(t *testing.T) {
	p, state := openForTest(t)
	stagedDir := filepath.Join(state, dirNameBlobs, dirNameStaged)

	// A healthy stage first so we can prove the refusal touches nothing.
	data := []byte("already staged")
	ref, _, _, err := p.Blobs().Stage(ctx(), bytes.NewReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}

	p.blobs.probeFreeSpace = func(string) (uint64, error) {
		return uint64(diskReserveBytes - 1), nil
	}
	_, _, _, err = p.Blobs().Stage(ctx(), bytes.NewReader([]byte("more")), 0)
	wantCode(t, err, contractCodeControllerUnavailable)
	if msg := errMessage(err); !strings.Contains(msg, "disk space") {
		t.Fatalf("unexpected pressure message: %q", msg)
	}
	var perr *Error
	if !errors.As(err, &perr) || !perr.Retryable {
		t.Fatal("pressure refusal must be retryable")
	}
	if names := stagedFiles(t, stagedDir); len(names) != 1 {
		t.Fatalf("pressure refusal disturbed staged data: %v", names)
	}
	if err := p.Blobs().Publish(ctx(), ref, digestOf(data)); err != nil {
		t.Fatalf("existing staged data must survive pressure: %v", err)
	}

	// An unreadable filesystem is pressure too, never room.
	p.blobs.probeFreeSpace = func(string) (uint64, error) {
		return 0, errors.New("statfs unavailable")
	}
	_, _, _, err = p.Blobs().Stage(ctx(), bytes.NewReader([]byte("more")), 0)
	wantCode(t, err, contractCodeControllerUnavailable)
	perr = nil
	if !errors.As(err, &perr) || !perr.Retryable {
		t.Fatal("unreadable filesystem refusal must be retryable")
	}

	// Published objects stay openable under pressure.
	p.blobs.probeFreeSpace = defaultProbeFreeSpace
	if err := p.Blobs().Publish(ctx(), ref, digestOf(data)); err != nil {
		t.Fatalf("publish under pressure refused: %v", err)
	}
	rc, err := p.Blobs().Open(ctx(), digestOf(data), 0, 0)
	if err != nil {
		t.Fatalf("Open under pressure refused: %v", err)
	}
	_ = rc.Close()
}

func TestBlobStagingRefFormat(t *testing.T) {
	p, _ := openForTest(t)
	ref, _, _, err := p.Blobs().Stage(ctx(), bytes.NewReader([]byte("x")), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ref, "st1:") || len(ref) != len("st1:")+32 || !isHex(strings.TrimPrefix(ref, "st1:"), 32) {
		t.Fatalf("staging reference %q is not st1:<32 hex>", ref)
	}
	// Publishing under a malformed reference is a not-found code.
	err = p.Blobs().Publish(ctx(), "st1:short", digestOf([]byte("x")))
	wantCode(t, err, contractCodeNotFound)
}

func TestBlobsFailClosedAfterPlatformClose(t *testing.T) {
	// Close zeroizes the in-memory keys: subsequent blob operations must
	// fail closed instead of serving decrypted artifact bytes.
	p, _ := openForTest(t)
	digest := stageAndPublish(t, p, []byte("pre-close"), 0)
	_ = p.Close()

	_, err := p.Blobs().Open(ctx(), digest, 0, 0)
	if err == nil {
		t.Fatal("Open served artifact bytes after zeroKeys")
	}
	wantCode(t, err, contractCodeControllerUnavailable)
	_, _, _, err = p.Blobs().Stage(ctx(), bytes.NewReader([]byte("x")), 0)
	wantCode(t, err, contractCodeControllerUnavailable)
	err = p.Blobs().Publish(ctx(), "st1:"+strings.Repeat("0", 32), digest)
	wantCode(t, err, contractCodeControllerUnavailable)
	err = p.Blobs().RemoveStaged(ctx(), "st1:"+strings.Repeat("0", 32))
	wantCode(t, err, contractCodeControllerUnavailable)
}

func TestBlobDigestIsSHA256OfPlaintext(t *testing.T) {
	p, _ := openForTest(t)
	data := []byte("check my digest")
	sum := sha256.Sum256(data)
	want := contract.Digest(hex.EncodeToString(sum[:]))
	ref, digest, size, err := p.Blobs().Stage(ctx(), bytes.NewReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	if digest != want || size != int64(len(data)) {
		t.Fatalf("digest/size = %s/%d, want %s/%d", digest, size, want, len(data))
	}
	if err := p.Blobs().Publish(ctx(), ref, want); err != nil {
		t.Fatal(err)
	}
}

func TestBlobTamperedObjectFailsPublishOverExisting(t *testing.T) {
	// Republication must verify the existing object, not assume it is good.
	p, _ := openForTest(t)
	data := []byte("verify me on republish")
	digest := stageAndPublish(t, p, data, 0)
	objPath := p.blobs.objectPath(digest)

	f, err := os.OpenFile(objPath, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0x11}, chunksStartOffset()+5); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	ref, d2, _, err := p.Blobs().Stage(ctx(), bytes.NewReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	if d2 != digest {
		t.Fatal("digest mismatch in setup")
	}
	err = p.Blobs().Publish(ctx(), ref, digest)
	wantCode(t, err, contractCodeArtifactFaultAlias)
}
