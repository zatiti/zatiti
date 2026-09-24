package qualification_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Native Mac qualification is intentionally split into observations a
// read-only host probe can make and journeys requiring a controlled, clean
// installed host. No supplied JSON, screenshot, or synthetic fixture can
// promote the latter into a passed case.
func TestNativeMacRelease(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			t.Run("signed_artifacts", func(t *testing.T) { qualifyNativeMacArtifacts(t, arch) })
			t.Run("installed_pkg_bootstrap", func(t *testing.T) {
				c := beginCase(t, "MAC."+arch+".installed_pkg_bootstrap", "QUALIFICATION",
					"clean native host installs the verified per-user pkg through the signed bootstrap; inbox and active trees bind to the signed release; LaunchAgent survives client close and upgrade")
				c.notRun("a controlled clean-host installation/upgrade driver with pkg inbox, active-tree, LaunchAgent and crash/rerun observations is not implemented in this harness")
			})
			t.Run("helper_keychain", func(t *testing.T) {
				c := beginCase(t, "MAC."+arch+".helper_keychain", "QUALIFICATION",
					"signed Runner launches the helper with IDs only; Keychain owner/master ACL works across upgrade; cancel, unknown acknowledgement, crash and rerun retain one challenge and no secret in public data")
				c.notRun("a signed installed Runner/helper and controlled GUI/Keychain crash-recovery driver with a disposable credential are required")
			})
			t.Run("serenity_provider_first_chat", func(t *testing.T) {
				c := beginCase(t, "MAC."+arch+".serenity_provider_first_chat", "QUALIFICATION",
					"real provider first chief reply is committed and displayed; Serenity public protocol, one writer per brain, scoped recall, command reconciliation, cost/disclosure bounds and backup revisions are observed")
				c.notRun("a real provider account, qualified Serenity service and native UI/controller evidence driver are required; a simulated reply or screenshot cannot prove this gate")
			})
		})
	}
}

func qualifyNativeMacArtifacts(t *testing.T, arch string) {
	c := beginCase(t, "MAC."+arch+".signed_artifacts", "QUALIFICATION",
		"native architecture matches", "pkg and bootstrap pass Gatekeeper and stapled notarization", "pkg, bootstrap and helper carry the pinned Developer ID Team ID", "helper and bootstrap contain native slices")
	if runtime.GOOS != "darwin" {
		c.notRun("requires a native macOS host")
	}
	hardware, err := nativeMacHardwareArch()
	if err != nil {
		c.notRun("native hardware architecture unavailable: %v", err)
	}
	if hardware != arch || runtime.GOARCH != arch {
		c.notRun("requires native %s hardware and process; observed hardware=%s process=%s", arch, hardware, runtime.GOARCH)
	}
	c.version("macos", nativeMacVersion())
	c.version("hardware_arch", hardware)
	team := os.Getenv("ZATITI_MAC_TEAM_ID")
	pkg := os.Getenv("ZATITI_MAC_PKG")
	app := os.Getenv("ZATITI_MAC_BOOTSTRAP_APP")
	helper := os.Getenv("ZATITI_MAC_HELPER")
	if team == "" || pkg == "" || app == "" || helper == "" {
		c.notRun("set ZATITI_MAC_TEAM_ID, ZATITI_MAC_PKG, ZATITI_MAC_BOOTSTRAP_APP and ZATITI_MAC_HELPER to final signed artifact inputs")
	}
	if !plainTeamID(team) {
		c.fail("Team ID input is malformed")
	}
	for label, path := range map[string]string{"pkg": pkg, "bootstrap": app, "helper": helper} {
		if err := safeReleaseArtifact(path, label == "bootstrap"); err != nil {
			c.fail("%s artifact: %v", label, err)
		}
		if label != "bootstrap" {
			c.attach(label+"_sha256", fileDigest(path))
		}
	}
	if !strings.Contains(filepath.Base(pkg), "-darwin-"+arch+"-installer.pkg") {
		c.fail("pkg filename does not bind native architecture")
	}
	c.attach("bootstrap_executable_sha256", fileDigest(filepath.Join(app, "Contents", "MacOS", "zatiti-bootstrap")))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	checks := []struct {
		name string
		args []string
	}{
		{"pkg signature", []string{"/usr/sbin/pkgutil", "--check-signature", pkg}},
		{"pkg Gatekeeper", []string{"/usr/sbin/spctl", "--assess", "--type", "install", "--verbose=2", pkg}},
		{"pkg staple", []string{"/usr/bin/xcrun", "stapler", "validate", pkg}},
		{"bootstrap signature", []string{"/usr/bin/codesign", "--verify", "--deep", "--strict", "--verbose=2", app}},
		{"bootstrap Gatekeeper", []string{"/usr/sbin/spctl", "--assess", "--type", "execute", "--verbose=2", app}},
		{"bootstrap staple", []string{"/usr/bin/xcrun", "stapler", "validate", app}},
		{"helper signature", []string{"/usr/bin/codesign", "--verify", "--strict", "--verbose=2", helper}},
	}
	for _, check := range checks {
		if _, err := boundedProbe(ctx, check.args...); err != nil {
			c.fail("%s: %v", check.name, err)
		}
		c.observe("%s passed", check.name)
	}
	pkgSignature, err := boundedProbe(ctx, "/usr/sbin/pkgutil", "--check-signature", pkg)
	if err != nil || !strings.Contains(pkgSignature, team) {
		c.fail("pkg signature does not establish pinned Team ID")
	}
	for label, path := range map[string]string{"bootstrap": app, "helper": helper} {
		out, err := boundedProbe(ctx, "/usr/bin/codesign", "-dv", "--verbose=4", path)
		if err != nil || !strings.Contains(out, "TeamIdentifier="+team) {
			c.fail("%s signature does not establish pinned Team ID", label)
		}
		binary := path
		if label == "bootstrap" {
			binary = filepath.Join(path, "Contents", "MacOS", "zatiti-bootstrap")
		}
		slices, err := boundedProbe(ctx, "/usr/bin/lipo", "-archs", binary)
		if err != nil || !strings.Contains(" "+slices+" ", " "+arch+" ") {
			c.fail("%s lacks native %s slice", label, arch)
		}
		c.observe("%s Team ID and %s slice matched", label, arch)
	}
	// This is a read-only artifact check. Its passing status cannot qualify an
	// install, Keychain ACL, native first chat, or the whole Mac release.
}

