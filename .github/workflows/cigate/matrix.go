package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	maxCandidateJSON     = 1 << 20
	maxCandidateArchive  = 1 << 30
	maxCandidateUnpacked = 2 << 30
	maxCandidateEntry    = 512 << 20
	maxCandidateEntries  = 50000
)

type candidateMatrix struct {
	Schema         string      `json:"schema"`
	SourceRevision string      `json:"source_revision"`
	Unsigned       bool        `json:"unsigned"`
	Components     []candidate `json:"components"`
}

func runCandidateMatrix(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("candidate-matrix", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "", "directory containing the four named downloaded artifacts")
	revision := fs.String("revision", "", "checked-out commit SHA")
	license := fs.String("license", "", "checked-out Apache license")
	lock := fs.String("dependency-lock", "", "checked-out dependency lock")
	pubspec := fs.String("pubspec-lock", "", "checked-out Flutter pubspec.lock")
	out := fs.String("out", "", "unsigned matrix evidence JSON")
	if err := fs.Parse(args); err != nil {
		return faultf(codeInvalidInput, "candidate-matrix flags: %v", err)
	}
	if fs.NArg() != 0 || !isSHA(*revision) || *root == "" || *license == "" || *lock == "" || *pubspec == "" || *out == "" {
		return faultf(codeInvalidInput, "candidate-matrix requires root, revision, license, dependency-lock, pubspec-lock and out")
	}
	for _, p := range []string{*root, *license, *lock, *pubspec, *out} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return faultf(codeInvalidInput, "candidate-matrix paths must be clean absolute paths")
		}
	}
	licHash, _, err := hashFile(*license)
	if err != nil {
		return err
	}
	lockHash, _, err := hashFile(*lock)
	if err != nil {
		return err
	}
	pubHash, _, err := hashFile(*pubspec)
	if err != nil {
		return err
	}
	index, err := verifyCandidateMatrix(*root, *revision, licHash, lockHash, pubHash)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	if len(raw) > maxCandidateJSON {
		return faultf(codeVerificationFailed, "candidate matrix evidence is too large")
	}
	if err := os.WriteFile(*out, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "unsigned Mac candidate matrix: four native components from %s verified\n", *revision)
	return nil
}

func verifyCandidateMatrix(root, revision, licenseHash, lockHash, pubHash string) (candidateMatrix, error) {
	index := candidateMatrix{Schema: "zatiti.ci.unsigned_candidate_matrix/v1", SourceRevision: revision, Unsigned: true, Components: []candidate{}}
	items := []struct{ dir, kind, arch string }{
		{"controller-amd64", "controller", "amd64"}, {"desktop-amd64", "desktop", "amd64"},
		{"controller-arm64", "controller", "arm64"}, {"desktop-arm64", "desktop", "arm64"},
	}
	children, err := os.ReadDir(root)
	if err != nil {
		return index, err
	}
	if len(children) != len(items) {
		return index, faultf(codeVerificationFailed, "candidate matrix needs exactly four downloaded artifacts")
	}
	known := map[string]bool{}
	for _, item := range items {
		known[item.dir] = true
	}
	for _, child := range children {
		if !child.IsDir() || !known[child.Name()] {
			return index, faultf(codeVerificationFailed, "candidate matrix contains an unexpected or duplicate artifact: %s", child.Name())
		}
	}
	for _, item := range items {
		dir := filepath.Join(root, item.dir)
		if err := checkDownloadedTree(dir); err != nil {
			return index, err
		}
		base := filepath.Join(dir, "candidate")
		if err := requireCandidateLayout(base, item.kind, item.arch); err != nil {
			return index, err
		}
		c, err := readCandidate(filepath.Join(base, "candidate.json"))
		if err != nil {
			return index, err
		}
		if c.Schema != "zatiti.ci.unsigned_candidate/v1" || !c.Unsigned || c.Kind != item.kind || c.Arch != item.arch || c.SourceRevision != revision || c.LicenseSHA256 != licenseHash || c.DependencyLockSHA256 != lockHash || (item.kind == "desktop" && c.PubspecLockSHA256 != pubHash) || (item.kind == "controller" && c.PubspecLockSHA256 != "") {
			return index, faultf(codeVerificationFailed, "candidate %s metadata, source, or notices mismatch", item.dir)
		}
		for name, want := range map[string]string{"LICENSE": licenseHash, "dependencies.lock.json": lockHash} {
			got, _, err := hashFile(filepath.Join(base, name))
			if err != nil || got != want {
				return index, faultf(codeVerificationFailed, "candidate %s %s differs from source", item.dir, name)
			}
		}
		if item.kind == "desktop" {
			got, _, err := hashFile(filepath.Join(base, "pubspec.lock"))
			if err != nil || got != pubHash {
				return index, faultf(codeVerificationFailed, "candidate %s pubspec.lock differs from source", item.dir)
			}
		}
		archive := filepath.Join(base, "zatiti-unsigned-darwin-"+item.arch+"-"+item.kind+".tar.gz")
		got, size, err := hashFile(archive)
		if err != nil {
			return index, err
		}
		if size <= 0 || size > maxCandidateArchive || got != c.ArchiveSHA256 || size != c.ArchiveSize {
			return index, faultf(codeVerificationFailed, "candidate %s archive hash or size mismatch", item.dir)
		}
		if err := verifyCandidateArchive(archive, c); err != nil {
			return index, fmt.Errorf("candidate %s: %w", item.dir, err)
		}
		if item.kind == "controller" {
			for _, p := range []string{filepath.Join(base, "zatiti"), filepath.Join(base, "controller-tree", "bin", "zatiti")} {
				got, _, err := hashFile(p)
				if err != nil || got != c.MachO[0].SHA256 {
					return index, faultf(codeVerificationFailed, "candidate %s controller binary differs from archive", item.dir)
				}
			}
		}
		index.Components = append(index.Components, c)
	}
	return index, nil
}

