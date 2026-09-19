package packaging

// This file holds everything that is specific to packaging the desktop
// client. The controller, service, helper and Serenity lifecycle do not
// depend on it beyond the validateDesktop call in Manifest.Validate, the
// desktopSBOMFindings call in auditSBOM, and the Desktop field on Manifest.
//
// Contract revision 2 (ADR 002) makes the desktop a Flutter application at
// apps/desktop, built once per target with `flutter build macos` or
// `flutter build linux` in release mode and packaged separately from the Go
// controller binary. So the desktop distribution is its own manifest: it
// carries the application bundle as one archive artifact, the Flutter and
// Dart SDK versions, the plugin versions with their license notices, and the
// native libraries the platform runner loads from the host. It carries no
// controller binary, no Serenity pin, no secure helper, no controller state,
// no database driver and no credential.

import (
	"path"
	"strings"
)

// DesktopFrameworkFlutter is the only desktop framework contract revision 2
// selects.
const DesktopFrameworkFlutter = "flutter"

// Native dependency kinds.
const (
	NativeSharedLibrary   = "shared_library"
	NativeSystemFramework = "system_framework"
)

// Plugin roles. A role names what the contract requires of a plugin; the
// plugin's name is whatever the pinned pubspec.lock says.
const (
	PluginRoleSecureStorage = "secure_storage"
)

// Bundle formats. The application bundle is one archive artifact because a
// macOS .app holds symlinks (framework version links) that a flat
// distribution tree refuses.
const (
	BundleFormatTarGz = "tar.gz"
)

// DesktopClient describes the packaged Flutter desktop client.
type DesktopClient struct {
	Framework      string `json:"framework"`
	FlutterVersion string `json:"flutter_version"`
	DartVersion    string `json:"dart_version"`
	// Bundle is the desktop_bundle artifact holding the application bundle.
	Bundle       string `json:"bundle"`
	BundleFormat string `json:"bundle_format"`
	// Executable is the path of the runner inside the bundle, for example
	// Zatiti.app/Contents/MacOS/zatiti_desktop or zatiti_desktop.
	Executable         string             `json:"executable"`
	Plugins            []DesktopPlugin    `json:"plugins"`
	NativeDependencies []NativeDependency `json:"native_dependencies"`
}

// DesktopPlugin is one pinned Flutter plugin. Role is empty for a plugin
// with no contract-level role.
type DesktopPlugin struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Role    string `json:"role,omitempty"`
}

// NativeDependency is one library or framework the desktop runner loads from
// the host rather than from the bundle.
type NativeDependency struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// PluginComponent is the license entry component name for a pub.dev plugin.
func PluginComponent(name string) string { return "pub.dev/packages/" + name }

// License entry components for the SDKs.
const (
	FlutterComponent = "flutter"
	DartComponent    = "dart"
)

// Names the Flutter Linux runner needs from the host, as the contract states
// them: GTK and the Secret Service client library.
var linuxRunnerLibraries = []string{"libgtk-3", "libsecret-1"}

// stateExtensions are file name suffixes that mark controller state or key
// material. None may appear in a desktop distribution.
var stateExtensions = []string{".db", ".sqlite", ".sqlite3", ".db-wal", ".db-shm", ".sock", ".key", ".pem", ".p12", ".pfx"}

// databaseDrivers are SBOM component names that would give the desktop
// client a way to open the controller database. This is a named list, not
// detection of every possible driver.
var databaseDrivers = map[string]bool{
	"modernc.org/sqlite": true, "sqlite3": true, "sqlite3_flutter_libs": true,
	"sqflite": true, "sqflite_common_ffi": true, "drift": true,
}

