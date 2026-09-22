package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const helperEnv = "CIGATE_TEST_HELPER"

// TestMain lets the test binary stand in for the go command, so runGoTest is
// exercised through a real subprocess, pipes and exit codes.
func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "":
		os.Exit(m.Run())
	case "pass":
		fmt.Println(`{"Action":"run","Package":"p","Test":"TestA"}`)
		fmt.Println(`{"Action":"pass","Package":"p","Test":"TestA"}`)
		fmt.Println(`{"Action":"pass","Package":"p"}`)
		os.Exit(0)
	case "fail":
		fmt.Println(`{"Action":"output","Package":"p","Test":"TestB","Output":"boom\n"}`)
		fmt.Println(`{"Action":"fail","Package":"p","Test":"TestB"}`)
		fmt.Println(`{"Action":"fail","Package":"p"}`)
		fmt.Fprintln(os.Stderr, "FAIL")
		os.Exit(1)
	case "args":
		fmt.Printf("{\"Action\":\"output\",\"Package\":\"p\",\"Test\":\"TestArgs\",\"Output\":%q}\n", strings.Join(os.Args[1:], " "))
		fmt.Println(`{"Action":"fail","Package":"p","Test":"TestArgs"}`)
		os.Exit(1)
	case "flood":
		line := `{"Action":"output","Package":"p","Test":"TestA","Output":"` + strings.Repeat("x", 1000) + `\n"}`
		for i := 0; i < 2000; i++ {
			fmt.Println(line)
		}
		fmt.Println(`{"Action":"pass","Package":"p","Test":"TestA"}`)
		os.Exit(0)
	case "silent-exit":
		// Tests passed according to the stream, but the process failed.
		fmt.Println(`{"Action":"pass","Package":"p","Test":"TestA"}`)
		os.Exit(3)
	}
	os.Exit(97)
}

func collect(t *testing.T, strict bool, exit int, lines ...string) testSummary {
	t.Helper()
	return collectRequire(t, strict, nil, exit, lines...)
}

func collectRequire(t *testing.T, strict bool, required []string, exit int, lines ...string) testSummary {
	t.Helper()
	c := newTestCollector("unit", []string{"./..."}, strict, required)
	if err := c.consume(strings.NewReader(strings.Join(lines, "\n"))); err != nil {
		t.Fatalf("consume: %v", err)
	}
	return c.finish(exit, false)
}

func TestCollectorVerdicts(t *testing.T) {
	t.Parallel()
	pass := `{"Action":"pass","Package":"p","Test":"TestA"}`
	skip := `{"Action":"skip","Package":"p","Test":"TestNeedsCredentials"}`
	noTests := `{"Action":"skip","Package":"q"}`
	cases := []struct {
		name     string
		strict   bool
		exit     int
		lines    []string
		passed   bool
		blocking string
	}{
		{"passing run", false, 0, []string{pass}, true, ""},
		{"skip tolerated outside release", false, 0, []string{pass, skip}, true, ""},
		{"skip blocks a release gate", true, 0, []string{pass, skip}, false, "skipped"},
		{"no test ran", false, 0, []string{noTests}, false, "no test ran"},
		{"only skips is not evidence", false, 0, []string{skip}, false, "no test ran"},
		{"empty stream", true, 0, nil, false, "no test ran"},
		{"failed test", false, 1, []string{pass, `{"Action":"fail","Package":"p","Test":"TestB"}`}, false, "failed"},
		{"build failure", false, 1, []string{pass, `{"ImportPath":"q [q.test]","Action":"build-output","Output":"undefined: x\n"}`, `{"ImportPath":"q [q.test]","Action":"build-fail"}`}, false, "failed"},
		{"nonzero exit without a failure event", false, 2, []string{pass}, false, "exited 2"},
		{"failure event with zero exit", false, 0, []string{pass, `{"Action":"fail","Package":"p"}`}, false, "failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := collect(t, tc.strict, tc.exit, tc.lines...)
			if s.Passed != tc.passed {
				t.Fatalf("Passed = %v, want %v; blocking %v", s.Passed, tc.passed, s.Blocking)
			}
			if tc.blocking != "" && !strings.Contains(strings.Join(s.Blocking, "|"), tc.blocking) {
				t.Errorf("Blocking = %v, want %q", s.Blocking, tc.blocking)
			}
		})
	}
}

