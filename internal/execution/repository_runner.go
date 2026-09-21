package execution

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
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// The controlled repository verifier: implements the repository_patch_applies
// and repository_command check kinds the default case in verifier.go's
// observe() used to report unavailable. It checks out a pinned local
// repository at a pinned base commit into a fresh disposable workspace,
// applies only the one reported patch artifact the check names, and (for
// repository_command) runs the one accepted argv/env command the pinned
// profile's command_id names -- never anything the request or the worker
// supplies directly.
//
// Containment honesty (internal/execution/AGENTS.md, P20 assignment): this
// runs the host git binary and the accepted command through os/exec with the
// child in its own process group, a wall-time bound from the pinned
// profile's timeout_seconds, and a captured output-byte bound. That is real,
// working containment for those three things and nothing else: there is no
// namespace, cgroup, filesystem or network isolation, so this file never
// describes it as a sandbox. Process-group kill terminates every process
// that stayed in the group -- the ordinary case for a shell-spawned child
// tree -- not a live cap on how many processes may exist at once, and a
// process that deliberately re-parents outside the group escapes it. Disk
// usage is bounded only by using one disposable temp directory per check and
// removing it afterward, never a quota. The profile's "qualified_allowlist"
// network capability has no enforcement mechanism available in this runtime
// (docs/implementation/dependencies.lock.json: no sandboxing library is
// pinned for the controlled repository runner) and is refused outright with
// a named prerequisite; only "disabled" is supported, which this runner
// satisfies by construction -- the source repository is always a local
// filesystem path and no network syscall is ever made on its behalf.

// repositoryVerifierRootEnv names the environment variable an installation
// sets to the local directory holding prequalified repository command
// definitions (see repositoryCommandDefinition). command_id, runner_profile
// and environment_profile in the pinned profile document resolve only
// against files already present under this root; nothing in the request or
// an attempt's report can add, choose or alter an entry -- matching
// "command_id, runner_profile and environment_profile resolve only to
// prequalified local verifiers; no request supplies executable paths or
// argv" (internal/execution/AGENTS.md). Unset or empty is a missing
// prerequisite, never a silent pass.
const repositoryVerifierRootEnv = "ZATITI_REPOSITORY_VERIFIER_ROOT"

// repositoryCommandDefinitionSchema is the local (execution-owned, not part
// of the frozen wire contract) file format an installation provisions under
// repositoryVerifierRootEnv. Its exact bytes are pinned by the profile's
// command_digest, so a tampered or substituted definition is caught as
// "tampered" rather than silently run.
const repositoryCommandDefinitionSchema = "zatiti.execution.repository-command-definition/v1"

// repositoryCommandDefinition is one prequalified local repository verifier
// entry: which real repository/base to check out and which real argv/env to
// run for repository_command checks. repository_patch_applies checks use
// only RepositoryPath/BaseSHA. A worker cannot influence any field here --
// it is read from local disk, addressed by the pinned profile's command_id
// and integrity-checked against the pinned profile's command_digest, never
// from request or attempt data.
type repositoryCommandDefinition struct {
	Schema         string   `json:"schema"`
	CommandID      string   `json:"command_id"`
	RepositoryPath string   `json:"repository_path"`
	BaseSHA        string   `json:"base_sha"`
	Argv           []string `json:"argv,omitempty"`
	Env            []string `json:"env,omitempty"`
}

// loadRepositoryDefinition reads and decodes the local definition file for
// commandID under root, returning its decoded value alongside the exact raw
// bytes so the caller can independently hash them against command_digest.
func loadRepositoryDefinition(root, commandID string) (repositoryCommandDefinition, []byte, error) {
	if commandID == "" {
		return repositoryCommandDefinition{}, nil, errors.New("empty command_id")
	}
	path := filepath.Join(root, commandID, "definition.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return repositoryCommandDefinition{}, nil, err
	}
	var def repositoryCommandDefinition
	if err := json.Unmarshal(raw, &def); err != nil {
		return repositoryCommandDefinition{}, nil, fmt.Errorf("decode local definition: %w", err)
	}
	if def.Schema != repositoryCommandDefinitionSchema {
		return repositoryCommandDefinition{}, nil, fmt.Errorf("local definition schema %q, want %q", def.Schema, repositoryCommandDefinitionSchema)
	}
	if def.CommandID != commandID {
		return repositoryCommandDefinition{}, nil, fmt.Errorf("local definition command_id %q does not match the pinned %q", def.CommandID, commandID)
	}
	if def.RepositoryPath == "" || def.BaseSHA == "" {
		return repositoryCommandDefinition{}, nil, errors.New("local definition is missing repository_path or base_sha")
	}
	return def, raw, nil
}

