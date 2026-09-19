package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	fixtureMod = `module example.invalid/m

go 1.26.0

toolchain go1.26.2

require (
	example.invalid/a v1.2.3
	example.invalid/b v0.4.0 // indirect
)

require example.invalid/c v2.0.0
`
	fixtureSum = `example.invalid/a v1.2.3 h1:aaaa=
example.invalid/a v1.2.3/go.mod h1:amod=
example.invalid/c v2.0.0 h1:cccc=
`
	fixtureLock = `{"go_directive":"1.26.0","toolchain":"go1.26.2","direct_modules":[
 {"module":"example.invalid/a","version":"v1.2.3","sum":"h1:aaaa="},
 {"module":"example.invalid/c","version":"v2.0.0","sum":"h1:cccc="}]}`
)

func TestCheckLock(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                 string
		goVersion            string
		mod, sum, lock, want string
	}{
		{"agreement", "go1.26.2", fixtureMod, fixtureSum, fixtureLock, ""},
		{"other toolchain running", "go1.26.0", fixtureMod, fixtureSum, fixtureLock, "running toolchain"},
		{"go.mod toolchain moved", "go1.26.3", strings.Replace(fixtureMod, "go1.26.2", "go1.26.3", 1), fixtureSum, fixtureLock, "lock toolchain"},
		{"go directive moved", "go1.26.2", strings.Replace(fixtureMod, "go 1.26.0", "go 1.26.1", 1), fixtureSum, fixtureLock, "go_directive"},
		{"module bumped without the lock", "go1.26.2", strings.Replace(fixtureMod, "a v1.2.3", "a v1.2.4", 1), fixtureSum, fixtureLock, "lock pins"},
		{"module dropped from go.mod", "go1.26.2", strings.Replace(fixtureMod, "require example.invalid/c v2.0.0\n", "", 1), fixtureSum, fixtureLock, "lock pins"},
		{"checksum differs", "go1.26.2", fixtureMod, strings.Replace(fixtureSum, "h1:aaaa=", "h1:zzzz=", 1), fixtureLock, "lock checksum"},
		{"checksum absent", "go1.26.2", fixtureMod, "", fixtureLock, "lock checksum"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			problems, err := checkLock(tc.goVersion, []byte(tc.mod), []byte(tc.sum), []byte(tc.lock))
			if err != nil {
				t.Fatalf("checkLock: %v", err)
			}
			joined := strings.Join(problems, "|")
			if (tc.want == "") != (len(problems) == 0) || !strings.Contains(joined, tc.want) {
				t.Fatalf("problems = %v, want %q", problems, tc.want)
			}
		})
	}
	for name, src := range map[string][3]string{
		"unpinned toolchain": {strings.Replace(fixtureMod, "toolchain go1.26.2\n", "", 1), fixtureSum, fixtureLock},
		"invalid lock":       {fixtureMod, fixtureSum, "{"},
		"empty lock":         {fixtureMod, fixtureSum, "{}"},
	} {
		if _, err := checkLock("go1.26.2", []byte(src[0]), []byte(src[1]), []byte(src[2])); faultCode(err) != codePrerequisiteMissing {
			t.Errorf("%s: error = %v, want %s", name, err, codePrerequisiteMissing)
		}
	}
}

// TestRepositoryLockAgrees runs the lock check against the real module files
// with the toolchain that is running the tests.
func TestRepositoryLockAgrees(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"lock", "-root", root, "-go-version", runtime.Version()}, &stdout, &stderr); code != 0 {
		t.Fatalf("lock check failed (%d):\n%s%s", code, stdout.String(), stderr.String())
	}
	if code := run(context.Background(), []string{"lock", "-root", root, "-go-version", "go1.0"}, &stdout, &stderr); code != 1 {
		t.Fatalf("a foreign toolchain passed the lock check (exit %d)", code)
	}
	if code := run(context.Background(), []string{"notices", "-license", filepath.Join(root, "LICENSE")}, &stdout, &stderr); code != 0 {
		t.Fatalf("the repository license failed the notice check: %s", stderr.String())
	}
}

func TestDriftDetectsGeneratedChanges(t *testing.T) {
	t.Parallel()
	dir, _ := gitRepo(t)
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"drift", "-dir", dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("clean tree: exit %d; %s", code, stderr.String())
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("regenerated differently\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new-generated.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := run(context.Background(), []string{"drift", "-dir", dir}, &stdout, &stderr); code != 1 {
		t.Fatalf("drifted tree: exit %d, want 1", code)
	}
	for _, want := range []string{"tracked.txt", "new-generated.md"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("drift report %q does not name %s", stdout.String(), want)
		}
	}
	if code := run(context.Background(), []string{"drift", "-dir", t.TempDir()}, &stdout, &stderr); code != 5 {
		t.Errorf("not a repository: exit %d, want 5", code)
	}
}