// TestRequiredTestMustBeObservedPassing proves the platform regression
// protection (docs/implementation-remediation/audit.md, "Hosted CI
// follow-up"; see policy.go's requiredPlatformRegressionTests): a required
// pkg#Test identity that never appears with a "pass" action blocks the run
// even when every test that DID run passed and the process exited zero.
// This is the guard -strict's own skip detection cannot provide, because a
// deleted or renamed test function emits no pass, fail, or skip event at
// all -- there is nothing in the stream to detect.
func TestRequiredTestMustBeObservedPassing(t *testing.T) {
	t.Parallel()
	pass := `{"Action":"pass","Package":"p","Test":"TestA"}`
	other := `{"Action":"pass","Package":"p","Test":"TestOther"}`
	cases := []struct {
		name     string
		required []string
		lines    []string
		passed   bool
		blocking string
	}{
		{"required test observed passing", []string{"p#TestA"}, []string{pass}, true, ""},
		{"no required tests configured", nil, []string{pass}, true, ""},
		{"required test never ran at all", []string{"p#TestMissing"}, []string{pass}, false, "p#TestMissing"},
		{"required test renamed: identity absent even though the suite is green", []string{"p#TestRenamedAway"}, []string{pass, other}, false, "p#TestRenamedAway"},
		{"required test ran but was skipped, not passed", []string{"p#TestA"}, []string{`{"Action":"skip","Package":"p","Test":"TestA"}`, other}, false, "p#TestA"},
		{"required test ran but failed", []string{"p#TestA"}, []string{`{"Action":"fail","Package":"p","Test":"TestA"}`}, false, "p#TestA"},
		{"a different package's same-named test does not satisfy the requirement", []string{"q#TestA"}, []string{pass}, false, "q#TestA"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := collectRequire(t, false, tc.required, 0, tc.lines...)
			if s.Passed != tc.passed {
				t.Fatalf("Passed = %v, want %v; blocking %v; required_missing %v", s.Passed, tc.passed, s.Blocking, s.RequiredMissing)
			}
			if tc.blocking == "" {
				if len(s.RequiredMissing) != 0 {
					t.Errorf("RequiredMissing = %v, want none", s.RequiredMissing)
				}
				return
			}
			if !contains(s.RequiredMissing, tc.blocking) {
				t.Errorf("RequiredMissing = %v, want %q", s.RequiredMissing, tc.blocking)
			}
			if !strings.Contains(strings.Join(s.Blocking, "|"), tc.blocking) {
				t.Errorf("Blocking = %v, want to mention %q", s.Blocking, tc.blocking)
			}
		})
	}
}

func TestCollectorRetainsBoundedFailureEvidence(t *testing.T) {
	t.Parallel()
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf(`{"Action":"output","Package":"p","Test":"TestBig","Output":%q}`, strings.Repeat("y", 1024)))
	}
	lines = append(lines, `{"Action":"fail","Package":"p","Test":"TestBig"}`, "not json at all")
	for i := 0; i < maxListedTests+25; i++ {
		lines = append(lines, fmt.Sprintf(`{"Action":"skip","Package":"p","Test":"TestSkip%d"}`, i))
	}
	lines = append(lines, `{"ImportPath":"q [q.test]","Action":"build-output","Output":"undefined: x\n"}`, `{"ImportPath":"q [q.test]","Action":"build-fail"}`)
	s := collect(t, false, 1, lines...)
	if len(s.Failed) != 2 || s.Failed[0].Test != "TestBig" {
		t.Fatalf("Failed = %+v", s.Failed)
	}
	if got := len(s.Failed[0].Output); got > maxTestOutputBytes+len("...[truncated]") || !strings.HasSuffix(s.Failed[0].Output, "...[truncated]") {
		t.Errorf("failure output is %d bytes and must be bounded to %d with a marker", got, maxTestOutputBytes)
	}
	if s.Failed[1].Package != "q [q.test]" || !strings.Contains(s.Failed[1].Output, "undefined: x") {
		t.Errorf("build failure evidence = %+v", s.Failed[1])
	}
	if len(s.SkippedTests) != maxListedTests || s.OmittedEntries != 25 || s.Tests.Skipped != maxListedTests+25 {
		t.Errorf("skipped list %d, omitted %d, counted %d", len(s.SkippedTests), s.OmittedEntries, s.Tests.Skipped)
	}
	if s.UnparsedLines != 1 {
		t.Errorf("UnparsedLines = %d, want 1", s.UnparsedLines)
	}
}

