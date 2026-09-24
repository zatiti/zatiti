package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// pin is one third-party action release whose commit was resolved from the
// upstream repository's tag and whose action.yml inputs were read at that
// commit. README.md in this directory records how to re-verify a pin.
type pin struct {
	SHA     string
	Version string
}

var verifiedPins = map[string]pin{
	"actions/checkout":          {SHA: "3d3c42e5aac5ba805825da76410c181273ba90b1", Version: "v7.0.1"},
	"actions/setup-go":          {SHA: "b7ad1dad31e06c5925ef5d2fc7ad053ef454303e", Version: "v7.0.0"},
	"actions/cache":             {SHA: "55cc8345863c7cc4c66a329aec7e433d2d1c52a9", Version: "v6.1.0"},
	"actions/upload-artifact":   {SHA: "043fb46d1a93c77aae656e7c1c64a875d1fc6a0a", Version: "v7.0.1"},
	"actions/download-artifact": {SHA: "70fc10c6e5e1ce46ad2ea6f2b72d43f7d47b13c3", Version: "v8.0.0"},
}

// profile selects the workflow-specific rules applied on top of the rules
// every workflow must satisfy.
type profile int

const (
	profileCI profile = iota
	profileRelease
)

// registeredWorkflows is the complete set of workflow files. A file outside
// this set fails validation, so a publishing workflow cannot be added without
// also changing the policy under review.
var registeredWorkflows = map[string]profile{
	"ci.yml":                    profileCI,
	"release-qualification.yml": profileRelease,
}

// requiredReleaseGates is the canonical list of jobs whose success a release
// qualification verdict depends on. The gate command compiles in the same
// list, so removing a job from the workflow blocks the verdict instead of
// weakening it.
var requiredReleaseGates = []string{
	"inputs", "spec", "workflows", "static", "test",
	"flutter", "qualification", "build", "candidate_matrix",
}

// platformReleaseGates must run on every supported release platform.
var platformReleaseGates = []string{"test", "flutter", "qualification", "build"}

// requiredPlatformRegressionTests are the internal/platform security
// regressions that a hosted Linux and macOS run actually observed failing
// while every affected local run stayed green (docs/implementation-remediation/audit.md,
// "Hosted CI follow-up": TestListenPrivateRefusesSymlinkedRunDirectory failed
// on both hosted platforms, TestBlobTamperedObjectFailsPublishOverExisting
// additionally failed on hosted macOS). The whole-module "test" job in both
// workflows must name both with cigate gotest -require-tests, so a hosted
// platform failure this specific and this expensive to rediscover can never
// again be silently superseded by a passing local run or by these tests
// being renamed or deleted without anyone noticing.
var requiredPlatformRegressionTests = []string{
	"github.com/zatiti/zatiti/internal/platform#TestListenPrivateRefusesSymlinkedRunDirectory",
	"github.com/zatiti/zatiti/internal/platform#TestBlobTamperedObjectFailsPublishOverExisting",
}

// macos-15 is GitHub's native Apple Silicon image; macos-15-intel is its
// native Intel image. The runtime architecture step below catches a runner
// label whose actual host does not match its documented architecture.
var supportedRunners = []string{"ubuntu-24.04", "macos-15", "macos-15-intel"}
var requiredMacRunners = []string{"macos-15", "macos-15-intel"}

const (
	verdictJob       = "verdict"
	flutterJob       = "flutter"
	specJob          = "spec"
	inputsJob        = "inputs"
	qualificationJob = "qualification"
	specCheckCommand = "python3 tools/specgen/render.py --check"
	cacheKeyHash     = "hashFiles('go.mod', 'go.sum', 'docs/implementation/dependencies.lock.json')"
	pubCacheKeyHash  = "hashFiles('apps/desktop/pubspec.lock')"
	pubCacheVersion  = "steps.pin.outputs.version"
	pubCachePath     = "~/.pub-cache"
	matrixRunner     = "${{ matrix.os }}"
	needsExpression  = "${{ toJSON(needs) }}"
	maxJobMinutes    = 60
	maxRetentionDays = 30
)

