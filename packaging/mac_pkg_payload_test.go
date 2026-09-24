package packaging

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func buildMacPayloadFixture(t *testing.T, root string) string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("native pkgbuild fixture requires macOS")
	}
	dir := t.TempDir()
	component, product := filepath.Join(dir, "component.pkg"), filepath.Join(dir, "product.pkg")
	for _, args := range [][]string{
		{"/usr/bin/pkgbuild", "--root", root, "--identifier", "com.zatiti.fixture", "--version", "1", "--install-location", "/", "--ownership", "preserve", component},
		{"/usr/bin/productbuild", "--package", component, product},
	} {
		cmd := exec.CommandContext(context.Background(), args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", args[0], err, out)
		}
	}
	return product
}

func TestMacPkgPayloadMatchesSignedBindingAndArchives(t *testing.T) {
	home, release, plan, _, _ := macPkgBindingFixture(t)
	pkg := buildMacPayloadFixture(t, home)
	if err := inspectMacPkgPayload(context.Background(), pkg, release, plan); err != nil {
		t.Fatal(err)
	}
	changed := plan
	changed.Controller.SHA256 = changed.Desktop.SHA256
	if err := inspectMacPkgPayload(context.Background(), pkg, release, changed); err == nil {
		t.Fatal("accepted mismatched signed archive")
	}
}

func TestMacPkgPayloadRejectsUnexpectedPathsModesAndLinks(t *testing.T) {
	cases := map[string]func(t *testing.T, inbox string){
		"extra file": func(t *testing.T, inbox string) { writeFile(t, filepath.Join(inbox, "extra"), []byte("x"), 0o600) },
		"permissive file": func(t *testing.T, inbox string) {
			if err := os.Chmod(filepath.Join(inbox, "binding.json"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"permissive directory": func(t *testing.T, inbox string) {
			if err := os.Chmod(inbox, 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, inbox string) {
			if err := os.Remove(filepath.Join(inbox, "desktop.tar.gz")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("controller.tar.gz", filepath.Join(inbox, "desktop.tar.gz")); err != nil {
				t.Fatal(err)
			}
		},
		"hardlink": func(t *testing.T, inbox string) {
			if err := os.Remove(filepath.Join(inbox, "desktop.tar.gz")); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(filepath.Join(inbox, "controller.tar.gz"), filepath.Join(inbox, "desktop.tar.gz")); err != nil {
				t.Fatal(err)
			}
		},
		"archive tamper": func(t *testing.T, inbox string) {
			path := filepath.Join(inbox, "controller.tar.gz")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			raw[0] ^= 0xff
			writeFile(t, path, raw, 0o600)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			home, release, plan, _, inbox := macPkgBindingFixture(t)
			mutate(t, inbox)
			pkg := buildMacPayloadFixture(t, home)
			if err := inspectMacPkgPayload(context.Background(), pkg, release, plan); err == nil {
				t.Fatal("accepted unsafe package payload")
			}
		})
	}
}

func TestMacPkgBOMRejectsCorruptedActualBytes(t *testing.T) {
	home, _, plan, b, _ := macPkgBindingFixture(t)
	pkg := buildMacPayloadFixture(t, home)
	x, err := openMacPkgXAR(pkg)
	if err != nil {
		t.Fatal(err)
	}
	entry := x.entries["component.pkg/Bom"]
	heap := x.heap
	if err := x.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(pkg)
	if err != nil {
		t.Fatal(err)
	}
	raw[heap+entry.offset] ^= 0xff
	corrupt := filepath.Join(t.TempDir(), "corrupt.pkg")
	if err := os.WriteFile(corrupt, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	x, err = openMacPkgXAR(corrupt)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = x.Close() }()
	if err := inspectMacPkgBOM(context.Background(), x, b, plan); err == nil {
		t.Fatal("accepted corrupted BOM member")
	}
}
