package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const workflowDir = ".."

func readWorkflow(t *testing.T, name string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(workflowDir, name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(src)
}

// TestCommittedWorkflowsPassPolicy is the syntax and policy gate for the
// workflow files actually committed in this directory.
func TestCommittedWorkflowsPassPolicy(t *testing.T) {
	t.Parallel()
	findings, err := lintDir(workflowDir)
	if err != nil {
		t.Fatalf("lintDir: %v", err)
	}
	for _, f := range findings {
		t.Errorf("%s", f)
	}
}

// TestActionlintAcceptsWorkflows cross-checks the committed files with a full
// YAML and workflow-schema implementation. CI sets ZATITI_REQUIRE_ACTIONLINT
// so an absent linter fails there instead of skipping.
func TestActionlintAcceptsWorkflows(t *testing.T) {
	t.Parallel()
	bin, err := exec.LookPath("actionlint")
	if err != nil {
		if os.Getenv("ZATITI_REQUIRE_ACTIONLINT") != "" {
			t.Fatalf("actionlint is required but not installed: %v", err)
		}
		t.Skip("actionlint is not installed; the subset parser and policy still ran")
	}
	for name := range registeredWorkflows {
		cmd := exec.Command(bin, "-no-color", filepath.Join(workflowDir, name))
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			t.Errorf("actionlint %s: %v\n%s", name, err, boundString(out.String(), 4<<10))
		}
	}
}

// TestEveryUsesIsAVerifiedPin checks the raw text independently of the YAML
// parser, so a parser defect cannot hide an unpinned action.
func TestEveryUsesIsAVerifiedPin(t *testing.T) {
	t.Parallel()
	for name := range registeredWorkflows {
		seen := 0
		for i, line := range strings.Split(readWorkflow(t, name), "\n") {
			trimmed := strings.TrimPrefix(strings.TrimSpace(line), "- ")
			if !strings.HasPrefix(trimmed, "uses:") {
				continue
			}
			seen++
			ref, comment, _ := strings.Cut(strings.TrimSpace(strings.TrimPrefix(trimmed, "uses:")), " # ")
			action, sha, _ := strings.Cut(ref, "@")
			p, ok := verifiedPins[action]
			if !ok || p.SHA != sha || p.Version != comment || len(sha) != 40 {
				t.Errorf("%s:%d: %q is not a verified full-commit pin", name, i+1, trimmed)
			}
		}
		if seen == 0 {
			t.Errorf("%s: found no uses lines; the raw scan is broken", name)
		}
	}
}

type mutation struct {
	name     string
	workflow string
	old, new string
	all      bool   // replace every occurrence instead of the first
	rule     string // rule expected among the findings
}

