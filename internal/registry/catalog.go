package registry

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
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
	// the document's "defs" field wraps under its "$defs" key.
	defsJSON json.RawMessage

	sharedDefs map[string]any // decoded $defs name -> schema

	// sharedRaw and sharedCanonical hold every shared definition as embedded
	// and in canonical form; resolveSchema compares module-delivered
	// definitions against them. verified remembers the delivered spellings
	// already proven equal.
	sharedRaw       map[string]json.RawMessage
	sharedCanonical map[string]string
	verified        sync.Map
	parsedDefs      sync.Map // raw $defs JSON -> strict, immutable decoded map
}

type parsedDefinitions struct {
	definitions map[string]json.RawMessage
	err         error
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
	var sharedRaw map[string]json.RawMessage
	if err := contract.DecodeStrict(inner, &sharedRaw); err != nil {
		return nil, fmt.Errorf("shared definitions are not strict JSON: %w", err)
	}
	sharedCanonical := make(map[string]string, len(sharedRaw))
	for name, def := range sharedRaw {
		canonical, err := contract.Canonicalize(def)
		if err != nil {
			return nil, fmt.Errorf("shared definition %s: %w", name, err)
		}
		sharedCanonical[name] = string(canonical)
	}
	c := &catalog{
		document:        doc,
		byID:            make(map[string]*catalogOperation, len(doc.Operations)),
		defsJSON:        append(json.RawMessage(nil), inner...),
		sharedDefs:      defs,
		sharedRaw:       sharedRaw,
		sharedCanonical: sharedCanonical,
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

// resolveSchema accepts an operation schema in either delivered form and
// returns its bare body plus the self-contained document used wherever the
// schema is evaluated.
//
// Domain modules deliver either the bare operation body, exactly as the
// frozen catalog prints it, or a self-contained document: the body plus a
// root $defs member. A delivered definition that shares a name with a shared
// catalog definition must be canonically equal to it, so no module can
// redefine the frozen wire types; any other name is an owner-private
// definition (internal operations reference those).
//
// body is the schema without $defs: the form compared against the frozen
// catalog and published through Public, capabilities and OpenAPI. document
// is the body plus exactly the definitions reachable from it, shared or
// owner-private, so "#/$defs/Name" references resolve at its root and
// unreachable definitions are never carried along.
func (c *catalog) resolveSchema(opSchema json.RawMessage) (body, document json.RawMessage, err error) {
	if len(opSchema) == 0 {
		return nil, nil, fmt.Errorf("operation schema is empty")
	}
	opRoot, err := decodeOperationSchema(opSchema)
	if err != nil {
		return nil, nil, fmt.Errorf("operation schema is not a strict JSON object: %w", err)
	}
	private := map[string]json.RawMessage{}
	if rawDefs, ok := opRoot["$defs"]; ok {
		own, err := c.parseDefinitions(rawDefs)
		if err != nil {
			return nil, nil, fmt.Errorf("operation schema $defs is not a strict JSON object: %w", err)
		}
		for name, def := range own {
			if _, shared := c.sharedRaw[name]; !shared {
				private[name] = def
				continue
			}
			if err := c.sameAsShared(name, def); err != nil {
				return nil, nil, err
			}
		}
		delete(opRoot, "$defs")
	}
	body, err = json.Marshal(opRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("operation schema does not marshal: %w", err)
	}
	// The streaming split above leaves operation-body values as RawMessages.
	// Validate them strictly after removing the separately cached $defs block,
	// so nested duplicate keys in a body remain rejected.
	var strictBody map[string]json.RawMessage
	if err := contract.DecodeStrict(body, &strictBody); err != nil {
		return nil, nil, fmt.Errorf("operation schema body is not strict JSON: %w", err)
	}

	// Transitive closure of the definitions the body reaches.
	reachable := map[string]json.RawMessage{}
	pending, err := definitionRefs(body)
	if err != nil {
		return nil, nil, err
	}
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if _, done := reachable[name]; done {
			continue
		}
		def, ok := private[name]
		if !ok {
			def, ok = c.sharedRaw[name]
		}
		if !ok {
			// Left unresolved: checkSchemaDocument names the dangling $ref.
			continue
		}
		reachable[name] = def
		next, err := definitionRefs(def)
		if err != nil {
			return nil, nil, err
		}
		pending = append(pending, next...)
	}
	if len(reachable) != 0 {
		rawDefs, err := json.Marshal(reachable)
		if err != nil {
			return nil, nil, fmt.Errorf("definitions do not marshal: %w", err)
		}
		opRoot["$defs"] = rawDefs
	}
	document, err = json.Marshal(opRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("operation schema does not marshal: %w", err)
	}
	return body, document, nil
}

// decodeOperationSchema reads only the top-level members as RawMessages.
// Operation schemas commonly repeat a large $defs block; decoding the entire
// tree with DecodeStrict for every operation needlessly allocates a map for
// every schema node. Nested strict validation is done once for $defs by
// parseDefinitions and separately for the small operation body.
func decodeOperationSchema(data []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	first, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if first != json.Delim('{') {
		return nil, fmt.Errorf("expected object")
	}
	root := make(map[string]json.RawMessage)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("object key is not a string")
		}
		if _, duplicate := root[key]; duplicate {
			return nil, fmt.Errorf("duplicate object key %q", key)
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		root[key] = value
	}
	last, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if last != json.Delim('}') {
		return nil, fmt.Errorf("expected object end")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected trailing JSON value")
		}
		return nil, err
	}
	return root, nil
}

