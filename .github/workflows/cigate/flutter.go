package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	flutterPinSchema     = "zatiti.ci.flutter_pin/v1"
	flutterLayoutSchema  = "zatiti.ci.flutter_layout/v1"
	boundedResultSchema  = "zatiti.ci.bounded_command/v1"
	flutterTestSchema    = "zatiti.ci.flutter_test_summary/v1"
	flutterRepositoryURL = "https://github.com/flutter/flutter.git"
)

// flutterPin is the exact SDK the Flutter job installs. It is read from the
// dependency lock report, which integration owns; pubspec.lock only carries
// SDK constraints, so the report is the single place the version is exact.
type flutterPin struct {
	Version           string `json:"version"`
	FrameworkRevision string `json:"framework_revision"`
	DartSDK           string `json:"dart_sdk"`
}

var (
	semverPattern     = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$`)
	revisionPattern   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	constraintPattern = regexp.MustCompile(`^>=(\d+\.\d+\.\d+)(?:\s*<(\d+\.\d+\.\d+))?$`)
)

// readFlutterPin extracts the flutter_sdk pin from the dependency lock report
// and fails with prerequisite_missing when the report has none.
func readFlutterPin(lockSrc []byte) (flutterPin, error) {
	var report struct {
		Flutter *flutterPin `json:"flutter_sdk"`
	}
	if err := json.Unmarshal(lockSrc, &report); err != nil {
		return flutterPin{}, faultf(codePrerequisiteMissing, "dependency lock report is not valid JSON: %v", err)
	}
	if report.Flutter == nil {
		return flutterPin{}, faultf(codePrerequisiteMissing, "dependency lock report has no flutter_sdk pin ({version, framework_revision, dart_sdk}); integration records it before the Flutter job can run")
	}
	p := *report.Flutter
	switch {
	case !semverPattern.MatchString(p.Version):
		return p, faultf(codePrerequisiteMissing, "flutter_sdk.version %q is not an exact version", p.Version)
	case !revisionPattern.MatchString(p.FrameworkRevision):
		return p, faultf(codePrerequisiteMissing, "flutter_sdk.framework_revision %q is not a full commit", p.FrameworkRevision)
	case !semverPattern.MatchString(p.DartSDK):
		return p, faultf(codePrerequisiteMissing, "flutter_sdk.dart_sdk %q is not an exact version", p.DartSDK)
	}
	return p, nil
}

// compareVersions orders two MAJOR.MINOR.PATCH versions.
func compareVersions(a, b string) (int, error) {
	ma, mb := semverPattern.FindStringSubmatch(a), semverPattern.FindStringSubmatch(b)
	if ma == nil || mb == nil {
		return 0, fmt.Errorf("versions %q and %q must be MAJOR.MINOR.PATCH", a, b)
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(ma[i])
		y, _ := strconv.Atoi(mb[i])
		if x != y {
			if x < y {
				return -1, nil
			}
			return 1, nil
		}
	}
	return 0, nil
}

// checkConstraint requires version to satisfy a ">=low" or ">=low <high"
// pubspec.lock SDK constraint.
func checkConstraint(what, version, constraint string) error {
	m := constraintPattern.FindStringSubmatch(strings.TrimSpace(constraint))
	if m == nil {
		return faultf(codePrerequisiteMissing, "pubspec.lock %s constraint %q is not in the form >=X.Y.Z [<X.Y.Z]", what, constraint)
	}
	if c, err := compareVersions(version, m[1]); err != nil || c < 0 {
		return faultf(codeVerificationFailed, "pinned %s %s does not satisfy pubspec.lock constraint %q", what, version, constraint)
	}
	if m[2] != "" {
		if c, err := compareVersions(version, m[2]); err != nil || c >= 0 {
			return faultf(codeVerificationFailed, "pinned %s %s does not satisfy pubspec.lock constraint %q", what, version, constraint)
		}
	}
	return nil
}

// bindPin checks the pinned SDK against the constraints pubspec.lock records.
func bindPin(p flutterPin, pubspecLock []byte) error {
	root, err := parseYAML(pubspecLock)
	if err != nil {
		return faultf(codePrerequisiteMissing, "pubspec.lock: %v", err)
	}
	flutter, ok := root.path("sdks", "flutter").scalar()
	if !ok {
		return faultf(codePrerequisiteMissing, "pubspec.lock has no sdks.flutter constraint")
	}
	dart, ok := root.path("sdks", "dart").scalar()
	if !ok {
		return faultf(codePrerequisiteMissing, "pubspec.lock has no sdks.dart constraint")
	}
	if err := checkConstraint("Flutter", p.Version, flutter); err != nil {
		return err
	}
	return checkConstraint("Dart", p.DartSDK, dart)
}

// appendOutputs appends key=value lines to a GitHub step output file.
func appendOutputs(path string, pairs [][2]string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening step output file: %w", err)
	}
	for _, kv := range pairs {
		if _, err := fmt.Fprintf(f, "%s=%s\n", kv[0], kv[1]); err != nil {
			_ = f.Close()
			return fmt.Errorf("writing step output: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing step output file: %w", err)
	}
	return nil
}

func runFlutterPin(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fl := newFlags("flutterpin", stderr)
	root := fl.String("root", ".", "repository root")
	lockPath := fl.String("lock", "docs/implementation/dependencies.lock.json", "dependency lock report, relative to the root")
	app := fl.String("app", "apps/desktop", "Flutter application directory, relative to the root")
	out := fl.String("out", "", "evidence file to write")
	outputFile := fl.String("output-file", os.Getenv("GITHUB_OUTPUT"), "step output file that receives version and revision")
	if err := parseFlags(fl, args); err != nil {
		return err
	}
	if *out == "" || *outputFile == "" {
		return faultf(codeInvalidInput, "-out and -output-file (or GITHUB_OUTPUT) are required")
	}
	lockSrc, err := os.ReadFile(under(*root, *lockPath))
	if err != nil {
		return faultf(codePrerequisiteMissing, "reading the dependency lock report: %v", err)
	}
	pin, err := readFlutterPin(lockSrc)
	if err != nil {
		return err
	}
	pubspecLock, err := os.ReadFile(filepath.Join(under(*root, *app), "pubspec.lock"))
	if err != nil {
		return faultf(codePrerequisiteMissing, "reading pubspec.lock: %v", err)
	}
	if err := bindPin(pin, pubspecLock); err != nil {
		return err
	}
	rec := map[string]any{"schema": flutterPinSchema, "flutter_sdk": pin, "repository": flutterRepositoryURL}
	if err := writeJSON(*out, rec); err != nil {
		return err
	}
	if err := appendOutputs(*outputFile, [][2]string{{"version", pin.Version}, {"revision", pin.FrameworkRevision}}); err != nil {
		return err
	}
	say(stdout, "Flutter %s (%s, Dart %s) is pinned and satisfies pubspec.lock", pin.Version, pin.FrameworkRevision, pin.DartSDK)
	return nil
}

// verifyInstalledFlutter compares "flutter --version --machine" output with
// the pin, so a wrong checkout or a floating channel fails the job.
func verifyInstalledFlutter(pin flutterPin, machineJSON []byte) error {
	var got struct {
		FrameworkVersion  string `json:"frameworkVersion"`
		FrameworkRevision string `json:"frameworkRevision"`
		DartSDKVersion    string `json:"dartSdkVersion"`
	}
	if err := json.Unmarshal(machineJSON, &got); err != nil {
		return faultf(codePrerequisiteMissing, "flutter --version --machine output is not valid JSON: %v", err)
	}
	switch {
	case got.FrameworkVersion != pin.Version:
		return faultf(codeVerificationFailed, "installed Flutter %q is not the pinned %s", got.FrameworkVersion, pin.Version)
	case got.FrameworkRevision != pin.FrameworkRevision:
		return faultf(codeVerificationFailed, "installed framework revision %q is not the pinned %s", got.FrameworkRevision, pin.FrameworkRevision)
	case got.DartSDKVersion != pin.DartSDK:
		return faultf(codeVerificationFailed, "installed Dart %q is not the pinned %s", got.DartSDKVersion, pin.DartSDK)
	}
	return nil
}

func runFlutterVerify(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fl := newFlags("flutterverify", stderr)
	root := fl.String("root", ".", "repository root")
	lockPath := fl.String("lock", "docs/implementation/dependencies.lock.json", "dependency lock report, relative to the root")
	versionFile := fl.String("version-file", "", "file holding the output of flutter --version --machine")
	if err := parseFlags(fl, args); err != nil {
		return err
	}
	if *versionFile == "" {
		return faultf(codeInvalidInput, "-version-file is required")
	}
	lockSrc, err := os.ReadFile(under(*root, *lockPath))
	if err != nil {
		return faultf(codePrerequisiteMissing, "reading the dependency lock report: %v", err)
	}
	pin, err := readFlutterPin(lockSrc)
	if err != nil {
		return err
	}
	machine, err := os.ReadFile(*versionFile)
	if err != nil {
		return faultf(codePrerequisiteMissing, "reading %s: %v", *versionFile, err)
	}
	if err := verifyInstalledFlutter(pin, machine); err != nil {
		return err
	}
	say(stdout, "installed Flutter %s at %s matches the pin", pin.Version, pin.FrameworkRevision)
	return nil
}

// flutterLayout reports which optional parts of the application exist, so a
// pull-request job can state what it did not run instead of failing on a
// suite that is not written yet. Release gates do not consult it.
func flutterLayout(app string) (map[string]bool, error) {
	if _, err := os.Stat(filepath.Join(app, "pubspec.lock")); err != nil {
		return nil, faultf(codePrerequisiteMissing, "%s has no pubspec.lock: %v", app, err)
	}
	layout := map[string]bool{}
	for _, dir := range []string{"integration_test", "linux", "macos"} {
		info, err := os.Stat(filepath.Join(app, dir))
		layout[dir] = err == nil && info.IsDir()
	}
	return layout, nil
}

func runFlutterLayout(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fl := newFlags("flutterlayout", stderr)
	app := fl.String("app", "apps/desktop", "Flutter application directory")
	out := fl.String("out", "", "evidence file to write")
	outputFile := fl.String("output-file", os.Getenv("GITHUB_OUTPUT"), "step output file")
	if err := parseFlags(fl, args); err != nil {
		return err
	}
	if *out == "" || *outputFile == "" {
		return faultf(codeInvalidInput, "-out and -output-file (or GITHUB_OUTPUT) are required")
	}
	layout, err := flutterLayout(*app)
	if err != nil {
		return err
	}
	if err := writeJSON(*out, map[string]any{"schema": flutterLayoutSchema, "present": layout}); err != nil {
		return err
	}
	var pairs [][2]string
	for _, dir := range []string{"integration_test", "linux", "macos"} {
		pairs = append(pairs, [2]string{dir, strconv.FormatBool(layout[dir])})
		say(stdout, "%s/%s present: %v", *app, dir, layout[dir])
	}
	return appendOutputs(*outputFile, pairs)
}

// runBounded runs one command, retains its combined output up to a size
// bound, and reports the exit code. It exists so long tool output lands in
// evidence instead of the console.
func runBounded(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fl := newFlags("bounded", stderr)
	name := fl.String("name", "", "evidence name")
	outDir := fl.String("out-dir", "", "evidence directory")
	dir := fl.String("dir", ".", "working directory for the command")
	timeout := fl.Duration("timeout", 40*time.Minute, "overall time limit")
	maxLog := fl.Int64("max-log-bytes", 4<<20, "size limit of the retained log")
	if err := parseFlags(fl, args); err != nil {
		return err
	}
	if !evidenceNamePattern.MatchString(*name) || *outDir == "" || fl.NArg() == 0 || *maxLog < 1 {
		return faultf(codeInvalidInput, "usage: bounded -name <name> -out-dir <dir> [-dir <dir>] -- <command> [args...]")
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return fmt.Errorf("creating evidence directory: %w", err)
	}
	logFile, err := os.Create(filepath.Join(*outDir, *name+".log"))
	if err != nil {
		return fmt.Errorf("creating log: %w", err)
	}
	log := &boundedFile{f: logFile, limit: *maxLog}
	tail := &tailBuffer{limit: 4 << 10}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, fl.Args()[0], fl.Args()[1:]...)
	cmd.Dir = *dir
	cmd.Stdout = io.MultiWriter(log, tail)
	cmd.Stderr = cmd.Stdout
	runErr := cmd.Run()
	if err := logFile.Close(); err != nil {
		return fmt.Errorf("closing log: %w", err)
	}
	exit, startErr := exitStatus(runErr)
	rec := map[string]any{
		"schema": boundedResultSchema, "name": *name, "command": fl.Args(), "dir": *dir,
		"exit_code": exit, "log_truncated": log.truncated, "timed_out": ctx.Err() != nil,
	}
	if err := writeJSON(filepath.Join(*outDir, *name+".result.json"), rec); err != nil {
		return err
	}
	say(stdout, "%s: exit %d; last output:\n%s", *name, exit, tail.String())
	switch {
	case startErr != nil:
		return faultf(codePrerequisiteMissing, "%s: %v", *name, startErr)
	case ctx.Err() != nil:
		return faultf(codeVerificationFailed, "%s: time limit %s exceeded", *name, *timeout)
	case exit != 0:
		return faultf(codeVerificationFailed, "%s: %s exited %d", *name, fl.Args()[0], exit)
	}
	return nil
}

// exitStatus splits a command error into an exit code and a start failure.
func exitStatus(err error) (int, error) {
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), nil
	default:
		return -1, err
	}
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	buf   []byte
	limit int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.limit {
		t.buf = t.buf[len(t.buf)-t.limit:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }

// flutterEvent is the subset of the dart test JSON reporter protocol that
// "flutter test --machine" emits and this tool consumes.
type flutterEvent struct {
	Type string `json:"type"`
	Test struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"test"`
	TestID     int    `json:"testID"`
	Result     string `json:"result"`
	Skipped    bool   `json:"skipped"`
	Hidden     bool   `json:"hidden"`
	Message    string `json:"message"`
	Error      string `json:"error"`
	StackTrace string `json:"stackTrace"`
	Success    *bool  `json:"success"`
}

