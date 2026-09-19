package packaging

import (
	"path/filepath"
	"testing"
)

const (
	testFlutterVersion = "3.35.0"
	testDartVersion    = "3.9.0"
	testSecurePlugin   = "flutter_secure_storage"
)

var (
	linuxRunnerDeps  = []NativeDependency{{Name: "libgtk-3.so.0", Kind: NativeSharedLibrary}, {Name: "libsecret-1.so.0", Kind: NativeSharedLibrary}}
	darwinRunnerDeps = []NativeDependency{{Name: "AppKit", Kind: NativeSystemFramework}, {Name: "Security", Kind: NativeSystemFramework}}
)

// newDesktopFixture stages a synthetic desktop distribution. The bundle
// archive is synthetic bytes: the manifest rules under test do not open it.
func newDesktopFixture(t *testing.T, version, goos string) fixture {
	t.Helper()
	root := t.TempDir()
	bundle, executable, deps := "desktop/zatiti_desktop-linux.tar.gz", "zatiti_desktop", linuxRunnerDeps
	if goos == "darwin" {
		bundle, executable, deps = "desktop/zatiti_desktop-macos.tar.gz", "Zatiti.app/Contents/MacOS/zatiti_desktop", darwinRunnerDeps
	}
	writeFile(t, filepath.Join(root, filepath.FromSlash(bundle)), []byte("synthetic bundle archive "+version), 0o644)
	writeFile(t, filepath.Join(root, "LICENSE"), repositoryLicense(t), 0o644)
	for _, notice := range []string{"flutter", "dart", testSecurePlugin} {
		writeFile(t, filepath.Join(root, "licenses", notice+".NOTICE"), []byte("synthetic notice for "+notice), 0o644)
	}
	sbom := `{"spdxVersion":"SPDX-2.3","packages":[` +
		`{"name":"` + ProjectComponent + `","versionInfo":"` + version + `"},` +
		`{"name":"flutter","versionInfo":"` + testFlutterVersion + `"},` +
		`{"name":"dart","versionInfo":"` + testDartVersion + `"},` +
		`{"name":"` + PluginComponent(testSecurePlugin) + `","versionInfo":"10.3.4"}]}`
	writeFile(t, filepath.Join(root, "sbom.spdx.json"), []byte(sbom), 0o644)
	return fixture{root: root, input: BuildInput{
		Root:           root,
		Distribution:   DistributionDesktop,
		Version:        version,
		Target:         Target{OS: goos, Arch: "arm64"},
		SourceRevision: testRevision,
		Toolchain:      "go1.26.2",
		Kinds: map[string]string{
			bundle:                    KindDesktopBundle,
			"LICENSE":                 KindLicense,
			"licenses/flutter.NOTICE": KindNotice,
			"licenses/dart.NOTICE":    KindNotice,
			"licenses/" + testSecurePlugin + ".NOTICE": KindNotice,
			"sbom.spdx.json": KindSBOM,
		},
		Licenses: []LicenseNotice{
			{Component: ProjectComponent, Version: version, SPDX: ProjectLicense, Notice: LicensePath},
			{Component: FlutterComponent, Version: testFlutterVersion, SPDX: "BSD-3-Clause", Notice: "licenses/flutter.NOTICE"},
			{Component: DartComponent, Version: testDartVersion, SPDX: "BSD-3-Clause", Notice: "licenses/dart.NOTICE"},
			{Component: PluginComponent(testSecurePlugin), Version: "10.3.4", SPDX: "BSD-3-Clause", Notice: "licenses/" + testSecurePlugin + ".NOTICE"},
		},
		SBOM: SBOMRef{Format: SBOMFormatSPDX, Path: "sbom.spdx.json"},
		Desktop: &DesktopClient{
			Framework: DesktopFrameworkFlutter, FlutterVersion: testFlutterVersion, DartVersion: testDartVersion,
			Bundle: bundle, BundleFormat: BundleFormatTarGz, Executable: executable,
			Plugins:            []DesktopPlugin{{Name: testSecurePlugin, Version: "10.3.4", Role: PluginRoleSecureStorage}},
			NativeDependencies: deps,
		},
	}}
}

func TestDesktopDistributionBuilds(t *testing.T) {
	t.Parallel()
	for _, goos := range []string{"darwin", "linux"} {
		f := newDesktopFixture(t, "1.0.0", goos)
		m := f.build(t)
		if m.Distribution != DistributionDesktop || m.Desktop == nil || m.Serenity != (SerenityPin{}) {
			t.Fatalf("%s desktop manifest: %+v", goos, m)
		}
		if err := VerifyTree(m, f.root); err != nil {
			t.Fatalf("%s VerifyTree: %v", goos, err)
		}
	}
}

