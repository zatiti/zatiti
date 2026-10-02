package execution

import (
	"regexp"
	"sort"

	"github.com/zatiti/zatiti/internal/contract"
)

var portableToolName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// markOfferedTools selects the model-visible subset in stable tool-ID order.
// The model dispatcher remains in the recipe for dispatch, but is never
// itself offered. Colliding names and tools without an adapter mapping stay
// unavailable to the model.
func markOfferedTools(components []contextComponent) {
	indices := make([]int, 0, len(components))
	for i := range components {
		if components[i].Kind == "tool" {
			indices = append(indices, i)
			components[i].Offered = false
			components[i].ToolOperationID = ""
			components[i].ToolOperationVersion = 0
		}
	}
	sort.SliceStable(indices, func(i, j int) bool {
		return components[indices[i]].ToolID < components[indices[j]].ToolID
	})
	seen := make(map[string]bool)
	for _, name := range contract.LocalDecisionToolNames() {
		seen[name] = true
	}
	for _, i := range indices {
		c := &components[i]
		if c.IsModelTool || !portableToolName.MatchString(c.Name) || seen[c.Name] {
			continue
		}
		op, version, ok := contract.ToolOperationFor(c.Effect)
		if !ok {
			continue
		}
		c.Offered = true
		c.ToolOperationID = op
		c.ToolOperationVersion = version
		seen[c.Name] = true
	}
}

func offeredToolRefs(components []contextComponent) []wireRef {
	decisions := localDecisionTools()
	refs := make([]wireRef, 0, len(decisions)+len(components))
	for _, tool := range decisions {
		refs = append(refs, tool.Tool)
	}
	for _, c := range components {
		if c.Kind == "tool" && c.Offered {
			refs = append(refs, wireRef{ID: c.ToolID, Version: c.ToolVersion})
		}
	}
	return refs
}
