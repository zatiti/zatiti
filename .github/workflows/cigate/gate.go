package main

import (
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
	"strings"
)

const (
	verdictSchema = "zatiti.ci.release_verdict/v1"
	inputsSchema  = "zatiti.ci.release_inputs/v1"
	verdictNotice = "Qualification verdict only. This workflow publishes nothing and grants no release permission."
)

var versionPattern = regexp.MustCompile(`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$`)

// gateResult is the observed state of one required gate.
type gateResult struct {
	Name     string `json:"name"`
	Result   string `json:"result"` // success | failure | cancelled | skipped | missing | <unrecognized>
	Blocking bool   `json:"blocking"`
}

// verdict is the retained release qualification decision.
type verdict struct {
	Schema    string       `json:"schema"`
	Version   string       `json:"version"`
	Qualified bool         `json:"qualified"`
	Gates     []gateResult `json:"gates"`
	Blocking  []string     `json:"blocking"`
	Notice    string       `json:"notice"`
}

// evaluateGates decides qualification from the GitHub "needs" context. Only
// a required gate that is present with result "success" passes; a failed,
// cancelled, skipped, unrecognized or omitted gate blocks.
func evaluateGates(required []string, needsJSON []byte, version string) (verdict, error) {
	v := verdict{Schema: verdictSchema, Version: version, Notice: verdictNotice, Blocking: []string{}}
	if !versionPattern.MatchString(version) {
		return v, faultf(codeInvalidInput, "release version %q is absent or not a vMAJOR.MINOR.PATCH tag", version)
	}
	if len(bytes.TrimSpace(needsJSON)) == 0 {
		return v, faultf(codeInvalidInput, "needs context is absent")
	}
	var needs map[string]struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(needsJSON, &needs); err != nil {
		return v, faultf(codeInvalidInput, "needs context is not a JSON object of job results: %v", err)
	}
	for _, name := range required {
		g := gateResult{Name: name, Result: "missing"}
		if n, ok := needs[name]; ok && n.Result != "" {
			g.Result = n.Result
		}
		g.Blocking = g.Result != "success"
		if g.Blocking {
			v.Blocking = append(v.Blocking, fmt.Sprintf("%s: %s", g.Name, g.Result))
		}
		v.Gates = append(v.Gates, g)
	}
	v.Qualified = len(v.Blocking) == 0
	return v, nil
}

func runGate(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlags("gate", stderr)
	needsEnv := fs.String("needs-env", "NEEDS_JSON", "environment variable holding toJSON(needs)")
	versionEnv := fs.String("version-env", "RELEASE_VERSION", "environment variable holding the resolved release version")
	out := fs.String("out", "", "verdict file to write")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *out == "" {
		return faultf(codeInvalidInput, "-out is required")
	}
	v, evalErr := evaluateGates(requiredReleaseGates, []byte(os.Getenv(*needsEnv)), os.Getenv(*versionEnv))
	if err := writeJSON(*out, v); err != nil {
		return err
	}
	for _, g := range v.Gates {
		say(stdout, "gate %-14s %s", g.Name, g.Result)
	}
	if evalErr != nil {
		return evalErr
	}
	if !v.Qualified {
		return faultf(codeVerificationFailed, "release %s is not qualified: %s", v.Version, strings.Join(v.Blocking, "; "))
	}
	say(stdout, "release %s: every required gate succeeded. %s", v.Version, verdictNotice)
	return nil
}

// releaseInputs is the retained record of how the release version resolved.
type releaseInputs struct {
	Schema      string `json:"schema"`
	Event       string `json:"event"`
	RefType     string `json:"ref_type"`
	RefName     string `json:"ref_name"`
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	ReviewedRef string `json:"reviewed_ref"`
}

