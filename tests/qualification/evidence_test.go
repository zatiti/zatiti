package qualification_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Case statuses. A record is written for every named case this package
// owns a harness identity for; "not_run" carries the named prerequisite
// that blocks it and blocks the release claims that depend on it.
const (
	statusPassed = "passed"
	statusFailed = "failed"
	statusNotRun = "not_run"
)

// record is one retained case result: what was expected, what was
// observed, under exactly which source, tool and platform versions.
type record struct {
	Case       string            `json:"case"`
	Gate       string            `json:"gate"`
	Status     string            `json:"status"`
	Reason     string            `json:"reason,omitempty"`
	Expected   []string          `json:"expected"`
	Observed   []string          `json:"observed"`
	Versions   map[string]string `json:"versions"`
	StartedAt  time.Time         `json:"started_at"`
	FinishedAt time.Time         `json:"finished_at"`
	Evidence   map[string]any    `json:"evidence,omitempty"`
}

var (
	evidenceDir string
	evidenceMu  sync.Mutex
	evidenceLog []record
)

// openEvidence resolves the evidence directory once per run.
func openEvidence() error {
	if dir := os.Getenv("ZATITI_QUALIFICATION_EVIDENCE"); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		evidenceDir = dir
		return nil
	}
	dir, err := os.MkdirTemp("", "zatiti-qualification-")
	if err != nil {
		return err
	}
	evidenceDir = dir
	return nil
}

// caseRun is the harness identity of one named case. Every observation is
// appended as it is made; the record is written when the case ends, in
// whichever state the test left it.
type caseRun struct {
	t   *testing.T
	rec record
}

// beginCase opens the record for a named case and arranges for it to be
// written at the end of the test, failed if the test failed.
func beginCase(t *testing.T, id, gate string, expected ...string) *caseRun {
	t.Helper()
	c := &caseRun{t: t, rec: record{
		Case: id, Gate: gate, Status: statusPassed, Expected: expected,
		Observed: []string{}, Versions: baseVersions(), StartedAt: time.Now().UTC(),
		Evidence: map[string]any{},
	}}
	t.Cleanup(func() {
		if c.rec.Status == statusPassed && t.Failed() {
			c.rec.Status = statusFailed
		}
		c.rec.FinishedAt = time.Now().UTC()
		if err := c.write(); err != nil {
			t.Errorf("writing evidence for %s: %v", id, err)
		}
	})
	return c
}

// observe appends one observation in the order it was made.
func (c *caseRun) observe(format string, args ...any) {
	c.rec.Observed = append(c.rec.Observed, fmt.Sprintf(format, args...))
}

// attach retains a named piece of evidence (a document, a count, a log).
func (c *caseRun) attach(key string, value any) { c.rec.Evidence[key] = value }

// version records one tool or source version the result depends on.
func (c *caseRun) version(tool, value string) { c.rec.Versions[tool] = value }

// notRun ends the case as not run for a named reason and skips the test.
// A not-run case blocks the release claims that depend on it; it never
// reads as a pass.
func (c *caseRun) notRun(format string, args ...any) {
	c.t.Helper()
	c.rec.Status = statusNotRun
	c.rec.Reason = fmt.Sprintf(format, args...)
	c.t.Skipf("%s not run: %s", c.rec.Case, c.rec.Reason)
}

// fail ends the case failed with retained evidence of the exact mismatch.
func (c *caseRun) fail(format string, args ...any) {
	c.t.Helper()
	c.rec.Status = statusFailed
	c.rec.Reason = fmt.Sprintf(format, args...)
	c.t.Fatalf("%s: %s", c.rec.Case, c.rec.Reason)
}

func (c *caseRun) write() error {
	raw, err := json.MarshalIndent(c.rec, "", "  ")
	if err != nil {
		return err
	}
	name := strings.NewReplacer("/", "_", " ", "_").Replace(c.rec.Case) + ".json"
	if err := os.WriteFile(filepath.Join(evidenceDir, name), raw, 0o600); err != nil {
		return err
	}
	evidenceMu.Lock()
	evidenceLog = append(evidenceLog, c.rec)
	evidenceMu.Unlock()
	return nil
}

// ---------- versions ----------

var (
	versionsOnce sync.Once
	versionsBase map[string]string
)

// baseVersions is the source/toolchain/platform identity every record
// carries: the module's git revision, the Go toolchain, the host platform.
func baseVersions() map[string]string {
	versionsOnce.Do(func() {
		versionsBase = map[string]string{
			"go":       runtime.Version(),
			"platform": runtime.GOOS + "/" + runtime.GOARCH,
		}
		if root, err := moduleRoot(); err == nil {
			versionsBase["module_root_go_mod"] = fileDigest(filepath.Join(root, "go.mod"))
			if rev, err := gitRevision(root); err == nil {
				versionsBase["source_revision"] = rev
			} else {
				versionsBase["source_revision"] = "unavailable: " + err.Error()
			}
		}
	})
	out := make(map[string]string, len(versionsBase))
	for k, v := range versionsBase {
		out[k] = v
	}
	return out
}

