package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

const (
	environSchema    = "zatiti.ci.environment/v1"
	provenanceSchema = "zatiti.ci.build_provenance/v1"
	// apacheLicenseSHA256 is the digest of the canonical Apache License 2.0
	// text (LICENSE-2.0.txt as published by the Apache Software Foundation).
	apacheLicenseSHA256 = "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30"
	maxListedPaths      = 50
)

// goMod is the part of go.mod the lock check compares.
type goMod struct {
	Go        string
	Toolchain string
	Require   map[string]string
}

func parseGoMod(src []byte) (goMod, error) {
	mod := goMod{Require: map[string]string{}}
	inRequire := false
	sc := bufio.NewScanner(bytes.NewReader(src))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		switch {
		case len(f) == 0:
		case inRequire && f[0] == ")":
			inRequire = false
		case inRequire && len(f) == 2:
			mod.Require[f[0]] = f[1]
		case f[0] == "require" && len(f) == 2 && f[1] == "(":
			inRequire = true
		case f[0] == "require" && len(f) == 3:
			mod.Require[f[1]] = f[2]
		case f[0] == "go" && len(f) == 2:
			mod.Go = f[1]
		case f[0] == "toolchain" && len(f) == 2:
			mod.Toolchain = f[1]
		}
	}
	if err := sc.Err(); err != nil {
		return mod, fmt.Errorf("reading go.mod: %w", err)
	}
	if mod.Go == "" || mod.Toolchain == "" {
		return mod, faultf(codePrerequisiteMissing, "go.mod must pin both a go directive and a toolchain")
	}
	return mod, nil
}

type lockReport struct {
	GoDirective   string `json:"go_directive"`
	Toolchain     string `json:"toolchain"`
	DirectModules []struct {
		Module  string `json:"module"`
		Version string `json:"version"`
		Sum     string `json:"sum"`
	} `json:"direct_modules"`
}

// checkLock returns every disagreement between the running toolchain,
// go.mod, go.sum and the dependency lock report.
func checkLock(goVersion string, modSrc, sumSrc, lockSrc []byte) ([]string, error) {
	mod, err := parseGoMod(modSrc)
	if err != nil {
		return nil, err
	}
	var lock lockReport
	if err := json.Unmarshal(lockSrc, &lock); err != nil {
		return nil, faultf(codePrerequisiteMissing, "dependency lock report is not valid JSON: %v", err)
	}
	if len(lock.DirectModules) == 0 {
		return nil, faultf(codePrerequisiteMissing, "dependency lock report lists no direct modules")
	}
	sums := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(sumSrc))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 3 {
			sums[f[0]+" "+f[1]] = f[2]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading go.sum: %w", err)
	}
	var problems []string
	if goVersion != mod.Toolchain {
		problems = append(problems, fmt.Sprintf("running toolchain %q is not the pinned %q", goVersion, mod.Toolchain))
	}
	if lock.GoDirective != mod.Go {
		problems = append(problems, fmt.Sprintf("lock go_directive %q differs from go.mod %q", lock.GoDirective, mod.Go))
	}
	if lock.Toolchain != mod.Toolchain {
		problems = append(problems, fmt.Sprintf("lock toolchain %q differs from go.mod %q", lock.Toolchain, mod.Toolchain))
	}
	for _, m := range lock.DirectModules {
		if got := mod.Require[m.Module]; got != m.Version {
			problems = append(problems, fmt.Sprintf("%s: lock pins %q, go.mod requires %q", m.Module, m.Version, got))
		}
		if got := sums[m.Module+" "+m.Version]; got != m.Sum || got == "" {
			problems = append(problems, fmt.Sprintf("%s %s: lock checksum %q, go.sum has %q", m.Module, m.Version, m.Sum, got))
		}
	}
	return problems, nil
}

func runLock(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fl := newFlags("lock", stderr)
	goVersion := fl.String("go-version", "", "output of go env GOVERSION")
	root := fl.String("root", ".", "module root")
	lockPath := fl.String("lock", "docs/implementation/dependencies.lock.json", "dependency lock report, relative to the root")
	if err := parseFlags(fl, args); err != nil {
		return err
	}
	if *goVersion == "" {
		return faultf(codeInvalidInput, "-go-version is required")
	}
	var src [3][]byte
	for i, name := range []string{"go.mod", "go.sum", *lockPath} {
		data, err := os.ReadFile(under(*root, name))
		if err != nil {
			return faultf(codePrerequisiteMissing, "reading %s: %v", name, err)
		}
		src[i] = data
	}
	problems, err := checkLock(*goVersion, src[0], src[1], src[2])
	if err != nil {
		return err
	}
	for _, p := range problems {
		say(stdout, "%v", p)
	}
	if len(problems) > 0 {
		return faultf(codeVerificationFailed, "%d toolchain or dependency lock disagreements", len(problems))
	}
	say(stdout, "toolchain %s and the dependency lock agree", *goVersion)
	return nil
}