// TestPolicyRejectsMutations mutates the committed workflows one property at
// a time and requires the policy to report the matching rule. Each case
// fails if its "old" text is not found, so the table cannot rot silently.
func TestPolicyRejectsMutations(t *testing.T) {
	t.Parallel()
	const ci, rel = "ci.yml", "release-qualification.yml"
	checkoutPin := "actions/checkout@" + verifiedPins["actions/checkout"].SHA + " # v7.0.1"
	cases := []mutation{
		{"tag instead of commit", ci, checkoutPin, "actions/checkout@v7", false, "pinned-actions"},
		{"unverified commit", ci, checkoutPin, "actions/checkout@" + strings.Repeat("a", 40) + " # v7.0.1", false, "pinned-actions"},
		{"missing version comment", ci, checkoutPin, "actions/checkout@" + verifiedPins["actions/checkout"].SHA, false, "pinned-actions"},
		{"unknown action", ci, checkoutPin, "someone/else@" + strings.Repeat("b", 40) + " # v1.0.0", false, "pinned-actions"},
		{"local action", ci, checkoutPin, "./.github/actions/x", false, "pinned-actions"},
		{"write permission", ci, "  contents: read", "  contents: write", false, "permissions"},
		{"extra permission", ci, "  contents: read", "  contents: read\n  id-token: read", false, "permissions"},
		{"shorthand permission", ci, "permissions:\n  contents: read", "permissions: write-all", false, "permissions"},
		{"no permissions", ci, "permissions:\n  contents: read\n", "", false, "permissions"},
		{"job write permission", ci, "    name: static checks\n", "    name: static checks\n    permissions:\n      packages: write\n", false, "permissions"},
		{"secret reference", ci, "  PYTHONDONTWRITEBYTECODE: \"1\"", "  PYTHONDONTWRITEBYTECODE: \"1\"\n  TOKEN: ${{ secrets.RELEASE_TOKEN }}", false, "no-secrets"},
		{"github token", ci, "  PYTHONDONTWRITEBYTECODE: \"1\"", "  PYTHONDONTWRITEBYTECODE: \"1\"\n  TOKEN: ${{ github.token }}", false, "no-secrets"},
		{"pull_request_target", ci, "  pull_request:\n", "  pull_request_target:\n", false, "triggers"},
		{"unfiltered push", ci, "  push:\n    branches:\n      - main\n", "  push:\n", false, "triggers"},
		{"push to any branch", ci, "      - main\n", "      - '**'\n", false, "triggers"},
		{"credentials persisted", ci, "          persist-credentials: false\n", "", false, "checkout"},
		{"floating toolchain", ci, "          go-version-file: go.mod\n", "          go-version: stable\n", false, "toolchain"},
		{"implicit setup-go cache", ci, "          cache: false\n", "", false, "toolchain"},
		{"lock check removed", ci, "          go run ./.github/workflows/cigate lock -go-version \"$(go env GOVERSION)\"\n", "", false, "toolchain-lock"},
		{"cache key without lock", ci, "-${{ hashFiles('go.mod', 'go.sum', 'docs/implementation/dependencies.lock.json') }}", "-${{ hashFiles('go.sum') }}", false, "cache"},
		{"cache restore keys", ci, "          key: go-test-", "          restore-keys: go-test-\n          key: go-test-", false, "cache"},
		{"floating runner", ci, "    runs-on: ubuntu-24.04", "    runs-on: ubuntu-latest", false, "runner"},
		{"floating matrix runner", ci, "          - macos-15", "          - macos-latest", false, "runner"},
		{"Intel runner omitted from CI", ci, "          - macos-15-intel\n", "", true, "runner"},
		{"Intel runner omitted from release gate", rel, "          - macos-15-intel\n", "", true, "release-gates"},
		{"native architecture check removed", ci, "      - name: Verify native runner architecture\n", "      - name: Skipped architecture check\n", false, "runner-arch"},
		{"Intel architecture check weakened", ci, "macos-15-intel) expected_os=macOS; expected_runner=X64; expected_uname=x86_64", "macos-15-intel) expected_os=macOS; expected_runner=ARM64; expected_uname=arm64", false, "runner-arch"},
		{"no timeout", ci, "    timeout-minutes: 15\n", "", false, "timeout"},
		{"unbounded timeout", ci, "    timeout-minutes: 15\n", "    timeout-minutes: 360\n", false, "timeout"},
		{"continue-on-error job", ci, "    timeout-minutes: 15\n", "    timeout-minutes: 15\n    continue-on-error: true\n", false, "job-keys"},
		{"continue-on-error step", ci, "      - name: Build\n", "      - name: Build\n        continue-on-error: true\n", false, "step-keys"},
		{"conditional job", ci, "    name: build and test\n", "    name: build and test\n    if: github.actor != 'x'\n", false, "job-condition"},
		{"conditional step", ci, "      - name: Build\n", "      - name: Build\n        if: github.event_name == 'push'\n", false, "step-condition"},
		{"always on a run step", ci, "      - name: Build\n", "      - name: Build\n        if: always()\n", false, "step-condition"},
		{"expression in script", ci, "        run: go build ./... ./.github/workflows/...\n", "        run: |\n          go build ./... ./.github/workflows/...\n          echo ${{ github.head_ref }}\n", false, "script-injection"},
		{"unpinned tool", ci, "actionlint@v1.7.12", "actionlint@latest", false, "pinned-tools"},
		{"network fetch", ci, "        run: go build ./... ./.github/workflows/...\n", "        run: |\n          go build ./... ./.github/workflows/...\n          curl -fsSL https://example.invalid/install.sh\n", false, "no-publish"},
		{"release publication", ci, "        run: go build ./... ./.github/workflows/...\n", "        run: |\n          go build ./... ./.github/workflows/...\n          gh release create v1.0.0\n", false, "no-publish"},
		{"tag push from a script", ci, "        run: go build ./... ./.github/workflows/...\n", "        run: |\n          go build ./... ./.github/workflows/...\n          git push origin v1.0.0\n", false, "no-publish"},
		{"evidence not retained on failure", ci, "        if: always()\n", "", false, "evidence"},
		{"unbounded retention", ci, "          retention-days: 14\n", "          retention-days: 400\n", false, "evidence"},
		{"no retention", ci, "          retention-days: 14\n", "", false, "evidence"},
		{"silent empty evidence", ci, "          if-no-files-found: error\n", "          if-no-files-found: warn\n", false, "evidence"},
		{"spec check not first", ci, "      - name: Check the generated specification first\n        run: python3 tools/specgen/render.py --check\n", "", false, "spec-first"},
		{"spec check weakened", ci, "        run: python3 tools/specgen/render.py --check\n", "        run: python3 tools/specgen/render.py --check || true\n", false, "spec-first"},
		{"job bypasses spec", ci, "    name: build and test\n    needs: spec\n", "    name: build and test\n", false, "spec-first"},
		{"unknown dependency", ci, "    name: build and test\n    needs: spec\n", "    name: build and test\n    needs: ghost\n", false, "needs"},
		{"default shell", ci, "    shell: bash\n", "    shell: sh\n", false, "shell"},
		{"syntax error", ci, "jobs:\n", "jobs: &anchor\n", false, "syntax"},

		{"release on branch push", rel, "    tags:\n      - 'v[0-9]+.[0-9]+.[0-9]+*'\n", "    branches:\n      - main\n", false, "triggers"},
		{"release on any tag", rel, "      - 'v[0-9]+.[0-9]+.[0-9]+*'\n", "      - '*'\n", false, "triggers"},
		{"release on pull request", rel, "  push:\n    tags:", "  pull_request:\n  push:\n    tags:", false, "triggers"},
		{"optional version input", rel, "        required: true\n", "        required: false\n", false, "triggers"},
		{"release with caches", rel, "      - name: Build\n", "      - name: Restore\n        uses: actions/cache@" + verifiedPins["actions/cache"].SHA + " # v6.1.0\n        with:\n          path: ~/go/pkg/mod\n          key: go-${{ runner.os }}-${{ runner.arch }}-${{ hashFiles('go.mod', 'go.sum', 'docs/implementation/dependencies.lock.json') }}\n      - name: Build\n", false, "cache"},
		{"release tolerates skips", rel, "gotest -strict -name qualification", "gotest -name qualification", false, "strict-tests"},
		{"gate removed from needs", rel, "      - qualification\n", "", false, "release-gates"},
		{"gate job deleted but still needed", rel, "  qualification:\n    name: platform, client, adapter and packaging qualification\n", "  renamed:\n    name: platform, client, adapter and packaging qualification\n", false, "release-gates"},
		{"platform dropped from a gate", rel, "          - macos-15\n", "", true, "release-gates"},
		{"fail-fast hides a platform", rel, "      fail-fast: false\n", "      fail-fast: true\n", true, "release-gates"},
		{"verdict skippable", rel, "    name: release qualification verdict\n    if: always()\n", "    name: release qualification verdict\n", false, "release-gates"},
		{"verdict ignores needs", rel, "          NEEDS_JSON: ${{ toJSON(needs) }}\n", "          NEEDS_JSON: '{}'\n", false, "release-gates"},
		{"verdict without gate command", rel, "cigate gate -out", "cigate environ -out", false, "release-gates"},
		{"unregistered release job", rel, "  verdict:\n", "  publish:\n    name: publish\n    needs: spec\n    runs-on: ubuntu-24.04\n    timeout-minutes: 5\n    steps:\n      - name: nothing\n        run: echo publish\n  verdict:\n", false, "release-gates"},
		{"gate bypasses inputs", rel, "    name: specification drift\n    needs: inputs\n", "    name: specification drift\n", false, "release-inputs"},
		{"release grants write", rel, "  contents: read", "  contents: write", false, "permissions"},
		{"release skips a missing suite", rel, "      - name: Run integration_test on macOS\n        if: runner.os == 'macOS'\n", "      - name: Run integration_test on macOS\n        if: runner.os == 'macOS' && steps.layout.outputs.integration_test == 'true'\n", false, "step-condition"},
		{"release tolerates flutter skips", rel, "fluttertest -strict -name flutter-test", "fluttertest -name flutter-test", false, "strict-tests"},
		{"flutter gate dropped", rel, "      - flutter\n", "", false, "release-gates"},
		{"pub cache in a Go job", ci, "          key: go-test-", "          key: go-test-${{ hashFiles('apps/desktop/pubspec.lock') }}-", false, "cache"},
		{"pub cache without the flutter version", ci, "-${{ steps.pin.outputs.version }}-", "-", false, "cache"},
		{"pub cache without pubspec.lock", ci, "${{ hashFiles('apps/desktop/pubspec.lock') }}", "${{ hashFiles('apps/desktop/pubspec.yaml') }}", false, "cache"},
		{"go cache in the flutter job", ci, "          path: ~/.pub-cache\n", "          path: |\n            ~/.pub-cache\n            ~/go/pkg/mod\n", false, "cache"},
		{"floating flutter clone", ci, "          go run ./.github/workflows/cigate flutterverify -version-file \"$EVIDENCE/flutter-version.json\"\n", "          curl -fsSL https://example.invalid/flutter.tar.xz\n", false, "no-publish"},
		{"short tests", ci, "-- -timeout 30m ./...", "-- -short -timeout 30m ./...", false, "no-short"},
		{"implicit go test timeout", ci, "-- -timeout 30m ./...", "-- ./...", false, "go-test-timeout"},
		{"implicit race timeout in release", rel, "-- -timeout 40m -race ./...", "-- -race ./...", false, "go-test-timeout"},
		{"pub publish", ci, "        run: go build ./... ./.github/workflows/...\n", "        run: |\n          go build ./... ./.github/workflows/...\n          flutter pub publish\n", false, "no-publish"},

		{"platform regression requirement dropped from the unit test step", ci, " -require-tests \"github.com/zatiti/zatiti/internal/platform#TestListenPrivateRefusesSymlinkedRunDirectory,github.com/zatiti/zatiti/internal/platform#TestBlobTamperedObjectFailsPublishOverExisting\" -out-dir \"$EVIDENCE\" -- -timeout 30m ./...", " -out-dir \"$EVIDENCE\" -- -timeout 30m ./...", false, "platform-regressions"},
		{"one required platform regression test narrowed away", ci, ",github.com/zatiti/zatiti/internal/platform#TestBlobTamperedObjectFailsPublishOverExisting\" -out-dir \"$EVIDENCE\" -- -timeout 30m ./...", "\" -out-dir \"$EVIDENCE\" -- -timeout 30m ./...", false, "platform-regressions"},
		{"qualification evidence enumeration step removed", rel, "      - name: Enforce qualification case enumeration and evidence freshness\n        run: go run ./.github/workflows/cigate qualevidence -report \"$EVIDENCE/qualification-cases/release-report.json\" -require-gate \"Z01,Z04,Z13,Z16,Z21,JOURNEY,QUALIFICATION\" -out \"$EVIDENCE/qualification-verdict.json\"\n", "", false, "release-gates"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := readWorkflow(t, tc.workflow)
			if !strings.Contains(src, tc.old) {
				t.Fatalf("mutation target %q is not present in %s", tc.old, tc.workflow)
			}
			n := 1
			if tc.all {
				n = -1
			}
			findings := lintWorkflow(tc.workflow, []byte(strings.Replace(src, tc.old, tc.new, n)))
			for _, f := range findings {
				if f.Rule == tc.rule {
					return
				}
			}
			t.Fatalf("no %q finding; got %v", tc.rule, findings)
		})
	}
}

