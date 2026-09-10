package platform

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSymlinkEscapeIsRefused plants symlinks at every state-internal path a
// controller touches and proves each is refused rather than traversed.
func TestSymlinkEscapeIsRefused(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	keyRef := writeKeyFile(t, testMasterKey(t))
	p, err := Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: keyRef})
	if err != nil {
		t.Fatal(err)
	}
	p.lockWatchInterval = 25 * time.Millisecond
	defer func() { _ = p.Close() }()

	target := filepath.Join(dir, "escape")
	if err := os.WriteFile(target, []byte("escaped"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A secret value file replaced by a symlink: the read must refuse
	// instead of following the link out of the state tree.
	ref, err := p.Secrets().Put(ctx(), "k", []byte("v"))
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimPrefix(ref, "hl1:")
	valPath := filepath.Join(state, dirNameSecrets, dirNameSecretVals, id)
	if err := os.Remove(valPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, valPath); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Secrets().Get(ctx(), ref); err == nil {
		t.Fatal("secret read traversed a symlink escape")
	}

	// Replacing an internal directory with a symlink: a fresh Open must
	// refuse to build on the tampered tree instead of traversing it.
	_ = os.RemoveAll(filepath.Join(state, dirNameSecrets, dirNameSecretVals))
	if err := os.Symlink(dir, filepath.Join(state, dirNameSecrets, dirNameSecretVals)); err != nil {
		t.Fatal(err)
	}
	_, err = Open(Config{StateDir: state, CredentialBackend: backendHeadless, MasterKeyRef: keyRef})
	wantCode(t, err, contractCodeInvalidInput)
	// Restore the tree so the rest of the test runs against a sane platform.
	_ = os.Remove(filepath.Join(state, dirNameSecrets, dirNameSecretVals))
	if err := os.Mkdir(filepath.Join(state, dirNameSecrets, dirNameSecretVals), 0o700); err != nil {
		t.Fatal(err)
	}

	// A staged artifact replaced by a symlink must not be published.
	data := []byte("staged payload")
	sref, digest, _, err := p.Blobs().Stage(ctx(), bytesReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	sid := strings.TrimPrefix(sref, "st1:")
	stagedPath := filepath.Join(state, dirNameBlobs, dirNameStaged, sid)
	if err := os.Remove(stagedPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, stagedPath); err != nil {
		t.Fatal(err)
	}
	if err := p.Blobs().Publish(ctx(), sref, digest); err == nil {
		t.Fatal("publish traversed a symlinked staging file")
	}
	if _, err := os.Stat(filepath.Join(state, dirNameBlobs, dirNameObjects, string(digest)[:2])); err == nil {
		t.Fatal("escaped bytes reached the object store")
	}
}

// shortTempDir creates a temp directory short enough for a Unix socket
// path (macOS bounds sun_path at 104 bytes).
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "zt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestListenPrivatePermissionsAndLifecycle(t *testing.T) {
	dir := filepath.Join(shortTempDir(t), "run")
	path := filepath.Join(dir, "controller.sock")

	l, err := ListenPrivate(path)
	if err != nil {
		t.Fatalf("ListenPrivate: %v", err)
	}
	if got := modeOf(t, dir); got != 0o700 {
		t.Fatalf("socket directory mode is %o, want 700", got)
	}
	if got := modeOf(t, path); got != 0o600 {
		t.Fatalf("socket mode is %o, want 600", got)
	}

	// The listener accepts a local connection.
	conn, err := dialUnix(path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()

	// Close removes the socket file.
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("socket file survived Close")
	}

	// Rebinding after close works.
	l2, err := ListenPrivate(path)
	if err != nil {
		t.Fatalf("rebind: %v", err)
	}
	_ = l2.Close()
}

func TestListenPrivateReplacesStaleSocket(t *testing.T) {
	dir := filepath.Join(shortTempDir(t), "run")
	path := filepath.Join(dir, "controller.sock")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	plantStaleSocket(t, path)

	l, err := ListenPrivate(path)
	if err != nil {
		t.Fatalf("stale socket was not replaced: %v", err)
	}
	_ = l.Close()
}

func TestListenPrivateRefusesOverlongPath(t *testing.T) {
	long := "/" + strings.Repeat("d", socketPathMax)
	_, err := ListenPrivate(long)
	wantCode(t, err, contractCodeInvalidInput)
}

func TestListenPrivateRefusesNonSocketEntries(t *testing.T) {
	dir := filepath.Join(shortTempDir(t), "run")
	path := filepath.Join(dir, "controller.sock")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	t.Run("regular file", func(t *testing.T) {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := ListenPrivate(path)
		wantCode(t, err, contractCodeInvalidInput)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		if err := os.Symlink(filepath.Join(dir, "elsewhere"), path); err != nil {
			t.Fatal(err)
		}
		_, err := ListenPrivate(path)
		wantCode(t, err, contractCodeInvalidInput)
	})

	t.Run("relative path", func(t *testing.T) {
		_, err := ListenPrivate("relative.sock")
		wantCode(t, err, contractCodeInvalidInput)
	})

	t.Run("empty path", func(t *testing.T) {
		_, err := ListenPrivate("")
		wantCode(t, err, contractCodeInvalidInput)
	})
}

func TestListenPrivateRefusesSymlinkedRunDirectory(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "elsewhere")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "run")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	_, err := ListenPrivate(filepath.Join(link, "controller.sock"))
	wantCode(t, err, contractCodeInvalidInput)
}

// bytesReader aliases bytes.NewReader for the attack-path tests.
func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// dialUnix makes one local unix-socket connection.
func dialUnix(path string) (net.Conn, error) {
	return net.Dial("unix", path)
}

// plantStaleSocket leaves a real socket inode with no listener at path, as a
// crashed predecessor would.
func plantStaleSocket(t *testing.T, path string) {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: path}); err != nil {
		_ = syscall.Close(fd)
		t.Fatal(err)
	}
	if err := syscall.Close(fd); err != nil {
		t.Fatal(err)
	}
}
