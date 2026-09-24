package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

// This file contains no credential bytes. It records enough information to
// reconcile a command after process death and to find an already-written
// opaque reference without writing the credential a second time.
const helperIntentSchema = "zatiti.helper_intent/v1"
const maxHelperIntentBytes = 16 << 10

var helperIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type helperIntent struct {
	Schema            string    `json:"schema"`
	InstallationID    string    `json:"installation_id"`
	ConnectionID      string    `json:"connection_id"`
	ConnectionVersion int64     `json:"connection_version"`
	AccountIdentity   string    `json:"account_identity"`
	BeginKey          string    `json:"begin_key"`
	ChallengeID       string    `json:"challenge_id,omitempty"`
	ChallengeVersion  int64     `json:"challenge_version,omitempty"`
	ExpiresAt         time.Time `json:"expires_at,omitempty"`
	CredentialName    string    `json:"credential_name"`
	CredentialRef     string    `json:"credential_ref,omitempty"`
	CompleteKey       string    `json:"complete_key"`
}

type helperIntentStore struct{ dir string }

func newHelperIntentStore(stateDir string) (helperIntentStore, error) {
	dir := filepath.Join(stateDir, "helper-intents")
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return helperIntentStore{}, fmt.Errorf("creating protected helper intent directory: %w", err)
	}
	if err := helperPrivatePath(dir, true); err != nil {
		return helperIntentStore{}, err
	}
	return helperIntentStore{dir: dir}, nil
}

func helperPrivatePath(path string, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.IsDir() != directory || info.Mode().Perm()&0o077 != 0 {
		return errors.New("helper intent path is not private and regular")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return errors.New("helper intent path has the wrong owner")
	}
	return nil
}

func (s helperIntentStore) path(id string) (string, error) {
	if !helperIDPattern.MatchString(id) {
		return "", errors.New("helper intent has an invalid connection identity")
	}
	return filepath.Join(s.dir, id+".json"), nil
}

func (s helperIntentStore) read(id string) (*helperIntent, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, syscall.ENOENT) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("helper intent is not a private regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return nil, errors.New("helper intent has the wrong owner")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxHelperIntentBytes+1))
	if err != nil || len(raw) > maxHelperIntentBytes {
		return nil, errors.New("helper intent is unreadable or oversized")
	}
	var intent helperIntent
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&intent) != nil || dec.Decode(new(any)) != io.EOF || intent.Schema != helperIntentSchema || intent.ConnectionID != id || !helperIDPattern.MatchString(intent.InstallationID) || intent.ConnectionVersion < 1 || intent.BeginKey == "" || intent.CompleteKey == "" || intent.CredentialName == "" || len(intent.CredentialRef) > 512 {
		return nil, errors.New("helper intent is malformed")
	}
	return &intent, nil
}

// lock serializes terminal and GUI invocations for one connection. flock is
// released by the kernel after a crash; a durable lock file is harmless.
func (s helperIntentStore) lock(id string) (func(), error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	lockPath := path + ".lock"
	fd, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), lockPath)
	info, statErr := f.Stat()
	stat, ok := infoSysStat(info)
	if statErr != nil || info == nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || !ok || stat.Uid != uint32(os.Getuid()) {
		_ = f.Close()
		return nil, errors.New("helper intent lock is not a private regular file")
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(fd, syscall.LOCK_UN); _ = f.Close() }, nil
}

func infoSysStat(info os.FileInfo) (*syscall.Stat_t, bool) {
	if info == nil {
		return nil, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return stat, ok
}

func (s helperIntentStore) write(intent helperIntent) error {
	path, err := s.path(intent.ConnectionID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(intent)
	if err != nil || len(raw) > maxHelperIntentBytes {
		return errors.New("helper intent cannot be encoded within its bound")
	}
	if err := helperPrivatePath(path, false); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".pending-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return syncHelperIntentDir(s.dir)
}

func (s helperIntentStore) remove(id string) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	if err := helperPrivatePath(path, false); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncHelperIntentDir(s.dir)
}

func syncHelperIntentDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
