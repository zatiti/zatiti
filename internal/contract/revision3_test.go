package contract

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Compile-time proof that the frozen revision 3 interfaces are implementable
// by external packages, mirroring the existing block in envelope_test.go.
var (
	_ WorkerOperator     = fakeWorkerOperator{}
	_ LocalJobRunner     = fakeJobRunner{}
	_ SnapshotInventory  = fakeSnapshotInventory{}
	_ RestoreCoordinator = fakeRestoreCoordinator{}
)

type fakeWorkerOperator struct{}

func (fakeWorkerOperator) ExecuteWorker(context.Context, WorkerRequest) (Result, error) {
	return Result{}, nil
}

type fakeJobRunner struct{}

func (fakeJobRunner) RunJob(context.Context, JobWork) (JobOutcome, error) {
	return JobOutcome{}, nil
}

type fakeSnapshotInventory struct{}

func (fakeSnapshotInventory) Inventory(context.Context) (json.RawMessage, error) {
	return nil, nil
}

type fakeRestoreCoordinator struct{}

func (fakeRestoreCoordinator) Prepare(context.Context, ArtifactRef) (json.RawMessage, error) {
	return nil, nil
}

func (fakeRestoreCoordinator) Commit(context.Context, json.RawMessage) error { return nil }

// TestArtifactRefWireShape pins ArtifactRef's JSON tags to $defs/ArtifactRef
// (id, digest) and proves DecodeStrict/ValidateSchema agree on it.
func TestArtifactRefWireShape(t *testing.T) {
	t.Parallel()
	ref := ArtifactRef{ID: NewID(), Digest: Hash([]byte("skill-archive-bytes-v1"))}
	raw, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := ValidateSchema(ArtifactRefSchema, raw); err != nil {
		t.Fatalf("ArtifactRefSchema rejected its own Go encoding: %v", err)
	}
	var back ArtifactRef
	if err := DecodeStrict(raw, &back); err != nil {
		t.Fatalf("strict decode: %v", err)
	}
	if back != ref {
		t.Fatalf("round trip mismatch: got %+v, want %+v", back, ref)
	}
	// additionalProperties:false: an unknown field is rejected by both the
	// schema and the strict decoder identically.
	widened := `{"id":"` + string(ref.ID) + `","digest":"` + string(ref.Digest) + `","classification":"public"}`
	if err := ValidateSchema(ArtifactRefSchema, json.RawMessage(widened)); err == nil {
		t.Fatalf("ArtifactRefSchema accepted a smuggled extra field")
	}
	if err := DecodeStrict([]byte(widened), &ArtifactRef{}); err == nil {
		t.Fatalf("DecodeStrict accepted a smuggled extra field")
	}
}

// TestRequirementWireShape pins Requirement's optional fields and proves a
// revision-2-shaped instance (code/message only, the fields that predate
// resource_id/challenge_id) still validates and decodes.
func TestRequirementWireShape(t *testing.T) {
	t.Parallel()
	minimal := `{"code":"prerequisite_missing","message":"database backup capability not configured"}`
	if err := ValidateSchema(RequirementSchema, json.RawMessage(minimal)); err != nil {
		t.Fatalf("minimal Requirement rejected: %v", err)
	}
	var r Requirement
	if err := DecodeStrict([]byte(minimal), &r); err != nil {
		t.Fatalf("strict decode of minimal Requirement: %v", err)
	}
	if r.ResourceID != nil || r.ChallengeID != nil {
		t.Fatalf("optional fields populated from absent JSON: %+v", r)
	}
	full := `{"code":"review_required","message":"needs approval","resource_id":"` +
		string(NewID()) + `","challenge_id":"` + string(NewID()) + `"}`
	if err := ValidateSchema(RequirementSchema, json.RawMessage(full)); err != nil {
		t.Fatalf("full Requirement rejected: %v", err)
	}
	var full2 Requirement
	if err := DecodeStrict([]byte(full), &full2); err != nil {
		t.Fatalf("strict decode of full Requirement: %v", err)
	}
	if full2.ResourceID == nil || full2.ChallengeID == nil {
		t.Fatalf("optional fields lost: %+v", full2)
	}
}

// TestDispatchCallbackRouteIsAdditiveOptional proves the revision 3
// CallbackRoute field on Dispatch (docs/implementation/contracts.md,
// "Effects callback routing"; $defs/Dispatch in
// docs/implementation/operations.json) is purely additive: a revision-2
// Dispatch JSON value with the field simply absent still decodes, and a
// populated CallbackRoute survives an untouched round trip since its
// concrete shape is owned by internal/effects, not contract.
func TestDispatchCallbackRouteIsAdditiveOptional(t *testing.T) {
	t.Parallel()
	revision2 := `{"operation_id":"` + string(NewID()) + `","attempt_id":"` + string(NewID()) +
		`","generation":1,"adapter":"github","action":{},"credential_ref":"cred-1",` +
		`"deadline":"2026-09-19T00:00:00Z"}`
	var d Dispatch
	if err := DecodeStrict([]byte(revision2), &d); err != nil {
		t.Fatalf("revision-2 Dispatch (no callback_route) failed to decode: %v", err)
	}
	if d.CallbackRoute != nil {
		t.Fatalf("CallbackRoute populated from absent JSON: %s", d.CallbackRoute)
	}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(raw); strings.Contains(got, "callback_route") {
		t.Fatalf("omitted CallbackRoute was emitted on the wire: %s", got)
	}

	d.CallbackRoute = json.RawMessage(`{"kind":"job","job_id":"` + string(NewID()) + `"}`)
	raw2, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal with callback_route: %v", err)
	}
	var back Dispatch
	if err := DecodeStrict(raw2, &back); err != nil {
		t.Fatalf("strict decode with callback_route: %v", err)
	}
	if back.CallbackRoute == nil || string(back.CallbackRoute) != string(d.CallbackRoute) {
		t.Fatalf("CallbackRoute lost on round trip: %s", back.CallbackRoute)
	}
}