// resolveVersion applies the release trigger rules. Qualification runs only
// for a version tag: a tag push, or a manual dispatch started from the tag
// whose version input names that same tag.
func resolveVersion(event, refType, refName, inputVersion string) (string, error) {
	if refType != "tag" {
		return "", faultf(codeInvalidInput, "release qualification must run from a version tag, got ref type %q (%q)", refType, refName)
	}
	if !versionPattern.MatchString(refName) {
		return "", faultf(codeInvalidInput, "tag %q is not a vMAJOR.MINOR.PATCH version", refName)
	}
	switch event {
	case "push":
		if inputVersion != "" {
			return "", faultf(codeInvalidInput, "a tag push carries no version input, got %q", inputVersion)
		}
	case "workflow_dispatch":
		if inputVersion == "" {
			return "", faultf(codeInvalidInput, "the version input is absent")
		}
		if inputVersion != refName {
			return "", faultf(codeInvalidInput, "version input %q does not match the tag %q the run started from", inputVersion, refName)
		}
	default:
		return "", faultf(codeInvalidInput, "event %q cannot start release qualification", event)
	}
	return refName, nil
}

// git runs one read-only git command in dir and returns trimmed stdout.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, boundString(stderr.String(), 512))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// verifyReviewed requires that HEAD is the tagged commit and that it is
// reachable from the reviewed branch, so a tag on an unreviewed branch cannot
// be qualified.
func verifyReviewed(ctx context.Context, dir, version, reviewedRef string) (string, error) {
	head, err := git(ctx, dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", faultf(codePrerequisiteMissing, "cannot resolve the checked-out commit: %v", err)
	}
	tagged, err := git(ctx, dir, "rev-parse", "--verify", "refs/tags/"+version+"^{commit}")
	if err != nil {
		return "", faultf(codePrerequisiteMissing, "tag %s is not present in the checkout: %v", version, err)
	}
	if tagged != head {
		return "", faultf(codeVerificationFailed, "checked-out commit %s is not the commit tagged %s (%s)", head, version, tagged)
	}
	if _, err := git(ctx, dir, "rev-parse", "--verify", reviewedRef+"^{commit}"); err != nil {
		return "", faultf(codePrerequisiteMissing, "reviewed ref %s is not present; check out with full history: %v", reviewedRef, err)
	}
	if _, err := git(ctx, dir, "merge-base", "--is-ancestor", head, reviewedRef); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", faultf(codeVerificationFailed, "tagged commit %s is not reachable from %s; unreviewed commits cannot be qualified", head, reviewedRef)
		}
		return "", faultf(codePrerequisiteMissing, "cannot compare the tagged commit with %s: %v", reviewedRef, err)
	}
	return head, nil
}

func runInputs(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlags("inputs", stderr)
	dir := fs.String("dir", ".", "repository checkout")
	reviewed := fs.String("reviewed-ref", "refs/remotes/origin/main", "ref the tagged commit must be reachable from")
	versionEnv := fs.String("version-env", "INPUT_VERSION", "environment variable holding the dispatch version input")
	out := fs.String("out", "", "evidence file to write")
	outputFile := fs.String("output-file", os.Getenv("GITHUB_OUTPUT"), "step output file that receives version=<tag>")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *out == "" || *outputFile == "" {
		return faultf(codeInvalidInput, "-out and -output-file (or GITHUB_OUTPUT) are required")
	}
	rec := releaseInputs{
		Schema:      inputsSchema,
		Event:       os.Getenv("GITHUB_EVENT_NAME"),
		RefType:     os.Getenv("GITHUB_REF_TYPE"),
		RefName:     os.Getenv("GITHUB_REF_NAME"),
		ReviewedRef: *reviewed,
	}
	version, err := resolveVersion(rec.Event, rec.RefType, rec.RefName, os.Getenv(*versionEnv))
	if err != nil {
		return err
	}
	commit, err := verifyReviewed(ctx, *dir, version, *reviewed)
	if err != nil {
		return err
	}
	rec.Version, rec.Commit = version, commit
	if err := writeJSON(*out, rec); err != nil {
		return err
	}
	f, err := os.OpenFile(*outputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening step output file: %w", err)
	}
	if _, err := fmt.Fprintf(f, "version=%s\n", version); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing step output: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing step output file: %w", err)
	}
	say(stdout, "release qualification inputs: version %s at %s, reachable from %s", version, commit, *reviewed)
	return nil
}

