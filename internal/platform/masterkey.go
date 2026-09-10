package platform

import (
	"bytes"
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
)

// Master key handling. The master key never lives in the database or in
// backups: it is loaded from a file outside the state directory (file: ref)
// or from the platform credential store (secret: ref), then split into
// purpose-bound subkeys so blob encryption and secret-file encryption never
// share a key.

const (
	masterRefFilePrefix   = "file:"
	masterRefSecretPrefix = "secret:"

	masterSourceFile   = 0
	masterSourceSecret = 1

	hkdfInfoBlob     = "zatiti/platform/blob/v1"
	hkdfInfoSecrets  = "zatiti/platform/secrets/v1"
	hkdfInfoInternal = "zatiti/platform/headless-secrets/v1"
)

type masterKeySource struct {
	kind   int
	file   string
	secret string
}

func parseMasterKeyRef(ref string) (masterKeySource, error) {
	switch {
	case len(ref) > len(masterRefFilePrefix) && ref[:len(masterRefFilePrefix)] == masterRefFilePrefix:
		path := ref[len(masterRefFilePrefix):]
		if path == "" {
			return masterKeySource{}, errf(contractCodeInvalidInput, "master key file reference is empty")
		}
		return masterKeySource{kind: masterSourceFile, file: path}, nil
	case len(ref) > len(masterRefSecretPrefix) && ref[:len(masterRefSecretPrefix)] == masterRefSecretPrefix:
		key := ref[len(masterRefSecretPrefix):]
		if key == "" {
			return masterKeySource{}, errf(contractCodeInvalidInput, "master key secret reference is empty")
		}
		return masterKeySource{kind: masterSourceSecret, secret: key}, nil
	default:
		return masterKeySource{}, errf(contractCodeInvalidInput, "master key reference must use the file: or secret: scheme")
	}
}

// resolveMasterKey loads 32 bytes of key material from the configured
// source. Key files must be owner-only (0600 or tighter) and are opened
// without following symlinks; secret references resolve through the
// platform credential store.
func resolveMasterKey(src masterKeySource, secrets *secretStore) ([]byte, error) {
	var raw []byte
	switch src.kind {
	case masterSourceFile:
		data, err := readKeyFile(src.file)
		if err != nil {
			return nil, err
		}
		raw = data
	case masterSourceSecret:
		if secrets == nil {
			return nil, errf(contractCodeInvalidInput, "the headless credential store cannot hold its own master key; use a file: master key reference")
		}
		ref := secrets.refForKey(src.secret)
		if ref == "" {
			return nil, errf(contractCodePrerequisiteMissing, "master key secret is not provisioned in the credential store")
		}
		value, err := secrets.Get(context.Background(), ref)
		if err != nil {
			return nil, errWrap(contractCodePrerequisiteMissing, "master key secret cannot be read from the credential store", err)
		}
		raw = value
	default:
		return nil, errf(contractCodeInternalErrorAlias, "master key reference kind is unknown")
	}
	defer zero(raw)
	key, err := decodeKeyMaterial(raw)
	if err != nil {
		return nil, err
	}
	return key, nil
}

// readKeyFile reads at most 4 KiB of key material with strict permissions:
// a group- or world-readable key file is refused outright.
func readKeyFile(path string) ([]byte, error) {
	f, err := openPrivate(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, errWrap(contractCodePrerequisiteMissing, "master key file is unreadable", err)
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return nil, errWrap(contractCodePrerequisiteMissing, "master key file cannot be inspected", err)
	}
	if fi.Mode().Type() != 0 {
		return nil, errf(contractCodePrerequisiteMissing, "master key file must be a regular file")
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return nil, errf(contractCodePrerequisiteMissing, "master key file must be readable only by its owner")
	}
	data := make([]byte, 4096)
	n, err := f.Read(data)
	if err != nil && n == 0 {
		return nil, errWrap(contractCodePrerequisiteMissing, "master key file is empty", err)
	}
	return data[:n], nil
}

// decodeKeyMaterial accepts a raw 32-byte key, 64 hex characters or
// base64-encoded bytes, and requires exactly 32 decoded bytes. An
// untrimmed 32-byte read is taken as raw material first: trimming would
// corrupt a key whose bytes happen to be whitespace, and encodings that
// arrive with trailing newlines still fall through the trim below.
func decodeKeyMaterial(raw []byte) ([]byte, error) {
	if len(raw) == 32 {
		out := make([]byte, 32)
		copy(out, raw)
		return out, nil
	}
	trimmed := bytes.TrimSpace(raw)
	switch {
	case len(trimmed) == 32:
		out := make([]byte, 32)
		copy(out, trimmed)
		return out, nil
	case len(trimmed) == 64:
		out := make([]byte, 32)
		if _, err := hex.Decode(out, trimmed); err != nil {
			return nil, errf(contractCodeInvalidInput, "master key material is not valid hex")
		}
		return out, nil
	default:
		decoded, err := base64.StdEncoding.DecodeString(string(trimmed))
		if err != nil || len(decoded) != 32 {
			decoded, err = base64.RawURLEncoding.DecodeString(string(trimmed))
			if err != nil || len(decoded) != 32 {
				return nil, errf(contractCodeInvalidInput, "master key material must be 32 raw, hex or base64 bytes")
			}
		}
		out := make([]byte, 32)
		copy(out, decoded)
		zero(decoded)
		return out, nil
	}
}

// deriveSubkey derives one purpose-bound 32-byte subkey from the master key.
func deriveSubkey(master []byte, info string) ([]byte, error) {
	key, err := hkdf.Key(sha256.New, master, nil, info, 32)
	if err != nil {
		return nil, errf(contractCodeInternalErrorAlias, "key derivation failed")
	}
	return key, nil
}

// zero wipes a sensitive buffer where practical.
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
