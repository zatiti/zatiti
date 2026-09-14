package github

import "strings"

const redactedPlaceholder = "[redacted]"

// scrubSecret returns s with every occurrence of secret replaced. Request
// building and response interpretation never thread the raw credential
// through evidence or fault text in the first place; this is a
// defense-in-depth backstop applied to any diagnostic text derived from
// provider bytes (for example a GitHub error message that happens to echo
// request content) before it can reach a Fault or evidence document.
func scrubSecret(secret []byte, s string) string {
	if len(secret) == 0 || s == "" {
		return s
	}
	return strings.ReplaceAll(s, string(secret), redactedPlaceholder)
}

// truncateText bounds s to at most n bytes, used to keep provider-supplied
// text (error messages, diagnostics) within the schema's field limits.
func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
