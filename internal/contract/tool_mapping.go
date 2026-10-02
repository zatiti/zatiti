package contract

import (
	"encoding/hex"
	"fmt"
)

// These mappings identify the operation that execution may perform for a
// model-visible tool. The provider cannot select or change either value.
const (
	LocalDecisionOperationPrefix          = "zatiti.local_decision."
	LocalDecisionOperationVersion Version = 1
	AdapterToolOperation                  = "_effects.prepare"
	AdapterToolOperationVersion   Version = 1
)

// LocalDecisionOperation returns the sealed execution-local mapping for one
// of the four decision tools. These identifiers are not catalog operations.
func LocalDecisionOperation(name string) (string, Version, bool) {
	switch name {
	case LocalDecisionToolReply, LocalDecisionToolClarify,
		LocalDecisionToolReportOutputs, LocalDecisionToolCycleDecision:
		return LocalDecisionOperationPrefix + name, LocalDecisionOperationVersion, true
	default:
		return "", 0, false
	}
}

func LocalDecisionOperationID(name string) string {
	op, _, _ := LocalDecisionOperation(name)
	return op
}

func LocalDecisionToolNames() []string {
	return []string{
		LocalDecisionToolReply, LocalDecisionToolClarify,
		LocalDecisionToolReportOutputs, LocalDecisionToolCycleDecision,
	}
}

// LocalDecisionToolID is the stable identity used by execution's context
// builder for one sealed decision tool.
func LocalDecisionToolID(name string) ID {
	if _, _, ok := LocalDecisionOperation(name); !ok {
		return ""
	}
	raw, err := hex.DecodeString(string(Hash([]byte("zatiti.local-decision-tool/" + name))))
	if err != nil || len(raw) < 16 {
		return ""
	}
	b := append([]byte(nil), raw[:16]...)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return ID(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

func LocalDecisionToolName(id ID, name string) (string, bool) {
	if _, _, ok := LocalDecisionOperation(name); ok && LocalDecisionToolID(name) == id {
		return name, true
	}
	return "", false
}

// ToolOperationFor maps adapter-dispatched effects to the governed prepare
// operation. Local effects have no model-visible mapping.
func ToolOperationFor(effect string) (string, Version, bool) {
	switch effect {
	case EffectDisclosure, EffectExternalRead, EffectExternalMutation:
		return AdapterToolOperation, AdapterToolOperationVersion, true
	default:
		return "", 0, false
	}
}