// qualificationReport is the subset of tests/qualification's
// release-report.json (QUALIFICATION.release_evidence) this command reads.
// It never imports the qualification package (not an allowed import from
// this root); this struct is the coordinated read-only contract with that
// file's committed JSON shape (tests/qualification/evidence_test.go).
type qualificationReport struct {
	Versions map[string]string `json:"versions"`
	Cases    []struct {
		Case     string                     `json:"case"`
		Status   string                     `json:"status"`
		Reason   string                     `json:"reason"`
		Versions map[string]string          `json:"versions"`
		Expected []string                   `json:"expected"`
		Observed []string                   `json:"observed"`
		Evidence map[string]json.RawMessage `json:"evidence"`
	} `json:"cases"`
	Gates []struct {
		Gate   string `json:"gate"`
		Status string `json:"status"`
	} `json:"gates"`
}

const (
	qualificationEvidenceSchema = "zatiti.ci.qualification_evidence/v1"
	qualStatusPassed            = "passed"
	qualGatePassedCasesOnly     = "passed_cases_only"
)

// qualificationVerdict is the retained decision of whether one qualification
// evidence run enumerates every required case, fresh from the checked-out
// source, with every case observed to have passed.
type qualificationVerdict struct {
	Schema      string   `json:"schema"`
	Qualified   bool     `json:"qualified"`
	Revision    string   `json:"revision"`
	GoModSHA256 string   `json:"go_mod_sha256"`
	Blocking    []string `json:"blocking"`
}

// evaluateQualificationEvidence enumerates every required case instead of
// trusting that tests/qualification's own process exit code alone proves
// completeness: a case whose test function was deleted outright leaves no
// pass, fail or skip event for go test to report, but it does leave the
// gate at less than passed_cases_only in the retained report, which this
// function still catches. It also refuses evidence generated from a
// different commit or a changed go.mod, so a stale report from an earlier
// run or a different revision can never stand in for the checked-out tree.
func evaluateQualificationEvidence(report qualificationReport, revision, goModDigest string, requiredGates []string) qualificationVerdict {
	return evaluateQualificationEvidenceForPlatform(report, revision, goModDigest, requiredGates, nil, "")
}

