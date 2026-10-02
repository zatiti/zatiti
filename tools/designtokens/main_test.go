package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func committedTokens(t *testing.T) ([]byte, string) {
	t.Helper()
	root, err := findRoot()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(root, "design/tokens/tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	return source, root
}

func TestCommittedOutputsCurrent(t *testing.T) {
	source, root := committedTokens(t)
	dart, css, err := generate(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := syncOutputs(root, dart, css, true); err != nil {
		t.Fatal(err)
	}
	for file, generated := range map[string][]byte{"tokens.g.dart.golden": dart, "tokens.css.golden": css} {
		golden, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(generated, golden) {
			t.Fatalf("%s differs from golden", file)
		}
	}
}

func TestCheckDetectsDrift(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"apps/desktop/lib/src/ui", "apps/extension/panel"} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := syncOutputs(root, []byte("dart"), []byte("css"), false); err != nil {
		t.Fatal(err)
	}
	if err := syncOutputs(root, []byte("dart"), []byte("css"), true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "apps/extension/panel/tokens.css"), []byte("drift"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syncOutputs(root, []byte("dart"), []byte("css"), true); err == nil {
		t.Fatal("check accepted drift")
	}
}

func TestRejectsUnknownGroup(t *testing.T) {
	source, _ := committedTokens(t)
	bad := bytes.Replace(source, []byte(`"schema":`), []byte(`"unknown": {}, "schema":`), 1)
	if _, _, err := generate(bad); err == nil || !strings.Contains(err.Error(), "unknown token group") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCSSAndDartColorsAgree(t *testing.T) {
	source, _ := committedTokens(t)
	dart, css, err := generate(source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(dart, []byte("Color(0x20B88C44)")) || !bytes.Contains(css, []byte("#B88C4420")) {
		t.Fatal("alpha color conversion differs between Dart and CSS")
	}
	if !bytes.Contains(dart, []byte("Color(0xFFFCFCFC)")) || !bytes.Contains(css, []byte("--z-color-accent: #FCFCFC")) {
		t.Fatal("dark accent missing")
	}
}
