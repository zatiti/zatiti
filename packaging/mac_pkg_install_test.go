package packaging

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type macPkgCommandCapture struct {
	called int
	name   string
	args   []string
}

func (c *macPkgCommandCapture) Run(_ context.Context, name string, args []string) error {
	c.called++
	c.name = name
	c.args = append([]string(nil), args...)
	return nil
}

func TestFixedMacPkgInstallerRehashesAndUsesOnlyCurrentUserHome(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("native Mac system installer command requires macOS")
	}
	_, _, plan, _, _ := macPkgBindingFixture(t)
	dir := canonicalTempDir(t)
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, plan.Installer.Filename)
	payload := []byte("synthetic package bytes")
	writeFile(t, path, payload, 0o600)
	sum := sha256.Sum256(payload)
	plan.Installer.Size, plan.Installer.SHA256 = int64(len(payload)), hex.EncodeToString(sum[:])
	runner := &macPkgCommandCapture{}
	if err := runFixedMacPkgInstaller(context.Background(), path, plan.Installer, runner); err != nil {
		t.Fatal(err)
	}
	if runner.called != 1 || runner.name != "/usr/sbin/installer" || len(runner.args) != 4 || runner.args[0] != "-pkg" || runner.args[1] != path || runner.args[2] != "-target" || runner.args[3] != "CurrentUserHomeDirectory" {
		t.Fatalf("unexpected system installer invocation: %+v", runner)
	}
	writeFile(t, path, []byte("changed after verification"), 0o600)
	if err := runFixedMacPkgInstaller(context.Background(), path, plan.Installer, runner); Code(err) != CodeVerificationFailed || runner.called != 1 {
		t.Fatalf("changed package reached installer: %v, calls=%d", err, runner.called)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "other.pkg"), path); err != nil {
		t.Fatal(err)
	}
	if err := runFixedMacPkgInstaller(context.Background(), path, plan.Installer, runner); Code(err) != CodeVerificationFailed || runner.called != 1 {
		t.Fatalf("symlinked package reached installer: %v, calls=%d", err, runner.called)
	}
}
