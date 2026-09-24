package qualification_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// P47 item 3: install produced artifacts on clean macOS/Linux hosts and
// execute service restart/backup/restore/uninstall journeys.
//
// Per this session's hard constraint against touching the real host, this
// follows exactly the pattern packaging's own tests (packaging/cli_test.go,
// landed under P45) already established: a temp directory standing in for
// a "clean host", and a recording shell script standing in for the real
// launchctl/systemctl, never the real ones. What is genuinely new here is
// that this package qualifies the PRODUCED ARTIFACT from outside the
// packaging package (driving the real, built packaging/cmd/zatiti-pack
// binary as a subprocess, since tests/qualification's allowed imports do
// not include the packaging library itself) and, where it is safe to do so
// without touching the real host, actually runs the real produced zatiti
// binary the packaging driver installed -- proving claims packaging/
// QUALIFICATION.md's own boundary table lists as unproven by its unit
// suite ("the controller binary accepts serve and the arguments the
// launcher passes", "run a CLI query against the private socket").
//
// packaging/QUALIFICATION.md itself lists what remains unproven without a
// real host: real launchd/systemd acceptance, a real keychain, a real
// Flutter desktop bundle, and code signing/notarization. None of those are
// attempted here; they are recorded as still-open boundary items on the
// case, exactly as that document already names them.

const controllerServiceLabel = "com.zatiti.controller"

// buildZatitiPack cross-compiles packaging/cmd/zatiti-pack for goos/goarch
// into dir and returns its path. zatiti-pack is packaging.RunCLI behind a
// five-line main package (packaging/cli.go's own doc comment), so driving
// this built binary exercises exactly the code a real release would ship.
func buildZatitiPack(dir, goos, goarch string) (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "zatiti-pack")
	if goos == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-p", "2", "-o", bin, "./packaging/cmd/zatiti-pack")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	if out, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build ./packaging/cmd/zatiti-pack: %v\n%s", err, out)
	}
	return bin, nil
}

// buildZatitiFor cross-compiles cmd/zatiti for goos/goarch into dir: the
// same "produced artifact" controller_test.go's buildBinary builds for the
// host platform, generalized so the Linux distribution case packages a
// real cross-compiled Linux binary rather than a synthetic placeholder.
func buildZatitiFor(dir, goos, goarch string) (string, error) {
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "zatiti")
	if goos == "windows" {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-p", "2", "-o", bin, "./cmd/zatiti")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	if out, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build ./cmd/zatiti: %v\n%s", err, out)
	}
	return bin, nil
}

// packRun invokes the built zatiti-pack binary as a subprocess and decodes
// its one JSON result document, exactly as an operator's shell would.
func packRun(bin string, args ...string) (data map[string]any, stdout, stderr string, err error) {
	cmd := exec.Command(bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	stdout, stderr = out.String(), errb.String()
	if runErr != nil {
		return nil, stdout, stderr, fmt.Errorf("zatiti-pack %v: %w\nstderr: %s", args, runErr, stderr)
	}
	if stdout != "" {
		_ = json.Unmarshal([]byte(stdout), &data)
	}
	return data, stdout, stderr, nil
}

// rawGitRevision is the repository HEAD as a bare lowercase hex commit id,
// with no "-dirty" suffix: the release manifest schema requires exactly 40
// or 64 hex characters, and this worktree has local edits while this card
// is in flight, so gitRevision()'s "-dirty"-suffixed form (used for the
// evidence "source_revision" version field elsewhere in this package)
// cannot also serve as the manifest's own source_revision.
func rawGitRevision() string {
	root, err := moduleRoot()
	if err != nil {
		return strings.Repeat("0", 40)
	}
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return strings.Repeat("0", 40)
	}
	rev := strings.TrimSpace(string(out))
	if len(rev) != 40 && len(rev) != 64 {
		return strings.Repeat("0", 40)
	}
	return rev
}

