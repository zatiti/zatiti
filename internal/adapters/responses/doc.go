// Package responses implements the qualified hosted Responses model adapter
// (adapter name "responses", profile schema zatiti.responses/v1).
//
// Nothing about the provider is built in. The endpoint URL, model
// identifier, connection, token bounds, prices, currency, cost ceiling and
// permitted disclosure destinations/classifications all come from the
// profile document; the credential comes from Dispatch.CredentialRef through
// the SecretStore. There is no default account, model, price or fallback
// provider.
//
// The upstream wire shape (HTTP method, credential placement, request and
// response bodies) is deliberately NOT part of this package's frozen
// specification: it is pinned during real-endpoint qualification. That
// translation lives behind the unexported wireProtocol seam, selected by the
// profile's capability_evidence.protocol_revision. This build registers no
// qualified protocol revision (see qualifiedProtocols), so a production
// Invoke performs every reachable local step -- action validation, token
// bounds, context loading and digest verification, disclosure
// classification -- and then refuses with capability_unsupported at the
// exact point where the request would have to be encoded. No byte is sent.
// The transport, accounting and evidence machinery behind the seam is real
// and is exercised in this package's tests through a synthetic protocol that
// makes no claim about any vendor's API.
//
// Before anything is sent, the exact secret-free request record (method,
// destination, permitted headers, body as sent) is staged and named by
// request_context as a staged ArtifactLocator; if it cannot be staged,
// nothing is sent. The action's context_artifact is not passed through as
// the request record, because the translated request adds the profile's
// model identifier beyond the persisted context.
//
// One Invoke is at most one physical HTTP request: redirects are never
// followed, the request body cannot be rewound (so net/http cannot replay
// it), and there is no retry, preflight or polling. Model tool proposals
// are observations only; this package never executes one.
//
// Model calls cost money, so lost responses are never reported as failures.
// A DNS or dial failure is not_sent with no charge. Any failure after bytes
// may have left -- a timeout, a reset, a truncated or undecodable body -- is
// unknown, and its usage carries the full admitted worst-case charge as an
// unknown amount so the caller keeps its reservation. Spend is taken from
// the provider's reported token usage and the profile's rational rates when
// the provider reports usage; when it does not, billing is unknown (or
// advisory), never zero and never a silent estimate.
//
// Reconcile never performs a physical call: the frozen profile carries no
// qualified authoritative lookup or retention window, so it refuses with
// capability_unsupported and the original outcome stays unknown.
//
// Return convention: Invoke returns a non-nil error (always a
// *contract.Fault) only when no physical call was attempted. Once a request
// is handed to the transport, the outcome is reported through
// Observation.Disposition with a nil error.
package responses
