package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/zatiti/zatiti/internal/contract"
)

// The trusted verifier: establishes task acceptance independently of the
// worker and outside any unit transaction. It observes each expected check
// against the blob store under the pinned accepted profile, never trusts
// worker-provided evidence, and stages the exact request document for later
// audit. The repository profile flavors require the controlled runner, which
// is unavailable in this runtime; their checks observe unavailable rather
// than pretending success.

// defaultVerifierMaxBytes bounds artifact reads when the pinned profile
// declares no read bound.
const defaultVerifierMaxBytes = 1 << 20

// verifier evaluates acceptance requests through the injected blob store.
type verifier struct {
	clock contract.Clock
	blobs contract.BlobStore
}

// NewVerifier constructs the execution verifier. Clock and blob store are
// required: the verifier stamps real time and reads real bytes, never
// simulated ones.
func NewVerifier(deps contract.VerifierDependencies) (contract.Verifier, error) {
	if deps.Clock == nil {
		return nil, &contract.Fault{Code: contract.CodeInternalError,
			Message: "execution verifier requires a clock"}
	}
	if deps.Blobs == nil {
		return nil, &contract.Fault{Code: contract.CodeInternalError,
			Message: "execution verifier requires a blob store"}
	}
	return &verifier{clock: deps.Clock, blobs: deps.Blobs}, nil
}

// Verify establishes the acceptance checks pinned in the request. It runs
// outside a unit: blob reads and staging are the verifier's only physical
// effects, and the returned document is what the controller records.
func (v *verifier) Verify(ctx context.Context, verification contract.Verification) (contract.VerificationResult, error) {
	var request wireVerificationRequest
	if err := contract.DecodeStrict(verification.Request, &request); err != nil {
		return contract.VerificationResult{}, verificationFailed(
			"verification request does not decode: %v", err)
	}
	if request.Schema != "zatiti.verification-request/v1" {
		return contract.VerificationResult{}, verificationFailed(
			"request schema %q is not the accepted verification request", request.Schema)
	}
	// The profile document carries repository flavor fields beyond the core
	// identity (supported checks, capability evidence); decode tolerantly and
	// require the identity fields the result must bind.
	var profile wireProfileCore
	if err := decodeJSON(string(request.Profile), &profile); err != nil {
		return contract.VerificationResult{}, verificationFailed(
			"pinned verifier profile does not decode: %v", err)
	}
	if profile.ID == "" || profile.Version == "" || profile.CodeDigest == "" {
		return contract.VerificationResult{}, verificationFailed(
			"pinned verifier profile does not carry a verifier identity")
	}

	started := v.clock.Now()
	observed := make([]wireObservedCheck, 0, len(request.ExpectedObservations))
	for _, want := range request.ExpectedObservations {
		obs, err := v.observe(ctx, want, profile.MaxBytes)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			return contract.VerificationResult{}, fmt.Errorf("execution: verify check %s: %w", want.CheckID, err)
		}
		observed = append(observed, obs)
	}
	finished := v.clock.Now()

	status, _ := effectiveVerdict(request.ExpectedObservations, observed)
	if ctx.Err() != nil {
		status = "interrupted"
	}

	ref, err := v.stageRequest(ctx, verification.Request)
	if err != nil {
		return contract.VerificationResult{}, fmt.Errorf("execution: stage verification request: %w", err)
	}

	document := wireVerificationResult{
		Schema:             "zatiti.verification-result/v1",
		JobID:              request.JobID,
		TaskID:             request.TaskID,
		AttemptID:          request.AttemptID,
		AcceptanceDigest:   request.AcceptanceDigest,
		VerifierID:         profile.ID,
		VerifierVersion:    profile.Version,
		VerifierCodeDigest: profile.CodeDigest,
		RequestArtifact:    ref,
		Status:             status,
		Observations:       observed,
		StartedAt:          formatStamp(started),
		FinishedAt:         formatStamp(finished),
		StagedOutputs:      []json.RawMessage{},
		OutputArtifacts:    []json.RawMessage{},
		Independent:        true,
	}
	doc, err := canonicalJSON(document)
	if err != nil {
		return contract.VerificationResult{}, err
	}
	return contract.VerificationResult{Document: doc}, nil
}

