package registry

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/zatiti/zatiti/internal/contract"
)

// catalog.json is the frozen public operation catalog extracted from the
// frozen implementation contract: every public operation's surface (owner,
// mode, effect, CLI tokens, MCP name, submission-key and expected-version
// semantics, scope requirement and input/output/completion schemas) plus the
// shared $defs object every schema resolves against. catalog_test.go re-parses
// AGENTS.md and asserts this file matches it, so the embedded catalog cannot
// drift from the committed contract.
//
//go:embed catalog.json
var catalogBytes []byte

// catalogOperation is one frozen operation entry.
type catalogOperation struct {
	ID              string          `json:"id"`
	Version         int64           `json:"version"`
	Owner           string          `json:"owner"`
	Visibility      string          `json:"visibility"`
	Mode            string          `json:"mode"`
	Effect          string          `json:"effect"`
	CLI             []string        `json:"cli"`
	MCP             string          `json:"mcp"`
	SubmissionKey   bool            `json:"submission_key"`
	Description     string          `json:"description"`
	Input           json.RawMessage `json:"input"`
	Output          json.RawMessage `json:"output"`
	Completion      json.RawMessage `json:"completion"`
	ExpectedVersion bool            `json:"expected_version"`
	ScopeRequired   []string        `json:"scope_required"`
}

// catalogDocument is the embedded catalog file. Defs holds the shared
// schema document: {"$defs": {...}}.
type catalogDocument struct {
	Revision   int                `json:"revision"`
	Defs       json.RawMessage    `json:"defs"`
	Operations []catalogOperation `json:"operations"`
}

// catalog is the parsed embedded catalog. Operations are sorted by ID.
type catalog struct {
	document catalogDocument
	byID     map[string]*catalogOperation

	// defsJSON holds the shared definition schemas themselves — the object
	// the document's "defs" field wraps under its "$defs" key. mergedSchema
	// injects exactly these bytes so "#/$defs/Name" references resolve at
	// the merged document's root.
	defsJSON json.RawMessage

	sharedDefs map[string]any // decoded $defs name -> schema
}

var (
	catalogOnce sync.Once
	catalogRef  *catalog
	catalogErr  error
)

// loadCatalog parses the embedded catalog.json exactly once. A failure is a
// build-time defect surfaced on every use, not silently ignored.
func loadCatalog() (*catalog, error) {
	catalogOnce.Do(func() {
		catalogRef, catalogErr = parseCatalog(catalogBytes)
	})
	return catalogRef, catalogErr
}

func parseCatalog(data []byte) (*catalog, error) {
	var doc catalogDocument
	if err := contract.DecodeStrict(data, &doc); err != nil {
		return nil, fmt.Errorf("catalog is not strict JSON: %w", err)
	}
	if doc.Revision < 1 {
		return nil, fmt.Errorf("catalog revision %d must be >= 1", doc.Revision)
	}
	var defsWrapper map[string]json.RawMessage
	if err := contract.DecodeStrict(doc.Defs, &defsWrapper); err != nil {
		return nil, fmt.Errorf("shared definitions are not strict JSON: %w", err)
	}
	inner, ok := defsWrapper["$defs"]
	if !ok {
		return nil, fmt.Errorf("shared definitions must hold a $defs object")
	}
	var defs map[string]any
	if err := contract.DecodeStrict(inner, &defs); err != nil {
		return nil, fmt.Errorf("shared definitions are not strict JSON: %w", err)
	}
	c := &catalog{
		document:   doc,
		byID:       make(map[string]*catalogOperation, len(doc.Operations)),
		defsJSON:   append(json.RawMessage(nil), inner...),
		sharedDefs: defs,
	}
	for i := range c.document.Operations {
		op := &c.document.Operations[i]
		// A frozen "null" completion schema means the operation declares no
		// eventual job result; normalize it to absent so presence checks and
		// schema merging see an empty value, not the JSON literal.
		if string(op.Completion) == "null" {
			op.Completion = nil
		}
		if _, dup := c.byID[op.ID]; dup {
			return nil, fmt.Errorf("catalog holds operation %s twice", op.ID)
		}
		c.byID[op.ID] = op
	}
	return c, nil
}

// descriptor materializes the frozen contract.Descriptor for the entry.
func (op *catalogOperation) descriptor() contract.Descriptor {
	return contract.Descriptor{
		ID:               op.ID,
		Version:          op.Version,
		Owner:            op.Owner,
		Visibility:       op.Visibility,
		Mode:             op.Mode,
		Effect:           op.Effect,
		InputSchema:      json.RawMessage(op.Input),
		OutputSchema:     json.RawMessage(op.Output),
		CompletionSchema: json.RawMessage(op.Completion),
		CLI:              append([]string(nil), op.CLI...),
		MCP:              op.MCP,
		ScopeRequired:    append([]string(nil), op.ScopeRequired...),
		ExpectedVersion:  op.ExpectedVersion,
		SubmissionKey:    op.SubmissionKey,
	}
}

// cliTokensFor derives the frozen CLI token sequence for an operation ID.
// Two operations are the declared exceptions; every other ID maps its
// dot-separated segments directly.
func cliTokensFor(id string) []string {
	switch id {
	case "installation.init":
		return []string{"init"}
	case "capabilities.list":
		return []string{"capabilities"}
	default:
		return strings.Split(id, ".")
	}
}

// mcpNameFor derives the frozen MCP tool name for an operation ID.
func mcpNameFor(id string) string {
	switch id {
	case "installation.init":
		return "zatiti_installation_init"
	case "capabilities.list":
		return "zatiti_capabilities"
	default:
		return "zatiti_" + strings.ReplaceAll(id, ".", "_")
	}
}

// localIOOperations is the frozen set of operations that route through the
// LocalIO Prepare/Perform/Finish seam. Only registered operations in this
// set whose owning module implements contract.LocalIO are routed there.
var localIOOperations = map[string]bool{
	"artifact.upload.chunk":     true,
	"artifact.upload.finish":    true,
	"artifact.upload.cancel":    true,
	"artifact.read":             true,
	"artifact.export":           true,
	"skill.import":              true,
	"connection.setup.begin":    true,
	"connection.setup.complete": true,
	"connection.setup.cancel":   true,
	"installation.init":         true,
	"installation.backup":       true,
	"installation.restore":      true,
}

// mergedSchema returns the operation schema extended with the shared $defs
// object so document-local $refs resolve. Operation schemas never carry
// their own $defs (checked here) and never use root pointers.
func mergedSchema(shared json.RawMessage, opSchema json.RawMessage) (json.RawMessage, error) {
	if len(opSchema) == 0 {
		return nil, fmt.Errorf("operation schema is empty")
	}
	var opRoot map[string]json.RawMessage
	if err := contract.DecodeStrict(opSchema, &opRoot); err != nil {
		return nil, fmt.Errorf("operation schema is not a strict JSON object: %w", err)
	}
	if _, exists := opRoot["$defs"]; exists {
		return nil, fmt.Errorf("operation schema must not declare its own $defs; the shared definitions are merged in")
	}
	opRoot["$defs"] = json.RawMessage(shared)
	return json.Marshal(opRoot)
}
