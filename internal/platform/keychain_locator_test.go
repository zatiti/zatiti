package platform

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func fakeLocatorKeychain(t *testing.T, encoded string, exit int) *keychainSecrets {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "security")
	body := "#!/bin/sh\n"
	if exit != 0 {
		body += "exit " + strconv.Itoa(exit) + "\n"
	} else {
		body += "printf '%s' '" + encoded + "'\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return &keychainSecrets{service: "com.zatiti.zatiti.v1.1234567890abcdef", helperPath: path}
}
func ownerHeader() string { return "Bearer " + base64.RawURLEncoding.EncodeToString(make([]byte, 32)) }
func TestKeychainLocatorVerifiedReadableItem(t *testing.T) {
	k := fakeLocatorKeychain(t, base64.StdEncoding.EncodeToString([]byte(ownerHeader())), 0)
	ref := keychainRef("installation/owner")
	service, account, err := keychainLocator(context.Background(), k, ref)
	if err != nil || service != k.service || account != "installation/owner" {
		t.Fatalf("locator: %q %q %v", service, account, err)
	}
	for _, bad := range []string{"hl1:garbage", "kc1:@@@", ref + "=", keychainRef("bad\x00account")} {
		if _, _, err := keychainLocator(context.Background(), k, bad); Code(err) != contract.CodeNotFound {
			t.Fatalf("accepted bad ref %q: %v", bad, err)
		}
	}
}
func TestKeychainLocatorMissingUnreadableAndClosed(t *testing.T) {
	ref := keychainRef("installation/owner")
	missing := fakeLocatorKeychain(t, "", 44)
	if _, _, err := keychainLocator(context.Background(), missing, ref); Code(err) != contract.CodeNotFound {
		t.Fatalf("missing item: %v", err)
	}
	locked := fakeLocatorKeychain(t, "", 1)
	if _, _, err := keychainLocator(context.Background(), locked, ref); Code(err) != contract.CodePrerequisiteMissing {
		t.Fatalf("locked item: %v", err)
	}
	unreadable := fakeLocatorKeychain(t, base64.StdEncoding.EncodeToString([]byte("not a header")), 0)
	if _, _, err := keychainLocator(context.Background(), unreadable, ref); Code(err) != contract.CodePrerequisiteMissing {
		t.Fatalf("invalid item: %v", err)
	}
	unavailable := &keychainSecrets{service: missing.service, helperPath: filepath.Join(t.TempDir(), "absent")}
	if _, _, err := keychainLocator(context.Background(), unavailable, ref); Code(err) != contract.CodePrerequisiteMissing {
		t.Fatalf("unavailable helper: %v", err)
	}
	k := fakeLocatorKeychain(t, base64.StdEncoding.EncodeToString([]byte(ownerHeader())), 0)
	k.zeroKeys()
	if _, _, err := keychainLocator(context.Background(), k, ref); Code(err) != contract.CodeControllerUnavailable {
		t.Fatalf("closed store: %v", err)
	}
}
func TestPublicKeychainLocatorCapabilityGate(t *testing.T) {
	p, _ := openDiscoveryForTest(t)
	_, _, err := p.KeychainLocator(context.Background(), keychainRef("installation/owner"))
	if runtime.GOOS == "darwin" {
		if Code(err) != contract.CodeCapabilityUnsupported {
			t.Fatalf("headless Mac capability: %v", err)
		}
	} else if Code(err) != contract.CodeCapabilityUnsupported {
		t.Fatalf("non-Mac capability: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), p.StateDir()) {
		t.Fatal("path leaked")
	}
}