func requireCandidateLayout(base, kind, arch string) error {
	want := map[string]bool{"candidate.json": true, "LICENSE": true, "dependencies.lock.json": true,
		"zatiti-unsigned-darwin-" + arch + "-" + kind + ".tar.gz": true}
	if kind == "desktop" {
		want["pubspec.lock"] = true
	} else {
		want["zatiti"] = true
		want["controller-tree"] = true
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	if len(entries) != len(want) {
		return faultf(codeVerificationFailed, "candidate %s/%s has missing or extra files", kind, arch)
	}
	for _, e := range entries {
		if !want[e.Name()] {
			return faultf(codeVerificationFailed, "candidate %s/%s has unexpected file %s", kind, arch, e.Name())
		}
	}
	if kind == "controller" {
		for _, p := range []string{filepath.Join(base, "controller-tree"), filepath.Join(base, "controller-tree", "bin")} {
			entries, err := os.ReadDir(p)
			if err != nil {
				return err
			}
			if (strings.HasSuffix(p, "controller-tree") && (len(entries) != 2 || entries[0].Name() != "LICENSE" || entries[1].Name() != "bin")) || (strings.HasSuffix(p, "bin") && (len(entries) != 1 || entries[0].Name() != "zatiti")) {
				return faultf(codeVerificationFailed, "controller candidate tree has unexpected files")
			}
		}
	}
	return nil
}

func readCandidate(p string) (candidate, error) {
	var c candidate
	info, err := os.Lstat(p)
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxCandidateJSON {
		return c, faultf(codeVerificationFailed, "candidate metadata is missing, nonregular or oversized")
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return c, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, faultf(codeVerificationFailed, "candidate metadata is malformed: %v", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return c, faultf(codeVerificationFailed, "candidate metadata has trailing content")
	}
	return c, nil
}

func checkDownloadedTree(root string) error {
	count := 0
	var total int64
	return filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		count++
		if count > maxCandidateEntries {
			return faultf(codeVerificationFailed, "downloaded candidate contains too many paths")
		}
		if e.Type()&os.ModeSymlink != 0 {
			return faultf(codeVerificationFailed, "downloaded candidate contains a symlink: %s", p)
		}
		if !e.IsDir() && !e.Type().IsRegular() {
			return faultf(codeVerificationFailed, "downloaded candidate contains a special file: %s", p)
		}
		if !e.IsDir() {
			info, err := e.Info()
			if err != nil {
				return err
			}
			if info.Size() < 0 || info.Size() > maxCandidateArchive || total > maxCandidateUnpacked-info.Size() {
				return faultf(codeVerificationFailed, "downloaded candidate exceeds file or total size bounds")
			}
			total += info.Size()
		}
		return nil
	})
}

