// Package contract owns Zatiti's shared Go types, strict wire validation,
// canonical JSON, schema-derived primitives and stable faults.
//
// It is a foundation package (integration wave 0): every domain owner and
// every transport depends on it, so it imports the standard library only and
// performs no I/O, no persistence and no side effects. The exported
// declarations below are frozen by the shared implementation contract;
// behavior-private helpers stay unexported.
//
// Wire conventions implemented here:
//
//   - Canonicalize produces versioned canonical JSON: object keys sorted,
//     duplicate keys rejected, exact integer values preserved (int64 range),
//     semantic array order preserved.
//   - Hash digests exact bytes (SHA-256, lowercase hex); callers canonicalize
//     JSON before hashing.
//   - DecodeStrict rejects duplicate keys, unknown fields, trailing data and
//     integers outside the int64 range.
//   - ValidateSchema validates instances against JSON Schema draft 2020-12
//     subsets declared by owners: local $defs/$ref, formats, oneOf/anyOf,
//     strict objects, integer bounds and collection limits. Remote schema
//     references are never fetched.
//
// Faults are the single error vocabulary across CLI, HTTP and MCP
// transports; CLIExit and HTTPStatus own their mappings.
package contract
