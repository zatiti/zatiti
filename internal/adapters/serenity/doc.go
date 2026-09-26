// Package serenity is the Serenity memory adapter and writer capability
// report, pinned to one inspected upstream source.
//
// PROTOCOL.md in this directory records that inspection: the upstream module
// and commit, the protocol it serves (MEMORY_VERBS v1 over MCP), and a table
// of every capability this package's assignment requires against what the
// pinned upstream provides. Nothing here may claim an upstream behavior that
// document does not record.
//
// At the current pin no operation can be dispatched. A Serenity tool call
// needs a three-request MCP session handshake, while one Invoke is exactly
// one accounted physical request and the frozen action kinds include no
// session action. Independently, upstream results carry SHA-256 fact ids and
// no version, confidence or observed time, so they cannot populate the
// frozen MemoryClaim shape without invented values; and upstream stores no
// caller command identity and serves no status lookup.
//
// The adapter is therefore truthful rather than functional:
//
//   - New strictly validates the zatiti.serenity/v2 profile, binds
//     capability evidence to the profile digest, requires the exact pinned
//     source, protocol and adapter build, and refuses any profile that claims
//     a capability the pinned upstream lacks.
//   - Contract publishes the frozen schemas plus the writer capability
//     report: every operation unsupported, with the named upstream gaps.
//   - Invoke runs the local checks that hold at any pin (mapped brain, single
//     writer owner, spend and disclosure bounds no wider than the profile)
//     and then refuses with capability_unsupported. It never simulates an
//     upstream result.
//   - Reconcile builds no request and refuses with capability_unsupported
//     naming the missing command lookup, so the original outcome stays
//     unknown. It never repeats a write.
//
// Neither path returns an Observation, so neither produces
// PhysicalCallEvidence or a request context: no request is ever built, and a
// staged record of a request that was never built would be fabricated.
//
// The adapter holds no HTTP client, so it cannot make a physical call. The
// only allowed production import is internal/contract; upstream's exported
// Go read facade is recorded in PROTOCOL.md and is not imported.
package serenity
