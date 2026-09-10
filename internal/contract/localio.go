package contract

import (
	"context"
	"encoding/json"
)

// Authenticator resolves credential material to the current Actor on a
// read snapshot. Only the byte-slice credential boundary carries secret
// authentication material, never Invocation JSON.
type Authenticator interface {
	Authenticate(ctx context.Context, reader Reader, credential []byte) (Actor, error)
	AuthenticateCertificate(ctx context.Context, reader Reader, fingerprint Digest) (Actor, error)
}

// IOPlan is the trusted in-memory plan produced by Prepare. It is never
// public, never accepted from an agent and never stored with secrets.
type IOPlan struct {
	ID               ID
	Owner            string
	Invocation       Invocation
	Actor            Actor
	Scope            Scope
	Generation       int64
	ExpectedVersions map[ID]Version
	Prepared         json.RawMessage
}

// IOResult carries local work metadata or a fault from Perform.
type IOResult struct {
	Data  json.RawMessage
	Fault *Fault
}

// LocalIO is the local-file, secure-helper and backup execution seam owned
// by artifacts, skills, connections and installation. Prepare strictly
// validates input, versions, identity and authority and records replayable
// local intent; Perform runs outside transactions on the exact trusted plan;
// Finish rechecks authority/versions/generation and commits result and
// evidence. Only registered local IO operations route through it.
type LocalIO interface {
	Prepare(ctx context.Context, unit Unit, invocation Invocation) (IOPlan, error)
	Perform(ctx context.Context, plan IOPlan) (IOResult, error)
	Finish(ctx context.Context, unit Unit, plan IOPlan, result IOResult) (Payload, error)
}

// Verification wraps the exact VerificationRequest JSON for one
// independently established acceptance check. Verify executes outside Unit.
type Verification struct {
	Request json.RawMessage
}

// VerificationResult wraps the exact VerificationResult document, including
// independently observed checks, pinned identity, task/attempt references
// and staged artifact handoff. A forged worker-provided result is never
// accepted through public report.
type VerificationResult struct {
	Document json.RawMessage
}

// Verifier establishes task acceptance against pinned verifier code.
type Verifier interface {
	Verify(ctx context.Context, verification Verification) (VerificationResult, error)
}

// VerifierDependencies are the capabilities injected into the verifier
// constructor NewVerifier(VerifierDependencies) (Verifier, error).
type VerifierDependencies struct {
	Clock Clock
	Blobs BlobStore
}
