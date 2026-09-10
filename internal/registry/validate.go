package registry

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

// operationIDSegment matches one dot-separated operation ID segment.
// Underscores are legal inside a segment; the dot/underscore collision
// safety comes from the derived CLI/MCP name uniqueness checks, not from
// the character set.
var operationIDSegment = regexp.MustCompile(`^[a-z0-9_]+$`)

// ownerName matches a module/owner name: a lowercase Go-identifier-like
// token usable as an SQL table prefix.
var ownerNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func validOperationID(id string) bool {
	if id == "" {
		return false
	}
	for _, seg := range strings.Split(id, ".") {
		if !operationIDSegment.MatchString(seg) {
			return false
		}
	}
	return true
}

func validOwnerName(name string) bool {
	return ownerNamePattern.MatchString(name)
}

// validateDescriptorShape checks the structural invariants every descriptor
// must hold regardless of visibility: well-formed ID, version >= 1, known
// visibility, mode and effect values, non-empty schemas and the
// mode/submission-key semantics (queries never require a submission key;
// mutations do, except the one-time init).
func validateDescriptorShape(d *contract.Descriptor) error {
	if !validOperationID(d.ID) {
		return fmt.Errorf("operation ID %q is not a dot-separated lowercase token sequence", d.ID)
	}
	if d.Version < 1 {
		return fmt.Errorf("operation version %d must be >= 1", d.Version)
	}
	if !validOwnerName(d.Owner) {
		return fmt.Errorf("owner %q is not a valid owner name", d.Owner)
	}
	switch d.Visibility {
	case contract.VisibilityPublic, contract.VisibilityInternal:
	default:
		return fmt.Errorf("visibility %q must be public or internal", d.Visibility)
	}
	switch d.Mode {
	case contract.ModeQuery, contract.ModeMutation:
	default:
		return fmt.Errorf("mode %q must be query or mutation", d.Mode)
	}
	switch d.Effect {
	case contract.EffectLocal, contract.EffectDisclosure,
		contract.EffectExternalRead, contract.EffectExternalMutation:
	default:
		return fmt.Errorf("effect %q must be local, disclosure, external_read or external_mutation", d.Effect)
	}
	if len(d.InputSchema) == 0 {
		return fmt.Errorf("operation %s: input schema is required", d.ID)
	}
	if len(d.OutputSchema) == 0 {
		return fmt.Errorf("operation %s: output schema is required", d.ID)
	}
	switch {
	case d.Mode == contract.ModeQuery && d.SubmissionKey:
		return fmt.Errorf("query operation must not require a submission key")
	case d.Mode == contract.ModeMutation && d.SubmissionKey && d.ID == "installation.init":
		return fmt.Errorf("one-time init never requires a submission key")
	case d.Mode == contract.ModeMutation && !d.SubmissionKey && d.ID != "installation.init":
		return fmt.Errorf("mutation operation requires a submission key")
	}
	for _, caller := range d.Callers {
		if !validOperationID(caller) {
			return fmt.Errorf("operation %s: caller %q is not a valid operation ID", d.ID, caller)
		}
	}
	return nil
}

