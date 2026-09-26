package contract

import (
	"context"
	"io"
)

// SecretStore stores opaque secret material under reference strings.
// Lookup recovers the installation-local reference for a stable secret name;
// it never returns secret bytes or creates a credential. Implementations
// validate the name like Put, return not_found when absent, and fail closed
// when the secure store is unavailable. References stay out of public
// operation payloads, receipts, logs and model context.
type SecretStore interface {
	Put(ctx context.Context, key string, secret []byte) (string, error)
	Lookup(ctx context.Context, key string) (string, error)
	Get(ctx context.Context, reference string) ([]byte, error)
	Delete(ctx context.Context, reference string) error
}

// BlobStore stores content-addressed blobs. Stage persists bytes and returns
// a staging reference, digest and size; Publish makes a staged blob
// addressable. All objects are encrypted at rest.
type BlobStore interface {
	Stage(ctx context.Context, r io.Reader, size int64) (string, Digest, int64, error)
	Publish(ctx context.Context, stagingRef string, digest Digest) error
	Open(ctx context.Context, digest Digest, offset, length int64) (io.ReadCloser, error)
	RemoveStaged(ctx context.Context, stagingRef string) error
}

// Operator calls one public operation through the full operation boundary
// (envelope, validation, submission key, result envelope).
type Operator interface {
	Call(ctx context.Context, operationID string, request Request) (Result, error)
}

// CredentialSource resolves one credential reference to secret bytes at the
// byte-slice credential boundary — the only carrier of secret material.
type CredentialSource interface {
	Credential(ctx context.Context) ([]byte, error)
}
