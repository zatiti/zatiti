package packaging

// Behavioral tests for the packaging/install driver (cli.go and its
// cli_*.go siblings). They call RunCLI directly - the exact function
// packaging/cmd/zatiti-pack's five-line main package calls - so these tests
// exercise the real, built driver's behavior, not a stand-in for it. Where a
// service manager is involved, the driver runs a real subprocess (through
// ExecRunner, exactly as production does); the tests point it at a small
// recording shell script instead of the host's real launchctl or systemctl,
// the same substitution TestExecRunnerDoesNotInheritTheEnvironment and the
// rest of this package's tests make at the Go level - here it is made at the
// subprocess boundary instead, because the driver is a separate process's
// entry point once built.
//
// What these tests do not prove: a real launchd or systemd, a real built
// zatiti binary actually accepting "serve" and doing work, and a real
// keychain. See QUALIFICATION.md; that boundary is unchanged by this file.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// runCLI invokes RunCLI in-process and captures both streams.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	code = RunCLI(args, &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), code
}

// writeDescriptor writes in (minus Root) as an "assemble" descriptor file.
func writeDescriptor(t *testing.T, path string, in BuildInput) {
	t.Helper()
	d := assembleDescriptor{
		Distribution: in.Distribution, Version: in.Version, Target: in.Target,
		SourceRevision: in.SourceRevision, Toolchain: in.Toolchain, Kinds: in.Kinds,
		Licenses: in.Licenses, SBOM: in.SBOM, Profiles: in.Profiles, Serenity: in.Serenity,
		SecureHelper: in.SecureHelper, Desktop: in.Desktop, Attestations: in.Attestations,
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, raw, 0o644)
}

func writeServiceSpecs(t *testing.T, path string, specs []serviceSpecDoc) {
	t.Helper()
	raw, err := json.Marshal(specs)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, raw, 0o644)
}

// writeFakeServiceTool writes a recording stand-in for launchctl/systemctl:
// pure shell builtins (no external command, so it needs no PATH), which logs
// every invocation to logPath and tracks each label's loaded/unloaded state
// as a file under stateDir, driven by the exact argument shapes
// servicemanager.go sends (verified against TestLaunchdManagerCommands and
// TestSystemdManagerCommands).
func writeFakeServiceTool(t *testing.T, logPath, stateDir string) string {
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
	writeFile(t, path, []byte(script), 0o755)
	return path
}

func assertServiceLoaded(t *testing.T, stateDir, label string, want bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(stateDir, label))
	got := err == nil && string(raw) == "loaded"
	if got != want {
		t.Fatalf("service %s loaded=%v (file=%q, err=%v), want %v", label, got, raw, err, want)
	}
}

func requirePrivateMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s permissions are %o, want %o", path, info.Mode().Perm(), want)
	}
}

// driverRelease is one synthetic controller release, assembled, signed and
// verified entirely through the driver.
type driverRelease struct {
	fixture   fixture
	manifest  string
	signature string
	pub       string
}

func buildSignedRelease(t *testing.T, version, goos string) driverRelease {
	t.Helper()
	f := newFixture(t, version, goos)
	descPath := filepath.Join(t.TempDir(), "descriptor.json")
	writeDescriptor(t, descPath, f.input)
	if _, stderr, code := runCLI(t, "assemble", "--root", f.root, "--descriptor", descPath, "--write"); code != 0 {
		t.Fatalf("assemble: exit %d: %s", code, stderr)
	}
	keyDir := t.TempDir()
	priv, pub := filepath.Join(keyDir, "priv.pem"), filepath.Join(keyDir, "pub.pem")
	if _, stderr, code := runCLI(t, "keygen", "--private-out", priv, "--public-out", pub); code != 0 {
		t.Fatalf("keygen: exit %d: %s", code, stderr)
	}
	manifestPath := filepath.Join(f.root, ManifestFileName)
	if _, stderr, code := runCLI(t, "sign", "--manifest", manifestPath, "--key", priv); code != 0 {
		t.Fatalf("sign: exit %d: %s", code, stderr)
	}
	sigPath := filepath.Join(f.root, SignatureFileName)
	if out, stderr, code := runCLI(t, "verify", "--manifest", manifestPath, "--sig", sigPath, "--trusted", pub, "--tree", f.root); code != 0 {
		t.Fatalf("verify: exit %d: %s\n%s", code, stderr, out)
	}
	return driverRelease{fixture: f, manifest: manifestPath, signature: sigPath, pub: pub}
}