func TestRequirePackage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("spec-only/AGENTS.md", "# assignment\n")
	write("lib/lib.go", "package lib\n")
	write("lib/helper_test.go", "package lib\n\nfunc helper() {}\n")
	write("cmd/app/main.go", "// Command app.\npackage main\n\nfunc main() {}\n")
	write("cmd/testonly/main_test.go", "package main\n")
	write("suite/deep/q_test.go", "package q_test\n\nimport \"testing\"\n\nfunc TestQualifies(t *testing.T) {}\n")

	cases := []struct {
		kind, dir, code string
	}{
		{"main", "cmd/app", ""},
		{"main", "spec-only", codePrerequisiteMissing},
		{"main", "lib", codePrerequisiteMissing},
		{"main", "cmd/testonly", codePrerequisiteMissing},
		{"main", "absent", codePrerequisiteMissing},
		{"tests", "suite", ""},
		{"tests", "spec-only", codePrerequisiteMissing},
		{"tests", "lib", codePrerequisiteMissing},
		{"tests", "absent", codePrerequisiteMissing},
		{"other", "suite", codeInvalidInput},
	}
	for _, tc := range cases {
		if err := requirePackage(tc.kind, filepath.Join(dir, tc.dir)); faultCode(err) != tc.code {
			t.Errorf("requirePackage(%s, %s) = %v, want code %q", tc.kind, tc.dir, err, tc.code)
		}
	}
}

func TestNoticesRejectsAModifiedLicense(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "LICENSE")
	if err := os.WriteFile(path, []byte("Apache License, Version 2.0, with changes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"notices", "-license", path}, &stdout, &stderr); code != 1 {
		t.Errorf("modified license: exit %d, want 1", code)
	}
	if code := run(context.Background(), []string{"notices", "-license", path + ".absent"}, &stdout, &stderr); code != 5 {
		t.Errorf("absent license: exit %d, want 5", code)
	}
}

func TestEnvironRecordsOnlyAllowlistedVariables(t *testing.T) {
	t.Setenv("GITHUB_SHA", "0123456789abcdef")
	t.Setenv("SYNTHETIC_CREDENTIAL", "synthetic-value-that-must-not-be-recorded")
	out := filepath.Join(t.TempDir(), "environment.json")
	root := filepath.Join("..", "..", "..")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"environ", "-root", root, "-go-version", runtime.Version(), "-out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("environ: exit %d; %s", code, stderr.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "synthetic-value") || strings.Contains(string(data), "SYNTHETIC_CREDENTIAL") {
		t.Error("evidence recorded a variable outside the allowlist")
	}
	var rec struct {
		Environment map[string]string `json:"environment"`
		SHA256      map[string]string `json:"sha256"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Environment["GITHUB_SHA"] != "0123456789abcdef" || len(rec.SHA256["go.sum"]) != 64 {
		t.Errorf("evidence = %+v", rec)
	}
}

// TestProvenance builds a real binary from a real commit and checks that
// provenance accepts it only for that commit, toolchain and a clean tree.
func TestProvenance(t *testing.T) {
	t.Parallel()
	dir, runGit := gitRepo(t)
	for name, content := range map[string]string{
		"go.mod":  "module example.invalid/app\n\ngo 1.26.0\n",
		"main.go": "package main\n\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit("add", ".")
	runGit("commit", "-q", "-m", "app")
	revision := runGit("rev-parse", "HEAD")
	outDir := t.TempDir()
	build := func(name string, flags ...string) string {
		t.Helper()
		bin := filepath.Join(outDir, name)
		cmd := exec.Command("go", append(append([]string{"build"}, flags...), "-o", bin, ".")...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build: %v\n%s", err, out)
		}
		return bin
	}
	clean := build("clean", "-trimpath", "-buildvcs=true")
	untrimmed := build("untrimmed", "-buildvcs=true")
	unstamped := build("unstamped", "-trimpath", "-buildvcs=false")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() { println() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty := build("dirty", "-trimpath", "-buildvcs=true")

	p, err := inspectBinary(clean, runtime.Version(), revision)
	if err != nil {
		t.Fatalf("clean binary: %v", err)
	}
	if len(p.SHA256) != 64 || p.Size == 0 || p.Settings["vcs.revision"] != revision {
		t.Errorf("provenance = %+v", p)
	}
	cases := []struct{ name, bin, goVersion, revision, code string }{
		{"other revision", clean, runtime.Version(), strings.Repeat("0", 40), codeVerificationFailed},
		{"other toolchain", clean, "go1.0", revision, codeVerificationFailed},
		{"modified tree", dirty, runtime.Version(), revision, codeVerificationFailed},
		{"without trimpath", untrimmed, runtime.Version(), revision, codeVerificationFailed},
		{"without vcs stamp", unstamped, runtime.Version(), revision, codeVerificationFailed},
		{"not a Go binary", filepath.Join(dir, "main.go"), runtime.Version(), revision, codePrerequisiteMissing},
	}
	for _, tc := range cases {
		if _, err := inspectBinary(tc.bin, tc.goVersion, tc.revision); faultCode(err) != tc.code {
			t.Errorf("%s: error = %v, want %s", tc.name, err, tc.code)
		}
	}

	evidence := filepath.Join(outDir, "provenance.json")
	var stdout, stderr bytes.Buffer
	args := []string{"provenance", "-go-version", runtime.Version(), "-revision", revision, "-out", evidence, clean, dirty}
	if code := run(context.Background(), args, &stdout, &stderr); code != 1 {
		t.Fatalf("provenance with a dirty binary: exit %d, want 1", code)
	}
	data, err := os.ReadFile(evidence)
	if err != nil || !strings.Contains(string(data), `"verified": false`) {
		t.Fatalf("failing provenance evidence was not retained: %v\n%s", err, data)
	}
}