func runHelper(t *testing.T, scenario string, extra ...string) (int, testSummary, string, string) {
	t.Helper()
	t.Setenv(helperEnv, scenario)
	dir := t.TempDir()
	args := append([]string{"gotest", "-go", os.Args[0], "-name", "unit", "-out-dir", dir}, extra...)
	args = append(args, "--", "-race", "./...")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, &stdout, &stderr)
	var s testSummary
	data, err := os.ReadFile(filepath.Join(dir, "unit.summary.json"))
	if err != nil {
		t.Fatalf("summary was not retained: %v", err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	return code, s, dir, stdout.String()
}

func TestRunGoTestSubprocess(t *testing.T) {
	code, s, dir, _ := runHelper(t, "pass")
	if code != 0 || !s.Passed || s.Tests.Passed != 1 || s.Packages.Passed != 1 {
		t.Fatalf("pass: code %d, summary %+v", code, s)
	}
	if info, err := os.Stat(filepath.Join(dir, "unit.events.jsonl")); err != nil || info.Size() == 0 {
		t.Errorf("event log was not retained: %v", err)
	}

	code, s, dir, console := runHelper(t, "fail")
	if code != 1 || s.Passed || s.ExitCode != 1 || len(s.Failed) == 0 || s.Failed[0].Output != "boom\n" {
		t.Fatalf("fail: code %d, summary %+v", code, s)
	}
	if !strings.Contains(console, "FAIL p TestB") {
		t.Errorf("console report = %q", console)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "unit.stderr.txt")); string(got) != "FAIL\n" {
		t.Errorf("stderr evidence = %q", got)
	}

	if code, s, _, _ = runHelper(t, "silent-exit"); code != 1 || s.Passed || s.ExitCode != 3 {
		t.Fatalf("silent-exit: code %d, summary %+v", code, s)
	}

	if _, s, _, _ = runHelper(t, "args"); len(s.Failed) != 1 || s.Failed[0].Output != "test -json -race ./..." {
		t.Fatalf("go test was not invoked as \"test -json <args>\": %+v", s.Failed)
	}
}

// TestRunGoTestSubprocessRequireTests proves -require-tests reaches the real
// CLI flag parsing and subprocess path, not just the in-memory collector:
// the "pass" helper scenario always exits 0 and reports zero failures, so
// only the -require-tests check can turn it into a blocking exit.
func TestRunGoTestSubprocessRequireTests(t *testing.T) {
	code, s, _, _ := runHelper(t, "pass", "-require-tests", "p#TestA")
	if code != 0 || !s.Passed || len(s.RequiredMissing) != 0 {
		t.Fatalf("satisfied requirement: code %d, summary %+v", code, s)
	}

	code, s, _, console := runHelper(t, "pass", "-require-tests", "p#TestNeverRan")
	if code != 1 || s.Passed || !contains(s.RequiredMissing, "p#TestNeverRan") {
		t.Fatalf("unsatisfied requirement: code %d, summary %+v", code, s)
	}
	if !strings.Contains(console, "MISSING required test p#TestNeverRan") {
		t.Errorf("console report = %q", console)
	}
}

func TestRunGoTestBoundsTheRetainedLog(t *testing.T) {
	code, s, dir, _ := runHelper(t, "flood", "-max-log-bytes", "65536")
	if code != 0 || !s.Passed || !s.LogTruncated {
		t.Fatalf("flood: code %d, summary %+v", code, s)
	}
	info, err := os.Stat(filepath.Join(dir, "unit.events.jsonl"))
	if err != nil || info.Size() != 65536 {
		t.Fatalf("retained log size = %v, %v; want exactly the 65536 byte bound", info, err)
	}
}

func TestRunGoTestRejectsBadInvocations(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	dir := t.TempDir()
	for _, args := range [][]string{
		{"gotest", "-name", "unit", "-out-dir", dir},
		{"gotest", "-name", "../escape", "-out-dir", dir, "--", "./..."},
		{"gotest", "-name", "unit", "--", "./..."},
	} {
		if code := run(context.Background(), args, &stdout, &stderr); code != 2 {
			t.Errorf("run(%v) = %d, want 2", args, code)
		}
	}
	args := []string{"gotest", "-go", filepath.Join(dir, "no-such-go"), "-name", "unit", "-out-dir", dir, "--", "./..."}
	if code := run(context.Background(), args, &stdout, &stderr); code != 5 {
		t.Errorf("missing go command: exit %d, want 5 (prerequisite_missing)", code)
	}
}
