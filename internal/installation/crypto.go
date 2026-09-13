package installation

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// Backup bundle encryption. A backup's confidentiality and authenticity must
// survive export off the local BlobStore (a USB drive, cloud storage), so it
// cannot rely on BlobStore's own transparent at-rest encryption, which only
// protects bytes while they remain in local custody. Every installation gets
// exactly one long-lived backup key, custodied by the secret store under a
// well-known per-installation reference so a later restore can resolve the
// same key without the bundle ever carrying it -- satisfying "never include
// decrypting master keys in same bundle" while still letting restore work
// from nothing but the uploaded artifact and this installation's own secret
// store.

// backupKeyRef is the well-known secret store reference for one
// installation's backup encryption key.
func backupKeyRef(installationID contract.ID) string {
	return "installation/" + string(installationID) + "/backup-key"
}

// ensureBackupKey resolves the installation's backup key, minting and
// custodying a fresh one on first use. It must run outside any transaction
// (Perform): secret store access is forbidden inside a Unit.
func ensureBackupKey(ctx context.Context, secrets contract.SecretStore, installationID contract.ID) ([]byte, error) {
	ref := backupKeyRef(installationID)
	if key, err := secrets.Get(ctx, ref); err == nil && len(key) == 32 {
		return key, nil
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("installation: mint backup key: %w", err)
	}
	if _, err := secrets.Put(ctx, ref, key); err != nil {
		return nil, fmt.Errorf("installation: custody backup key: %w", err)
	}
	return key, nil
}

// sealBundle encrypts plaintext with AES-256-GCM under key, returning
// nonce||ciphertext||tag. Each call mints a fresh random nonce.
func sealBundle(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("installation: build backup cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("installation: build backup AEAD: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("installation: mint backup nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// openBundle decrypts and authenticates a sealBundle output under key. A
// tampered ciphertext, a truncated bundle or the wrong key all fail the same
// way: the caller cannot distinguish which, by design.
func openBundle(key, sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("installation: build backup cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("installation: build backup AEAD: %w", err)
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, fmt.Errorf("installation: backup bundle is shorter than one nonce")
	}
	nonce, ciphertext := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("installation: backup bundle failed authentication: %w", err)
	}
	return plaintext, nil
}