// packagingWriteFakeServiceTool is a Go port of packaging's own test-only
// recording stand-in for launchctl/systemctl (packaging/cli_test.go's
// writeFakeServiceTool, verified there against TestLaunchdManagerCommands
// and TestSystemdManagerCommands): pure shell builtins, no external
// command, logging every invocation and tracking each label's loaded state
// as a file under stateDir. It never touches a real service manager.
func packagingWriteFakeServiceTool(t *testing.T, logPath, stateDir string) string {
	t.Helper()
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"LOG=\"" + logPath + "\"\n" +
		"STATE=\"" + stateDir + "\"\n" +
		"printf '%s\\n' \"$*\" >> \"$LOG\"\n" +
		"case \"$1\" in\n" +
		"  print)\n" +
		"    label=\"${2##*/}\"\n" +
		"    val=\"\"\n" +
		"    if [ -f \"$STATE/$label\" ]; then IFS= read -r val < \"$STATE/$label\"; fi\n" +
		"    [ \"$val\" = loaded ] && exit 0\n" +
		"    exit 113\n" +
		"    ;;\n" +
		"  bootstrap)\n" +
		"    label=\"${3##*/}\"; label=\"${label%.plist}\"\n" +
		"    printf loaded > \"$STATE/$label\"\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"  bootout)\n" +
		"    label=\"${2##*/}\"\n" +
		"    printf unloaded > \"$STATE/$label\"\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"  --user)\n" +
		"    case \"$2\" in\n" +
		"      daemon-reload) exit 0 ;;\n" +
		"      enable)\n" +
		"        label=\"${4##*/}\"; label=\"${label%.service}\"\n" +
		"        printf loaded > \"$STATE/$label\"\n" +
		"        exit 0\n" +
		"        ;;\n" +
		"      disable)\n" +
		"        label=\"${4##*/}\"; label=\"${label%.service}\"\n" +
		"        printf unloaded > \"$STATE/$label\"\n" +
		"        exit 0\n" +
		"        ;;\n" +
		"    esac\n" +
		"    ;;\n" +
		"esac\n" +
		"exit 1\n"
	path := filepath.Join(t.TempDir(), "fakectl.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func packagingAssertServiceLoaded(t *testing.T, stateDir, label string, want bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(stateDir, label))
	got := err == nil && string(raw) == "loaded"
	if got != want {
		t.Fatalf("service %s loaded=%v (file=%q, err=%v), want %v", label, got, raw, err, want)
	}
}

// distributionLayout is this test's own independent computation of the
// packaging library's Layout for a given (goos, home, stateDir): it cannot
// import the packaging package (not in this root's allowed imports), so it
// reproduces packaging/layout.go's NewLayout path rules from that file's
// own committed source, which this package read and cites here.
type distributionLayout struct {
	distRoot, current, unitDir, controllerExecutable string
}

func computeDistributionLayout(goos, home string) distributionLayout {
	var l distributionLayout
	switch goos {
	case "darwin":
		l.distRoot = filepath.Join(home, "Library", "Application Support", "zatiti-dist")
		l.unitDir = filepath.Join(home, "Library", "LaunchAgents")
	case "linux":
		l.distRoot = filepath.Join(home, ".local", "share", "zatiti-dist")
		l.unitDir = filepath.Join(home, ".config", "systemd", "user")
	}
	l.current = filepath.Join(l.distRoot, "current")
	l.controllerExecutable = filepath.Join(l.current, "bin", "zatiti")
	return l
}

