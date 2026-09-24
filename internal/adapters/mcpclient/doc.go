// Package mcpclient is the generic MCP client connection adapter: a
// bounded, credential-scoped streamable-HTTP client for an operator-
// qualified Model Context Protocol server, pinned to protocol revision
// 2025-11-25 (docs/implementation/contracts.md, "MCP client connection
// adapter (coordinated revision 2026-09-24)").
//
// Four action kinds compose a session lifecycle: open_session (the
// contract's single declared exception to one-physical-request-per-attempt
// -- initialize plus notifications/initialized), list_tools (exactly one
// tools/list call, one page), call_tool (exactly one tools/call, gated by
// the profile's allowed_tools, the pinned per-tool input schema and
// digest, and the action's classification) and close_session (at most one
// DELETE; a stateless server never assigns a session ID, so nothing is
// sent to delete). Every server-to-client request (sampling, elicitation,
// roots, ping) is refused with JSON-RPC method-not-found and named in
// refused_server_requests: unsupported features are refusals, never
// inferred success. Reconcile is capability_unsupported for every kind --
// MCP has no authoritative call-lookup capability.
//
// The SDK session's raw Mcp-Session-Id never leaves this package: callers
// address an open session only by an adapter-minted opaque handle, kept in
// a bounded in-memory table for the life of this process.
//
// callRoundTripper is this adapter's single integration point with the
// go-sdk client for every physical request a session's whole lifetime
// makes: it stages the exact secret-free request record before any byte
// reaches the wire, sets the Authorization header only after staging
// succeeds, bounds request and response size, and captures a 3xx
// response's Location header, since max_redirects is frozen at 0 and the
// go-sdk's own client API exposes no other way to reach it.
package mcpclient
