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
package artifacts
