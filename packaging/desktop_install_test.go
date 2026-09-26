package packaging

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// stageBundle writes a synthetic flutter build output directory shaped like
// the real one: a Linux bundle directory or a macOS .app with the framework
// version symlinks.
func stageBundle(t *testing.T, goos, version string) (dir, executable string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "build")
	if goos == "darwin" {
		app := filepath.Join(dir, "Zatiti.app", "Contents")
		writeFile(t, filepath.Join(app, "MacOS", "zatiti_desktop"), []byte("synthetic runner "+version), 0o755)
		writeFile(t, filepath.Join(app, "Info.plist"), []byte("<plist/>"), 0o644)
		fw := filepath.Join(app, "Frameworks", "FlutterMacOS.framework")
		writeFile(t, filepath.Join(fw, "Versions", "A", "FlutterMacOS"), []byte("synthetic engine"), 0o755)
		if err := os.Symlink("A", filepath.Join(fw, "Versions", "Current")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("Versions/Current/FlutterMacOS", filepath.Join(fw, "FlutterMacOS")); err != nil {
			t.Fatal(err)
		}
		return dir, "Zatiti.app/Contents/MacOS/zatiti_desktop"
	}
	writeFile(t, filepath.Join(dir, "zatiti_desktop"), []byte("synthetic runner "+version), 0o755)
	writeFile(t, filepath.Join(dir, "lib", "libapp.so"), []byte("synthetic dart code"), 0o644)
	writeFile(t, filepath.Join(dir, "data", "flutter_assets", "AssetManifest.bin"), []byte{0}, 0o644)
	return dir, "zatiti_desktop"
}

// newBundledDesktopFixture is newDesktopFixture with a real archive built by
// AssembleBundle in place of the synthetic bytes.
func newBundledDesktopFixture(t *testing.T, version, goos string) fixture {
	t.Helper()
	f := newDesktopFixture(t, version, goos)
	bundleDir, executable := stageBundle(t, goos, version)
	archive := filepath.Join(f.root, filepath.FromSlash(f.input.Desktop.Bundle))
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	if _, _, err := AssembleBundle(bundleDir, archive); err != nil {
		t.Fatalf("AssembleBundle: %v", err)
	}
	f.input.Desktop.Executable = executable
	return f
}

func desktopLayout(t *testing.T, goos string) Layout {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home", "operator")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := NewDesktopLayout(DesktopLayoutInput{OS: goos, Home: home, StateDir: filepath.Join(home, "zatiti-state")})
	if err != nil {
		t.Fatalf("NewDesktopLayout: %v", err)
	}
	return l
}

func TestAssembleBundleIsDeterministicAndRoundTrips(t *testing.T) {
	t.Parallel()
	for _, goos := range []string{"darwin", "linux"} {
		dir, executable := stageBundle(t, goos, "1.0.0")
		first := filepath.Join(t.TempDir(), "a.tar.gz")
		second := filepath.Join(t.TempDir(), "b.tar.gz")
		digestA, sizeA, err := AssembleBundle(dir, first)
		if err != nil {
			t.Fatalf("%s AssembleBundle: %v", goos, err)
		}
		digestB, sizeB, err := AssembleBundle(dir, second)
		if err != nil {
			t.Fatal(err)
		}
		if digestA != digestB || sizeA != sizeB {
			t.Fatalf("%s: two assemblies differ", goos)
		}
		dest := filepath.Join(t.TempDir(), "unpacked")
		if err := extractBundle(first, dest, digestA, sizeA, executable); err != nil {
			t.Fatalf("%s extractBundle: %v", goos, err)
		}
		if err := VerifyBundle(first, dest); err != nil {
			t.Fatalf("%s VerifyBundle: %v", goos, err)
		}
		if goos == "darwin" {
			target, err := os.Readlink(filepath.Join(dest, "Zatiti.app", "Contents", "Frameworks", "FlutterMacOS.framework", "Versions", "Current"))
			if err != nil || target != "A" {
				t.Fatalf("framework version link was not preserved: %q, %v", target, err)
			}
		}
		info, err := os.Stat(filepath.Join(dest, filepath.FromSlash(executable)))
		if err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("runner mode: %v, %v", info, err)
		}
		if _, _, err := AssembleBundle(dir, first); Code(err) != CodeConflict {
			t.Fatalf("assembling over an existing archive: %v", err)
		}
	}
}

func TestAssembleBundleRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(t *testing.T, dir string)
		code   string
	}{
		{"absolute symlink", func(t *testing.T, dir string) {
			if err := os.Symlink("/etc/passwd", filepath.Join(dir, "lib", "passwd")); err != nil {
				t.Fatal(err)
			}
		}, CodeInvalidInput},
		{"symlink escaping the bundle", func(t *testing.T, dir string) {
			if err := os.Symlink("../../outside", filepath.Join(dir, "lib", "escape")); err != nil {
				t.Fatal(err)
			}
		}, CodeInvalidInput},
		{"database file", func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, "data", "cache.db"), []byte("x"), 0o644)
		}, CodeInvalidInput},
		{"key material", func(t *testing.T, dir string) {
			writeFile(t, filepath.Join(dir, "data", "client.key"), []byte("x"), 0o600)
		}, CodeInvalidInput},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir, _ := stageBundle(t, "linux", "1.0.0")
			tc.mutate(t, dir)
			_, _, err := AssembleBundle(dir, filepath.Join(t.TempDir(), "out.tar.gz"))
			wantCode(t, err, tc.code)
		})
	}
}

// craftArchive writes a tar.gz from raw headers, to reach cases that
// AssembleBundle never produces.
func craftArchive(t *testing.T, entries []tar.Header) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for i := range entries {
		hdr := entries[i]
		if err := tw.WriteHeader(&hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write(bytes.Repeat([]byte("x"), int(hdr.Size))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "crafted.tar.gz")
	writeFile(t, p, buf.Bytes(), 0o644)
	return p
}

func TestExtractBundleRefusesHostileArchives(t *testing.T) {
	t.Parallel()
	runner := tar.Header{Name: "zatiti_desktop", Typeflag: tar.TypeReg, Mode: 0o755, Size: 4}
	tests := []struct {
		name    string
		entries []tar.Header
		code    string
	}{
		{"parent traversal", []tar.Header{runner, {Name: "../escape", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}}, CodeInvalidInput},
		{"absolute path", []tar.Header{runner, {Name: "/etc/motd", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}}, CodeInvalidInput},
		{"hard link", []tar.Header{runner, {Name: "twin", Typeflag: tar.TypeLink, Linkname: "zatiti_desktop"}}, CodeInvalidInput},
		{"device node", []tar.Header{runner, {Name: "null", Typeflag: tar.TypeChar}}, CodeInvalidInput},
		{"absolute symlink", []tar.Header{runner, {Name: "lib", Typeflag: tar.TypeSymlink, Linkname: "/usr/lib"}}, CodeInvalidInput},
		{"escaping symlink", []tar.Header{runner, {Name: "lib/x", Typeflag: tar.TypeSymlink, Linkname: "../../y"}}, CodeInvalidInput},
		{"duplicate entry", []tar.Header{runner, runner}, CodeInvalidInput},
		{"file through a symlinked directory", []tar.Header{runner,
			{Name: "lib", Typeflag: tar.TypeSymlink, Linkname: "data"},
			{Name: "lib/planted", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}}, CodeInternalError},
		{"runner missing", []tar.Header{{Name: "other", Typeflag: tar.TypeReg, Mode: 0o755, Size: 1}}, CodeVerificationFailed},
		{"runner not executable", []tar.Header{{Name: "zatiti_desktop", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}}, CodeVerificationFailed},
		{"state file", []tar.Header{runner, {Name: "data/cache.sqlite", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1}}, CodeInvalidInput},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			archive := craftArchive(t, tc.entries)
			digest, size, err := hashFile(archive)
			if err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(t.TempDir(), "unpacked")
			wantCode(t, extractBundle(archive, dest, digest, size, "zatiti_desktop"), tc.code)
			if _, err := os.Lstat(dest); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("a refused extraction left files behind")
			}
		})
	}

	t.Run("digest mismatch", func(t *testing.T) {
		t.Parallel()
		archive := craftArchive(t, []tar.Header{runner})
		_, size, err := hashFile(archive)
		if err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "unpacked")
		wantCode(t, extractBundle(archive, dest, strings.Repeat("0", 64), size, "zatiti_desktop"), CodeVerificationFailed)
		if _, err := os.Lstat(dest); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("a refused extraction left files behind")
		}
	})

	t.Run("not gzip", func(t *testing.T) {
		t.Parallel()
		archive := filepath.Join(t.TempDir(), "plain.tar.gz")
		writeFile(t, archive, []byte("not an archive"), 0o644)
		wantCode(t, extractBundle(archive, filepath.Join(t.TempDir(), "unpacked"), strings.Repeat("0", 64), 14, "x"), CodeInvalidInput)
	})
}