// TestDriverControllerLifecycleInstallBootstrapRestartUpgradeBackupKeyUninstall
// is the card's first required behavioral test: install a produced artifact
// on a clean layout, bootstrap it (a real, recording service load), confirm
// what "first work" packaging can honestly prove (the launcher names the
// exact verified, installed executable - not that a real controller process
// ran a task, which this package does not own), restart, upgrade, prove the
// backup/restoration master key prerequisite survives uninstall, and
// uninstall according to packaging/README.md.
func TestDriverControllerLifecycleInstallBootstrapRestartUpgradeBackupKeyUninstall(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "home", "operator")
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			stateDir := filepath.Join(home, "zatiti-state")
			l, err := NewLayout(LayoutInput{OS: goos, Home: home, StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}

			logPath := filepath.Join(t.TempDir(), "calls.log")
			fakeToolStateDir := t.TempDir()
			fakeTool := writeFakeServiceTool(t, logPath, fakeToolStateDir)

			managerKind, toolFlag := "launchd", "--launchctl"
			if goos == "linux" {
				managerKind, toolFlag = "systemd", "--systemctl"
			}

			// 1. Assemble, sign and verify the 1.0.0 release.
			rel := buildSignedRelease(t, "1.0.0", goos)

			controllerExec := filepath.Join(l.Current, "bin", "zatiti")
			socket := "/tmp/ztp-" + goos + "-" + strconv.Itoa(os.Getpid()) + ".sock"
			servicesPath := filepath.Join(t.TempDir(), "services.json")
			writeServiceSpecs(t, servicesPath, []serviceSpecDoc{{
				Role: RoleController, Label: ControllerLabel, Description: "Zatiti controller",
				Executable: controllerExec, Arguments: []string{"serve", "--state-dir", stateDir, "--socket", socket},
				Owns: stateDir,
			}})

			installArgs := func(rel driverRelease) []string {
				return []string{
					"install", "--distribution", DistributionController, "--os", goos,
					"--home", home, "--state-dir", stateDir, "--source", rel.fixture.root, "--host-arch", "arm64",
					"--services", servicesPath, "--trusted", rel.pub, "--apply",
					"--service-manager", managerKind, toolFlag, fakeTool, "--uid", "501",
				}
			}

			// 2. Install: bootstrap. A real (recording) service load, not
			// just files on disk.
			out, stderr, code := runCLI(t, installArgs(rel)...)
			if code != 0 {
				t.Fatalf("install: exit %d: %s", code, stderr)
			}
			var installResult map[string]any
			if err := json.Unmarshal([]byte(out), &installResult); err != nil || installResult["kind"] != "install" {
				t.Fatalf("install result: %s (%v)", out, err)
			}
			if raw, err := os.ReadFile(controllerExec); err != nil || string(raw) != "synthetic controller 1.0.0" {
				t.Fatalf("installed controller binary: %q, %v", raw, err)
			}
			if err := AuditInstalled(l, []string{ControllerLabel}); err != nil {
				t.Fatalf("AuditInstalled after install: %v", err)
			}
			assertServiceLoaded(t, fakeToolStateDir, ControllerLabel, true)

			// 3. "First work": packaging owns no controller runtime, so
			// what this driver can honestly prove is that the rendered
			// launcher names the exact verified, installed executable the
			// service manager just (recorded as) loaded - not that a
			// process ran a task.
			unitPath := filepath.Join(l.UnitDir, unitFileName(l.Manager, ControllerLabel))
			unitRaw, err := os.ReadFile(unitPath)
			if err != nil {
				t.Fatalf("reading the launcher: %v", err)
			}
			if !bytes.Contains(unitRaw, []byte(controllerExec)) {
				t.Fatalf("the launcher does not name the installed controller: %s", unitRaw)
			}

			// 4. Restart: unload then load again, through the same driver
			// command an operator would run by hand.
			restartOut, stderr, code := runCLI(t, "service", "--verb", "restart", "--label", ControllerLabel,
				"--unit", unitPath, "--os", goos, "--service-manager", managerKind, toolFlag, fakeTool, "--uid", "501")
			if code != 0 {
				t.Fatalf("service restart: exit %d: %s", code, stderr)
			}
			var restartResult struct {
				Steps []string `json:"steps"`
			}
			if err := json.Unmarshal([]byte(restartOut), &restartResult); err != nil {
				t.Fatal(err)
			}
			if len(restartResult.Steps) != 2 || restartResult.Steps[0] != VerbUnload || restartResult.Steps[1] != VerbLoad {
				t.Fatalf("restart steps: %v", restartResult.Steps)
			}
			assertServiceLoaded(t, fakeToolStateDir, ControllerLabel, true)

			// 5. Upgrade: install a later release into the same layout.
			rel2 := buildSignedRelease(t, "1.1.0", goos)
			out, stderr, code = runCLI(t, installArgs(rel2)...)
			if code != 0 {
				t.Fatalf("upgrade: exit %d: %s", code, stderr)
			}
			if err := json.Unmarshal([]byte(out), &installResult); err != nil || installResult["kind"] != "upgrade" || installResult["previous_version"] != "1.0.0" {
				t.Fatalf("upgrade result: %s (%v)", out, err)
			}
			if raw, err := os.ReadFile(controllerExec); err != nil || string(raw) != "synthetic controller 1.1.0" {
				t.Fatalf("upgraded controller binary: %q, %v", raw, err)
			}
			if err := AuditInstalled(l, []string{ControllerLabel}); err != nil {
				t.Fatalf("AuditInstalled after upgrade: %v", err)
			}
			assertServiceLoaded(t, fakeToolStateDir, ControllerLabel, true)
			inspOut, _, code := runCLI(t, "inspect", "--distribution", DistributionController, "--os", goos, "--home", home, "--state-dir", stateDir)
			if code != 0 {
				t.Fatalf("inspect: exit %d", code)
			}
			var insp struct {
				Current  string   `json:"current"`
				Versions []string `json:"versions"`
			}
			if err := json.Unmarshal([]byte(inspOut), &insp); err != nil || insp.Current != "1.1.0" || len(insp.Versions) != 2 {
				t.Fatalf("inspect: %s (%v)", inspOut, err)
			}

			// 6. Backup/restoration key prerequisite: the master key sits
			// outside the state and distribution directories by
			// construction (README.md, "Backup and restoration key
			// prerequisites"), so it must survive an uninstall untouched -
			// proven below, not merely documented.
			keyPath := filepath.Join(home, "zatiti-secrets", "master.key")
			provOut, stderr, code := runCLI(t, "master-key", "provision", "--distribution", DistributionController,
				"--os", goos, "--home", home, "--state-dir", stateDir, "--key-path", keyPath)
			if code != 0 {
				t.Fatalf("master-key provision: exit %d: %s", code, stderr)
			}
			var prov struct {
				Reference string `json:"reference"`
			}
			if err := json.Unmarshal([]byte(provOut), &prov); err != nil || prov.Reference != "file:"+keyPath {
				t.Fatalf("master-key provision result: %s", provOut)
			}
			if _, stderr, code := runCLI(t, "master-key", "check", "--key-path", keyPath); code != 0 {
				t.Fatalf("master-key check: exit %d: %s", code, stderr)
			}

			// Durable state a real controller would have written. Packaging
			// never writes inside the state directory (README.md), so a
			// real controller's first run is what gives it 0700; simulate
			// that here before writing the file the same run would leave
			// behind.
			if err := os.MkdirAll(stateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			stateFile := filepath.Join(stateDir, "zatiti.db")
			writeFile(t, stateFile, []byte("durable state"), 0o600)

			// 7. Uninstall without --remove-state preserves state and the
			// master key, and permissions on both stay private.
			_, stderr, code = runCLI(t, "uninstall", "--distribution", DistributionController, "--os", goos,
				"--home", home, "--state-dir", stateDir, "--labels", ControllerLabel, "--apply",
				"--service-manager", managerKind, toolFlag, fakeTool, "--uid", "501")
			if code != 0 {
				t.Fatalf("uninstall: exit %d: %s", code, stderr)
			}
			assertServiceLoaded(t, fakeToolStateDir, ControllerLabel, false)
			if raw, err := os.ReadFile(stateFile); err != nil || string(raw) != "durable state" {
				t.Fatalf("state was not preserved: %q, %v", raw, err)
			}
			requirePrivateMode(t, stateDir, 0o700)
			requirePrivateMode(t, stateFile, 0o600)
			requirePrivateMode(t, keyPath, 0o600)
			if _, err := os.Lstat(l.DistRoot); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("uninstall left the distribution directory: %v", err)
			}

			// 8. A confirmed state removal actually removes it, and never
			// touches the master key, which lives outside the layout.
			if _, stderr, code := runCLI(t, installArgs(rel2)...); code != 0 {
				t.Fatalf("reinstall before state removal: exit %d: %s", code, stderr)
			}
			_, stderr, code = runCLI(t, "uninstall", "--distribution", DistributionController, "--os", goos,
				"--home", home, "--state-dir", stateDir, "--labels", ControllerLabel,
				"--remove-state", "--confirm-state-dir", stateDir, "--apply",
				"--service-manager", managerKind, toolFlag, fakeTool, "--uid", "501")
			if code != 0 {
				t.Fatalf("uninstall with state removal: exit %d: %s", code, stderr)
			}
			if _, err := os.Lstat(stateDir); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("confirmed state removal left the state directory: %v", err)
			}
			if _, err := os.Lstat(keyPath); err != nil {
				t.Fatalf("state removal touched the master key: %v", err)
			}
		})
	}
}

