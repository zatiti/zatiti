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

func TestParseFrontmatterRejections(t *testing.T) {
	cases := []struct {
		name string
		doc  string
	}{
		{"not frontmatter", "# Just a body\n"},
		{"unclosed block", "---\nname: x\ndescription: y\n"},
		{"unknown key", "---\nname: x\ndescription: y\nsecret-instruction: do bad\n---\nbody"},
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
