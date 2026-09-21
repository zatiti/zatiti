package execution

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Proves the controlled repository verifier (repository_runner.go): it
// checks out a real local git repository at a pinned base into a fresh
// disposable workspace, applies only the reported patch artifact and runs
// only the accepted, locally prequalified command -- never anything a
// worker or the request itself supplies -- and reports each distinct
// failure mode (nonapplying patch, failing command, wrong base) on its own,
// never conflated into one generic failure.

// runGit runs one setup git command against a real repository directory,
// failing the test on error. This is ordinary test-fixture plumbing, not
// the runner under test. It strips GIT_* from the environment for the same
// reason repositoryRunner.git does (repository_runner.go's gitEnv): a test
// process invoked from inside this repo's own pre-commit hook (which runs
// `go test ./...`, this package included) inherits GIT_DIR/GIT_INDEX_FILE
// from that outer git commit, and without stripping them this fixture's
// "independent" temp repository would silently operate on the real zatiti
// repository's index instead -- which is exactly what recursively tripped
// the real pre-commit hook the first time this test was written.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// newRepoFixture creates a real local git repository with one committed
// file, returning its path, HEAD sha and a real unified diff (not yet
// applied to the pristine repository) that appends a line to that file.
func newRepoFixture(t *testing.T) (repoPath, baseSHA string, validPatch []byte) {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "--quiet", "--initial-branch=main")
	runGit(t, dir, "config", "user.email", "test@zatiti.invalid")
	runGit(t, dir, "config", "user.name", "zatiti test")
	if err := os.WriteFile(filepath.Join(dir, "greeting.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	runGit(t, dir, "add", "greeting.txt")
	runGit(t, dir, "commit", "--quiet", "-m", "init")
	head := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))

	// Produce a real diff by actually editing the tracked file, then revert
	// the working tree so the pristine source repository stays exactly at
	// baseSHA -- the runner clones this path and must never see it dirty.
	if err := os.WriteFile(filepath.Join(dir, "greeting.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("edit fixture file: %v", err)
	}
	patch := runGit(t, dir, "diff")
	runGit(t, dir, "checkout", "--quiet", "--", "greeting.txt")
	if strings.TrimSpace(runGit(t, dir, "status", "--porcelain")) != "" {
		t.Fatalf("fixture repository is not clean after revert")
	}
	return dir, head, []byte(patch)
}

// brokenPatch mutates a real, valid patch's unchanged context line (" hello",
// the unified-diff context for the fixture's single pre-existing line) so it
// no longer matches the pinned repository's actual content: git rejects
// this as not applying -- a genuinely malformed hunk against the real base,
// not a different but still-valid change. The fixture patch is a pure
// addition (base "hello\n" -> "hello\nworld\n"), so there is no removed
// ("-...") line to corrupt; the context line is the only line git checks
// against the real file.
func brokenPatch(valid []byte) []byte {
	corrupted := strings.Replace(string(valid), "\n hello\n", "\n this-context-line-does-not-exist-in-the-repository\n", 1)
	if corrupted == string(valid) {
		panic("brokenPatch: expected context line \" hello\" not found in fixture patch: " + string(valid))
	}
	return []byte(corrupted)
}

// repositoryVerifierEnv wires a repository verifier command definition into
// a fresh local registry root and returns the verifier plus the pinned
// command_digest for that definition.
type repoFixture struct {
	t       *testing.T
	e       *testEnv
	root    string
	repo    string
	baseSHA string
	patch   []byte
	blobs   *fakeBlobStore
	verify  *verifier
}

func newRepoVerifierFixture(t *testing.T) *repoFixture {
	t.Helper()
	e := newEnv(t)
	repo, base, patch := newRepoFixture(t)
	root := t.TempDir()
	t.Setenv(repositoryVerifierRootEnv, root)
	blobs := newFakeBlobStore(nil)
	v, err := NewVerifier(contract.VerifierDependencies{Clock: e.clock, Blobs: blobs})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return &repoFixture{t: t, e: e, root: root, repo: repo, baseSHA: base, patch: patch, blobs: blobs, verify: v.(*verifier)}
}

