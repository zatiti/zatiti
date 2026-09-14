// Package httpread implements the qualified bounded public HTTP read
// adapter (adapter name "httpread", profile schema zatiti.httpread/v1).
//
// The adapter performs exactly one bounded GET over net/http per Invoke or
// Reconcile call. It is read-only and unauthenticated: the permitted action
// headers (Accept, Accept-Language, If-None-Match, If-Modified-Since,
// User-Agent) exclude any raw secret carrier, so this package never
// resolves a credential. Every destination is bound-checked before any byte
// is sent: the action's URL origin must appear in the profile's
// allowed_origins, and the resolved dial address must not be
// loopback/private/link-local/metadata -- resolution happens once per call
// and the adapter dials the exact validated IP it checked, closing the
// DNS-rebinding window between check and connect. Redirects are never
// followed (the frozen profile schema fixes max_redirects to 0): a 3xx
// response is reported as a failed read whose evidence carries
// redirect_location, so a caller wanting the redirected resource issues a
// new, explicit read action for it.
//
// Return convention: Invoke and Reconcile return a non-nil error (always a
// *contract.Fault) when the physical HTTP request was never sent -- an
// invalid profile/action, a destination outside the profile's allowed
// origins/media types, a destination that resolves to a disallowed address,
// or any transport failure that occurs before an HTTP response line is
// received. This package's own evidence schema (unlike a sibling mutation
// adapter's) requires a real HTTP status on every recorded evidence
// document, so a failure with no status to honestly report is never encoded
// as a fabricated Observation; because a GET has no side effect to protect,
// this is safe even though it departs from the outcome_unknown-preserving
// convention a mutating adapter needs. Once a response line is received,
// whatever happens next (a bounded success, a policy-refused redirect, a
// bound violation, a media-type mismatch, a non-2xx status, or a read that
// is cut short mid-body) is reported through Observation.Disposition with a
// nil error, because it is durable information the caller must record.
package httpread
