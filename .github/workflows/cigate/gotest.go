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
	"sort"
	"strings"
	"time"
)

const (
	testSummarySchema = "zatiti.ci.test_summary/v1"
	// maxTestOutputBytes matches the repository's 8 KiB log record bound.
	maxTestOutputBytes = 8 << 10
	maxListedTests     = 200
	maxStderrBytes     = 64 << 10
	maxEventLineBytes  = 4 << 20
)

var evidenceNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	// ImportPath replaces Package on build-output and build-fail events.
	ImportPath string `json:"ImportPath"`
	Test       string `json:"Test"`
	Output     string `json:"Output"`
}

type failedTest struct {
	Package string `json:"package"`
	Test    string `json:"test,omitempty"`
	Output  string `json:"output"`
}

type counts struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// testSummary is the bounded, retained result of one go test run.
type testSummary struct {
	Schema               string       `json:"schema"`
	Name                 string       `json:"name"`
	Args                 []string     `json:"args"`
	Strict               bool         `json:"strict"`
	ExitCode             int          `json:"exit_code"`
	Packages             counts       `json:"packages"`
	Tests                counts       `json:"tests"`
	Failed               []failedTest `json:"failed"`
	SkippedTests         []string     `json:"skipped_tests"`
	PackagesWithoutTests []string     `json:"packages_without_tests"`
	RequiredMissing      []string     `json:"required_missing"`
	UnparsedLines        int          `json:"unparsed_lines"`
	OmittedEntries       int          `json:"omitted_entries"`
	LogTruncated         bool         `json:"log_truncated"`
	Blocking             []string     `json:"blocking"`
	Passed               bool         `json:"passed"`
}

// testCollector folds a go test -json stream into a testSummary while holding
// a bounded amount of output per running test. required names the
// "pkg#Test" identities (see runGoTest's -require-tests) that must be
// observed passing; a required identity whose test function was renamed or
// deleted never emits a pass, fail or skip event at all, so this is
// independent of, and stricter than, -strict's skip detection.
type testCollector struct {
	sum      testSummary
	output   map[string]*boundedBuffer
	required map[string]bool
	seenPass map[string]bool
}

func newTestCollector(name string, args []string, strict bool, required []string) *testCollector {
	req := make(map[string]bool, len(required))
	for _, r := range required {
		req[r] = true
	}
	return &testCollector{
		sum: testSummary{
			Schema: testSummarySchema, Name: name, Args: args, Strict: strict,
			Failed: []failedTest{}, SkippedTests: []string{}, PackagesWithoutTests: []string{}, RequiredMissing: []string{}, Blocking: []string{},
		},
		output:   map[string]*boundedBuffer{},
		required: req,
		seenPass: map[string]bool{},
	}
}

func (c *testCollector) consume(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxEventLineBytes)
	for sc.Scan() {
		var ev testEvent
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil || ev.Action == "" {
			c.sum.UnparsedLines++
			continue
		}
		c.event(ev)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("reading go test events: %w", err)
	}
	return nil
}

func (c *testCollector) event(ev testEvent) {
	if ev.Package == "" {
		ev.Package = ev.ImportPath
	}
	key := ev.Package + "\x00" + ev.Test
	switch ev.Action {
	case "output", "build-output":
		buf := c.output[key]
		if buf == nil {
			buf = &boundedBuffer{limit: maxTestOutputBytes}
			c.output[key] = buf
		}
		_, _ = buf.Write([]byte(ev.Output))
	case "pass":
		if ev.Test == "" {
			c.sum.Packages.Passed++
		} else {
			c.sum.Tests.Passed++
			if req := ev.Package + "#" + ev.Test; c.required[req] {
				c.seenPass[req] = true
			}
		}
		delete(c.output, key)
	case "skip":
		if ev.Test == "" {
			c.sum.Packages.Skipped++
			c.sum.PackagesWithoutTests = c.list(c.sum.PackagesWithoutTests, ev.Package)
		} else {
			c.sum.Tests.Skipped++
			c.sum.SkippedTests = c.list(c.sum.SkippedTests, ev.Package+"."+ev.Test)
		}
		delete(c.output, key)
	case "fail", "build-fail":
		if ev.Test == "" {
			c.sum.Packages.Failed++
		} else {
			c.sum.Tests.Failed++
		}
		var out string
		if buf := c.output[key]; buf != nil {
			out = buf.String()
		}
		delete(c.output, key)
		if len(c.sum.Failed) < maxListedTests {
			c.sum.Failed = append(c.sum.Failed, failedTest{Package: ev.Package, Test: ev.Test, Output: out})
		} else {
			c.sum.OmittedEntries++
		}
	}
}

func (c *testCollector) list(dst []string, item string) []string {
	if len(dst) < maxListedTests {
		return append(dst, item)
	}
	c.sum.OmittedEntries++
	return dst
}

