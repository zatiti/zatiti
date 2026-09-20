package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/zatiti/zatiti/internal/contract"
)

// finish writes one result to streams.Out — the full JSON envelope in
// --json mode, its human rendering otherwise — and reports the process
// exit code contract.CLIExit assigns the result's fault. A domain failure
// itself is a normal, expected result and is never additionally reported
// to streams.Err; only a genuine local write failure is a diagnostic.
func finish(streams IO, jsonMode bool, result contract.Result) error {
	if jsonMode {
		if err := writeEnvelope(streams.Out, result); err != nil {
			_, _ = fmt.Fprintf(streams.Err, "cli: writing result: %v\n", err)
			return &cliError{code: 1, msg: fmt.Sprintf("writing result: %v", err)}
		}
	} else {
		writeHuman(streams.Out, result)
	}
	code := contract.CLIExit(result.Error)
	if code == 0 {
		return nil
	}
	return &cliError{code: code, msg: result.Error.Error()}
}

// writeEnvelope emits exactly one contract.Result as one JSON document.
// SetEscapeHTML(false) preserves the envelope's data bytes exactly as the
// operator produced them instead of re-escaping characters such as '<' or
// '&' that a caller's data may legitimately contain.
func writeEnvelope(w io.Writer, result contract.Result) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(result)
}

// writeHuman renders the same result as short, honest lines: an accepted
// command is not presented as finished, and an unknown outcome or a
// required manual review is named rather than reported as a bare failure.
func writeHuman(w io.Writer, result contract.Result) {
	var b strings.Builder
	fmt.Fprintf(&b, "command:  %s\n", result.CommandID)
	fmt.Fprintf(&b, "status:   %s\n", humanStatus(result))
	if result.Error != nil {
		fmt.Fprintf(&b, "fault:    %s\n", result.Error.Code)
		if result.Error.Message != "" {
			fmt.Fprintf(&b, "message:  %s\n", result.Error.Message)
		}
		if result.Error.Retryable {
			b.WriteString("retry:    retryable\n")
		}
	}
	if data := prettyData(result.Data); data != "" {
		b.WriteString("data:\n")
		b.WriteString(data)
		b.WriteString("\n")
	}
	if result.NextCursor != nil {
		fmt.Fprintf(&b, "next_cursor: %s\n", *result.NextCursor)
	}
	// strings.Builder never returns a write error; io.WriteString's error
	// (a genuinely broken stdout) is reported the same way writeEnvelope's
	// is, by the caller.
	_, _ = io.WriteString(w, b.String())
}

// humanStatus derives one honest status label from the same result the
// JSON envelope carries. "accepted" is durable but not finished; an
// outcome_unknown or controller_unavailable fault means no authoritative
// disposition was established (never a proven failure); a blocked fault
// (prerequisite_missing, external_action_required, budget_unavailable,
// capability_unsupported) means the request is refused only until a named
// external condition is resolved, distinct from both an ordinary failure
// and a genuinely unknown outcome; a review_required fault means a human
// decision is pending, not that the request itself was rejected.
func humanStatus(result contract.Result) string {
	switch result.Status {
	case contract.StatusAccepted:
		return "accepted (durable work continues; not yet complete)"
	case contract.StatusFailed:
		if result.Error == nil {
			return "failed"
		}
		switch result.Error.Code {
		case contract.CodeOutcomeUnknown:
			return "failed: outcome unknown (neither success nor failure is established; do not retry with a new key)"
		case contract.CodeControllerUnavailable:
			return "failed: controller unavailable (no disposition was established; safe to retry once it recovers)"
		case contract.CodeReviewRequired:
			return "failed: manual review required"
		case contract.CodePrerequisiteMissing, contract.CodeExternalActionRequired, contract.CodeBudgetUnavailable, contract.CodeCapabilityUnsupported:
			return "failed: blocked (" + result.Error.Code + "; resolve the named condition, then resubmit)"
		default:
			return "failed"
		}
	default:
		return result.Status
	}
}

// prettyData indents a result's data payload for human display, or reports
// nothing for an empty or null payload.
func prettyData(data json.RawMessage) string {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, trimmed, "", "  "); err != nil {
		return string(trimmed)
	}
	return buf.String()
}