// moduleRoot walks up from the working directory to the module's go.mod.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found above the working directory")
		}
		dir = parent
	}
}

// gitRevision reports HEAD plus a dirty marker when the tree has local
// modifications, so evidence never claims a clean revision it did not run.
func gitRevision(root string) (string, error) {
	head, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	rev := strings.TrimSpace(string(head))
	status, err := exec.Command("git", "-C", root, "status", "--porcelain", "--untracked-files=no").Output()
	if err == nil && strings.TrimSpace(string(status)) != "" {
		rev += "-dirty"
	}
	return rev, nil
}

// fileDigest is the SHA-256 of one file, or a named absence.
func fileDigest(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "unavailable: " + err.Error()
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// toolVersion runs a version probe and returns its first line, or the
// reason it is unavailable. It never fails the case: a missing tool is a
// recorded prerequisite, not an error in the harness.
func toolVersion(name string, args ...string) (string, bool) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "not installed", false
	}
	cmd := exec.Command(path, args...)
	out, err := cmd.CombinedOutput()
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if err != nil {
		return fmt.Sprintf("%s (probe failed: %v)", line, err), false
	}
	return line, true
}

// ---------- release report ----------

// gateRow is one Z01-Z21/journey/qualification gate in the release report.
type gateRow struct {
	Gate    string   `json:"gate"`
	Status  string   `json:"status"`
	Cases   []string `json:"cases"`
	Reasons []string `json:"reasons,omitempty"`
}

// releaseReport is QUALIFICATION.release_evidence: every retained case
// result of this run audited against the gates, with an explicit verdict
// on whether a release may be claimed from this evidence alone (never).
type releaseReport struct {
	GeneratedAt      time.Time         `json:"generated_at"`
	Versions         map[string]string `json:"versions"`
	Cases            []record          `json:"cases"`
	Gates            []gateRow         `json:"gates"`
	ReleaseClaimable bool              `json:"release_claimable"`
	Blocking         []string          `json:"blocking"`
}

// gates the report audits. Cases integration owns are reported as owned
// elsewhere so this report never counts an absent case as a pass.
var reportGates = []string{
	"Z01", "Z02", "Z03", "Z04", "Z05", "Z06", "Z07", "Z08", "Z09", "Z10", "Z11",
	"Z12", "Z13", "Z14", "Z15", "Z16", "Z17", "Z18", "Z19", "Z20", "Z21",
	"JOURNEY", "QUALIFICATION",
}

func writeReleaseReport() (string, error) {
	evidenceMu.Lock()
	cases := append([]record(nil), evidenceLog...)
	evidenceMu.Unlock()
	sort.Slice(cases, func(i, j int) bool { return cases[i].Case < cases[j].Case })

	byGate := map[string]*gateRow{}
	for _, g := range reportGates {
		byGate[g] = &gateRow{Gate: g, Status: "no_case_executed_here", Cases: []string{}}
	}
	for _, c := range cases {
		row, ok := byGate[c.Gate]
		if !ok {
			row = &gateRow{Gate: c.Gate, Status: "no_case_executed_here", Cases: []string{}}
			byGate[c.Gate] = row
		}
		row.Cases = append(row.Cases, c.Case+": "+c.Status)
		switch c.Status {
		case statusFailed:
			row.Status = statusFailed
			row.Reasons = append(row.Reasons, c.Case+": "+c.Reason)
		case statusNotRun:
			if row.Status != statusFailed {
				row.Status = statusNotRun
			}
			row.Reasons = append(row.Reasons, c.Case+": "+c.Reason)
		case statusPassed:
			if row.Status == "no_case_executed_here" {
				row.Status = "passed_cases_only"
			}
		}
	}
	report := releaseReport{
		GeneratedAt: time.Now().UTC(), Versions: baseVersions(), Cases: cases,
		ReleaseClaimable: false,
	}
	for _, g := range reportGates {
		row := byGate[g]
		report.Gates = append(report.Gates, *row)
		switch row.Status {
		case statusFailed, statusNotRun:
			report.Blocking = append(report.Blocking, g+": "+row.Status)
		case "no_case_executed_here":
			report.Blocking = append(report.Blocking, g+": no case executed by this package; integration owns its cases and must report them")
		case "passed_cases_only":
			report.Blocking = append(report.Blocking, g+": only the cases this package executed passed; the gate's remaining cases are owned elsewhere")
		}
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(evidenceDir, "release-report.json")
	return path, os.WriteFile(path, raw, 0o600)
}