func evaluateQualificationEvidenceForPlatform(report qualificationReport, revision, goModDigest string, requiredGates, requiredCases []string, platform string) qualificationVerdict {
	v := qualificationVerdict{Schema: qualificationEvidenceSchema, Revision: revision, GoModSHA256: goModDigest, Blocking: []string{}}
	if got := report.Versions["source_revision"]; got != revision {
		v.Blocking = append(v.Blocking, fmt.Sprintf("stale evidence: report source revision %q does not match the checked-out %q", got, revision))
	}
	if got := report.Versions["module_root_go_mod"]; got != goModDigest {
		v.Blocking = append(v.Blocking, fmt.Sprintf("stale evidence: report go.mod digest %q does not match the checked-out %q", got, goModDigest))
	}
	if platform != "" && report.Versions["platform"] != platform {
		v.Blocking = append(v.Blocking, fmt.Sprintf("platform %q does not match required %q", report.Versions["platform"], platform))
	}
	byCase := map[string]int{}
	for _, c := range report.Cases {
		byCase[c.Case]++
		if byCase[c.Case] > 1 {
			v.Blocking = append(v.Blocking, fmt.Sprintf("case %s: duplicate evidence", c.Case))
		}
		if platform != "" && c.Versions["platform"] != platform {
			v.Blocking = append(v.Blocking, fmt.Sprintf("case %s: platform %q does not match %q", c.Case, c.Versions["platform"], platform))
		}
		if platform != "" && (c.Versions["source_revision"] != revision || c.Versions["module_root_go_mod"] != goModDigest) {
			v.Blocking = append(v.Blocking, fmt.Sprintf("case %s: stale source or module evidence", c.Case))
		}
		if c.Status == qualStatusPassed {
			continue
		}
		msg := fmt.Sprintf("case %s: %s", c.Case, c.Status)
		if c.Reason != "" {
			msg += " (" + c.Reason + ")"
		}
		v.Blocking = append(v.Blocking, msg)
	}
	for _, want := range requiredCases {
		if byCase[want] != 1 {
			v.Blocking = append(v.Blocking, fmt.Sprintf("case %s: required case produced no unique evidence", want))
		}
		for _, c := range report.Cases {
			if c.Case != want {
				continue
			}
			if len(c.Expected) == 0 || len(c.Observed) == 0 || len(c.Evidence) == 0 {
				v.Blocking = append(v.Blocking, fmt.Sprintf("case %s: missing expected, observed, or linked evidence", want))
			}
			if marker, ok := c.Evidence["synthetic"]; ok && string(marker) == "true" {
				v.Blocking = append(v.Blocking, fmt.Sprintf("case %s: synthetic fixture cannot qualify the release", want))
			}
		}
	}
	byGate := map[string]string{}
	for _, g := range report.Gates {
		byGate[g.Gate] = g.Status
	}
	for _, want := range requiredGates {
		status, ok := byGate[want]
		switch {
		case !ok:
			v.Blocking = append(v.Blocking, fmt.Sprintf("%s: required gate produced no evidence in this report", want))
		case status != qualGatePassedCasesOnly:
			v.Blocking = append(v.Blocking, fmt.Sprintf("%s: %s", want, status))
		}
	}
	v.Qualified = len(v.Blocking) == 0
	return v
}

func runQualEvidence(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fs := newFlags("qualevidence", stderr)
	reportPath := fs.String("report", "", "tests/qualification release-report.json")
	root := fs.String("root", ".", "repository checkout")
	revision := fs.String("revision", os.Getenv("GITHUB_SHA"), "commit the evidence must have been generated from")
	requireGate := fs.String("require-gate", "", "comma-separated gate names that must read passed_cases_only in the report")
	requireCase := fs.String("require-case", "", "comma-separated case names that must have unique passed evidence")
	platform := fs.String("platform", "", "required report and per-case GOOS/GOARCH platform")
	out := fs.String("out", "", "verdict file to write")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *reportPath == "" || *revision == "" || *out == "" {
		return faultf(codeInvalidInput, "-report, -revision (or GITHUB_SHA) and -out are required")
	}
	data, err := os.ReadFile(*reportPath)
	if err != nil {
		return faultf(codePrerequisiteMissing, "reading the qualification release report: %v", err)
	}
	var report qualificationReport
	if err := json.Unmarshal(data, &report); err != nil {
		return faultf(codeInvalidInput, "qualification release report is not valid JSON: %v", err)
	}
	goModDigest, _, err := fileSHA256(under(*root, "go.mod"))
	if err != nil {
		return faultf(codePrerequisiteMissing, "hashing go.mod: %v", err)
	}
	v := evaluateQualificationEvidenceForPlatform(report, *revision, goModDigest, splitCSV(*requireGate), splitCSV(*requireCase), *platform)
	if err := writeJSON(*out, v); err != nil {
		return err
	}
	for _, b := range v.Blocking {
		say(stdout, "%v", b)
	}
	if !v.Qualified {
		return faultf(codeVerificationFailed, "qualification evidence is not release-claimable: %s", strings.Join(v.Blocking, "; "))
	}
	say(stdout, "qualification evidence at revision %s is fresh, and every required gate reports %s", *revision, qualGatePassedCasesOnly)
	return nil
}

// writeJSON writes v as indented JSON, creating parent directories.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating evidence directory: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// boundString truncates s to at most limit bytes with a marker.
func boundString(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "...[truncated]"
}