// install writes one local command definition and returns its command_digest.
func (f *repoFixture) install(commandID string, argv []string) contract.Digest {
	f.t.Helper()
	def := repositoryCommandDefinition{
		Schema:         repositoryCommandDefinitionSchema,
		CommandID:      commandID,
		RepositoryPath: f.repo,
		BaseSHA:        f.baseSHA,
		Argv:           argv,
		Env:            []string{"PATH=/usr/bin:/bin:/usr/local/bin"},
	}
	raw, err := json.Marshal(def)
	if err != nil {
		f.t.Fatalf("marshal definition: %v", err)
	}
	dir := filepath.Join(f.root, commandID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatalf("mkdir definition dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "definition.json"), raw, 0o644); err != nil {
		f.t.Fatalf("write definition: %v", err)
	}
	return sha256Hex(raw)
}

// registerPatch stages the fixture's real patch bytes into the fake blob
// store and returns the artifact ref the worker's report would have bound.
func (f *repoFixture) registerPatch() wireArtifactRef {
	f.t.Helper()
	digest := sha256Hex(f.patch)
	f.blobs.objects[digest] = f.patch
	return wireArtifactRef{ID: f.e.ids.New(), Digest: digest}
}

func (f *repoFixture) profile(commandID string, digest contract.Digest, network string, timeoutSeconds, maxOutputBytes int64) json.RawMessage {
	profile := map[string]any{
		"schema":              "zatiti.verifier-profile/v1",
		"kind":                "repository_patch",
		"id":                  "repo-verifier",
		"version":             "1.0.0",
		"code_digest":         verifierCode,
		"runner_profile":      "default",
		"command_id":          commandID,
		"command_digest":      digest,
		"environment_profile": "default",
		"network":             network,
		"max_output_bytes":    maxOutputBytes,
		"timeout_seconds":     timeoutSeconds,
		"capability_evidence": map[string]any{
			"artifact":          map[string]any{"id": "00000000-0000-4000-8000-000000000099", "digest": digestA},
			"adapter_version":   "1.0.0",
			"source_revision":   "rev-1",
			"protocol_revision": "proto-1",
			"profile_digest":    verifierCode,
			"qualified_at":      "2026-01-01T00:00:00Z",
			"capabilities":      []string{"repository_patch_applies", "repository_command"},
			"limitations":       []string{"no namespace/cgroup isolation; process-group kill and byte-bounded output only"},
		},
	}
	raw, err := json.Marshal(profile)
	if err != nil {
		f.t.Fatalf("marshal profile: %v", err)
	}
	return raw
}

func (f *repoFixture) request(profile json.RawMessage, checks []wireExpectedObservation, outputs []wireVerifierOutputRequirement) json.RawMessage {
	return mustMarshal(f.t, wireVerificationRequest{
		Schema:               "zatiti.verification-request/v1",
		JobID:                f.e.ids.New(),
		TaskID:               f.e.ids.New(),
		AttemptID:            f.e.ids.New(),
		Scope:                f.e.scope,
		AcceptanceDigest:     digestA,
		Profile:              profile,
		SealedInputs:         []wireArtifactRef{},
		Outputs:              outputs,
		ExpectedObservations: checks,
	})
}

func (f *repoFixture) verifyOne(request json.RawMessage) wireVerificationResult {
	f.t.Helper()
	res, err := f.verify.Verify(context.Background(), contract.Verification{Request: request})
	if err != nil {
		f.t.Fatalf("Verify: %v", err)
	}
	var doc wireVerificationResult
	f.e.decode(res.Document, &doc)
	return doc
}

func firstObservation(t *testing.T, doc wireVerificationResult, checkID string) wireObservedCheck {
	t.Helper()
	for _, obs := range doc.Observations {
		if obs.CheckID == checkID {
			return obs
		}
	}
	t.Fatalf("no observation for check %q in %+v", checkID, doc.Observations)
	return wireObservedCheck{}
}

// TestRepositoryPatchAppliesPasses proves the happy path: a real patch that
// actually applies against the pinned base is observed as passed, and the
// staged evidence names the real base sha and the patch's own digest.
func TestRepositoryPatchAppliesPasses(t *testing.T) {
	f := newRepoVerifierFixture(t)
	digest := f.install("patch-check", nil)
	patchRef := f.registerPatch()
	profile := f.profile("patch-check", digest, "disabled", 30, 65536)
	req := f.request(profile, []wireExpectedObservation{{
		CheckID: "applies", Kind: "repository_patch_applies", Expected: "pass", ArtifactName: "patch",
	}}, []wireVerifierOutputRequirement{{Name: "patch", Artifact: patchRef, MediaType: "text/x-diff"}})

	doc := f.verifyOne(req)
	obs := firstObservation(t, doc, "applies")
	if obs.Status != "passed" {
		t.Fatalf("repository_patch_applies status %q (%s), want passed", obs.Status, obs.Explanation)
	}
	if doc.Status != "passed" {
		t.Fatalf("overall verification status %q, want passed", doc.Status)
	}
	if len(obs.Evidence) == 0 {
		t.Fatalf("passed check carries no independent evidence")
	}
}

// TestRepositoryCommandPasses proves a repository_command check applies the
// real patch first and then runs the accepted command against the patched
// tree: the check only succeeds because the patch was actually applied
// (the command greps for content the patch, not the base, introduces).
func TestRepositoryCommandPasses(t *testing.T) {
	f := newRepoVerifierFixture(t)
	digest := f.install("grep-check", []string{"sh", "-c", "grep -q world greeting.txt"})
	patchRef := f.registerPatch()
	profile := f.profile("grep-check", digest, "disabled", 30, 65536)
	req := f.request(profile, []wireExpectedObservation{{
		CheckID: "runs", Kind: "repository_command", Expected: "pass", ArtifactName: "patch", CommandID: "grep-check",
	}}, []wireVerifierOutputRequirement{{Name: "patch", Artifact: patchRef, MediaType: "text/x-diff"}})

	doc := f.verifyOne(req)
	obs := firstObservation(t, doc, "runs")
	if obs.Status != "passed" {
		t.Fatalf("repository_command status %q (%s), want passed", obs.Status, obs.Explanation)
	}
	if obs.ObservedExitCode == nil || *obs.ObservedExitCode != 0 {
		t.Fatalf("observed exit code %v, want 0", obs.ObservedExitCode)
	}
}

// TestRepositoryNonapplyingPatchFailsDistinctly proves a patch that does not
// apply against the pinned base fails with an explanation naming patch
// application specifically, not a generic failure.
func TestRepositoryNonapplyingPatchFailsDistinctly(t *testing.T) {
	f := newRepoVerifierFixture(t)
	digest := f.install("patch-check", nil)
	f.patch = brokenPatch(f.patch)
	patchRef := f.registerPatch()
	profile := f.profile("patch-check", digest, "disabled", 30, 65536)
	req := f.request(profile, []wireExpectedObservation{{
		CheckID: "applies", Kind: "repository_patch_applies", Expected: "pass", ArtifactName: "patch",
	}}, []wireVerifierOutputRequirement{{Name: "patch", Artifact: patchRef, MediaType: "text/x-diff"}})

	doc := f.verifyOne(req)
	obs := firstObservation(t, doc, "applies")
	if obs.Status != "failed" {
		t.Fatalf("nonapplying patch status %q, want failed", obs.Status)
	}
	if !strings.Contains(obs.Explanation, "does not apply") {
		t.Fatalf("explanation %q does not name patch application as the failure", obs.Explanation)
	}
}

// TestRepositoryFailingCommandFailsDistinctly proves a command that exits
// nonzero fails with an explanation naming the command's own exit status,
// distinct from a patch-application failure.
func TestRepositoryFailingCommandFailsDistinctly(t *testing.T) {
	f := newRepoVerifierFixture(t)
	digest := f.install("fail-check", []string{"sh", "-c", "exit 7"})
	patchRef := f.registerPatch()
	profile := f.profile("fail-check", digest, "disabled", 30, 65536)
	req := f.request(profile, []wireExpectedObservation{{
		CheckID: "runs", Kind: "repository_command", Expected: "pass", ArtifactName: "patch", CommandID: "fail-check",
	}}, []wireVerifierOutputRequirement{{Name: "patch", Artifact: patchRef, MediaType: "text/x-diff"}})

	doc := f.verifyOne(req)
	obs := firstObservation(t, doc, "runs")
	if obs.Status != "failed" {
		t.Fatalf("failing command status %q, want failed", obs.Status)
	}
	if obs.ObservedExitCode == nil || *obs.ObservedExitCode != 7 {
		t.Fatalf("observed exit code %v, want 7", obs.ObservedExitCode)
	}
	if !strings.Contains(obs.Explanation, "exited 7") {
		t.Fatalf("explanation %q does not name the command's own exit status", obs.Explanation)
	}
	if strings.Contains(obs.Explanation, "does not apply") {
		t.Fatalf("command failure explanation %q wrongly blames patch application", obs.Explanation)
	}
}

// TestRepositoryWrongHeadFailsDistinctly proves a base_sha the pinned
// repository does not contain fails with an explanation naming the
// checkout/head mismatch, distinct from both a patch failure and a command
// failure.
func TestRepositoryWrongHeadFailsDistinctly(t *testing.T) {
	f := newRepoVerifierFixture(t)
	wrongSHA := strings.Repeat("f", 40)
	// Install a definition that pins a base sha the repository never
	// contained -- f.install always writes RepositoryPath/BaseSHA from the
	// fixture's real repo/head, so build the raw definition directly here
	// instead.
	raw, err := json.Marshal(repositoryCommandDefinition{
		Schema: repositoryCommandDefinitionSchema, CommandID: "wrong-head",
		RepositoryPath: f.repo, BaseSHA: wrongSHA,
	})
	if err != nil {
		t.Fatalf("marshal definition: %v", err)
	}
	dir := filepath.Join(f.root, "wrong-head")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir definition dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "definition.json"), raw, 0o644); err != nil {
		t.Fatalf("write definition: %v", err)
	}
	digest := sha256Hex(raw)
	patchRef := f.registerPatch()
	profile := f.profile("wrong-head", digest, "disabled", 30, 65536)
	req := f.request(profile, []wireExpectedObservation{{
		CheckID: "applies", Kind: "repository_patch_applies", Expected: "pass", ArtifactName: "patch",
	}}, []wireVerifierOutputRequirement{{Name: "patch", Artifact: patchRef, MediaType: "text/x-diff"}})

	doc := f.verifyOne(req)
	obs := firstObservation(t, doc, "applies")
	if obs.Status != "failed" {
		t.Fatalf("wrong head status %q, want failed", obs.Status)
	}
	if !strings.Contains(obs.Explanation, "checkout failed") {
		t.Fatalf("explanation %q does not name the checkout/head mismatch", obs.Explanation)
	}
	if strings.Contains(obs.Explanation, "does not apply") {
		t.Fatalf("wrong-head explanation %q wrongly blames patch application", obs.Explanation)
	}
}

