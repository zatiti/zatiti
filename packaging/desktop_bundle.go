package packaging

// Desktop bundle archives. A `flutter build` output directory becomes one
// deterministic tar.gz artifact; an installer unpacks it under the same rules
// it was packed with. Nothing here depends on Flutter: the rules are about
// paths, links, modes and sizes.

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Bundle bounds. They are wide for a Flutter desktop bundle and narrow
// enough that an archive cannot exhaust the installer.
const (
	MaxBundleEntries       = 65536
	MaxBundleBytes   int64 = 1 << 30
)

type bundleEntry struct {
	path string // slash-separated, relative to the bundle root
	mode fs.FileMode
	size int64
	link string // symlink target, empty for a regular file or directory
	kind byte   // tar.TypeReg, tar.TypeDir or tar.TypeSymlink
}

// AssembleBundle archives a built application bundle. Entries are sorted,
// timestamps and ownership are zero, and only the owner-execute bit of a
// file mode survives, so the same bundle always produces the same bytes.
// Directories, regular files and relative symlinks that stay inside the
// bundle are accepted; anything else is refused. A file name that looks like
// controller state or key material is refused, because the desktop bundle
// never carries either.
//
// AssembleBundle returns the archive's SHA-256 digest and size.
func AssembleBundle(bundleDir, archivePath string) (string, int64, error) {
	if err := requireRealDir(bundleDir); err != nil {
		return "", 0, err
	}
	if !cleanAbs(archivePath) || pathWithin(archivePath, bundleDir) {
		return "", 0, errf(CodeInvalidInput, "the archive path must be a clean absolute path outside the bundle")
	}
	entries, err := scanBundle(bundleDir)
	if err != nil {
		return "", 0, err
	}
	out, err := os.OpenFile(archivePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", 0, errWrap(CodeConflict, "the archive path could not be created; it must not exist", err)
	}
	h := sha256.New()
	counter := &countingWriter{w: io.MultiWriter(out, h)}
	err = writeBundle(bundleDir, entries, counter)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(archivePath)
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), counter.n, nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func writeBundle(bundleDir string, entries []bundleEntry, w io.Writer) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.path, Mode: int64(e.mode), Format: tar.FormatPAX}
		switch e.kind {
		case tar.TypeDir:
			hdr.Typeflag = tar.TypeDir
			hdr.Name += "/"
		case tar.TypeSymlink:
			hdr.Typeflag = tar.TypeSymlink
			hdr.Linkname = e.link
		default:
			hdr.Typeflag = tar.TypeReg
			hdr.Size = e.size
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return errWrap(CodeInternalError, "the bundle archive could not be written", err)
		}
		if e.kind != tar.TypeReg {
			continue
		}
		if err := copyFileInto(tw, filepath.Join(bundleDir, filepath.FromSlash(e.path)), e.size); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return errWrap(CodeInternalError, "the bundle archive could not be written", err)
	}
	if err := gz.Close(); err != nil {
		return errWrap(CodeInternalError, "the bundle archive could not be written", err)
	}
	return nil
}

func copyFileInto(w io.Writer, p string, size int64) error {
	f, err := os.Open(p)
	if err != nil {
		return errWrap(CodeInternalError, "a bundle file could not be read", err)
	}
	defer func() { _ = f.Close() }()
	n, err := io.Copy(w, io.LimitReader(f, size))
	if err != nil {
		return errWrap(CodeInternalError, "a bundle file could not be read", err)
	}
	if n != size {
		return errf(CodeVerificationFailed, "a bundle file changed while it was archived")
	}
	return nil
}

// scanBundle lists a bundle directory in archive order and applies the
// entry rules.
func scanBundle(bundleDir string) ([]bundleEntry, error) {
	var entries []bundleEntry
	var total int64
	walkErr := filepath.WalkDir(bundleDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == bundleDir {
			return nil
		}
		relOS, err := filepath.Rel(bundleDir, p)
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(relOS)
		info, err := d.Info()
		if err != nil {
			return err
		}
		e := bundleEntry{path: rel}
		switch {
		case info.IsDir():
			e.kind, e.mode = tar.TypeDir, 0o755
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			e.kind, e.mode, e.link = tar.TypeSymlink, 0o777, target
		case info.Mode().IsRegular():
			e.kind, e.mode, e.size = tar.TypeReg, 0o644, info.Size()
			if info.Mode()&0o100 != 0 {
				e.mode = 0o755
			}
			total += e.size
		default:
			return errf(CodeInvalidInput, "bundle entry %s is a special file", printablePath(rel))
		}
		if err := checkBundleEntry(e); err != nil {
			return err
		}
		entries = append(entries, e)
		if len(entries) > MaxBundleEntries || total > MaxBundleBytes {
			return errf(CodeInvalidInput, "the bundle exceeds %d entries or %d bytes", MaxBundleEntries, MaxBundleBytes)
		}
		return nil
	})
	if walkErr != nil {
		var perr *Error
		if errors.As(walkErr, &perr) {
			return nil, walkErr
		}
		return nil, errWrap(CodeInternalError, "the bundle could not be read", walkErr)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	return entries, nil
}