// observe establishes one expected check against the blob store. A
// transport-level blob failure returns an error; any check-level outcome is
// reported as a status, never invented.
func (v *verifier) observe(ctx context.Context, want wireExpectedObservation, profileMaxBytes int64) (wireObservedCheck, error) {
	obs := wireObservedCheck{
		CheckID:     want.CheckID,
		Kind:        want.Kind,
		Evidence:    []json.RawMessage{},
		Explanation: "",
	}
	switch want.Kind {
	case "artifact_presence":
		rc, err := v.blobs.Open(ctx, want.ExpectedDigest, 0, 1)
		if err != nil {
			if ctx.Err() != nil {
				return obs, err
			}
			obs.Status = "unavailable"
			obs.Explanation = fmt.Sprintf("artifact %s is not addressable in the blob store", want.ExpectedDigest)
			return obs, nil
		}
		defer func() { _ = rc.Close() }()
		obs.Status = "passed"
		obs.Explanation = fmt.Sprintf("artifact %s is addressable", want.ExpectedDigest)
		return obs, nil

	case "artifact_digest":
		data, err := v.readBounded(ctx, want.ExpectedDigest, profileMaxBytes)
		if err != nil {
			if ctx.Err() != nil {
				return obs, err
			}
			obs.Status = "unavailable"
			obs.Explanation = fmt.Sprintf("artifact %s could not be read for digest comparison", want.ExpectedDigest)
			return obs, nil
		}
		obs.ObservedDigest = sha256Hex(data)
		if obs.ObservedDigest == want.ExpectedDigest {
			obs.Status = "passed"
			obs.Explanation = "observed digest matches the pinned expectation"
		} else {
			obs.Status = "failed"
			obs.Explanation = "observed digest does not match the pinned expectation"
		}
		return obs, nil

	case "json_schema":
		data, err := v.readBounded(ctx, want.ExpectedDigest, profileMaxBytes)
		if err != nil {
			if ctx.Err() != nil {
				return obs, err
			}
			obs.Status = "unavailable"
			obs.Explanation = fmt.Sprintf("artifact %s could not be read for schema validation", want.ExpectedDigest)
			return obs, nil
		}
		obs.ObservedDigest = sha256Hex(data)
		if err := contract.ValidateSchema(want.Schema, data); err != nil {
			obs.Status = "failed"
			obs.Explanation = fmt.Sprintf("artifact does not satisfy the pinned schema: %v", err)
		} else {
			obs.Status = "passed"
			obs.Explanation = "artifact satisfies the pinned schema"
		}
		return obs, nil

	default:
		// repository_patch_applies and repository_command require the
		// controlled runner. This runtime does not host it; the honest
		// observation is unavailability, never a fabricated pass.
		obs.Status = "unavailable"
		obs.Explanation = "repository verifier profile requires the controlled runner, which is unavailable in this runtime"
		return obs, nil
	}
}

// readBounded reads one artifact's full content under the read bound.
func (v *verifier) readBounded(ctx context.Context, digest contract.Digest, profileMaxBytes int64) ([]byte, error) {
	limit := profileMaxBytes
	if limit <= 0 {
		limit = defaultVerifierMaxBytes
	}
	rc, err := v.blobs.Open(ctx, digest, 0, limit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("artifact %s exceeds the %d byte read bound", digest, limit)
	}
	return data, nil
}

// stageRequest persists the exact request document as an addressable
// artifact so every recorded verdict binds the request it verified.
func (v *verifier) stageRequest(ctx context.Context, request []byte) (wireArtifactRef, error) {
	digest := sha256Hex(request)
	stagingRef, stagedDigest, _, err := v.blobs.Stage(ctx, bytes.NewReader(request), int64(len(request)))
	if err != nil {
		return wireArtifactRef{}, err
	}
	if err := v.blobs.Publish(ctx, stagingRef, stagedDigest); err != nil {
		return wireArtifactRef{}, err
	}
	return wireArtifactRef{ID: uuidFromDigest(digest), Digest: digest}, nil
}