// TestRepositoryCommandTimeoutKillsOwnedProcessGroup proves a timeout
// terminates the whole owned process group -- including a background child
// the accepted command spawned, not just the immediate shell -- and that
// bounded diagnostics captured before the kill are retained.
func TestRepositoryCommandTimeoutKillsOwnedProcessGroup(t *testing.T) {
	f := newRepoVerifierFixture(t)
	marker := filepath.Join(t.TempDir(), "should-not-exist")
	// The immediate shell blocks well past the timeout; its background
	// child would touch the marker file after 12s if it survived the kill.
	// The 6s profile timeout (well above the clone+checkout+apply steps'
	// own cost, which share the same budget) is deliberately generous
	// rather than tight, so this stays reliable on a loaded shared
	// machine: the property under test is "the group dies", not "it dies
	// within one second".
	script := "echo starting-diagnostic-output; (sleep 12; touch " + marker + ") & sleep 100"
	digest := f.install("timeout-check", []string{"sh", "-c", script})
	patchRef := f.registerPatch()
	profile := f.profile("timeout-check", digest, "disabled", 6, 65536)
	req := f.request(profile, []wireExpectedObservation{{
		CheckID: "runs", Kind: "repository_command", Expected: "pass", ArtifactName: "patch", CommandID: "timeout-check",
	}}, []wireVerifierOutputRequirement{{Name: "patch", Artifact: patchRef, MediaType: "text/x-diff"}})

	started := time.Now()
	doc := f.verifyOne(req)
	elapsed := time.Since(started)
	if elapsed > 30*time.Second {
		t.Fatalf("verify took %s, want termination reasonably close to the 6s timeout", elapsed)
	}
	obs := firstObservation(t, doc, "runs")
	if obs.Status != "failed" {
		t.Fatalf("timed-out command status %q (%s), want failed", obs.Status, obs.Explanation)
	}
	if !strings.Contains(obs.Explanation, "timed out") {
		t.Fatalf("explanation %q does not name the timeout", obs.Explanation)
	}
	if len(obs.Evidence) == 0 {
		t.Fatalf("timed-out check retained no diagnostics evidence at all")
	}

	// Give a genuinely surviving background child ample time to have
	// touched the marker (12s from process start), then confirm it never
	// did: the whole owned process group died with the timeout, not just
	// the parent shell.
	time.Sleep(10 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("marker file exists: the background child survived the timeout kill")
	}
}

