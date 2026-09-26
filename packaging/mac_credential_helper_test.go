package packaging

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestMacCredentialHelperRequiredAtFixedSignedExecutablePath(t *testing.T) {
	f := newFixture(t, "1.2.3", "darwin")
	if _, err := Build(f.input); err != nil {
		t.Fatal(err)
	}
	delete(f.input.Kinds, "bin/zatiti-credential-helper")
	if err := os.Remove(filepath.Join(f.root, "bin", "zatiti-credential-helper")); err != nil {
		t.Fatal(err)
	}
	wantCode(t, f.buildOrError(), CodeInvalidInput)

	f = newFixture(t, "1.2.3", "darwin")
	f.input.Attestations = nil
	wantCode(t, f.buildOrError(), CodeInvalidInput)

	f = newFixture(t, "1.2.3", "darwin")
	writeFile(t, filepath.Join(f.root, "bin", "zatiti-credential-helper"), []byte("non-executable"), 0o644)
	wantCode(t, f.buildOrError(), CodeInvalidInput)

	f = newFixture(t, "1.2.3", "darwin")
	f.input.Kinds["bin/zatiti-credential-helper"] = KindDocumentation
	wantCode(t, f.buildOrError(), CodeInvalidInput)
}

func TestMacReleaseRejectsCredentialHelperWrongArchitecture(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	parts := macParts(t, priv)
	for i := range parts {
		m, err := Decode(parts[i].Manifest)
		if err != nil {
			t.Fatal(err)
		}
		if m.Target.Arch != "amd64" || m.Distribution != DistributionController {
			continue
		}
		writeFile(t, filepath.Join(parts[i].Root, "bin", "zatiti-credential-helper"), syntheticMachO("arm64"), 0o755)
		f := newFixture(t, m.Version, "darwin")
		f.root = parts[i].Root
		f.input.Root, f.input.Target.Arch = parts[i].Root, "amd64"
		m = f.build(t)
		parts[i].Manifest, err = Encode(m)
		if err != nil {
			t.Fatal(err)
		}
		parts[i].Signature, err = Sign(parts[i].Manifest, priv)
		if err != nil {
			t.Fatal(err)
		}
		break
	}
	if _, err := AssembleMacRelease(parts, []ed25519.PublicKey{pub}); Code(err) != CodeVerificationFailed {
		t.Fatalf("wrong-architecture signed helper accepted: %v", err)
	}
}

func (f fixture) buildOrError() error {
	_, err := Build(f.input)
	return err
}

func TestCredentialHelperKindRefusedOutsideMacController(t *testing.T) {
	f := newFixture(t, "1.2.3", "linux")
	writeFile(t, filepath.Join(f.root, "bin", "zatiti-credential-helper"), []byte("helper"), 0o755)
	f.input.Kinds["bin/zatiti-credential-helper"] = KindCredentialHelper
	wantCode(t, f.buildOrError(), CodeInvalidInput)

	d := newDesktopFixture(t, "1.2.3", "darwin")
	writeFile(t, filepath.Join(d.root, "bin", "zatiti-credential-helper"), []byte("helper"), 0o755)
	d.input.Kinds["bin/zatiti-credential-helper"] = KindCredentialHelper
	wantCode(t, d.buildOrError(), CodeInvalidInput)
}