func TestVerifyBundleReportsDrift(t *testing.T) {
	t.Parallel()
	dir, executable := stageBundle(t, "darwin", "1.0.0")
	archive := filepath.Join(t.TempDir(), "bundle.tar.gz")
	digest, size, err := AssembleBundle(dir, archive)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "unpacked")
	if err := extractBundle(archive, dest, digest, size, executable); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dest, filepath.FromSlash(executable)), []byte("replaced"), 0o755)
	writeFile(t, filepath.Join(dest, "Zatiti.app", "Contents", "extra"), []byte("x"), 0o644)
	if err := os.Remove(filepath.Join(dest, "Zatiti.app", "Contents", "Info.plist")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dest, "Zatiti.app", "Contents", "Frameworks", "FlutterMacOS.framework", "Versions", "Current")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("B", link); err != nil {
		t.Fatal(err)
	}
	perr := wantFault(t, VerifyBundle(archive, dest), CodeVerificationFailed)
	wantFinding(t, perr, executable, "differs")
	wantFinding(t, perr, "Zatiti.app/Contents/extra", "not in the bundle")
	wantFinding(t, perr, "Zatiti.app/Contents/Info.plist", "missing")
	wantFinding(t, perr, "Zatiti.app/Contents/Frameworks/FlutterMacOS.framework/Versions/Current", "link")
}