// TestRepositoryModifiedCommandDefinitionCannotSatisfySealedChecks proves a
// locally modified verifier command -- one whose bytes no longer match the
// profile's pinned command_digest -- is refused as tampered rather than
// silently run, even though the modified command would trivially "pass".
func TestRepositoryModifiedCommandDefinitionCannotSatisfySealedChecks(t *testing.T) {
	f := newRepoVerifierFixture(t)
	sealedDigest := f.install("swap-check", []string{"sh", "-c", "exit 1"})
	// An attacker (or a worker with local disk access) swaps the installed
	// command for one that would trivially pass -- but the profile still
	// pins the digest of the original, honest command.
	tampered := repositoryCommandDefinition{
		Schema: repositoryCommandDefinitionSchema, CommandID: "swap-check",
		RepositoryPath: f.repo, BaseSHA: f.baseSHA, Argv: []string{"sh", "-c", "exit 0"},
		Env: []string{"PATH=/usr/bin:/bin"},
	}
	raw, _ := json.Marshal(tampered)
	if err := os.WriteFile(filepath.Join(f.root, "swap-check", "definition.json"), raw, 0o644); err != nil {
		t.Fatalf("swap definition: %v", err)
	}
	patchRef := f.registerPatch()
	profile := f.profile("swap-check", sealedDigest, "disabled", 30, 65536)
	req := f.request(profile, []wireExpectedObservation{{
		CheckID: "runs", Kind: "repository_command", Expected: "pass", ArtifactName: "patch", CommandID: "swap-check",
	}}, []wireVerifierOutputRequirement{{Name: "patch", Artifact: patchRef, MediaType: "text/x-diff"}})

	doc := f.verifyOne(req)
	obs := firstObservation(t, doc, "runs")
	if obs.Status != "tampered" {
		t.Fatalf("modified command definition status %q, want tampered", obs.Status)
	}
	if doc.Status == "passed" {
		t.Fatalf("overall verification status is passed despite a tampered command definition")
	}
	// effectiveVerdict is the same recomputation _execution.verification.
	// record applies before trusting a result: confirm it agrees the
	// modified command definition never yields a pass.
	verdict, _ := effectiveVerdict(
		[]wireExpectedObservation{{CheckID: "runs", Kind: "repository_command", Expected: "pass"}},
		doc.Observations)
	if verdict != "tampered" {
		t.Fatalf("effectiveVerdict %q, want tampered", verdict)
	}
}