func runDrift(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fl := newFlags("drift", stderr)
	dir := fl.String("dir", ".", "repository checkout")
	if err := parseFlags(fl, args); err != nil {
		return err
	}
	out, err := git(ctx, *dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return faultf(codePrerequisiteMissing, "cannot inspect the working tree: %v", err)
	}
	if out == "" {
		say(stdout, "working tree is clean; generated files match the committed ones")
		return nil
	}
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if i == maxListedPaths {
			say(stdout, "... %d more paths omitted", len(lines)-maxListedPaths)
			break
		}
		say(stdout, "%v", l)
	}
	return faultf(codeVerificationFailed, "%d paths changed or appeared; regenerate and commit them", len(lines))
}

var (
	mainPackagePattern = regexp.MustCompile(`(?m)^package main\b`)
	testFuncPattern    = regexp.MustCompile(`(?m)^func Test[A-Z_0-9]\w*\(`)
)

// requirePackage fails with prerequisite_missing unless dir holds what a gate
// is about to exercise. go build and go test succeed quietly on a directory
// without Go files, which would turn an unimplemented gate into a pass.
func requirePackage(kind, dir string) error {
	switch kind {
	case "main":
		entries, err := os.ReadDir(dir)
		if err != nil {
			return faultf(codePrerequisiteMissing, "%s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			src, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return fmt.Errorf("reading %s: %w", e.Name(), err)
			}
			if mainPackagePattern.Match(src) {
				return nil
			}
		}
		return faultf(codePrerequisiteMissing, "%s has no main package; the entrypoint is not implemented", dir)
	case "tests":
		found := false
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if found || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			found = testFuncPattern.Match(src)
			return nil
		})
		if err != nil {
			return faultf(codePrerequisiteMissing, "%s: %v", dir, err)
		}
		if !found {
			return faultf(codePrerequisiteMissing, "%s has no Go tests; the suite is not implemented", dir)
		}
		return nil
	case "dir":
		entries, err := os.ReadDir(dir)
		if err != nil {
			return faultf(codePrerequisiteMissing, "%s: %v", dir, err)
		}
		if len(entries) == 0 {
			return faultf(codePrerequisiteMissing, "%s is empty; the suite is not implemented", dir)
		}
		return nil
	default:
		return faultf(codeInvalidInput, "kind must be main, tests or dir, got %q", kind)
	}
}

func runRequire(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fl := newFlags("require", stderr)
	kind := fl.String("kind", "", "main, tests or dir")
	if err := parseFlags(fl, args); err != nil {
		return err
	}
	if fl.NArg() == 0 {
		return faultf(codeInvalidInput, "usage: require -kind main|tests|dir <dir>...")
	}
	for _, dir := range fl.Args() {
		if err := requirePackage(*kind, dir); err != nil {
			return err
		}
		say(stdout, "%s: %s present", dir, *kind)
	}
	return nil
}

func fileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func runNotices(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fl := newFlags("notices", stderr)
	license := fl.String("license", "LICENSE", "license file")
	if err := parseFlags(fl, args); err != nil {
		return err
	}
	sum, _, err := fileSHA256(*license)
	if err != nil {
		return faultf(codePrerequisiteMissing, "reading the license: %v", err)
	}
	if sum != apacheLicenseSHA256 {
		return faultf(codeVerificationFailed, "%s (sha256 %s) is not the unmodified Apache License 2.0 text (sha256 %s)", *license, sum, apacheLicenseSHA256)
	}
	say(stdout, "%s is the unmodified Apache License 2.0 text", *license)
	return nil
}

// recordedEnv is the allowlist of environment variables copied into
// evidence. Nothing outside it is recorded, so evidence cannot carry a
// secret from the runner environment.
var recordedEnv = []string{
	"GITHUB_WORKFLOW", "GITHUB_JOB", "GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT", "GITHUB_EVENT_NAME",
	"GITHUB_REF", "GITHUB_REF_TYPE", "GITHUB_SHA", "GITHUB_REPOSITORY",
	"RUNNER_OS", "RUNNER_ARCH", "ImageOS", "ImageVersion",
	"GOTOOLCHAIN", "CGO_ENABLED", "GOFLAGS",
}

