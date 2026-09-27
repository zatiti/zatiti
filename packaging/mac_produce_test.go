package packaging

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// appleCall records one Apple tool invocation.
type appleCall struct {
	name string
	args []string
}

type recordingAppleTools struct {
	calls  []appleCall
	output map[string][]byte
	fail   map[string]bool
}

func (r *recordingAppleTools) Run(_ context.Context, name string, args []string) ([]byte, error) {
	r.calls = append(r.calls, appleCall{name, append([]string(nil), args...)})
	key := name
	if len(args) > 0 {
		key = name + " " + args[0]
	}
	if r.fail[key] {
		return []byte("tool said no\n"), errors.New("exit status 1")
	}
	return r.output[key], nil
}

func fullAppleConfig() AppleSigningConfig {
	return AppleSigningConfig{AppIdentity: "Developer ID Application: Example (TEAMID0000)", InstallerIdentity: "Developer ID Installer: Example (TEAMID0000)", NotaryProfile: "zatiti-notary", Keychain: "/tmp/zatiti-release.keychain-db"}
}

func TestAppleStepsFailClosedWhenNotConfigured(t *testing.T) {
	ctx := context.Background()
	tools := &recordingAppleTools{}
	var empty AppleSigningConfig
	cases := map[string]error{
		AppleStepCodesign:    AppleCodesign(ctx, tools, empty, "", "/abs/bin"),
		AppleStepProductsign: AppleProductsign(ctx, tools, empty, "/abs/in.pkg", "/abs/out.pkg"),
		AppleStepStaple:      AppleStaple(ctx, tools, empty, "/abs/out.pkg"),
	}
	_, cases[AppleStepNotarize] = AppleNotarize(ctx, tools, empty, "/abs/out.pkg", time.Minute)
	for step, err := range cases {
		perr := wantFault(t, err, CodePrerequisiteMissing)
		if !strings.Contains(perr.Message, "not configured") {
			t.Errorf("%s: message %q does not say not configured", step, perr.Message)
		}
	}
	if len(tools.calls) != 0 {
		t.Fatalf("an unconfigured step ran a tool: %+v", tools.calls)
	}
	for _, s := range empty.Status() {
		if s.Configured || len(s.Missing) == 0 {
			t.Errorf("empty config reports %s configured", s.Step)
		}
	}
	for _, s := range fullAppleConfig().Status() {
		if !s.Configured {
			t.Errorf("full config reports %s unconfigured: %v", s.Step, s.Missing)
		}
	}
}

func TestAppleCodesignUsesHardenedRuntimeAndVerifies(t *testing.T) {
	tools := &recordingAppleTools{}
	cfg := fullAppleConfig()
	if err := AppleCodesign(context.Background(), tools, cfg, "/abs/ent.plist", "/abs/helper", "/abs/app"); err != nil {
		t.Fatal(err)
	}
	if len(tools.calls) != 4 {
		t.Fatalf("want sign+verify per path, got %+v", tools.calls)
	}
	sign := strings.Join(tools.calls[0].args, " ")
	for _, want := range []string{"--options runtime", "--timestamp", "--sign " + cfg.AppIdentity, "--keychain " + cfg.Keychain, "--entitlements /abs/ent.plist", "/abs/helper"} {
		if !strings.Contains(sign, want) {
			t.Errorf("codesign args %q lack %q", sign, want)
		}
	}
	if tools.calls[1].args[0] != "--verify" || tools.calls[3].args[len(tools.calls[3].args)-1] != "/abs/app" {
		t.Errorf("verification did not follow signing: %+v", tools.calls)
	}
	if err := AppleCodesign(context.Background(), tools, cfg, "", "relative/path"); err == nil {
		t.Fatal("a relative path was signed")
	}
}

func TestAppleNotarizeAcceptsOnlyAcceptedVerdict(t *testing.T) {
	cfg := fullAppleConfig()
	for status, ok := range map[string]bool{"Accepted": true, "Invalid": false, "In Progress": false} {
		tools := &recordingAppleTools{output: map[string][]byte{"/usr/bin/xcrun notarytool": []byte(`{"id":"sub-1","status":"` + status + `"}`)}}
		id, err := AppleNotarize(context.Background(), tools, cfg, "/abs/out.pkg", time.Minute)
		if ok && (err != nil || id != "sub-1") {
			t.Errorf("%s: want success, got %q %v", status, id, err)
		}
		if !ok {
			_ = wantFault(t, err, CodeVerificationFailed)
		}
		args := strings.Join(tools.calls[0].args, " ")
		if !strings.Contains(args, "--keychain-profile zatiti-notary") || !strings.Contains(args, "--wait") {
			t.Errorf("notarytool args %q", args)
		}
	}
	tools := &recordingAppleTools{output: map[string][]byte{"/usr/bin/xcrun notarytool": []byte("not json")}}
	if _, err := AppleNotarize(context.Background(), tools, cfg, "/abs/out.pkg", time.Minute); err == nil {
		t.Fatal("unreadable notary output was treated as success")
	}
	tools = &recordingAppleTools{fail: map[string]bool{"/usr/bin/xcrun notarytool": true}}
	if _, err := AppleNotarize(context.Background(), tools, cfg, "/abs/out.pkg", time.Minute); err == nil {
		t.Fatal("a failed notarytool run was treated as success")
	}
}

