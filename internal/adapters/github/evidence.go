package github

import (
	"bytes"
	"context"

	"github.com/zatiti/zatiti/internal/contract"
)

// requestContextRef returns the ArtifactRef PhysicalCallEvidence.request_context
// carries.
//
// KNOWN CONTRACT GAP: request_context is typed as a bare ArtifactRef
// (id+digest), which only exists once the artifacts domain has minted a
// durable UUID for published bytes. An adapter has no IDSource, cannot mint
// artifact IDs ("The adapter cannot mint artifact IDs" -- shared foundation
// contract), and BlobStore.Stage/Publish never return one either. There is
// therefore no way for this package, using only its declared
// AdapterDependencies, to honestly produce a fresh, durable ArtifactRef for
// the wire bytes it builds synchronously inside Invoke/Reconcile.
// Fabricating a locally-minted UUID would produce a schema-shaped but
// dangling reference -- exactly the "fake success" the implementation
// assignment forbids.
//
// Pending a contract fix (for example typing request_context as the
// existing ArtifactLocator, which already has a "staged" variant for
// exactly this case), this package uses the one durable, non-fabricated
// ArtifactRef it legitimately holds at call time: the profile's own
// capability_evidence.artifact. That is not a claim that this artifact
// contains the literal request bytes; it is the least-misleading value
// available under the current contract. See the package's delivery report
// for the integration lead.
func requestContextRef(profile *githubProfile) wireArtifactRef {
	return profile.CapabilityEvidence.Artifact
}

// noChargeUsage is the ProviderUsage this adapter reports: GitHub's
// documented REST API carries no per-call metered cost, so accounting is a
// known, exact zero rather than an unknown/advisory estimate.
func noChargeUsage() wireProviderUsage {
	return wireProviderUsage{
		Accounting: wireUsage{Currency: "USD"},
		Billing:    "no_charge",
	}
}

// stageProviderResponse stages the raw response body as provider_response
// evidence. Staging is best-effort: a nil BlobStore or empty body yields no
// staged output, and a Stage failure is returned to the caller to decide
// whether it is worth recording -- it must never erase an already-confirmed
// provider observation ("Publication failure ... does not erase a
// confirmed provider observation").
func stageProviderResponse(ctx context.Context, blobs contract.BlobStore, body []byte, contentType string) (*wireStagedOutput, error) {
	if len(body) == 0 || blobs == nil {
		return nil, nil
	}
	stagingRef, digest, size, err := blobs.Stage(ctx, bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, blobFault(err)
	}
	mediaType := contentType
	if mediaType == "" {
		mediaType = "application/json"
	}
	return &wireStagedOutput{
		StagingRef:     stagingRef,
		Digest:         digest,
		Size:           size,
		MediaType:      mediaType,
		Classification: "internal",
		Purpose:        "provider_response",
	}, nil
}