// matchCatalog compares a public descriptor against its frozen catalog
// entry. Every frozen field must be equal; the embedded catalog is the
// single behavior authority for the public surface.
func matchCatalog(d *contract.Descriptor, frozen *catalogOperation) error {
	if d.Version != frozen.Version {
		return fmt.Errorf("version %d does not match the frozen version %d", d.Version, frozen.Version)
	}
	if d.Owner != frozen.Owner {
		return fmt.Errorf("owner %q does not match the frozen owner %q", d.Owner, frozen.Owner)
	}
	if d.Mode != frozen.Mode {
		return fmt.Errorf("mode %q does not match the frozen mode %q", d.Mode, frozen.Mode)
	}
	if d.Effect != frozen.Effect {
		return fmt.Errorf("effect %q does not match the frozen effect %q", d.Effect, frozen.Effect)
	}
	if !slices.Equal(d.CLI, frozen.CLI) {
		return fmt.Errorf("CLI mapping %v does not match the frozen mapping %v", d.CLI, frozen.CLI)
	}
	derived := cliTokensFor(d.ID)
	if !slices.Equal(derived, frozen.CLI) {
		return fmt.Errorf("frozen CLI mapping %v does not match the name derived from the operation ID %v", frozen.CLI, derived)
	}
	if d.MCP != frozen.MCP {
		return fmt.Errorf("MCP name %q does not match the frozen name %q", d.MCP, frozen.MCP)
	}
	if derivedMCP := mcpNameFor(d.ID); derivedMCP != frozen.MCP {
		return fmt.Errorf("frozen MCP name %q does not match the name derived from the operation ID %q", frozen.MCP, derivedMCP)
	}
	if d.SubmissionKey != frozen.SubmissionKey {
		return fmt.Errorf("submission-key requirement %t does not match the frozen value %t", d.SubmissionKey, frozen.SubmissionKey)
	}
	if d.ExpectedVersion != frozen.ExpectedVersion {
		return fmt.Errorf("expected-version requirement %t does not match the frozen value %t", d.ExpectedVersion, frozen.ExpectedVersion)
	}
	if !slices.Equal(d.ScopeRequired, frozen.ScopeRequired) {
		return fmt.Errorf("scope requirements %v do not match the frozen values %v", d.ScopeRequired, frozen.ScopeRequired)
	}
	if len(d.Callers) != 0 {
		return fmt.Errorf("public operation must not declare an internal caller allowlist")
	}
	if err := sameSchema(d.InputSchema, frozen.Input, "input"); err != nil {
		return err
	}
	if err := sameSchema(d.OutputSchema, frozen.Output, "output"); err != nil {
		return err
	}
	return sameSchema(d.CompletionSchema, frozen.Completion, "completion")
}

// sameSchema compares the registered schema bytes with the frozen schema
// bytes canonically. A frozen nil completion schema requires an empty
// registered schema and vice versa.
func sameSchema(registered, frozen json.RawMessage, label string) error {
	if len(registered) == 0 && len(frozen) == 0 {
		return nil
	}
	if len(registered) == 0 || len(frozen) == 0 {
		return fmt.Errorf("%s schema presence does not match the frozen catalog", label)
	}
	a, err := contract.Canonicalize(registered)
	if err != nil {
		return fmt.Errorf("%s schema: %w", label, err)
	}
	b, err := contract.Canonicalize(frozen)
	if err != nil {
		return fmt.Errorf("frozen %s schema: %w", label, err)
	}
	if string(a) != string(b) {
		return fmt.Errorf("%s schema does not match the frozen schema", label)
	}
	return nil
}

// checkSchemaDocument verifies that a schema document (the operation schema
// merged with the shared $defs) uses only document-local $refs that resolve
// inside the document. Data positions (enum/const/default/examples) are not
// schema nodes and are not walked. Cycle-safe: every node is visited once.
func checkSchemaDocument(doc json.RawMessage) error {
	var root any
	dec := json.NewDecoder(strings.NewReader(string(doc)))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return fmt.Errorf("not a strict JSON document: %w", err)
	}
	_, err := walkSchema(root, root, "#", map[string]bool{})
	return err
}

// schemaPositions maps a schema keyword to the child positions that hold
// subschemas. Keys not listed here are either data (never walked) or
// annotations.
var schemaChildPositions = map[string]string{
	"properties":           "map",
	"patternProperties":    "map",
	"dependentSchemas":     "map",
	"$defs":                "map",
	"allOf":                "list",
	"anyOf":                "list",
	"oneOf":                "list",
	"prefixItems":          "list",
	"items":                "single",
	"contains":             "single",
	"additionalProperties": "single-or-bool",
	"propertyNames":        "single",
	"not":                  "single",
	"if":                   "single",
	"then":                 "single",
	"else":                 "single",
}

