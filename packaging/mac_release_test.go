package packaging

import (
	"crypto/ed25519"
	"crypto/rand"
	"debug/macho"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func syntheticMachO(arch string) []byte {
	header := make([]byte, 32)
	binary.LittleEndian.PutUint32(header[0:], 0xfeedfacf)
	cpu := macho.CpuAmd64
	if arch == "arm64" {
		cpu = macho.CpuArm64
	}
	binary.LittleEndian.PutUint32(header[4:], uint32(cpu))
	binary.LittleEndian.PutUint32(header[12:], uint32(macho.TypeExec))
	return header
}

func syntheticFatMachO() []byte {
	raw := make([]byte, 112)
	binary.BigEndian.PutUint32(raw[0:], 0xcafebabe)
	binary.BigEndian.PutUint32(raw[4:], 2)
	for i, arch := range []string{"amd64", "arm64"} {
		record := raw[8+i*20:]
		cpu := macho.CpuAmd64
		if arch == "arm64" {
			cpu = macho.CpuArm64
		}
		binary.BigEndian.PutUint32(record[0:], uint32(cpu))
		binary.BigEndian.PutUint32(record[8:], uint32(48+i*32))
		binary.BigEndian.PutUint32(record[12:], 32)
		binary.BigEndian.PutUint32(record[16:], 2)
		copy(raw[48+i*32:], syntheticMachO(arch))
	}
	return raw
}

func macPartWithBinary(t *testing.T, signer ed25519.PrivateKey, version, arch, distribution string, binary []byte) MacReleasePart {
	t.Helper()
	var f fixture
	if distribution == DistributionController {
		f = newFixture(t, version, "darwin")
		writeFile(t, filepath.Join(f.root, "bin", "zatiti"), binary, 0o755)
	} else {
		f = newDesktopFixture(t, version, "darwin")
		bundleDir, executable := stageBundle(t, "darwin", version)
		writeFile(t, filepath.Join(bundleDir, filepath.FromSlash(executable)), binary, 0o755)
		archive := filepath.Join(f.root, filepath.FromSlash(f.input.Desktop.Bundle))
		if err := os.Remove(archive); err != nil {
			t.Fatal(err)
		}
		if _, _, err := AssembleBundle(bundleDir, archive); err != nil {
			t.Fatal(err)
		}
		f.input.Desktop.Executable = executable
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
	return MacReleasePart{Root: f.root, Manifest: raw, Signature: sig}
}

func macPart(t *testing.T, signer ed25519.PrivateKey, version, arch, distribution string) MacReleasePart {
	return macPartWithBinary(t, signer, version, arch, distribution, syntheticMachO(arch))
}

func macParts(t *testing.T, signer ed25519.PrivateKey) []MacReleasePart {
	t.Helper()
	var parts []MacReleasePart
	for _, arch := range []string{"amd64", "arm64"} {
		for _, distribution := range []string{DistributionController, DistributionDesktop} {
			parts = append(parts, macPart(t, signer, "1.0.0", arch, distribution))
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
	mismatch := append([]MacReleasePart{}, parts...)
	mismatch[3] = macPart(t, signer, "1.0.1", "arm64", DistributionDesktop)
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

func TestMacReleaseRejectsWrongOrAmbiguousMachO(t *testing.T) {
	pub, signer, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	good := macParts(t, signer)
	notExecutable := syntheticMachO("amd64")
	binary.LittleEndian.PutUint32(notExecutable[12:], uint32(macho.TypeDylib))
	for _, tc := range []struct {
		name       string
		index      int
		executable []byte
	}{
		{"Intel controller with arm64 code", 0, syntheticMachO("arm64")},
		{"Apple Silicon desktop with amd64 code", 3, syntheticMachO("amd64")},
		{"universal controller", 0, syntheticFatMachO()},
		{"library passed as controller executable", 0, notExecutable},
		{"non Mach-O controller", 0, []byte("not a native binary")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parts := append([]MacReleasePart{}, good...)
			arch, dist := "amd64", DistributionController
			if tc.index == 3 {
				arch, dist = "arm64", DistributionDesktop
			}
			parts[tc.index] = macPartWithBinary(t, signer, "1.0.0", arch, dist, tc.executable)
			_, err := AssembleMacRelease(parts, []ed25519.PublicKey{pub})
			wantCode(t, err, CodeVerificationFailed)
		})
	}
}

func macPartWithNestedLibrary(t *testing.T, signer ed25519.PrivateKey, arch string, runner, library []byte) MacReleasePart {
	t.Helper()
	f := newDesktopFixture(t, "1.0.0", "darwin")
	bundleDir, executable := stageBundle(t, "darwin", "1.0.0")
	writeFile(t, filepath.Join(bundleDir, filepath.FromSlash(executable)), runner, 0o755)
	writeFile(t, filepath.Join(bundleDir, "Zatiti.app", "Contents", "Frameworks", "libswiftCore.dylib"), library, 0o755)
	archive := filepath.Join(f.root, filepath.FromSlash(f.input.Desktop.Bundle))
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	if _, _, err := AssembleBundle(bundleDir, archive); err != nil {
		t.Fatal(err)
	}
	f.input.Desktop.Executable = executable
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
	return MacReleasePart{Root: f.root, Manifest: raw, Signature: sig}
}

func TestMacReleaseAuditsEveryNestedMachO(t *testing.T) {
	pub, signer, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	parts := macParts(t, signer)
	library := syntheticMachO("amd64")
	binary.LittleEndian.PutUint32(library[12:], uint32(macho.TypeDylib))
	parts[1] = macPartWithNestedLibrary(t, signer, "amd64", syntheticFatMachO(), library)
	parts[3] = macPartWithNestedLibrary(t, signer, "arm64", syntheticFatMachO(), library)
	if _, err := AssembleMacRelease(parts, []ed25519.PublicKey{pub}); Code(err) != CodeVerificationFailed {
		t.Fatalf("arm64 bundle accepted Intel-only nested dylib: %v", err)
	}
	// The same bundle is valid for Intel, and an independently native arm64
	// library makes the Apple Silicon component valid too.
	armLibrary := syntheticMachO("arm64")
	binary.LittleEndian.PutUint32(armLibrary[12:], uint32(macho.TypeDylib))
	parts[3] = macPartWithNestedLibrary(t, signer, "arm64", syntheticFatMachO(), armLibrary)
	if _, err := AssembleMacRelease(parts, []ed25519.PublicKey{pub}); err != nil {
		t.Fatalf("compatible mixed thin/fat bundles: %v", err)
	}
}

func TestMacBundleRejectsMalformedAndUnsupportedFatSlices(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func([]byte)
	}{
		{"unsupported CPU", func(b []byte) { binary.BigEndian.PutUint32(b[8:], uint32(macho.Cpu386)) }},
		{"out of bounds slice", func(b []byte) { binary.BigEndian.PutUint32(b[8+20+8:], 0xffffff00) }},
		{"fat64 unsupported", func(b []byte) { binary.BigEndian.PutUint32(b[:4], 0xcafebabf) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := syntheticFatMachO()
			tc.mutate(raw)
			path := filepath.Join(t.TempDir(), "object.dylib")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			wantCode(t, verifyDesktopMachO(path, "amd64", false), CodeVerificationFailed)
		})
	}
}
