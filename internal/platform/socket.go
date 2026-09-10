package platform

import (
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// socketPathLimit is the common sun_path bound for Unix-domain sockets
// (104 bytes on darwin, 108 on Linux): a longer path fails at bind time.
const socketPathMax = 104

// ListenPrivate binds the controller's local HTTP listener on a private
// Unix-domain socket: the parent directory is forced to 0700, the socket
// itself is 0600, a stale socket left by a previous controller is replaced,
// and anything else at the path (regular file, symlink) is refused rather
// than removed. Closing the listener removes the socket file.
func ListenPrivate(path string) (net.Listener, error) {
	if path == "" {
		return nil, errf(contractCodeInvalidInput, "socket path is required")
	}
	if !filepath.IsAbs(path) {
		return nil, errf(contractCodeInvalidInput, "socket path must be absolute")
	}
	if len(path) >= socketPathMax {
		return nil, errf(contractCodeInvalidInput, "socket path is too long")
	}
	dir := filepath.Dir(path)
	// Resolve pre-existing parent symlinks (OS-managed aliases such as
	// /var) so the strict directory walk below applies to the real location.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
		path = filepath.Join(dir, filepath.Base(path))
	}
	if err := mkdirPrivate(dir); err != nil {
		return nil, err
	}
	if err := checkFilesystemLocal(dir); err != nil {
		return nil, err
	}
	if fi, err := os.Lstat(path); err == nil {
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			return nil, errf(contractCodeInvalidInput, "socket path must not be a symbolic link")
		case fi.Mode()&os.ModeSocket == 0:
			return nil, errf(contractCodeInvalidInput, "socket path exists and is not a socket")
		default:
			// Stale socket from a crashed predecessor: safe to replace.
			if err := os.Remove(path); err != nil {
				return nil, errWrap(contractCodeControllerUnavailable, "stale socket cannot be removed", err)
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, errWrap(contractCodeControllerUnavailable, "socket path cannot be inspected", err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, errWrap(contractCodeControllerUnavailable, "private socket cannot be bound", err)
	}
	if err := os.Chmod(path, filePrivate); err != nil {
		_ = l.Close()
		return nil, errWrap(contractCodeControllerUnavailable, "private socket permissions cannot be enforced", err)
	}
	return &privateListener{Listener: l, path: path, closeOnce: sync.Once{}}, nil
}

// privateListener removes its socket file on Close so a stopped controller
// leaves no stale socket behind.
type privateListener struct {
	net.Listener
	path      string
	closeOnce sync.Once
}

func (l *privateListener) Close() error {
	err := l.Listener.Close()
	l.closeOnce.Do(func() {
		if fi, statErr := os.Lstat(l.path); statErr == nil && fi.Mode()&os.ModeSocket != 0 {
			_ = os.Remove(l.path)
		}
	})
	return err
}
