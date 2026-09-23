package packaging

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

func macParts(t *testing.T, signer ed25519.PrivateKey) []MacReleasePart {
	t.Helper()
	var parts []MacReleasePart
	for _, arch := range []string{"amd64", "arm64"} {
		for _, distribution := range []string{DistributionController, DistributionDesktop} {
			var f fixture
			if distribution == DistributionController {
				f = newFixture(t, "1.0.0", "darwin")
			} else {
				f = newBundledDesktopFixture(t, "1.0.0", "darwin")
			}
			f.input.Target.Arch = arch
			m := f.build(t)
			raw, err := Encode(m)
			if err != nil {
				t.Fatal(err)
			}
			sig, err := Sign(raw, signer)
			if err != nil {
				t.Fatal(err)
			}
			parts = append(parts, MacReleasePart{Root: f.root, Manifest: raw, Signature: sig})
		}
	}
	return parts
}

func TestMacReleaseMatchesFourSignedComponents(t *testing.T) {
	pub, signer, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	parts := macParts(t, signer)
	d, err := AssembleMacRelease(parts, []ed25519.PublicKey{pub})
	if err != nil {
		t.Fatal(err)
	}
	if d.Version != "1.0.0" || len(d.Components) != 4 {
		t.Fatalf("descriptor: %+v", d)
	}
	raw, err := EncodeMacRelease(d)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := SignMacRelease(raw, signer)
	if err != nil {
		t.Fatal(err)
	}
	sigBytes, err := EncodeMacReleaseSignature(signed)
	if err != nil {
		t.Fatal(err)
	}
	signed, err = DecodeMacReleaseSignature(sigBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyMacReleaseSignature(raw, signed, []ed25519.PublicKey{pub}); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeMacRelease(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyMacRelease(decoded, parts, []ed25519.PublicKey{pub}); err != nil {
		t.Fatal(err)
	}
	parts[0], parts[3] = parts[3], parts[0]
	if err := VerifyMacRelease(decoded, parts, []ed25519.PublicKey{pub}); err != nil {
		t.Fatalf("part order changed result: %v", err)
	}
}

func TestMacReleaseRejectsTamperMismatchAndUnsupportedMetadata(t *testing.T) {
	pub, signer, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	parts := macParts(t, signer)
	d, err := AssembleMacRelease(parts, []ed25519.PublicKey{pub})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := EncodeMacRelease(d)
	signed, _ := SignMacRelease(raw, signer)
	changedDescriptor := d
	changedDescriptor.Version = "1.0.1"
	validTamper, err := EncodeMacRelease(changedDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	wantCode(t, VerifyMacReleaseSignature(validTamper, signed, []ed25519.PublicKey{pub}), CodeVerificationFailed)
	wantCode(t, VerifyMacRelease(changedDescriptor, parts, []ed25519.PublicKey{pub}), CodeVerificationFailed)
	changed := append([]byte{}, raw...)
	changed[len(changed)-2] = 'x'
	wantCode(t, VerifyMacReleaseSignature(changed, signed, []ed25519.PublicKey{pub}), CodeInvalidInput)
	differentPub, _, _ := ed25519.GenerateKey(rand.Reader)
	wantCode(t, VerifyMacReleaseSignature(raw, signed, []ed25519.PublicKey{differentPub}), CodeVerificationFailed)

	missing := append([]MacReleasePart{}, parts[:3]...)
	_, err = AssembleMacRelease(missing, []ed25519.PublicKey{pub})
	wantCode(t, err, CodeInvalidInput)
	duplicate := append([]MacReleasePart{}, parts...)
	duplicate[3] = duplicate[2]
	_, err = AssembleMacRelease(duplicate, []ed25519.PublicKey{pub})
	wantCode(t, err, CodeCapabilityUnsupported)
	otherVersion := newBundledDesktopFixture(t, "1.0.1", "darwin")
	otherVersion.input.Target.Arch = "arm64"
	m := otherVersion.build(t)
	manifest, _ := Encode(m)
	sig, _ := Sign(manifest, signer)
	mismatch := append([]MacReleasePart{}, parts...)
	mismatch[3] = MacReleasePart{Root: otherVersion.root, Manifest: manifest, Signature: sig}
	_, err = AssembleMacRelease(mismatch, []ed25519.PublicKey{pub})
	wantCode(t, err, CodeConflict)
	badArchive := newDesktopFixture(t, "1.0.0", "darwin")
	badArchive.input.Target.Arch = "arm64"
	badManifest := badArchive.build(t)
	badRaw, _ := Encode(badManifest)
	badSig, _ := Sign(badRaw, signer)
	invalidBundle := append([]MacReleasePart{}, parts...)
	invalidBundle[3] = MacReleasePart{Root: badArchive.root, Manifest: badRaw, Signature: badSig}
	_, err = AssembleMacRelease(invalidBundle, []ed25519.PublicKey{pub})
	wantCode(t, err, CodeInvalidInput)

	writeFile(t, filepath.Join(parts[0].Root, "bin", "zatiti"), []byte("tampered"), 0o755)
	wantCode(t, VerifyMacRelease(d, parts, []ed25519.PublicKey{pub}), CodeVerificationFailed)
	if err := os.Remove(filepath.Join(parts[0].Root, "bin", "zatiti")); err != nil {
		t.Fatal(err)
	}
	_, err = AssembleMacRelease(parts, []ed25519.PublicKey{pub})
	wantCode(t, err, CodeVerificationFailed)
	d.Schema = "zatiti.mac_release/v2"
	wantCode(t, VerifyMacRelease(d, parts, []ed25519.PublicKey{pub}), CodeCapabilityUnsupported)
}
