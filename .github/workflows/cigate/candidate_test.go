package main

import (
	"bytes"
	"context"
	"debug/macho"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func thinMachO(cpu macho.Cpu) []byte {
	b := make([]byte, 32)
	binary.LittleEndian.PutUint32(b[0:], 0xfeedfacf)
	binary.LittleEndian.PutUint32(b[4:], uint32(cpu))
	binary.LittleEndian.PutUint32(b[8:], 3)
	binary.LittleEndian.PutUint32(b[12:], 2)
	return b
}

func TestCandidateEvidenceBindsNativeFilesAndLocks(t *testing.T) {
	dir := t.TempDir()
	controller := filepath.Join(dir, "zatiti")
	putMachO(t, controller, macho.CpuAmd64)
	archive := filepath.Join(dir, "controller.tar.gz")
	license := filepath.Join(dir, "LICENSE")
	lock := filepath.Join(dir, "dependencies.lock.json")
	out := filepath.Join(dir, "candidate.json")
	for path, data := range map[string]string{archive: "archive", license: "Apache-2.0", lock: "lock"} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"-kind", "controller", "-arch", "amd64", "-root", controller, "-archive", archive, "-license", license, "-dependency-lock", lock, "-revision", strings.Repeat("a", 40), "-out", out}
	if err := runCandidate(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var evidence candidate
	if err := json.Unmarshal(raw, &evidence); err != nil {
		t.Fatal(err)
	}
	if !evidence.Unsigned || evidence.ArchiveSize != int64(len("archive")) || evidence.LicenseSHA256 == "" || evidence.DependencyLockSHA256 == "" || len(evidence.MachO) != 1 || evidence.MachO[0].Path != "bin/zatiti" {
		t.Fatalf("incomplete unsigned candidate evidence: %+v", evidence)
	}
	args[3] = "arm64"
	if err := runCandidate(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("wrong-architecture candidate accepted")
	}
}

func putMachO(t *testing.T, path string, cpu macho.Cpu) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, thinMachO(cpu), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestCandidateAuditsEveryNestedMachO(t *testing.T) {
	root := filepath.Join(t.TempDir(), "zatiti_desktop.app")
	runner := filepath.Join(root, "Contents", "MacOS", "zatiti_desktop")
	lib := filepath.Join(root, "Contents", "Frameworks", "libswiftTest.dylib")
	putMachO(t, runner, macho.CpuArm64)
	putMachO(t, lib, macho.CpuArm64)
	rec, err := auditCandidate("desktop", "arm64", root)
	if err != nil || len(rec.MachO) != 2 || rec.MachO[0].Path != "Contents/Frameworks/libswiftTest.dylib" {
		t.Fatalf("native candidate = %+v, %v", rec, err)
	}
	putMachO(t, lib, macho.CpuAmd64)
	if _, err := auditCandidate("desktop", "arm64", root); err == nil || !strings.Contains(err.Error(), "need arm64") {
		t.Fatalf("Intel-only nested dylib accepted for arm64: %v", err)
	}
	if _, err := auditCandidate("desktop", "amd64", root); err == nil || !strings.Contains(err.Error(), "need amd64") {
		t.Fatalf("arm64-only runner accepted for amd64: %v", err)
	}
}

func TestCandidateRequiresExecutableRunnerAndParsesMachO(t *testing.T) {
	root := filepath.Join(t.TempDir(), "zatiti_desktop.app")
	runner := filepath.Join(root, "Contents", "MacOS", "zatiti_desktop")
	putMachO(t, runner, macho.CpuAmd64)
	if _, err := auditCandidate("desktop", "amd64", root); err != nil {
		t.Fatalf("native desktop: %v", err)
	}
	if err := os.Chmod(runner, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := auditCandidate("desktop", "amd64", root); err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("non-executable runner accepted: %v", err)
	}
	controller := filepath.Join(t.TempDir(), "zatiti")
	putMachO(t, controller, macho.CpuAmd64)
	if _, err := auditCandidate("controller", "amd64", controller); err != nil {
		t.Fatalf("native controller: %v", err)
	}
	if err := os.WriteFile(controller, []byte("not a Mach-O"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := auditCandidate("controller", "amd64", controller); err == nil {
		t.Fatal("malformed controller accepted")
	}
}