func TestLintDirRejectsUnregisteredAndMissingWorkflows(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ci.yml"), []byte(readWorkflow(t, "ci.yml")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "publish.yaml"), []byte("name: publish\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	findings, err := lintDir(dir)
	if err != nil {
		t.Fatalf("lintDir: %v", err)
	}
	got := map[string]bool{}
	for _, f := range findings {
		got[f.File+"/"+f.Rule] = true
	}
	for _, want := range []string{"publish.yaml/registered-workflow", "release-qualification.yml/registered-workflow"} {
		if !got[want] {
			t.Errorf("missing finding %s in %v", want, findings)
		}
	}
	if got["ci.yml/registered-workflow"] {
		t.Errorf("ci.yml was reported as unregistered")
	}
	if _, err := lintDir(filepath.Join(dir, "absent")); err == nil {
		t.Error("lintDir on a missing directory succeeded")
	}
}

func TestReleaseGateListsAgree(t *testing.T) {
	t.Parallel()
	for _, g := range platformReleaseGates {
		if !contains(requiredReleaseGates, g) {
			t.Errorf("platform gate %q is not a required gate", g)
		}
	}
	root, err := parseYAML([]byte(readWorkflow(t, "release-qualification.yml")))
	if err != nil {
		t.Fatal(err)
	}
	var jobs []string
	for _, j := range root.get("jobs").entries() {
		if j.Key != verdictJob {
			jobs = append(jobs, j.Key)
		}
	}
	if strings.Join(jobs, ",") != strings.Join(requiredReleaseGates, ",") {
		t.Errorf("workflow gates %v differ from requiredReleaseGates %v", jobs, requiredReleaseGates)
	}
}