// checkBundleEntry applies the path, name and link rules to one entry.
func checkBundleEntry(e bundleEntry) error {
	if err := validateRelPath(e.path); err != nil {
		return errf(CodeInvalidInput, "bundle entry %s is not a clean relative path", printablePath(e.path))
	}
	for _, ext := range stateExtensions {
		if strings.HasSuffix(strings.ToLower(path.Base(e.path)), ext) {
			return errf(CodeInvalidInput, "bundle entry %s looks like controller state or key material", printablePath(e.path))
		}
	}
	if e.kind != tar.TypeSymlink {
		return nil
	}
	if e.link == "" || path.IsAbs(e.link) || hasControl(e.link) || strings.ContainsRune(e.link, '\\') {
		return errf(CodeInvalidInput, "bundle link %s must be relative", printablePath(e.path))
	}
	resolved := path.Join(path.Dir(e.path), e.link)
	if resolved == ".." || strings.HasPrefix(resolved, "../") || resolved == "." {
		return errf(CodeInvalidInput, "bundle link %s points outside the bundle", printablePath(e.path))
	}
	return nil
}

// readBundle streams archive entries through visit after validating each
// header. It returns the archive's digest and size so callers can check them
// against the manifest.
func readBundle(archivePath string, visit func(e bundleEntry, r io.Reader) error) (string, int64, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", 0, errWrap(CodeNotFound, "the bundle archive is missing", err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	counter := &countingWriter{w: h}
	gz, err := gzip.NewReader(io.TeeReader(io.LimitReader(f, MaxBundleBytes+1), counter))
	if err != nil {
		return "", 0, errWrap(CodeInvalidInput, "the bundle archive is not gzip data", err)
	}
	tr := tar.NewReader(gz)
	entries, seen := 0, map[string]bool{}
	var total int64
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", 0, errWrap(CodeInvalidInput, "the bundle archive is not a valid tar stream", err)
		}
		e := bundleEntry{path: strings.TrimSuffix(hdr.Name, "/"), mode: fs.FileMode(hdr.Mode) & 0o777, size: hdr.Size, link: hdr.Linkname}
		switch hdr.Typeflag {
		case tar.TypeDir:
			e.kind = tar.TypeDir
		case tar.TypeSymlink:
			e.kind = tar.TypeSymlink
		case tar.TypeReg:
			e.kind = tar.TypeReg
		default:
			return "", 0, errf(CodeInvalidInput, "bundle entry %s has a type that is not distributed", printablePath(e.path))
		}
		if err := checkBundleEntry(e); err != nil {
			return "", 0, err
		}
		if seen[e.path] {
			return "", 0, errf(CodeInvalidInput, "bundle entry %s appears twice", printablePath(e.path))
		}
		seen[e.path] = true
		entries++
		total += e.size
		if entries > MaxBundleEntries || total > MaxBundleBytes || e.size < 0 {
			return "", 0, errf(CodeInvalidInput, "the bundle exceeds %d entries or %d bytes", MaxBundleEntries, MaxBundleBytes)
		}
		if err := visit(e, io.LimitReader(tr, e.size)); err != nil {
			return "", 0, err
		}
	}
	// Drain so the digest covers the whole file, including the gzip
	// trailer.
	if _, err := io.Copy(io.Discard, io.TeeReader(io.LimitReader(f, MaxBundleBytes+1), counter)); err != nil {
		return "", 0, errWrap(CodeInternalError, "the bundle archive could not be read", err)
	}
	if counter.n > MaxBundleBytes {
		return "", 0, errf(CodeInvalidInput, "the bundle archive exceeds %d bytes", MaxBundleBytes)
	}
	return hex.EncodeToString(h.Sum(nil)), counter.n, nil
}