// flutterCollector folds the reporter stream into a testSummary.
type flutterCollector struct {
	sum    testSummary
	names  map[int]string
	output map[int]*boundedBuffer
	done   *bool
}

func newFlutterCollector(name string, args []string, strict bool) *flutterCollector {
	return &flutterCollector{
		sum: testSummary{
			Schema: flutterTestSchema, Name: name, Args: args, Strict: strict,
			Failed: []failedTest{}, SkippedTests: []string{}, PackagesWithoutTests: []string{}, Blocking: []string{},
		},
		names:  map[int]string{},
		output: map[int]*boundedBuffer{},
	}
}

func (c *flutterCollector) consume(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxEventLineBytes)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev flutterEvent
		if err := json.Unmarshal(line, &ev); err != nil || ev.Type == "" {
			// The runner also prints JSON arrays of daemon events; they carry
			// no test result and are counted as unparsed for the record.
			c.sum.UnparsedLines++
			continue
		}
		c.event(ev)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("reading flutter test events: %w", err)
	}
	return nil
}

func (c *flutterCollector) event(ev flutterEvent) {
	switch ev.Type {
	case "testStart":
		c.names[ev.Test.ID] = ev.Test.Name
	case "print", "error":
		buf := c.output[ev.TestID]
		if buf == nil {
			buf = &boundedBuffer{limit: maxTestOutputBytes}
			c.output[ev.TestID] = buf
		}
		text := ev.Message
		if ev.Type == "error" {
			text = ev.Error + "\n" + ev.StackTrace
		}
		_, _ = buf.Write([]byte(text + "\n"))
	case "testDone":
		name := c.names[ev.TestID]
		var out string
		if buf := c.output[ev.TestID]; buf != nil {
			out = buf.String()
		}
		delete(c.output, ev.TestID)
		delete(c.names, ev.TestID)
		switch {
		case ev.Hidden && ev.Result == "success":
			// Suite loading entries are not tests.
		case ev.Skipped:
			c.sum.Tests.Skipped++
			if len(c.sum.SkippedTests) < maxListedTests {
				c.sum.SkippedTests = append(c.sum.SkippedTests, name)
			} else {
				c.sum.OmittedEntries++
			}
		case ev.Result == "success":
			c.sum.Tests.Passed++
		default:
			c.sum.Tests.Failed++
			if len(c.sum.Failed) < maxListedTests {
				c.sum.Failed = append(c.sum.Failed, failedTest{Package: ev.Result, Test: name, Output: out})
			} else {
				c.sum.OmittedEntries++
			}
		}
	case "done":
		c.done = ev.Success
	}
}

