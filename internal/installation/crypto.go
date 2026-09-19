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

// backupKeyName is the name one installation's backup encryption key is
// custodied under. It is the NAME handed to SecretStore.Put; it is never a
// lookup handle: the store returns an opaque reference, and only that
// reference resolves the key again. The reference is recorded in the
// installation_backup_keys table for reuse across backups and carried in
// every bundle's clear header so a restore can resolve the key from the
// artifact alone (see bundle.go).
func backupKeyName(installationID contract.ID) string {
	return "installation/" + string(installationID) + "/backup-key"
}

// resolveBackupKey returns the installation's backup key by the recorded
// store reference, or mints and custodies a fresh key when no reference is
// recorded or the recorded one no longer resolves. It reports the reference
// the key resolves under and whether it minted. It must run outside any
// transaction (Perform): secret store access is forbidden inside a Unit.
func resolveBackupKey(ctx context.Context, secrets contract.SecretStore, installationID contract.ID, ref string) (key []byte, storeRef string, minted bool, err error) {
	if ref != "" {
		if key, err := secrets.Get(ctx, ref); err == nil && len(key) == 32 {
			return key, ref, false, nil
		}
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, "", false, fmt.Errorf("installation: mint backup key: %w", err)
	}
	storeRef, err = secrets.Put(ctx, backupKeyName(installationID), key)
	if err != nil {
		return nil, "", false, fmt.Errorf("installation: custody backup key: %w", err)
	}
	if storeRef == "" {
		return nil, "", false, fmt.Errorf("installation: secret store returned no reference for the backup key")
	}
	// The reference must resolve the key; a store that cannot hand it back
	// would make every bundle sealed under it unrestorable.
	if back, err := secrets.Get(ctx, storeRef); err != nil || len(back) != 32 {
		return nil, "", false, fmt.Errorf("installation: backup key reference %q does not resolve after custody", storeRef)
	}
	return key, storeRef, true, nil
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
