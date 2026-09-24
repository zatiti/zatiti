package packaging

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func macPkgBindingFixture(t *testing.T) (string, MacReleaseDescriptor, MacDownloadPlan, MacPkgBinding, string) {
	t.Helper()
	d, _, _, _, _, _ := deliveryFixture(t)
	plan, err := SelectMacDownloadPlan(d, "arm64")
	if err != nil {
		t.Fatal(err)
	}
	controller, desktop := []byte("signed controller archive"), []byte("signed desktop archive")
	cs, ds := sha256.Sum256(controller), sha256.Sum256(desktop)
	plan.Controller.Size, plan.Desktop.Size = int64(len(controller)), int64(len(desktop))
	plan.Controller.SHA256, plan.Desktop.SHA256 = hex.EncodeToString(cs[:]), hex.EncodeToString(ds[:])
	b, err := NewMacPkgBinding(d.Release, plan)
	if err != nil {
		t.Fatal(err)
	}
	home := canonicalTempDir(t)
	dir, err := MacPkgInboxPath(home, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := EncodeMacPkgBinding(b)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"binding.json": raw, "controller.tar.gz": controller, "desktop.tar.gz": desktop} {
		writeFile(t, filepath.Join(dir, name), data, 0o600)
	}
	return home, d.Release, plan, b, dir
}

func TestMacPkgBindingCanonicalAcyclicAndPlanMatched(t *testing.T) {
	_, release, plan, b, _ := macPkgBindingFixture(t)
	raw, err := EncodeMacPkgBinding(b)
	if err != nil || len(raw) > MaxMacPkgBindingBytes {
		t.Fatalf("encode: %v", err)
	}
	got, err := DecodeMacPkgBinding(raw)
	if err != nil || got != b || VerifyMacPkgBinding(raw, release, plan) != nil {
		t.Fatalf("roundtrip or signed-plan match failed: %v", err)
	}
	for _, forbidden := range []string{"delivery_sha256", "installer_sha256", "package_sha256", "url", "signing_key", "path"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatalf("binding contains circular or mutable field %q", forbidden)
		}
	}
	for _, bad := range [][]byte{
		append([]byte(" "), raw...),
		append(append([]byte{}, bytes.TrimSuffix(raw, []byte("\n"))...), []byte("  \n")...),
		[]byte(strings.Replace(string(raw), `"schema":`, `"schema":"duplicate","schema":`, 1)),
		[]byte(strings.Replace(string(raw), `"arch":`, `"extra":1,"arch":`, 1)),
		bytes.Repeat([]byte("a"), MaxMacPkgBindingBytes+1),
		[]byte{0xff},
	} {
		if _, err := DecodeMacPkgBinding(bad); err == nil {
			t.Fatalf("accepted corrupt binding prefix %q", bad[:min(len(bad), 80)])
		}
	}
	wrongPlan := plan
	wrongPlan.Controller.SHA256 = strings.Repeat("b", 64)
	wantCode(t, VerifyMacPkgBinding(raw, release, wrongPlan), CodeVerificationFailed)
	changedArch := []byte(strings.Replace(string(raw), `"arch":"arm64"`, `"arch":"amd64"`, 1))
	wantCode(t, VerifyMacPkgBinding(changedArch, release, plan), CodeVerificationFailed)
}

func TestMacPkgInboxExactProtectedFilesAndDigests(t *testing.T) {
	home, release, plan, b, dir := macPkgBindingFixture(t)
	if _, err := VerifyMacPkgInbox(home, b, release, plan); err != nil {
		t.Fatalf("valid inbox: %v", err)
	}
	writeFile(t, filepath.Join(dir, "extra"), []byte("x"), 0o600)
	if _, err := VerifyMacPkgInbox(home, b, release, plan); Code(err) != CodeVerificationFailed {
		t.Fatalf("extra file: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "extra")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "controller.tar.gz"), []byte("tampered controller archive"), 0o600)
	if _, err := VerifyMacPkgInbox(home, b, release, plan); Code(err) != CodeVerificationFailed {
		t.Fatalf("tampered archive: %v", err)
	}
}

func TestMacPkgInboxRejectsSymlinkAndPermissiveMode(t *testing.T) {
	home, release, plan, b, dir := macPkgBindingFixture(t)
	file := filepath.Join(dir, "desktop.tar.gz")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "controller.tar.gz"), file); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyMacPkgInbox(home, b, release, plan); Code(err) != CodeVerificationFailed {
		t.Fatalf("symlinked archive: %v", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	writeFile(t, file, []byte("signed desktop archive"), 0o644)
	if _, err := VerifyMacPkgInbox(home, b, release, plan); Code(err) != CodeVerificationFailed {
		t.Fatalf("permissive archive: %v", err)
	}
}
