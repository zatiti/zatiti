package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func needsJSON(t *testing.T, override map[string]string) []byte {
	t.Helper()
	needs := map[string]map[string]string{}
	for _, g := range requiredReleaseGates {
		needs[g] = map[string]string{"result": "success"}
	}
	for k, v := range override {
		if v == "" {
			delete(needs, k)
			continue
		}
		needs[k] = map[string]string{"result": v}
	}
	data, err := json.Marshal(needs)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func faultCode(err error) string {
	var f *fault
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}

func TestEvaluateGates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		override  map[string]string
		qualified bool
		blocking  string
	}{
		{"every gate succeeded", nil, true, ""},
		{"failed gate blocks", map[string]string{"test": "failure"}, false, "test: failure"},
		{"skipped gate blocks", map[string]string{"qualification": "skipped"}, false, "qualification: skipped"},
		{"cancelled gate blocks", map[string]string{"flutter": "cancelled"}, false, "flutter: cancelled"},
		{"omitted gate blocks", map[string]string{"static": ""}, false, "static: missing"},
		{"unrecognized result blocks", map[string]string{"build": "neutral"}, false, "build: neutral"},
		{"extra jobs do not qualify anything", map[string]string{"bonus": "failure"}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v, err := evaluateGates(requiredReleaseGates, needsJSON(t, tc.override), "v1.2.3")
			if err != nil {
				t.Fatalf("evaluateGates: %v", err)
			}
			if v.Qualified != tc.qualified {
				t.Errorf("Qualified = %v, want %v (%v)", v.Qualified, tc.qualified, v.Blocking)
			}
			if tc.blocking != "" && !contains(v.Blocking, tc.blocking) {
				t.Errorf("Blocking = %v, want %q", v.Blocking, tc.blocking)
			}
			if len(v.Gates) != len(requiredReleaseGates) {
				t.Errorf("reported %d gates, want %d", len(v.Gates), len(requiredReleaseGates))
			}
		})
	}
}

func TestEvaluateGatesFailsClosedOnAbsentInputs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, needs, version string
	}{
		{"absent version", string(needsJSON(t, nil)), ""},
		{"branch name as version", string(needsJSON(t, nil)), "main"},
		{"absent needs", "", "v1.0.0"},
		{"needs is not an object", "[]", "v1.0.0"},
		{"needs is not JSON", "{", "v1.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v, err := evaluateGates(requiredReleaseGates, []byte(tc.needs), tc.version)
			if faultCode(err) != codeInvalidInput {
				t.Fatalf("error = %v, want %s", err, codeInvalidInput)
			}
			if v.Qualified {
				t.Error("verdict is qualified despite invalid inputs")
			}
		})
	}
}

func TestRunGateWritesVerdictAndBlocks(t *testing.T) {
	out := filepath.Join(t.TempDir(), "evidence", "verdict.json")
	t.Setenv("NEEDS_JSON", string(needsJSON(t, map[string]string{"qualification": "failure"})))
	t.Setenv("RELEASE_VERSION", "v0.1.0")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"gate", "-out", out}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1; stderr %s", code, stderr.String())
	}
	var v verdict
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the blocking verdict was not retained: %v", err)
	}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	if v.Qualified || v.Schema != verdictSchema || !contains(v.Blocking, "qualification: failure") || v.Notice != verdictNotice {
		t.Errorf("verdict = %+v", v)
	}

	t.Setenv("NEEDS_JSON", string(needsJSON(t, nil)))
	if code := run(context.Background(), []string{"gate", "-out", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr %s", code, stderr.String())
	}
	t.Setenv("RELEASE_VERSION", "")
	if code := run(context.Background(), []string{"gate", "-out", out}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code with absent version = %d, want 2", code)
	}
}

func TestResolveVersion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, event, refType, refName, input, want, code string
	}{
		{"tag push", "push", "tag", "v1.2.3", "", "v1.2.3", ""},
		{"prerelease tag push", "push", "tag", "v1.2.3-rc.1", "", "v1.2.3-rc.1", ""},
		{"dispatch from the tag", "workflow_dispatch", "tag", "v1.2.3", "v1.2.3", "v1.2.3", ""},
		{"dispatch without version", "workflow_dispatch", "tag", "v1.2.3", "", "", codeInvalidInput},
		{"dispatch version mismatch", "workflow_dispatch", "tag", "v1.2.3", "v1.2.4", "", codeInvalidInput},
		{"dispatch from a branch", "workflow_dispatch", "branch", "main", "v1.2.3", "", codeInvalidInput},
		{"branch push", "push", "branch", "main", "", "", codeInvalidInput},
		{"non-version tag", "push", "tag", "nightly", "", "", codeInvalidInput},
		{"leading zero", "push", "tag", "v01.2.3", "", "", codeInvalidInput},
		{"pull request", "pull_request", "tag", "v1.2.3", "", "", codeInvalidInput},
		{"absent event", "", "", "", "", "", codeInvalidInput},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveVersion(tc.event, tc.refType, tc.refName, tc.input)
			if got != tc.want || faultCode(err) != tc.code {
				t.Fatalf("resolveVersion = %q, %v; want %q, code %q", got, err, tc.want, tc.code)
			}
		})
	}
}

