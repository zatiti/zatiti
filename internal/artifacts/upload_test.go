package artifacts

import (
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestUploadFullLifecycleAcrossMultipleChunks(t *testing.T) {
	e := newEnv(t)
	data := make([]byte, 3*512+37)
	for i := range data {
		data[i] = byte(i)
	}
	a := e.uploadFull(data, 512)
	if a.Size != int64(len(data)) {
		t.Fatalf("artifact size = %d, want %d", a.Size, len(data))
	}
	if a.Digest != digestOf(data) {
		t.Fatalf("artifact digest mismatch")
	}
	if a.State != "available" {
		t.Fatalf("artifact state = %q, want available", a.State)
	}
}

func TestChunkReplayIsIdempotent(t *testing.T) {
	e := newEnv(t)
	data := []byte("hello world, this is a chunk of bytes")
	u := e.beginUpload(len(data), digestOf(data))

	first := e.mustSendChunk(u.ID, 0, data)
	second := e.mustSendChunk(u.ID, 0, data) // exact replay: same offset, same bytes
	if first.Version != second.Version {
		t.Fatalf("replay changed the upload version: %d -> %d", first.Version, second.Version)
	}
	if second.ReceivedSize != int64(len(data)) {
		t.Fatalf("replay changed received size to %d", second.ReceivedSize)
	}
}

func TestChunkReplayWithDifferentBytesConflicts(t *testing.T) {
	e := newEnv(t)
	data := []byte("original bytes for this chunk")
	u := e.beginUpload(len(data)+10, digestOf(append(data, []byte("0123456789")...)))
	e.mustSendChunk(u.ID, 0, data)

	_, payload, err := e.sendChunk(u.ID, 0, []byte("different bytes, same length!"))
	f := e.assertFault(opUploadChunk, payload, err, contract.CodeConflict)
	_ = f
}

func TestChunkRequiresExactContiguousOffset(t *testing.T) {
	e := newEnv(t)
	data := []byte("0123456789")
	u := e.beginUpload(20, digestOf(append(data, data...)))
	e.mustSendChunk(u.ID, 0, data)

	// Skipping ahead of the next contiguous offset is refused.
	_, payload, err := e.sendChunk(u.ID, 15, data)
	_ = e.assertFault(opUploadChunk, payload, err, contract.CodeInvalidInput)
}

func TestChunkForgedDigestIsRejected(t *testing.T) {
	e := newEnv(t)
	data := []byte("the real bytes of this chunk")
	u := e.beginUpload(len(data), digestOf(data))

	_ = e.expectLocalFault(opUploadChunk, chunkInput{
		Scope: e.scope, UploadID: u.ID, Offset: 0,
		BytesB64: b64(data), ChunkDigest: digestOf([]byte("not the real bytes")),
	}, true, contract.CodeInvalidInput)
}

func TestChunkOverOneMiBIsRejected(t *testing.T) {
	e := newEnv(t)
	big := make([]byte, maxChunkBytes+1)
	u := e.beginUpload(len(big), digestOf(big))
	_ = e.expectLocalFault(opUploadChunk, chunkInput{
		Scope: e.scope, UploadID: u.ID, Offset: 0,
		BytesB64: b64(big), ChunkDigest: digestOf(big),
	}, true, contract.CodeInvalidInput)
}

func TestUploadFinishRejectsIncompleteUpload(t *testing.T) {
	e := newEnv(t)
	data := []byte("only part of the declared upload")
	u := e.beginUpload(len(data)+100, digestOf(data)) // declares more bytes than will arrive
	up := e.mustSendChunk(u.ID, 0, data)

	payload, err := e.finishUpload(u.ID, int64(up.Version))
	_ = e.assertFault(opUploadFinish, payload, err, contract.CodePrerequisiteMissing)
}

func TestUploadFinishForgedExpectedDigestIsRejected(t *testing.T) {
	e := newEnv(t)
	data := []byte("the actual uploaded content, byte for byte")
	// The client declares a digest that does not match what it actually
	// uploads: a forged expected_digest at upload.begin.
	u := e.beginUpload(len(data), digestOf([]byte("something else entirely, same length!!!")))
	up := e.mustSendChunk(u.ID, 0, data)

	payload, err := e.finishUpload(u.ID, int64(up.Version))
	_ = e.assertFault(opUploadFinish, payload, err, contract.CodeConflict)

	// The upload stays open and reclaimable; nothing was published as a
	// successful artifact.
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		row, lerr := loadUpload(e.ctx, unit, u.ID)
		if lerr != nil {
			return lerr
		}
		if row.State != "open" {
			t.Fatalf("upload state = %q after a rejected finish, want open", row.State)
		}
		return nil
	}); err != nil {
		t.Fatalf("reload upload: %v", err)
	}
}

