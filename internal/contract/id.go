package contract

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// NewID mints a new UUIDv4 identity in canonical lowercase hyphenated form.
// It is deterministic only in its format: every call produces a fresh
// random identity. crypto/rand cannot fail on supported platforms; a failure
// to read entropy is unrecoverable and panics rather than returning a
// weaker identity.
func NewID() ID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("contract: entropy source unavailable: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return ID(formatUUID(b))
}

func formatUUID(b [16]byte) string {
	dst := make([]byte, 36)
	hex.Encode(dst[0:8], b[0:4])
	dst[8] = '-'
	hex.Encode(dst[9:13], b[4:6])
	dst[13] = '-'
	hex.Encode(dst[14:18], b[6:8])
	dst[18] = '-'
	hex.Encode(dst[19:23], b[8:10])
	dst[23] = '-'
	hex.Encode(dst[24:36], b[10:16])
	return string(dst)
}

// validID reports whether id is a syntactically valid UUID: 8-4-4-4-12
// lowercase hexadecimal digits. Existing stable references may be any UUID
// version; only minting is pinned to v4.
func validID(id ID) bool {
	s := string(id)
	if len(s) != 36 {
		return false
	}
	for i, r := range []byte(s) {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !isHexLower(r) {
				return false
			}
		}
	}
	return true
}

func isHexLower(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f')
}

// validDigest reports whether d is a 64-character lowercase hex string.
func validDigest(d Digest) bool {
	if len(d) != sha256.Size*2 {
		return false
	}
	for _, c := range []byte(d) {
		if !isHexLower(c) {
			return false
		}
	}
	return true
}

// Hash returns the SHA-256 digest of data as a lowercase hex Digest.
// Hash operates on exact bytes; callers canonicalize JSON first so that
// semantically equal inputs hash equally.
func Hash(data []byte) Digest {
	sum := sha256.Sum256(data)
	return Digest(hex.EncodeToString(sum[:]))
}
