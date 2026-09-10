package registry

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

//go:embed AGENTS.md
var agentsMD []byte

// The embedded catalog.json must never drift from the frozen implementation
// contract in AGENTS.md. This file re-parses the contract document with the
// same rules the catalog was extracted with and asserts that the committed
// catalog is exactly the frozen surface: same operations, same derived
// fields, same schemas, same shared definitions.

var (
	catalogHeading = regexp.MustCompile(`^### ` + "`" + `([a-z0-9_.]+)` + "`" + ` v(\d+) — ([a-z][a-z0-9_]*) / (public|internal) / (query|mutation) / (local|disclosure|external_read|external_mutation)$`)
	catalogCLIMCP  = regexp.MustCompile(`^CLI ` + "`" + `([^` + "`" + `]+)` + "`" + `; MCP ` + "`" + `([^` + "`" + `]+)` + "`" + `\. Submission key: (required|not required)\b`)
)

// mustInt parses one decimal integer from the contract document.
func mustInt(t *testing.T, raw, context string) int64 {
	t.Helper()
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		t.Fatalf("contract: %s: %q is not an integer: %v", context, raw, err)
	}
	return v
}

// parsedOperation is one operation as declared in the contract document.
type parsedOperation struct {
	id              string
	version         int64
	owner           string
	visibility      string
	mode            string
	effect          string
	cli             []string
	mcp             string
	submissionKey   bool
	description     string
	input           json.RawMessage
	output          json.RawMessage
	completion      json.RawMessage
	expectedVersion bool
	scopeRequired   []string
}

