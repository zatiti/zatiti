package skills

import (
	"regexp"
	"strconv"
	"strings"
)

// SKILL.md frontmatter parsing.
//
// The adapter supports the published Agent Skills metadata keys: name,
// description, license, allowed-tools and dependencies. Parsing is a bounded
// strict subset parser: unknown keys, duplicate keys and malformed values are
// rejected, and nothing here executes or grants authority — imported text is
// untrusted data and allowed-tools entries become pinned requirements.
// Zatiti execution schemas are stored separately from this metadata.

const (
	maxFrontmatterBytes  = 64 * 1024
	maxFrontmatterLines  = 512
	maxDescriptionLength = 1024
	maxNameLength        = 64
	maxToolCount         = 256
	maxToolLength        = 8192
	maxDependencyCount   = 128
)

// dependencyRef is one parsed dependency entry: a skill name, optionally
// pinned to an exact version with name@N. Version 0 means unpinned.
type dependencyRef struct {
	Name    string `json:"name"`
	Version int64  `json:"version"`
}

// frontmatter is the validated Agent Skills metadata of one SKILL.md.
type frontmatter struct {
	Name         string
	Description  string
	License      string
	AllowedTools []string
	Dependencies []dependencyRef
}

var (
	skillNamePattern  = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	dependencyPattern = regexp.MustCompile(`^([a-z0-9]+(-[a-z0-9]+)*)(@([1-9][0-9]*))?$`)
)

// parseFrontmatter extracts and validates the frontmatter block of a
// SKILL.md document and returns the instruction body that follows it.
func parseFrontmatter(data []byte) (*frontmatter, []byte, error) {
	text := string(data)
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return nil, nil, invalidInput("SKILL.md must begin with a --- frontmatter block")
	}
	rest := strings.TrimPrefix(text[4:], "\r")

	// Locate the closing delimiter line. Only the frontmatter block is
	// bounded here; the instruction body is bounded by the archive limits.
	var lines []string
	bodyOffset := 0
	frontmatterBytes := 0
	closed := false
	for _, line := range strings.SplitAfter(rest, "\n") {
		if line == "" {
			break
		}
		frontmatterBytes += len(line)
		if frontmatterBytes > maxFrontmatterBytes {
			return nil, nil, invalidInput("SKILL.md frontmatter exceeds the %d byte limit", maxFrontmatterBytes)
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "---" {
			bodyOffset += len(line)
			closed = true
			break
		}
		if len(lines) >= maxFrontmatterLines {
			return nil, nil, invalidInput("SKILL.md frontmatter exceeds %d lines", maxFrontmatterLines)
		}
		lines = append(lines, trimmed)
		bodyOffset += len(line)
	}
	if !closed {
		return nil, nil, invalidInput("SKILL.md frontmatter is not closed with a --- line")
	}

	fm := &frontmatter{AllowedTools: []string{}, Dependencies: []dependencyRef{}}
	seen := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || key != strings.TrimSpace(key) || strings.TrimSpace(key) != key {
			return nil, nil, invalidInput("SKILL.md frontmatter line %d is not a strict key: value pair", i+1)
		}
		key = strings.TrimSpace(key)
		if !knownFrontmatterKey(key) {
			return nil, nil, invalidInput("SKILL.md frontmatter key %q is not supported", key)
		}
		if seen[key] {
			return nil, nil, invalidInput("SKILL.md frontmatter key %q is duplicated", key)
		}
		seen[key] = true

		switch key {
		case "dependencies":
			if strings.TrimSpace(value) != "" {
				return nil, nil, invalidInput("SKILL.md frontmatter key %q takes a block list, not an inline value", key)
			}
			// Block list: subsequent "- item" lines belong to this key.
			for i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "- ") {
				i++
				item := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "- "))
				ref, err := parseDependency(item)
				if err != nil {
					return nil, nil, err
				}
				fm.Dependencies = append(fm.Dependencies, ref)
			}
		default:
			value = strings.TrimSpace(value)
			if value == "" {
				return nil, nil, invalidInput("SKILL.md frontmatter key %q requires a value", key)
			}
			if err := applyFrontmatterScalar(fm, key, value); err != nil {
				return nil, nil, err
			}
		}
	}

	if fm.Name == "" {
		return nil, nil, invalidInput("SKILL.md frontmatter requires a name")
	}
	if !skillNamePattern.MatchString(fm.Name) || len(fm.Name) > maxNameLength {
		return nil, nil, invalidInput("SKILL.md frontmatter name must be lowercase hyphenated, at most %d characters", maxNameLength)
	}
	if fm.Description == "" {
		return nil, nil, invalidInput("SKILL.md frontmatter requires a description")
	}
	if len(fm.Description) > maxDescriptionLength {
		return nil, nil, invalidInput("SKILL.md frontmatter description exceeds %d characters", maxDescriptionLength)
	}
	if len(fm.Dependencies) > maxDependencyCount {
		return nil, nil, invalidInput("SKILL.md frontmatter declares more than %d dependencies", maxDependencyCount)
	}

	// The instruction body follows the closing delimiter; it is retained
	// byte-exact and treated as untrusted data.
	body := []byte(rest[bodyOffset:])
	return fm, body, nil
}