// finish records the verdict. A run that executed no test is never evidence;
// under strict, a skipped test is an unmet prerequisite and blocks too.
func (c *testCollector) finish(exit int, logTruncated bool) testSummary {
	s := &c.sum
	s.ExitCode, s.LogTruncated = exit, logTruncated
	sort.Strings(s.SkippedTests)
	sort.Strings(s.PackagesWithoutTests)
	if exit != 0 {
		s.Blocking = append(s.Blocking, fmt.Sprintf("go test exited %d", exit))
	}
	if n := s.Tests.Failed + s.Packages.Failed; n > 0 {
		s.Blocking = append(s.Blocking, fmt.Sprintf("%d failed tests or packages", n))
	}
	if s.Tests.Passed+s.Tests.Failed == 0 {
		s.Blocking = append(s.Blocking, "no test ran")
	}
	if s.Strict && s.Tests.Skipped > 0 {
		s.Blocking = append(s.Blocking, fmt.Sprintf("%d skipped tests; a required qualification that did not run blocks", s.Tests.Skipped))
	}
	for req := range c.required {
		if !c.seenPass[req] {
			s.RequiredMissing = append(s.RequiredMissing, req)
		}
	}
	sort.Strings(s.RequiredMissing)
	if len(s.RequiredMissing) > 0 {
		s.Blocking = append(s.Blocking, fmt.Sprintf("%d required tests were never observed passing: %s", len(s.RequiredMissing), strings.Join(s.RequiredMissing, ", ")))
	}
	s.Passed = len(s.Blocking) == 0
	return *s
}

// boundedBuffer keeps the first limit bytes written to it.
type boundedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room < len(p) {
		b.truncated = true
		if room > 0 {
			b.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *boundedBuffer) String() string {
	if b.truncated {
		return b.buf.String() + "...[truncated]"
	}
	return b.buf.String()
}

// boundedFile writes at most limit bytes to a file and drops the rest.
type boundedFile struct {
	f         *os.File
	limit     int64
	written   int64
	truncated bool
}

func (b *boundedFile) Write(p []byte) (int, error) {
	room := b.limit - b.written
	if room <= 0 {
		b.truncated = b.truncated || len(p) > 0
		return len(p), nil
	}
	chunk := p
	if int64(len(chunk)) > room {
		chunk = chunk[:room]
		b.truncated = true
	}
	n, err := b.f.Write(chunk)
	b.written += int64(n)
	if err != nil {
		return n, err
	}
	return len(p), nil
}

func runGoTest(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlags("gotest", stderr)
	name := fs.String("name", "", "evidence name, for example unit or race")
	outDir := fs.String("out-dir", "", "evidence directory")
	strict := fs.Bool("strict", false, "block on skipped tests")
	requireTests := fs.String("require-tests", "", "comma-separated pkg#Test identities that must be observed passing, regardless of overall pass/fail counts")
	goBin := fs.String("go", "go", "go command")
	timeout := fs.Duration("timeout", 40*time.Minute, "overall time limit")
	maxLog := fs.Int64("max-log-bytes", 8<<20, "size limit of the retained event log")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if !evidenceNamePattern.MatchString(*name) || *outDir == "" || fs.NArg() == 0 || *maxLog < 1 {
		return faultf(codeInvalidInput, "usage: gotest -name <name> -out-dir <dir> [-strict] -- <go test arguments>")
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
	testArgs := append([]string{"test", "-json"}, fs.Args()...)
	cmd := exec.CommandContext(ctx, *goBin, testArgs...)
	errBuf := &boundedBuffer{limit: maxStderrBytes}
	cmd.Stderr = errBuf
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = logFile.Close()
		return fmt.Errorf("attaching to go test: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return faultf(codePrerequisiteMissing, "starting %s: %v", *goBin, err)
	}
	col := newTestCollector(*name, fs.Args(), *strict, splitCSV(*requireTests))
	readErr := col.consume(io.TeeReader(pipe, log))
	if readErr != nil {
		// Keep draining so the child is not blocked on a full pipe.
		_, _ = io.Copy(io.Discard, pipe)
	}
	waitErr := cmd.Wait()
	if err := logFile.Close(); err != nil {
		return fmt.Errorf("closing event log: %w", err)
	}
	exit := 0
	var exitErr *exec.ExitError
	switch {
	case errors.As(waitErr, &exitErr):
		exit = exitErr.ExitCode()
	case waitErr != nil:
		exit = -1
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

// printSummary writes the bounded console report; full bounded detail stays
// in the retained evidence files.
func printSummary(w io.Writer, s testSummary) {
	say(w, "%s: tests %d passed, %d failed, %d skipped; packages %d passed, %d failed, %d without tests",
		s.Name, s.Tests.Passed, s.Tests.Failed, s.Tests.Skipped, s.Packages.Passed, s.Packages.Failed, s.Packages.Skipped)
	const maxPrinted = 20
	for i, f := range s.Failed {
		if i == maxPrinted {
			say(w, "... %d more failures in the summary file", len(s.Failed)-maxPrinted)
			break
		}
		say(w, "FAIL %s %s\n%s", f.Package, f.Test, boundString(f.Output, 2<<10))
	}
	for _, m := range s.RequiredMissing {
		say(w, "MISSING required test %s", m)
	}
	for i, t := range s.SkippedTests {
		if i == maxPrinted {
			say(w, "... %d more skipped tests in the summary file", len(s.SkippedTests)-maxPrinted)
			break
		}
		say(w, "SKIP %s", t)
	}
}
