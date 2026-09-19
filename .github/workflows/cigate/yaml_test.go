package main

import (
	"strings"
	"testing"
)

func TestParseYAMLStructure(t *testing.T) {
	t.Parallel()
	src := `# leading comment
name: demo
on:
  pull_request:
  push:
    branches: [main, "release/x"]   # flow
env:
  QUOTED: "a: b # not a comment"
  SINGLE: 'it''s'
jobs:
  build:
    steps:
      - name: first
        uses: actions/checkout@0123 # v1.2.3
        with:
          depth: 0
      - name: second
        run: |
          echo one
          # kept

          echo two
      - name: folded
        run: >-
          a
          b
    list:
      - plain
      -
        nested: value
`
	root, err := parseYAML([]byte(src))
	if err != nil {
		t.Fatalf("parseYAML: %v", err)
	}
	if got, _ := root.get("name").scalar(); got != "demo" {
		t.Errorf("name = %q", got)
	}
	if n := root.path("on", "pull_request"); n == nil || !n.Null {
		t.Errorf("pull_request should be a null scalar, got %+v", n)
	}
	if got, _ := root.path("on", "push", "branches").strings(); strings.Join(got, "|") != "main|release/x" {
		t.Errorf("branches = %q", got)
	}
	if got, _ := root.path("env", "QUOTED").scalar(); got != "a: b # not a comment" {
		t.Errorf("QUOTED = %q", got)
	}
	if got, _ := root.path("env", "SINGLE").scalar(); got != "it's" {
		t.Errorf("SINGLE = %q", got)
	}
	steps := root.path("jobs", "build", "steps").items()
	if len(steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(steps))
	}
	uses := steps[0].get("uses")
	if uses.Value != "actions/checkout@0123" || uses.Comment != "v1.2.3" || uses.Line != 14 {
		t.Errorf("uses = %+v", uses)
	}
	if got, _ := steps[0].path("with", "depth").scalar(); got != "0" {
		t.Errorf("depth = %q", got)
	}
	if got, _ := steps[1].get("run").scalar(); got != "echo one\n# kept\n\necho two\n" {
		t.Errorf("literal block = %q", got)
	}
	if got, _ := steps[2].get("run").scalar(); got != "a b" {
		t.Errorf("folded block = %q", got)
	}
	list := root.path("jobs", "build", "list").items()
	if len(list) != 2 || list[0].Value != "plain" {
		t.Fatalf("list = %+v", list)
	}
	if got, _ := list[1].get("nested").scalar(); got != "value" {
		t.Errorf("nested = %q", got)
	}
}

func TestParseYAMLRejectsUnsupportedConstructs(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, src, want string }{
		{"empty", "# only a comment\n", "empty"},
		{"tab indentation", "a:\n\tb: c\n", "tab"},
		{"carriage return", "a: b\r\n", "carriage"},
		{"duplicate key", "a: 1\na: 2\n", "duplicate key"},
		{"duplicate nested key", "a:\n  permissions: read\n  permissions: write\n", "duplicate key"},
		{"anchor", "a: &x 1\n", "unsupported YAML construct"},
		{"alias", "a: *x\n", "unsupported YAML construct"},
		{"tag", "a: !!str 1\n", "unsupported YAML construct"},
		{"flow mapping", "a: {b: c}\n", "unsupported YAML construct"},
		{"nested flow", "a: [b, [c]]\n", "nested flow"},
		{"document marker", "---\na: b\n", "document markers"},
		{"multi-line plain scalar", "a: b\n  c\n", "multi-line"},
		{"mapping indicator in plain scalar", "a: b: c\n", "mapping indicator"},
		{"unterminated quote", "a: \"b\n", "unterminated"},
		{"unsupported escape", "a: \"\\x41\"\n", "unsupported escape"},
		{"text after quoted value", "a: \"b\" c\n", "unexpected text"},
		{"sequence at key indentation", "a:\n- b\n", "indent sequences"},
		{"over-indented sibling", "a:\n  b: 1\n    c: 2\n", "multi-line"},
		{"indented root", "  a: b\n", "column 0"},
		{"non-key line", "a:\n  just text\n", "expected a plain"},
		{"block scalar in sequence", "a:\n  - |\n    x\n", "block scalars as sequence items"},
		{"empty block scalar", "a: |\nb: c\n", "no content"},
		{"unterminated flow", "a: [b, c\n", "unterminated flow"},
		{"merge key", "a:\n  <<: x\n", "expected a plain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseYAML([]byte(tc.src))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("parseYAML error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}

func TestParseYAMLBounds(t *testing.T) {
	t.Parallel()
	if _, err := parseYAML([]byte("a: " + strings.Repeat("x", maxYAMLBytes))); err == nil {
		t.Error("oversized document was accepted")
	}
	var deep strings.Builder
	for i := 0; i <= maxYAMLDepth+1; i++ {
		deep.WriteString(strings.Repeat(" ", i) + "k:\n")
	}
	deep.WriteString(strings.Repeat(" ", maxYAMLDepth+2) + "k: v\n")
	if _, err := parseYAML([]byte(deep.String())); err == nil || !strings.Contains(err.Error(), "nesting") {
		t.Errorf("deep nesting error = %v", err)
	}
}
