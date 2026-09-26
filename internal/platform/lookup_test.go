package platform

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHeadlessLookupTracksCurrentOpaqueReference(t *testing.T) {
	p, state := openForTest(t)
	t.Cleanup(func() { _ = p.Close() })
	name := "connections/helper/receipt-key"
	if ref, err := p.secrets.Lookup(ctx(), name); ref != "" {
		t.Fatalf("missing Lookup returned %q, %v", ref, err)
	} else {
		wantCode(t, err, contractCodeNotFound)
	}
	first, err := p.Secrets().Put(ctx(), name, []byte("first-key"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.secrets.Lookup(ctx(), name); err != nil || got != first || got == name {
		t.Fatalf("Lookup = %q, %v; want opaque %q", got, err, first)
	}
	second, err := p.Secrets().Put(ctx(), name, []byte("second-key"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.secrets.Lookup(ctx(), name); err != nil || got != second || got == first {
		t.Fatalf("Lookup after replacement = %q, %v; want %q", got, err, second)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	p, err = reopenForTest(t, state)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.secrets.Lookup(ctx(), name); err != nil || got != second {
		t.Fatalf("Lookup after reopen = %q, %v; want %q", got, err, second)
	}
	if err := p.Secrets().Delete(ctx(), second); err != nil {
		t.Fatal(err)
	}
	if ref, err := p.secrets.Lookup(ctx(), name); ref != "" {
		t.Fatalf("deleted Lookup returned %q, %v", ref, err)
	} else {
		wantCode(t, err, contractCodeNotFound)
	}
}

func TestHeadlessLookupRefusesInvalidAndUnavailableIndex(t *testing.T) {
	for _, key := range []string{"", strings.Repeat("k", 257), "bad\x00key"} {
		p, _ := openForTest(t)
		_, err := p.secrets.Lookup(ctx(), key)
		wantCode(t, err, contractCodeInvalidInput)
		_ = p.Close()
	}

	for _, tc := range []struct {
		name, replacement string
		code              string
	}{
		{"malformed reference", `{"v":1,"keys":{"k":"not-a-reference"}}`, contractCodeControllerUnavailable},
		{"corrupt index", `{broken`, contractCodeControllerUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, state := openForTest(t)
			ref, err := p.Secrets().Put(ctx(), "k", []byte("value"))
			if err != nil {
				t.Fatal(err)
			}
			_ = p.Close()
			index := filepath.Join(state, dirNameSecrets, fileSecretIndex)
			if err := os.WriteFile(index, []byte(tc.replacement), 0o600); err != nil {
				t.Fatal(err)
			}
			reopened, err := reopenForTest(t, state)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reopened.Close() }()
			got, err := reopened.secrets.Lookup(ctx(), "k")
			if got != "" {
				t.Fatalf("corrupt index returned reference %q", got)
			}
			wantCode(t, err, tc.code)
			// Direct, already-issued references remain independently recoverable.
			if value, err := reopened.Secrets().Get(ctx(), ref); err != nil || string(value) != "value" {
				t.Fatalf("direct Get after index failure = %q, %v", value, err)
			}
		})
	}
}

func TestHeadlessLookupRefusesMissingOrTamperedValue(t *testing.T) {
	for _, tamper := range []bool{false, true} {
		p, state := openForTest(t)
		ref, err := p.Secrets().Put(ctx(), "k", []byte("value"))
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(state, dirNameSecrets, dirNameSecretVals, strings.TrimPrefix(ref, secretRefPrefix))
		if tamper {
			if err := os.WriteFile(path, []byte("tampered"), 0o600); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		got, err := p.secrets.Lookup(ctx(), "k")
		if got != "" {
			t.Fatalf("missing/corrupt value returned reference %q", got)
		}
		code := contractCodeNotFound
		if tamper {
			code = contractCodeControllerUnavailable
		}
		wantCode(t, err, code)
		_ = p.Close()
	}
}

func TestLookupFailsAfterClose(t *testing.T) {
	p, _ := openForTest(t)
	if _, err := p.Secrets().Put(ctx(), "k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	_ = p.Close()
	ref, err := p.secrets.Lookup(ctx(), "k")
	if ref != "" {
		t.Fatalf("closed store returned %q", ref)
	}
	wantCode(t, err, contractCodeControllerUnavailable)
}

func TestKeychainLookupProvesItemExists(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "security")
	script := "#!/bin/sh\n" +
		"test \"$1\" = find-generic-password || exit 1\n" +
		"test \"$3\" = com.zatiti.test || exit 1\n" +
		"case \"$5\" in\n" +
		"  present) printf 'c2VjcmV0' ;;\n" +
		"  corrupt) printf 'not-base64!' ;;\n" +
		"  *) exit 44 ;;\n" +
		"esac\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	k := &keychainSecrets{service: "com.zatiti.test", helperPath: helper}
	if ref, err := k.Lookup(context.Background(), "present"); err != nil || ref != keychainRef("present") {
		t.Fatalf("present Lookup = %q, %v", ref, err)
	}
	if ref, err := k.Lookup(context.Background(), "missing"); ref != "" {
		t.Fatalf("missing Lookup returned %q, %v", ref, err)
	} else {
		wantCode(t, err, contractCodeNotFound)
	}
	if ref, err := k.Lookup(context.Background(), "corrupt"); ref != "" {
		t.Fatalf("corrupt Keychain item returned %q, %v", ref, err)
	} else {
		wantCode(t, err, contractCodeControllerUnavailable)
	}
	if ref, err := k.Lookup(context.Background(), ""); ref != "" {
		t.Fatalf("invalid Lookup returned %q, %v", ref, err)
	} else {
		wantCode(t, err, contractCodeInvalidInput)
	}
	k.zeroKeys()
	if ref, err := k.Lookup(context.Background(), "present"); ref != "" {
		t.Fatalf("closed Lookup returned %q, %v", ref, err)
	} else {
		wantCode(t, err, contractCodeControllerUnavailable)
	}
}