func TestDesktopInstallUpgradeUninstall(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			l := desktopLayout(t, goos)
			state := filepath.Join(l.StateDir, "zatiti.db")
			writeFile(t, state, []byte("durable state"), 0o600)
			plan := func(version string) (Plan, Manifest) {
				f := newBundledDesktopFixture(t, version, goos)
				m := f.build(t)
				inst, err := Inspect(l)
				if err != nil {
					t.Fatal(err)
				}
				p, err := PlanDesktopInstallation(DesktopInstallInput{Layout: l, Manifest: m, SourceRoot: f.root, HostArch: "arm64", Installed: inst})
				if err != nil {
					t.Fatalf("PlanDesktopInstallation %s: %v", version, err)
				}
				return p, m
			}

			first, m := plan("1.0.0")
			if first.Kind != PlanInstall || len(first.Before)+len(first.After) != 0 {
				t.Fatalf("first plan: kind %s, %d service actions", first.Kind, len(first.Before)+len(first.After))
			}
			if err := Apply(context.Background(), first, nil); err != nil {
				t.Fatalf("Apply install: %v", err)
			}
			if err := AuditDesktopInstalled(l); err != nil {
				t.Fatalf("AuditDesktopInstalled: %v", err)
			}
			repeat, _ := plan("1.0.0")
			if repeat.Kind != PlanNoop {
				t.Fatalf("repeat desktop install is %s", repeat.Kind)
			}
			if err := Apply(context.Background(), repeat, nil); err != nil {
				t.Fatalf("repeat desktop install: %v", err)
			}
			runner := filepath.Join(l.CurrentBundle, filepath.FromSlash(m.Desktop.Executable))
			if raw, err := os.ReadFile(runner); err != nil || string(raw) != "synthetic runner 1.0.0" {
				t.Fatalf("active runner: %q, %v", raw, err)
			}
			launcher := desktopLauncherPath(l, m)
			if goos == "linux" {
				raw, err := os.ReadFile(launcher)
				if err != nil || !strings.Contains(string(raw), "Exec="+runner+"\n") {
					t.Fatalf("desktop entry: %q, %v", raw, err)
				}
			} else {
				target, err := os.Readlink(launcher)
				if err != nil || target != filepath.Join(l.CurrentBundle, "Zatiti.app") {
					t.Fatalf("application link: %q, %v", target, err)
				}
			}

			second, _ := plan("1.1.0")
			if second.Kind != PlanUpgrade {
				t.Fatalf("second plan kind is %s", second.Kind)
			}
			if err := Apply(context.Background(), second, nil); err != nil {
				t.Fatalf("Apply upgrade: %v", err)
			}
			if err := AuditDesktopInstalled(l); err != nil {
				var perr *Error
				errors.As(err, &perr)
				t.Fatalf("AuditDesktopInstalled after upgrade: %v %+v", err, perr)
			}
			if raw, err := os.ReadFile(runner); err != nil || string(raw) != "synthetic runner 1.1.0" {
				t.Fatalf("active runner after upgrade: %q, %v", raw, err)
			}
			got, err := Inspect(l)
			if err != nil || got.Current != "1.1.0" || !reflect.DeepEqual(got.Versions, []string{"1.0.0", "1.1.0"}) {
				t.Fatalf("after upgrade: %+v, %v", got, err)
			}

			un, err := PlanDesktopUninstallation(l, m)
			if err != nil {
				t.Fatal(err)
			}
			if err := Apply(context.Background(), un, nil); err != nil {
				t.Fatalf("Apply uninstall: %v", err)
			}
			for _, gone := range []string{l.DistRoot, launcher} {
				if _, err := os.Lstat(gone); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("%s survived uninstall: %v", filepath.Base(gone), err)
				}
			}
			if raw, err := os.ReadFile(state); err != nil || string(raw) != "durable state" {
				t.Fatalf("controller state was touched: %q, %v", raw, err)
			}
		})
	}
}

func TestDesktopPlanRefusals(t *testing.T) {
	t.Parallel()
	l := desktopLayout(t, "linux")
	controllerLayout := testLayout(t, "linux")
	f := newBundledDesktopFixture(t, "1.0.0", "linux")
	m := f.build(t)
	base := DesktopInstallInput{Layout: l, Manifest: m, SourceRoot: f.root, HostArch: "arm64"}

	in := base
	in.Layout = controllerLayout
	_, err := PlanDesktopInstallation(in)
	wantCode(t, err, CodeInvalidInput)

	in = base
	in.HostArch = "amd64"
	_, err = PlanDesktopInstallation(in)
	wantCode(t, err, CodeCapabilityUnsupported)

	cf := newFixture(t, "1.0.0", "linux")
	in = base
	in.Manifest, in.SourceRoot = cf.build(t), cf.root
	_, err = PlanDesktopInstallation(in)
	wantCode(t, err, CodeCapabilityUnsupported)

	// A controller install never lands in a desktop layout, and vice versa.
	_, err = PlanInstallation(InstallInput{Layout: l, Manifest: cf.build(t), SourceRoot: cf.root, HostArch: "arm64", Services: []ServiceSpec{controllerSpec(controllerLayout, cf.build(t))}})
	wantCode(t, err, CodeInvalidInput)
	_, err = PlanUninstallation(UninstallInput{Layout: l, Labels: []string{ControllerLabel}})
	wantCode(t, err, CodeInvalidInput)
	_, err = PlanDesktopUninstallation(controllerLayout, m)
	wantCode(t, err, CodeInvalidInput)

	// The archive is checked again at apply time.
	plan, err := PlanDesktopInstallation(base)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.root, filepath.FromSlash(m.Desktop.Bundle)), []byte("swapped"), 0o644)
	wantCode(t, Apply(context.Background(), plan, nil), CodeVerificationFailed)
	if _, err := Inspect(l); err != nil {
		t.Fatal(err)
	}
}

