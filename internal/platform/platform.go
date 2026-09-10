// Package platform owns the local-controller foundation: the exclusive
// installation lock, protected local state layout, secret custody and
// encrypted blob storage.
//
// Design points frozen by the implementation contract:
//
//   - Open validates configuration and prepares the state directory (state
//     root 0700, sensitive files 0600, no symlink traversal, no network
//     filesystems). Acquire takes the exclusive installation lock; Close
//     releases resources. Lock release is always explicit via the returned
//     contract.Ownership.
//   - Secrets use the OS keychain on macOS (local trusted helper process) or
//     an explicitly provisioned headless store (encrypted files under the
//     master key). There is no plaintext fallback when a secure store is
//     unavailable.
//   - Blobs are always encrypted at rest with AES-256-GCM under a subkey of
//     the configured master key, with a per-envelope random nonce and
//     authenticated metadata binding the plaintext digest and size.
//   - Errors are redacted: no local absolute paths, command output or secret
//     material ever appears in an error message.
package platform

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// DefaultMaxArtifactBytes is the shipped per-object artifact bound. A
// configuration may narrow it; widening requires authorized configuration.
const DefaultMaxArtifactBytes int64 = 256 << 20

// Config configures one platform instance for one state directory.
type Config struct {
	// StateDir is the absolute path of the installation state directory.
	StateDir string

	// CredentialBackend selects secret custody: "keychain" (OS secure
	// store), "headless" (explicitly provisioned encrypted files) or ""
	// (macOS default: keychain; elsewhere configuration is required).
	CredentialBackend string

	// MasterKeyRef locates the master key outside the state directory:
	// "file:<absolute path>" reads a 0600 key file (32 raw bytes, or 32
	// bytes as hex/base64); "secret:<key>" resolves through the
	// credential store. Required: blobs are always encrypted at rest.
	MasterKeyRef string

	// MaxArtifactBytes narrows the per-object artifact bound; 0 selects
	// DefaultMaxArtifactBytes.
	MaxArtifactBytes int64
}

// Platform owns one installation's local foundation. It is safe for
// concurrent use; Acquire may be called at most successfully once per
// Platform and the returned ownership is released only through
// contract.Ownership.Close.
type Platform struct {
	stateDir string
	maxBytes int64

	secrets *secretStore
	blobs   *blobStore

	// lockWatchInterval is the ownership watchdog period; shortened by
	// tests.
	lockWatchInterval time.Duration

	mu     sync.Mutex
	closed bool
}

const (
	dirNameSecrets    = "secrets"
	dirNameSecretVals = "blobs"
	dirNameBlobs      = "blobs"
	dirNameStaged     = "staged"
	dirNameObjects    = "objects"
	fileInstanceID    = "instance.id"
	fileLock          = "controller.lock"
	fileSecretIndex   = "index.json"
)

