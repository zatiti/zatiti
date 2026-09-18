package responses

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"
)

const redactedPlaceholder = "[redacted]"

// scrubSecret returns s with every occurrence of secret replaced. The raw
// credential is only ever placed on the outgoing request by the wire
// protocol; this is the backstop applied to every string derived from
// provider bytes or transport errors before it can reach evidence or a
// Fault.
func scrubSecret(secret []byte, s string) string {
	if len(secret) == 0 || s == "" {
		return s
	}
	return strings.ReplaceAll(s, string(secret), redactedPlaceholder)
}

// scrubSecretBytes is scrubSecret for a raw provider body about to be
// staged as evidence. A body that never echoes the credential is returned
// unchanged, so its digest is the digest of the exact provider bytes.
func scrubSecretBytes(secret, body []byte) []byte {
	if len(secret) == 0 || !bytes.Contains(body, secret) {
		return body
	}
	return bytes.ReplaceAll(body, secret, []byte(redactedPlaceholder))
}

// sanitizeText prepares provider- or transport-derived text for an evidence
// field: the credential is scrubbed, control characters other than newline
// and tab are dropped, invalid UTF-8 is dropped, and the result is bounded
// to at most limit characters.
func sanitizeText(secret []byte, s string, limit int) string {
	s = scrubSecret(secret, s)
	var b strings.Builder
	count := 0
	for _, r := range s {
		if count >= limit {
			break
		}
		if r == utf8.RuneError || (unicode.IsControl(r) && r != '\n' && r != '\t') {
			continue
		}
		b.WriteRune(r)
		count++
	}
	return b.String()
}
