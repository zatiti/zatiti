package mcp

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

// This file parses this package's own embedded AGENTS.md ("## Exact
// operation and dependency schemas") into the real, frozen revision-3
// public operation catalog, for this package's own tests only.
//
// P40's allowed writes are internal/mcp only: building a real
// *registry.Registry (internal/registry.New) requires every domain module's
// contract.Module implementation to satisfy its "every catalog operation is
// registered by exactly one module" completeness check, which this package
// cannot supply without importing sibling domains (out of scope, and
// integration-owned per AGENTS.md's "Completion policy"). Hand-writing 200+
// duplicate descriptor fixtures would drift from the frozen contract the
// moment it changes. Parsing the one frozen document this package already
// embeds (agentsMD, declared in defs_test.go) keeps the acceptance fixture
// byte-driven from the exact same contract TestEmbeddedDefsMatchContract
// already proves has not drifted, so a catalog-shape change fails this
// parser loudly instead of silently testing a stale shadow copy.
var (
	catalogHeaderRe = regexp.MustCompile("^`([a-zA-Z0-9_.]+)` v(\\d+) — (\\w+) / (public|internal) / (query|mutation) / (\\S+)")
	catalogCLIRe    = regexp.MustCompile(`(?m)^CLI ` + "`zatiti ([^`]*)`; MCP `([^`]+)`" + `\. Submission key: (required|not required[^\n.]*)\.`)
	catalogInputRe  = regexp.MustCompile("(?s)Input schema:\n```json\n(.*?)\n```")
	catalogOutputRe = regexp.MustCompile("(?s)Output data schema:\n```json\n(.*?)\n```")
)

// CatalogDescriptorsForTest returns every operation in AGENTS.md's "Exact
// operation and dependency schemas" section as a contract.Descriptor, in
// document order. Every entry there is revision-3 public (confirmed by
// exhaustively matching every "### `id` v..." header), matching
// docs/implementation/operations.json's own public surface. It fails loudly
// (via the returned error, never a partial result) if the document's shape
// no longer matches what this parser expects, so a future contract
// revision cannot silently leave this fixture stale.
func CatalogDescriptorsForTest() ([]contract.Descriptor, error) {
	text := string(agentsMD)
	start := strings.Index(text, "## Exact operation and dependency schemas")
	if start < 0 {
		return nil, fmt.Errorf("AGENTS.md: no %q heading", "## Exact operation and dependency schemas")
	}
	end := strings.Index(text, "## Named acceptance cases")
	if end < 0 || end < start {
		return nil, fmt.Errorf("AGENTS.md: no %q heading after the operation schemas", "## Named acceptance cases")
	}
	section := text[start:end]

	blocks := strings.Split(section, "\n### ")
	if len(blocks) < 2 {
		return nil, fmt.Errorf("AGENTS.md: no \"### \" operation entries found in the exact operation schemas section")
	}
	blocks = blocks[1:] // drop the section's own heading line/preamble

	descriptors := make([]contract.Descriptor, 0, len(blocks))
	for _, block := range blocks {
		header, rest, _ := strings.Cut(block, "\n")
		hm := catalogHeaderRe.FindStringSubmatch(header)
		if hm == nil {
			if header == "Local schema definitions" {
				continue // not an operation entry; defs_test.go covers it
			}
			return nil, fmt.Errorf("AGENTS.md: unrecognized entry header %q", header)
		}
		id, owner, visibility, mode, effect := hm[1], hm[3], hm[4], hm[5], hm[6]
		version, err := strconv.ParseInt(hm[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("AGENTS.md: operation %s: invalid version %q: %w", id, hm[2], err)
		}

		cm := catalogCLIRe.FindStringSubmatch(rest)
		if cm == nil {
			return nil, fmt.Errorf("AGENTS.md: operation %s: no CLI/MCP declaration line", id)
		}
		im := catalogInputRe.FindStringSubmatch(rest)
		if im == nil {
			return nil, fmt.Errorf("AGENTS.md: operation %s: no \"Input schema:\" block", id)
		}
		om := catalogOutputRe.FindStringSubmatch(rest)
		if om == nil {
			return nil, fmt.Errorf("AGENTS.md: operation %s: no \"Output data schema:\" block", id)
		}
		if !json.Valid([]byte(im[1])) {
			return nil, fmt.Errorf("AGENTS.md: operation %s: input schema is not valid JSON", id)
		}
		if !json.Valid([]byte(om[1])) {
			return nil, fmt.Errorf("AGENTS.md: operation %s: output schema is not valid JSON", id)
		}

		descriptors = append(descriptors, contract.Descriptor{
			ID:            id,
			Version:       version,
			Owner:         owner,
			Visibility:    visibility,
			Mode:          mode,
			Effect:        effect,
			InputSchema:   json.RawMessage(im[1]),
			OutputSchema:  json.RawMessage(om[1]),
			CLI:           strings.Fields(cm[1]),
			MCP:           cm[2],
			SubmissionKey: strings.TrimSpace(cm[3]) == "required",
		})
	}
	if len(descriptors) == 0 {
		return nil, fmt.Errorf("AGENTS.md: parsed zero operations from the exact operation schemas section")
	}
	return descriptors, nil
}
