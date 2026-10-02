package contract

import "testing"

func TestLocalDecisionOperationMapping(t *testing.T) {
	for _, name := range []string{
		LocalDecisionToolReply, LocalDecisionToolClarify,
		LocalDecisionToolReportOutputs, LocalDecisionToolCycleDecision,
	} {
		op, version, ok := LocalDecisionOperation(name)
		if !ok || op != LocalDecisionOperationPrefix+name || version != 1 {
			t.Fatalf("%q mapped to %q@%d, ok=%v", name, op, version, ok)
		}
	}
	for _, name := range []string{"", "Reply", "unknown", "reply ", "REPLY"} {
		if op, version, ok := LocalDecisionOperation(name); ok || op != "" || version != 0 {
			t.Fatalf("unexpected mapping for %q", name)
		}
	}
}

func TestToolOperationForEffect(t *testing.T) {
	for _, effect := range []string{EffectDisclosure, EffectExternalRead, EffectExternalMutation} {
		op, version, ok := ToolOperationFor(effect)
		if !ok || op != AdapterToolOperation || version != 1 {
			t.Fatalf("%q mapped to %q@%d, ok=%v", effect, op, version, ok)
		}
	}
	for _, effect := range []string{"", "local", "external_write", "External_Read"} {
		if op, version, ok := ToolOperationFor(effect); ok || op != "" || version != 0 {
			t.Fatalf("unexpected mapping for %q", effect)
		}
	}
}
