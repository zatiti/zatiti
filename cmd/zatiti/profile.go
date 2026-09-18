package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/zatiti/zatiti/internal/contract"
)

const (
	dirPrivate  = 0o700
	filePrivate = 0o600
	// maxProfileBytes bounds a credential profile read; a header value is a
	// few hundred bytes, anything larger is not a credential this binary
	// minted.
	maxProfileBytes = 8 << 10
)

// errProfileMissing reports that the selected profile has never been
// written: the operator either has not initialized the installation or
// selected the wrong profile or state directory.
var errProfileMissing = errors.New("credential profile is not present")

// profileStore is the CLI's protected client credential storage: one 0600
// file per profile under a 0700 directory inside the state directory. The
// file holds the complete Authorization header value installation minted
// and nothing else. It is written exactly once by the controller at
// bootstrap completion and read per request by clients of this binary.
type profileStore struct{ dir string }

func (p profileStore) path(name string) (string, error) {
	if err := validateProfileName(name); err != nil {
		return "", err
	}
	return filepath.Join(p.dir, name), nil
}

// exists reports whether the named profile has been written.
func (p profileStore) exists(name string) (bool, error) {
	path, err := p.path(name)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Mode().IsRegular(), nil
}

// write stores credential under name atomically with private permissions.
// An existing profile is replaced: a re-bootstrapped installation mints a
// new owner credential and the old one authenticates nothing.
func (p profileStore) write(name string, credential []byte) error {
	path, err := p.path(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.dir, dirPrivate); err != nil {
		return fmt.Errorf("creating the profile directory: %w", err)
	}
	if err := os.Chmod(p.dir, dirPrivate); err != nil {
		return fmt.Errorf("protecting the profile directory: %w", err)
	}
	tmp, err := os.CreateTemp(p.dir, "."+name+".*")
	if err != nil {
		return fmt.Errorf("staging the profile: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if err := tmp.Chmod(filePrivate); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("protecting the profile: %w", err)
	}
	if _, err := tmp.Write(credential); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("writing the profile: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("syncing the profile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing the profile: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("publishing the profile: %w", err)
	}
	return nil
}

// read returns the stored header value. Trailing newlines are tolerated so
// an operator-edited profile still works; anything that is not a legal HTTP
// header value is refused here, before it could be sent or logged.
func (p profileStore) read(name string) ([]byte, error) {
	path, err := p.path(name)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: profile %q", errProfileMissing, name)
	}
	if err != nil {
		return nil, fmt.Errorf("inspecting profile %q: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("profile %q is not a regular file", name)
	}
	if info.Size() > maxProfileBytes {
		return nil, fmt.Errorf("profile %q is larger than a credential", name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading profile %q: %w", name, err)
	}
	data = bytes.TrimRight(data, "\r\n")
	if err := validateHeaderValue(data); err != nil {
		return nil, fmt.Errorf("profile %q: %w", name, err)
	}
	return data, nil
}

// validateHeaderValue accepts exactly what net/http will send as a header
// value: printable ASCII (and horizontal tab), nothing else, non-empty.
func validateHeaderValue(v []byte) error {
	if len(v) == 0 {
		return errors.New("credential is empty")
	}
	for _, b := range v {
		if b == '\t' {
			continue
		}
		if b < 0x20 || b >= 0x7f {
			return errors.New("credential is not a valid Authorization header value; installation must custody the complete header value (\"Bearer <token>\")")
		}
	}
	return nil
}

// profileCredential implements contract.CredentialSource over one profile.
// It reads the file per request so a rotated profile takes effect without a
// restart and no credential lingers in process memory between calls.
type profileCredential struct {
	store profileStore
	name  string
}

func (c profileCredential) Credential(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.store.read(c.name)
}

var _ contract.CredentialSource = profileCredential{}