// Open validates cfg, prepares the protected state layout, resolves the
// master key and constructs the credential and blob stores. It does not take
// the installation lock; Acquire does that explicitly.
func Open(cfg Config) (*Platform, error) {
	if err := platformSupported(); err != nil {
		return nil, err
	}
	if cfg.StateDir == "" {
		return nil, errf(contractCodeInvalidInput, "state directory is required")
	}
	if !filepath.IsAbs(cfg.StateDir) {
		return nil, errf(contractCodeInvalidInput, "state directory must be an absolute path")
	}
	maxBytes := cfg.MaxArtifactBytes
	switch {
	case maxBytes < 0:
		return nil, errf(contractCodeInvalidInput, "maximum artifact size must not be negative")
	case maxBytes == 0:
		maxBytes = DefaultMaxArtifactBytes
	}
	switch cfg.CredentialBackend {
	case "", backendKeychain, backendHeadless:
	default:
		return nil, errf(contractCodeInvalidInput, "credential backend must be keychain or headless")
	}
	if cfg.MasterKeyRef == "" {
		return nil, errf(contractCodeInvalidInput, "master key reference is required because artifacts are always encrypted at rest")
	}
	src, err := parseMasterKeyRef(cfg.MasterKeyRef)
	if err != nil {
		return nil, err
	}
	// A headless store encrypts its own files under the master key; it can
	// never resolve that key from itself.
	if cfg.CredentialBackend == backendHeadless && src.kind == masterSourceSecret {
		return nil, errf(contractCodeInvalidInput, "the headless credential store cannot hold its own master key; use a file: master key reference")
	}

	// Canonicalize through pre-existing parent symlinks first (macOS aliases
	// /var to /private/var); the walk below the state root stays symlink-free.
	cfg.StateDir = canonicalizeStatePath(cfg.StateDir)
	if err := ensureDirChain(cfg.StateDir); err != nil {
		return nil, err
	}
	if err := checkFilesystemLocal(cfg.StateDir); err != nil {
		return nil, err
	}
	for _, sub := range []string{dirNameSecrets, filepath.Join(dirNameSecrets, dirNameSecretVals), dirNameBlobs, filepath.Join(dirNameBlobs, dirNameStaged), filepath.Join(dirNameBlobs, dirNameObjects)} {
		if err := mkdirPrivate(filepath.Join(cfg.StateDir, sub)); err != nil {
			return nil, err
		}
	}
	instance, err := ensureInstanceID(cfg.StateDir)
	if err != nil {
		return nil, err
	}

	secrets, key, err := newSecretStore(cfg.CredentialBackend, cfg.StateDir, instance, src)
	if err != nil {
		return nil, err
	}
	blobKey, err := deriveSubkey(key, hkdfInfoBlob)
	if err != nil {
		zero(key)
		return nil, err
	}
	zero(key)
	blobs, err := newBlobStore(cfg.StateDir, maxBytes, blobKey)
	if err != nil {
		return nil, err
	}
	return &Platform{
		stateDir:          cfg.StateDir,
		maxBytes:          maxBytes,
		secrets:           secrets,
		blobs:             blobs,
		lockWatchInterval: lockWatchIntervalDefault,
	}, nil
}

// StateDir returns the absolute state directory backing this platform.
func (p *Platform) StateDir() string { return p.stateDir }

// Secrets returns the platform credential store. Secrets move only between
// this process and the configured secure store; they never appear in
// environment variables, logs or fault messages.
func (p *Platform) Secrets() contract.SecretStore { return p.secrets }

// Blobs returns the encrypted, content-addressed artifact store.
func (p *Platform) Blobs() contract.BlobStore { return p.blobs }

// Close releases platform resources. It is idempotent and does not release
// installation ownership: that is explicit via contract.Ownership.Close.
func (p *Platform) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	p.secrets.zeroKeys()
	p.blobs.zeroKeys()
	return nil
}

func (p *Platform) ensureOpen() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errf(contractCodeControllerUnavailable, "platform is closed")
	}
	return nil
}

// ensureInstanceID reads or creates the per-installation instance tag used
// to namespace credentials in the OS keychain without exposing paths.
func ensureInstanceID(stateDir string) (string, error) {
	path := filepath.Join(stateDir, fileInstanceID)
	if data, err := os.ReadFile(path); err == nil {
		id := string(bytes.TrimSpace(data))
		if isHex(id, instanceIDHexLen) {
			return id, nil
		}
		return "", errf(contractCodeControllerUnavailable, "installation identity file is corrupt")
	} else if !os.IsNotExist(err) {
		return "", errWrap(contractCodeControllerUnavailable, "installation identity file cannot be read", err)
	}
	f, err := openPrivate(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePrivate)
	if os.IsExist(err) {
		// A concurrent Open in this installation won the race; use its id.
		data, err := os.ReadFile(path)
		if err == nil {
			if id := string(bytes.TrimSpace(data)); isHex(id, instanceIDHexLen) {
				return id, nil
			}
		}
		return "", errf(contractCodeControllerUnavailable, "installation identity file is corrupt")
	}
	if err != nil {
		return "", errWrap(contractCodeControllerUnavailable, "installation identity file cannot be created", err)
	}
	defer func() { _ = f.Close() }()
	id := randHex(instanceIDBytes)
	if _, err := f.WriteString(id); err != nil {
		return "", errWrap(contractCodeControllerUnavailable, "installation identity file cannot be written", err)
	}
	return id, nil
}

const (
	instanceIDBytes  = 16
	instanceIDHexLen = 32
)
