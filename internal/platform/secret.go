package platform

import (
	"context"
	"encoding/base64"
	"errors"
	"io/fs"
	"os/exec"
	"sync"
)

// Credential backends. "keychain" uses the OS secure store through a local
// trusted helper process; "headless" is the explicitly provisioned encrypted
// file store for servers and CI. Empty means the platform default.
const (
	backendKeychain = "keychain"
	backendHeadless = "headless"
)

// secretStore is the Platform's credential custody object. It delegates to
// one backend implementation; secrets never leave this process except into
// the secure store itself.
type secretStore struct {
	impl secretBackend
}

// secretBackend is the internal backend surface.
type secretBackend interface {
	Put(ctx context.Context, key string, secret []byte) (string, error)
	Lookup(ctx context.Context, key string) (string, error)
	Get(ctx context.Context, ref string) ([]byte, error)
	Delete(ctx context.Context, ref string) error
	refForKey(key string) string
	zeroKeys()
}

func (s *secretStore) Put(ctx context.Context, key string, secret []byte) (string, error) {
	return s.impl.Put(ctx, key, secret)
}

// Lookup returns the current opaque reference for a trusted, stable local
// name only after proving the referenced credential is still readable.
func (s *secretStore) Lookup(ctx context.Context, key string) (string, error) {
	return s.impl.Lookup(ctx, key)
}

func (s *secretStore) Get(ctx context.Context, ref string) ([]byte, error) {
	return s.impl.Get(ctx, ref)
}

func (s *secretStore) Delete(ctx context.Context, ref string) error {
	return s.impl.Delete(ctx, ref)
}

func (s *secretStore) refForKey(key string) string { return s.impl.refForKey(key) }

func (s *secretStore) zeroKeys() { s.impl.zeroKeys() }

// newSecretStore constructs the configured backend and resolves the master
// key. The headless backend derives its file-encryption subkey from the
// key-file material directly; the keychain backend resolves file or secret
// references through itself.
func newSecretStore(backend, stateDir, instance string, src masterKeySource) (*secretStore, []byte, error) {
	switch backend {
	case backendHeadless:
		// Validated at Open: a headless store cannot resolve secret: refs.
		key, err := resolveMasterKey(src, nil)
		if err != nil {
			return nil, nil, err
		}
		impl, err := newHeadlessSecrets(stateDir, key)
		if err != nil {
			zero(key)
			return nil, nil, err
		}
		// The master copy is returned for further derivation (and wiped by
		// the caller); the store keeps only its own purpose-bound subkey.
		return &secretStore{impl: impl}, key, nil
	default: // backendKeychain, or empty (platform default)
		if backend == "" && !defaultBackendIsKeychain {
			return nil, nil, errf(contractCodeInvalidInput, "credential backend must be configured explicitly on this platform; use headless or keychain")
		}
		impl, err := newKeychainSecrets(instance)
		if err != nil {
			return nil, nil, err
		}
		wrapper := &secretStore{impl: impl}
		key, err := resolveMasterKey(src, wrapper)
		if err != nil {
			return nil, nil, err
		}
		return wrapper, key, nil
	}
}

// validateSecretKey bounds caller-facing key names.
func validateSecretKey(key string) error {
	if key == "" || len(key) > 256 {
		return errf(contractCodeInvalidInput, "credential key must be 1 to 256 characters")
	}
	for i := 0; i < len(key); i++ {
		if key[i] == 0 {
			return errf(contractCodeInvalidInput, "credential key must not contain NUL bytes")
		}
	}
	return nil
}

// keychainSecrets stores credentials in the OS keychain. Writes use the
// Security.framework on macOS so secret bytes never enter a child process's
// arguments. Reads and deletes retain the security CLI for compatibility.
// Values are base64 encoded so arbitrary bytes survive; the returned
// reference is opaque.
type keychainSecrets struct {
	service    string // keychain service name, namespaced per installation
	helperPath string
	mu         sync.RWMutex
	closed     bool
}

const defaultSecurityHelper = "/usr/bin/security"

func newKeychainSecrets(instance string) (*keychainSecrets, error) {
	if len(instance) < 16 {
		return nil, errf(contractCodeControllerUnavailable, "installation identity is unavailable")
	}
	return &keychainSecrets{
		service:    "com.zatiti.zatiti.v1." + instance[:16],
		helperPath: defaultSecurityHelper,
	}, nil
}

// keyRef is the stable opaque reference for a key: the account name is
// reversibly encoded without exposing anything sensitive.
func keychainRef(key string) string {
	return "kc1:" + base64.RawURLEncoding.EncodeToString([]byte(key))
}