var (
	usesPattern      = regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)@([0-9a-f]{40})$`)
	goInstallPattern = regexp.MustCompile(`\bgo install\s+(\S+)`)
	pinnedModule     = regexp.MustCompile(`@v\d+\.\d+\.\d+$`)
	forbiddenRun     = []*regexp.Regexp{
		regexp.MustCompile(`\bgh\s+(release|api|workflow|pr|repo)\b`),
		regexp.MustCompile(`\bgit\s+(push|tag)\b`),
		regexp.MustCompile(`\b(curl|wget|goreleaser|cosign)\b`),
		regexp.MustCompile(`\b(docker|podman)\s+(push|login)\b`),
		regexp.MustCompile(`\b(npm|twine|cargo|pub|flutter pub|dart pub)\s+(publish|upload)\b`),
	}
	// allowedStepConditions are the only step conditions accepted anywhere.
	allowedStepConditions = map[string]bool{"always()": true, "runner.os == 'Linux'": true, "runner.os == 'macOS'": true}
	// ciOnlyStepConditions let a pull-request job state what it did not run
	// when an optional suite is absent. A release gate never skips.
	ciOnlyStepConditions = map[string]bool{
		"runner.os == 'Linux' && steps.layout.outputs.integration_test == 'true'": true,
		"runner.os == 'macOS' && steps.layout.outputs.integration_test == 'true'": true,
	}
	allowedTopKeys  = set("name", "on", "permissions", "concurrency", "defaults", "env", "jobs")
	allowedJobKeys  = set("name", "needs", "if", "runs-on", "timeout-minutes", "strategy", "steps", "outputs", "env", "permissions")
	allowedStepKeys = set("name", "id", "uses", "with", "run", "env", "if")
)

func set(items ...string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, it := range items {
		out[it] = true
	}
	return out
}

// finding is one policy violation.
type finding struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

func (f finding) String() string {
	return fmt.Sprintf("%s:%d: [%s] %s", f.File, f.Line, f.Rule, f.Message)
}

// lintDir validates every workflow file in dir against the policy.
func lintDir(dir string) ([]finding, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading workflow directory %s: %w", dir, err)
	}
	var out []finding
	present := map[string]bool{}
	for _, e := range entries {
		ext := filepath.Ext(e.Name())
		if e.IsDir() || (ext != ".yml" && ext != ".yaml") {
			continue
		}
		present[e.Name()] = true
		src, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading workflow %s: %w", e.Name(), err)
		}
		out = append(out, lintWorkflow(e.Name(), src)...)
	}
	for name := range registeredWorkflows {
		if !present[name] {
			out = append(out, finding{File: name, Rule: "registered-workflow", Message: "registered workflow file is missing"})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out, nil
}

type linter struct {
	file     string
	prof     profile
	findings []finding
}

func (l *linter) add(line int, rule, format string, args ...any) {
	l.findings = append(l.findings, finding{File: l.file, Line: line, Rule: rule, Message: fmt.Sprintf(format, args...)})
}

// lintWorkflow validates one workflow document. name is its base file name.
func lintWorkflow(name string, src []byte) []finding {
	l := &linter{file: name}
	prof, ok := registeredWorkflows[name]
	if !ok {
		l.add(0, "registered-workflow", "workflow is not registered in the policy; unregistered workflows are rejected")
		return l.findings
	}
	l.prof = prof
	root, err := parseYAML(src)
	if err != nil {
		l.add(0, "syntax", "%v", err)
		return l.findings
	}
	if root.Kind != mapNode {
		l.add(root.Line, "syntax", "workflow must be a mapping")
		return l.findings
	}
	for _, e := range root.Entries {
		if !allowedTopKeys[e.Key] {
			l.add(e.Line, "top-level-keys", "key %q is not allowed", e.Key)
		}
	}
	l.checkScalars(root)
	l.checkTriggers(root)
	l.checkPermissions(root.get("permissions"), root.Line, true)
	if shell, _ := root.path("defaults", "run", "shell").scalar(); shell != "bash" {
		l.add(root.Line, "shell", "defaults.run.shell must be bash so every script runs with errexit and pipefail")
	}
	jobs := root.get("jobs")
	if jobs == nil || jobs.Kind != mapNode || len(jobs.Entries) == 0 {
		l.add(root.Line, "jobs", "workflow must define jobs")
		return l.findings
	}
	for _, j := range jobs.Entries {
		l.checkJob(j)
	}
	l.checkGraph(jobs)
	if prof == profileRelease {
		l.checkReleaseGates(jobs)
	}
	return l.findings
}

func (l *linter) checkScalars(root *node) {
	root.walkScalars("", func(loc string, s *node) {
		lower := strings.ToLower(s.Value)
		if strings.Contains(lower, "secrets.") || strings.Contains(lower, "github.token") || strings.Contains(lower, "github_token") {
			l.add(s.Line, "no-secrets", "%s references a secret or token; no workflow secret is provisioned", strings.TrimPrefix(loc, "."))
		}
	})
}

func (l *linter) checkTriggers(root *node) {
	on := root.get("on")
	if on == nil || on.Kind != mapNode {
		l.add(root.Line, "triggers", "\"on\" must be a mapping of triggers")
		return
	}
	want := set("pull_request", "push")
	if l.prof == profileRelease {
		want = set("workflow_dispatch", "push")
	}
	for _, e := range on.Entries {
		if !want[e.Key] {
			l.add(e.Line, "triggers", "trigger %q is not allowed for this workflow", e.Key)
		}
		delete(want, e.Key)
	}
	for missing := range want {
		l.add(on.Line, "triggers", "trigger %q is required", missing)
	}
	push := on.get("push")
	if push == nil || push.Kind != mapNode {
		l.add(on.Line, "triggers", "push must be filtered; an unfiltered push trigger is rejected")
		return
	}
	filter := "branches"
	if l.prof == profileRelease {
		filter = "tags"
	}
	for _, e := range push.Entries {
		if e.Key != filter {
			l.add(e.Line, "triggers", "push filter %q is not allowed; only %q is", e.Key, filter)
		}
	}
	values, ok := push.get(filter).strings()
	if !ok || len(values) == 0 {
		l.add(push.Line, "triggers", "push.%s must list at least one pattern", filter)
		return
	}
	for _, v := range values {
		if l.prof == profileCI && v != "main" {
			l.add(push.Line, "triggers", "push.branches may only contain main, got %q", v)
		}
		if l.prof == profileRelease && !strings.HasPrefix(v, "v") {
			l.add(push.Line, "triggers", "push.tags patterns must be version tags starting with v, got %q", v)
		}
	}
	if l.prof != profileRelease {
		return
	}
	version := on.path("workflow_dispatch", "inputs", "version")
	if req, _ := version.get("required").scalar(); req != "true" {
		l.add(on.Line, "triggers", "workflow_dispatch.inputs.version must be declared with required: true")
	}
	if typ, _ := version.get("type").scalar(); typ != "string" {
		l.add(on.Line, "triggers", "workflow_dispatch.inputs.version must have type: string")
	}
}

func (l *linter) checkPermissions(perms *node, line int, top bool) {
	if perms == nil {
		if top {
			l.add(line, "permissions", "workflow must declare top-level permissions")
		}
		return
	}
	if perms.Kind != mapNode {
		l.add(perms.Line, "permissions", "permissions must be a mapping; shorthand grants are rejected")
		return
	}
	for _, e := range perms.Entries {
		v, _ := e.Value.scalar()
		if v != "read" && v != "none" {
			l.add(e.Line, "permissions", "permission %s: %q is not allowed; workflows are read-only", e.Key, v)
		}
		if top && (e.Key != "contents" || v != "read") {
			l.add(e.Line, "permissions", "top-level permissions must be exactly contents: read")
		}
	}
	if top && len(perms.Entries) != 1 {
		l.add(perms.Line, "permissions", "top-level permissions must be exactly contents: read")
	}
}

func (l *linter) checkJob(j entry) {
	job := j.Value
	if job.Kind != mapNode {
		l.add(j.Line, "jobs", "job %s must be a mapping", j.Key)
		return
	}
	for _, e := range job.Entries {
		if !allowedJobKeys[e.Key] {
			l.add(e.Line, "job-keys", "job %s: key %q is not allowed", j.Key, e.Key)
		}
	}
	l.checkPermissions(job.get("permissions"), j.Line, false)
	if cond := job.get("if"); cond != nil {
		v, _ := cond.scalar()
		if l.prof != profileRelease || j.Key != verdictJob || v != "always()" {
			l.add(cond.Line, "job-condition", "job %s: job-level conditions can skip a gate and are rejected", j.Key)
		}
	}
	l.checkRunner(j.Key, job)
	minutes, _ := job.get("timeout-minutes").scalar()
	if n, err := strconv.Atoi(minutes); err != nil || n < 1 || n > maxJobMinutes {
		l.add(j.Line, "timeout", "job %s: timeout-minutes must be an integer from 1 to %d", j.Key, maxJobMinutes)
	}
	steps := job.get("steps")
	if steps == nil || steps.Kind != seqNode || len(steps.Items) == 0 {
		l.add(j.Line, "steps", "job %s must list steps", j.Key)
		return
	}
	var sawLock bool
	for i, st := range steps.Items {
		if st.Kind != mapNode {
			l.add(st.Line, "steps", "job %s: step %d must be a mapping", j.Key, i+1)
			continue
		}
		l.checkStep(j.Key, i, st)
		if run, ok := st.get("run").scalar(); ok && strings.Contains(run, "cigate lock ") {
			sawLock = true
		}
	}
	if !sawLock {
		l.add(j.Line, "toolchain-lock", "job %s must verify the toolchain and dependency lock with \"cigate lock\"", j.Key)
	}
	if runner, _ := job.get("runs-on").scalar(); runner == matrixRunner {
		l.checkNativeArchitecture(j.Key, steps)
	}
	if name, _ := actionName(steps.Items[0]); name != "actions/checkout" {
		l.add(steps.Items[0].Line, "steps", "job %s: the first step must check out the source", j.Key)
	}
	last := steps.Items[len(steps.Items)-1]
	if name, _ := actionName(last); name != "actions/upload-artifact" {
		l.add(last.Line, "evidence", "job %s: the last step must retain evidence with actions/upload-artifact", j.Key)
	}
}

// checkNativeArchitecture requires an early, unconditional host check in
// every matrix job. A runner label alone is insufficient evidence that a
// Go or Flutter build actually ran on the requested native CPU.
func (l *linter) checkNativeArchitecture(jobName string, steps *node) {
	if len(steps.Items) < 2 {
		l.add(steps.Line, "runner-arch", "job %s must check its native host architecture", jobName)
		return
	}
	st := steps.Items[1]
	name, _ := st.get("name").scalar()
	run, _ := st.get("run").scalar()
	image, _ := st.path("env", "EXPECTED_IMAGE").scalar()
	if name != "Verify native runner architecture" || image != matrixRunner || st.get("if") != nil ||
		!strings.Contains(run, `macos-15) expected_os=macOS; expected_runner=ARM64; expected_uname=arm64`) ||
		!strings.Contains(run, `macos-15-intel) expected_os=macOS; expected_runner=X64; expected_uname=x86_64`) ||
		!strings.Contains(run, `test "$RUNNER_OS" = "$expected_os"`) ||
		!strings.Contains(run, `test "$RUNNER_ARCH" = "$expected_runner"`) ||
		!strings.Contains(run, `test "$(uname -m)" = "$expected_uname"`) {
		l.add(st.Line, "runner-arch", "job %s must verify the native OS and CPU against its matrix image immediately after checkout", jobName)
	}
}

func (l *linter) checkRunner(jobName string, job *node) {
	runner, _ := job.get("runs-on").scalar()
	strategy := job.get("strategy")
	if strategy != nil {
		for _, e := range strategy.Entries {
			if e.Key != "fail-fast" && e.Key != "matrix" {
				l.add(e.Line, "runner", "job %s: strategy key %q is not allowed", jobName, e.Key)
			}
		}
		for _, e := range strategy.get("matrix").entries() {
			if e.Key != "os" {
				l.add(e.Line, "runner", "job %s: matrix key %q is not allowed", jobName, e.Key)
			}
		}
	}
	runners := []string{runner}
	if runner == matrixRunner {
		var ok bool
		runners, ok = strategy.path("matrix", "os").strings()
		if !ok || len(runners) == 0 {
			l.add(job.Line, "runner", "job %s: matrix.os must list runner images", jobName)
			return
		}
	}
	for _, r := range runners {
		if !contains(supportedRunners, r) {
			l.add(job.Line, "runner", "job %s: runner %q is not a pinned supported image (%s)", jobName, r, strings.Join(supportedRunners, ", "))
		}
	}
	if l.prof == profileCI && (jobName == "test" || jobName == flutterJob) {
		for _, r := range requiredMacRunners {
			if !contains(runners, r) {
				l.add(job.Line, "runner", "job %s must run on native Mac image %s", jobName, r)
			}
		}
	}
}

func (n *node) entries() []entry {
	if n == nil || n.Kind != mapNode {
		return nil
	}
	return n.Entries
}

func contains(list []string, s string) bool {
	for _, it := range list {
		if it == s {
			return true
		}
	}
	return false
}

// actionName returns the owner/repo of a step's action and its uses node.
func actionName(step *node) (string, *node) {
	uses := step.get("uses")
	v, ok := uses.scalar()
	if !ok {
		return "", nil
	}
	name, _, _ := strings.Cut(v, "@")
	return name, uses
}

func (l *linter) checkStep(jobName string, index int, st *node) {
	where := fmt.Sprintf("job %s step %d", jobName, index+1)
	for _, e := range st.Entries {
		if !allowedStepKeys[e.Key] {
			l.add(e.Line, "step-keys", "%s: key %q is not allowed", where, e.Key)
		}
	}
	if _, ok := st.get("name").scalar(); !ok {
		l.add(st.Line, "step-keys", "%s must have a name", where)
	}
	run, hasRun := st.get("run").scalar()
	name, uses := actionName(st)
	if hasRun == (uses != nil) {
		l.add(st.Line, "step-keys", "%s must have exactly one of run and uses", where)
	}
	if cond := st.get("if"); cond != nil {
		v, _ := cond.scalar()
		if !allowedStepConditions[v] && (l.prof != profileCI || !ciOnlyStepConditions[v]) {
			l.add(cond.Line, "step-condition", "%s: condition %q is not on the allowlist", where, v)
		}
		if v == "always()" && name != "actions/upload-artifact" {
			l.add(cond.Line, "step-condition", "%s: always() is reserved for evidence retention", where)
		}
	}
	if hasRun {
		l.checkRun(jobName, where, st.get("run").Line, run)
	}
	if uses != nil {
		l.checkUses(where, name, uses, st)
	}
}

func (l *linter) checkRun(jobName, where string, line int, run string) {
	if strings.Contains(run, "${{") {
		l.add(line, "script-injection", "%s: expressions are not allowed inside run scripts; pass values through env", where)
	}
	for _, re := range forbiddenRun {
		if m := re.FindString(run); m != "" {
			l.add(line, "no-publish", "%s: command %q can publish or fetch unpinned content and is rejected", where, m)
		}
	}
	for _, m := range goInstallPattern.FindAllStringSubmatch(run, -1) {
		if !pinnedModule.MatchString(m[1]) {
			l.add(line, "pinned-tools", "%s: go install %s must name an exact module version", where, m[1])
		}
	}
	if strings.Contains(run, "-short") {
		l.add(line, "no-short", "%s: -short would skip the end-to-end and integration tests that prove the product runs", where)
	}
	for _, ln := range strings.Split(run, "\n") {
		if !strings.Contains(ln, "cigate gotest") {
			continue
		}
		// cmd/zatiti builds a binary and runs real controllers; its race run
		// exceeds go test's default 10-minute package timeout under load.
		if _, goArgs, ok := strings.Cut(ln, " -- "); !ok || !strings.Contains(goArgs, "-timeout ") {
			l.add(line, "go-test-timeout", "%s: every go test run must pass an explicit -timeout after --", where)
		}
		if jobName == "test" && wholeModuleArg(ln) {
			for _, want := range requiredPlatformRegressionTests {
				if !strings.Contains(ln, want) {
					l.add(line, "platform-regressions", "%s: the whole-module test run must require %q with -require-tests, so a hosted-only platform regression cannot be silently dropped again", where, want)
				}
			}
		}
	}
	if l.prof == profileRelease {
		for _, ln := range strings.Split(run, "\n") {
			if (strings.Contains(ln, "cigate gotest") || strings.Contains(ln, "cigate fluttertest")) && !strings.Contains(ln, " -strict ") {
				l.add(line, "strict-tests", "%s: release test gates must run tests with -strict so skipped or absent tests block", where)
			}
		}
	}
}

// wholeModuleArg reports whether run line ln passes "./..." as a standalone
// argument, the pattern that runs every package in the module and so must
// carry the platform regression requirement.
func wholeModuleArg(ln string) bool {
	for _, f := range strings.Fields(ln) {
		if f == "./..." {
			return true
		}
	}
	return false
}

func (l *linter) checkUses(where, name string, uses, st *node) {
	value, _ := uses.scalar()
	m := usesPattern.FindStringSubmatch(value)
	if m == nil {
		l.add(uses.Line, "pinned-actions", "%s: %q must be owner/repo@<40-hex commit>; tags, branches, local and container actions are rejected", where, value)
		return
	}
	p, ok := verifiedPins[name]
	switch {
	case !ok:
		l.add(uses.Line, "pinned-actions", "%s: action %s has no verified pin in the policy", where, name)
	case p.SHA != m[2]:
		l.add(uses.Line, "pinned-actions", "%s: %s commit %s is not the verified commit for %s", where, name, m[2], p.Version)
	case uses.Comment != p.Version:
		l.add(uses.Line, "pinned-actions", "%s: %s must carry the trailing comment \"# %s\"", where, name, p.Version)
	}
	with := st.get("with")
	str := func(key string) string { v, _ := with.get(key).scalar(); return v }
	switch name {
	case "actions/checkout":
		if str("persist-credentials") != "false" {
			l.add(uses.Line, "checkout", "%s: checkout must set persist-credentials: false", where)
		}
	case "actions/setup-go":
		if str("go-version-file") != "go.mod" || with.get("go-version") != nil {
			l.add(uses.Line, "toolchain", "%s: setup-go must take the toolchain from go-version-file: go.mod only", where)
		}
		if str("cache") != "false" {
			l.add(uses.Line, "toolchain", "%s: setup-go must set cache: false; caching is explicit and lock-bound", where)
		}
	case "actions/cache":
		if l.prof == profileRelease {
			l.add(uses.Line, "cache", "%s: release qualification builds from a clean checkout without caches", where)
		}
		key, path := str("key"), str("path")
		// Go caches bind the Go lock and toolchain; the pub cache binds
		// pubspec.lock and the Flutter version. Neither job restores the
		// other's cache.
		parts := []string{cacheKeyHash, "runner.os", "runner.arch"}
		if strings.HasPrefix(where, "job "+flutterJob+" ") {
			parts = []string{pubCacheKeyHash, pubCacheVersion, "runner.os", "runner.arch"}
			if strings.TrimSpace(path) != pubCachePath {
				l.add(uses.Line, "cache", "%s: the Flutter job caches only %s", where, pubCachePath)
			}
		} else if strings.Contains(path, "pub-cache") || strings.Contains(key, "pubspec") {
			l.add(uses.Line, "cache", "%s: only the Flutter job may cache pub packages", where)
		}
		for _, part := range parts {
			if !strings.Contains(key, part) {
				l.add(uses.Line, "cache", "%s: cache key must contain %s", where, part)
			}
		}
		if with.get("restore-keys") != nil {
			l.add(uses.Line, "cache", "%s: restore-keys would restore a cache built from another lock", where)
		}
	case "actions/upload-artifact":
		if v, _ := st.get("if").scalar(); v != "always()" {
			l.add(uses.Line, "evidence", "%s: evidence must be retained with if: always()", where)
		}
		if n, err := strconv.Atoi(str("retention-days")); err != nil || n < 1 || n > maxRetentionDays {
			l.add(uses.Line, "evidence", "%s: retention-days must be an integer from 1 to %d", where, maxRetentionDays)
		}
		if str("if-no-files-found") != "error" {
			l.add(uses.Line, "evidence", "%s: if-no-files-found must be error", where)
		}
		if str("name") == "" || str("path") == "" {
			l.add(uses.Line, "evidence", "%s: artifact name and path are required", where)
		}
	}
}

// needsOf returns the declared needs of a job.
func needsOf(job *node) []string {
	list, _ := job.get("needs").strings()
	return list
}

// checkGraph validates the needs graph and that the specification check
// precedes all other work.
func (l *linter) checkGraph(jobs *node) {
	byName := map[string]*node{}
	for _, j := range jobs.Entries {
		byName[j.Key] = j.Value
	}
	for _, j := range jobs.Entries {
		for _, n := range needsOf(j.Value) {
			if byName[n] == nil {
				l.add(j.Line, "needs", "job %s needs unknown job %q", j.Key, n)
			}
		}
	}
	spec := byName[specJob]
	if spec == nil {
		l.add(jobs.Line, "spec-first", "workflow must have a %q job", specJob)
		return
	}
	if steps := spec.get("steps"); steps != nil && steps.Kind == seqNode && len(steps.Items) > 1 {
		if run, _ := steps.Items[1].get("run").scalar(); strings.TrimSpace(run) != specCheckCommand {
			l.add(steps.Items[1].Line, "spec-first", "the first step after checkout in %q must run exactly %q", specJob, specCheckCommand)
		}
	} else {
		l.add(spec.Line, "spec-first", "job %q must run %q right after checkout", specJob, specCheckCommand)
	}
	for _, j := range jobs.Entries {
		reach := reachable(byName, j.Key)
		if reach[j.Key] {
			l.add(j.Line, "needs", "job %s is part of a needs cycle", j.Key)
		}
		if j.Key == specJob || (l.prof == profileRelease && j.Key == inputsJob) {
			continue
		}
		if !reach[specJob] {
			l.add(j.Line, "spec-first", "job %s must depend on %q so the specification check runs first", j.Key, specJob)
		}
		if l.prof == profileRelease && !reach[inputsJob] {
			l.add(j.Line, "release-inputs", "job %s must depend on %q so absent release inputs stop everything", j.Key, inputsJob)
		}
	}
	if l.prof == profileRelease && !reachable(byName, specJob)[inputsJob] {
		l.add(spec.Line, "release-inputs", "job %q must depend on %q", specJob, inputsJob)
	}
}

// reachable returns every job transitively needed by start. start itself is
// in the result only when it sits on a cycle.
func reachable(byName map[string]*node, start string) map[string]bool {
	seen := map[string]bool{}
	var visit func(name string)
	visit = func(name string) {
		for _, n := range needsOf(byName[name]) {
			if !seen[n] {
				seen[n] = true
				visit(n)
			}
		}
	}
	visit(start)
	return seen
}

func (l *linter) checkReleaseGates(jobs *node) {
	byName := map[string]entry{}
	for _, j := range jobs.Entries {
		byName[j.Key] = j
		if j.Key != verdictJob && !contains(requiredReleaseGates, j.Key) {
			l.add(j.Line, "release-gates", "job %s is not a registered release gate; register it in requiredReleaseGates", j.Key)
		}
	}
	for _, g := range requiredReleaseGates {
		if _, ok := byName[g]; !ok {
			l.add(jobs.Line, "release-gates", "required release gate %q is missing", g)
		}
	}
	// The native Security.framework Keychain writer is built only with cgo.
	// A controller candidate without it cannot complete installed setup.
	if build, ok := byName["build"]; ok {
		var nativeController bool
		for _, st := range build.Value.get("steps").items() {
			name, _ := st.get("name").scalar()
			if name != "Build from the unmodified tagged commit" {
				continue
			}
			nativeController = true
			cgo, _ := st.path("env", "CGO_ENABLED").scalar()
			run, _ := st.get("run").scalar()
			if cgo != "1" || !strings.Contains(run, `test "$(go env CGO_ENABLED)" = 1`) ||
				!strings.Contains(run, `go version -m "$RUNNER_TEMP/candidate/zatiti" | grep -Eq '^[[:space:]]*build[[:space:]]+CGO_ENABLED=1$'`) ||
				!strings.Contains(run, `nm "$RUNNER_TEMP/candidate/zatiti" | grep -F '_SecItemAdd' >/dev/null`) ||
				!strings.Contains(run, `nm "$RUNNER_TEMP/candidate/zatiti" | grep -F '_SecItemUpdate' >/dev/null`) {
				l.add(st.Line, "native-keychain", "native Mac controller candidate must build with cgo and verify its Keychain symbols")
			}
		}
		if !nativeController {
			l.add(build.Line, "native-keychain", "native Mac controller build step is missing")
		}
	}
	for _, g := range platformReleaseGates {
		j, ok := byName[g]
		if !ok {
			continue
		}
		runners, _ := j.Value.path("strategy", "matrix", "os").strings()
		for _, r := range requiredMacRunners {
			if !contains(runners, r) {
				l.add(j.Line, "release-gates", "gate %s must run on %s", g, r)
			}
		}
		if ff, _ := j.Value.path("strategy", "fail-fast").scalar(); ff != "false" {
			l.add(j.Line, "release-gates", "gate %s must set fail-fast: false so every platform reports", g)
		}
		if g == qualificationJob {
			var sawQualEvidence bool
			for _, st := range j.Value.get("steps").items() {
				if run, _ := st.get("run").scalar(); strings.Contains(run, "cigate qualevidence") {
					sawQualEvidence = true
				}
			}
			if !sawQualEvidence {
				l.add(j.Line, "release-gates", "gate %s must enforce case enumeration and evidence freshness with \"cigate qualevidence\"", g)
			}
		}
	}
	v, ok := byName[verdictJob]
	if !ok {
		l.add(jobs.Line, "release-gates", "release workflow must have a %q job", verdictJob)
		return
	}
	if cond, _ := v.Value.get("if").scalar(); cond != "always()" {
		l.add(v.Line, "release-gates", "%s must run with if: always() so failed gates still produce a blocking verdict", verdictJob)
	}
	needs := needsOf(v.Value)
	for _, g := range requiredReleaseGates {
		if !contains(needs, g) {
			l.add(v.Line, "release-gates", "%s must need required gate %q", verdictJob, g)
		}
	}
	var gated bool
	for _, st := range v.Value.get("steps").items() {
		run, _ := st.get("run").scalar()
		if !strings.Contains(run, "cigate gate ") {
			continue
		}
		gated = true
		if expr, _ := st.path("env", "NEEDS_JSON").scalar(); expr != needsExpression {
			l.add(st.Line, "release-gates", "the gate step must receive NEEDS_JSON: %s", needsExpression)
		}
	}
	if !gated {
		l.add(v.Line, "release-gates", "%s must evaluate the gates with \"cigate gate\"", verdictJob)
	}
}

func (n *node) items() []*node {
	if n == nil || n.Kind != seqNode {
		return nil
	}
	return n.Items
}