func TestAppleProductsignAndStapleVerifyTheirResult(t *testing.T) {
	cfg := fullAppleConfig()
	tools := &recordingAppleTools{}
	if err := AppleProductsign(context.Background(), tools, cfg, "/abs/in.pkg", "/abs/out.pkg"); err != nil {
		t.Fatal(err)
	}
	if tools.calls[1].name != "/usr/sbin/pkgutil" || tools.calls[1].args[0] != "--check-signature" {
		t.Fatalf("productsign was not verified: %+v", tools.calls)
	}
	if err := AppleProductsign(context.Background(), tools, cfg, "/abs/same.pkg", "/abs/same.pkg"); err == nil {
		t.Fatal("productsign accepted identical input and output")
	}
	tools = &recordingAppleTools{fail: map[string]bool{"/usr/bin/xcrun stapler": true}}
	if err := AppleStaple(context.Background(), tools, cfg, "/abs/out.pkg"); err == nil {
		t.Fatal("a failed staple was treated as success")
	}
}

func TestCLIAppleStatusRequireFailsClosed(t *testing.T) {
	for _, k := range []string{EnvAppleAppIdentity, EnvAppleInstallerIdentity, EnvAppleNotaryProfile, EnvAppleKeychain} {
		t.Setenv(k, "")
	}
	var out, errOut bytes.Buffer
	if code := RunCLI([]string{"apple", "--step", "status"}, &out, &errOut); code != 0 {
		t.Fatalf("status without --require failed: %s", errOut.String())
	}
	out.Reset()
	if code := RunCLI([]string{"apple", "--step", "status", "--require"}, &out, &errOut); code == 0 {
		t.Fatal("status --require passed with nothing configured")
	}
	if !strings.Contains(errOut.String(), "prerequisite_missing") || !strings.Contains(errOut.String(), EnvAppleNotaryProfile) {
		t.Fatalf("error does not name the missing setting: %s", errOut.String())
	}
	errOut.Reset()
	if code := RunCLI([]string{"apple", "--step", "notarize", "/abs/out.pkg"}, &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "not configured") {
		t.Fatalf("notarize without a profile did not fail closed: %d %s", code, errOut.String())
	}
}

func writeKeyPair(t *testing.T, dir, name string) (ed25519.PrivateKey, string, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	privPath, pubPath := filepath.Join(dir, name+".key"), filepath.Join(dir, name+".pub")
	writeFile(t, privPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}), 0o600)
	writeFile(t, pubPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}), 0o644)
	return priv, privPath, pubPath
}

func writePart(t *testing.T, part MacReleasePart) string {
	t.Helper()
	writeFile(t, filepath.Join(part.Root, ManifestFileName), part.Manifest, 0o644)
	sigRaw, err := EncodeSignature(part.Signature)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(part.Root, SignatureFileName), sigRaw, 0o644)
	return part.Root
}