func verifyCandidateArchive(p string, c candidate) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return faultf(codeVerificationFailed, "candidate archive gzip is malformed: %v", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	actual := []candidateO{}
	var previous string
	var total int64
	entries := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return faultf(codeVerificationFailed, "candidate tar is malformed: %v", err)
		}
		entries++
		if entries > maxCandidateEntries {
			return faultf(codeVerificationFailed, "candidate archive has too many entries")
		}
		name := strings.TrimSuffix(h.Name, "/")
		if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || seen[name] || (previous != "" && name <= previous) {
			return faultf(codeVerificationFailed, "candidate archive has an unsafe, repeated or unsorted path: %s", h.Name)
		}
		seen[name] = true
		previous = name
		if h.Size < 0 || h.Size > maxCandidateEntry || total > maxCandidateUnpacked-h.Size {
			return faultf(codeVerificationFailed, "candidate archive exceeds size bounds")
		}
		total += h.Size
		switch h.Typeflag {
		case tar.TypeDir:
			if h.Size != 0 || !strings.HasSuffix(h.Name, "/") {
				return faultf(codeVerificationFailed, "candidate directory entry is malformed")
			}
		case tar.TypeSymlink:
			if h.Size != 0 || path.IsAbs(h.Linkname) || strings.Contains(h.Linkname, "\\") || h.Linkname == "" || path.Clean(path.Join(path.Dir(name), h.Linkname)) == ".." || strings.HasPrefix(path.Clean(path.Join(path.Dir(name), h.Linkname)), "../") {
				return faultf(codeVerificationFailed, "candidate symlink escapes archive")
			}
		case tar.TypeReg:
			if err := verifyCandidateEntry(tr, h, name, c.Arch, &actual); err != nil {
				return err
			}
		default:
			return faultf(codeVerificationFailed, "candidate archive contains unsupported tar type")
		}
	}
	if c.Kind == "controller" && (!seen["bin/zatiti"] || !seen["LICENSE"]) {
		return faultf(codeVerificationFailed, "controller archive lacks binary or license")
	}
	if c.Kind == "desktop" && !seen["Contents/MacOS/zatiti_desktop"] {
		return faultf(codeVerificationFailed, "desktop archive lacks runner")
	}
	if len(actual) != len(c.MachO) {
		return faultf(codeVerificationFailed, "candidate Mach-O count differs from archive")
	}
	for i := range actual {
		if actual[i].Path != c.MachO[i].Path || actual[i].SHA256 != c.MachO[i].SHA256 || strings.Join(actual[i].CPUs, ",") != strings.Join(c.MachO[i].CPUs, ",") {
			return faultf(codeVerificationFailed, "candidate Mach-O evidence differs at %s", actual[i].Path)
		}
	}
	return nil
}

func verifyCandidateEntry(r io.Reader, h *tar.Header, name, arch string, actual *[]candidateO) error {
	var first [4]byte
	n, err := io.ReadFull(r, first[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return err
	}
	if int64(n) > h.Size {
		return faultf(codeVerificationFailed, "candidate entry size mismatch")
	}
	reader := io.MultiReader(bytes.NewReader(first[:n]), io.LimitReader(r, h.Size-int64(n)))
	mach := hasMachOMagic(first) || name == "bin/zatiti" || name == "Contents/MacOS/zatiti_desktop" || strings.HasSuffix(name, ".dylib") || strings.HasSuffix(name, ".so")
	if !mach {
		copied, err := io.Copy(io.Discard, reader)
		if err != nil || copied != h.Size {
			return faultf(codeVerificationFailed, "candidate entry is truncated")
		}
		return nil
	}
	if h.Mode&0o111 == 0 && (name == "bin/zatiti" || name == "Contents/MacOS/zatiti_desktop") {
		return faultf(codeVerificationFailed, "candidate executable lost its mode")
	}
	tmp, err := os.CreateTemp("", "zt-matrix-macho-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	copied, err := io.Copy(tmp, reader)
	if err != nil || copied != h.Size {
		_ = tmp.Close()
		return faultf(codeVerificationFailed, "candidate Mach-O entry is truncated")
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	m, err := auditMachO(tmp.Name(), arch)
	if err != nil {
		return err
	}
	m.Path = name
	*actual = append(*actual, m)
	return nil
}
