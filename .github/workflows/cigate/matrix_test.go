package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/macho"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type matrixTestEntry struct {
	name string
	data []byte
	mode int64
	kind byte
	link string
}

func matrixArchive(t *testing.T, p string, entries []matrixTestEntry) {
	t.Helper()
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Mode: e.mode, Typeflag: e.kind, Size: int64(len(e.data)), Linkname: e.link, Format: tar.FormatPAX}
		if e.kind == tar.TypeDir {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if len(e.data) > 0 {
			if _, err := tw.Write(e.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func matrixDigest(b []byte) string { d := sha256.Sum256(b); return hex.EncodeToString(d[:]) }
func matrixWrite(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

type matrixFixture struct {
	root, revision     string
	license, lock, pub []byte
}

func newMatrixFixture(t *testing.T) matrixFixture {
	t.Helper()
	m := matrixFixture{root: t.TempDir(), revision: strings.Repeat("a", 40), license: []byte("Apache-2.0"), lock: []byte("dependency-lock"), pub: []byte("pubspec-lock")}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, kind := range []string{"controller", "desktop"} {
			m.component(t, arch, kind)
		}
	}
	return m
}

func (m matrixFixture) component(t *testing.T, arch, kind string) {
	t.Helper()
	base := filepath.Join(m.root, kind+"-"+arch, "candidate")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	matrixWrite(t, filepath.Join(base, "LICENSE"), m.license)
	matrixWrite(t, filepath.Join(base, "dependencies.lock.json"), m.lock)
	if kind == "desktop" {
		matrixWrite(t, filepath.Join(base, "pubspec.lock"), m.pub)
	}
	cpu := macho.CpuAmd64
	if arch == "arm64" {
		cpu = macho.CpuArm64
	}
	binary := thinMachO(cpu)
	runner := "bin/zatiti"
	entries := []matrixTestEntry{}
	if kind == "controller" {
		entries = []matrixTestEntry{{"LICENSE", m.license, 0o644, tar.TypeReg, ""}, {"bin/", nil, 0o755, tar.TypeDir, ""}, {runner, binary, 0o755, tar.TypeReg, ""}}
		matrixWrite(t, filepath.Join(base, "zatiti"), binary)
		matrixWrite(t, filepath.Join(base, "controller-tree", "LICENSE"), m.license)
		matrixWrite(t, filepath.Join(base, "controller-tree", "bin", "zatiti"), binary)
	} else {
		runner = "Contents/MacOS/zatiti_desktop"
		entries = []matrixTestEntry{{"Contents/", nil, 0o755, tar.TypeDir, ""}, {"Contents/MacOS/", nil, 0o755, tar.TypeDir, ""}, {runner, binary, 0o755, tar.TypeReg, ""}}
	}
	archive := filepath.Join(base, "zatiti-unsigned-darwin-"+arch+"-"+kind+".tar.gz")
	matrixArchive(t, archive, entries)
	digest, size, err := hashFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	c := candidate{Schema: "zatiti.ci.unsigned_candidate/v1", Kind: kind, Arch: arch, SourceRevision: m.revision, ArchiveSHA256: digest, ArchiveSize: size, LicenseSHA256: matrixDigest(m.license), DependencyLockSHA256: matrixDigest(m.lock), MachO: []candidateO{{Path: runner, CPUs: []string{cpu.String()}, SHA256: matrixDigest(binary)}}, Unsigned: true}
	if kind == "desktop" {
		c.PubspecLockSHA256 = matrixDigest(m.pub)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	matrixWrite(t, filepath.Join(base, "candidate.json"), raw)
}

func TestCandidateMatrixAcceptsFourNativeArchives(t *testing.T) {
	m := newMatrixFixture(t)
	index, err := verifyCandidateMatrix(m.root, m.revision, matrixDigest(m.license), matrixDigest(m.lock), matrixDigest(m.pub))
	if err != nil || len(index.Components) != 4 || !index.Unsigned {
		t.Fatalf("matrix = %+v, %v", index, err)
	}
}

func TestCandidateMatrixWritesBoundedUnsignedIndex(t *testing.T) {
	m := newMatrixFixture(t)
	source := t.TempDir()
	license := filepath.Join(source, "LICENSE")
	lock := filepath.Join(source, "dependencies.lock.json")
	pub := filepath.Join(source, "pubspec.lock")
	out := filepath.Join(source, "matrix.json")
	matrixWrite(t, license, m.license)
	matrixWrite(t, lock, m.lock)
	matrixWrite(t, pub, m.pub)
	args := []string{"-root", m.root, "-revision", m.revision, "-license", license, "-dependency-lock", lock, "-pubspec-lock", pub, "-out", out}
	if err := runCandidateMatrix(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil || len(raw) > maxCandidateJSON {
		t.Fatalf("matrix index size/read = %d, %v", len(raw), err)
	}
	var index candidateMatrix
	if err := json.Unmarshal(raw, &index); err != nil || index.Schema != "zatiti.ci.unsigned_candidate_matrix/v1" || !index.Unsigned || len(index.Components) != 4 {
		t.Fatalf("matrix index = %+v, %v", index, err)
	}
}

func TestCandidateMatrixRejectsMissingDuplicateMismatchedAndTampered(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, matrixFixture)
	}{
		{"missing architecture", func(t *testing.T, m matrixFixture) {
			if err := os.Rename(filepath.Join(m.root, "desktop-arm64"), filepath.Join(m.root, "absent")); err != nil {
				t.Fatal(err)
			}
		}},
		{"duplicate component", func(t *testing.T, m matrixFixture) {
			p := filepath.Join(m.root, "desktop-arm64", "candidate", "candidate.json")
			var c candidate
			b, _ := os.ReadFile(p)
			_ = json.Unmarshal(b, &c)
			c.Kind = "controller"
			b, _ = json.Marshal(c)
			matrixWrite(t, p, b)
		}},
		{"source mismatch", func(t *testing.T, m matrixFixture) {
			p := filepath.Join(m.root, "controller-amd64", "candidate", "candidate.json")
			var c candidate
			b, _ := os.ReadFile(p)
			_ = json.Unmarshal(b, &c)
			c.SourceRevision = strings.Repeat("b", 40)
			b, _ = json.Marshal(c)
			matrixWrite(t, p, b)
		}},
		{"archive tamper", func(t *testing.T, m matrixFixture) {
			p := filepath.Join(m.root, "desktop-amd64", "candidate", "zatiti-unsigned-darwin-amd64-desktop.tar.gz")
			f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
			_, _ = f.Write([]byte("tamper"))
			_ = f.Close()
		}},
		{"copied lock tamper", func(t *testing.T, m matrixFixture) {
			matrixWrite(t, filepath.Join(m.root, "desktop-amd64", "candidate", "pubspec.lock"), []byte("other"))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newMatrixFixture(t)
			tc.mutate(t, m)
			if _, err := verifyCandidateMatrix(m.root, m.revision, matrixDigest(m.license), matrixDigest(m.lock), matrixDigest(m.pub)); err == nil {
				t.Fatal("invalid matrix accepted")
			}
		})
	}
}

func TestCandidateArchiveRejectsWrongSliceAndUnsafePaths(t *testing.T) {
	m := newMatrixFixture(t)
	base := filepath.Join(m.root, "desktop-arm64", "candidate")
	archive := filepath.Join(base, "zatiti-unsigned-darwin-arm64-desktop.tar.gz")
	b, _ := os.ReadFile(filepath.Join(base, "candidate.json"))
	var c candidate
	_ = json.Unmarshal(b, &c)
	for _, tc := range []struct {
		name    string
		entries []matrixTestEntry
	}{
		{"wrong slice", []matrixTestEntry{{"Contents/", nil, 0o755, tar.TypeDir, ""}, {"Contents/MacOS/", nil, 0o755, tar.TypeDir, ""}, {"Contents/MacOS/zatiti_desktop", thinMachO(macho.CpuAmd64), 0o755, tar.TypeReg, ""}}},
		{"traversal", []matrixTestEntry{{"../escape", []byte("x"), 0o644, tar.TypeReg, ""}}},
		{"absolute", []matrixTestEntry{{"/escape", []byte("x"), 0o644, tar.TypeReg, ""}}},
		{"escaping symlink", []matrixTestEntry{{"Contents/", nil, 0o755, tar.TypeDir, ""}, {"Contents/escape", nil, 0o777, tar.TypeSymlink, "../../outside"}}},
		{"duplicate", []matrixTestEntry{{"Contents/", nil, 0o755, tar.TypeDir, ""}, {"Contents/", nil, 0o755, tar.TypeDir, ""}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			matrixArchive(t, archive, tc.entries)
			if err := verifyCandidateArchive(archive, c); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}

func TestCandidateMatrixRejectsSymlinkedDownload(t *testing.T) {
	m := newMatrixFixture(t)
	base := filepath.Join(m.root, "desktop-arm64", "candidate")
	if err := os.Symlink(filepath.Join(base, "candidate.json"), filepath.Join(base, "extra.json")); err != nil {
		t.Fatal(err)
	}
	if err := checkDownloadedTree(filepath.Join(m.root, "desktop-arm64")); err == nil {
		t.Fatal("download symlink accepted")
	}
}