// parseContractCatalog re-derives the frozen catalog from AGENTS.md.
func parseContractCatalog(t *testing.T) ([]parsedOperation, map[string]any) {
	t.Helper()
	lines := strings.Split(string(agentsMD), "\n")

	// Shared $defs block under its frozen heading. The document is decoded
	// twice: strictly, to reject duplicate or unknown members, and with
	// UseNumber, so every number survives the rebuild exactly.
	var defs map[string]any
	defsFound := false
	for i, line := range lines {
		if line != "### Local schema definitions" {
			continue
		}
		raw := fencedBlock(t, lines, i+1, "local schema definitions")
		var strict map[string]map[string]any
		if err := contract.DecodeStrict(raw, &strict); err != nil {
			t.Fatalf("contract: shared $defs block is not strict JSON: %v", err)
		}
		if len(strict) != 1 {
			t.Fatalf("contract: local schema definitions must hold only $defs, got %d keys", len(strict))
		}
		defs, defsFound = decodeUseNumber(t, raw).(map[string]any)["$defs"].(map[string]any)
		if !defsFound {
			t.Fatal("contract: shared definitions block does not hold a $defs object")
		}
		break
	}
	if !defsFound {
		t.Fatal("contract: no local schema definitions block found")
	}

	// One section per operation: its heading and everything up to the next
	// heading. Parsing inside bounded sections keeps the block extractor's
	// cursor from ever crossing an operation boundary and swallowing the
	// heading that follows an operation without a completion block.
	var heads []int
	for i, line := range lines {
		if catalogHeading.MatchString(line) {
			heads = append(heads, i)
		}
	}

	var ops []parsedOperation
	for h, head := range heads {
		end := len(lines)
		if h+1 < len(heads) {
			end = heads[h+1]
		}
		section := lines[head:end]
		match := catalogHeading.FindStringSubmatch(section[0])
		op := parsedOperation{id: match[1]}
		op.version = mustInt(t, match[2], op.id)
		op.owner, op.visibility, op.mode, op.effect = match[3], match[4], match[5], match[6]

		i := 1
		for i < len(section) && strings.TrimSpace(section[i]) == "" {
			i++
		}
		cm := catalogCLIMCP.FindStringSubmatch(section[i])
		if cm == nil {
			t.Fatalf("contract: operation %s: missing CLI/MCP line: %q", op.id, section[i])
		}
		op.cli = strings.Fields(cm[1])[1:] // drop the binary name
		op.mcp = cm[2]
		op.submissionKey = cm[3] == "required"
		i++

		var description []string
		for i < len(section) && !strings.HasPrefix(section[i], "Input schema:") {
			if strings.TrimSpace(section[i]) != "" {
				description = append(description, strings.TrimSpace(section[i]))
			}
			i++
		}
		op.description = strings.Join(description, " ")
		op.input = contractBlock(t, section, &i, "Input schema:", op.id)
		op.output = contractBlock(t, section, &i, "Output data schema:", op.id)
		op.completion = contractBlock(t, section, &i, "Eventual job result schema (job.get resource.result):", op.id)

		// Derived semantics are frozen by the shared contract: expected
		// versions and scope requirements come from the input schema, mode
		// binds submission keys except the one-time init.
		var input map[string]any
		decoded, ok := decodeUseNumber(t, op.input).(map[string]any)
		if !ok {
			t.Fatalf("contract: operation %s: input schema is not a JSON object", op.id)
		}
		input = decoded
		if required, ok := input["required"].([]any); ok {
			for _, entry := range required {
				s, _ := entry.(string)
				if s == "expected_version" {
					op.expectedVersion = true
				}
				if s == "scope" {
					op.scopeRequired = []string{"installation_id"}
				}
			}
		}
		if op.scopeRequired == nil {
			op.scopeRequired = []string{}
		}
		if op.mode == contract.ModeQuery && op.submissionKey {
			t.Fatalf("contract: %s: query must not require a submission key", op.id)
		}
		if op.mode == contract.ModeMutation && !op.submissionKey && op.id != "installation.init" {
			t.Fatalf("contract: mutation %s requires a submission key except one-time init", op.id)
		}
		// Derived CLI/MCP names must equal the declared mappings.
		if wantCLI := cliTokensFor(op.id); !slices.Equal(op.cli, wantCLI) {
			t.Fatalf("contract: operation %s: CLI %v does not match the derived %v", op.id, op.cli, wantCLI)
		}
		if wantMCP := mcpNameFor(op.id); op.mcp != wantMCP {
			t.Fatalf("contract: operation %s: MCP %q does not match the derived %q", op.id, op.mcp, wantMCP)
		}
		ops = append(ops, op)
	}

	// Uniqueness of IDs, CLI token lists and MCP names across the surface,
	// and every document-local $ref resolves against the shared definitions.
	seenID := map[string]bool{}
	seenCLI := map[string]string{}
	seenMCP := map[string]string{}
	for i := range ops {
		op := &ops[i]
		key := op.id + " v" + strconv.FormatInt(op.version, 10)
		if seenID[key] {
			t.Fatalf("contract: duplicate operation %s", key)
		}
		seenID[key] = true
		if prev, dup := seenCLI[strings.Join(op.cli, " ")]; dup {
			t.Fatalf("contract: CLI mapping collision: %s and %s", op.id, prev)
		}
		seenCLI[strings.Join(op.cli, " ")] = op.id
		if prev, dup := seenMCP[op.mcp]; dup {
			t.Fatalf("contract: MCP name collision: %s and %s", op.id, prev)
		}
		seenMCP[op.mcp] = op.id
		for _, schema := range []json.RawMessage{op.input, op.output, op.completion} {
			for _, ref := range schemaRefs(t, schema) {
				if _, ok := defs[ref]; !ok {
					t.Fatalf("contract: operation %s: $ref #/$defs/%s is missing from the shared definitions", op.id, ref)
				}
			}
		}
	}
	if len(ops) == 0 {
		t.Fatal("contract: no operations found in the contract document")
	}
	return ops, defs
}

// contractBlock extracts the fenced JSON block following a label line. A
// missing block (a nil result) is valid: only the eventual job result schema
// is optional.
func contractBlock(t *testing.T, lines []string, i *int, label, id string) json.RawMessage {
	for *i < len(lines) && !strings.HasPrefix(lines[*i], label) && !strings.HasPrefix(lines[*i], "### ") {
		*i++
	}
	if *i >= len(lines) || !strings.HasPrefix(lines[*i], label) {
		return nil
	}
	return json.RawMessage(fencedContent(t, lines, *i+1))
}

