package installation

import (
	"context"

	"github.com/zatiti/zatiti/internal/contract"
)

// trustedVerifierProfiles is the fixed, build-defined catalog of verifier
// identities this installation actually ships and internal/execution's
// verifier (internal/execution/verifier.go) actually implements: presence,
// digest and json_schema checks against the blob store for an
// artifact_contract profile. It is Go source, not stored data, and never
// derives from a task's acceptance contract, an uploaded artifact or any
// other caller-supplied input -- so no amount of self-published
// "capability evidence" can add, remove or alter an entry here. That is
// what keeps installation.verifier.list from manufacturing installed
// capability qualification out of untrusted input (P25 item 3): the only
// way this list changes is a new build of the binary.
//
// Only the "artifact" kind is listed. internal/execution's verifier also
// evaluates repository_patch_applies/repository_command checks
// (observeRepository), but those additionally require a locally configured
// repository runner root this package has no way to observe or attest to,
// and this package must not report an availability it cannot establish
// (completion policy: never invented production success for an unavailable
// prerequisite).
var trustedVerifierProfiles = []wireVerifierDescriptor{
	{ID: "zatiti-verifier", Version: 1, Kind: "artifact", Classification: "internal"},
}

// handleVerifierList serves installation.verifier.list: a secret-free
// enumeration of installed, trusted verifier identities a task's acceptance
// contract can name (verifier_id/verifier_version), as opposed to a value
// the task's author invents. The catalog is small and fixed, so cursor is
// accepted syntactically (schema-required) but never produces a second
// page: it is a foreign, opaque cursor from another operation if non-empty,
// and is refused rather than silently ignored.
func handleVerifierList(ctx context.Context, s *Service, unit contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	in, err := decodeInto[verifierListInput](s, opVerifierList, inv.Input)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.Payload{}, err
	}
	if in.Cursor != nil && *in.Cursor != "" {
		return contract.Payload{}, invalidInput("installation.verifier.list has one page; cursor %q is not recognized", *in.Cursor)
	}
	items := make([]wireVerifierDescriptor, len(trustedVerifierProfiles))
	copy(items, trustedVerifierProfiles)
	if in.Limit != nil && *in.Limit > 0 && int64(len(items)) > *in.Limit {
		items = items[:*in.Limit]
	}
	return completed(verifierListOutput{Items: items})
}
