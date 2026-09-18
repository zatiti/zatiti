package main

import (
	"io"
	"log/slog"
	"strings"
)

// redactedKeys are attribute names whose values never reach a log line,
// whatever a caller passes. Matching is a case-insensitive substring match
// on the key so "owner_credential" and "Authorization" are both caught.
var redactedKeys = []string{"authorization", "credential", "token", "secret", "password", "key_ref", "master_key"}

const redacted = "[redacted]"

// newLogger builds the process logger: text records on stderr (stdout is
// reserved for the CLI envelope and MCP frames) with sensitive attributes
// replaced before they are written.
func newLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: redactAttr,
	}))
}

// redactAttr implements slog.HandlerOptions.ReplaceAttr.
func redactAttr(_ []string, a slog.Attr) slog.Attr {
	lower := strings.ToLower(a.Key)
	for _, k := range redactedKeys {
		if strings.Contains(lower, k) {
			return slog.String(a.Key, redacted)
		}
	}
	return a
}
