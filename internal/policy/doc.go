// Package policy owns deterministic authority intersection, standing and
// exact-review policy, and earned autonomy for one zatiti installation.
//
// The package answers one question for every admission decision: may the
// authenticated actor perform this capability in this scope right now? The
// answer intersects the actor's current identity grants (including explicit
// denials and expiry), hierarchy ceilings, project and binding context,
// worker and task envelope, active restrictions, standing policy rules with
// bounded declarative conditions, and required exact human reviews. Explicit
// denials win; missing or unsupported required conditions refuse admission;
// unknown decision states fail closed.
//
// The package also owns earned autonomy: operator-approved promotion rules
// deterministically evaluate independently established evidence before
// activating a narrow grant inside a prior ceiling. Qualifications bind the
// exact capability, destinations, worker, model, tool and skill versions,
// evidence window and rule version that produced them; relevant dependency
// changes invalidate the affected qualifications and immediately restrict the
// grants they had produced.
//
// Public operations (policy.*, autonomy.*) stage configuration changes
// through the configuration compiler and never activate definitions
// directly. Internal operations (_policy.check, _policy.validate,
// _policy.activate, _policy.invalidate) serve the application policy gate,
// the configuration compiler and peer owners under strict caller
// allowlists. Tables are private under the policy_ prefix.
package policy

import "github.com/zatiti/zatiti/internal/contract"

// Compile-time interface conformance checks.
var (
	_ contract.Module = (*Service)(nil)
)