// TestRepositoryWorkerAuthoredPassingOutputCannotSatisfySealedChecks proves
// that text a command prints -- exactly what a worker-authored "passing
// log" would look like -- has zero influence on the check's own verdict:
// only the real, independently observed exit code decides pass or fail.
func TestRepositoryWorkerAuthoredPassingOutputCannotSatisfySealedChecks(t *testing.T) {
	f := newRepoVerifierFixture(t)
	digest := f.install("fake-log-check", []string{"sh", "-c", "echo ALL TESTS PASSED; exit 1"})
	patchRef := f.registerPatch()
	profile := f.profile("fake-log-check", digest, "disabled", 30, 65536)
	req := f.request(profile, []wireExpectedObservation{{
		CheckID: "runs", Kind: "repository_command", Expected: "pass", ArtifactName: "patch", CommandID: "fake-log-check",
	}}, []wireVerifierOutputRequirement{{Name: "patch", Artifact: patchRef, MediaType: "text/x-diff"}})

	doc := f.verifyOne(req)
	obs := firstObservation(t, doc, "runs")
	if obs.Status != "failed" {
		t.Fatalf("status %q despite a nonzero exit code, want failed regardless of stdout content", obs.Status)
	}
	if obs.ObservedExitCode == nil || *obs.ObservedExitCode != 1 {
		t.Fatalf("observed exit code %v, want the real 1", obs.ObservedExitCode)
	}
	// Confirm the passing-looking text was genuinely captured as evidence
	// (so we know the test exercised the real path) while still failing.
	var ev struct {
		Kind     string `json:"kind"`
		Artifact struct {
			ID     contract.ID     `json:"id"`
			Digest contract.Digest `json:"digest"`
		} `json:"artifact"`
	}
	if len(obs.Evidence) == 0 {
		t.Fatalf("no evidence staged")
	}
	if err := json.Unmarshal(obs.Evidence[0], &ev); err != nil {
		t.Fatalf("decode evidence locator: %v", err)
	}
	raw, err := f.blobs.Open(context.Background(), ev.Artifact.Digest, 0, 1<<20)
	if err != nil {
		t.Fatalf("open staged evidence: %v", err)
	}
	defer func() { _ = raw.Close() }()
	var doc2 repositoryEvidenceDoc
	if err := json.NewDecoder(raw).Decode(&doc2); err != nil {
		t.Fatalf("decode evidence doc: %v", err)
	}
	if !strings.Contains(doc2.Stdout, "ALL TESTS PASSED") {
		t.Fatalf("evidence stdout %q does not contain the worker-visible passing text", doc2.Stdout)
	}
}