// walkSchema visits every schema node in the document, verifying that each
// $ref is a document-local fragment that resolves. The visited set is keyed
// by JSON pointer so recursive $refs terminate.
func walkSchema(root, node any, path string, visited map[string]bool) (bool, error) {
	if visited[path] {
		return false, nil
	}
	switch n := node.(type) {
	case map[string]any:
		visited[path] = true
		if ref, ok := n["$ref"]; ok {
			s, ok := ref.(string)
			if !ok {
				return true, fmt.Errorf("%s: $ref must be a string", path)
			}
			if !strings.HasPrefix(s, "#") {
				return true, fmt.Errorf("%s: $ref %q is not a document-local fragment; remote references are never fetched", path, s)
			}
			target, err := resolvePointer(root, s)
			if err != nil {
				return true, fmt.Errorf("%s: $ref %q: %w", path, s, err)
			}
			if _, err := walkSchema(root, target, "ref"+strings.TrimPrefix(s, "#"), visited); err != nil {
				return true, err
			}
		}
		for key, kind := range schemaChildPositions {
			child, ok := n[key]
			if !ok {
				continue
			}
			switch kind {
			case "map":
				m, ok := child.(map[string]any)
				if !ok {
					return true, fmt.Errorf("%s/%s: expected an object of subschemas", path, key)
				}
				for name, sub := range m {
					if _, err := walkSchema(root, sub, path+"/"+key+"/"+name, visited); err != nil {
						return true, err
					}
				}
			case "list":
				list, ok := child.([]any)
				if !ok {
					return true, fmt.Errorf("%s/%s: expected an array of schemas", path, key)
				}
				for i, sub := range list {
					if _, err := walkSchema(root, sub, fmt.Sprintf("%s/%s/%d", path, key, i), visited); err != nil {
						return true, err
					}
				}
			default: // "single" and "single-or-bool"
				if m, ok := child.(map[string]any); ok {
					if _, err := walkSchema(root, m, path+"/"+key, visited); err != nil {
						return true, err
					}
				}
			}
		}
	case []any:
		visited[path] = true
		for i, sub := range n {
			if _, err := walkSchema(root, sub, fmt.Sprintf("%s/%d", path, i), visited); err != nil {
				return true, err
			}
		}
	}
	return true, nil
}

// resolvePointer resolves a document-local fragment ("#", "#/$defs/Name")
// against the root document.
func resolvePointer(root any, ref string) (any, error) {
	pointer := strings.TrimPrefix(ref, "#")
	cur := root
	if pointer == "" {
		return cur, nil
	}
	for _, raw := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		tok := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[tok]
			if !ok {
				return nil, fmt.Errorf("pointer does not resolve")
			}
			cur = next
		case []any:
			var idx int
			if _, err := fmt.Sscanf(tok, "%d", &idx); err != nil || idx < 0 || fmt.Sprintf("%d", idx) != tok {
				return nil, fmt.Errorf("pointer does not resolve")
			}
			if idx >= len(node) {
				return nil, fmt.Errorf("pointer does not resolve")
			}
			cur = node[idx]
		default:
			return nil, fmt.Errorf("pointer does not resolve")
		}
	}
	return cur, nil
}

// cloneDescriptor deep-copies the mutable slice fields of a descriptor.
func cloneDescriptor(d *contract.Descriptor) contract.Descriptor {
	return contract.Descriptor{
		ID:               d.ID,
		Version:          d.Version,
		Owner:            d.Owner,
		Visibility:       d.Visibility,
		Mode:             d.Mode,
		Effect:           d.Effect,
		InputSchema:      append(json.RawMessage(nil), d.InputSchema...),
		OutputSchema:     append(json.RawMessage(nil), d.OutputSchema...),
		CompletionSchema: append(json.RawMessage(nil), d.CompletionSchema...),
		CLI:              append([]string(nil), d.CLI...),
		MCP:              d.MCP,
		ScopeRequired:    append([]string(nil), d.ScopeRequired...),
		Callers:          append([]string(nil), d.Callers...),
		ExpectedVersion:  d.ExpectedVersion,
		SubmissionKey:    d.SubmissionKey,
	}
}
