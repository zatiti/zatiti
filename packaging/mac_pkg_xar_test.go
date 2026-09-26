package packaging

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func makeNativeTestProduct(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("native pkgbuild fixture requires macOS")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	inbox := filepath.Join(root, "Library", "Application Support", "zatiti-installer", "inbox", "1", "amd64")
	if err := os.MkdirAll(inbox, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"binding.json", "controller.tar.gz", "desktop.tar.gz"} {
		if err := os.WriteFile(filepath.Join(inbox, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	component := filepath.Join(dir, "component.pkg")
	product := filepath.Join(dir, "product.pkg")
	for _, args := range [][]string{
		{"/usr/bin/pkgbuild", "--root", root, "--identifier", "com.zatiti.fixture", "--version", "1", "--install-location", "/", component},
		{"/usr/bin/productbuild", "--package", component, product},
	} {
		cmd := exec.CommandContext(context.Background(), args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", args[0], err, out)
		}
	}
	return product
}

func rewriteTestXARTOC(t *testing.T, pkg string, transform func(string) string) string {
	t.Helper()
	raw, err := os.ReadFile(pkg)
	if err != nil {
		t.Fatal(err)
	}
	n := int(binary.BigEndian.Uint64(raw[8:16]))
	zr, err := zlib.NewReader(bytes.NewReader(raw[28 : 28+n]))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if err := zr.Close(); err != nil {
		t.Fatal(err)
	}
	changed := []byte(transform(string(plain)))
	var b bytes.Buffer
	zw := zlib.NewWriter(&b)
	if _, err := zw.Write(changed); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	header := bytes.Clone(raw[:28])
	binary.BigEndian.PutUint64(header[8:16], uint64(b.Len()))
	binary.BigEndian.PutUint64(header[16:24], uint64(len(changed)))
	mutated := append(append(header, b.Bytes()...), raw[28+n:]...)
	path := filepath.Join(t.TempDir(), "changed.pkg")
	if err := os.WriteFile(path, mutated, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMacPkgXARInventoryRejectsUnsafeNativeProduct(t *testing.T) {
	pkg := makeNativeTestProduct(t)
	x, err := openMacPkgXAR(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.entries) != 4 {
		t.Fatalf("members: %#v", x.entries)
	}
	for _, name := range []string{"Distribution", "component.pkg/Bom", "component.pkg/PackageInfo"} {
		if data, err := x.readSmallMember(name); err != nil || len(data) == 0 {
			t.Fatalf("read %s: %v", name, err)
		}
	}
	if err := x.Close(); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(string) string{
		"extra member": func(s string) string {
			return strings.Replace(s, "</toc>", `<file><type>file</type><name>evil</name></file></toc>`, 1)
		},
		"traversal": func(s string) string { return strings.Replace(s, "<name>Payload</name>", "<name>../Payload</name>", 1) },
		"wrong extent": func(s string) string {
			return strings.Replace(s, "<offset>20</offset>", "<offset>999999999999</offset>", 1)
		},
		"duplicate": func(s string) string { return strings.ReplaceAll(s, "<name>Bom</name>", "<name>Payload</name>") },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			path := rewriteTestXARTOC(t, pkg, mutate)
			if got, err := openMacPkgXAR(path); err == nil {
				_ = got.Close()
				t.Fatal("accepted unsafe archive inventory")
			}
		})
	}
	t.Run("corrupt compressed metadata", func(t *testing.T) {
		raw, err := os.ReadFile(pkg)
		if err != nil {
			t.Fatal(err)
		}
		raw[x.heap+x.entries["Distribution"].offset] ^= 0xff
		path := filepath.Join(t.TempDir(), "corrupt.pkg")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		broken, err := openMacPkgXAR(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = broken.Close() }()
		if _, err := broken.readSmallMember("Distribution"); err == nil {
			t.Fatal("accepted corrupted metadata bytes")
		}
	})
}
