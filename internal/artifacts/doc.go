// Package artifacts owns immutable artifact metadata, resumable bounded
// uploads and integrity-visible byte access.
//
// The package implements the artifacts domain owner of the frozen zatiti
// contract: *Service is a contract.Module whose owned tables live under the
// artifacts_ namespace, and it implements contract.LocalIO for the five
// registered local IO operations artifact.upload.chunk, artifact.upload.finish,
// artifact.upload.cancel, artifact.read and artifact.export.
//
// Bytes live in the injected contract.BlobStore, which owns encryption and
// the content-addressed file layout; this package owns only metadata, the
// upload/chunk/pin/fault bookkeeping and the strict operation boundaries.
// Byte IO runs exclusively in LocalIO Perform, outside transactions; the
// finish phases publish content-addressed bytes first and commit metadata,
// chunk rows and events in one transaction afterwards. Metadata whose bytes
// later turn out to be missing or corrupt yields a visible artifact fault,
// never a silent success.
//
// # Revision 3 provenance (known contract gap)
//
// docs/implementation/contracts.md's revision-3 change note says "Artifact
// gains optional source_operation_id/purpose (provenance)", and the frozen
// Artifact $def (docs/implementation/operations.json, spliced into
// schema_defs.go) declares both as optional output fields. artifactRow,
// wireArtifact and the storage schema (migrations.go schemaV2) all carry
// them end to end. But no operation this package owns accepts either field
// as INPUT: _artifacts.publish's frozen input schema is still exactly
// {scope, digest, size, media_type, classification, encrypted}, and
// artifact.upload.begin/finish carry no such field either. Nothing this
// package can supply itself (Unit and Invocation carry no "producing
// operation" identity) can populate them, and adding an unfrozen input
// property here would either be silently stripped by additionalProperties:
// false validation or drift ahead of the generated catalog this package
// must not independently edit. Until a coordinated revision adds an
// optional source_operation_id (and optionally purpose) to
// _artifacts.publish's input -- which would also give internal callers
// (controller, execution, memory, skills, installation) a natural
// idempotency key for crash-safe replay -- these fields stay wire-correct
// but unpopulated through every existing entry point. See
// TestArtifactProvenanceFieldsRoundTrip for the storage/wire proof and the
// P09 landing report for the reported gap.
package artifacts