// packagingDescriptor writes the assemble-command's JSON descriptor
// (packaging/cli_assemble.go's assembleDescriptor) for a controller
// distribution whose bin/zatiti artifact is the real produced binary at
// controllerBinPath -- not a synthetic placeholder. Serenity remains a
// synthetic pin: this repository's Serenity dependency is itself still
// unresolved (docs/implementation-remediation; packaging/QUALIFICATION.md
// precondition 2), so packaging's own tests pin one synthetically too; this
// descriptor documents that plainly rather than fabricating a real one.
func packagingDescriptor(t *testing.T, root, controllerBinPath, version, goos, arch string) map[string]any {
	t.Helper()
	licenseRoot, err := moduleRoot()
	if err != nil {
		t.Fatalf("module root: %v", err)
	}
	license, err := os.ReadFile(filepath.Join(licenseRoot, "LICENSE"))
	if err != nil {
		t.Fatalf("reading the repository LICENSE: %v", err)
	}
	controllerBytes, err := os.ReadFile(controllerBinPath)
	if err != nil {
		t.Fatalf("reading the produced controller binary: %v", err)
	}
	write := func(rel string, data []byte, mode os.FileMode) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	write("bin/zatiti", controllerBytes, 0o755)
	write("serenity/serenity", []byte("qualification-synthetic serenity runtime (real Serenity pin unresolved)"), 0o755)
	write("serenity/read-facade", []byte("qualification-synthetic serenity read facade"), 0o755)
	write("LICENSE", license, 0o644)
	write("licenses/serenity.NOTICE", []byte("qualification-synthetic notice"), 0o644)
	sbom := fmt.Sprintf(`{"spdxVersion":"SPDX-2.3","packages":[{"name":"github.com/zatiti/zatiti","versionInfo":"%s"},{"name":"example.invalid/serenity","versionInfo":"1.4.0"}]}`, version)
	write("sbom.spdx.json", []byte(sbom), 0o644)
	write("evidence/serenity.json", []byte(`{"synthetic":true,"reason":"the real Serenity dependency pin is unresolved on this tree"}`), 0o644)

	serenityRevision := strings.Repeat("cd", 20) // 40 hex chars, synthetic
	return map[string]any{
		"distribution": "controller", "version": version,
		"target":          map[string]any{"os": goos, "arch": arch},
		"source_revision": rawGitRevision(), "toolchain": runtime.Version(),
		"kinds": map[string]string{
			"bin/zatiti":               "controller_binary",
			"serenity/serenity":        "serenity_runtime",
			"serenity/read-facade":     "serenity_read_facade",
			"LICENSE":                  "license",
			"licenses/serenity.NOTICE": "notice",
			"sbom.spdx.json":           "sbom",
			"evidence/serenity.json":   "attestation_evidence",
		},
		"licenses": []map[string]any{
			{"component": "github.com/zatiti/zatiti", "version": version, "spdx": "Apache-2.0", "notice": "LICENSE"},
			{"component": "example.invalid/serenity", "version": "1.4.0", "spdx": "MIT", "notice": "licenses/serenity.NOTICE"},
		},
		"sbom": map[string]any{"format": "spdx-json", "path": "sbom.spdx.json"},
		"serenity": map[string]any{
			"source": "example.invalid/serenity", "version": "1.4.0", "revision": serenityRevision,
			"license": "MIT", "interface_version": "v1",
			"runtime": "serenity/serenity", "read_facade": "serenity/read-facade",
			"evidence": "evidence/serenity.json",
		},
		"secure_helper": map[string]any{"kind": "headless_master_key"},
	}
}

// waitForSocket polls until path exists as a socket or deadline elapses.
func waitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("no socket at %s after %s", path, timeout)
}