// fencedContent returns the content of the JSON fence starting after line at.
func fencedContent(t *testing.T, lines []string, at int) string {
	t.Helper()
	j := at
	for j < len(lines) && !strings.HasPrefix(lines[j], "```json") {
		if strings.HasPrefix(lines[j], "### ") || strings.HasPrefix(lines[j], "```") {
			t.Fatalf("contract: no json fence where one is required (line %d)", j+1)
		}
		j++
	}
	var buf []string
	j++
	for j < len(lines) && !strings.HasPrefix(lines[j], "```") {
		buf = append(buf, lines[j])
		j++
	}
	if len(buf) == 0 {
		t.Fatal("contract: fenced json block is empty")
	}
	return strings.Join(buf, "\n")
}

// fencedBlock is fencedContent addressed by heading position.
func fencedBlock(t *testing.T, lines []string, at int, what string) []byte {
	t.Helper()
	for at < len(lines) && !strings.HasPrefix(lines[at], "```json") {
		at++
	}
	if at >= len(lines) {
		t.Fatalf("contract: %s fence is missing", what)
	}
	return []byte(fencedContent(t, lines, at))
}

// decodeUseNumber decodes JSON preserving integer literals exactly.
func decodeUseNumber(t *testing.T, data []byte) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("contract: invalid JSON: %v", err)
	}
	return v
}

// schemaRefs lists every #/$defs/Name reference inside one schema.
func schemaRefs(t *testing.T, schema json.RawMessage) []string {
	if len(schema) == 0 {
		return nil
	}
	var refs []string
	var walk func(node any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			if ref, ok := n["$ref"].(string); ok && strings.HasPrefix(ref, "#/$defs/") {
				refs = append(refs, strings.TrimPrefix(ref, "#/$defs/"))
			}
			for _, value := range n {
				walk(value)
			}
		case []any:
			for _, value := range n {
				walk(value)
			}
		}
	}
	walk(decodeUseNumber(t, schema))
	return refs
}

// marshalCanonical renders v as canonical JSON: sorted keys at every level,
// exact number literals, no insignificant whitespace.
func marshalCanonical(t *testing.T, v any) []byte {
	t.Helper()
	compact, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("value does not marshal: %v", err)
	}
	canonical, err := contract.Canonicalize(compact)
	if err != nil {
		t.Fatalf("value does not canonicalize: %v", err)
	}
	return canonical
}