// observeRepository establishes one repository_patch_applies or
// repository_command check. It never trusts request-carried argv/env or the
// worker's own report: the only inputs are the pinned profile's opaque
// identifiers (resolved against the local prequalified registry) and the
// worker's reported patch artifact bytes (resolved by content digest through
// the blob store and then actually applied, never assumed).
func (v *verifier) observeRepository(ctx context.Context, want wireExpectedObservation, request wireVerificationRequest) wireObservedCheck {
	obs := wireObservedCheck{CheckID: want.CheckID, Kind: want.Kind, Evidence: []json.RawMessage{}, Explanation: ""}

	var profile wireRepositoryProfile
	if err := decodeJSON(string(request.Profile), &profile); err != nil || profile.Kind != "repository_patch" {
		obs.Status = "unavailable"
		obs.Explanation = "pinned verifier profile is not a repository_patch profile"
		return obs
	}
	if want.CommandID != "" && want.CommandID != profile.CommandID {
		obs.Status = "failed"
		obs.Explanation = fmt.Sprintf("check declares command_id %q but the pinned profile names %q", want.CommandID, profile.CommandID)
		return obs
	}
	if profile.Network != "disabled" {
		obs.Status = "unavailable"
		obs.Explanation = fmt.Sprintf(
			"network capability %q has no enforcement mechanism in this runtime; only \"disabled\" is supported", profile.Network)
		return obs
	}
	root := os.Getenv(repositoryVerifierRootEnv)
	if root == "" {
		obs.Status = "unavailable"
		obs.Explanation = "repository verifier root is not configured: set " + repositoryVerifierRootEnv + " to an installation-provisioned directory"
		return obs
	}
	def, defBytes, err := loadRepositoryDefinition(root, profile.CommandID)
	if err != nil {
		obs.Status = "unavailable"
		obs.Explanation = fmt.Sprintf("prequalified repository command definition %q is not installed: %v", profile.CommandID, err)
		return obs
	}
	if sha256Hex(defBytes) != profile.CommandDigest {
		obs.Status = "tampered"
		obs.Explanation = "the local command definition does not match the pinned command_digest"
		return obs
	}
	if _, err := exec.LookPath("git"); err != nil {
		obs.Status = "unavailable"
		obs.Explanation = "required containment prerequisite unavailable: git executable not found on PATH"
		return obs
	}

	timeout := time.Duration(profile.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runner := &repositoryRunner{clock: v.clock, blobs: v.blobs, maxOutputBytes: profile.MaxOutputBytes}
	result := runner.run(ctx, def, want, request.Outputs, timeout)
	obs.Status = result.status
	obs.Explanation = result.explanation
	obs.ObservedExitCode = result.exitCode
	if result.evidence != nil {
		obs.Evidence = result.evidence
	}
	return obs
}

// repositoryRunner executes one checkout+patch+(command) cycle in a fresh
// disposable workspace under a bounded, owned process group.
type repositoryRunner struct {
	clock          contract.Clock
	blobs          contract.BlobStore
	maxOutputBytes int64
}

// repositoryCheckResult is the runner's outcome for one expected observation.
type repositoryCheckResult struct {
	status      string // passed | failed | unavailable
	explanation string
	exitCode    *int64
	evidence    []json.RawMessage
}

func unavailableResult(format string, args ...any) repositoryCheckResult {
	return repositoryCheckResult{status: "unavailable", explanation: fmt.Sprintf(format, args...)}
}

func failedResult(format string, args ...any) repositoryCheckResult {
	return repositoryCheckResult{status: "failed", explanation: fmt.Sprintf(format, args...)}
}

// run checks out def.RepositoryPath at def.BaseSHA into a fresh disposable
// workspace, resolves the one named patch output (if the check needs one),
// applies it and, for repository_command, runs def.Argv/def.Env. The whole
// cycle shares one wall-time budget (timeout); on expiry the owned process
// group is terminated and bounded diagnostics captured so far are retained.
// Only the runner's own disposable workspace is ever removed -- def.
// RepositoryPath, the pristine local source, is never modified.
func (rr *repositoryRunner) run(ctx context.Context, def repositoryCommandDefinition, want wireExpectedObservation, outputs []wireVerifierOutputRequirement, timeout time.Duration) repositoryCheckResult {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	workspace, err := os.MkdirTemp("", "zatiti-verify-*")
	if err != nil {
		return unavailableResult("could not create a disposable workspace: %v", err)
	}
	defer func() { _ = os.RemoveAll(workspace) }()

	gitVersion := rr.gitVersion(cctx)

	if res := rr.git(cctx, "", "clone", "--quiet", "--no-checkout", def.RepositoryPath, workspace); res.startErr != nil || !res.ok() {
		return unavailableResult("could not clone the pinned repository into a disposable workspace: %s", res.summary())
	}
	if res := rr.git(cctx, workspace, "checkout", "--quiet", "--detach", def.BaseSHA); res.startErr != nil || !res.ok() {
		return failedResult("repository checkout failed at the pinned base %s: %s", def.BaseSHA, res.summary())
	}
	observedBase := strings.TrimSpace(string(rr.git(cctx, workspace, "rev-parse", "HEAD").stdout))
	if observedBase != def.BaseSHA {
		return failedResult("checked out head %s does not match the pinned base %s", observedBase, def.BaseSHA)
	}

	explicitName := want.ArtifactName != ""
	artifactName := want.ArtifactName
	if !explicitName {
		artifactName = "patch"
	}
	patchRef, hasPatch := findNamedOutput(outputs, artifactName)
	requirePatch := want.Kind == "repository_patch_applies" || explicitName
	if requirePatch && !hasPatch {
		return failedResult("required patch output %q was not present in the attempt's reported outputs", artifactName)
	}

	var patchDigest contract.Digest
	if hasPatch {
		patchBytes, err := rr.readPatch(cctx, patchRef.Digest)
		if err != nil {
			return unavailableResult("could not read the reported patch artifact: %v", err)
		}
		patchDigest = sha256Hex(patchBytes)
		patchPath, cleanup, err := writeTempPatch(patchBytes)
		if err != nil {
			return unavailableResult("could not stage the reported patch for application: %v", err)
		}
		defer cleanup()

		if want.Kind == "repository_patch_applies" {
			res := rr.git(cctx, workspace, "apply", "--check", patchPath)
			if res.startErr != nil || !res.ok() {
				return repositoryCheckResult{
					status:      "failed",
					explanation: fmt.Sprintf("patch does not apply cleanly against the pinned base %s: %s", def.BaseSHA, res.summary()),
					evidence:    rr.stageEvidence(cctx, want, def, observedBase, "", patchDigest, gitVersion, nil),
				}
			}
			return repositoryCheckResult{
				status:      "passed",
				explanation: "patch applies cleanly against the pinned base",
				evidence:    rr.stageEvidence(cctx, want, def, observedBase, "", patchDigest, gitVersion, nil),
			}
		}
		res := rr.git(cctx, workspace, "apply", patchPath)
		if res.startErr != nil || !res.ok() {
			return failedResult("patch does not apply cleanly against the pinned base %s: %s", def.BaseSHA, res.summary())
		}
	}

	if want.Kind == "repository_patch_applies" {
		// requirePatch is always true for this kind, so hasPatch is always
		// true above and this branch is unreachable; kept as an explicit
		// guard rather than an assumption.
		return unavailableResult("repository_patch_applies observed no patch to check")
	}

	if len(def.Argv) == 0 {
		return unavailableResult("local command definition %q names no argv to run", def.CommandID)
	}
	observedHead := rr.observedHead(cctx, workspace)
	runRes := rr.exec(cctx, workspace, def.Argv, def.Env)
	evidence := rr.stageEvidence(cctx, want, def, observedBase, observedHead, patchDigest, gitVersion, &runRes)

	if runRes.startErr != nil {
		return repositoryCheckResult{status: "unavailable",
			explanation: fmt.Sprintf("accepted command could not be started: %v", runRes.startErr), evidence: evidence}
	}
	if runRes.timedOut {
		return repositoryCheckResult{status: "failed",
			explanation: fmt.Sprintf("accepted command timed out after %s and the owned process group was terminated", timeout),
			evidence:    evidence}
	}
	expected := int64(0)
	if want.ExpectedExitCode != nil {
		expected = *want.ExpectedExitCode
	}
	if runRes.exitCode == nil {
		return repositoryCheckResult{status: "failed",
			explanation: "accepted command did not report a usable exit code", evidence: evidence}
	}
	if *runRes.exitCode != expected {
		return repositoryCheckResult{status: "failed", exitCode: runRes.exitCode,
			explanation: fmt.Sprintf("accepted command exited %d, want %d", *runRes.exitCode, expected), evidence: evidence}
	}
	return repositoryCheckResult{status: "passed", exitCode: runRes.exitCode,
		explanation: fmt.Sprintf("accepted command exited %d as expected", *runRes.exitCode), evidence: evidence}
}

// observedHead computes a real git tree identity for the current (patched)
// working tree without creating any commit or touching any ref: `git add
// -A` stages exactly what is on disk, then `git write-tree` returns the
// resulting tree object's SHA. Best-effort evidence only; a failure here
// never changes the check's pass/fail outcome.
func (rr *repositoryRunner) observedHead(ctx context.Context, workspace string) string {
	if res := rr.git(ctx, workspace, "add", "-A"); res.startErr != nil || !res.ok() {
		return ""
	}
	res := rr.git(ctx, workspace, "write-tree")
	if res.startErr != nil || !res.ok() {
		return ""
	}
	return strings.TrimSpace(string(res.stdout))
}

// gitVersion reports the exact local git binary's version string for
// evidence; failure to determine it is not fatal to the check.
func (rr *repositoryRunner) gitVersion(ctx context.Context) string {
	res := rr.git(ctx, "", "--version")
	if res.startErr != nil || !res.ok() {
		return "unknown"
	}
	return strings.TrimSpace(string(res.stdout))
}

// readPatch reads the reported patch artifact's full bytes from the blob
// store, bounded so an oversized artifact cannot be used to exhaust memory.
func (rr *repositoryRunner) readPatch(ctx context.Context, digest contract.Digest) ([]byte, error) {
	rc, err := rr.blobs.Open(ctx, digest, 0, defaultVerifierMaxBytes+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, defaultVerifierMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > defaultVerifierMaxBytes {
		return nil, fmt.Errorf("patch artifact exceeds the %d byte read bound", defaultVerifierMaxBytes)
	}
	return data, nil
}

// stageEvidence stages one JSON evidence document capturing exactly what
// this runner independently observed: the pinned repository/base, the
// checkout's observed head, the reported patch's own digest, the local git
// version, and (for a command check) argv/exit status/bounded output. It is
// published through the blob store like every other verifier artifact and
// referenced as an ArtifactLocator, never invented inline. A staging
// failure is swallowed (evidence is diagnostic, not the pass/fail signal)
// and simply yields no evidence entries.
func (rr *repositoryRunner) stageEvidence(ctx context.Context, want wireExpectedObservation, def repositoryCommandDefinition,
	observedBase, observedHead string, patchDigest contract.Digest, gitVersion string, cmd *commandRunResult) []json.RawMessage {
	doc := repositoryEvidenceDoc{
		Schema:         "zatiti.execution.repository-verification-evidence/v1",
		CheckID:        want.CheckID,
		Kind:           want.Kind,
		RepositoryPath: def.RepositoryPath,
		BaseSHA:        def.BaseSHA,
		ObservedBase:   observedBase,
		ObservedHead:   observedHead,
		PatchDigest:    patchDigest,
		Runner:         fmt.Sprintf("os/exec owned-process-group (go %s; %s)", runtime.Version(), gitVersion),
	}
	if cmd != nil {
		doc.Argv = def.Argv
		doc.ExitCode = cmd.exitCode
		doc.TimedOut = cmd.timedOut
		doc.DurationMS = cmd.duration.Milliseconds()
		doc.Stdout = boundedPreview(cmd.stdout)
		doc.Stderr = boundedPreview(cmd.stderr)
	}
	raw, err := canonicalJSON(doc)
	if err != nil {
		return nil
	}
	digest := sha256Hex(raw)
	stagingRef, stagedDigest, _, err := rr.blobs.Stage(ctx, bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil
	}
	if err := rr.blobs.Publish(ctx, stagingRef, stagedDigest); err != nil {
		return nil
	}
	locator := wireArtifactLocator{Kind: "artifact", Artifact: &wireArtifactRef{ID: uuidFromDigest(digest), Digest: digest}}
	locatorRaw, err := json.Marshal(locator)
	if err != nil {
		return nil
	}
	return []json.RawMessage{locatorRaw}
}

// repositoryEvidenceDoc is the execution-owned (not frozen wire contract)
// shape staged as independent evidence for a repository verification check.
type repositoryEvidenceDoc struct {
	Schema         string          `json:"schema"`
	CheckID        string          `json:"check_id"`
	Kind           string          `json:"kind"`
	RepositoryPath string          `json:"repository_path"`
	BaseSHA        string          `json:"base_sha"`
	ObservedBase   string          `json:"observed_base_sha"`
	ObservedHead   string          `json:"observed_head_sha,omitempty"`
	PatchDigest    contract.Digest `json:"patch_digest,omitempty"`
	Runner         string          `json:"runner"`
	Argv           []string        `json:"argv,omitempty"`
	ExitCode       *int64          `json:"exit_code,omitempty"`
	TimedOut       bool            `json:"timed_out"`
	DurationMS     int64           `json:"duration_ms"`
	Stdout         string          `json:"stdout,omitempty"`
	Stderr         string          `json:"stderr,omitempty"`
}

// commandRunResult is the outcome of one owned-process-group execution.
type commandRunResult struct {
	exitCode *int64
	stdout   []byte
	stderr   []byte
	timedOut bool
	startErr error
	duration time.Duration
}

// ok reports whether the process started, ran to completion (not killed by
// our own timeout) and exited zero -- the convenience check the internal git
// plumbing steps use; the accepted command's own exit-code comparison is
// handled separately against want.ExpectedExitCode.
func (r commandRunResult) ok() bool {
	return r.startErr == nil && !r.timedOut && r.exitCode != nil && *r.exitCode == 0
}

// summary renders a bounded, human-readable failure summary for embedding in
// an explanation string.
func (r commandRunResult) summary() string {
	if r.startErr != nil {
		return "could not start: " + r.startErr.Error()
	}
	if r.timedOut {
		return "timed out"
	}
	code := int64(-1)
	if r.exitCode != nil {
		code = *r.exitCode
	}
	return fmt.Sprintf("exit %d: %s", code, boundedPreview(r.stderr))
}

// git runs one internal git plumbing step (clone/checkout/apply/rev-parse/
// write-tree/--version) inheriting the verifier process's own environment,
// minus every GIT_* variable -- these are the runner's own trusted
// invocations of git, never the accepted command, which always gets
// exactly def.Env and nothing inherited. Stripping GIT_* matters, not just
// as hygiene: git honors GIT_DIR/GIT_INDEX_FILE/GIT_WORK_TREE from the
// environment ahead of ordinary directory-based discovery, so a runner
// process that itself happened to inherit those (for example, if it were
// ever started from inside another git command) would otherwise have every
// git call here silently redirected at that other repository instead of
// the intended dir, regardless of dir itself.
func (rr *repositoryRunner) git(ctx context.Context, dir string, args ...string) commandRunResult {
	return rr.exec(ctx, dir, append([]string{"git"}, args...), gitEnv())
}

// gitEnv returns the calling process's environment with every GIT_*
// variable removed.
func gitEnv() []string {
	base := os.Environ()
	out := make([]string, 0, len(base))
	for _, kv := range base {
		if strings.HasPrefix(kv, "GIT_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// exec runs one owned-process-group child: the child starts its own process
// group so a timeout or output-bound breach can terminate the whole tree via
// a single negative-pid signal, output is captured into a hard byte bound,
// and env is exactly what the caller supplies (nil means "inherit this
// process's own environment", used only for the runner's own git plumbing;
// the accepted command always receives an explicit, possibly empty, slice).
func (rr *repositoryRunner) exec(ctx context.Context, dir string, argv []string, env []string) commandRunResult {
	if len(argv) == 0 {
		return commandRunResult{startErr: errors.New("empty argv")}
	}
	resolved, err := resolveArgv0(argv)
	if err != nil {
		return commandRunResult{startErr: err}
	}
	cmd := exec.CommandContext(ctx, resolved[0], resolved[1:]...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 5 * time.Second
	// exec.CommandContext's default Cancel only signals cmd.Process itself,
	// which leaves grandchildren the child spawned free to keep running
	// past the deadline. Killing the whole negative-pid process group is
	// the one actually-owned containment guarantee this runner provides.
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	limit := rr.maxOutputBytes
	if limit <= 0 {
		limit = defaultVerifierMaxBytes
	}
	stdout := newBoundedWriter(limit)
	stderr := newBoundedWriter(limit)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	started := rr.clock.Now()
	if err := cmd.Start(); err != nil {
		return commandRunResult{startErr: err}
	}
	pgid := cmd.Process.Pid
	done := make(chan struct{})
	go func() {
		select {
		case <-stdout.exceeded:
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		case <-stderr.exceeded:
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		case <-done:
		}
	}()
	waitErr := cmd.Wait()
	close(done)
	duration := rr.clock.Now().Sub(started)
	res := commandRunResult{stdout: stdout.bytes(), stderr: stderr.bytes(), duration: duration,
		timedOut: errors.Is(ctx.Err(), context.DeadlineExceeded)}

	if waitErr == nil {
		zero := int64(0)
		res.exitCode = &zero
		return res
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		code := int64(exitErr.ExitCode())
		if code >= 0 {
			res.exitCode = &code
		}
		return res
	}
	// A non-ExitError Wait failure (killed by our own Cancel, a start-up
	// race, ...) carries no usable exit code.
	return res
}

// resolveArgv0 resolves argv[0] to an absolute path via the trusted runner
// host's own PATH (never the accepted command's own def.Env, which the
// child process receives as its complete environment and which may
// deliberately carry no PATH at all).
func resolveArgv0(argv []string) ([]string, error) {
	if strings.Contains(argv[0], string(os.PathSeparator)) {
		return argv, nil
	}
	resolved, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, err
	}
	out := append([]string{resolved}, argv[1:]...)
	return out, nil
}

// findNamedOutput looks up one worker-reported output artifact by its bound
// name -- the same resolved, published bindings attempt_ops.go's reportAttempt
// already seals into the request, never raw worker-submitted claims.
func findNamedOutput(outputs []wireVerifierOutputRequirement, name string) (wireArtifactRef, bool) {
	for _, o := range outputs {
		if o.Name == name {
			return o.Artifact, true
		}
	}
	return wireArtifactRef{}, false
}

// writeTempPatch stages the reported patch's bytes as a real file `git
// apply` can consume, returning a cleanup that removes exactly that one
// file.
func writeTempPatch(patch []byte) (string, func(), error) {
	f, err := os.CreateTemp("", "zatiti-verify-patch-*.diff")
	if err != nil {
		return "", func() {}, err
	}
	path := f.Name()
	_, writeErr := f.Write(patch)
	closeErr := f.Close()
	if writeErr != nil {
		_ = os.Remove(path)
		return "", func() {}, writeErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", func() {}, closeErr
	}
	return path, func() { _ = os.Remove(path) }, nil
}

// boundedPreview renders a short, bounded text preview of captured output
// bytes for embedding in an explanation or evidence document.
func boundedPreview(b []byte) string {
	const previewLimit = 4096
	if len(b) > previewLimit {
		b = b[:previewLimit]
	}
	return string(b)
}

// boundedWriter caps how many bytes it retains and closes its exceeded
// channel exactly once the bound is crossed, so the caller can actively
// terminate the owned process group as soon as the bound is breached rather
// than merely truncating what gets kept. Write never itself returns an
// error (which would otherwise surface as a broken-pipe Wait failure); the
// exceeded signal is the deliberate, observable containment action instead.
type boundedWriter struct {
	mu       sync.Mutex
	limit    int64
	buf      bytes.Buffer
	exceeded chan struct{}
	signaled bool
}

func newBoundedWriter(limit int64) *boundedWriter {
	if limit <= 0 {
		limit = defaultVerifierMaxBytes
	}
	return &boundedWriter{limit: limit, exceeded: make(chan struct{})}
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if int64(w.buf.Len()) < w.limit {
		room := w.limit - int64(w.buf.Len())
		if room > int64(len(p)) {
			w.buf.Write(p)
		} else {
			w.buf.Write(p[:room])
		}
	}
	if int64(w.buf.Len()) >= w.limit && !w.signaled {
		w.signaled = true
		close(w.exceeded)
	}
	return len(p), nil
}

func (w *boundedWriter) bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buf.Bytes()...)
}
