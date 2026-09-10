package application

import (
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// scopeProbe mirrors the operation input shape's top-level scope field. The
// catalog convention places the explicit scope on every scoped operation;
// scope-less operations (capabilities queries, bootstrap) have no such key.
type scopeProbe struct {
	Scope *contract.Scope `json:"scope"`
}

// deriveScope returns the Unit scope for a public operation call. The scope
// comes from the validated input when the operation carries one; the local
// installation is overlaid when the input omits it. An input scope naming a
// different installation is a scope violation: one controller serves one
// installation and clients cannot select another.
//
// descriptor.ScopeRequired lists "installation_id" exactly when the input
// schema requires the scope field; a missing scope is then invalid_input.
func deriveScope(desc contract.Descriptor, input []byte, installation contract.ID) (contract.Scope, error) {
	var probe scopeProbe
	if len(input) > 0 {
		if err := json.Unmarshal(input, &probe); err != nil {
			// The input already passed strict schema validation; a probe
			// failure means the operation has no decodable scope field.
			probe.Scope = nil
		}
	}
	if probe.Scope == nil {
		if requiresScope(desc) {
			return contract.Scope{}, invalidFault("operation %s requires a scope in the input", desc.ID)
		}
		return contract.Scope{InstallationID: installation}, nil
	}
	scope := *probe.Scope
	if scope.InstallationID == "" {
		scope.InstallationID = installation
	}
	if installation != "" && scope.InstallationID != installation {
		return contract.Scope{}, permissionFault(
			"input scope targets installation %s outside this controller", scope.InstallationID)
	}
	if scope.InstallationID == "" {
		return contract.Scope{}, invalidFault("input scope is missing installation_id")
	}
	return scope, nil
}

// requiresScope reads the descriptor's ScopeRequired contract: the catalog
// lists "installation_id" when the operation input requires a scope.
func requiresScope(desc contract.Descriptor) bool {
	for _, s := range desc.ScopeRequired {
		if s == "installation_id" {
			return true
		}
	}
	return false
}

// checkScopeShape rejects a scope that storage would refuse later with a
// less precise error, keeping validation before any transaction opens.
func checkScopeShape(scope contract.Scope) error {
	if scope.InstallationID == "" {
		return invalidFault("installation scope is required")
	}
	return nil
}