func (c *catalog) parseDefinitions(raw json.RawMessage) (map[string]json.RawMessage, error) {
	key := string(raw)
	if cached, ok := c.parsedDefs.Load(key); ok {
		result := cached.(parsedDefinitions)
		return result.definitions, result.err
	}
	var definitions map[string]json.RawMessage
	err := contract.DecodeStrict(raw, &definitions)
	result := parsedDefinitions{definitions: definitions, err: err}
	cached, _ := c.parsedDefs.LoadOrStore(key, result)
	result = cached.(parsedDefinitions)
	return result.definitions, result.err
}

// mergedSchema returns only the self-contained document of resolveSchema.
func (c *catalog) mergedSchema(opSchema json.RawMessage) (json.RawMessage, error) {
	_, document, err := c.resolveSchema(opSchema)
	return document, err
}

// sameAsShared verifies that a module-delivered definition equals the shared
// catalog definition of the same name. Byte-equal definitions, the common
// case, skip canonicalization; verified spellings are remembered because
// every schema of a module repeats the same $defs document.
func (c *catalog) sameAsShared(name string, def json.RawMessage) error {
	if string(def) == string(c.sharedRaw[name]) {
		return nil
	}
	key := name + "\x00" + string(def)
	if _, ok := c.verified.Load(key); ok {
		return nil
	}
	canonical, err := contract.Canonicalize(def)
	if err != nil {
		return fmt.Errorf("definition %s: %w", name, err)
	}
	if string(canonical) != c.sharedCanonical[name] {
		return fmt.Errorf("operation schema redefines the shared definition %s", name)
	}
	c.verified.Store(key, struct{}{})
	return nil
}

// definitionRefs lists the "#/$defs/Name" definitions a schema fragment
// references. Every "$ref" string is followed wherever it appears: following
// one from a data position only carries an unused definition along.
func definitionRefs(fragment json.RawMessage) ([]string, error) {
	var root any
	dec := json.NewDecoder(strings.NewReader(string(fragment)))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("not a strict JSON document: %w", err)
	}
	var names []string
	var walk func(node any)
	walk = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			if ref, ok := n["$ref"].(string); ok && strings.HasPrefix(ref, "#/$defs/") {
				name, _, _ := strings.Cut(strings.TrimPrefix(ref, "#/$defs/"), "/")
				name = strings.ReplaceAll(strings.ReplaceAll(name, "~1", "/"), "~0", "~")
				names = append(names, name)
			}
			for _, child := range n {
				walk(child)
			}
		case []any:
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(root)
	return names, nil
}
