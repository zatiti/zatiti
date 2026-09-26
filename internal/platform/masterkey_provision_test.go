package platform

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fakeMacMasterStore struct {
	value       []byte
	readErr     error
	createErr   error
	createCalls int
	service     string
	account     string
	duplicate   []byte
}

func (f *fakeMacMasterStore) Read(_ context.Context, service, account string) ([]byte, error) {
	f.service, f.account = service, account
	if f.readErr != nil {
		return nil, f.readErr
	}
	if f.value == nil {
		return nil, fs.ErrNotExist
	}
	return bytes.Clone(f.value), nil
}

func (f *fakeMacMasterStore) Create(_ context.Context, service, account string, v []byte) (bool, error) {
	f.createCalls++
	f.service, f.account = service, account
	if f.createErr != nil {
		return false, f.createErr
	}
	if f.duplicate != nil {
		f.value = bytes.Clone(f.duplicate)
		return false, nil
	}
	if f.value != nil {
		return false, nil
	}
	f.value = bytes.Clone(v)
	return true, nil
}

func validEncodedMacMaster() []byte {
	return []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x51}, 32)))
}

func TestProvisionMacMasterKeyCreateAndRerun(t *testing.T) {
	state := filepath.Join(t.TempDir(), "zatiti")
	store := &fakeMacMasterStore{}
	if err := provisionMacMasterKey(context.Background(), state, store); err != nil {
		t.Fatal(err)
	}
	if store.createCalls != 1 || store.account != "master" || !strings.HasPrefix(store.service, "com.zatiti.zatiti.v1.") {
		t.Fatalf("wrong item creation: calls=%d service=%q account=%q", store.createCalls, store.service, store.account)
	}
	first := bytes.Clone(store.value)
	if err := provisionMacMasterKey(context.Background(), state, store); err != nil {
		t.Fatal(err)
	}
	if store.createCalls != 1 || !bytes.Equal(first, store.value) {
		t.Fatal("rerun replaced the master item")
	}
	if got := len(store.value); got != 44 {
		t.Fatalf("encoded master length %d, want 44", got)
	}
	fi, err := os.Stat(filepath.Join(state, fileInstanceID))
	if err != nil || fi.Mode().Perm() != 0o600 || !desktopOwned(fi) {
		t.Fatalf("instance identity not private: %v, %v", fi, err)
	}
	fi, err = os.Stat(state)
	if err != nil || fi.Mode().Perm() != 0o700 || !desktopOwned(fi) {
		t.Fatalf("state not private: %v, %v", fi, err)
	}
}

func TestProvisionMacMasterKeyExistingAndDuplicateRace(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "duplicate"}[duplicate], func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "zatiti")
			store := &fakeMacMasterStore{}
			want := validEncodedMacMaster()
			if duplicate {
				store.duplicate = want
			} else {
				store.value = want
			}
			if err := provisionMacMasterKey(context.Background(), state, store); err != nil {
				t.Fatal(err)
			}
			if duplicate && store.createCalls != 1 || !duplicate && store.createCalls != 0 {
				t.Fatalf("wrong create count: %d", store.createCalls)
			}
			if !bytes.Equal(store.value, want) {
				t.Fatal("existing or racing item changed")
			}
		})
	}
}

func TestProvisionMacMasterKeyRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store *fakeMacMasterStore
	}{
		{"malformed", &fakeMacMasterStore{value: []byte("not-base64")}},
		{"locked", &fakeMacMasterStore{readErr: errors.New("locked")}},
		{"create refused", &fakeMacMasterStore{createErr: errors.New("ACL refused")}},
		{"racing malformed", &fakeMacMasterStore{duplicate: []byte("bad")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "zatiti")
			if err := provisionMacMasterKey(context.Background(), state, tc.store); err == nil {
				t.Fatal("accepted unavailable or malformed Keychain item")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &fakeMacMasterStore{}
	if err := provisionMacMasterKey(ctx, filepath.Join(t.TempDir(), "zatiti"), store); err == nil || store.createCalls != 0 {
		t.Fatal("cancelled provisioning touched Keychain")
	}
}

func TestProvisionMacMasterKeyUnsafeInstance(t *testing.T) {
	for _, tc := range []string{"symlink", "permissive", "malformed"} {
		t.Run(tc, func(t *testing.T) {
			state := filepath.Join(t.TempDir(), "zatiti")
			if err := os.Mkdir(state, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(state, fileInstanceID)
			switch tc {
			case "symlink":
				if err := os.Symlink(filepath.Join(t.TempDir(), "other"), path); err != nil {
					t.Fatal(err)
				}
			case "permissive":
				if err := os.WriteFile(path, []byte(strings.Repeat("a", 32)), 0o644); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(path, []byte("broken"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			store := &fakeMacMasterStore{}
			if err := provisionMacMasterKey(context.Background(), state, store); err == nil || store.createCalls != 0 {
				t.Fatal("unsafe instance reached Keychain")
			}
		})
	}
}

func TestProvisionMacMasterKeyUnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("real login Keychain is never used in unit tests")
	}
	if err := ProvisionMacMasterKey(context.Background(), filepath.Join(t.TempDir(), "zatiti")); err == nil {
		t.Fatal("unsupported platform accepted Mac provisioning")
	}
}
