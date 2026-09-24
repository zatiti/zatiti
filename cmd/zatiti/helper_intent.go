package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	Schema                string    `json:"schema"`
	InstallationID        string    `json:"installation_id"`
	ConnectionID          string    `json:"connection_id"`
	ConnectionVersion     int64     `json:"connection_version"`
	AccountIdentity       string    `json:"account_identity"`
	BeginKey              string    `json:"begin_key"`
	BeginRequestSHA256    string    `json:"begin_request_sha256"`
	ChallengeID           string    `json:"challenge_id,omitempty"`
	ChallengeVersion      int64     `json:"challenge_version,omitempty"`
	ExpiresAt             time.Time `json:"expires_at,omitempty"`
	CredentialName        string    `json:"credential_name"`
	CredentialRef         string    `json:"credential_ref,omitempty"`
	CompleteKey           string    `json:"complete_key"`
	CompleteRequestSHA256 string    `json:"complete_request_sha256,omitempty"`
}

var helperDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func helperMutationDigest(operation, key string, input []byte) string {
	// Input is the exact json.Marshal result passed to the operator. Include
	// operation identity and submission key so neither can drift on replay.
	preimage, _ := json.Marshal(struct {
		Operation     string          `json:"operation"`
		Version       int             `json:"operation_version"`
		SubmissionKey string          `json:"submission_key"`
		Input         json.RawMessage `json:"input"`
	}{operation, 1, key, input})
	sum := sha256.Sum256(preimage)
	return hex.EncodeToString(sum[:])
}

func validHelperIntent(i helperIntent) bool {
	if i.Schema != helperIntentSchema || !helperIDPattern.MatchString(i.InstallationID) || !helperIDPattern.MatchString(i.ConnectionID) ||
		i.ConnectionVersion < 1 || i.ConnectionVersion > 1<<53 || i.AccountIdentity == "" || len(i.AccountIdentity) > 8192 ||
		len(i.BeginKey) == 0 || len(i.BeginKey) > 256 || len(i.CompleteKey) == 0 || len(i.CompleteKey) > 256 || i.BeginKey == i.CompleteKey ||
		strings.ContainsAny(i.BeginKey+i.CompleteKey, "\r\n\x00") || !helperDigestPattern.MatchString(i.BeginRequestSHA256) ||
		!strings.HasPrefix(i.CredentialName, "connections/credential/") || len(i.CredentialName) > 256 ||
		!helperIDPattern.MatchString(strings.TrimPrefix(i.CredentialName, "connections/credential/")) || len(i.CredentialRef) > 512 {
		return false
	}
	if i.ChallengeID == "" {
		return i.ChallengeVersion == 0 && i.ExpiresAt.IsZero() && i.CredentialRef == "" && i.CompleteRequestSHA256 == ""
	}
	if !helperIDPattern.MatchString(i.ChallengeID) || i.ChallengeVersion < 1 || i.ExpiresAt.IsZero() {
		return false
	}
	if i.CompleteRequestSHA256 != "" && (i.CredentialRef == "" || !helperDigestPattern.MatchString(i.CompleteRequestSHA256)) {
		return false
	}
	return true
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
	if dec.Decode(&intent) != nil || dec.Decode(new(any)) != io.EOF || intent.ConnectionID != id || !validHelperIntent(intent) {
		return nil, errors.New("helper intent is malformed")
	}
	canonical, err := json.Marshal(intent)
	if err != nil || !bytes.Equal(raw, append(canonical, '\n')) {
		return nil, errors.New("helper intent is not canonical")
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
	if !validHelperIntent(intent) {
		return errors.New("helper intent is malformed")
	}
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