// qualifyDistribution is QUALIFICATION.macos_distribution and
// QUALIFICATION.linux_distribution: assemble, sign and verify a release
// tree carrying the real produced zatiti binary, install it with the real
// zatiti-pack driver onto a temp-directory "clean host" through a recording
// service-manager stand-in, and -- when goos matches this host, so the
// produced binary can actually be executed -- run the real installed
// binary directly (never through a real launchd/systemd) to prove it
// accepts the launcher's own arguments, answers a real CLI query over its
// real private socket, and carries the backup/restore journey to its
// current, already-documented honest ceiling (P46/P33: restore fails
// closed at StageCandidate with prerequisite_missing and the installation
// stays safely paused, never silently resumed). Then restart (recording)
// and uninstall.
func qualifyDistribution(t *testing.T, caseID, goos string) {
	c := beginCase(t, caseID, "QUALIFICATION",
		"Documented installation commands work from actual artifacts on a clean supported host; controller ownership, private socket, secure store, lock, Serenity lifecycle and backup/restore exercised.",
		"Release evidence identifies exact OS/architecture/artifact/source/tool versions and observed results; compile success is not platform qualification.")
	native := runtime.GOOS == goos
	arch := runtime.GOARCH
	c.version("target_os", goos)
	c.version("target_arch", arch)
	c.attach("native_host", native)
	c.attach("unproven_on_this_host", []string{
		"a real launchd/systemd actually accepting and supervising the rendered launcher (packaging/QUALIFICATION.md §3; this harness uses a recording stand-in and, where native, runs the binary directly instead)",
		"the real OS keychain custodying a secret (this profile declares headless_master_key, never os_keychain, so no real keychain is touched)",
		"a real Flutter desktop bundle install/launch (apps/desktop has no release bundle on this tree; see JOURNEY.desktop_daily_work and the Z21 cases)",
		"code signing and notarization (packaging/QUALIFICATION.md §9; this repository holds no real release identity)",
	})

	work, err := os.MkdirTemp("", "ztqpack")
	if err != nil {
		c.fail("temp root: %v", err)
	}
	// macOS may return /var/... while /var itself is a symlink to
	// /private/var. Exercise the installer with a real, canonical home path.
	work, err = filepath.EvalSymlinks(work)
	if err != nil {
		c.fail("canonical temp root: %v", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	packBin, err := buildZatitiPack(work, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		c.fail("building zatiti-pack: %v", err)
	}
	c.version("zatiti_pack_binary_sha256", fileDigest(packBin))

	controllerBin, err := buildZatitiFor(work, goos, arch)
	if err != nil {
		c.fail("building the produced controller binary for %s/%s: %v", goos, arch, err)
	}
	c.version("zatiti_binary_sha256", fileDigest(controllerBin))
	c.observe("produced artifact: real cmd/zatiti built for %s/%s (sha256 %s)", goos, arch, fileDigest(controllerBin))

	releaseRoot := filepath.Join(work, "release")
	desc := packagingDescriptor(t, releaseRoot, controllerBin, "1.0.0", goos, arch)
	descPath := filepath.Join(work, "descriptor.json")
	descRaw, _ := json.MarshalIndent(desc, "", "  ")
	if err := os.WriteFile(descPath, descRaw, 0o644); err != nil {
		c.fail("writing descriptor: %v", err)
	}
	if _, _, stderr, err := packRun(packBin, "assemble", "--root", releaseRoot, "--descriptor", descPath, "--write"); err != nil {
		c.fail("zatiti-pack assemble: %v\n%s", err, stderr)
	}
	c.observe("assemble: manifest.json written from the real produced binary and the repository's own LICENSE")

	keyDir := filepath.Join(work, "keys")
	if err := os.MkdirAll(keyDir, 0o755); err != nil {
		c.fail("%v", err)
	}
	privKey, pubKey := filepath.Join(keyDir, "priv.pem"), filepath.Join(keyDir, "pub.pem")
	if _, _, stderr, err := packRun(packBin, "keygen", "--private-out", privKey, "--public-out", pubKey); err != nil {
		c.fail("zatiti-pack keygen: %v\n%s", err, stderr)
	}
	manifestPath := filepath.Join(releaseRoot, "manifest.json")
	if _, _, stderr, err := packRun(packBin, "sign", "--manifest", manifestPath, "--key", privKey); err != nil {
		c.fail("zatiti-pack sign: %v\n%s", err, stderr)
	}
	sigPath := filepath.Join(releaseRoot, "manifest.sig.json")
	if _, _, stderr, err := packRun(packBin, "verify", "--manifest", manifestPath, "--sig", sigPath, "--trusted", pubKey, "--tree", releaseRoot); err != nil {
		c.fail("zatiti-pack verify: %v\n%s", err, stderr)
	}
	c.observe("keygen/sign/verify: a qualification-only Ed25519 key signs the manifest; verify confirms the signature and the tree, exactly as install will re-check before touching anything")

	// A "clean host": a fresh temp directory tree standing in for $HOME,
	// never this development machine's real home, launchd/systemd state,
	// or keychain.
	home := filepath.Join(work, "home", "operator")
	if err := os.MkdirAll(home, 0o755); err != nil {
		c.fail("%v", err)
	}
	stateDir := filepath.Join(work, "zatiti-state")
	layout := computeDistributionLayout(goos, home)

	logPath := filepath.Join(work, "calls.log")
	fakeToolStateDir := filepath.Join(work, "fake-service-state")
	fakeTool := packagingWriteFakeServiceTool(t, logPath, fakeToolStateDir)
	managerKind, toolFlag := "launchd", "--launchctl"
	if goos == "linux" {
		managerKind, toolFlag = "systemd", "--systemctl"
	}

	socket := filepath.Join(work, "s.sock") // short: stays under the 104-byte AF_UNIX bound
	servicesPath := filepath.Join(work, "services.json")

	// The master key is provisioned through the packaging driver itself
	// (master-key provision), the same secure-helper mechanics the release
	// documents, so the real controller can start headless without ever
	// touching a keychain.
	keyPath := filepath.Join(home, "zatiti-secrets", "master.key")
	provOut, _, stderr, err := packRun(packBin, "master-key", "provision", "--distribution", "controller",
		"--os", goos, "--home", home, "--state-dir", stateDir, "--key-path", keyPath)
	if err != nil {
		c.fail("zatiti-pack master-key provision: %v\n%s", err, stderr)
	}
	if _, _, stderr, err := packRun(packBin, "master-key", "check", "--key-path", keyPath); err != nil {
		c.fail("zatiti-pack master-key check: %v\n%s", err, stderr)
	}
	masterKeyRef, _ := provOut["reference"].(string)
	services := []map[string]any{{
		"role": "controller", "label": controllerServiceLabel, "description": "Zatiti controller",
		"executable": layout.controllerExecutable,
		"arguments":  []string{"serve", "--state-dir", stateDir, "--socket", socket, "--credential-backend", "headless", "--master-key", masterKeyRef},
		"owns":       stateDir,
	}}
	servicesRaw, _ := json.Marshal(services)
	if err := os.WriteFile(servicesPath, servicesRaw, 0o644); err != nil {
		c.fail("%v", err)
	}
	c.observe("master-key provision/check: a headless master key is provisioned outside the state and distribution directories, exactly as backup/restoration requires")

	installArgs := []string{
		"install", "--distribution", "controller", "--os", goos,
		"--home", home, "--state-dir", stateDir, "--source", releaseRoot, "--host-arch", arch,
		"--services", servicesPath, "--trusted", pubKey, "--apply",
		"--service-manager", managerKind, toolFlag, fakeTool,
	}
	if goos == "darwin" {
		installArgs = append(installArgs, "--uid", "501")
	}
	installResult, _, stderr, err := packRun(packBin, installArgs...)
	if err != nil {
		c.fail("zatiti-pack install: %v\n%s", err, stderr)
	}
	if installResult["kind"] != "install" || installResult["applied"] != true {
		c.fail("install result: %+v", installResult)
	}
	installedBytes, err := os.ReadFile(layout.controllerExecutable)
	if err != nil {
		c.fail("reading the installed controller binary: %v", err)
	}
	wantBytes, _ := os.ReadFile(controllerBin)
	if !bytes.Equal(installedBytes, wantBytes) {
		c.fail("the installed controller binary does not match the produced artifact byte-for-byte")
	}
	packagingAssertServiceLoaded(t, fakeToolStateDir, controllerServiceLabel, true)
	c.observe("install: the real produced %s/%s zatiti binary is installed byte-identical at %s; the recording service manager reports %s loaded", goos, arch, layout.controllerExecutable, controllerServiceLabel)

	unitEntries, err := os.ReadDir(layout.unitDir)
	if err != nil || len(unitEntries) != 1 {
		c.fail("launcher directory %s: %d entries, err=%v, want exactly 1", layout.unitDir, len(unitEntries), err)
	}
	unitPath := filepath.Join(layout.unitDir, unitEntries[0].Name())
	unitRaw, err := os.ReadFile(unitPath)
	if err != nil || !bytes.Contains(unitRaw, []byte(layout.controllerExecutable)) {
		c.fail("the rendered launcher %s does not name the installed controller executable: %v", unitPath, err)
	}
	c.observe("the rendered launcher %s names the exact installed, verified controller executable", filepath.Base(unitPath))

	if !native {
		c.notRun("this host is %s/%s, not a %s host: the produced %s binary above is real (cross-compiled and packaged with a verified signature, installed byte-identical onto a clean-host layout, and its launcher correctly names it), but it cannot be executed here, so the controller-boots/CLI-query/backup-restore journey and the real launchd/systemd acceptance packaging/QUALIFICATION.md §3 requires stay unproven without an actual %s host", runtime.GOOS, runtime.GOARCH, goos, goos, goos)
		return
	}

	// The produced, installed binary is run directly -- never through a
	// real launchd/systemd -- with exactly the arguments the rendered
	// launcher names, proving packaging/QUALIFICATION.md's own "the
	// controller binary accepts serve and the arguments the launcher
	// passes" boundary item for real.
	serveCmd := exec.Command(layout.controllerExecutable, "serve", "--state-dir", stateDir, "--socket", socket,
		"--credential-backend", "headless", "--master-key", masterKeyRef)
	var serveLog lockedBuffer
	serveCmd.Stdout, serveCmd.Stderr = &serveLog, &serveLog
	if err := serveCmd.Start(); err != nil {
		c.fail("starting the installed controller: %v", err)
	}
	defer func() {
		if serveCmd.Process != nil {
			_ = serveCmd.Process.Kill()
			_, _ = serveCmd.Process.Wait()
		}
	}()
	if err := waitForSocket(socket, 3*time.Minute); err != nil {
		c.fail("the installed controller did not listen: %v\n%s", err, serveLog.String())
	}
	c.observe("the installed controller binary, run with exactly the launcher's own arguments, accepted `serve` and is listening on its private socket")

	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "ZATITI_STATE_DIR=" + stateDir, "ZATITI_SOCKET=" + socket}
	runCLI := func(args ...string) (map[string]any, string, error) {
		cmd := exec.Command(layout.controllerExecutable, args...)
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return nil, string(out), fmt.Errorf("%v: %s", err, ee.Stderr)
			}
			return nil, string(out), err
		}
		var env2 map[string]any
		_ = json.Unmarshal(out, &env2)
		return env2, string(out), nil
	}

	initOut, initRaw, err := runCLI("init", "--json", "--input", `{"credential_store":"headless","owner_name":"Qualification Distribution Owner","headless_key_ref":"installation/owner"}`)
	if err != nil || initOut["status"] != "completed" {
		c.fail("zatiti init against the installed controller: %v\n%s", err, initRaw)
	}
	installationID, _ := digString(initOut, "data", "resource", "installation_id")
	if installationID == "" {
		c.fail("init did not report an installation_id: %s", initRaw)
	}
	c.observe("`zatiti init` against the installed controller completed and created installation %s", installationID)

	// A CLI query against the private socket, expecting a completed result
	// (packaging/QUALIFICATION.md §1 step 4).
	statusOut, statusRaw, err := runCLI("installation", "status", "--json", "--input", fmt.Sprintf(`{"scope":{"installation_id":%q}}`, installationID))
	if err != nil || statusOut["status"] != "completed" {
		c.fail("installation status query: %v\n%s", err, statusRaw)
	}
	c.observe("a CLI query (`installation status`) against the private socket of the installed, produced binary returned a completed result")

	// Backup/restore journey, carried all the way through. Until P50 this
	// stopped at a documented ceiling -- installation.restore was accepted
	// and then failed closed with prerequisite_missing at StageCandidate,
	// because nothing could resolve the backup artifact or merge the
	// recovery overlay. P50 closed that: the real cmd/zatiti
	// restoreLifecycle (restore.go) now resolves the published encrypted
	// bundle, the controller performs the atomic database swap, and each
	// owner's own restore.merge folds the recovery overlay back.
	//
	// That makes this the one place in the tree where a genuine restore
	// handoff is exercised inside a real, separately spawned `zatiti serve`
	// OS process rather than in-process, and it is worth stating what that
	// costs: a restore ends the serving lifetime by design (P32/P33's
	// ErrRestoreHandoff -- this lifetime's Application was built over the
	// database handle CommitRestore closed), so runServe tears the listener
	// down, reassembles over the freshly reopened database and binds a new
	// one. The private socket therefore genuinely disappears for the length
	// of that reassembly (measured at roughly five seconds on this host,
	// almost all of it the same module/registry assembly that startup
	// already spends) and a client must poll through the gap rather than
	// treat the first connection error as the answer. The journey below
	// does exactly that, and that restart-and-recover is itself the
	// property being qualified.
	pauseOut, pauseRaw, err := runCLI("installation", "pause", "--json", "--submission-key", "qual-dist-pause-1",
		"--input", fmt.Sprintf(`{"scope":{"installation_id":%q},"expected_version":1}`, installationID))
	if err != nil || pauseOut["status"] != "completed" {
		c.fail("installation pause: %v\n%s", err, pauseRaw)
	}
	backupOut, backupRaw, err := runCLI("installation", "backup", "--json", "--submission-key", "qual-dist-backup-1",
		"--input", fmt.Sprintf(`{"scope":{"installation_id":%q}}`, installationID))
	if err != nil || (backupOut["status"] != "completed" && backupOut["status"] != "accepted") {
		c.fail("installation backup: %v\n%s", err, backupRaw)
	}
	backupState, _ := digString(backupOut, "data", "resource", "state")
	if backupState != "succeeded" {
		c.fail("installation backup did not succeed: %s", backupRaw)
	}
	artifactID, _ := digString(backupOut, "data", "resource", "result", "resource", "artifact", "id")
	artifactDigest, _ := digString(backupOut, "data", "resource", "result", "resource", "artifact", "digest")
	c.observe("installation backup succeeded against the installed binary: artifact %s", artifactID)

	maintOut, maintRaw, err := runCLI("installation", "maintenance", "enter", "--json", "--submission-key", "qual-dist-maint-1",
		"--input", fmt.Sprintf(`{"scope":{"installation_id":%q},"expected_version":2}`, installationID))
	if err != nil || maintOut["status"] != "completed" {
		c.fail("installation maintenance enter: %v\n%s", err, maintRaw)
	}
	restoreOut, restoreRaw, err := runCLI("installation", "restore", "--json", "--submission-key", "qual-dist-restore-1",
		"--input", fmt.Sprintf(`{"scope":{"installation_id":%q},"backup_artifact":{"id":%q,"digest":%q},"expected_version":3}`, installationID, artifactID, artifactDigest))
	if err != nil || (restoreOut["status"] != "completed" && restoreOut["status"] != "accepted") {
		c.fail("installation restore: %v\n%s", err, restoreRaw)
	}
	restoreJobID, _ := digString(restoreOut, "data", "resource", "id")

	// Poll the installed binary's own CLI across the restart window until
	// the process is serving again. A connection error here is the expected
	// shape of the handoff, not a verdict: the server is rebuilding itself.
	// The deadline is what distinguishes a restart from a hang.
	var statusAfter map[string]any
	deadline := time.Now().Add(3 * time.Minute)
	var lastErr error
	for time.Now().Before(deadline) {
		out, _, err := runCLI("installation", "status", "--json",
			"--input", fmt.Sprintf(`{"scope":{"installation_id":%q}}`, installationID))
		if err == nil && out["status"] == "completed" {
			statusAfter = out
			break
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	if statusAfter == nil {
		c.fail("the controller never served again after the restore handoff (last error: %v)\n%s", lastErr, serveLog.String())
	}

	// It is the SAME installation, restored rather than re-created, and the
	// generation advanced: CommitRestore reopened the swapped file under a
	// strictly newer generation, which is what fences every pre-restore
	// lease and claim out.
	restoredID, _ := digString(statusAfter, "data", "resource", "installation_id")
	if restoredID != installationID {
		c.fail("after the restore the controller serves installation %q, want the same installation %q", restoredID, installationID)
	}
	restoredGeneration, _ := digFloat(statusAfter, "data", "resource", "generation")
	if restoredGeneration <= 1 {
		c.fail("generation after the restore is %v, want it strictly advanced past the pre-restore generation", restoredGeneration)
	}

	// The restore job itself is gone, and that is the correct outcome, not a
	// missing record: installation.restore's job ledger lives in the very
	// database the restore replaces, and the backup necessarily predates the
	// restore that selected it. Everything created after the backup -- the
	// restore job included -- is rewound away with it.
	jobOut, jobRaw, err := runCLI("installation", "job", "get", "--json",
		"--input", fmt.Sprintf(`{"scope":{"installation_id":%q},"id":%q}`, installationID, restoreJobID))
	if err == nil {
		state, _ := digString(jobOut, "data", "resource", "state")
		c.fail("restore job %s still exists after its own restore (state=%q); the rewind did not take effect\n%s", restoreJobID, state, jobRaw)
	}

	// The installation stays paused: a restore restores the backed-up
	// lifecycle state and never silently resumes admissions on its own.
	doctorOut, doctorRaw, err := runCLI("installation", "doctor", "--json", "--input", fmt.Sprintf(`{"scope":{"installation_id":%q}}`, installationID))
	if err != nil || doctorOut["status"] != "completed" {
		c.fail("installation doctor: %v\n%s", err, doctorRaw)
	}
	paused, _ := digBool(doctorOut, "data", "resource", "paused")
	if !paused {
		c.fail("the installation did not stay paused after the restore")
	}
	c.observe("backup/restore journey completed end to end against the installed binary in a real spawned process: restore job %s drove an actual database swap, the controller ended its lifetime with the restore handoff, reassembled over the freshly reopened database and served again on its private socket as the same installation %s under generation %v, still paused -- never silently resumed", restoreJobID, installationID, restoredGeneration)

	// Stop the directly-run process before the recording restart/uninstall
	// steps, which never touch it (they exercise the driver against the
	// recording stand-in only, exactly as packaging's own tests do).
	if serveCmd.Process != nil {
		_ = serveCmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- serveCmd.Wait() }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = serveCmd.Process.Kill()
			<-done
		}
	}

	restartArgs := []string{"service", "--verb", "restart", "--label", controllerServiceLabel, "--unit", unitPath,
		"--os", goos, "--service-manager", managerKind, toolFlag, fakeTool}
	if goos == "darwin" {
		restartArgs = append(restartArgs, "--uid", "501")
	}
	restartResult, _, stderr, err := packRun(packBin, restartArgs...)
	if err != nil {
		c.fail("zatiti-pack service restart: %v\n%s", err, stderr)
	}
	steps, _ := restartResult["steps"].([]any)
	if len(steps) != 2 || steps[0] != "unload" || steps[1] != "load" {
		c.fail("restart steps: %v", steps)
	}
	packagingAssertServiceLoaded(t, fakeToolStateDir, controllerServiceLabel, true)
	c.observe("service restart (recording launchd/systemd stand-in): unload then load, in order, naming the same verified launcher")

	// Real durable state a real controller run left behind (the exact
	// installation directory the direct run above wrote to), preserved
	// across uninstall without --remove-state, exactly as packaging/
	// README.md documents.
	uninstallArgs := []string{"uninstall", "--distribution", "controller", "--os", goos,
		"--home", home, "--state-dir", stateDir, "--labels", controllerServiceLabel, "--apply",
		"--service-manager", managerKind, toolFlag, fakeTool}
	if goos == "darwin" {
		uninstallArgs = append(uninstallArgs, "--uid", "501")
	}
	if _, _, stderr, err := packRun(packBin, uninstallArgs...); err != nil {
		c.fail("zatiti-pack uninstall: %v\n%s", err, stderr)
	}
	packagingAssertServiceLoaded(t, fakeToolStateDir, controllerServiceLabel, false)
	if _, err := os.Stat(stateDir); err != nil {
		c.fail("uninstall removed the state directory though --remove-state was not requested: %v", err)
	}
	if _, err := os.Lstat(layout.distRoot); !os.IsNotExist(err) {
		c.fail("uninstall left the distribution directory: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		c.fail("uninstall touched the master key, which lives outside the layout: %v", err)
	}
	c.observe("uninstall: service unloaded, distribution directory removed, real durable state and the master key preserved untouched")
}

