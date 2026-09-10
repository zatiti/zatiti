package platform

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestEnvelopeMetaRoundtrip(t *testing.T) {
	key := testMasterKey(t)
	gcm, err := newCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "env")

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	if err := writeEnvelopeHeader(f); err != nil {
		t.Fatal(err)
	}
	meta := blobMeta{Chunk: blobChunkSize, Digest: string(digestOf([]byte("payload"))), Size: 42}
	if err := writeEnvelopeMeta(f, gcm, meta); err != nil {
		t.Fatal(err)
	}

	got, chunkStart, err := readEnvelopeMeta(f, gcm)
	if err != nil {
		t.Fatalf("readEnvelopeMeta: %v", err)
	}
	if got != meta {
		t.Fatalf("meta = %+v, want %+v", got, meta)
	}
	if chunkStart != chunksStartOffset() {
		t.Fatalf("chunk start = %d, want %d", chunkStart, chunksStartOffset())
	}

	// Fixed-width layout: every metadata slot is the same length.
	alt := blobMeta{Chunk: 512, Digest: string(digestOf([]byte("other"))), Size: 1 << 40}
	plain := fmt.Sprintf(blobMetaFormat, alt.Chunk, alt.Digest, alt.Size)
	if len(plain) != blobMetaPlainLen {
		t.Fatalf("meta plain len = %d, want %d", len(plain), blobMetaPlainLen)
	}
}

func TestChunkAADBindsIndex(t *testing.T) {
	if bytes.Equal(chunkAAD(1), chunkAAD(2)) {
		t.Fatal("chunk AAD does not bind the index")
	}
	var idx uint64 = 1 << 40
	if got := chunkAAD(idx); len(got) != len(blobMagic)+8 {
		t.Fatalf("chunk AAD length = %d", len(got))
	}
	if !bytes.HasPrefix(chunkAAD(idx), []byte(blobMagic)) {
		t.Fatal("chunk AAD lost the magic prefix")
	}
	if binary.BigEndian.Uint64(chunkAAD(idx)[len(blobMagic):]) != idx {
		t.Fatal("chunk AAD index encoding mismatch")
	}
}

func TestEnvelopeLayoutConstants(t *testing.T) {
	// metaCtLen and chunksStartOffset must agree with the documented layout.
	want := int64(len(blobMagic)) + 4 + blobNonceLen + int64(blobMetaPlainLen+blobTagLen)
	if got := chunksStartOffset(); got != want {
		t.Fatalf("chunksStartOffset = %d, want %d", got, want)
	}
	if got := metaCtLen(); got != blobMetaPlainLen+blobTagLen {
		t.Fatalf("metaCtLen = %d, want %d", got, blobMetaPlainLen+blobTagLen)
	}
	if blobChunkSize != 1<<20 {
		t.Fatalf("blobChunkSize = %d, want 1MiB", blobChunkSize)
	}
}

func TestReadEnvelopeMetaRejectsCorruption(t *testing.T) {
	key := testMasterKey(t)
	gcm, _ := newCipher(key)

	build := func(t *testing.T) (*os.File, func()) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "env")
		f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeEnvelopeHeader(f); err != nil {
			t.Fatal(err)
		}
		if err := writeEnvelopeMeta(f, gcm, blobMeta{Chunk: blobChunkSize, Digest: string(digestOf([]byte("x"))), Size: 1}); err != nil {
			t.Fatal(err)
		}
		return f, func() { _ = f.Close() }
	}

	t.Run("bad magic", func(t *testing.T) {
		f, done := build(t)
		defer done()
		if _, err := f.WriteAt([]byte("XXXXXX\n"), 0); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readEnvelopeMeta(f, gcm); err == nil {
			t.Fatal("bad magic accepted")
		}
	})

	t.Run("bad meta length", func(t *testing.T) {
		f, done := build(t)
		defer done()
		if _, err := f.WriteAt([]byte{0, 0, 0, 99}, int64(len(blobMagic))); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readEnvelopeMeta(f, gcm); err == nil {
			t.Fatal("bad metadata length accepted")
		}
	})

	t.Run("tampered metadata", func(t *testing.T) {
		f, done := build(t)
		defer done()
		pos := int64(len(blobMagic)) + 4
		var b [1]byte
		if _, err := f.ReadAt(b[:], pos); err != nil {
			t.Fatal(err)
		}
		b[0] ^= 0xFF
		if _, err := f.WriteAt(b[:], pos); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readEnvelopeMeta(f, gcm); err == nil {
			t.Fatal("tampered metadata accepted")
		}
	})

	t.Run("truncated file", func(t *testing.T) {
		f, done := build(t)
		defer done()
		if err := f.Truncate(10); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readEnvelopeMeta(f, gcm); err == nil {
			t.Fatal("truncated envelope accepted")
		}
	})
}

func TestEnvelopeMetaFormatIsStrictJSON(t *testing.T) {
	// The authenticated plaintext must round-trip through the strict decoder
	// used by readEnvelopeMeta, and the zero-padded size must stay inside a
	// JSON string (leading zeros are not valid JSON numbers).
	meta := blobMeta{Chunk: 1048576, Digest: string(digestOf([]byte("d"))), Size: 12345}
	plain := fmt.Sprintf(blobMetaFormat, meta.Chunk, meta.Digest, meta.Size)
	got, err := decodeBlobMetaPlain([]byte(plain))
	if err != nil {
		t.Fatalf("strict decode of envelope metadata: %v", err)
	}
	if got != meta {
		t.Fatalf("strict decode = %+v, want %+v", got, meta)
	}
}

func TestVerifyEnvelopeAcceptsAndRejects(t *testing.T) {
	key := testMasterKey(t)
	gcm, _ := newCipher(key)

	path := filepath.Join(t.TempDir(), "env")
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	if err := writeEnvelopeHeader(f); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("payload-bytes"), 500) // within one chunk
	sealed, err := sealWith(gcm, payload, chunkAAD(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(putU32be(uint32(len(sealed))), chunksStartOffset()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(sealed, chunksStartOffset()+4); err != nil {
		t.Fatal(err)
	}
	meta := blobMeta{Chunk: blobChunkSize, Digest: string(digestOf(payload)), Size: int64(len(payload))}
	if err := writeEnvelopeMeta(f, gcm, meta); err != nil {
		t.Fatal(err)
	}

	if _, err := verifyEnvelope(f, gcm); err != nil {
		t.Fatalf("clean envelope rejected: %v", err)
	}

	// A re-sealed envelope claiming a different size must fail: the digest
	// no longer matches the authenticated count.
	wrong := meta
	wrong.Size = int64(len(payload)) + 1
	if err := writeEnvelopeMeta(f, gcm, wrong); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyEnvelope(f, gcm); err == nil {
		t.Fatal("size drift accepted by verifyEnvelope")
	}
}
