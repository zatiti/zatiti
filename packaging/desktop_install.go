package packaging

// Desktop installation: the desktop distribution unpacks into its own
// per-user layout, separate from the controller's, and is reached through a
// freedesktop launcher entry on Linux or an application link on macOS. No
// service is involved: the desktop client is started by the user, and
// closing it never touches the controller.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ManagerNone marks a layout that runs no service: the desktop layout.
const ManagerNone = "none"

// Desktop launcher identity. The application identifier matches
// apps/desktop/linux/CMakeLists.txt; the display name is what the operating
// system shows.
const (
	DesktopApplicationID = "dev.zatiti.zatiti_desktop"
	DesktopDisplayName   = "Zatiti"

	dirNameDesktopDist = "zatiti-desktop-dist"
	dirNameBundles     = "bundles"
	linkNameBundle     = "current-bundle"
)

// DesktopLayoutInput selects where the desktop client is installed.
type DesktopLayoutInput struct {
	OS   string
	Home string
	// DataHome is the XDG data directory on linux; empty selects the
	// default under Home. Ignored on darwin.
	DataHome string
	// ApplicationsDir overrides where the launcher entry (linux) or the
	// application link (darwin) goes.
	ApplicationsDir string
	DistRoot        string
	// StateDir is the controller's state directory, recorded so the desktop
	// layout provably stays outside it.
	StateDir string
}

// NewDesktopLayout resolves a desktop layout. It reuses Layout: UnitDir is
// the applications directory, Manager is none, and Bundles and CurrentBundle
// hold the unpacked application.
func NewDesktopLayout(in DesktopLayoutInput) (Layout, error) {
	for _, p := range []string{in.Home, in.StateDir} {
		if !cleanAbs(p) {
			return Layout{}, errf(CodeInvalidInput, "the home and state directories must be clean absolute paths")
		}
	}
	for _, p := range []string{in.DataHome, in.ApplicationsDir, in.DistRoot} {
		if p != "" && !cleanAbs(p) {
			return Layout{}, errf(CodeInvalidInput, "layout overrides must be clean absolute paths")
		}
	}
	l := Layout{OS: in.OS, Manager: ManagerNone, Home: in.Home, StateDir: in.StateDir, DistRoot: in.DistRoot, UnitDir: in.ApplicationsDir}
	switch in.OS {
	case "darwin":
		if l.DistRoot == "" {
			l.DistRoot = filepath.Join(in.Home, "Library", "Application Support", dirNameDesktopDist)
		}
		if l.UnitDir == "" {
			l.UnitDir = filepath.Join(in.Home, "Applications")
		}
	case "linux":
		dataHome := in.DataHome
		if dataHome == "" {
			dataHome = filepath.Join(in.Home, ".local", "share")
		}
		if l.DistRoot == "" {
			l.DistRoot = filepath.Join(dataHome, dirNameDesktopDist)
		}
		if l.UnitDir == "" {
			l.UnitDir = filepath.Join(dataHome, "applications")
		}
	default:
		return Layout{}, errf(CodeCapabilityUnsupported, "the desktop client is supported on darwin and linux only")
	}
	l.Versions = filepath.Join(l.DistRoot, dirNameVersions)
	l.Current = filepath.Join(l.DistRoot, linkNameCurrent)
	l.Bundles = filepath.Join(l.DistRoot, dirNameBundles)
	l.CurrentBundle = filepath.Join(l.DistRoot, linkNameBundle)
	if err := l.validate(); err != nil {
		return Layout{}, err
	}
	return l, nil
}

// desktopLauncherPath is the launcher entry (linux) or application link
// (darwin) for a desktop layout.
func desktopLauncherPath(l Layout, m Manifest) string {
	if l.OS == "darwin" {
		app := m.Desktop.Executable[:strings.Index(m.Desktop.Executable, ".app/")+len(".app")]
		return filepath.Join(l.UnitDir, filepath.Base(app))
	}
	return filepath.Join(l.UnitDir, DesktopApplicationID+".desktop")
}

// DesktopInstallInput is everything a desktop install or upgrade depends on.
type DesktopInstallInput struct {
	Layout     Layout
	Manifest   Manifest
	SourceRoot string
	HostArch   string
	Installed  Installed
}

