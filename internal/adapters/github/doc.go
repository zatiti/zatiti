// Package github implements the qualified GitHub repository
// artifact/publication adapter (adapter name "github", profile schema
// zatiti.github/v1).
//
// The adapter performs documented GitHub REST calls over net/http. Mutation
// retries are disabled: Invoke and Reconcile each perform exactly one
// physical HTTP request per call, never a hidden retry or preflight read.
// Callers that need a precondition read (for example confirming a branch
// head before pushing a commit) issue a separate read_repository dispatch
// and pass its evidence forward as preflight_evidence; this package never
// performs an unaccounted extra call inside a mutating Invoke.
//
// Before any byte is sent, the exact secret-free request record (method,
// destination, permitted headers and body as sent; never Authorization) is
// staged through the BlobStore and named by physical_call.request_context
// as a staged ArtifactLocator with one matching StagedOutput of purpose
// context. If it cannot be staged, nothing is sent. The record is kept for
// every disposition, including not_sent and unknown.
//
// Return convention: Invoke and Reconcile return a non-nil error (always a
// *contract.Fault) only when the physical call was never attempted -- an
// invalid profile/action, a capability outside the qualified v1 surface, or
// an automation bound the profile does not permit. Once a physical call is
// actually sent, whatever happens next (success, provider rejection, or an
// inconclusive network failure) is reported through Observation.Disposition
// with a nil error, because that outcome is durable information the caller
// must record, not a Go-level abort.
package github
