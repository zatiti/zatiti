package github

import "testing"

func TestValidGitBranch(t *testing.T) {
	valid := []string{"main", "feature/foo", "release-1.2.3", "a/b/c", "x"}
	for _, name := range valid {
		if err := validGitBranch(name); err != nil {
			t.Errorf("validGitBranch(%q) = %v, want nil", name, err)
		}
	}

	invalid := map[string]string{
		"":                 "empty",
		"@":                "exactly @",
		"foo@{bar}":        "contains @{",
		"foo..bar":         "double dot",
		"/leading":         "leading slash",
		"trailing/":        "trailing slash",
		"double//slash":    "double slash",
		"trailing.":        "trailing dot",
		"back\\slash":      "backslash",
		"has space":        "space",
		"has~tilde":        "tilde",
		"has^caret":        "caret",
		"has:colon":        "colon",
		"has?question":     "question mark",
		"has*star":         "asterisk",
		"has[bracket":      "bracket",
		".hidden":          "component starts with dot",
		"a/.hidden":        "component starts with dot",
		"refs.lock":        "ends with .lock",
		"a/refs.lock":      "component ends with .lock",
		"\x01control":      "control character",
		string(rune(0x7f)): "DEL character",
	}
	for name, reason := range invalid {
		if err := validGitBranch(name); err == nil {
			t.Errorf("validGitBranch(%q) = nil, want error (%s)", name, reason)
		}
	}
}

func TestValidRepositoryPath(t *testing.T) {
	valid := []string{"a", "a/b", "path/to/file.go"}
	for _, p := range valid {
		if err := validRepositoryPath(p); err != nil {
			t.Errorf("validRepositoryPath(%q) = %v, want nil", p, err)
		}
	}

	invalid := []string{"", "/abs", "a//b", "a/../b", "..", ".", "a/./b", "back\\slash", "has\x00nul"}
	for _, p := range invalid {
		if err := validRepositoryPath(p); err == nil {
			t.Errorf("validRepositoryPath(%q) = nil, want error", p)
		}
	}
}
