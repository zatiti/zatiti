package platform

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

const macMasterAccount = "master"

// macMasterStore is deliberately smaller than SecretStore: creating a master
// item must never use SecretStore.Put, which replaces existing Keychain data.
// Read returns the base64 representation used by the existing Keychain store.
type macMasterStore interface {
	Read(context.Context, string, string) ([]byte, error)
	Create(context.Context, string, string, []byte) (bool, error)
}

// provisionMacMasterKey is the testable portion of Mac provisioning. Only the
// Darwin entrypoint calls it in production, after checking the installed path.
func provisionMacMasterKey(ctx context.Context, stateDir string, store macMasterStore) error {
	if store == nil {
		return errf(contractCodePrerequisiteMissing, "Mac Keychain provisioning is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return errWrap(contractCodePrerequisiteMissing, "Mac Keychain provisioning was interrupted", err)
	}
	if !filepath.IsAbs(stateDir) || filepath.Clean(stateDir) != stateDir {
		return errf(contractCodeInvalidInput, "Mac state directory must be a clean absolute path")
	}
	stateDir = canonicalizeStatePath(stateDir)
	if err := ensureDirChain(stateDir); err != nil {
		return err
	}
	if err := checkFilesystemLocal(stateDir); err != nil {
		return err
	}
	if err := checkMacProvisionState(stateDir); err != nil {
		return err
	}
	if err := checkExistingMacProvisionInstance(filepath.Join(stateDir, fileInstanceID)); err != nil {
		return err
	}
	instance, err := ensureInstanceID(stateDir)
	if err != nil {
		return err
	}
	if err := checkMacProvisionInstance(filepath.Join(stateDir, fileInstanceID)); err != nil {
		return err
	}
	service := "com.zatiti.zatiti.v1." + instance[:16]
	encoded, err := store.Read(ctx, service, macMasterAccount)
	if err == nil {
		defer zero(encoded)
		if err := ctx.Err(); err != nil {
			return errWrap(contractCodePrerequisiteMissing, "Mac Keychain provisioning was interrupted", err)
		}
		return validateMacMasterValue(encoded)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return errf(contractCodePrerequisiteMissing, "Mac master Keychain item cannot be read")
	}

	key := make([]byte, 32)
	defer zero(key)
	if _, err := rand.Read(key); err != nil {
		return errf(contractCodePrerequisiteMissing, "Mac master key randomness is unavailable")
	}
	encoded = make([]byte, base64.StdEncoding.EncodedLen(len(key)))
	base64.StdEncoding.Encode(encoded, key)
	defer zero(encoded)
	created, err := store.Create(ctx, service, macMasterAccount, encoded)
	if err != nil {
		return errf(contractCodePrerequisiteMissing, "Mac master Keychain item cannot be created")
	}
	// Even a successful SecItemAdd is read back. This detects an ambiguous
	// success, wrong backend/service and a concurrent creator without replacing
	// its item. A duplicate must validate the winner's value, not ours.
	actual, err := store.Read(ctx, service, macMasterAccount)
	if err != nil {
		return errf(contractCodePrerequisiteMissing, "Mac master Keychain item cannot be verified")
	}
	defer zero(actual)
	if err := validateMacMasterValue(actual); err != nil {
		return err
	}
	if created && !equalMacMasterEncoded(actual, encoded) {
		return errf(contractCodePrerequisiteMissing, "Mac master Keychain item changed during provisioning")
	}
	if err := ctx.Err(); err != nil {
		return errWrap(contractCodePrerequisiteMissing, "Mac Keychain provisioning was interrupted", err)
	}
	return nil
}

func equalMacMasterEncoded(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var mismatch byte
	for i := range a {
		mismatch |= a[i] ^ b[i]
	}
	return mismatch == 0
}

func validateMacMasterValue(encoded []byte) error {
	if len(encoded) != base64.StdEncoding.EncodedLen(32) {
		return errf(contractCodePrerequisiteMissing, "Mac master Keychain item is malformed")
	}
	decoded := make([]byte, 32)
	defer zero(decoded)
	n, err := base64.StdEncoding.Decode(decoded, encoded)
	if err != nil || n != 32 {
		return errf(contractCodePrerequisiteMissing, "Mac master Keychain item is malformed")
	}
	canonical := make([]byte, len(encoded))
	defer zero(canonical)
	base64.StdEncoding.Encode(canonical, decoded)
	if !equalMacMasterEncoded(canonical, encoded) {
		return errf(contractCodePrerequisiteMissing, "Mac master Keychain item is malformed")
	}
	return nil
}

func checkMacProvisionState(stateDir string) error {
	fi, err := os.Lstat(stateDir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != dirPrivate || !desktopOwned(fi) {
		return errf(contractCodePrerequisiteMissing, "Mac state directory is not private and owner-controlled")
	}
	return nil
}

func checkMacProvisionInstance(path string) error {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != filePrivate || !desktopOwned(fi) {
		return errf(contractCodePrerequisiteMissing, "Mac installation identity is not private and owner-controlled")
	}
	f, err := openPrivate(path, os.O_RDONLY, 0)
	if err != nil {
		return errf(contractCodePrerequisiteMissing, "Mac installation identity cannot be opened")
	}
	defer func() { _ = f.Close() }()
	actual, err := f.Stat()
	if err != nil || !actual.Mode().IsRegular() || actual.Mode().Perm() != filePrivate || !desktopOwned(actual) {
		return errf(contractCodePrerequisiteMissing, "Mac installation identity changed during inspection")
	}
	return nil
}

func checkExistingMacProvisionInstance(path string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errf(contractCodePrerequisiteMissing, "Mac installation identity cannot be inspected")
	}
	return checkMacProvisionInstance(path)
}