func parseKeychainRef(ref string) (string, error) {
	const prefix = "kc1:"
	if len(ref) <= len(prefix) || ref[:len(prefix)] != prefix {
		return "", errf(contractCodeNotFound, "credential reference is unknown")
	}
	key, err := base64.RawURLEncoding.DecodeString(ref[len(prefix):])
	if err != nil || len(key) == 0 {
		return "", errf(contractCodeNotFound, "credential reference is unknown")
	}
	return string(key), nil
}

func (k *keychainSecrets) refForKey(key string) string { return keychainRef(key) }

func (k *keychainSecrets) zeroKeys() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.closed = true
}

func (k *keychainSecrets) ensureOpen() error {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.closed {
		return errf(contractCodeControllerUnavailable, "platform is closed")
	}
	return nil
}

func (k *keychainSecrets) Put(ctx context.Context, key string, secret []byte) (string, error) {
	if err := validateSecretKey(key); err != nil {
		return "", err
	}
	if len(secret) == 0 {
		return "", errf(contractCodeInvalidInput, "credential value must not be empty")
	}
	if len(secret) > 32*1024 {
		return "", errf(contractCodeInvalidInput, "credential exceeds the keychain size limit")
	}
	if err := k.ensureOpen(); err != nil {
		return "", err
	}
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(secret)))
	base64.StdEncoding.Encode(encoded, secret)
	defer zero(encoded)
	if err := k.putEncoded(ctx, key, encoded); err != nil {
		return "", err
	}
	return keychainRef(key), nil
}

func (k *keychainSecrets) Lookup(ctx context.Context, key string) (string, error) {
	if err := validateSecretKey(key); err != nil {
		return "", err
	}
	if err := k.ensureOpen(); err != nil {
		return "", err
	}
	ref := keychainRef(key)
	value, err := k.Get(ctx, ref)
	if err != nil {
		return "", err
	}
	zero(value)
	return ref, nil
}

func (k *keychainSecrets) Get(ctx context.Context, ref string) ([]byte, error) {
	if err := k.ensureOpen(); err != nil {
		return nil, err
	}
	key, err := parseKeychainRef(ref)
	if err != nil {
		return nil, err
	}
	args := []string{"find-generic-password", "-s", k.service, "-a", key, "-w"}
	out, err := k.output(ctx, args)
	if err != nil {
		return nil, err
	}
	value, derr := base64.StdEncoding.DecodeString(string(out))
	if derr != nil {
		return nil, errf(contractCodeControllerUnavailable, "stored credential is unreadable")
	}
	return value, nil
}

func (k *keychainSecrets) Delete(ctx context.Context, ref string) error {
	if err := k.ensureOpen(); err != nil {
		return err
	}
	key, err := parseKeychainRef(ref)
	if err != nil {
		return err
	}
	args := []string{"delete-generic-password", "-s", k.service, "-a", key}
	err = k.run(ctx, args)
	if isHelperNotFound(err) {
		return nil // already absent: deletion is idempotent
	}
	return err
}

// run and output invoke the trusted helper. Helper output is never included
// in error messages: it can echo secret material.
func (k *keychainSecrets) run(ctx context.Context, args []string) error {
	cmd := exec.CommandContext(ctx, k.helperPath, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return k.wrapHelperErr(err)
	}
	return nil
}

func (k *keychainSecrets) output(ctx context.Context, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, k.helperPath, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, k.wrapHelperErr(err)
	}
	trimmed := trimBytes(out)
	if len(trimmed) == 0 {
		return nil, errf(contractCodeNotFound, "credential reference is unknown")
	}
	return trimmed, nil
}

func (k *keychainSecrets) wrapHelperErr(err error) error {
	if isHelperNotFound(err) {
		return errf(contractCodeNotFound, "credential reference is unknown")
	}
	// A helper that is simply not installed (bad path, raw ENOENT) is a
	// named prerequisite failure, distinct from a refusal by the helper.
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return errf(contractCodePrerequisiteMissing, "the OS keychain helper is unavailable")
	}
	return errWrap(contractCodePrerequisiteMissing, "the OS keychain refused the credential operation", err)
}

// helperNotFound reports the macOS security CLI's item-not-found exit code
// (44).
func isHelperNotFound(err error) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode() == 44
	}
	return false
}

func trimBytes(b []byte) []byte {
	start := 0
	for start < len(b) && (b[start] == '\n' || b[start] == '\r' || b[start] == ' ' || b[start] == '\t') {
		start++
	}
	end := len(b)
	for end > start && (b[end-1] == '\n' || b[end-1] == '\r' || b[end-1] == ' ' || b[end-1] == '\t') {
		end--
	}
	return b[start:end]
}