// gitRepo creates an isolated repository with one commit on main and returns
// its directory and a runner for further git commands.
func gitRepo(t *testing.T) (string, func(args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required: %v", err)
	}
	dir := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		full := append([]string{"-c", "user.name=ci", "-c", "user.email=ci@example.invalid", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)
		cmd := exec.Command("git", full...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-q", "-m", "first")
	return dir, runGit
}

func TestVerifyReviewed(t *testing.T) {
	t.Parallel()
	dir, runGit := gitRepo(t)
	runGit("update-ref", "refs/remotes/origin/main", "HEAD")
	runGit("tag", "v1.0.0")
	ctx := context.Background()
	const reviewed = "refs/remotes/origin/main"

	head, err := verifyReviewed(ctx, dir, "v1.0.0", reviewed)
	if err != nil || head != runGit("rev-parse", "HEAD") {
		t.Fatalf("reviewed tag: %q, %v", head, err)
	}
	if _, err := verifyReviewed(ctx, dir, "v9.9.9", reviewed); faultCode(err) != codePrerequisiteMissing {
		t.Errorf("absent tag: %v", err)
	}
	if _, err := verifyReviewed(ctx, dir, "v1.0.0", "refs/remotes/origin/absent"); faultCode(err) != codePrerequisiteMissing {
		t.Errorf("absent reviewed ref: %v", err)
	}

	// A tag on a commit that main does not contain must not qualify.
	runGit("checkout", "-q", "-b", "unreviewed")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("commit", "-q", "-am", "unreviewed")
	runGit("tag", "v2.0.0")
	if _, err := verifyReviewed(ctx, dir, "v2.0.0", reviewed); faultCode(err) != codeVerificationFailed || !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("unreviewed tag: %v", err)
	}
	// HEAD must be the tagged commit itself.
	if _, err := verifyReviewed(ctx, dir, "v1.0.0", reviewed); faultCode(err) != codeVerificationFailed || !strings.Contains(err.Error(), "is not the commit tagged") {
		t.Errorf("HEAD differs from tag: %v", err)
	}
}

func TestRunInputs(t *testing.T) {
	dir, runGit := gitRepo(t)
	runGit("update-ref", "refs/remotes/origin/main", "HEAD")
	runGit("tag", "v1.0.0")
	tmp := t.TempDir()
	out, stepOut := filepath.Join(tmp, "inputs.json"), filepath.Join(tmp, "step-output")
	args := []string{"inputs", "-dir", dir, "-out", out, "-output-file", stepOut}
	var stdout, stderr bytes.Buffer

	t.Setenv("GITHUB_EVENT_NAME", "workflow_dispatch")
	t.Setenv("GITHUB_REF_TYPE", "tag")
	t.Setenv("GITHUB_REF_NAME", "v1.0.0")
	t.Setenv("INPUT_VERSION", "")
	if code := run(context.Background(), args, &stdout, &stderr); code != 2 {
		t.Fatalf("absent version input: exit %d, want 2", code)
	}
	if _, err := os.Stat(stepOut); err == nil {
		t.Fatal("a version output was written despite absent inputs")
	}

	t.Setenv("INPUT_VERSION", "v1.0.0")
	if code := run(context.Background(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("valid dispatch: exit %d; %s", code, stderr.String())
	}
	got, err := os.ReadFile(stepOut)
	if err != nil || string(got) != "version=v1.0.0\n" {
		t.Fatalf("step output = %q, %v", got, err)
	}
	var rec releaseInputs
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Version != "v1.0.0" || rec.Commit != runGit("rev-parse", "HEAD") || rec.Schema != inputsSchema {
		t.Errorf("inputs evidence = %+v", rec)
	}
}

func TestRunRejectsUnknownCommandsAndFlags(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	for _, args := range [][]string{nil, {"publish"}, {"lint", "-no-such-flag"}, {"gate"}, {"lock"}, {"require", "-kind", "other", "."}} {
		if code := run(context.Background(), args, &stdout, &stderr); code != 2 {
			t.Errorf("run(%v) = %d, want 2", args, code)
		}
	}
}