// validateDesktop applies the rules of the desktop distribution.
func (m Manifest) validateDesktop(index map[string]Artifact) error {
	var bundles []string
	for _, a := range m.Artifacts {
		switch a.Kind {
		case KindDesktopBundle:
			bundles = append(bundles, a.Path)
		case KindControllerBinary, KindSerenityRuntime, KindSerenityReadFacade:
			return errf(CodeInvalidInput, "the desktop distribution carries no controller or Serenity binary; they are packaged separately")
		}
		for _, ext := range stateExtensions {
			if strings.HasSuffix(strings.ToLower(path.Base(a.Path)), ext) {
				return errf(CodeInvalidInput, "artifact %s looks like controller state or key material, which the desktop bundle never carries", a.Path)
			}
		}
	}
	if m.Serenity != (SerenityPin{}) || m.SecureHelper != (SecureHelper{}) {
		return errf(CodeInvalidInput, "the desktop distribution carries no Serenity pin or secure helper; the client uses its own secure-storage plugin")
	}
	d := m.Desktop
	if d == nil {
		return errf(CodeInvalidInput, "the desktop distribution needs a desktop section")
	}
	if d.Framework != DesktopFrameworkFlutter {
		return errf(CodeCapabilityUnsupported, "desktop framework %s is not selected by contract revision %d", printable(d.Framework), ContractRevision)
	}
	if !tokenPattern.MatchString(d.FlutterVersion) || !tokenPattern.MatchString(d.DartVersion) {
		return errf(CodeInvalidInput, "the desktop section must pin the Flutter and Dart SDK versions")
	}
	if len(bundles) != 1 || bundles[0] != d.Bundle {
		return errf(CodeInvalidInput, "the desktop section must reference the single desktop_bundle file in the tree")
	}
	if d.BundleFormat != BundleFormatTarGz {
		return errf(CodeCapabilityUnsupported, "bundle format %s is not supported", printable(d.BundleFormat))
	}
	if err := validateRelPath(d.Executable); err != nil {
		return errf(CodeInvalidInput, "the desktop executable must be a clean relative path inside the bundle")
	}
	if m.Target.OS == "darwin" && !strings.Contains(d.Executable, ".app/Contents/MacOS/") {
		return errf(CodeInvalidInput, "a macOS desktop executable lives inside the application bundle's Contents/MacOS directory")
	}
	if err := m.validateDesktopLicenses(d); err != nil {
		return err
	}
	return m.validateNativeDependencies(d)
}

// validateDesktopLicenses requires a license entry for the Flutter SDK, the
// Dart SDK and every plugin, at the pinned versions, and exactly one plugin
// in the secure-storage role.
func (m Manifest) validateDesktopLicenses(d *DesktopClient) error {
	licensed := make(map[string]string, len(m.Licenses))
	for _, l := range m.Licenses {
		licensed[l.Component] = l.Version
	}
	for _, sdk := range []struct{ component, version string }{{FlutterComponent, d.FlutterVersion}, {DartComponent, d.DartVersion}} {
		if licensed[sdk.component] != sdk.version {
			return errf(CodeInvalidInput, "the %s SDK needs a license entry at the pinned version", sdk.component)
		}
	}
	secure := 0
	for i, p := range d.Plugins {
		if !tokenPattern.MatchString(p.Name) || !tokenPattern.MatchString(p.Version) {
			return errf(CodeInvalidInput, "plugin entry %d must carry a name and version", i)
		}
		if i > 0 && d.Plugins[i-1].Name >= p.Name {
			return errf(CodeInvalidInput, "plugins must be unique and sorted by name")
		}
		switch p.Role {
		case "":
		case PluginRoleSecureStorage:
			secure++
		default:
			return errf(CodeInvalidInput, "plugin %s has an unknown role", p.Name)
		}
		if licensed[PluginComponent(p.Name)] != p.Version {
			return errf(CodeInvalidInput, "plugin %s needs a license entry at its pinned version", p.Name)
		}
	}
	if secure != 1 {
		return errf(CodeInvalidInput, "the desktop client pins exactly one operating-system secure-storage plugin, found %d", secure)
	}
	return nil
}

func (m Manifest) validateNativeDependencies(d *DesktopClient) error {
	if len(d.NativeDependencies) == 0 {
		return errf(CodeInvalidInput, "the desktop section must declare the native libraries or frameworks the runner loads from the host")
	}
	for i, dep := range d.NativeDependencies {
		if !tokenPattern.MatchString(dep.Name) {
			return errf(CodeInvalidInput, "native dependency entry %d must carry a name", i)
		}
		switch {
		case dep.Kind == NativeSharedLibrary && m.Target.OS == "linux":
		case dep.Kind == NativeSystemFramework && m.Target.OS == "darwin":
		default:
			return errf(CodeInvalidInput, "native dependency %s: kind %s does not apply to %s", dep.Name, printable(dep.Kind), m.Target.OS)
		}
		if i > 0 && d.NativeDependencies[i-1].Name >= dep.Name {
			return errf(CodeInvalidInput, "native dependencies must be unique and sorted by name")
		}
	}
	if m.Target.OS == "linux" {
		for _, want := range linuxRunnerLibraries {
			found := false
			for _, dep := range d.NativeDependencies {
				found = found || strings.HasPrefix(dep.Name, want)
			}
			if !found {
				return errf(CodeInvalidInput, "the Linux runner needs %s declared as a native dependency", want)
			}
		}
	}
	return nil
}

// desktopSBOMFindings reports SBOM components a desktop distribution must
// not ship.
func desktopSBOMFindings(m Manifest, components []sbomComponent) []Finding {
	if m.Distribution != DistributionDesktop {
		return nil
	}
	var findings []Finding
	for _, c := range components {
		if databaseDrivers[c.Name] {
			findings = append(findings, Finding{Path: m.SBOM.Path, Problem: "component " + printablePath(c.Name) + " is a database driver, which the desktop client never carries"})
		}
	}
	return findings
}
