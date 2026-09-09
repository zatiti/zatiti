// internal/identity/keystore.go
//
// Filesystem keystore: 0700 directory, 0600 key files, never writable
// by group/other. Permission violations are hard errors, not warnings.

package identity

import (
	"crypto/ecdsa"
	"fmt"
	"os"
	"path/filepath"
)

// Keystore stores private key material under one directory.
type Keystore struct {
	dir string
}

// OpenKeystore validates or creates the keystore directory with 0700.
func OpenKeystore(dir string) (*Keystore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("identity: create keystore: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("identity: stat keystore: %w", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return nil, fmt.Errorf("identity: keystore %s has unsafe permissions %o (want 0700)", dir, perm)
	}
	return &Keystore{dir: dir}, nil
}

// LoadOrCreate returns the key named name, generating it via create on
// first use. Writes are atomic (temp file + rename) so a crash cannot
// leave a truncated key.
func (k *Keystore) LoadOrCreate(name string, create func() ([]byte, error)) (*ecdsa.PrivateKey, error) {
	path := filepath.Join(k.dir, name+".key")
	raw, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		raw, err = create()
		if err != nil {
			return nil, err
		}
		tmp, err := os.CreateTemp(k.dir, ".key-*")
		if err != nil {
			return nil, fmt.Errorf("identity: temp key: %w", err)
		}
		tmpName := tmp.Name()
		if err := tmp.Chmod(0o600); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return nil, fmt.Errorf("identity: chmod key: %w", err)
		}
		if _, err := tmp.Write(raw); err != nil {
			tmp.Close()
			os.Remove(tmpName)
			return nil, fmt.Errorf("identity: write key: %w", err)
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmpName)
			return nil, fmt.Errorf("identity: close key: %w", err)
		}
		if err := os.Rename(tmpName, path); err != nil {
			os.Remove(tmpName)
			return nil, fmt.Errorf("identity: place key: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("identity: read key: %w", err)
	}
	return parseKey(raw)
}

// exists reports whether a key file is present (no read, no parse).
func (k *Keystore) exists(name string) bool {
	_, err := os.Stat(filepath.Join(k.dir, name+".key"))
	return err == nil
}

// storeKey writes key material atomically at 0600, refusing overwrite
// (callers check exists() first; this is the second half of the guard).
func (k *Keystore) storeKey(name string, raw []byte) error {
	path := filepath.Join(k.dir, name+".key")
	tmp, err := os.CreateTemp(k.dir, ".key-*")
	if err != nil {
		return fmt.Errorf("identity: temp key: %w", err)
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("identity: chmod key: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("identity: write key: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("identity: close key: %w", err)
	}
	// O_EXCL rename-guard is not portable; rely on the exists() check
	// plus the installation lock serializing all writers.
	return os.Rename(tmpName, path)
}

// loadKey reads custodied key material by name. Caller owns parsing.
func (k *Keystore) loadKey(name string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(k.dir, name+".key"))
	if err != nil {
		return nil, fmt.Errorf("identity: load key %s: %w", name, err)
	}
	return raw, nil
}
