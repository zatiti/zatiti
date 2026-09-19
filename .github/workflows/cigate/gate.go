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
