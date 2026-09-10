package platform

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Private file modes. The state root and every platform subdirectory are
// 0700; every sensitive file is 0600. Group and other access is never
// granted.
const (
	dirPrivate  fs.FileMode = 0o700
	filePrivate fs.FileMode = 0o600
)

// openPrivate opens path with O_NOFOLLOW so a symlink swapped in place of a
// state file is refused instead of traversed.
func openPrivate(path string, flag int, perm fs.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag|syscall.O_NOFOLLOW, perm)
}

// mkdirPrivate creates dir refusing to traverse or follow symlinks. Existing
// entries that are symlinks, or that are not directories, fail the call.
// enforce forces the 0700 mode on an existing directory (the state root and
// platform subdirectories); system parents are walked without chmod.
func mkdirPrivate(dir string) error {
	return mkdirPrivateMode(dir, true)
}

func mkdirPrivateMode(dir string, enforce bool) error {
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if mkErr := os.Mkdir(dir, dirPrivate); mkErr != nil && !errors.Is(mkErr, fs.ErrExist) {
			return errWrap(contractCodeControllerUnavailable, "state directory cannot be created", mkErr)
		}
		// A concurrent creator may have won the race; fall through and
		// validate the entry that now exists.
		fi, err = os.Lstat(dir)
	}
	switch {
	case err != nil:
		return errWrap(contractCodeControllerUnavailable, "state directory cannot be inspected", err)
	case fi.Mode()&os.ModeSymlink != 0:
		return errf(contractCodeInvalidInput, "state directory must not contain symbolic links")
	case !fi.IsDir():
		return errf(contractCodeInvalidInput, "state directory path is not a directory")
	}
	if !enforce {
		return nil
	}
	if err := os.Chmod(dir, dirPrivate); err != nil {
		return errWrap(contractCodeControllerUnavailable, "state directory permissions cannot be enforced", err)
	}
	return nil
}

// ensureDirChain walks each component of dir below an existing parent and
// creates missing directories with 0700. A symlink at any component is
// refused; the walk never follows one. Only the final component (the state
// root) is mode-enforced: pre-existing system parents keep their modes.
func ensureDirChain(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return errWrap(contractCodeInvalidInput, "state directory path is not resolvable", err)
	}
	parts := splitPath(abs)
	cur := "/"
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		if err := mkdirPrivateMode(cur, i == len(parts)-1); err != nil {
			return err
		}
	}
	return nil
}

func splitPath(abs string) []string {
	trimmed := strings.TrimPrefix(abs, string(filepath.Separator))
	parts := make([]string, 0, 8)
	for _, part := range strings.Split(trimmed, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		parts = append(parts, part)
	}
	return parts
}

// canonicalizeStatePath resolves pre-existing symlinks in the state path's
// ancestors (macOS aliases /var to /private/var) so the strict walk below
// the state root enforces the no-symlink property where it matters: inside
// the installation. The leaf itself is never resolved: a symlinked state
// path stays a symlink so the strict walk refuses it.
func canonicalizeStatePath(abs string) string {
	parent := filepath.Dir(abs)
	if parent == abs {
		return abs
	}
	p := parent
	for {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(resolved, strings.TrimPrefix(abs, p))
		}
		upper := filepath.Dir(p)
		if upper == p {
			return abs // reached the root without resolution
		}
		p = upper
	}
}

// writeFileSync writes data to path atomically: a temporary file in the same
// directory is created 0600, synced, renamed over path and the directory is
// synced. The rename replaces the named entry without following symlinks.
func writeFileSync(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return errWrap(contractCodeControllerUnavailable, "temporary file cannot be created", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if err := tmp.Chmod(filePrivate); err != nil {
		_ = tmp.Close()
		return errWrap(contractCodeControllerUnavailable, "temporary file permissions cannot be enforced", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return errWrap(contractCodeControllerUnavailable, "temporary file cannot be written", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return errWrap(contractCodeControllerUnavailable, "temporary file cannot be flushed", err)
	}
	if err := tmp.Close(); err != nil {
		return errWrap(contractCodeControllerUnavailable, "temporary file cannot be closed", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return errWrap(contractCodeControllerUnavailable, "file cannot be published", err)
	}
	return fsyncDir(dir)
}

// fsyncDir flushes a directory entry change so a rename survives a crash.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return errWrap(contractCodeControllerUnavailable, "directory cannot be opened for durability", err)
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		return errWrap(contractCodeControllerUnavailable, "directory cannot be flushed", err)
	}
	return nil
}

// readFilePrivate reads an existing file without following symlinks.
func readFilePrivate(path string) ([]byte, error) {
	f, err := openPrivate(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// removeFile deletes a state file; a missing file is already removed.
func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errWrap(contractCodeControllerUnavailable, "file cannot be removed", err)
	}
	return nil
}

// strictUnmarshal decodes JSON rejecting unknown fields and trailing data.
func strictUnmarshal(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("trailing data after JSON value")
	}
	return nil
}

// marshal encodes with stable field order via struct definition.
func marshal(v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, errf(contractCodeControllerUnavailable, "encoding failed")
	}
	return data, nil
}

// randHex returns n random bytes encoded as lowercase hex (2n characters).
func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is unrecoverable; fail loudly rather than
		// minting a predictable identity.
		panic(fmt.Sprintf("platform: crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(b)
}

// validDigest reports whether s is a lowercase SHA-256 hex digest.
func validDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

const hexChars = "0123456789abcdef"

// isHex reports whether s is exactly n lowercase hex characters.
func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune(hexChars, rune(s[i])) {
			return false
		}
	}
	return true
}
