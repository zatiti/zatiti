package packaging

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvisionMasterKey(t *testing.T) {
	t.Parallel()
	l := testLayout(t, "linux")
	keyPath := filepath.Join(l.Home, "zatiti-keys", "master.key")

	ref, err := ProvisionMasterKey(l, keyPath)
	if err != nil {
		t.Fatalf("ProvisionMasterKey: %v", err)
	}
	if ref != "file:"+keyPath {
		t.Fatalf("reference is %q", ref)
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 32 || bytes.Equal(key, make([]byte, 32)) {
		t.Fatalf("key file holds %d bytes, or only zeros", len(key))
	}
	if strings.Contains(ref, string(key)) {
		t.Fatal("the reference carries key material")
	}
	for path, want := range map[string]os.FileMode{keyPath: 0o600, filepath.Dir(keyPath): 0o700} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s has mode %v, want %o (%v)", filepath.Base(path), info.Mode().Perm(), want, err)
		}
	}
	if err := CheckMasterKey(keyPath); err != nil {
		t.Fatalf("CheckMasterKey: %v", err)
	}

	// A second run must never replace the key.
	_, err = ProvisionMasterKey(l, keyPath)
	wantCode(t, err, CodeConflict)
	after, err := os.ReadFile(keyPath)
	if err != nil || !bytes.Equal(key, after) {
		t.Fatal("an existing master key was modified")
	}

	other, err := ProvisionMasterKey(l, filepath.Join(l.Home, "zatiti-keys", "second.key"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(strings.TrimPrefix(other, "file:"))
	if err != nil || bytes.Equal(second, key) {
		t.Fatal("two provisioned keys are identical")
	}
}

func TestProvisionMasterKeyRefusals(t *testing.T) {
	t.Parallel()
	l := testLayout(t, "linux")
	loose := filepath.Join(l.Home, "shared")
	if err := os.Mkdir(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(loose, filepath.Join(l.Home, "linked")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		path string
		code string
	}{
		{"relative", "master.key", CodeInvalidInput},
		{"inside state", filepath.Join(l.StateDir, "master.key"), CodeInvalidInput},
		{"inside distribution", filepath.Join(l.DistRoot, "keys", "master.key"), CodeInvalidInput},
		{"inside launchers", filepath.Join(l.UnitDir, "master.key"), CodeInvalidInput},
		{"directory open to others", filepath.Join(loose, "master.key"), CodeConflict},
		{"directory is a symlink", filepath.Join(l.Home, "linked", "master.key"), CodeConflict},
		{"grandparent missing", filepath.Join(l.Home, "a", "b", "master.key"), CodePrerequisiteMissing},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ProvisionMasterKey(l, tc.path)
			perr := wantFault(t, err, tc.code)
			if strings.Contains(perr.Error(), l.Home) {
				t.Fatalf("message leaks a path: %s", perr.Error())
			}
		})
	}
	if _, err := os.Lstat(filepath.Join(loose, "master.key")); err == nil {
		t.Fatal("a key was written into a directory open to others")
	}
}

func TestCheckMasterKey(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "keys")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	wantCode(t, CheckMasterKey(filepath.Join(dir, "absent.key")), CodePrerequisiteMissing)

	readable := filepath.Join(dir, "readable.key")
	writeFile(t, readable, bytes.Repeat([]byte{7}, 32), 0o640)
	wantCode(t, CheckMasterKey(readable), CodeVerificationFailed)

	empty := filepath.Join(dir, "empty.key")
	writeFile(t, empty, nil, 0o600)
	wantCode(t, CheckMasterKey(empty), CodeVerificationFailed)

	good := filepath.Join(dir, "good.key")
	writeFile(t, good, bytes.Repeat([]byte{7}, 32), 0o600)
	if err := CheckMasterKey(good); err != nil {
		t.Fatalf("CheckMasterKey: %v", err)
	}
	if err := os.Symlink(good, filepath.Join(dir, "link.key")); err != nil {
		t.Fatal(err)
	}
	wantCode(t, CheckMasterKey(filepath.Join(dir, "link.key")), CodeVerificationFailed)

	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	wantCode(t, CheckMasterKey(good), CodeVerificationFailed)
}
