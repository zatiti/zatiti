package packaging

import (
	"crypto/rand"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	masterKeyBytes     = 32
	masterKeyRefPrefix = "file:"
)

// ProvisionMasterKey is the installation-time secure helper for a headless
// host. It draws a 32-byte master key from the OS random source and writes
// it to keyPath with owner-only permissions, in the raw form the platform
// package's "file:" master key reference reads. The key never appears in an
// argument, an environment variable, a return value or an error; the caller
// receives only the reference to configure.
//
// An existing file is never overwritten: every blob and headless secret is
// encrypted under that key, so replacing it destroys the installation. The
// key must sit outside the state and distribution directories so that a
// backup of state does not carry its own key and an uninstall does not
// delete it.
func ProvisionMasterKey(l Layout, keyPath string) (string, error) {
	if err := l.validate(); err != nil {
		return "", err
	}
	if !cleanAbs(keyPath) {
		return "", errf(CodeInvalidInput, "the master key path must be a clean absolute path")
	}
	for _, dir := range []string{l.StateDir, l.DistRoot, l.UnitDir} {
		if pathWithin(keyPath, dir) {
			return "", errf(CodeInvalidInput, "the master key must sit outside the state, distribution and launcher directories")
		}
	}
	dir := filepath.Dir(keyPath)
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.Mkdir(dir, 0o700); err != nil {
			return "", errWrap(CodePrerequisiteMissing, "the master key directory could not be created; its parent must exist", err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return "", errWrap(CodeInternalError, "the master key directory could not be protected", err)
		}
	case err != nil:
		return "", errWrap(CodeInternalError, "the master key directory could not be inspected", err)
	case !info.IsDir():
		return "", errf(CodeConflict, "the master key directory is a symlink or file")
	case info.Mode().Perm()&0o077 != 0:
		return "", errf(CodeConflict, "the master key directory must be accessible to its owner only")
	}

	key := make([]byte, masterKeyBytes)
	defer zero(key)
	if _, err := rand.Read(key); err != nil {
		return "", errWrap(CodeInternalError, "the OS random source failed", err)
	}
	f, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return "", errf(CodeConflict, "a master key already exists at that path; it is never overwritten")
	}
	if err != nil {
		return "", errWrap(CodeInternalError, "the master key file could not be created", err)
	}
	_, err = f.Write(key)
	if err == nil {
		err = f.Chmod(0o600)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = syncDir(dir)
	}
	if err != nil {
		_ = os.Remove(keyPath)
		return "", errWrap(CodeInternalError, "the master key file could not be written", err)
	}
	return masterKeyRefPrefix + keyPath, nil
}

// CheckMasterKey reports whether an existing key file is safe to reference:
// a regular, non-empty, owner-only file in an owner-only directory. It does
// not read the key.
func CheckMasterKey(keyPath string) error {
	if !cleanAbs(keyPath) {
		return errf(CodeInvalidInput, "the master key path must be a clean absolute path")
	}
	info, err := os.Lstat(keyPath)
	if err != nil {
		return errWrap(CodePrerequisiteMissing, "the master key file does not exist", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errf(CodeVerificationFailed, "the master key must be a regular, non-empty file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errf(CodeVerificationFailed, "the master key file must be accessible to its owner only")
	}
	dirInfo, err := os.Lstat(filepath.Dir(keyPath))
	if err != nil {
		return errWrap(CodeInternalError, "the master key directory could not be inspected", err)
	}
	if !dirInfo.IsDir() || dirInfo.Mode().Perm()&0o077 != 0 {
		return errf(CodeVerificationFailed, "the master key directory must be a real directory accessible to its owner only")
	}
	return nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
