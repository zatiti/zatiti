package packaging

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// AuditMacReleaseTrees binds the two active Mac trees to the signed release
// descriptor. It returns false only when neither tree is installed; a partial
// or mismatched installation is an error. This is an audit component, not a
// MacReleaseActivator: exact fixed controller launcher bytes and the Apple
// code-signing decision still need a separately qualified composition.
func AuditMacReleaseTrees(ctx context.Context, home, stateDir string, release MacReleaseDescriptor, arch string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, err := EncodeMacRelease(release); err != nil {
		return false, err
	}
	if !cleanAbs(home) || stateDir != filepath.Join(home, "Library", "Application Support", "zatiti") || (arch != "amd64" && arch != "arm64") {
		return false, errf(CodeInvalidInput, "Mac active audit target is invalid")
	}
	controller, err := NewLayout(LayoutInput{OS: "darwin", Home: home, StateDir: stateDir})
	if err != nil {
		return false, err
	}
	desktop, err := NewDesktopLayout(DesktopLayoutInput{OS: "darwin", Home: home, StateDir: stateDir})
	if err != nil {
		return false, err
	}
	ci, err := Inspect(controller)
	if err != nil {
		return false, err
	}
	di, err := Inspect(desktop)
	if err != nil {
		return false, err
	}
	if ci.Current == "" && di.Current == "" {
		return false, nil
	}
	if ci.Current != release.Version || di.Current != release.Version {
		return false, errf(CodeVerificationFailed, "Mac active controller and desktop versions differ from signed release")
	}
	cm, err := readDescriptorBoundMacManifest(controller, release, arch, DistributionController)
	if err != nil {
		return false, err
	}
	dm, err := readDescriptorBoundMacManifest(desktop, release, arch, DistributionDesktop)
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := AuditInstalled(controller, []string{ControllerLabel}); err != nil {
		return false, err
	}
	if err := AuditDesktopInstalled(desktop); err != nil {
		return false, err
	}
	controllerExe := controller.ControllerExecutable(cm)
	if err := verifyThinMachO(controllerExe, arch); err != nil {
		return false, err
	}
	helper := filepath.Join(controller.VersionDir(release.Version), "bin", "zatiti-credential-helper")
	if err := verifyThinMachO(helper, arch); err != nil {
		return false, err
	}
	if dm.Desktop == nil {
		return false, errf(CodeVerificationFailed, "Mac active desktop manifest lacks bundle")
	}
	appEnd := strings.Index(dm.Desktop.Executable, ".app/")
	if appEnd < 0 {
		return false, errf(CodeVerificationFailed, "Mac active desktop executable path is invalid")
	}
	app := dm.Desktop.Executable[:appEnd+len(".app")]
	launcher := desktopLauncherPath(desktop, dm)
	want := filepath.Join(desktop.CurrentBundle, filepath.FromSlash(app))
	if got, err := os.Readlink(launcher); err != nil || got != want {
		return false, errf(CodeVerificationFailed, "Mac desktop launcher differs from signed bundle")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return true, nil
}

func readDescriptorBoundMacManifest(l Layout, release MacReleaseDescriptor, arch, distribution string) (Manifest, error) {
	var component *MacReleaseComponent
	for i := range release.Components {
		c := &release.Components[i]
		if c.Arch == arch && c.Distribution == distribution {
			component = c
			break
		}
	}
	if component == nil {
		return Manifest{}, errf(CodeVerificationFailed, "Mac release descriptor lacks native component")
	}
	raw, err := readSmallFile(filepath.Join(l.VersionDir(release.Version), ManifestFileName))
	if err != nil {
		return Manifest{}, errWrap(CodeVerificationFailed, "Mac active release manifest is unavailable", err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != component.ManifestSHA256 {
		return Manifest{}, errf(CodeVerificationFailed, "Mac active manifest differs from signed descriptor")
	}
	m, err := Decode(raw)
	if err != nil {
		return Manifest{}, err
	}
	if m.Version != release.Version || m.Target.OS != "darwin" || m.Target.Arch != arch || m.Distribution != distribution || m.SourceRevision != release.SourceRevision || m.ContractRevision != release.ContractRevision || m.Protocols != release.Protocols {
		return Manifest{}, errf(CodeVerificationFailed, "Mac active manifest identity differs from signed descriptor")
	}
	return m, nil
}

// AuditMacControllerLauncherBytes verifies the protected LaunchAgent exactly
// against independently rendered trusted bytes. The caller must not derive
// expected bytes from the installed file or mutable package receipt.
func AuditMacControllerLauncherBytes(l Layout, expected []byte) error {
	if l.OS != "darwin" || l.Manager != ManagerLaunchd || len(expected) == 0 || len(expected) > 1<<20 {
		return errf(CodeInvalidInput, "Mac controller launcher audit inputs are invalid")
	}
	if err := l.validate(); err != nil {
		return err
	}
	p := filepath.Join(l.UnitDir, unitFileName(l.Manager, ControllerLabel))
	if err := refuseSymlinkedParents(l, p); err != nil {
		return err
	}
	fd, err := syscall.Open(p, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return errWrap(CodeVerificationFailed, "Mac controller launcher cannot be opened safely", err)
	}
	f := os.NewFile(uintptr(fd), p)
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !acceptedOwned(info) || info.Mode().Perm() != 0o644 || info.Size() != int64(len(expected)) {
		return errf(CodeVerificationFailed, "Mac controller launcher is missing or unsafe")
	}
	got, err := io.ReadAll(io.LimitReader(f, int64(len(expected))+1))
	if err != nil || !bytes.Equal(got, expected) {
		return errf(CodeVerificationFailed, "Mac controller launcher bytes differ from trusted rendering")
	}
	return nil
}
