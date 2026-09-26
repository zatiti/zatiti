package packaging

import (
	"context"
	"path/filepath"
)

// HostedMacControllerService is the fixed first-release LaunchAgent recipe.
// It accepts only the default per-user layout and a hosted Mac controller
// manifest. The selectors name a retained Keychain item, never key bytes.
func HostedMacControllerService(l Layout, m Manifest) (ServiceSpec, error) {
	canonical, err := NewLayout(LayoutInput{OS: "darwin", Home: l.Home, StateDir: filepath.Join(l.Home, "Library", "Application Support", "zatiti")})
	if err != nil {
		return ServiceSpec{}, err
	}
	if l != canonical || m.Target.OS != "darwin" || m.Distribution != DistributionController || m.Serenity != (SerenityPin{}) || m.SecureHelper.Kind != HelperOSKeychain {
		return ServiceSpec{}, errf(CodeInvalidInput, "hosted Mac controller requires the default layout and Keychain manifest")
	}
	if err := m.Validate(); err != nil {
		return ServiceSpec{}, err
	}
	s := ServiceSpec{
		Manager: ManagerLaunchd, Role: RoleController, Label: ControllerLabel,
		Description: "Zatiti controller", Executable: l.ControllerExecutable(m),
		Arguments: []string{"serve", "--credential-backend", "keychain", "--master-key", "secret:master"},
		Owns:      l.StateDir,
	}
	if err := s.validate(); err != nil {
		return ServiceSpec{}, err
	}
	return s, nil
}

// AuditHostedMacRelease checks the descriptor-bound trees and the exact fixed
// LaunchAgent rendering. A production caller still needs the Apple trust
// decision and verified inbox before using this as an activation gate.
func AuditHostedMacRelease(ctx context.Context, home, stateDir string, release MacReleaseDescriptor, arch string) (bool, error) {
	active, err := AuditMacReleaseTrees(ctx, home, stateDir, release, arch)
	if err != nil || !active {
		return active, err
	}
	l, err := NewLayout(LayoutInput{OS: "darwin", Home: home, StateDir: stateDir})
	if err != nil {
		return false, err
	}
	m, err := readDescriptorBoundMacManifest(l, release, arch, DistributionController)
	if err != nil {
		return false, err
	}
	s, err := HostedMacControllerService(l, m)
	if err != nil {
		return false, err
	}
	u, err := Render(s)
	if err != nil {
		return false, err
	}
	if err := AuditMacControllerLauncherBytes(l, u.Content); err != nil {
		return false, err
	}
	return true, nil
}
