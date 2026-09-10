package platform

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
)

// AES-256-GCM helpers shared by the blob envelope and the headless secret
// store. Every ciphertext carries a fresh random nonce; associated data
// binds ciphertexts to their position and purpose.

func newCipher(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errf(contractCodeInternalErrorAlias, "cipher construction failed")
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errf(contractCodeInternalErrorAlias, "cipher construction failed")
	}
	return gcm, nil
}

// cipherAEAD is a readable alias for the GCM interface used across the
// blob envelope helpers.
type cipherAEAD = cipher.AEAD

func randomNonce(n int) ([]byte, error) {
	nonce := make([]byte, n)
	if _, err := rand.Read(nonce); err != nil {
		return nil, errf(contractCodeInternalErrorAlias, "random generation failed")
	}
	return nonce, nil
}

// sealWith seals plain under a fresh random nonce, returning nonce||ct.
func sealWith(gcm cipher.AEAD, plain, aad []byte) ([]byte, error) {
	nonce, err := randomNonce(gcm.NonceSize())
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(nonce)+len(plain)+gcm.Overhead())
	out = append(out, nonce...)
	return gcm.Seal(out, nonce, plain, aad), nil
}

// openWith verifies and opens nonce||ct with the given associated data.
func openWith(gcm cipher.AEAD, data, aad []byte) ([]byte, error) {
	if len(data) < gcm.NonceSize()+gcm.Overhead() {
		return nil, errf(contractCodeInternalErrorAlias, "ciphertext is truncated")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], aad)
	if err != nil {
		return nil, errf(contractCodeInternalErrorAlias, "ciphertext failed authentication")
	}
	return plain, nil
}

// sealSecret wraps a headless credential value: magic || nonce || GCM(value).
// The stored id is authenticated as associated data so a value file moved
// under another reference fails verification instead of decrypting.
func sealSecret(key, id, value []byte) ([]byte, error) {
	gcm, err := newCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(secretMagic)+12+len(value)+16)
	out = append(out, secretMagic...)
	sealed, err := sealWith(gcm, value, secretAAD(id))
	if err != nil {
		return nil, err
	}
	return append(out, sealed...), nil
}

// openSecret unwraps a sealed credential file, refusing wrong magic,
// tampered bytes, truncated files and value files swapped between ids.
func openSecret(key, id, data []byte) ([]byte, error) {
	if len(data) < len(secretMagic) || string(data[:len(secretMagic)]) != secretMagic {
		return nil, errf(contractCodeInternalErrorAlias, "sealed credential format is unrecognized")
	}
	gcm, err := newCipher(key)
	if err != nil {
		return nil, err
	}
	return openWith(gcm, data[len(secretMagic):], secretAAD(id))
}

// secretAAD binds a credential envelope to its magic and storage id.
func secretAAD(id []byte) []byte {
	aad := make([]byte, 0, len(secretMagic)+len(id))
	aad = append(aad, secretMagic...)
	return append(aad, id...)
}

const secretMagic = "ZTSEC1"
