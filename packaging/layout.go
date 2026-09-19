package packaging

import (
	"path/filepath"
)

// LayoutInput selects where a per-user installation lives. Every path is
// supplied by the caller; this package reads no environment variable.
type LayoutInput struct {
	OS   string // darwin | linux
	Home string
	// DataHome and ConfigHome are the XDG base directories on linux. Empty
	// selects the XDG defaults under Home. Ignored on darwin.
	DataHome   string
	ConfigHome string
	// DistRoot overrides the default distribution directory.
	DistRoot string
	// StateDir is the installation state directory. Packaging never writes
	// inside it; it is recorded so that every plan can prove it stays
	// outside.
	StateDir string
}

// Layout is the resolved set of directories an installation uses. The
// distribution and the state directory are disjoint trees, which is what lets
// upgrade and uninstall leave state alone.
type Layout struct {
	OS       string
	Manager  string
	Home     string
	DistRoot string
	// Versions holds one directory per installed release.
	Versions string
	// Current is a symlink to the active release directory. Launchers
	// reference binaries through it, so an upgrade does not rewrite them.
	Current  string
	UnitDir  string
	StateDir string
}

const (
	dirNameDist     = "zatiti-dist"
	dirNameVersions = "versions"
	linkNameCurrent = "current"
)

// NewLayout resolves and validates an installation layout.
func NewLayout(in LayoutInput) (Layout, error) {
	for _, p := range []string{in.Home, in.StateDir} {
		if !cleanAbs(p) {
			return Layout{}, errf(CodeInvalidInput, "the home and state directories must be clean absolute paths")
		}
	}
	for _, p := range []string{in.DataHome, in.ConfigHome, in.DistRoot} {
		if p != "" && !cleanAbs(p) {
			return Layout{}, errf(CodeInvalidInput, "layout overrides must be clean absolute paths")
		}
	}
	l := Layout{OS: in.OS, Home: in.Home, StateDir: in.StateDir, DistRoot: in.DistRoot}
	switch in.OS {
	case "darwin":
		l.Manager = ManagerLaunchd
		if l.DistRoot == "" {
			l.DistRoot = filepath.Join(in.Home, "Library", "Application Support", dirNameDist)
		}
		l.UnitDir = filepath.Join(in.Home, "Library", "LaunchAgents")
	case "linux":
		l.Manager = ManagerSystemdUser
		dataHome, configHome := in.DataHome, in.ConfigHome
		if dataHome == "" {
			dataHome = filepath.Join(in.Home, ".local", "share")
		}
		if configHome == "" {
			configHome = filepath.Join(in.Home, ".config")
		}
		if l.DistRoot == "" {
			l.DistRoot = filepath.Join(dataHome, dirNameDist)
		}
		l.UnitDir = filepath.Join(configHome, "systemd", "user")
	default:
		return Layout{}, errf(CodeCapabilityUnsupported, "installation is supported on darwin and linux only")
	}
	l.Versions = filepath.Join(l.DistRoot, dirNameVersions)
	l.Current = filepath.Join(l.DistRoot, linkNameCurrent)
	if err := l.validate(); err != nil {
		return Layout{}, err
	}
	return l, nil
}

// validate re-establishes the layout invariants. Apply calls it again, so a
// hand-built Layout gets no weaker treatment than one from NewLayout.
func (l Layout) validate() error {
	for _, p := range []string{l.Home, l.DistRoot, l.Versions, l.Current, l.UnitDir, l.StateDir} {
		if !cleanAbs(p) {
			return errf(CodeInvalidInput, "layout paths must be clean absolute paths")
		}
	}
	if l.Manager != ManagerLaunchd && l.Manager != ManagerSystemdUser {
		return errf(CodeCapabilityUnsupported, "the layout names no supported service manager")
	}
	if l.Versions != filepath.Join(l.DistRoot, dirNameVersions) || l.Current != filepath.Join(l.DistRoot, linkNameCurrent) {
		return errf(CodeInvalidInput, "the release directories must sit directly under the distribution directory")
	}
	for _, p := range []string{l.DistRoot, l.StateDir} {
		if isShallow(p) || pathWithin(l.Home, p) {
			return errf(CodeInvalidInput, "the distribution and state directories must be dedicated directories, not the home directory or an ancestor of it")
		}
	}
	for _, other := range []string{l.DistRoot, l.UnitDir} {
		if pathWithin(l.StateDir, other) || pathWithin(other, l.StateDir) {
			return errf(CodeConflict, "the state directory must be disjoint from the distribution and launcher directories")
		}
	}
	if pathWithin(l.UnitDir, l.DistRoot) || pathWithin(l.DistRoot, l.UnitDir) {
		return errf(CodeConflict, "the launcher directory must be disjoint from the distribution directory")
	}
	return nil
}

// VersionDir is the directory of one installed release.
func (l Layout) VersionDir(version string) string { return filepath.Join(l.Versions, version) }

// ControllerExecutable is the launcher-stable path of the controller binary.
func (l Layout) ControllerExecutable(m Manifest) string {
	for _, a := range m.Artifacts {
		if a.Kind == KindControllerBinary {
			return filepath.Join(l.Current, filepath.FromSlash(a.Path))
		}
	}
	return ""
}

func cleanAbs(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && !hasControl(p)
}

// isShallow reports a path with fewer than two components below the root,
// such as "/" or "/home". No plan may remove or own such a directory.
func isShallow(p string) bool {
	depth := 0
	for dir := p; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		depth++
	}
	return depth < 2
}