func TestDesktopDistributionRejections(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		goos   string
		mutate func(t *testing.T, f *fixture)
		code   string
	}{
		{"no desktop section", "linux", func(_ *testing.T, f *fixture) { f.input.Desktop = nil }, CodeInvalidInput},
		{"framework the contract retired", "linux", func(_ *testing.T, f *fixture) { f.input.Desktop.Framework = "fyne" }, CodeCapabilityUnsupported},
		{"unpinned flutter", "linux", func(_ *testing.T, f *fixture) { f.input.Desktop.FlutterVersion = "" }, CodeInvalidInput},
		{"flutter license entry at another version", "linux", func(_ *testing.T, f *fixture) { f.input.Licenses[1].Version = "3.0.0" }, CodeInvalidInput},
		{"dart license entry missing", "linux", func(_ *testing.T, f *fixture) {
			f.input.Licenses = append(f.input.Licenses[:2], f.input.Licenses[3:]...)
		}, CodeInvalidInput},
		{"plugin without license entry", "linux", func(_ *testing.T, f *fixture) {
			f.input.Desktop.Plugins = append(f.input.Desktop.Plugins, DesktopPlugin{Name: "path_provider", Version: "2.1.0"})
		}, CodeInvalidInput},
		{"plugin listed but unpinned in licenses", "linux", func(_ *testing.T, f *fixture) { f.input.Licenses[3].Version = "9.0.0" }, CodeInvalidInput},
		{"no secure storage plugin", "linux", func(_ *testing.T, f *fixture) { f.input.Desktop.Plugins[0].Role = "" }, CodeInvalidInput},
		{"unknown plugin role", "linux", func(_ *testing.T, f *fixture) { f.input.Desktop.Plugins[0].Role = "database" }, CodeInvalidInput},
		{"bundle format", "linux", func(_ *testing.T, f *fixture) { f.input.Desktop.BundleFormat = "zip" }, CodeCapabilityUnsupported},
		{"bundle names another file", "linux", func(_ *testing.T, f *fixture) { f.input.Desktop.Bundle = "LICENSE" }, CodeInvalidInput},
		{"two bundles", "linux", func(t *testing.T, f *fixture) {
			writeFile(t, filepath.Join(f.root, "desktop", "second.tar.gz"), []byte("x"), 0o644)
			f.input.Kinds["desktop/second.tar.gz"] = KindDesktopBundle
		}, CodeInvalidInput},
		{"executable escapes the bundle", "linux", func(_ *testing.T, f *fixture) { f.input.Desktop.Executable = "../zatiti_desktop" }, CodeInvalidInput},
		{"macos executable outside the app bundle", "darwin", func(_ *testing.T, f *fixture) { f.input.Desktop.Executable = "zatiti_desktop" }, CodeInvalidInput},
		{"linux runner without gtk", "linux", func(_ *testing.T, f *fixture) { f.input.Desktop.NativeDependencies = linuxRunnerDeps[1:] }, CodeInvalidInput},
		{"linux runner without libsecret", "linux", func(_ *testing.T, f *fixture) { f.input.Desktop.NativeDependencies = linuxRunnerDeps[:1] }, CodeInvalidInput},
		{"darwin runner without frameworks", "darwin", func(_ *testing.T, f *fixture) { f.input.Desktop.NativeDependencies = nil }, CodeInvalidInput},
		{"framework kind on linux", "linux", func(_ *testing.T, f *fixture) {
			f.input.Desktop.NativeDependencies = append(f.input.Desktop.NativeDependencies, NativeDependency{Name: "libsecret-2", Kind: NativeSystemFramework})
		}, CodeInvalidInput},
		{"unsorted dependencies", "linux", func(_ *testing.T, f *fixture) {
			f.input.Desktop.NativeDependencies = []NativeDependency{linuxRunnerDeps[1], linuxRunnerDeps[0]}
		}, CodeInvalidInput},
		{"controller binary in the desktop tree", "linux", func(t *testing.T, f *fixture) {
			writeFile(t, filepath.Join(f.root, "zatiti"), []byte("x"), 0o755)
			f.input.Kinds["zatiti"] = KindControllerBinary
		}, CodeInvalidInput},
		{"serenity pin in the desktop tree", "linux", func(_ *testing.T, f *fixture) { f.input.Serenity.Source = "x" }, CodeInvalidInput},
		{"secure helper in the desktop tree", "linux", func(_ *testing.T, f *fixture) {
			f.input.SecureHelper = SecureHelper{Kind: HelperHeadlessMasterKey}
		}, CodeInvalidInput},
		{"database file in the desktop tree", "linux", func(t *testing.T, f *fixture) {
			writeFile(t, filepath.Join(f.root, "desktop", "cache.sqlite"), []byte("x"), 0o644)
			f.input.Kinds["desktop/cache.sqlite"] = KindDocumentation
		}, CodeInvalidInput},
		{"key material in the desktop tree", "linux", func(t *testing.T, f *fixture) {
			writeFile(t, filepath.Join(f.root, "desktop", "client.pem"), []byte("x"), 0o644)
			f.input.Kinds["desktop/client.pem"] = KindDocumentation
		}, CodeInvalidInput},
		{"database driver in the sbom", "linux", func(t *testing.T, f *fixture) {
			f.input.Licenses = append(f.input.Licenses, LicenseNotice{Component: "sqflite", Version: "2.3.0", SPDX: "MIT", Notice: "licenses/dart.NOTICE"})
			writeFile(t, filepath.Join(f.root, "sbom.spdx.json"), []byte(`{"spdxVersion":"SPDX-2.3","packages":[`+
				`{"name":"`+ProjectComponent+`","versionInfo":"1.0.0"},{"name":"flutter","versionInfo":"`+testFlutterVersion+`"},`+
				`{"name":"dart","versionInfo":"`+testDartVersion+`"},{"name":"`+PluginComponent(testSecurePlugin)+`","versionInfo":"10.3.4"},`+
				`{"name":"sqflite","versionInfo":"2.3.0"}]}`), 0o644)
		}, CodeVerificationFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newDesktopFixture(t, "1.0.0", tc.goos)
			tc.mutate(t, &f)
			_, err := Build(f.input)
			wantCode(t, err, tc.code)
		})
	}
}

// The controller installer never installs a desktop distribution.
func TestDesktopDistributionIsNotAControllerInstall(t *testing.T) {
	t.Parallel()
	l := testLayout(t, "linux")
	f := newDesktopFixture(t, "1.0.0", "linux")
	m := f.build(t)
	_, err := PlanInstallation(InstallInput{Layout: l, Manifest: m, SourceRoot: f.root, HostArch: "arm64"})
	wantCode(t, err, CodeCapabilityUnsupported)
}
