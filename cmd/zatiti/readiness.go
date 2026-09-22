package main

import "github.com/zatiti/zatiti/internal/contract"

// readinessLevel classifies what the assembled runtime can actually do,
// distinct from whether the controller/server process is merely up (P24
// item 5): a caller distinguishes "the installation exists and its stored
// data is reachable" from "a model turn can start" from "durable
// task automation can finish its work", instead of one up/down signal that
// hides which capability is missing. Bootstrap and storage-only operation
// stay usable with no paid provider configured at all -- readinessLevel
// only ever narrows what serve reports, never what it refuses to start.
type readinessLevel string

const (
	// readinessStorageOnly: the installation is open and its stored state
	// (tasks, principals, configuration, evidence, ...) is reachable
	// through every query and non-model mutation, but no adapter capable of
	// a paid model step (modelAdapterNames) is registered -- nothing that
	// needs a hosted model can start.
	readinessStorageOnly readinessLevel = "storage_only"
	// readinessChatReady: at least one hosted-model adapter is registered
	// from a valid, explicit, self-binding profile (loadAdapters/
	// responses.New already refuse anything less), so a controlled model
	// turn can start. Durable task automation may still be incomplete: a
	// catalog job kind may have no attached runner, or the trusted verifier
	// may be unavailable.
	readinessChatReady readinessLevel = "chat_ready"
	// readinessTaskReady: chat-ready, every catalog-supported job kind has
	// an attached runner, and the trusted verifier is attached -- a task
	// can run to independently verified completion, not only start a
	// conversation. Missing non-model "tools" adapters (github/httpread/
	// serenity) are still reported as requirements but do not by
	// themselves demote task-ready: a task that never dispatches to an
	// unconfigured tool adapter completes correctly without it, so tool
	// breadth is a configuration choice, not a correctness gate the way an
	// unattached verifier or job runner is.
	readinessTaskReady readinessLevel = "task_ready"
)

// startupRequirement names one concrete, inspectable reason the assembled
// runtime cannot do something -- never a silent gap (P24 item 4: "expose
// startup doctor requirements when provider, price, currency, tools,
// helper or runner are missing"). serve logs one line per requirement at
// startup; a caller who needs the structured list can read superviseController
// through readinessReport's return value in-process (cmd/zatiti exposes no
// separate query operation for it: the six categories describe
// process-assembly state -- which adapter profiles loaded, which job
// runners attached -- that only this entrypoint process ever computes, as
// distinct from internal/installation's own installation.doctor, which
// reports installation-module-level state such as paused/maintenance and
// is unaffected by this).
type startupRequirement struct {
	// Categories names which of the six startup aspects this requirement
	// leaves unmet, using the assignment's own vocabulary: "provider",
	// "price", "currency", "tools", "helper", "runner". More than one
	// applies when a single missing registration accounts for several at
	// once: an absent responses adapter profile is why provider, price and
	// currency are all simultaneously unknown, since cmd/zatiti never
	// independently inspects a price or currency that no adapter
	// construction ever validated -- responses.New's loadProfile
	// (internal/adapters/responses/profile.go) is the sole place those
	// fields are checked, and it already fails startup hard on an invalid
	// profile (loadAdapters wraps its error), so the only *reportable*,
	// non-fatal gap left for a running process is "no profile at all".
	Categories []string `json:"categories"`
	Message    string   `json:"message"`
}

// modelAdapterNames are the adapters this product treats as capable of a
// paid, controlled model step (i.e. that back readinessChatReady/
// readinessTaskReady). Only "responses" is landed on this tree; this is a
// slice, not a single constant, so a future second hosted-model adapter
// needs no readiness-logic change, only a new entry here.
var modelAdapterNames = []string{"responses"}

func isModelAdapter(name string) bool {
	for _, m := range modelAdapterNames {
		if m == name {
			return true
		}
	}
	return false
}

// assemblyReadiness classifies the runtime from what actually assembled at
// startup and reports every gap as an inspectable startupRequirement,
// never a silent fallback:
//
//   - unregistered lists every adapter name the product knows (landed or
//     not) that is not currently registered -- loadAdapters' own missing
//     list (a landed adapter with no profile file) plus unimplementedAdapters
//     (a named adapter whose package has not landed); serve.go already
//     computes both.
//   - missingRunners is missingJobRunners' result against the Jobs map
//     superviseController actually attached.
//   - verifierAttached and helperProvisioned are the two remaining
//     Collaborators/setup-mechanism signals this entrypoint can observe
//     directly (the trusted verifier's own construction error already
//     fails startup hard, so "attached" here really means "constructed
//     successfully"; helperProvisioned reports whether the connection-setup
//     helper's shared HMAC receipt key exists yet -- see helper.go).
func assemblyReadiness(registeredAdapters map[string]contract.Adapter, unregistered []string, missingRunners []jobKind, verifierAttached, helperProvisioned bool) (readinessLevel, []startupRequirement) {
	var reqs []startupRequirement
	for _, name := range unregistered {
		if isModelAdapter(name) {
			reqs = append(reqs, startupRequirement{
				Categories: []string{"provider", "price", "currency"},
				Message:    "adapter " + name + " is not registered; no hosted model step can start until a valid " + name + " profile exists at <state-dir>/adapters/" + name + ".json",
			})
			continue
		}
		reqs = append(reqs, startupRequirement{
			Categories: []string{"tools"},
			Message:    "adapter " + name + " is not registered; a dispatch naming it is recorded not_sent",
		})
	}
	for _, k := range missingRunners {
		reqs = append(reqs, startupRequirement{
			Categories: []string{"runner"},
			Message:    "job kind " + k.key() + " is catalog-supported but no local job runner is attached; a pending job of this kind is never claimed",
		})
	}
	if !verifierAttached {
		reqs = append(reqs, startupRequirement{
			Categories: []string{"runner"},
			Message:    "no trusted verifier is attached; a claimed VerificationRequest is never executed",
		})
	}
	if !helperProvisioned {
		reqs = append(reqs, startupRequirement{
			Categories: []string{"helper"},
			Message:    "the connection-setup helper's receipt key is not provisioned yet; run `zatiti connection helper` once before completing a credential import",
		})
	}

	level := readinessStorageOnly
	if hasRegisteredModelAdapter(registeredAdapters) {
		level = readinessChatReady
		if len(missingRunners) == 0 && verifierAttached {
			level = readinessTaskReady
		}
	}
	return level, reqs
}

func hasRegisteredModelAdapter(registered map[string]contract.Adapter) bool {
	for _, name := range modelAdapterNames {
		if _, ok := registered[name]; ok {
			return true
		}
	}
	return false
}
