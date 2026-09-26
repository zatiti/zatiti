package packaging

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestHostedMacControllerServiceUsesFixedKeychainSelector(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "zatiti-hosted-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	l, err := NewLayout(LayoutInput{OS: "darwin", Home: home, StateDir: filepath.Join(home, "Library", "Application Support", "zatiti")})
	if err != nil {
		t.Fatal(err)
	}
	m := hostedMacFixture(t, "1.0.0").build(t)
	s, err := HostedMacControllerService(l, m)
	if err != nil {
		t.Fatal(err)
	}
	u, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(u.Content, []byte("secret:master")) || !bytes.Contains(u.Content, []byte("keychain")) {
		t.Fatal("fixed launcher lost Keychain selectors")
	}
	changed := l
	changed.StateDir = filepath.Join(home, "alternate")
	wantCode(t, func() error { _, err := HostedMacControllerService(changed, m); return err }(), CodeInvalidInput)
	local := newFixture(t, "1.0.0", "darwin").build(t)
	wantCode(t, func() error { _, err := HostedMacControllerService(l, local); return err }(), CodeInvalidInput)
}

func TestAssembleHostedMacReleaseWithoutLocalRuntime(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	parts := make([]MacReleasePart, 0, 4)
	for _, arch := range []string{"amd64", "arm64"} {
		f := hostedMacFixture(t, "1.0.0")
		f.input.Target.Arch = arch
		writeFile(t, filepath.Join(f.root, "bin", "zatiti"), syntheticMachO(arch), 0o755)
		writeFile(t, filepath.Join(f.root, "bin", "zatiti-credential-helper"), syntheticMachO(arch), 0o755)
		m := f.build(t)
		raw, err := Encode(m)
		if err != nil {
			t.Fatal(err)
		}
		sig, err := Sign(raw, priv)
		if err != nil {
			t.Fatal(err)
		}
		parts = append(parts, MacReleasePart{Root: f.root, Manifest: raw, Signature: sig})
		parts = append(parts, macPart(t, priv, "1.0.0", arch, DistributionDesktop))
	}
	if _, err := AssembleMacRelease(parts, []ed25519.PublicKey{pub}); err != nil {
		t.Fatalf("hosted Mac release: %v", err)
	}
}

func hostedMacFixture(t *testing.T, version string) fixture {
	t.Helper()
	f := newFixture(t, version, "darwin")
	for _, rel := range []string{"serenity/serenity", "serenity/read-facade", "licenses/serenity.NOTICE"} {
		if err := os.Remove(filepath.Join(f.root, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
		delete(f.input.Kinds, rel)
	}
	f.input.Serenity = SerenityPin{}
	f.input.Licenses = f.input.Licenses[:1]
	writeFile(t, filepath.Join(f.root, "sbom.spdx.json"), []byte(`{"spdxVersion":"SPDX-2.3","packages":[{"name":"`+ProjectComponent+`","versionInfo":"`+version+`"}]}`), 0o644)
	return f
}

func TestHostedMacControllerNeedsNoLocalSerenity(t *testing.T) {
	f := hostedMacFixture(t, "1.0.0")
	m := f.build(t)
	if m.Serenity != (SerenityPin{}) {
		t.Fatal("hosted manifest unexpectedly pinned local Serenity")
	}
	if err := VerifyTree(m, f.root); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{KindSerenityRuntime, KindSerenityReadFacade} {
		bad := m
		bad.Artifacts = append([]Artifact(nil), m.Artifacts...)
		bad.Artifacts[len(bad.Artifacts)-1].Kind = kind
		wantCode(t, bad.Validate(), CodeInvalidInput)
	}
	home := t.TempDir()
	state := filepath.Join(home, "Library", "Application Support", "zatiti")
	l, err := NewLayout(LayoutInput{OS: "darwin", Home: home, StateDir: state})
	if err != nil {
		t.Fatal(err)
	}
	controller := controllerSpec(l, m)
	if _, err := PlanInstallation(InstallInput{Layout: l, Manifest: m, SourceRoot: f.root, HostArch: "arm64", Services: []ServiceSpec{controller}}); err != nil {
		t.Fatalf("hosted controller-only plan: %v", err)
	}
	serenity := ServiceSpec{Manager: l.Manager, Role: RoleSerenity, Label: SerenityLabel, Description: "Serenity", Executable: filepath.Join(l.Current, "serenity", "serenity"), Owns: filepath.Join(home, "brain")}
	_, err = PlanInstallation(InstallInput{Layout: l, Manifest: m, SourceRoot: f.root, HostArch: "arm64", Services: []ServiceSpec{controller, serenity}})
	wantCode(t, err, CodeInvalidInput)
}

func TestFixedMacMasterSelector(t *testing.T) {
	s := launchdSpec()
	s.Owns = filepath.Join(t.TempDir(), "Library", "Application Support", "zatiti")
	s.Arguments = []string{"serve", "--socket", "/tmp/zatiti-hosted-test.sock", "--credential-backend", "keychain", "--master-key", "secret:master"}
	if _, err := Render(s); err != nil {
		t.Fatalf("fixed selector rejected: %v", err)
	}
	for _, args := range [][]string{
		{"serve", "--socket", "/tmp/zatiti-hosted-test.sock", "--credential-backend", "keychain", "--master-key", "secret:other"},
		{"serve", "--socket", "/tmp/zatiti-hosted-test.sock", "--credential-backend", "keychain", "--master-key=secret:master"},
		{"serve", "--socket", "/tmp/zatiti-hosted-test.sock", "--master-key", "secret:master"},
		{"serve", "--socket", "/tmp/zatiti-hosted-test.sock", "--credential-backend", "keychain", "--master-key", "secret:master", "--master-key", "secret:master"},
		{"serve", "--socket", "/tmp/zatiti-hosted-test.sock", "--credential-backend", "keychain", "--master-key", "secret:master", "--master-key-ref=secret:other"},
		{"serve", "--socket", "/tmp/zatiti-hosted-test.sock", "--credential-backend", "file", "--master-key", "secret:master"},
	} {
		s.Arguments = args
		wantCode(t, s.validate(), CodeInvalidInput)
	}
	s.Arguments = []string{"serve", "--socket", "/tmp/zatiti-hosted-test.sock", "--credential-backend", "keychain", "--master-key", "secret:master"}
	s.Role = RoleSerenity
	wantCode(t, s.validate(), CodeInvalidInput)
}