func TestRenderDesktopEntry(t *testing.T) {
	t.Parallel()
	entry, err := RenderDesktopEntry(DesktopEntry{Name: "Zatiti", Comment: "Zatiti workspace", Exec: "/home/operator/.local/share/zatiti-desktop-dist/current-bundle/zatiti_desktop"})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"[Desktop Entry]", "Type=Application", "Name=Zatiti", "Exec=/home/operator/.local/share/zatiti-desktop-dist/current-bundle/zatiti_desktop", "Terminal=false"} {
		if !strings.Contains(string(entry), line+"\n") {
			t.Fatalf("entry lacks %q:\n%s", line, entry)
		}
	}
	for name, e := range map[string]DesktopEntry{
		"space in path":     {Name: "Zatiti", Comment: "x", Exec: "/opt/my apps/zatiti_desktop"},
		"field code":        {Name: "Zatiti", Comment: "x", Exec: "/opt/%u/zatiti_desktop"},
		"newline in name":   {Name: "Zatiti\nExec=/bin/sh", Comment: "x", Exec: "/opt/zatiti_desktop"},
		"relative exec":     {Name: "Zatiti", Comment: "x", Exec: "zatiti_desktop"},
		"quoted icon":       {Name: "Zatiti", Comment: "x", Exec: "/opt/zatiti_desktop", Icon: `/opt/"icon".png`},
		"unclean exec path": {Name: "Zatiti", Comment: "x", Exec: "/opt/../zatiti_desktop"},
	} {
		if _, err := RenderDesktopEntry(e); Code(err) != CodeInvalidInput {
			t.Fatalf("%s: got %v", name, err)
		}
	}
}

func TestDesktopLayout(t *testing.T) {
	t.Parallel()
	l, err := NewDesktopLayout(DesktopLayoutInput{OS: "linux", Home: "/home/operator", StateDir: "/home/operator/.local/state/zatiti", DataHome: "/data/operator"})
	if err != nil {
		t.Fatal(err)
	}
	if l.DistRoot != "/data/operator/zatiti-desktop-dist" || l.UnitDir != "/data/operator/applications" || l.Manager != ManagerNone || l.CurrentBundle != "/data/operator/zatiti-desktop-dist/current-bundle" {
		t.Fatalf("linux desktop layout: %+v", l)
	}
	d, err := NewDesktopLayout(DesktopLayoutInput{OS: "darwin", Home: "/Users/operator", StateDir: "/Users/operator/Library/Application Support/Zatiti"})
	if err != nil {
		t.Fatal(err)
	}
	if d.UnitDir != "/Users/operator/Applications" || d.DistRoot != "/Users/operator/Library/Application Support/zatiti-desktop-dist" {
		t.Fatalf("darwin desktop layout: %+v", d)
	}
	_, err = NewDesktopLayout(DesktopLayoutInput{OS: "linux", Home: "/home/operator", StateDir: "/home/operator/.local/share/zatiti-desktop-dist"})
	wantCode(t, err, CodeConflict)
	_, err = NewDesktopLayout(DesktopLayoutInput{OS: "windows", Home: "/home/operator", StateDir: "/home/operator/state"})
	wantCode(t, err, CodeCapabilityUnsupported)
}