// TestCatalogMatchesFrozenContract rebuilds the catalog from the contract
// document and compares it with the committed catalog.json. The comparison
// is canonical: identical content in any key order or whitespace matches;
// any content difference fails.
func TestCatalogMatchesFrozenContract(t *testing.T) {
	ops, defs := parseContractCatalog(t)
	slices.SortFunc(ops, func(a, b parsedOperation) int { return strings.Compare(a.id, b.id) })

	cat, err := loadCatalog()
	if err != nil {
		t.Fatalf("committed catalog does not parse: %v", err)
	}
	if cat.document.Revision != 1 {
		t.Fatalf("committed catalog revision %d, want 1", cat.document.Revision)
	}

	// Operation-for-operation equality first, so drift names the operation
	// instead of failing an opaque whole-document comparison.
	if len(ops) != len(cat.document.Operations) {
		t.Fatalf("committed catalog holds %d operations, contract declares %d", len(cat.document.Operations), len(ops))
	}
	for i := range ops {
		op := &ops[i]
		frozen := cat.byID[op.id]
		if frozen == nil {
			t.Fatalf("committed catalog is missing operation %s", op.id)
		}
		expected := marshalCanonical(t, map[string]any{
			"id":               op.id,
			"version":          op.version,
			"owner":            op.owner,
			"visibility":       op.visibility,
			"mode":             op.mode,
			"effect":           op.effect,
			"cli":              op.cli,
			"mcp":              op.mcp,
			"submission_key":   op.submissionKey,
			"description":      op.description,
			"input":            op.input,
			"output":           op.output,
			"completion":       op.completion,
			"expected_version": op.expectedVersion,
			"scope_required":   op.scopeRequired,
		})
		actual := marshalCanonical(t, *frozen)
		if string(expected) != string(actual) {
			t.Fatalf("operation %s drifted: first difference at byte %d\nexpected: %s\nactual:   %s",
				op.id, firstDiff(expected, actual), expected, actual)
		}
	}

	// Definition-for-definition equality, decoded with UseNumber so integer
	// literals compare exactly.
	committedDefs, ok := decodeUseNumber(t, cat.document.Defs).(map[string]any)["$defs"].(map[string]any)
	if !ok {
		t.Fatal("committed catalog does not hold a $defs object")
	}
	if len(defs) != len(committedDefs) {
		t.Fatalf("committed catalog holds %d shared definitions, contract declares %d", len(committedDefs), len(defs))
	}
	for name, want := range defs {
		got, ok := committedDefs[name]
		if !ok {
			t.Fatalf("committed catalog is missing shared definition %s", name)
		}
		wantBytes := marshalCanonical(t, want)
		gotBytes := marshalCanonical(t, got)
		if string(wantBytes) != string(gotBytes) {
			t.Fatalf("shared definition %s drifted: first difference at byte %d\nexpected: %s\nactual:   %s",
				name, firstDiff(wantBytes, gotBytes), wantBytes, gotBytes)
		}
	}

	// Finally the committed file must be exactly the frozen document: same
	// revision, operations and definitions, in canonical form.
	expectedOps := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		expectedOps = append(expectedOps, map[string]any{
			"id":               op.id,
			"version":          op.version,
			"owner":            op.owner,
			"visibility":       op.visibility,
			"mode":             op.mode,
			"effect":           op.effect,
			"cli":              op.cli,
			"mcp":              op.mcp,
			"submission_key":   op.submissionKey,
			"description":      op.description,
			"input":            op.input,
			"output":           op.output,
			"completion":       op.completion,
			"expected_version": op.expectedVersion,
			"scope_required":   op.scopeRequired,
		})
	}
	expected := marshalCanonical(t, map[string]any{
		"revision":   1,
		"defs":       map[string]any{"$defs": defs},
		"operations": expectedOps,
	})
	actual := marshalCanonical(t, json.RawMessage(catalogBytes))
	if string(expected) != string(actual) {
		at := firstDiff(expected, actual)
		t.Fatalf("committed catalog.json drifted from the frozen contract in AGENTS.md "+
			"(first difference at byte %d; expected ...%s...; actual ...%s...)",
			at, contextAt(expected, at), contextAt(actual, at))
	}
}

// firstDiff returns the index of the first differing byte, or the shared
// length when one value is a prefix of the other.
func firstDiff(a, b []byte) int {
	at := 0
	for at < len(a) && at < len(b) && a[at] == b[at] {
		at++
	}
	return at
}

// contextAt renders a bounded window around an offset for failure messages.
func contextAt(b []byte, at int) string {
	lo := at - 80
	if lo < 0 {
		lo = 0
	}
	hi := at + 80
	if hi > len(b) {
		hi = len(b)
	}
	return string(b[lo:hi])
}

// TestContractOperationsAreWellFormed asserts the contract document itself
// holds a coherent frozen surface: unique IDs, unique derived CLI/MCP
// mappings, submission-key semantics and closed schema references.
func TestContractOperationsAreWellFormed(t *testing.T) {
	ops, defs := parseContractCatalog(t)
	if len(ops) < 100 {
		t.Fatalf("contract declares %d operations, far fewer than the frozen surface", len(ops))
	}
	publicCount := 0
	for i := range ops {
		if ops[i].visibility == "public" {
			publicCount++
		}
	}
	if publicCount != len(ops) {
		t.Fatalf("contract declares %d public of %d operations; the frozen catalog is entirely public", publicCount, len(ops))
	}
	if len(defs) < 10 {
		t.Fatalf("contract holds %d shared definitions, far fewer than the frozen set", len(defs))
	}
}