// TestRepositoryVerifierRootNotConfiguredRefusesWithNamedPrerequisite proves
// a missing containment prerequisite (no local runner root configured)
// refuses the check by name rather than silently downgrading to a pass.
func TestRepositoryVerifierRootNotConfiguredRefusesWithNamedPrerequisite(t *testing.T) {
	f := newRepoVerifierFixture(t)
	digest := f.install("patch-check", nil)
	patchRef := f.registerPatch()
	profile := f.profile("patch-check", digest, "disabled", 30, 65536)
	req := f.request(profile, []wireExpectedObservation{{
		CheckID: "applies", Kind: "repository_patch_applies", Expected: "pass", ArtifactName: "patch",
	}}, []wireVerifierOutputRequirement{{Name: "patch", Artifact: patchRef, MediaType: "text/x-diff"}})

	t.Setenv(repositoryVerifierRootEnv, "")
	doc := f.verifyOne(req)
	obs := firstObservation(t, doc, "applies")
	if obs.Status != "unavailable" {
		t.Fatalf("status %q with no configured runner root, want unavailable", obs.Status)
	}
	if !strings.Contains(obs.Explanation, repositoryVerifierRootEnv) {
		t.Fatalf("explanation %q does not name the missing prerequisite", obs.Explanation)
	}
	if doc.Status != "prerequisite_missing" {
		t.Fatalf("overall verification status %q, want prerequisite_missing", doc.Status)
	}
}

// TestRepositoryUnsupportedNetworkCapabilityRefusesWithNamedPrerequisite
// proves a profile declaring a network capability this runtime cannot
// enforce is refused by name rather than silently treated as satisfied.
func TestRepositoryUnsupportedNetworkCapabilityRefusesWithNamedPrerequisite(t *testing.T) {
	f := newRepoVerifierFixture(t)
	digest := f.install("patch-check", nil)
	patchRef := f.registerPatch()
	profile := f.profile("patch-check", digest, "qualified_allowlist", 30, 65536)
	req := f.request(profile, []wireExpectedObservation{{
		CheckID: "applies", Kind: "repository_patch_applies", Expected: "pass", ArtifactName: "patch",
	}}, []wireVerifierOutputRequirement{{Name: "patch", Artifact: patchRef, MediaType: "text/x-diff"}})

	doc := f.verifyOne(req)
	obs := firstObservation(t, doc, "applies")
	if obs.Status != "unavailable" {
		t.Fatalf("status %q for an unsupported network capability, want unavailable", obs.Status)
	}
	if !strings.Contains(obs.Explanation, "network") {
		t.Fatalf("explanation %q does not name the network capability as the prerequisite", obs.Explanation)
	}
}
