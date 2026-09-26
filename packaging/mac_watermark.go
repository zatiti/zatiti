package packaging

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	MacAcceptedReleaseSchema = "zatiti.mac_accepted_release/v1"
	macAcceptedMaxBytes      = 4096
	macAcceptedFile          = "accepted.json"
	macBootstrapLockFile     = "bootstrap.lock"
)

type macAcceptedDocument struct {
	Schema          string `json:"schema"`
	ReleaseSequence int64  `json:"release_sequence"`
	DeliverySHA256  string `json:"delivery_sha256"`
}

// FileMacSequenceWatermark owns the per-user accepted-release fence and the
// separate bootstrap lock. Home must be the installing user's real home in
// production; custom homes are useful only for isolated tests. This is a
// trusted in-process collaborator: callers must use Advance only within the
// RunMacBootstrap critical section. The held flag prevents accidental direct
// writes, while the kernel lock serializes distinct bootstrap processes.
type FileMacSequenceWatermark struct {
	dir  string
	mu   sync.Mutex
	held bool
}

func NewFileMacSequenceWatermark(home string) (*FileMacSequenceWatermark, error) {
	if !filepath.IsAbs(home) || filepath.Clean(home) != home || strings.IndexByte(home, 0) >= 0 {
		return nil, errf(CodeInvalidInput, "Mac installer home path is unsafe")
	}
	// Lstat(home) alone follows symlinks in its parents. Resolve the entire
	// supplied path before using it as the identity of the replay fence and
	// lock: two spellings of a home must not select different release state.
	resolved, err := filepath.EvalSymlinks(home)
	if err != nil || resolved != home {
		return nil, errf(CodeInvalidInput, "Mac installer home path traverses a symlink or is unavailable")
	}
	return &FileMacSequenceWatermark{dir: filepath.Join(home, "Library", "Application Support", "zatiti-installer")}, nil
}

func (w *FileMacSequenceWatermark) Lock(ctx context.Context) (func(), error) {
	if err := w.ensureDir(); err != nil {
		return nil, err
	}
	w.mu.Lock()
	if w.held {
		w.mu.Unlock()
		return nil, errf(CodeConflict, "Mac bootstrap lock is already held by this process")
	}
	w.mu.Unlock()
	path := filepath.Join(w.dir, macBootstrapLockFile)
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, errWrap(CodeConflict, "Mac bootstrap lock cannot be opened safely", err)
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !acceptedFileInfo(info) {
		_ = f.Close()
		return nil, errf(CodeConflict, "Mac bootstrap lock is not owner-only")
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = f.Close()
			return nil, errWrap(CodeConflict, "Mac bootstrap lock wait was cancelled", err)
		}
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = f.Close()
			return nil, errWrap(CodeConflict, "Mac bootstrap lock failed", err)
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = f.Close()
			return nil, errWrap(CodeConflict, "Mac bootstrap lock wait was cancelled", ctx.Err())
		case <-timer.C:
		}
	}
	w.mu.Lock()
	if w.held {
		w.mu.Unlock()
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		_ = f.Close()
		return nil, errf(CodeConflict, "Mac bootstrap lock is already held by this process")
	}
	w.held = true
	w.mu.Unlock()
	once := sync.Once{}
	return func() {
		once.Do(func() {
			w.mu.Lock()
			w.held = false
			w.mu.Unlock()
			_ = syscall.Flock(fd, syscall.LOCK_UN)
			_ = f.Close()
		})
	}, nil
}

func (w *FileMacSequenceWatermark) Load(ctx context.Context) (MacAcceptedRelease, error) {
	if err := ctx.Err(); err != nil {
		return MacAcceptedRelease{}, err
	}
	if err := w.checkDir(); err != nil {
		return MacAcceptedRelease{}, err
	}
	path := filepath.Join(w.dir, macAcceptedFile)
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return MacAcceptedRelease{}, nil
	}
	if err != nil {
		return MacAcceptedRelease{}, errWrap(CodeVerificationFailed, "Mac release fence cannot be opened safely", err)
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !acceptedFileInfo(info) {
		return MacAcceptedRelease{}, errf(CodeVerificationFailed, "Mac release fence is not owner-only")
	}
	raw, err := io.ReadAll(io.LimitReader(f, macAcceptedMaxBytes+1))
	if err != nil {
		return MacAcceptedRelease{}, errWrap(CodeVerificationFailed, "Mac release fence cannot be read", err)
	}
	if len(raw) == 0 || len(raw) > macAcceptedMaxBytes || !utf8.Valid(raw) {
		return MacAcceptedRelease{}, errf(CodeVerificationFailed, "Mac release fence is corrupt")
	}
	var d macAcceptedDocument
	if err := strictDecode(raw, &d); err != nil {
		return MacAcceptedRelease{}, errf(CodeVerificationFailed, "Mac release fence is corrupt")
	}
	if d.Schema != MacAcceptedReleaseSchema || d.ReleaseSequence <= 0 || !digestPattern.MatchString(d.DeliverySHA256) {
		return MacAcceptedRelease{}, errf(CodeVerificationFailed, "Mac release fence is corrupt or unsupported")
	}
	canonical, err := canonicalJSON(d)
	if err != nil || !bytes.Equal(canonical, raw) {
		return MacAcceptedRelease{}, errf(CodeVerificationFailed, "Mac release fence is not canonical")
	}
	return MacAcceptedRelease{Sequence: d.ReleaseSequence, DeliverySHA256: d.DeliverySHA256}, nil
}

