package integration_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/zatiti/zatiti/internal/application"
	"github.com/zatiti/zatiti/internal/contract"
)

// localIOOperations is the frozen set of registered local IO operations from
// the shared contract ("Local IO, authentication, jobs and verification
// seams").
var localIOOperations = map[string]bool{
	"artifact.upload.chunk": true, "artifact.upload.finish": true,
	"artifact.upload.cancel": true, "artifact.read": true, "artifact.export": true,
	"skill.import":           true,
	"connection.setup.begin": true, "connection.setup.complete": true,
	"connection.setup.cancel": true,
	"installation.init":       true, "installation.backup": true, "installation.restore": true,
}

// moduleCatalog is the catalogDirect operation catalog: an
// application.Catalog and application.IOLookup built from nothing but the
// real modules' own Descriptors(), Handle and LocalIO seams. It carries no
// operation behavior of its own and validates nothing the application does
// not already validate. It does not serve the registry-owned capabilities
// operations, the frozen-catalog completeness check, or OpenAPI; those stay
// the real registry's job and are exercised only in catalogRegistry mode.
//
// One normalization, recorded by TestLandedDescriptorsDeliverOneSchemaForm:
// five landed domains deliver bare operation schemas whose $refs point into
// the shared $defs, which internal/application cannot validate against. For
// exactly those descriptors the catalog merges the frozen shared $defs
// document, the same merge internal/registry performs. Self-contained
// descriptors pass through untouched.
//
// It exists because internal/registry cannot currently assemble the landed
// modules (registry_seam_test.go); delete it when defaultCatalogMode flips.
type moduleCatalog struct {
	ops    map[string]catalogEntry
	public []contract.Descriptor
}

type catalogEntry struct {
	desc    contract.Descriptor
	handler contract.Handler
	local   contract.LocalIO
}

var (
	_ application.Catalog  = (*moduleCatalog)(nil)
	_ application.IOLookup = (*moduleCatalog)(nil)
)

// frozenCatalogPath locates the frozen operation catalog relative to this
// package directory, which is the working directory of its tests.
var frozenCatalogPath = filepath.Join("..", "..", "internal", "registry", "catalog.json")

// sharedDefs reads the frozen shared $defs object.
func sharedDefs() (json.RawMessage, error) {
	raw, err := os.ReadFile(frozenCatalogPath)
	if err != nil {
		return nil, fmt.Errorf("frozen catalog: %w", err)
	}
	var doc struct {
		Defs struct {
			Defs json.RawMessage `json:"$defs"`
		} `json:"defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("frozen catalog: %w", err)
	}
	if len(doc.Defs.Defs) == 0 {
		return nil, fmt.Errorf("frozen catalog carries no shared $defs")
	}
	return doc.Defs.Defs, nil
}

// isBareSchema reports whether a schema document carries no $defs of its own.
func isBareSchema(schema json.RawMessage) bool {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(schema, &root); err != nil {
		return false
	}
	_, has := root["$defs"]
	return !has
}

// withSharedDefs merges the shared $defs into a bare schema document.
func withSharedDefs(schema, defs json.RawMessage) (json.RawMessage, error) {
	if len(schema) == 0 || !isBareSchema(schema) {
		return schema, nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(schema, &root); err != nil {
		return nil, err
	}
	root["$defs"] = defs
	return json.Marshal(root)
}

func newModuleCatalog(modules []contract.Module) (*moduleCatalog, error) {
	c := &moduleCatalog{ops: map[string]catalogEntry{}}
	defs, err := sharedDefs()
	if err != nil {
		return nil, err
	}
	for _, m := range modules {
		local, _ := m.(contract.LocalIO)
		for _, d := range m.Descriptors() {
			if d.Owner != m.Name() {
				return nil, fmt.Errorf("module %s registers %s for owner %q", m.Name(), d.ID, d.Owner)
			}
			if _, dup := c.ops[d.ID]; dup {
				return nil, fmt.Errorf("operation %s is registered more than once", d.ID)
			}
			if d.Visibility == contract.VisibilityPublic {
				if d.InputSchema, err = withSharedDefs(d.InputSchema, defs); err != nil {
					return nil, fmt.Errorf("operation %s input schema: %w", d.ID, err)
				}
				if d.OutputSchema, err = withSharedDefs(d.OutputSchema, defs); err != nil {
					return nil, fmt.Errorf("operation %s output schema: %w", d.ID, err)
				}
			}
			e := catalogEntry{desc: d, handler: m.Handle}
			if localIOOperations[d.ID] {
				if local == nil {
					return nil, fmt.Errorf("local IO operation %s owner %s does not implement contract.LocalIO", d.ID, m.Name())
				}
				e.local = local
			}
			c.ops[d.ID] = e
			if d.Visibility == contract.VisibilityPublic {
				c.public = append(c.public, d)
			}
		}
	}
	sort.Slice(c.public, func(i, j int) bool { return c.public[i].ID < c.public[j].ID })
	return c, nil
}

// Lookup resolves an operation. Version 0 means the current version, which
// is how internal/application addresses the catalog.
func (c *moduleCatalog) Lookup(id string, version int64) (contract.Descriptor, contract.Handler, error) {
	e, ok := c.ops[id]
	if !ok || (version != 0 && version != e.desc.Version) {
		return contract.Descriptor{}, nil, &contract.Fault{
			Code:    contract.CodeNotFound,
			Message: fmt.Sprintf("operation %s version %d is not registered", id, version),
		}
	}
	return e.desc, e.handler, nil
}

func (c *moduleCatalog) Public() []contract.Descriptor {
	return append([]contract.Descriptor(nil), c.public...)
}

func (c *moduleCatalog) LocalIOFor(operation string) (contract.LocalIO, bool) {
	e, ok := c.ops[operation]
	if !ok || e.local == nil {
		return nil, false
	}
	return e.local, true
}