// extractBundle unpacks an archive whose digest and size must match the
// manifest into dest, which must not exist yet, and confirms that the
// declared executable is a regular executable file inside it.
func extractBundle(archivePath, dest, sha256Hex string, size int64, executable string) error {
	if err := os.Mkdir(dest, 0o755); err != nil {
		return errWrap(CodeConflict, "the bundle destination could not be created; it must not exist", err)
	}
	var links []bundleEntry
	digest, n, err := readBundle(archivePath, func(e bundleEntry, r io.Reader) error {
		target := filepath.Join(dest, filepath.FromSlash(e.path))
		switch e.kind {
		case tar.TypeDir:
			return os.MkdirAll(target, 0o755)
		case tar.TypeSymlink:
			links = append(links, e)
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		mode := fs.FileMode(0o644)
		if e.mode&0o100 != 0 {
			mode = 0o755
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		written, err := io.Copy(f, r)
		if err == nil {
			err = f.Chmod(mode)
		}
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err == nil && written != e.size {
			err = errf(CodeInvalidInput, "bundle entry %s is shorter than its header", printablePath(e.path))
		}
		return err
	})
	if err == nil && (digest != sha256Hex || n != size) {
		err = errf(CodeVerificationFailed, "the bundle archive differs from the manifest")
	}
	// Links go last so an entry can never be written through one.
	for _, e := range links {
		if err != nil {
			break
		}
		target := filepath.Join(dest, filepath.FromSlash(e.path))
		if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
			err = mkErr
			break
		}
		err = os.Symlink(filepath.FromSlash(e.link), target)
	}
	if err == nil {
		err = checkBundleExecutable(dest, executable)
	}
	if err != nil {
		_ = os.RemoveAll(dest)
		var perr *Error
		if errors.As(err, &perr) {
			return err
		}
		return errWrap(CodeInternalError, "the bundle could not be unpacked", err)
	}
	return nil
}

func checkBundleExecutable(dest, executable string) error {
	info, err := os.Lstat(filepath.Join(dest, filepath.FromSlash(executable)))
	if err != nil {
		return errWrap(CodeVerificationFailed, "the declared desktop executable is not in the bundle", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o100 == 0 {
		return errf(CodeVerificationFailed, "the declared desktop executable is not a regular executable file")
	}
	return nil
}

// VerifyBundle checks an unpacked bundle against its archive: every archive
// entry is present with the same content, mode class and link target, and
// the directory holds nothing else.
func VerifyBundle(archivePath, dir string) error {
	if err := requireRealDir(dir); err != nil {
		return err
	}
	expected := map[string]bool{}
	var findings []Finding
	_, _, err := readBundle(archivePath, func(e bundleEntry, r io.Reader) error {
		expected[e.path] = true
		p := filepath.Join(dir, filepath.FromSlash(e.path))
		info, err := os.Lstat(p)
		if err != nil {
			findings = append(findings, Finding{Path: e.path, Problem: "bundle entry is missing"})
			_, _ = io.Copy(io.Discard, r)
			return nil
		}
		switch e.kind {
		case tar.TypeDir:
			if !info.IsDir() {
				findings = append(findings, Finding{Path: e.path, Problem: "bundle entry is not a directory"})
			}
		case tar.TypeSymlink:
			target, _ := os.Readlink(p)
			if info.Mode()&fs.ModeSymlink == 0 || filepath.ToSlash(target) != e.link {
				findings = append(findings, Finding{Path: e.path, Problem: "bundle link differs from the archive"})
			}
		default:
			if !info.Mode().IsRegular() {
				findings = append(findings, Finding{Path: e.path, Problem: "bundle entry is not a regular file"})
				_, _ = io.Copy(io.Discard, r)
				return nil
			}
			want := sha256.New()
			if _, err := io.Copy(want, r); err != nil {
				return err
			}
			got, _, err := hashFile(p)
			if err != nil {
				return err
			}
			if got != hex.EncodeToString(want.Sum(nil)) {
				findings = append(findings, Finding{Path: e.path, Problem: "bundle file differs from the archive"})
			}
			if (info.Mode()&0o100 != 0) != (e.mode&0o100 != 0) || info.Mode().Perm()&0o022 != 0 {
				findings = append(findings, Finding{Path: e.path, Problem: "bundle file permissions differ from the archive"})
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	walkErr := filepath.WalkDir(dir, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel != "." && !expected[filepath.ToSlash(rel)] {
			findings = append(findings, Finding{Path: filepath.ToSlash(rel), Problem: "entry is not in the bundle archive"})
		}
		return nil
	})
	if walkErr != nil {
		return errWrap(CodeInternalError, "the unpacked bundle could not be read", walkErr)
	}
	if len(findings) != 0 {
		sortFindings(findings)
		return errFindings("the unpacked bundle does not match its archive", findings)
	}
	return nil
}