func (w *FileMacSequenceWatermark) Advance(ctx context.Context, next MacAcceptedRelease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	held := w.held
	w.mu.Unlock()
	if !held {
		return errf(CodeConflict, "Mac bootstrap lock is required to advance release fence")
	}
	if next.Sequence <= 0 || !digestPattern.MatchString(next.DeliverySHA256) {
		return errf(CodeInvalidInput, "Mac accepted release is invalid")
	}
	current, err := w.Load(ctx)
	if err != nil {
		return err
	}
	if next.Sequence < current.Sequence || (next.Sequence == current.Sequence && next.DeliverySHA256 != current.DeliverySHA256) {
		return errf(CodeVerificationFailed, "Mac release fence cannot move backward or change an accepted release")
	}
	if next == current {
		return nil
	}
	raw, err := canonicalJSON(macAcceptedDocument{Schema: MacAcceptedReleaseSchema, ReleaseSequence: next.Sequence, DeliverySHA256: next.DeliverySHA256})
	if err != nil {
		return err
	}
	if len(raw) > macAcceptedMaxBytes {
		return errf(CodeInvalidInput, "Mac release fence exceeds size limit")
	}
	f, err := os.CreateTemp(w.dir, ".accepted-*")
	if err != nil {
		return errWrap(CodePrerequisiteMissing, "Mac release fence cannot be staged", err)
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return errWrap(CodePrerequisiteMissing, "Mac release fence permissions failed", err)
	}
	if _, err := f.Write(raw); err != nil {
		_ = f.Close()
		return errWrap(CodePrerequisiteMissing, "Mac release fence write failed", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return errWrap(CodePrerequisiteMissing, "Mac release fence flush failed", err)
	}
	if err := f.Close(); err != nil {
		return errWrap(CodePrerequisiteMissing, "Mac release fence close failed", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(name, filepath.Join(w.dir, macAcceptedFile)); err != nil {
		return errWrap(CodePrerequisiteMissing, "Mac release fence publication failed", err)
	}
	dir, err := os.Open(w.dir)
	if err != nil {
		return errWrap(CodePrerequisiteMissing, "Mac release fence directory flush failed", err)
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil || closeErr != nil {
		return errf(CodePrerequisiteMissing, "Mac release fence directory flush failed")
	}
	return nil
}

func (w *FileMacSequenceWatermark) ensureDir() error {
	home := filepath.Dir(filepath.Dir(filepath.Dir(w.dir)))
	for _, dir := range []string{home, filepath.Join(home, "Library"), filepath.Join(home, "Library", "Application Support"), w.dir} {
		info, err := os.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) && dir != home {
			if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return errWrap(CodePrerequisiteMissing, "Mac installer directory cannot be created", err)
			}
			info, err = os.Lstat(dir)
		}
		if err != nil || !info.IsDir() || !acceptedOwned(info) {
			return errf(CodeVerificationFailed, "Mac installer directory is unsafe")
		}
		if dir == w.dir && info.Mode().Perm() != 0o700 {
			return errf(CodeVerificationFailed, "Mac installer directory is not owner-only")
		}
	}
	return nil
}
func (w *FileMacSequenceWatermark) checkDir() error {
	home := filepath.Dir(filepath.Dir(filepath.Dir(w.dir)))
	for _, dir := range []string{home, filepath.Join(home, "Library"), filepath.Join(home, "Library", "Application Support"), w.dir} {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || !acceptedOwned(info) {
			return errf(CodeVerificationFailed, "Mac installer directory is unsafe")
		}
		if dir == w.dir && info.Mode().Perm() != 0o700 {
			return errf(CodeVerificationFailed, "Mac installer directory is not owner-only")
		}
	}
	return nil
}
func acceptedFileInfo(info os.FileInfo) bool {
	return info != nil && info.Mode().IsRegular() && info.Mode().Perm() == 0o600 && acceptedOwned(info)
}
func acceptedOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}

var _ MacSequenceWatermark = (*FileMacSequenceWatermark)(nil)
