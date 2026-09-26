package skills

import (
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// The SKILL.md frontmatter parser is a bounded strict subset parser. Every
// rejection here keeps untrusted metadata from being interpreted as anything
// but inert, validated fields.

func TestParseFrontmatterValid(t *testing.T) {
	t.Run("minimal", func(t *testing.T) {
		fm, body, err := parseFrontmatter([]byte(minimalSkillMD))
		if err != nil {
			t.Fatalf("minimal rejected: %v", err)
		}
		if fm.Name != "minimal" || fm.Description != "A minimal skill." {
			t.Fatalf("parsed %+v", fm)
		}
		if fm.License != "" || len(fm.AllowedTools) != 0 || len(fm.Dependencies) != 0 {
			t.Fatalf("unexpected optional values: %+v", fm)
		}
		if string(body) != "\nBody.\n" {
			t.Fatalf("body = %q, want byte-exact remainder", body)
		}
	})

	t.Run("full metadata", func(t *testing.T) {
		doc := "---\nname: greeter\ndescription: Greets.\nlicense: MIT\n" +
			"allowed-tools: tool:echo ,  tool:clock\ndependencies:\n  - base-utils\n  - pins@7\n---\nBody"
		fm, _, err := parseFrontmatter([]byte(doc))
		if err != nil {
			t.Fatalf("full metadata rejected: %v", err)
		}
		if fm.License != "MIT" {
			t.Fatalf("license = %q", fm.License)
		}
		if len(fm.AllowedTools) != 2 || fm.AllowedTools[0] != "tool:echo" || fm.AllowedTools[1] != "tool:clock" {
			t.Fatalf("allowed tools = %v", fm.AllowedTools)
		}
		if len(fm.Dependencies) != 2 ||
			fm.Dependencies[0].Name != "base-utils" || fm.Dependencies[0].Version != 0 ||
			fm.Dependencies[1].Name != "pins" || fm.Dependencies[1].Version != 7 {
			t.Fatalf("dependencies = %+v", fm.Dependencies)
		}
	})
}

// TestParseFrontmatterClaudeCodeExtensions proves argument-hint,
// disable-model-invocation and metadata (both real observed shapes) are
// accepted and fully discarded: the resulting frontmatter carries the same
// Name/Description/License/AllowedTools/Dependencies as if the keys were
// absent. Written before frontmatter.go accepts these keys, per Z-M5's
// test-first requirement; it fails today against knownFrontmatterKey.
func TestParseFrontmatterClaudeCodeExtensions(t *testing.T) {
	assertDiscarded := func(t *testing.T, fm *frontmatter) {
		t.Helper()
		if fm.Name != "x" || fm.Description != "y" {
			t.Fatalf("name/description corrupted: %+v", fm)
		}
		if fm.License != "" || len(fm.AllowedTools) != 0 || len(fm.Dependencies) != 0 {
			t.Fatalf("unexpected field populated from a discarded key: %+v", fm)
		}
	}

	t.Run("argument-hint and disable-model-invocation alone", func(t *testing.T) {
		doc := "---\nname: x\ndescription: y\nargument-hint: \"[--fix] [--repo <path>]\"\n" +
			"disable-model-invocation: true\n---\nbody"
		fm, _, err := parseFrontmatter([]byte(doc))
		if err != nil {
			t.Fatalf("rejected: %v", err)
		}
		assertDiscarded(t, fm)
	})

	t.Run("metadata canvas shape: two-level list-under-map", func(t *testing.T) {
		// The real shape observed at ~/.agents/skills/canvas/SKILL.md:
		// metadata: -> surfaces: (indent 2) -> - ide / - cloud (indent 4).
		doc := "---\nname: x\ndescription: y\nmetadata:\n  surfaces:\n    - ide\n    - cloud\n---\nbody"
		fm, _, err := parseFrontmatter([]byte(doc))
		if err != nil {
			t.Fatalf("rejected: %v", err)
		}
		assertDiscarded(t, fm)
	})

	t.Run("metadata launch/sire shape: single flat line", func(t *testing.T) {
		// The real shape observed at ~/.agents/skills/launch/SKILL.md and
		// sire's SKILL.md: metadata: -> version: 1.0.0 (indent 2, scalar).
		doc := "---\nname: x\ndescription: y\nmetadata:\n  version: 1.0.0\n---\nbody"
		fm, _, err := parseFrontmatter([]byte(doc))
		if err != nil {
			t.Fatalf("rejected: %v", err)
		}
		assertDiscarded(t, fm)
	})

	t.Run("metadata immediately followed by a real key at metadata's own indent: allowed-tools", func(t *testing.T) {
		// Proves the skip stops exactly at the right line: canvas's real
		// file has an "environments:" key at metadata's own indent right
		// after the block; here we use an ACCEPTED key in that position to
		// prove it still parses correctly (a genuinely unsupported key in
		// that position is covered separately as a rejection).
		doc := "---\nname: x\ndescription: y\nmetadata:\n  surfaces:\n    - ide\n    - cloud\nallowed-tools: tool:echo\n---\nbody"
		fm, _, err := parseFrontmatter([]byte(doc))
		if err != nil {
			t.Fatalf("rejected: %v", err)
		}
		if fm.License != "" || len(fm.Dependencies) != 0 {
			t.Fatalf("unexpected field populated from a discarded key: %+v", fm)
		}
		if len(fm.AllowedTools) != 1 || fm.AllowedTools[0] != "tool:echo" {
			t.Fatalf("allowed-tools after metadata = %v, want [tool:echo] (skip stopped one line late/early)", fm.AllowedTools)
		}
	})

	t.Run("metadata immediately followed by a real block key at metadata's own indent: dependencies", func(t *testing.T) {
		doc := "---\nname: x\ndescription: y\nmetadata:\n  version: 1.0.0\ndependencies:\n  - base-utils\n---\nbody"
		fm, _, err := parseFrontmatter([]byte(doc))
		if err != nil {
			t.Fatalf("rejected: %v", err)
		}
		if fm.License != "" || len(fm.AllowedTools) != 0 {
			t.Fatalf("unexpected field populated from a discarded key: %+v", fm)
		}
		if len(fm.Dependencies) != 1 || fm.Dependencies[0].Name != "base-utils" {
			t.Fatalf("dependencies after metadata = %+v (skip stopped one line late/early)", fm.Dependencies)
		}
	})

	t.Run("all three combined", func(t *testing.T) {
		doc := "---\nname: x\ndescription: y\nargument-hint: \"[--fix]\"\n" +
			"disable-model-invocation: false\nmetadata:\n  surfaces:\n    - ide\n---\nbody"
		fm, _, err := parseFrontmatter([]byte(doc))
		if err != nil {
			t.Fatalf("rejected: %v", err)
		}
		assertDiscarded(t, fm)
	})
}

func TestParseFrontmatterRejections(t *testing.T) {
	cases := []struct {
		name string
		doc  string
	}{
		{"not frontmatter", "# Just a body\n"},
		{"unclosed block", "---\nname: x\ndescription: y\n"},
		{"unknown key", "---\nname: x\ndescription: y\nsecret-instruction: do bad\n---\nbody"},
		// Regression guard (Z-M5): this card narrows the reject set to
		// exactly argument-hint/disable-model-invocation/metadata; a key
		// outside that set is still rejected, proving the parser stays a
		// strict subset parser rather than becoming permissive. This
		// mirrors the real, still-unsupported "environments:" key found
		// sitting alongside metadata in ~/.agents/skills/canvas/SKILL.md.
		{"unsupported key after a metadata block", "---\nname: x\ndescription: y\nmetadata:\n  surfaces:\n    - ide\nenvironments:\n  - local\n---\nbody"},
		{"claude-code key not in the accepted set", "---\nname: x\ndescription: y\nmodel: opus\n---\nbody"},
		// Metadata's inline-value guard mirrors dependencies' own.
		{"metadata inline value", "---\nname: x\ndescription: y\nmetadata: not-a-block\n---\nbody"},
		// An unterminated metadata block (never dedents, and the
		// frontmatter never closes) must fail closed via the existing
		// line-count bound, not hang or silently truncate.
		{"unterminated metadata block", "---\nname: x\ndescription: y\nmetadata:\n" + strings.Repeat("  - item\n", 600)},
		{"duplicate key", "---\nname: x\nname: y\ndescription: z\n---\nbody"},
		{"inline dependencies", "---\nname: x\ndescription: y\ndependencies: foo\n---\nbody"},
		{"missing name", "---\ndescription: y\n---\nbody"},
		{"missing description", "---\nname: x\n---\nbody"},
		{"uppercase name", "---\nname: Big\ndescription: y\n---\nbody"},
		{"underscore name", "---\nname: snake_case\ndescription: y\n---\nbody"},
		{"leading hyphen name", "---\nname: -lead\ndescription: y\n---\nbody"},
		{"empty key value", "---\nname: x\ndescription:   \n---\nbody"},
		{"indented key", "---\n  name: x\ndescription: y\n---\nbody"},
		{"bad dependency form", "---\nname: x\ndescription: y\ndependencies:\n  - not a ref!\n---\nbody"},
		{"dependency zero version", "---\nname: x\ndescription: y\ndependencies:\n  - base@0\n---\nbody"},
		{"dependency leading zero version", "---\nname: x\ndescription: y\ndependencies:\n  - base@01\n---\nbody"},
		{"empty allowed tool entry", "---\nname: x\ndescription: y\nallowed-tools: a,,b\n---\nbody"},
		{"over-long description", "---\nname: x\ndescription: " + strings.Repeat("a", 1025) + "\n---\nbody"},
		{"over-long name", "---\nname: " + strings.Repeat("a", 65) + "\ndescription: y\n---\nbody"},
		{"frontmatter over 64 KiB", "---\nname: x\ndescription: " + strings.Repeat("a", 70*1024) + "\n---\nbody"},
		{"frontmatter over 512 lines", "---\n" + strings.Repeat("\n", 600) + "name: x\ndescription: y\n---\nbody"},
		{"over 128 dependencies", func() string {
			var b strings.Builder
			b.WriteString("---\nname: x\ndescription: y\ndependencies:\n")
			for i := 0; i < 129; i++ {
				b.WriteString("  - dep" + strings.Repeat("a", i+1) + "\n")
			}
			b.WriteString("---\nbody")
			return b.String()
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := parseFrontmatter([]byte(tc.doc))
			if err == nil {
				t.Fatal("invalid frontmatter accepted")
			}
			var f *contract.Fault
			if !asFault(err, &f) || f.Code != contract.CodeInvalidInput {
				t.Fatalf("rejection fault = %v, want invalid_input", err)
			}
		})
	}
}

func TestTruncateForMessage(t *testing.T) {
	long := strings.Repeat("x", 100)
	got := truncateForMessage(long)
	if len(got) != 64 || got != long[:64] {
		t.Fatalf("truncateForMessage = %d characters, want the first 64", len(got))
	}
	if short := truncateForMessage("abc"); short != "abc" {
		t.Fatalf("short string altered: %q", short)
	}
}