func TestUploadCancelTransitionsAndReplaysIdempotently(t *testing.T) {
	e := newEnv(t)
	data := []byte("abandoned upload content")
	u := e.beginUpload(len(data), digestOf(data))
	e.mustSendChunk(u.ID, 0, data[:5])

	payload := e.mustLocalOK(opUploadCancel, uploadRefInput{Scope: e.scope, UploadID: u.ID, ExpectedVersion: 2}, true)
	var out uploadCancelOutput
	e.decode(payload.Data, &out)
	if out.Resource == nil || out.Resource.State != "cancelled" {
		t.Fatalf("cancel result = %+v, want state cancelled", out.Resource)
	}

	// A second cancel of the now-terminal upload replays idempotently
	// rather than failing.
	replay := e.mustLocalOK(opUploadCancel, uploadRefInput{Scope: e.scope, UploadID: u.ID, ExpectedVersion: 2}, true)
	var replayOut uploadCancelOutput
	e.decode(replay.Data, &replayOut)
	if replayOut.Resource == nil || replayOut.Resource.State != "cancelled" {
		t.Fatalf("replayed cancel = %+v, want state cancelled", replayOut.Resource)
	}
}

func TestUploadCancelPastExpiryRecordsExpiredNotCancelled(t *testing.T) {
	e := newEnv(t)
	data := []byte("late cancel content")
	u := e.beginUpload(len(data), digestOf(data))
	e.clock.advance(25 * time.Hour) // past the 24h default upload expiry

	payload := e.mustLocalOK(opUploadCancel, uploadRefInput{Scope: e.scope, UploadID: u.ID, ExpectedVersion: 1}, true)
	var out uploadCancelOutput
	e.decode(payload.Data, &out)
	if out.Resource == nil || out.Resource.State != "expired" {
		t.Fatalf("late cancel result = %+v, want state expired", out.Resource)
	}
}

func TestChunkAfterExpiryIsRefused(t *testing.T) {
	e := newEnv(t)
	data := []byte("too late now")
	u := e.beginUpload(len(data), digestOf(data))
	e.clock.advance(25 * time.Hour)

	payload, err := e.driveLocalIO(opUploadChunk, chunkInput{
		Scope: e.scope, UploadID: u.ID, Offset: 0, BytesB64: b64(data), ChunkDigest: digestOf(data),
	}, true)
	_ = e.assertFault(opUploadChunk, payload, err, contract.CodePrerequisiteMissing)
}

func TestInterruptedUploadResumesFromReceivedSize(t *testing.T) {
	e := newEnv(t)
	data := make([]byte, 100)
	for i := range data {
		data[i] = byte(i)
	}
	u := e.beginUpload(len(data), digestOf(data))

	// Simulate a client that sent the first chunk, then disconnected.
	up := e.mustSendChunk(u.ID, 0, data[:40])
	if up.ReceivedSize != 40 {
		t.Fatalf("received size = %d, want 40", up.ReceivedSize)
	}

	// Resuming reads the stable upload identity and continues from the
	// exact contiguous offset; retrying the already-accepted chunk replays.
	replay := e.mustSendChunk(u.ID, 0, data[:40])
	if replay.ReceivedSize != 40 {
		t.Fatalf("retry of accepted chunk changed received size to %d", replay.ReceivedSize)
	}
	final := e.mustSendChunk(u.ID, 40, data[40:])
	if final.ReceivedSize != int64(len(data)) {
		t.Fatalf("received size = %d, want %d", final.ReceivedSize, len(data))
	}
	a := e.mustFinishUpload(u.ID, int64(final.Version))
	if a.Digest != digestOf(data) {
		t.Fatalf("finished artifact digest mismatch")
	}
}

// TestFinishLeavesNoStagedBlobsBehind checks that a successful finish
// publishes every blob it stages: no staged-but-unpublished bytes are left
// for the store to reclaim on the happy path.
func TestFinishLeavesNoStagedBlobsBehind(t *testing.T) {
	e := newEnv(t)
	data := make([]byte, 250)
	for i := range data {
		data[i] = byte(i * 3)
	}
	e.uploadFull(data, 64)
	if n := e.blobs.stagedCount(); n != 0 {
		t.Fatalf("staged blobs remaining after finish = %d, want 0", n)
	}
}

// TestFinishAbandonsStagingOnDigestMismatch checks that a rejected
// reassembly (forged expected digest) does not leave its failed staged
// attempt behind either.
func TestFinishAbandonsStagingOnDigestMismatch(t *testing.T) {
	e := newEnv(t)
	data := []byte("the actual uploaded content for this test case")
	u := e.beginUpload(len(data), digestOf([]byte("a forged expected digest, same length as above!")))
	up := e.mustSendChunk(u.ID, 0, data)

	if _, err := e.finishUpload(u.ID, int64(up.Version)); err != nil {
		t.Fatalf("finish upload transport error: %v", err)
	}
	if n := e.blobs.stagedCount(); n != 0 {
		t.Fatalf("staged blobs remaining after a rejected finish = %d, want 0", n)
	}
}