func TestCLIMacReleaseAndDeliveryProduceVerifiableMetadata(t *testing.T) {
	keys := t.TempDir()
	priv, privPath, pubPath := writeKeyPair(t, keys, "release")
	args := []string{"mac-release", "--trusted", pubPath, "--key", privPath}
	for _, part := range macParts(t, priv) {
		args = append(args, "--part", writePart(t, part))
	}
	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := RunCLI(append(args, "--out-dir", out), &stdout, &stderr); code != 0 {
		t.Fatalf("mac-release: %s", stderr.String())
	}
	releaseRaw, err := os.ReadFile(filepath.Join(out, MacReleaseFileName))
	if err != nil {
		t.Fatal(err)
	}
	sigRaw, err := os.ReadFile(filepath.Join(out, MacReleaseSignatureFileName))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := DecodeMacReleaseSignature(sigRaw)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyMacReleaseSignature(releaseRaw, sig, []ed25519.PublicKey{priv.Public().(ed25519.PublicKey)}); err != nil {
		t.Fatalf("release signature does not verify: %v", err)
	}
	release, err := DecodeMacRelease(releaseRaw)
	if err != nil {
		t.Fatal(err)
	}

	// Three parts, or an untrusted signer, are refused.
	stderr.Reset()
	if code := RunCLI(append(args[:len(args)-2], "--out-dir", out), &stdout, &stderr); code == 0 {
		t.Fatal("mac-release accepted three parts")
	}
	_, _, otherPub := writeKeyPair(t, keys, "other")
	bad := append([]string(nil), args...)
	bad[2] = otherPub
	if code := RunCLI(append(bad, "--out-dir", t.TempDir()), &stdout, &stderr); code == 0 {
		t.Fatal("mac-release accepted parts signed by an untrusted key")
	}

	assets := t.TempDir()
	dargs := []string{"mac-delivery", "--release", filepath.Join(out, MacReleaseFileName), "--release-sig", filepath.Join(out, MacReleaseSignatureFileName),
		"--base-url", "https://downloads.zatiti.example/releases", "--sequence", "3", "--published-at", "2026-09-27T00:00:00Z", "--key", privPath, "--out-dir", out}
	for _, arch := range []string{"amd64", "arm64"} {
		for _, role := range []string{"controller", "desktop", "installer"} {
			p := filepath.Join(assets, arch+"-"+role)
			writeFile(t, p, []byte("final "+arch+" "+role), 0o644)
			dargs = append(dargs, "--asset", arch+"/"+role+"="+p)
		}
	}
	stdout.Reset()
	stderr.Reset()
	if code := RunCLI(dargs, &stdout, &stderr); code != 0 {
		t.Fatalf("mac-delivery: %s", stderr.String())
	}
	deliveryRaw, _ := os.ReadFile(filepath.Join(out, MacDeliveryFileName))
	dsRaw, _ := os.ReadFile(filepath.Join(out, MacDeliverySigFileName))
	ds, err := DecodeMacDeliverySignature(dsRaw)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	plan, err := VerifyMacDownloadPlan(deliveryRaw, ds, []ed25519.PublicKey{priv.Public().(ed25519.PublicKey)}, now, 0, "arm64")
	if err != nil {
		t.Fatalf("delivery does not verify: %v", err)
	}
	if plan.Installer.Size != int64(len("final arm64 installer")) || plan.Installer.URL != "https://downloads.zatiti.example/releases/zatiti-"+release.Version+"-darwin-arm64-installer.pkg" {
		t.Fatalf("installer asset was not measured from its file: %+v", plan.Installer)
	}
	var doc struct {
		Assets []MacDeliveryAsset `json:"assets"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil || len(doc.Assets) != 6 {
		t.Fatalf("stdout is not the six-asset summary: %s", stdout.String())
	}
	stderr.Reset()
	if code := RunCLI(dargs[:len(dargs)-2], &stdout, &stderr); code == 0 {
		t.Fatal("mac-delivery accepted five assets")
	}
}

func TestCLIMacPkgBuildsBoundInstaller(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Mac package production requires macOS pkgbuild and productbuild")
	}
	d, _, _, _, _, _ := deliveryFixture(t)
	dir := t.TempDir()
	releaseRaw, err := EncodeMacRelease(d.Release)
	if err != nil {
		t.Fatal(err)
	}
	releasePath := filepath.Join(dir, MacReleaseFileName)
	writeFile(t, releasePath, releaseRaw, 0o644)
	controller, desktop := filepath.Join(dir, "c.tar.gz"), filepath.Join(dir, "d.tar.gz")
	writeFile(t, controller, []byte("controller archive"), 0o600)
	writeFile(t, desktop, []byte("desktop archive"), 0o600)
	var stdout, stderr bytes.Buffer
	code := RunCLI([]string{"mac-pkg", "--release", releasePath, "--arch", "arm64", "--sequence", "4", "--controller", controller, "--desktop", desktop, "--out-dir", dir}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("mac-pkg: %s", stderr.String())
	}
	var doc struct {
		Assets    []MacDeliveryAsset `json:"assets"`
		Installer string             `json:"installer"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil || len(doc.Assets) != 3 {
		t.Fatalf("stdout: %s", stdout.String())
	}
	size, sum, err := measureFile(doc.Installer)
	if err != nil || size != doc.Assets[2].Size || sum != doc.Assets[2].SHA256 {
		t.Fatalf("installer asset %+v does not match its file (%d %s %v)", doc.Assets[2], size, sum, err)
	}
	if code := RunCLI([]string{"mac-pkg", "--release", releasePath, "--arch", "ppc", "--sequence", "4", "--controller", controller, "--desktop", desktop, "--out-dir", dir}, &stdout, &stderr); code == 0 {
		t.Fatal("mac-pkg accepted an unsupported architecture")
	}
}