func TestQualificationMacOSDistribution(t *testing.T) {
	qualifyDistribution(t, "QUALIFICATION.macos_distribution", "darwin")
}

func TestQualificationLinuxDistribution(t *testing.T) {
	qualifyDistribution(t, "QUALIFICATION.linux_distribution", "linux")
}

// digString walks a decoded JSON document by successive map/slice keys and
// returns the string leaf, or "" if the path does not resolve. Slice
// indices are given as their decimal string form (e.g. "0").
func digString(v any, path ...string) (string, bool) {
	cur := v
	for _, k := range path {
		switch node := cur.(type) {
		case map[string]any:
			cur = node[k]
		case []any:
			var idx int
			if _, err := fmt.Sscanf(k, "%d", &idx); err != nil || idx < 0 || idx >= len(node) {
				return "", false
			}
			cur = node[idx]
		default:
			return "", false
		}
	}
	s, ok := cur.(string)
	return s, ok
}

func digBool(v any, path ...string) (bool, bool) {
	cur := any(v)
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return false, false
		}
		cur = m[k]
	}
	b, ok := cur.(bool)
	return b, ok
}

// digFloat resolves a numeric leaf. JSON numbers decode as float64 through
// the generic map used here, so a version or generation arrives as one.
func digFloat(v any, path ...string) (float64, bool) {
	cur := any(v)
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return 0, false
		}
		cur = m[k]
	}
	f, ok := cur.(float64)
	return f, ok
}
