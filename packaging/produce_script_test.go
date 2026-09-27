package packaging

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// descriptorFor renders a BuildInput as the assemble descriptor the producer
// script reads.
func descriptorFor(t *testing.T, in BuildInput) []byte {
	t.Helper()
	raw, err := json.Marshal(assembleDescriptor{
		Distribution: in.Distribution, Version: in.Version, Target: in.Target, SourceRevision: in.SourceRevision,
		Toolchain: in.Toolchain, Kinds: in.Kinds, Licenses: in.Licenses, SBOM: in.SBOM, Profiles: in.Profiles,
		Serenity: in.Serenity, SecureHelper: in.SecureHelper, Desktop: in.Desktop, Attestations: in.Attestations,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// stageProducerInput writes one architecture's controller tree, desktop tree,
// application and descriptors in the layout produce-mac-release.sh reads.
func stageProducerInput(t *testing.T, dir, version, arch string) {
	t.Helper()
	src := filepath.Join(dir, arch)
	controller := filepath.Join(src, "controller")
	writeFile(t, filepath.Join(controller, "bin", "zatiti"), syntheticMachO(arch), 0o755)
	writeFile(t, filepath.Join(controller, "bin", "zatiti-credential-helper"), syntheticMachO(arch), 0o755)
	writeFile(t, filepath.Join(controller, "LICENSE"), repositoryLicense(t), 0o644)
	writeFile(t, filepath.Join(controller, "sbom.spdx.json"), []byte(`{"spdxVersion":"SPDX-2.3","packages":[{"name":"`+ProjectComponent+`","versionInfo":"`+version+`"}]}`), 0o644)
	writeFile(t, filepath.Join(controller, "evidence", "helper-signature.json"), []byte(`{"synthetic":true}`), 0o644)
	cin := BuildInput{
		Distribution: DistributionController, Version: version, Target: Target{OS: "darwin", Arch: arch},
		SourceRevision: testRevision, Toolchain: "go1.26.2",
		Kinds: map[string]string{
			"bin/zatiti": KindControllerBinary, "bin/zatiti-credential-helper": KindCredentialHelper,
			"LICENSE": KindLicense, "sbom.spdx.json": KindSBOM, "evidence/helper-signature.json": KindAttestationEvidence,
		},
		Licenses:     []LicenseNotice{{Component: ProjectComponent, Version: version, SPDX: ProjectLicense, Notice: LicensePath}},
		SBOM:         SBOMRef{Format: SBOMFormatSPDX, Path: "sbom.spdx.json"},
		SecureHelper: SecureHelper{Kind: HelperOSKeychain, Path: "/usr/bin/security"},
		Attestations: []Attestation{{Kind: AttestationCodeSignature, Subject: "bin/zatiti-credential-helper", Evidence: "evidence/helper-signature.json"}},
	}
	writeFile(t, filepath.Join(src, "controller.descriptor.json"), descriptorFor(t, cin), 0o644)

	f := newDesktopFixture(t, version, "darwin")
	if err := os.Remove(filepath.Join(f.root, filepath.FromSlash(f.input.Desktop.Bundle))); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.root, filepath.Join(src, "desktop")); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(src, "zatiti_desktop.app", "Contents")
	writeFile(t, filepath.Join(app, "MacOS", "zatiti_desktop"), syntheticMachO(arch), 0o755)
	writeFile(t, filepath.Join(app, "Info.plist"), []byte("<plist/>"), 0o644)
	f.input.Target.Arch = arch
	f.input.Desktop.Executable = "zatiti_desktop.app/Contents/MacOS/zatiti_desktop"
	writeFile(t, filepath.Join(src, "desktop.descriptor.json"), descriptorFor(t, f.input), 0o644)
}

func runProducer(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("scripts", "produce-mac-release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	return out.String(), err
}

// TestProducerScriptDryRunProducesVerifiableReleaseSet runs the whole
// producer chain on synthetic inputs with every Apple step skipped, and
// checks that its output verifies as a release set, and that a signed run
// without Apple configuration fails closed before producing anything.
func TestProducerScriptDryRunProducesVerifiableReleaseSet(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the Mac release producer requires macOS")
	}
	if testing.Short() {
		t.Skip("builds zatiti-pack and runs pkgbuild")
	}
	input := t.TempDir()
	for _, arch := range []string{"amd64", "arm64"} {
		stageProducerInput(t, input, "1.0.0", arch)
	}
	keys := t.TempDir()
	priv, privPath, pubPath := writeKeyPair(t, keys, "release")
	common := []string{"--input", input, "--sequence", "5", "--base-url", "https://downloads.zatiti.example/releases"}
	operatorKeys := []string{"--release-key", privPath, "--trusted", pubPath}
	unconfigured := []string{EnvAppleAppIdentity + "=", EnvAppleInstallerIdentity + "=", EnvAppleNotaryProfile + "=", EnvAppleKeychain + "="}

	signedOut := filepath.Join(t.TempDir(), "signed")
	log, err := runProducer(t, unconfigured, append(append(common, operatorKeys...), "--out", signedOut)...)
	if err == nil || !strings.Contains(log, "not configured") {
		t.Fatalf("a signed run without Apple configuration did not fail closed: %v\n%s", err, log)
	}
	if entries, _ := os.ReadDir(filepath.Join(signedOut, "assets")); len(entries) != 0 {
		t.Fatalf("an unconfigured signed run produced assets: %v", entries)
	}

	refused := filepath.Join(t.TempDir(), "dry-with-keys")
	log, err = runProducer(t, unconfigured, append(append(common, operatorKeys...), "--out", refused, "--unsigned-dry-run")...)
	if err == nil || !strings.Contains(log, "throwaway") {
		t.Fatalf("a dry run accepted the operator's release key: %v\n%s", err, log)
	}
	if _, err := os.Stat(refused); !os.IsNotExist(err) {
		t.Fatal("a refused dry run created its output directory")
	}
	spaced := filepath.Join(t.TempDir(), "has space")
	log, err = runProducer(t, unconfigured, append(common, "--out", spaced, "--unsigned-dry-run")...)
	if err == nil || !strings.Contains(log, "whitespace or glob") {
		t.Fatalf("an output path with a space was accepted: %v\n%s", err, log)
	}

	out := filepath.Join(t.TempDir(), "dry")
	log, err = runProducer(t, unconfigured, append(common, "--out", out, "--unsigned-dry-run")...)
	if err != nil {
		t.Fatalf("dry run failed: %v\n%s", err, log)
	}
	if _, err := os.Stat(filepath.Join(out, "UNSIGNED-DRY-RUN")); err != nil {
		t.Fatal("dry run output is not marked UNSIGNED-DRY-RUN")
	}
	deliveryRaw, err := os.ReadFile(filepath.Join(out, MacDeliveryFileName))
	if err != nil {
		t.Fatal(err)
	}
	sigRaw, err := os.ReadFile(filepath.Join(out, MacDeliverySigFileName))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := DecodeMacDeliverySignature(sigRaw)
	if err != nil {
		t.Fatal(err)
	}
	dryPub, err := loadEd25519PublicKey(filepath.Join(out, "UNSIGNED-DRY-RUN.pub.pem"))
	if err != nil {
		t.Fatalf("dry run did not emit its throwaway public key: %v", err)
	}
	operator := []ed25519.PublicKey{priv.Public().(ed25519.PublicKey)}
	if _, err := VerifyMacDownloadPlan(deliveryRaw, sig, operator, time.Now().Add(time.Hour), 0, "arm64"); err == nil {
		t.Fatal("unsigned dry-run output verifies against the operator's trusted release key")
	}
	trusted := []ed25519.PublicKey{dryPub}
	for _, arch := range []string{"amd64", "arm64"} {
		plan, err := VerifyMacDownloadPlan(deliveryRaw, sig, trusted, time.Now().Add(time.Hour), 0, arch)
		if err != nil {
			t.Fatalf("%s delivery plan does not verify: %v", arch, err)
		}
		for _, a := range []MacDeliveryAsset{plan.Controller, plan.Desktop, plan.Installer} {
			size, sum, err := measureFile(filepath.Join(out, "assets", a.Filename))
			if err != nil || size != a.Size || sum != a.SHA256 {
				t.Fatalf("asset %s does not match the delivery index", a.Filename)
			}
		}
	}
	sums, err := os.ReadFile(filepath.Join(out, "SHA256SUMS"))
	if err != nil || bytes.Count(sums, []byte("\n")) != 10 {
		t.Fatalf("SHA256SUMS does not list the four metadata files and six assets: %s", sums)
	}
	if _, err := os.Stat(filepath.Join(out, ".work")); !os.IsNotExist(err) {
		t.Fatal("the producer left its private work directory behind")
	}
}
