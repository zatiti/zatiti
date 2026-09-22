package packaging

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestDriverRefusesTamperedManifestBinaryHelperProfileBeforeServiceStart is
// the card's second required behavioral test. Four distinct tampers, each
// caught by a different part of the pipeline before "install" ever touches a
// service:
//
//   - a byte flipped anywhere in manifest.json (including deep inside the
//     secure_helper section) invalidates the signature, which install
//     checks first;
//   - a byte flipped in the controller binary, with the manifest and its
//     signature untouched, is caught by VerifyTree inside PlanInstallation -
//     a valid signature over a manifest whose tree no longer matches it;
//   - a byte flipped in a claimed profile's evidence file, likewise with
//     the manifest untouched, is caught the same way, proving a profile
//     claim is backed by tree-verified evidence, not just a declaration.
//
// In every case the fake service manager's log must stay empty: refusal
// happens before Apply, so no load is ever attempted.
func TestDriverRefusesTamperedManifestBinaryHelperProfileBeforeServiceStart(t *testing.T) {
	goos := "linux"
	f := newFixture(t, "1.0.0", goos)
	// Add a profile with its own evidence file distinct from the Serenity
	// evidence, so the "profile" case tampers something the "manifest"
	// case does not.
	writeFile(t, filepath.Join(f.root, "evidence", "profile.json"), []byte(`{"synthetic":true}`), 0o644)
	f.input.Kinds["evidence/profile.json"] = KindAttestationEvidence
	f.input.Profiles = []Profile{{Adapter: "responses", Name: "openai", Version: "v1", Evidence: "evidence/profile.json"}}

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
	signedManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	controllerBinPath := filepath.Join(f.root, "bin", "zatiti")
	signedBinary, err := os.ReadFile(controllerBinPath)
	if err != nil {
		t.Fatal(err)
	}
	evidencePath := filepath.Join(f.root, "evidence", "profile.json")
	signedEvidence, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatal(err)
	}

	home := filepath.Join(t.TempDir(), "home", "operator")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(home, "zatiti-state")
	l, err := NewLayout(LayoutInput{OS: goos, Home: home, StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	servicesPath := filepath.Join(t.TempDir(), "services.json")
	writeServiceSpecs(t, servicesPath, []serviceSpecDoc{{
		Role: RoleController, Label: ControllerLabel, Description: "Zatiti controller",
		Executable: filepath.Join(l.Current, "bin", "zatiti"),
		Arguments:  []string{"serve", "--state-dir", stateDir, "--socket", "/tmp/ztp-tamper.sock"},
		Owns:       stateDir,
	}})

	attempt := func(t *testing.T, restore func()) (stderr string, code int) {
		t.Helper()
		t.Cleanup(restore)
		logPath := filepath.Join(t.TempDir(), "calls.log")
		fakeToolStateDir := t.TempDir()
		fakeTool := writeFakeServiceTool(t, logPath, fakeToolStateDir)
		_, stderr, code = runCLI(t, "install", "--distribution", DistributionController, "--os", goos,
			"--home", home, "--state-dir", stateDir, "--source", f.root, "--host-arch", "arm64",
			"--services", servicesPath, "--trusted", pub, "--apply",
			"--service-manager", "systemd", "--systemctl", fakeTool)
		if raw, err := os.ReadFile(logPath); err == nil && len(raw) != 0 {
			t.Fatalf("a tampered install reached the service manager: %q", raw)
		}
		return stderr, code
	}

	t.Run("manifest", func(t *testing.T) {
		tampered := bytes.Replace(signedManifest, []byte(`"version":"1.0.0"`), []byte(`"version":"1.0.1"`), 1)
		if bytes.Equal(tampered, signedManifest) {
			t.Fatal("the tamper did not change the manifest")
		}
		writeFile(t, manifestPath, tampered, 0o644)
		stderr, code := attempt(t, func() { writeFile(t, manifestPath, signedManifest, 0o644) })
		wantCLIVerificationFailed(t, stderr, code)
	})

	t.Run("helper", func(t *testing.T) {
		tampered := bytes.Replace(signedManifest, []byte(`"kind":"headless_master_key"`), []byte(`"kind":"os_keychain"`), 1)
		if bytes.Equal(tampered, signedManifest) {
			t.Fatal("the tamper did not change the manifest")
		}
		writeFile(t, manifestPath, tampered, 0o644)
		stderr, code := attempt(t, func() { writeFile(t, manifestPath, signedManifest, 0o644) })
		wantCLIVerificationFailed(t, stderr, code)
	})

	t.Run("binary", func(t *testing.T) {
		writeFile(t, controllerBinPath, []byte("not the signed controller"), 0o755)
		stderr, code := attempt(t, func() { writeFile(t, controllerBinPath, signedBinary, 0o755) })
		wantCLIVerificationFailed(t, stderr, code)
	})

	t.Run("profile", func(t *testing.T) {
		writeFile(t, evidencePath, []byte(`{"synthetic":false}`), 0o644)
		stderr, code := attempt(t, func() { writeFile(t, evidencePath, signedEvidence, 0o644) })
		wantCLIVerificationFailed(t, stderr, code)
	})
}

func wantCLIVerificationFailed(t *testing.T, stderr string, code int) {
	t.Helper()
	if code == 0 {
		t.Fatalf("a tampered install succeeded: %s", stderr)
	}
	var doc struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stderr), &doc); err != nil {
		t.Fatalf("error output is not the driver's JSON error document: %s: %v", stderr, err)
	}
	if doc.Error.Code != CodeVerificationFailed {
		t.Fatalf("fault code = %q, want %q: %s", doc.Error.Code, CodeVerificationFailed, stderr)
	}
}