func runEnviron(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fl := newFlags("environ", stderr)
	root := fl.String("root", ".", "module root")
	goVersion := fl.String("go-version", "", "output of go env GOVERSION")
	out := fl.String("out", "", "evidence file to write")
	if err := parseFlags(fl, args); err != nil {
		return err
	}
	if *out == "" || *goVersion == "" {
		return faultf(codeInvalidInput, "-out and -go-version are required")
	}
	env := map[string]string{}
	for _, k := range recordedEnv {
		if v, ok := os.LookupEnv(k); ok {
			env[k] = boundString(v, 256)
		}
	}
	digests := map[string]string{}
	for _, name := range []string{"go.mod", "go.sum", "docs/implementation/dependencies.lock.json"} {
		sum, _, err := fileSHA256(filepath.Join(*root, name))
		if err != nil {
			return faultf(codePrerequisiteMissing, "hashing %s: %v", name, err)
		}
		digests[name] = sum
	}
	rec := map[string]any{
		"schema":         environSchema,
		"go_version":     *goVersion,
		"cigate_runtime": runtime.Version(),
		"goos":           runtime.GOOS,
		"goarch":         runtime.GOARCH,
		"environment":    env,
		"sha256":         digests,
	}
	if err := writeJSON(*out, rec); err != nil {
		return err
	}
	say(stdout, "recorded %s %s/%s environment evidence in %s", *goVersion, runtime.GOOS, runtime.GOARCH, *out)
	return nil
}

type binaryProvenance struct {
	File      string            `json:"file"`
	SHA256    string            `json:"sha256"`
	Size      int64             `json:"size"`
	GoVersion string            `json:"go_version"`
	Path      string            `json:"path"`
	Settings  map[string]string `json:"settings"`
	Deps      []string          `json:"deps"`
}

// inspectBinary reads the build information embedded in a Go binary and
// requires that it was built by the pinned toolchain from exactly the
// qualified, unmodified commit.
func inspectBinary(path, goVersion, revision string) (binaryProvenance, error) {
	p := binaryProvenance{File: filepath.Base(path), Settings: map[string]string{}, Deps: []string{}}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return p, faultf(codePrerequisiteMissing, "%s carries no Go build information: %v", path, err)
	}
	sum, size, err := fileSHA256(path)
	if err != nil {
		return p, fmt.Errorf("hashing %s: %w", path, err)
	}
	p.SHA256, p.Size, p.GoVersion, p.Path = sum, size, info.GoVersion, info.Path
	for _, s := range info.Settings {
		p.Settings[s.Key] = s.Value
	}
	for _, d := range info.Deps {
		p.Deps = append(p.Deps, d.Path+" "+d.Version+" "+d.Sum)
	}
	sort.Strings(p.Deps)
	switch {
	case p.GoVersion != goVersion:
		return p, faultf(codeVerificationFailed, "%s was built by %s, not the pinned %s", path, p.GoVersion, goVersion)
	case p.Settings["vcs.revision"] != revision:
		return p, faultf(codeVerificationFailed, "%s records revision %q, expected %q", path, p.Settings["vcs.revision"], revision)
	case p.Settings["vcs.modified"] != "false":
		return p, faultf(codeVerificationFailed, "%s was built from a modified tree (vcs.modified=%q)", path, p.Settings["vcs.modified"])
	case p.Settings["-trimpath"] != "true":
		return p, faultf(codeVerificationFailed, "%s was not built with -trimpath", path)
	}
	return p, nil
}

func runProvenance(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fl := newFlags("provenance", stderr)
	goVersion := fl.String("go-version", "", "pinned toolchain the binaries must report")
	revision := fl.String("revision", os.Getenv("GITHUB_SHA"), "commit the binaries must be built from")
	out := fl.String("out", "", "evidence file to write")
	if err := parseFlags(fl, args); err != nil {
		return err
	}
	if *out == "" || *goVersion == "" || *revision == "" || fl.NArg() == 0 {
		return faultf(codeInvalidInput, "usage: provenance -go-version <v> -revision <sha> -out <file> <binary>...")
	}
	binaries := []binaryProvenance{}
	var firstErr error
	for _, path := range fl.Args() {
		p, err := inspectBinary(path, *goVersion, *revision)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		binaries = append(binaries, p)
		say(stdout, "%s  %s", p.SHA256, p.File)
	}
	rec := map[string]any{"schema": provenanceSchema, "revision": *revision, "binaries": binaries, "verified": firstErr == nil}
	if err := writeJSON(*out, rec); err != nil {
		return err
	}
	return firstErr
}
