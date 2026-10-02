package execution

import (
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestToolOfferingUsesPinnedOperationMapping(t *testing.T) {
	components := []contextComponent{
		{Kind: "tool", ToolID: "00000000-0000-4000-8000-000000000008", ToolVersion: 1, Name: "model", Effect: "external_mutation", IsModelTool: true},
		{Kind: "tool", ToolID: "00000000-0000-4000-8000-000000000005", ToolVersion: 1, Name: "fetch_source", Effect: contract.EffectExternalRead},
		{Kind: "tool", ToolID: "00000000-0000-4000-8000-000000000003", ToolVersion: 1, Name: "fetch_source", Effect: contract.EffectExternalRead},
		{Kind: "tool", ToolID: "00000000-0000-4000-8000-000000000004", ToolVersion: 1, Name: "reply", Effect: contract.EffectExternalRead},
		{Kind: "tool", ToolID: "00000000-0000-4000-8000-000000000006", ToolVersion: 1, Name: "write_local", Effect: "local"},
		{Kind: "tool", ToolID: "00000000-0000-4000-8000-000000000007", ToolVersion: 1, Name: "bad.name", Effect: contract.EffectExternalRead},
		{Kind: "tool", ToolID: "00000000-0000-4000-8000-000000000009", ToolVersion: 1, Name: "mcp_read", Effect: contract.EffectDisclosure},
	}
	markOfferedTools(components)
	for i, c := range components {
		want := i == 2 || i == 6
		if c.Offered != want {
			t.Fatalf("%s offered=%v, want %v", c.Name, c.Offered, want)
		}
		if want && (c.ToolOperationID != contract.AdapterToolOperation || c.ToolOperationVersion != 1) {
			t.Fatalf("%s mapping=%s@%d", c.Name, c.ToolOperationID, c.ToolOperationVersion)
		}
	}
	refs := offeredToolRefs(components)
	if len(refs) != 6 {
		t.Fatalf("offered refs=%v, want four decisions and two adapter tools", refs)
	}
	if refs[4].ID != components[2].ToolID || refs[5].ID != components[6].ToolID {
		t.Fatalf("offered refs=%v", refs)
	}
}