// TestDriverUninstallRefusesStateRemovalWithoutExactConfirmation is the
// state-removal half of the card's third required behavioral test: state is
// removed only when the operator both requests it and repeats the exact
// state directory, and a refused request never touches state or its
// permissions.
func TestDriverUninstallRefusesStateRemovalWithoutExactConfirmation(t *testing.T) {
	goos := "linux"
	rel := buildSignedRelease(t, "1.0.0", goos)
	home := filepath.Join(t.TempDir(), "home", "operator")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(home, "zatiti-state")
	l, err := NewLayout(LayoutInput{OS: goos, Home: home, StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	servicesPath := filepath.Join(t.TempDir(), "services.json")
	writeServiceSpecs(t, servicesPath, []serviceSpecDoc{{
		Role: RoleController, Label: ControllerLabel, Description: "Zatiti controller",
		Executable: filepath.Join(l.Current, "bin", "zatiti"),
		Arguments:  []string{"serve", "--state-dir", stateDir, "--socket", "/tmp/ztp-confirm.sock"},
		Owns:       stateDir,
	}})
	logPath := filepath.Join(t.TempDir(), "calls.log")
	fakeToolStateDir := t.TempDir()
	fakeTool := writeFakeServiceTool(t, logPath, fakeToolStateDir)
	if _, stderr, code := runCLI(t, "install", "--distribution", DistributionController, "--os", goos,
		"--home", home, "--state-dir", stateDir, "--source", rel.fixture.root, "--host-arch", "arm64",
		"--services", servicesPath, "--trusted", rel.pub, "--apply",
		"--service-manager", "systemd", "--systemctl", fakeTool); code != 0 {
		t.Fatalf("install: exit %d: %s", code, stderr)
	}
	// Packaging never writes inside the state directory; a real controller's
	// first run is what gives it 0700. Simulate that before writing the
	// file the same run would leave behind.
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(stateDir, "zatiti.db")
	writeFile(t, stateFile, []byte("durable state"), 0o600)

	for name, args := range map[string][]string{
		"request without confirmation": {"--remove-state"},
		"wrong directory":              {"--remove-state", "--confirm-state-dir", home},
		"confirmation without request": {"--confirm-state-dir", stateDir},
	} {
		t.Run(name, func(t *testing.T) {
			full := append([]string{"uninstall", "--distribution", DistributionController, "--os", goos,
				"--home", home, "--state-dir", stateDir, "--labels", ControllerLabel, "--apply",
				"--service-manager", "systemd", "--systemctl", fakeTool}, args...)
			_, stderr, code := runCLI(t, full...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2 (invalid_input): %s", code, stderr)
			}
			if raw, err := os.ReadFile(stateFile); err != nil || string(raw) != "durable state" {
				t.Fatalf("a refused uninstall touched state: %q, %v", raw, err)
			}
			requirePrivateMode(t, stateDir, 0o700)
			requirePrivateMode(t, stateFile, 0o600)
		})
	}
}
