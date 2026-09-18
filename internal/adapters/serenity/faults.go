package serenity

import (
	"encoding/json"
	"fmt"

	"github.com/zatiti/zatiti/internal/contract"
)

// fault builds a *contract.Fault, the stable error vocabulary every
// transport shares. *contract.Fault implements error, so Invoke/Reconcile
// return it directly wherever a physical call was never attempted.
func fault(code, format string, args ...any) *contract.Fault {
	return &contract.Fault{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
	}
}

func invalidInput(format string, args ...any) *contract.Fault {
	return fault(contract.CodeInvalidInput, format, args...)
}

func permissionDenied(format string, args ...any) *contract.Fault {
	return fault(contract.CodePermissionDenied, format, args...)
}

func capabilityUnsupported(format string, args ...any) *contract.Fault {
	return fault(contract.CodeCapabilityUnsupported, format, args...)
}

func internalError(format string, args ...any) *contract.Fault {
	return fault(contract.CodeInternalError, format, args...)
}

// unsupportedDetails is the machine-readable body of an operation refusal:
// which pinned upstream source was inspected and which named gaps block the
// operation. It carries no secret, path or upstream response bytes.
type unsupportedDetails struct {
	Kind           string   `json:"kind"`
	UpstreamModule string   `json:"upstream_module"`
	UpstreamCommit string   `json:"upstream_commit"`
	Missing        []string `json:"missing"`
}

// operationUnsupported is the refusal every action kind ends in at this pin:
// capability_unsupported, naming the exact upstream gaps from the capability
// report. It is not retryable; only a new qualified pin can change it.
func operationUnsupported(op operationCapability) *contract.Fault {
	f := capabilityUnsupported(
		"serenity %s is unavailable: the pinned upstream %s@%s does not provide %v; see PROTOCOL.md",
		op.Kind, pinnedModule, pinnedCommit[:12], op.Missing)
	details, err := json.Marshal(unsupportedDetails{
		Kind: op.Kind, UpstreamModule: pinnedModule, UpstreamCommit: pinnedCommit, Missing: op.Missing,
	})
	if err == nil {
		f.Details = details
	}
	return f
}
