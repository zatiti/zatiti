// Package mcp is Zatiti's hand-written stdio MCP adapter (protocol revision
// 2025-11-25) built on the official MCP Go SDK. Serve registers one typed
// tool per public operation descriptor and dispatches every call through the
// shared authenticated controller client (internal/client), which already
// implements contract.Operator. This package adds no authority, scheduler or
// credential handling of its own: it only translates between MCP tool calls
// and the common request/result envelope.
package mcp
