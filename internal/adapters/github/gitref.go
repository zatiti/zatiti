package github

import (
	"errors"
	"fmt"
	"strings"
)

// validGitBranch validates name as a safe Git branch (ref short-name),
// rejecting traversal, control characters and invalid ref syntax per the
// core git-check-ref-format rules. The frozen GitBranch schema only bounds
// length; this Go-level check is the "Git-aware validation" the
// implementation assignment calls for.
func validGitBranch(name string) error {
	if name == "" {
		return errors.New("branch name is empty")
	}
	if name == "@" {
		return errors.New(`branch name must not be exactly "@"`)
	}
	if strings.Contains(name, "@{") {
		return errors.New(`branch name must not contain "@{"`)
	}
	if strings.Contains(name, "..") {
		return errors.New(`branch name must not contain ".."`)
	}
	if strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return errors.New(`branch name must not begin or end with "/"`)
	}
	if strings.Contains(name, "//") {
		return errors.New("branch name must not contain consecutive slashes")
	}
	if strings.HasSuffix(name, ".") {
		return errors.New(`branch name must not end with "."`)
	}
	if strings.Contains(name, "\\") {
		return errors.New("branch name must not contain a backslash")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return errors.New("branch name must not contain control characters")
		}
		switch r {
		case ' ', '~', '^', ':', '?', '*', '[':
			return fmt.Errorf("branch name must not contain %q", r)
		}
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" {
			continue // leading/trailing/doubled slashes are already rejected above
		}
		if strings.HasPrefix(part, ".") {
			return fmt.Errorf("branch component %q must not begin with \".\"", part)
		}
		if strings.HasSuffix(part, ".lock") {
			return fmt.Errorf("branch component %q must not end with \".lock\"", part)
		}
	}
	return nil
}

// validRepositoryPath validates p as a normalized relative repository path,
// rejecting absolute paths, dot/dot-dot components, NUL and backslash
// ambiguity per the RepositoryPath schema's documented intent.
func validRepositoryPath(p string) error {
	if p == "" {
		return errors.New("path is empty")
	}
	if strings.ContainsRune(p, 0) {
		return errors.New("path must not contain a NUL byte")
	}
	if strings.HasPrefix(p, "/") {
		return errors.New("path must not be absolute")
	}
	if strings.Contains(p, "\\") {
		return errors.New("path must not contain a backslash")
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" {
			return errors.New("path must not contain empty components")
		}
		if part == "." || part == ".." {
			return fmt.Errorf("path component %q is not allowed", part)
		}
	}
	return nil
}