func (c *flutterCollector) finish(exit int, logTruncated bool) testSummary {
	s := &c.sum
	s.ExitCode, s.LogTruncated = exit, logTruncated
	if exit != 0 {
		s.Blocking = append(s.Blocking, fmt.Sprintf("flutter test exited %d", exit))
	}
	if s.Tests.Failed > 0 {
		s.Blocking = append(s.Blocking, fmt.Sprintf("%d failed tests", s.Tests.Failed))
	}
	if s.Tests.Passed+s.Tests.Failed == 0 {
		s.Blocking = append(s.Blocking, "no test ran")
	}
	if c.done == nil || !*c.done {
		s.Blocking = append(s.Blocking, "the test runner did not report a successful completion")
	}
	if s.Strict && s.Tests.Skipped > 0 {
		s.Blocking = append(s.Blocking, fmt.Sprintf("%d skipped tests; a required qualification that did not run blocks", s.Tests.Skipped))
	}
	s.Passed = len(s.Blocking) == 0
	return *s
}

func runFlutterTest(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fl := newFlags("fluttertest", stderr)
	name := fl.String("name", "", "evidence name")
	outDir := fl.String("out-dir", "", "evidence directory")
	dir := fl.String("dir", "apps/desktop", "Flutter application directory")
	strict := fl.Bool("strict", false, "block on skipped tests")
	flutterBin := fl.String("flutter", "flutter", "flutter command")
	prefix := fl.String("prefix", "", "space-separated command to run flutter under, for example a virtual display wrapper")
	timeout := fl.Duration("timeout", 40*time.Minute, "overall time limit")
	maxLog := fl.Int64("max-log-bytes", 8<<20, "size limit of the retained event log")
	if err := parseFlags(fl, args); err != nil {
		return err
	}
	if !evidenceNamePattern.MatchString(*name) || *outDir == "" || *maxLog < 1 {
		return faultf(codeInvalidInput, "usage: fluttertest -name <name> -out-dir <dir> [-dir <app>] [-strict] [-- <flutter test arguments>]")
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return fmt.Errorf("creating evidence directory: %w", err)
	}
	logFile, err := os.Create(filepath.Join(*outDir, *name+".events.jsonl"))
	if err != nil {
		return fmt.Errorf("creating event log: %w", err)
	}
	log := &boundedFile{f: logFile, limit: *maxLog}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	argv := append(strings.Fields(*prefix), *flutterBin, "test", "--machine")
	argv = append(argv, fl.Args()...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = *dir
	errBuf := &boundedBuffer{limit: maxStderrBytes}
	cmd.Stderr = errBuf
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = logFile.Close()
		return fmt.Errorf("attaching to flutter test: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return faultf(codePrerequisiteMissing, "starting %s: %v", argv[0], err)
	}
	col := newFlutterCollector(*name, fl.Args(), *strict)
	readErr := col.consume(io.TeeReader(pipe, log))
	if readErr != nil {
		_, _ = io.Copy(io.Discard, pipe)
	}
	exit, _ := exitStatus(cmd.Wait())
	if err := logFile.Close(); err != nil {
		return fmt.Errorf("closing event log: %w", err)
	}
	sum := col.finish(exit, log.truncated)
	if readErr != nil {
		sum.Blocking = append(sum.Blocking, readErr.Error())
		sum.Passed = false
	}
	if ctx.Err() != nil {
		sum.Blocking = append(sum.Blocking, fmt.Sprintf("time limit %s exceeded", *timeout))
		sum.Passed = false
	}
	if err := os.WriteFile(filepath.Join(*outDir, *name+".stderr.txt"), []byte(errBuf.String()), 0o644); err != nil {
		return fmt.Errorf("writing stderr evidence: %w", err)
	}
	if err := writeJSON(filepath.Join(*outDir, *name+".summary.json"), sum); err != nil {
		return err
	}
	printSummary(stdout, sum)
	if !sum.Passed {
		return faultf(codeVerificationFailed, "%s: %s", *name, strings.Join(sum.Blocking, "; "))
	}
	return nil
}
