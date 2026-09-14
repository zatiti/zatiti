package server

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
)

// socketMode is the private permission a local operation socket carries: the
// owning user only, no group or world access. It is the local access
// control for a transport that carries no per-connection authentication of
// its own beyond the bearer credential a caller supplies inside the
// envelope.
const socketMode = 0o600

// listenUnix binds a Unix-domain socket at path with owner-only permissions.
// A stale socket file left by a previous, no-longer-running controller is
// replaced; anything else already at path (a regular file, a directory, a
// symlink) is refused rather than removed.
func listenUnix(path string) (net.Listener, error) {
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("socket path %s exists and is not a socket", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("removing stale socket: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("inspecting socket path: %w", err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("binding unix socket: %w", err)
	}
	if err := os.Chmod(path, socketMode); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("setting socket permissions: %w", err)
	}
	return l, nil
}
