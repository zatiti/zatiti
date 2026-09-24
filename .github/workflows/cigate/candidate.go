package main

import (
	"context"
	"crypto/sha256"
	"debug/macho"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// candidate is evidence about unsigned build output. It is intentionally not
// a packaging manifest, signature, notarization record or release verdict.
type candidate struct {
	Schema               string       `json:"schema"`
	Kind                 string       `json:"kind"`
	Arch                 string       `json:"arch"`
	SourceRevision       string       `json:"source_revision"`
	ArchiveSHA256        string       `json:"archive_sha256"`
	ArchiveSize          int64        `json:"archive_size"`
	LicenseSHA256        string       `json:"license_sha256"`
	DependencyLockSHA256 string       `json:"dependency_lock_sha256"`
	PubspecLockSHA256    string       `json:"pubspec_lock_sha256,omitempty"`
	MachO                []candidateO `json:"mach_o"`
	Unsigned             bool         `json:"unsigned"`
}

type candidateO struct {
	Path   string   `json:"path"`
	CPUs   []string `json:"cpus"`
	SHA256 string   `json:"sha256"`
}

func runCandidate(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("candidate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("kind", "", "controller or desktop")
	arch := fs.String("arch", "", "native amd64 or arm64")
	root := fs.String("root", "", "controller executable or desktop .app directory")
	archive := fs.String("archive", "", "deterministic candidate archive")
	license := fs.String("license", "", "copied Apache license")
	dependencyLock := fs.String("dependency-lock", "", "copied dependency lock report")
	pubspecLock := fs.String("pubspec-lock", "", "copied Flutter pubspec.lock (desktop only)")
	revision := fs.String("revision", "", "source commit SHA")
	out := fs.String("out", "", "candidate evidence JSON")
	if err := fs.Parse(args); err != nil {
		return faultf(codeInvalidInput, "candidate flags: %v", err)
	}
	if fs.NArg() != 0 || (*kind != "controller" && *kind != "desktop") || (*arch != "amd64" && *arch != "arm64") || !isSHA(*revision) || *root == "" || *archive == "" || *license == "" || *dependencyLock == "" || (*kind == "desktop" && *pubspecLock == "") || (*kind == "controller" && *pubspecLock != "") || *out == "" {
		return faultf(codeInvalidInput, "candidate requires kind, arch, root, archive, license, dependency lock, revision and out; desktop also requires pubspec lock")
	}
	paths := []string{*root, *archive, *license, *dependencyLock, *out}
	if *pubspecLock != "" {
		paths = append(paths, *pubspecLock)
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return faultf(codeInvalidInput, "candidate paths must be clean absolute paths")
		}
	}
	record, err := auditCandidate(*kind, *arch, *root)
	if err != nil {
		return err
	}
	record.SourceRevision = *revision
	if record.ArchiveSHA256, record.ArchiveSize, err = hashFile(*archive); err != nil {
		return err
	}
	if record.LicenseSHA256, _, err = hashFile(*license); err != nil {
		return err
	}
	if record.DependencyLockSHA256, _, err = hashFile(*dependencyLock); err != nil {
		return err
	}
	if *pubspecLock != "" {
		if record.PubspecLockSHA256, _, err = hashFile(*pubspecLock); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "%s/%s unsigned candidate: %d Mach-O files, archive sha256 %s\n", record.Kind, record.Arch, len(record.MachO), record.ArchiveSHA256)
	return nil
}

func isSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func auditCandidate(kind, arch, root string) (candidate, error) {
	rec := candidate{Schema: "zatiti.ci.unsigned_candidate/v1", Kind: kind, Arch: arch, Unsigned: true, MachO: []candidateO{}}
	info, err := os.Lstat(root)
	if err != nil {
		return rec, err
	}
	if kind == "controller" {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return rec, faultf(codeVerificationFailed, "controller candidate is not an executable regular file")
		}
		m, err := auditMachO(root, arch)
		if err != nil {
			return rec, err
		}
		m.Path = "bin/zatiti"
		rec.MachO = append(rec.MachO, m)
		return rec, nil
	}
	if !info.IsDir() || filepath.Ext(root) != ".app" {
		return rec, faultf(codeInvalidInput, "desktop candidate must be a .app directory")
	}
	runner := filepath.Join(root, "Contents", "MacOS", "zatiti_desktop")
	runnerSeen := false
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil // deterministic archiver validates relative symlink targets
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return faultf(codeVerificationFailed, "desktop bundle contains a special file: %s", path)
		}
		magic, err := machoMagic(path)
		if err != nil {
			return err
		}
		mustBeMachO := path == runner || strings.HasSuffix(path, ".dylib") || strings.HasSuffix(path, ".so")
		if !magic && !mustBeMachO {
			return nil
		}
		m, err := auditMachO(path, arch)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		m.Path = filepath.ToSlash(rel)
		rec.MachO = append(rec.MachO, m)
		if path == runner {
			runnerSeen = info.Mode().Perm()&0o111 != 0
		}
		return nil
	})
	if err != nil {
		return rec, err
	}
	if !runnerSeen {
		return rec, faultf(codeVerificationFailed, "desktop runner is missing or not executable")
	}
	sort.Slice(rec.MachO, func(i, j int) bool { return rec.MachO[i].Path < rec.MachO[j].Path })
	return rec, nil
}

func machoMagic(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, nil
		}
		return false, err
	}
	return hasMachOMagic(magic), nil
}

func hasMachOMagic(magic [4]byte) bool {
	switch magic {
	case [4]byte{0xfe, 0xed, 0xfa, 0xce}, [4]byte{0xce, 0xfa, 0xed, 0xfe},
		[4]byte{0xfe, 0xed, 0xfa, 0xcf}, [4]byte{0xcf, 0xfa, 0xed, 0xfe},
		[4]byte{0xca, 0xfe, 0xba, 0xbe}, [4]byte{0xbe, 0xba, 0xfe, 0xca},
		[4]byte{0xca, 0xfe, 0xba, 0xbf}, [4]byte{0xbf, 0xba, 0xfe, 0xca}:
		return true
	}
	return false
}

func auditMachO(path, arch string) (candidateO, error) {
	var out candidateO
	want := macho.CpuAmd64
	if arch == "arm64" {
		want = macho.CpuArm64
	}
	fat, err := macho.OpenFat(path)
	if err == nil {
		defer func() { _ = fat.Close() }()
		found := false
		for _, a := range fat.Arches {
			out.CPUs = append(out.CPUs, a.Cpu.String())
			if a.Cpu == want {
				found = true
			}
		}
		if !found {
			return out, faultf(codeVerificationFailed, "Mach-O %s has no native %s slice", path, arch)
		}
	} else if errors.Is(err, macho.ErrNotFat) {
		thin, thinErr := macho.Open(path)
		if thinErr != nil {
			return out, faultf(codeVerificationFailed, "Mach-O %s is malformed: %v", path, thinErr)
		}
		defer func() { _ = thin.Close() }()
		out.CPUs = []string{thin.Cpu.String()}
		if thin.Cpu != want {
			return out, faultf(codeVerificationFailed, "Mach-O %s targets %s, need %s", path, thin.Cpu, arch)
		}
	} else {
		return out, faultf(codeVerificationFailed, "Mach-O %s is malformed: %v", path, err)
	}
	out.SHA256, _, err = hashFile(path)
	return out, err
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", 0, faultf(codeInvalidInput, "candidate input is not a regular file: %s", path)
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
