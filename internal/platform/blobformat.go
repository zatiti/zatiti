package platform

import (
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Encrypted blob envelope, version 1.
//
//	magic "ZTBLB1\n"
//	u32be metaCtLen || nonce(12) || GCM(metaJSON, aad=magic+"meta")
//	per chunk: u32be chunkCtLen || nonce(12) || GCM(chunk, aad=magic||u64be(index))
//
// metaJSON is fixed-width: {"chunk":"<8 digits>","digest":"<64 hex>","size":"<20 digits>"}
// so its offset and length are constant per version. The chunk and size
// fields are zero-padded JSON strings because leading zeros are not valid
// JSON numbers; widths are enforced on read. Chunks are indexed from 1; the
// authenticated metadata binds the plaintext SHA-256 digest and exact size,
// so truncation, reordering, cross-object swapping and bit flips all fail
// verification before any bytes are returned.
const (
	blobMagic    = "ZTBLB1\n"
	blobMetaAAD  = blobMagic + "meta"
	blobNonceLen = 12
	blobTagLen   = 16
)

// blobChunkSize is the plaintext chunk size of the envelope.
const blobChunkSize = int64(1 << 20)

const blobMetaFormat = `{"chunk":"%08d","digest":"%s","size":"%020d"}`

var blobMetaPlainLen = len(fmt.Sprintf(blobMetaFormat, blobChunkSize, strings.Repeat("0", 64), int64(0)))

type blobMeta struct {
	Chunk  int64  `json:"chunk"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// metaCtLen is the constant ciphertext length of the metadata envelope.
func metaCtLen() int { return blobMetaPlainLen + blobTagLen }

// chunksStartOffset is the fixed file offset of the first chunk header.
func chunksStartOffset() int64 {
	return int64(len(blobMagic) + 4 + blobNonceLen + metaCtLen())
}

func chunkAAD(idx uint64) []byte {
	b := make([]byte, 0, len(blobMagic)+8)
	b = append(b, blobMagic...)
	return binary.BigEndian.AppendUint64(b, idx)
}

func putU32be(v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return b[:]
}

// writeEnvelopeHeader writes magic and the zero-filled metadata slot.
func writeEnvelopeHeader(w *os.File) error {
	if _, err := w.WriteAt([]byte(blobMagic), 0); err != nil {
		return err
	}
	if _, err := w.WriteAt(putU32be(uint32(metaCtLen())), int64(len(blobMagic))); err != nil {
		return err
	}
	slot := make([]byte, blobNonceLen+metaCtLen())
	_, err := w.WriteAt(slot, int64(len(blobMagic))+4)
	return err
}

// writeEnvelopeMeta encrypts and writes the authenticated metadata in place.
func writeEnvelopeMeta(w *os.File, gcm cipher.AEAD, meta blobMeta) error {
	plain := fmt.Sprintf(blobMetaFormat, meta.Chunk, meta.Digest, meta.Size)
	if len(plain) != blobMetaPlainLen {
		return errf(contractCodeInternalErrorAlias, "blob metadata encoding is inconsistent")
	}
	sealed, err := sealWith(gcm, []byte(plain), []byte(blobMetaAAD))
	if err != nil {
		return err
	}
	if len(sealed) != blobNonceLen+metaCtLen() {
		return errf(contractCodeInternalErrorAlias, "blob metadata encoding is inconsistent")
	}
	_, err = w.WriteAt(sealed, int64(len(blobMagic))+4)
	return err
}

// readEnvelopeMeta validates magic and returns the authenticated metadata
// plus the offset of the first chunk.
func readEnvelopeMeta(f *os.File, gcm cipher.AEAD) (blobMeta, int64, error) {
	magic := make([]byte, len(blobMagic))
	if _, err := f.ReadAt(magic, 0); err != nil || string(magic) != blobMagic {
		return blobMeta{}, 0, artifactFault("artifact format is unrecognized or corrupt")
	}
	lenBuf := make([]byte, 4)
	if _, err := f.ReadAt(lenBuf, int64(len(blobMagic))); err != nil {
		return blobMeta{}, 0, artifactFault("artifact format is unrecognized or corrupt")
	}
	if n := binary.BigEndian.Uint32(lenBuf); n != uint32(metaCtLen()) {
		return blobMeta{}, 0, artifactFault("artifact format is unrecognized or corrupt")
	}
	metaStart := int64(len(blobMagic)) + 4
	sealed := make([]byte, blobNonceLen+metaCtLen())
	if _, err := f.ReadAt(sealed, metaStart); err != nil {
		return blobMeta{}, 0, artifactFault("artifact format is unrecognized or corrupt")
	}
	plain, err := openWith(gcm, sealed, []byte(blobMetaAAD))
	if err != nil {
		return blobMeta{}, 0, artifactFault("artifact metadata failed integrity verification")
	}
	meta, err := decodeBlobMetaPlain(plain)
	if err != nil {
		return blobMeta{}, 0, err
	}
	return meta, metaStart + int64(blobNonceLen+metaCtLen()), nil
}

// blobMetaWire is the on-disk metadata shape: chunk and size are zero-padded
// strings of fixed width, which keeps the plaintext a fixed length and stays
// valid JSON.
type blobMetaWire struct {
	Chunk  string `json:"chunk"`
	Digest string `json:"digest"`
	Size   string `json:"size"`
}

// decodeBlobMetaPlain parses and validates the authenticated metadata
// plaintext.
func decodeBlobMetaPlain(plain []byte) (blobMeta, error) {
	var wire blobMetaWire
	if err := strictUnmarshal(plain, &wire); err != nil {
		return blobMeta{}, artifactFault("artifact metadata failed integrity verification")
	}
	if len(wire.Chunk) != 8 || len(wire.Size) != 20 {
		return blobMeta{}, artifactFault("artifact metadata failed integrity verification")
	}
	chunk, cerr := strconv.ParseInt(wire.Chunk, 10, 64)
	size, serr := strconv.ParseInt(wire.Size, 10, 64)
	if cerr != nil || serr != nil || size < 0 || chunk < 512 || chunk > 16<<20 {
		return blobMeta{}, artifactFault("artifact metadata failed integrity verification")
	}
	meta := blobMeta{Chunk: chunk, Digest: wire.Digest, Size: size}
	if !validDigest(meta.Digest) {
		return blobMeta{}, artifactFault("artifact metadata failed integrity verification")
	}
	return meta, nil
}

// verifyEnvelope decrypts and authenticates the entire object, comparing the
// running plaintext hash and byte count against the authenticated metadata.
// It returns the verified metadata. This is the full-integrity pass every
// Open and Publish runs before returning or publishing bytes.
func verifyEnvelope(f *os.File, gcm cipher.AEAD) (blobMeta, error) {
	meta, pos, err := readEnvelopeMeta(f, gcm)
	if err != nil {
		return blobMeta{}, err
	}
	h := sha256.New()
	var total int64
	idx := uint64(0)
	for {
		idx++
		var lenBuf [4]byte
		n, err := f.ReadAt(lenBuf[:], pos)
		if err != nil {
			if n == 0 && errors.Is(err, io.EOF) {
				break // clean end at a chunk boundary
			}
			return blobMeta{}, artifactFault("artifact bytes failed integrity verification")
		}
		ctLen := binary.BigEndian.Uint32(lenBuf[:])
		if ctLen < blobNonceLen+blobTagLen || ctLen > uint32(blobNonceLen+meta.Chunk+blobTagLen) {
			return blobMeta{}, artifactFault("artifact bytes failed integrity verification")
		}
		sealed := make([]byte, ctLen)
		if _, err := f.ReadAt(sealed, pos+4); err != nil {
			return blobMeta{}, artifactFault("artifact bytes failed integrity verification")
		}
		plain, err := openWith(gcm, sealed, chunkAAD(idx))
		if err != nil {
			return blobMeta{}, artifactFault("artifact bytes failed integrity verification")
		}
		h.Write(plain)
		total += int64(len(plain))
		zero(plain)
		pos = pos + 4 + int64(ctLen)
	}
	if total != meta.Size || hex.EncodeToString(h.Sum(nil)) != meta.Digest {
		return blobMeta{}, artifactFault("artifact bytes failed integrity verification")
	}
	return meta, nil
}

// readChunkAt returns the decrypted plaintext of chunk idx (1-based).
func readChunkAt(f *os.File, gcm cipher.AEAD, pos int64, idx uint64, maxPlain int) ([]byte, int64, error) {
	var lenBuf [4]byte
	if _, err := f.ReadAt(lenBuf[:], pos); err != nil {
		return nil, 0, artifactFault("artifact bytes failed integrity verification")
	}
	ctLen := binary.BigEndian.Uint32(lenBuf[:])
	if ctLen < blobNonceLen+blobTagLen || ctLen > uint32(blobNonceLen+maxPlain+blobTagLen) {
		return nil, 0, artifactFault("artifact bytes failed integrity verification")
	}
	sealed := make([]byte, ctLen)
	if _, err := f.ReadAt(sealed, pos+4); err != nil {
		return nil, 0, artifactFault("artifact bytes failed integrity verification")
	}
	plain, err := openWith(gcm, sealed, chunkAAD(uint64(idx)))
	if err != nil {
		return nil, 0, artifactFault("artifact bytes failed integrity verification")
	}
	return plain, pos + 4 + int64(ctLen), nil
}