// PlanDesktopInstallation plans a first install or an upgrade of the desktop
// distribution. The release directory and the unpacked bundle are published
// next to the previous ones, then the active links move and the launcher
// entry is written. No service is loaded or unloaded.
func PlanDesktopInstallation(in DesktopInstallInput) (Plan, error) {
	l, m := in.Layout, in.Manifest
	if err := l.validate(); err != nil {
		return Plan{}, err
	}
	if l.Manager != ManagerNone {
		return Plan{}, errf(CodeInvalidInput, "the desktop distribution installs into a desktop layout")
	}
	if err := VerifyTree(m, in.SourceRoot); err != nil {
		return Plan{}, err
	}
	if m.Distribution != DistributionDesktop {
		return Plan{}, errf(CodeCapabilityUnsupported, "this plan installs the desktop distribution; the controller is installed separately")
	}
	if m.Target.OS != l.OS || m.Target.Arch != in.HostArch {
		return Plan{}, errf(CodeCapabilityUnsupported, "the release targets %s/%s, not this host", m.Target.OS, m.Target.Arch)
	}
	if pathWithin(in.SourceRoot, l.DistRoot) || pathWithin(in.SourceRoot, l.StateDir) {
		return Plan{}, errf(CodeInvalidInput, "the source tree must sit outside the distribution and state directories")
	}
	plan, err := planRelease(l, m, in.SourceRoot, in.Installed)
	if err != nil {
		return Plan{}, err
	}
	final := l.VersionDir(m.Version)
	bundlePartial := filepath.Join(l.Bundles, "."+m.Version+".partial")
	bundleFinal := filepath.Join(l.Bundles, m.Version)
	var bundle Artifact
	for _, a := range m.Artifacts {
		if a.Kind == KindDesktopBundle {
			bundle = a
		}
	}
	plan.Prepare = append(plan.Prepare,
		Step{Action: ActionEnsureDir, Path: l.Bundles, Mode: 0o755},
		Step{Action: ActionRemoveTree, Path: bundleFinal},
		Step{Action: ActionRemoveTree, Path: bundlePartial},
		Step{
			Action: ActionExtractBundle,
			Path:   bundlePartial,
			Source: filepath.Join(final, filepath.FromSlash(bundle.Path)),
			SHA256: bundle.SHA256, Size: bundle.Size,
			Target:  bundleFinal,
			Content: []byte(m.Desktop.Executable),
		},
	)
	plan.Steps = append(plan.Steps,
		Step{Action: ActionSwapLink, Path: l.Current, Target: filepath.Join(dirNameVersions, m.Version)},
		Step{Action: ActionSwapLink, Path: l.CurrentBundle, Target: filepath.Join(dirNameBundles, m.Version)},
		Step{Action: ActionEnsureDir, Path: l.UnitDir, Mode: 0o755},
	)
	launcher := desktopLauncherPath(l, m)
	if l.OS == "darwin" {
		app := m.Desktop.Executable[:strings.Index(m.Desktop.Executable, ".app/")+len(".app")]
		plan.Steps = append(plan.Steps, Step{Action: ActionSwapLink, Path: launcher, Target: filepath.Join(l.CurrentBundle, filepath.FromSlash(app))})
	} else {
		entry, err := RenderDesktopEntry(DesktopEntry{
			Name:    DesktopDisplayName,
			Comment: "Zatiti workspace",
			Exec:    filepath.Join(l.CurrentBundle, filepath.FromSlash(m.Desktop.Executable)),
		})
		if err != nil {
			return Plan{}, err
		}
		plan.Steps = append(plan.Steps, Step{Action: ActionWriteFile, Path: launcher, Content: entry, Mode: 0o644})
	}
	plan.Notices = append(plan.Notices, "The desktop client is not a service; the user starts it, and closing it never stops the controller.")
	return plan, nil
}

// PlanDesktopUninstallation removes the launcher entry or application link
// and the desktop distribution. The desktop keeps no state directory: its
// credential and drafts live in operating-system secure storage, which is
// not touched.
func PlanDesktopUninstallation(l Layout, m Manifest) (Plan, error) {
	if err := l.validate(); err != nil {
		return Plan{}, err
	}
	if l.Manager != ManagerNone || m.Distribution != DistributionDesktop || m.Desktop == nil {
		return Plan{}, errf(CodeInvalidInput, "desktop uninstallation needs a desktop layout and a desktop manifest")
	}
	plan := Plan{Kind: PlanUninstall, Layout: l}
	plan.Steps = append(plan.Steps,
		Step{Action: ActionRemoveFile, Path: desktopLauncherPath(l, m)},
		Step{Action: ActionRemoveFile, Path: l.CurrentBundle},
		Step{Action: ActionRemoveFile, Path: l.Current},
		Step{Action: ActionRemoveTree, Path: l.DistRoot},
	)
	plan.Notices = append(plan.Notices, "Credentials and drafts in operating-system secure storage are not removed.")
	return plan, nil
}