// knownFrontmatterKey reports whether the key is part of the supported
// published metadata set.
func knownFrontmatterKey(key string) bool {
	switch key {
	case "name", "description", "license", "allowed-tools", "dependencies":
		return true
	}
	return false
}

// applyFrontmatterScalar stores one scalar frontmatter value.
func applyFrontmatterScalar(fm *frontmatter, key, value string) error {
	switch key {
	case "name":
		fm.Name = value
	case "description":
		fm.Description = value
	case "license":
		if len(value) > 8192 {
			return invalidInput("SKILL.md frontmatter license exceeds 8192 characters")
		}
		fm.License = value
	case "allowed-tools":
		tools, err := parseAllowedTools(value)
		if err != nil {
			return err
		}
		fm.AllowedTools = tools
	}
	return nil
}

// parseAllowedTools splits the comma-separated allowed-tools declaration
// into pinned tool requirement strings. Entries are requirement names, never
// grants.
func parseAllowedTools(value string) ([]string, error) {
	parts := strings.Split(value, ",")
	tools := make([]string, 0, len(parts))
	for _, p := range parts {
		tool := strings.TrimSpace(p)
		if tool == "" {
			return nil, invalidInput("SKILL.md allowed-tools contains an empty entry")
		}
		if len(tool) > maxToolLength {
			return nil, invalidInput("SKILL.md allowed-tools entry exceeds %d characters", maxToolLength)
		}
		tools = append(tools, tool)
	}
	if len(tools) > maxToolCount {
		return nil, invalidInput("SKILL.md allowed-tools declares more than %d tools", maxToolCount)
	}
	return tools, nil
}

// parseDependency parses one dependency list entry: name or name@version.
func parseDependency(item string) (dependencyRef, error) {
	m := dependencyPattern.FindStringSubmatch(item)
	if m == nil {
		return dependencyRef{}, invalidInput("SKILL.md dependency %q is not a skill name or name@version reference", truncateForMessage(item))
	}
	ref := dependencyRef{Name: m[1]}
	if m[4] != "" {
		version, err := strconv.ParseInt(m[4], 10, 64)
		if err != nil {
			return dependencyRef{}, invalidInput("SKILL.md dependency %q carries an out-of-range version", truncateForMessage(item))
		}
		ref.Version = version
	}
	return ref, nil
}

// truncateForMessage bounds untrusted text used inside fault messages so
// oversized content cannot bloat the result envelope.
func truncateForMessage(s string) string {
	const max = 64
	if len(s) <= max {
		return s
	}
	return s[:max]
}
