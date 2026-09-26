package packaging

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// lockInstallation serializes controller and desktop changes for one user.
// A kernel advisory lock is released even if the installer process dies.
func lockInstallation(ctx context.Context, l Layout) (func(), error) {
	path := filepath.Join(l.Home, ".zatiti-install.lock")
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, errWrap(CodeConflict, "installation lock could not be opened safely", err)
	}
	f := os.NewFile(uintptr(fd), path)
	closeFile := func() { _ = f.Close() }
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		closeFile()
		return nil, errf(CodeConflict, "installation lock is not an owner-only regular file")
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(fd, syscall.LOCK_UN); closeFile() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			closeFile()
			return nil, errWrap(CodeConflict, "installation lock failed", err)
		}
		select {
		case <-ctx.Done():
			closeFile()
			return nil, errWrap(CodeConflict, "installation lock wait was cancelled", ctx.Err())
		case <-deadline.C:
			closeFile()
			return nil, errf(CodeConflict, "another installation is still in progress")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
