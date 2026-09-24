package packaging

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

func macAuditFixture(t *testing.T) (string, string, ed25519.PublicKey, ed25519.PrivateKey, Layout, Layout, *recordingManager) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, "Library", "Application Support", "zatiti")
	controller, err := NewLayout(LayoutInput{OS: "darwin", Home: home, StateDir: state})
	if err != nil {
		t.Fatal(err)
	}
	desktop, err := NewDesktopLayout(DesktopLayoutInput{OS: "darwin", Home: home, StateDir: state})
	if err != nil {
		t.Fatal(err)
	}
	return home, state, pub, priv, controller, desktop, &recordingManager{layout: controller}
}

func macAuditParts(t *testing.T, signer ed25519.PrivateKey, version string) []MacReleasePart {
	t.Helper()
	var parts []MacReleasePart
	for _, arch := range []string{"amd64", "arm64"} {
		for _, distribution := range []string{DistributionController, DistributionDesktop} {
			parts = append(parts, macPart(t, signer, version, arch, distribution))
		}
	}
	return parts
}

func applyMacAuditPart(t *testing.T, part MacReleasePart, controller, desktop Layout, manager *recordingManager) {
	t.Helper()
	m, err := Decode(part.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if m.Target.Arch != "arm64" {
		return
	}
	if m.Distribution == DistributionController {
		installed, err := Inspect(controller)
		if err != nil {
			t.Fatal(err)
		}
		p, err := PlanInstallation(InstallInput{Layout: controller, Manifest: m, SourceRoot: part.Root, HostArch: "arm64", Services: []ServiceSpec{controllerSpec(controller, m)}, Installed: installed})
		if err != nil {
			t.Fatal(err)
		}
		if err := Apply(context.Background(), p, manager); err != nil {
			t.Fatal(err)
		}
	} else {
		installed, err := Inspect(desktop)
		if err != nil {
			t.Fatal(err)
		}
		p, err := PlanDesktopInstallation(DesktopInstallInput{Layout: desktop, Manifest: m, SourceRoot: part.Root, HostArch: "arm64", Installed: installed})
		if err != nil {
			t.Fatal(err)
		}
		if err := Apply(context.Background(), p, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMacActiveAuditBindsSignedControllerDesktopAndHelper(t *testing.T) {
	home, state, pub, priv, controller, desktop, manager := macAuditFixture(t)
	parts := macAuditParts(t, priv, "1.0.0")
	release, err := AssembleMacRelease(parts, []ed25519.PublicKey{pub})
	if err != nil {
		t.Fatal(err)
	}
	active, err := AuditMacReleaseTrees(context.Background(), home, state, release, "arm64")
	if err != nil || active {
		t.Fatalf("empty audit: %v %v", active, err)
	}
	for _, part := range parts {
		if m, _ := Decode(part.Manifest); m.Target.Arch == "arm64" && m.Distribution == DistributionController {
			applyMacAuditPart(t, part, controller, desktop, manager)
		}
	}
	if _, err := AuditMacReleaseTrees(context.Background(), home, state, release, "arm64"); Code(err) != CodeVerificationFailed {
		t.Fatalf("partial activation accepted: %v", err)
	}
	for _, part := range parts {
		if m, _ := Decode(part.Manifest); m.Target.Arch == "arm64" && m.Distribution == DistributionDesktop {
			applyMacAuditPart(t, part, controller, desktop, manager)
		}
	}
	if active, err = AuditMacReleaseTrees(context.Background(), home, state, release, "arm64"); err != nil || !active {
		t.Fatalf("signed active release: %v %v", active, err)
	}
	launcher := filepath.Join(controller.UnitDir, unitFileName(controller.Manager, ControllerLabel))
	want, err := os.ReadFile(launcher)
	if err != nil {
		t.Fatal(err)
	}
	if err := AuditMacControllerLauncherBytes(controller, want); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, append(want, byte('x')), 0o644); err != nil {
		t.Fatal(err)
	}
	if Code(AuditMacControllerLauncherBytes(controller, want)) != CodeVerificationFailed {
		t.Fatal("changed LaunchAgent accepted")
	}
	if err := os.WriteFile(launcher, want, 0o644); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(controller.VersionDir(release.Version), "bin", "zatiti-credential-helper")
	if err := os.WriteFile(helper, []byte("tampered helper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := AuditMacReleaseTrees(context.Background(), home, state, release, "arm64"); err == nil {
		t.Fatal("tampered helper accepted")
	}
}

func TestMacActiveAuditRejectsChangedDescriptorAndDesktopLauncher(t *testing.T) {
	home, state, pub, priv, controller, desktop, manager := macAuditFixture(t)
	parts := macAuditParts(t, priv, "1.0.0")
	release, err := AssembleMacRelease(parts, []ed25519.PublicKey{pub})
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range parts {
		applyMacAuditPart(t, part, controller, desktop, manager)
	}
	changed := release
	changed.Components = append([]MacReleaseComponent(nil), release.Components...)
	for i := range changed.Components {
		if changed.Components[i].Arch == "arm64" && changed.Components[i].Distribution == DistributionController {
			changed.Components[i].ManifestSHA256 = release.Components[0].ManifestSHA256
			changed.Components[i].Signature.ManifestSHA256 = changed.Components[i].ManifestSHA256
		}
	}
	if _, err := AuditMacReleaseTrees(context.Background(), home, state, changed, "arm64"); Code(err) != CodeVerificationFailed {
		t.Fatalf("changed descriptor accepted: %v", err)
	}
	m, err := readDescriptorBoundMacManifest(desktop, release, "arm64", DistributionDesktop)
	if err != nil {
		t.Fatal(err)
	}
	launcher := desktopLauncherPath(desktop, m)
	if err := os.Remove(launcher); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(desktop.CurrentBundle, "Other.app"), launcher); err != nil {
		t.Fatal(err)
	}
	if _, err := AuditMacReleaseTrees(context.Background(), home, state, release, "arm64"); err == nil {
		t.Fatal("redirected desktop launcher accepted")
	}
}

func TestMacActiveAuditUpgradePreservesStateAndRejectsOldRelease(t *testing.T) {
	home, state, pub, priv, controller, desktop, manager := macAuditFixture(t)
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(state, "zatiti.db")
	if err := os.WriteFile(stateFile, []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := macAuditParts(t, priv, "1.0.0")
	d1, err := AssembleMacRelease(first, []ed25519.PublicKey{pub})
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range first {
		applyMacAuditPart(t, part, controller, desktop, manager)
	}
	second := macAuditParts(t, priv, "2.0.0")
	d2, err := AssembleMacRelease(second, []ed25519.PublicKey{pub})
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range second {
		applyMacAuditPart(t, part, controller, desktop, manager)
	}
	if active, err := AuditMacReleaseTrees(context.Background(), home, state, d2, "arm64"); err != nil || !active {
		t.Fatalf("new release: %v %v", active, err)
	}
	if _, err := AuditMacReleaseTrees(context.Background(), home, state, d1, "arm64"); Code(err) != CodeVerificationFailed {
		t.Fatalf("old release accepted: %v", err)
	}
	raw, err := os.ReadFile(stateFile)
	if err != nil || string(raw) != "retained" {
		t.Fatalf("state changed: %q %v", raw, err)
	}
}