// TestDriverDesktopInstallAuditUninstall is a lighter companion covering the
// desktop distribution through the same driver commands: no service is
// involved, so this is the desktop half of "install a produced artifact,
// upgrade and uninstall according to docs".
func TestDriverDesktopInstallAuditUninstall(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			f := newBundledDesktopFixture(t, "2.0.0", goos)
			descPath := filepath.Join(t.TempDir(), "descriptor.json")
			writeDescriptor(t, descPath, f.input)
			if _, stderr, code := runCLI(t, "assemble", "--root", f.root, "--descriptor", descPath, "--write"); code != 0 {
				t.Fatalf("assemble: exit %d: %s", code, stderr)
			}
			keyDir := t.TempDir()
			priv, pub := filepath.Join(keyDir, "priv.pem"), filepath.Join(keyDir, "pub.pem")
			if _, stderr, code := runCLI(t, "keygen", "--private-out", priv, "--public-out", pub); code != 0 {
				t.Fatalf("keygen: exit %d: %s", code, stderr)
			}
			manifestPath := filepath.Join(f.root, ManifestFileName)
			if _, stderr, code := runCLI(t, "sign", "--manifest", manifestPath, "--key", priv); code != 0 {
				t.Fatalf("sign: exit %d: %s", code, stderr)
			}

			home := filepath.Join(t.TempDir(), "home", "operator")
			if err := os.MkdirAll(home, 0o755); err != nil {
				t.Fatal(err)
			}
			stateDir := filepath.Join(home, "zatiti-state")
			installArgs := []string{
				"install", "--distribution", DistributionDesktop, "--os", goos,
				"--home", home, "--state-dir", stateDir, "--source", f.root, "--host-arch", "arm64",
				"--trusted", pub, "--apply",
			}
			if _, stderr, code := runCLI(t, installArgs...); code != 0 {
				t.Fatalf("desktop install: exit %d: %s", code, stderr)
			}
			if _, stderr, code := runCLI(t, "audit", "--distribution", DistributionDesktop, "--os", goos, "--home", home, "--state-dir", stateDir); code != 0 {
				t.Fatalf("desktop audit: exit %d: %s", code, stderr)
			}
			if _, stderr, code := runCLI(t, "uninstall", "--distribution", DistributionDesktop, "--os", goos,
				"--home", home, "--state-dir", stateDir, "--apply"); code != 0 {
				t.Fatalf("desktop uninstall: exit %d: %s", code, stderr)
			}
			l, err := NewDesktopLayout(DesktopLayoutInput{OS: goos, Home: home, StateDir: stateDir})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(l.DistRoot); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("desktop uninstall left the distribution directory: %v", err)
			}
		})
	}
}
