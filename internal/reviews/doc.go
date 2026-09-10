// Package reviews owns exact reviews, eligibility checks, immutable
// decisions and delegated review authority.
//
// # The preview is the action
//
// A review's preview is the exact immutable Action — account identity,
// destination, content and media digests, timing, preconditions,
// configuration revision and tool versions — never a model summary. The
// action digest is SHA-256 over the canonical JSON of the action after a
// round trip through the wire DTO: json.Marshal, contract.Canonicalize,
// contract.Hash. The canonical form makes the digest independent of input
// key order and integer spelling, and the DTO round trip guarantees that
// what was validated as a schema instance is exactly what was hashed.
//
// # Decision immutability and digest veto
//
// reviews_decisions and reviews_delegations are append-only; a review row
// transitions at most pending -> approved | rejected, and an approved or
// rejected review refuses further decisions with conflict. Rejection is a
// permanent veto for the exact action digest: _reviews.ensure never creates
// a fresh review for a rejected digest, and _reviews.check answers
// eligible=false with the rejection decision attached so callers see what
// refused the action. Changing the action changes the digest, which is the
// brief's "changed action creates a new review" branch; the brief's "or
// invalidates old one" state exists in the schema and is honored by every
// read and mutation path, but no wave-2 operation produces it.
//
// # Requirement retention and lazy expiry
//
// The requirement is recorded at creation and never rewritten. Its
// expires_at bounds everything: a wrongly light requirement heals by
// expiry, a pending review past expiry is transitioned to expired (with a
// version bump and event) by the next ensure of the same digest, and an
// approval stops standing the moment its window closes. Queries never
// mutate: check reads an expired pending review as not eligible and leaves
// the stored state alone, because storage transitions belong to mutation
// paths only.
//
// An approval also stops standing when its reviewer loses current
// authority — revoked, or the wrong principal kind where the requirement
// demands a human, or a proposer deciding where separation was required.
// Every such recheck consults _identity.authority live; no claimed
// approved_by_human boolean, CLI invocation or stored snapshot can
// substitute, and an agent credential can never satisfy a human-required
// review.
//
// # Delegation is single-hop
//
// Delegation records the (review, delegator, delegatee) triple. The
// delegator must appear in the review's eligible snapshot, which
// structurally prevents chains: a delegatee is not in the snapshot, so a
// delegatee can never delegate again. Authority is never broadened: the
// delegator's kind, revocation and proposer separation are rechecked at
// decide time alongside the delegatee's, a delegatee cannot re-delegate,
// self-delegation is refused, and an identical delegation is a no-op that
// does not consume a review version.
//
// # Scope and fault mapping
//
// Storage is installation-scoped on every query; a request body whose
// scope installation does not match the executing unit is invalid input.
// Eligibility answers are data (eligible=false), while authorization
// failures are faults: permission_denied for revoked, unregistered or
// wrong-kind principals, review_required when a review can no longer be
// decided and a current one must be requested, stale_version for lost
// optimistic races, and cursor_expired with snapshot_required=true for a
// list cursor past its fifteen-minute lifetime.
package reviews