// AuditDesktopInstalled checks a desktop installation: the release directory
// matches its manifest, the unpacked bundle matches its archive, no entry
// is group or world writable, and the launcher points into the active
// bundle.
func AuditDesktopInstalled(l Layout) error {
	if l.Manager != ManagerNone {
		return errf(CodeInvalidInput, "a desktop audit needs a desktop layout")
	}
	inst, err := Inspect(l)
	if err != nil {
		return err
	}
	if inst.Current == "" {
		return errf(CodeNotFound, "no desktop release is installed in this layout")
	}
	versionDir := l.VersionDir(inst.Current)
	raw, err := readSmallFile(filepath.Join(versionDir, ManifestFileName))
	if err != nil {
		return errWrap(CodeVerificationFailed, "the active desktop release has no manifest", err)
	}
	m, err := Decode(raw)
	if err != nil {
		return err
	}
	var findings []Finding
	collect := func(err error) error {
		var perr *Error
		if err == nil {
			return nil
		}
		if !errors.As(err, &perr) || perr.Code != CodeVerificationFailed {
			return err
		}
		findings = append(findings, perr.Findings...)
		return nil
	}
	if err := collect(VerifyTree(m, versionDir)); err != nil {
		return err
	}
	bundleDir := filepath.Join(l.Bundles, inst.Current)
	if err := collect(VerifyBundle(filepath.Join(versionDir, filepath.FromSlash(m.Desktop.Bundle)), bundleDir)); err != nil {
		return err
	}
	if target, err := os.Readlink(l.CurrentBundle); err != nil || filepath.ToSlash(target) != dirNameBundles+"/"+inst.Current {
		findings = append(findings, Finding{Path: linkNameBundle, Problem: "the active bundle link does not point at the active release"})
	}
	// Unpacked bundles hold their own symlinks and are covered by
	// VerifyBundle, so the writable walk stays out of the bundles tree.
	writable, err := auditWritable(l.DistRoot, map[string]bool{l.Current: true, l.CurrentBundle: true}, l.Bundles)
	if err != nil {
		return err
	}
	findings = append(findings, writable...)
	launcher := desktopLauncherPath(l, m)
	info, err := os.Lstat(launcher)
	switch {
	case err != nil:
		findings = append(findings, Finding{Path: filepath.Base(launcher), Problem: "launcher is missing"})
	case l.OS == "darwin":
		if target, err := os.Readlink(launcher); err != nil || !pathWithin(target, l.CurrentBundle) {
			findings = append(findings, Finding{Path: filepath.Base(launcher), Problem: "application link does not point into the active bundle"})
		}
	case !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0:
		findings = append(findings, Finding{Path: filepath.Base(launcher), Problem: "launcher entry is not a protected regular file"})
	default:
		if raw, err := readSmallFile(launcher); err != nil || !bytes.Contains(raw, []byte("Exec="+filepath.Join(l.CurrentBundle, filepath.FromSlash(m.Desktop.Executable)))) {
			findings = append(findings, Finding{Path: filepath.Base(launcher), Problem: "launcher entry does not start the active bundle"})
		}
	}
	if len(findings) != 0 {
		sortFindings(findings)
		return errFindings("the desktop installation does not pass its audit", findings)
	}
	return nil
}

// DesktopEntry is a freedesktop.org desktop entry for the Linux launcher.
type DesktopEntry struct {
	Name    string
	Comment string
	// Exec is the absolute path of the runner. It is written unquoted, so
	// it is restricted to characters the Exec key takes literally.
	Exec string
	// Icon is an optional absolute path of an icon file.
	Icon string
}

var desktopTextPattern = regexp.MustCompile(`^[A-Za-z0-9 .,()_-]{1,80}$`)

// RenderDesktopEntry renders a desktop entry file. Paths with characters
// that the Exec key would interpret (space, quotes, %, backslash, and the
// reserved field codes) are refused rather than escaped.
func RenderDesktopEntry(e DesktopEntry) ([]byte, error) {
	if !desktopTextPattern.MatchString(e.Name) || !desktopTextPattern.MatchString(e.Comment) {
		return nil, errf(CodeInvalidInput, "desktop entry name and comment must be 1 to 80 plain characters")
	}
	if !systemdPathPattern.MatchString(e.Exec) || filepath.Clean(e.Exec) != e.Exec {
		return nil, errf(CodeInvalidInput, "the desktop entry executable path contains a character the Exec key cannot carry safely")
	}
	if e.Icon != "" && (!systemdPathPattern.MatchString(e.Icon) || filepath.Clean(e.Icon) != e.Icon) {
		return nil, errf(CodeInvalidInput, "the desktop entry icon path contains a character the Icon key cannot carry safely")
	}
	var buf bytes.Buffer
	buf.WriteString("[Desktop Entry]\nType=Application\nVersion=1.5\n")
	buf.WriteString("Name=" + e.Name + "\nComment=" + e.Comment + "\n")
	buf.WriteString("Exec=" + e.Exec + "\n")
	if e.Icon != "" {
		buf.WriteString("Icon=" + e.Icon + "\n")
	}
	buf.WriteString("Terminal=false\nCategories=Office;Utility;\nStartupNotify=true\n")
	return buf.Bytes(), nil
}
