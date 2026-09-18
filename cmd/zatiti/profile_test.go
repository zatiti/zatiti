package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileStoreRoundTrip(t *testing.T) {
	t.Parallel()
	store := profileStore{dir: filepath.Join(t.TempDir(), "profiles")}
	if err := store.write("owner", []byte("Bearer abc.def")); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Stat(filepath.Join(store.dir, "owner"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != filePrivate {
		t.Fatalf("profile mode = %o, want %o", info.Mode().Perm(), filePrivate)
	}
	dirInfo, err := os.Stat(store.dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if dirInfo.Mode().Perm() != dirPrivate {
		t.Fatalf("profile dir mode = %o, want %o", dirInfo.Mode().Perm(), dirPrivate)
	}
	got, err := profileCredential{store: store, name: "owner"}.Credential(context.Background())
	if err != nil || string(got) != "Bearer abc.def" {
		t.Fatalf("Credential = %q, %v", got, err)
	}
	// A rewrite replaces the credential atomically.
	if err := store.write("owner", []byte("Bearer second\n")); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if got, err := store.read("owner"); err != nil || string(got) != "Bearer second" {
		t.Fatalf("read after rewrite = %q, %v", got, err)
	}
}

func TestProfileStoreRefusals(t *testing.T) {
	t.Parallel()
	store := profileStore{dir: filepath.Join(t.TempDir(), "profiles")}
	if _, err := store.read("owner"); !errors.Is(err, errProfileMissing) {
		t.Fatalf("missing profile: %v, want errProfileMissing", err)
	}
	if err := store.write("../escape", []byte("x")); err == nil || !strings.Contains(err.Error(), "profile name") {
		t.Fatalf("path escape accepted: %v", err)
	}
	if err := store.write("raw", []byte{0x00, 0x01, 0xff}); err != nil {
		t.Fatalf("write raw: %v", err)
	}
	if _, err := store.read("raw"); err == nil || !strings.Contains(err.Error(), "Authorization header value") {
		t.Fatalf("raw bytes accepted as a header value: %v", err)
	}
	if err := store.write("empty", []byte("\n")); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	if _, err := store.read("empty"); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty credential accepted: %v", err)
	}
}

func TestLoggerRedactsSensitiveAttributes(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	log := newLogger(&buf, slog.LevelInfo)
	log.Info("x", "authorization", "Bearer hunter2", "owner_credential", "hunter2", "master_key_ref", "file:/k", "generation", 3)
	out := buf.String()
	if strings.Contains(out, "hunter2") || strings.Contains(out, "file:/k") {
		t.Fatalf("secret reached the log: %s", out)
	}
	if !strings.Contains(out, "generation=3") || strings.Count(out, redacted) != 3 {
		t.Fatalf("unexpected record: %s", out)
	}
}
