// Package cli owns Zatiti's command-line surface: generating one Cobra
// command per public operation descriptor, reading structured input the
// same way for every command, and emitting deterministic output and
// process exit codes.
//
// Command generation. New walks every contract.Descriptor's CLI path and
// builds the matching Cobra command tree; intermediate path segments (for
// example "capabilities" above "capabilities schema") become plain
// grouping commands. A descriptor whose path ends where another
// descriptor's path also ends is a contract defect (a CLI naming
// collision) and New reports it as an error rather than silently
// overwriting one command with another. Transport mechanics — serve, mcp
// serve, help and completion — are not product operations; they are not
// registered here and the caller adds them to the returned *cobra.Command
// after New returns.
//
// Input. Every generated command accepts --input, resolved identically
// across three forms: "@path" reads a local file, "-" reads stdin once,
// and anything else is parsed as an inline JSON object. Omitting the flag
// sends an empty JSON object; the CLI never blocks on a TTY and never
// prompts for missing fields; a missing required field is reported by the
// operator as invalid_input, not guessed or defaulted locally. Every
// mutation descriptor (Descriptor.SubmissionKey) additionally accepts
// --submission-key, the caller-generated idempotency key, forwarded to the
// operator exactly as given so identical retries replay rather than
// resubmit.
//
// Output and exit codes. --json emits exactly one contract.Result envelope
// to stdout and nothing else; without it, the same envelope is rendered as
// short human-readable lines, still derived from the identical result and
// naming an accepted job, a genuinely unknown outcome, a request blocked on
// a named external condition or a required manual review honestly rather
// than as a bare failure. Diagnostics — malformed input, transport
// failures — go to stderr only. The process exit code is always
// contract.CLIExit of the result's fault, so a completed or accepted
// command exits 0 and a domain failure's exit code never depends on which
// transport served it.
package cli