func nativeMacHardwareArch() (string, error) {
	out, err := boundedProbe(context.Background(), "/usr/sbin/sysctl", "-n", "hw.optional.arm64")
	if err == nil && strings.TrimSpace(out) == "1" {
		return "arm64", nil
	}
	// hw.optional.arm64 is absent on older Intel Macs. A Rosetta process
	// still reports 1 above on Apple Silicon, while a native Intel host
	// exposes its CPU vendor through machdep.cpu.vendor.
	vendor, vendorErr := boundedProbe(context.Background(), "/usr/sbin/sysctl", "-n", "machdep.cpu.vendor")
	if vendorErr == nil && strings.TrimSpace(vendor) == "GenuineIntel" {
		return "amd64", nil
	}
	return "", fmt.Errorf("unexpected hardware architecture response")
}

func nativeMacVersion() string {
	out, err := boundedProbe(context.Background(), "/usr/bin/sw_vers", "-productVersion")
	if err != nil {
		return "unavailable"
	}
	return strings.TrimSpace(out)
}

func boundedProbe(ctx context.Context, args ...string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("no command")
	}
	probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, args[0], args[1:]...)
	var output bytes.Buffer
	limited := &boundedProbeWriter{dst: &output, remaining: 64 << 10}
	cmd.Stdout, cmd.Stderr = limited, limited
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s failed: %w", filepath.Base(args[0]), err)
	}
	return output.String(), nil
}

type boundedProbeWriter struct {
	dst       io.Writer
	remaining int
}

func (w *boundedProbeWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, fmt.Errorf("probe output exceeded 64 KiB")
	}
	n, err := w.dst.Write(p)
	w.remaining -= n
	return n, err
}

func safeReleaseArtifact(path string, directory bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("path must be clean and absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		return fmt.Errorf("expected a nonsymlink %s", map[bool]string{true: "directory", false: "file"}[directory])
	}
	return nil
}

func plainTeamID(s string) bool {
	if len(s) != 10 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}
