package github

import (
	"errors"
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

func prerequisiteMissing(format string, args ...any) *contract.Fault {
	return fault(contract.CodePrerequisiteMissing, format, args...)
}

func internalError(format string, args ...any) *contract.Fault {
	return fault(contract.CodeInternalError, format, args...)
}

// blobFault maps a BlobStore failure onto the shared fault vocabulary. This
// package's only allowed production import is internal/contract, so it
// cannot inspect a concrete platform-level error type; a bare
// *contract.Fault passes through unchanged and everything else fails
// closed as internal_error without leaking the underlying message.
func blobFault(err error) *contract.Fault {
	var f *contract.Fault
	if errors.As(err, &f) && f != nil {
		return f
	}
	return internalError("github: reading a referenced artifact failed")
}

// secretFault maps a SecretStore failure onto the shared fault vocabulary,
// for the same reason and with the same limitation as blobFault.
func secretFault(err error) *contract.Fault {
	var f *contract.Fault
	if errors.As(err, &f) && f != nil {
		return f
	}
	return internalError("github: resolving the credential reference failed")
}
