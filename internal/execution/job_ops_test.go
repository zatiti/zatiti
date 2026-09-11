package execution

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Durable jobs: identity is the source id plus the canonical input hash, the
// claim is generation-owned, and only the current claim holder records.

func jobCreateFixture(e *testEnv, source contract.ID) jobCreateInput {
	return jobCreateInput{
		Scope:     e.scope,
		Owner:     "tasks",
		Operation: "task.export",
		Input:     json.RawMessage(`{"bounds":{"limit":10}}`),
		SourceID:  source,
	}
}

func mustJobCreate(e *testEnv, source contract.ID) contract.ID {
	e.t.Helper()
	payload := e.mustOK(opJobCreate, jobCreateFixture(e, source))
	var body jobBody
	e.decode(payload.Data, &body)
	return body.Resource.ID
}

func TestJobCreateAndDedup(t *testing.T) {
	e := newEnv(t)
	source := e.ids.New()
	payload := e.mustOK(opJobCreate, jobCreateFixture(e, source))
	var first jobBody
	e.decode(payload.Data, &first)
	if first.Resource.Kind != "operation" || first.Resource.State != "pending" {
		t.Fatalf("created job %+v", first.Resource)
	}
	if first.Resource.Owner != "tasks" || first.Resource.Operation != "task.export" {
		t.Fatalf("created job owner %q operation %q", first.Resource.Owner, first.Resource.Operation)
	}

	payload = e.mustOK(opJobCreate, jobCreateFixture(e, source))
	var replay jobBody
	e.decode(payload.Data, &replay)
	if replay.Resource.ID != first.Resource.ID {
		t.Fatalf("replayed create minted job %s, want %s", replay.Resource.ID, first.Resource.ID)
	}
}

func TestJobCreateDifferentInputConflicts(t *testing.T) {
	e := newEnv(t)
	source := e.ids.New()
	e.mustOK(opJobCreate, jobCreateFixture(e, source))
	in := jobCreateFixture(e, source)
	in.Input = json.RawMessage(`{"bounds":{"limit":99}}`)
	f := e.expectFault(opJobCreate, in, contract.CodeConflict)
	if !strings.Contains(f.Message, "already committed with different input") {
		t.Fatalf("fault message %q does not name the dedup fence", f.Message)
	}
}

func TestJobCreateRequiresOwnerAndOperation(t *testing.T) {
	e := newEnv(t)
	source := e.ids.New()
	noOwner := jobCreateFixture(e, source)
	noOwner.Owner = ""
	_ = e.expectFault(opJobCreate, noOwner, contract.CodeInvalidInput)
	noOp := jobCreateFixture(e, source)
	noOp.Operation = ""
	_ = e.expectFault(opJobCreate, noOp, contract.CodeInvalidInput)
}

func TestJobClaimAndReplay(t *testing.T) {
	e := newEnv(t)
	jobID := mustJobCreate(e, e.ids.New())

	payload := e.mustOK(opJobClaim, jobClaimInput{JobID: jobID, ExpectedVersion: 1, Generation: 7})
	var claim jobClaimBody
	e.decode(payload.Data, &claim)
	if claim.Job.State != "running" || claim.Job.Version != 2 {
		t.Fatalf("claimed job %+v", claim.Job)
	}

	// A lost acknowledgement replays the same claim for the same generation.
	payload = e.mustOK(opJobClaim, jobClaimInput{JobID: jobID, ExpectedVersion: 2, Generation: 7})
	var replay jobClaimBody
	e.decode(payload.Data, &replay)
	if replay.Job.ID != jobID || replay.Job.State != "running" {
		t.Fatalf("replayed claim %+v, want the running job", replay.Job)
	}
}

func TestJobClaimOtherGenerationConflicts(t *testing.T) {
	e := newEnv(t)
	jobID := mustJobCreate(e, e.ids.New())
	e.mustOK(opJobClaim, jobClaimInput{JobID: jobID, ExpectedVersion: 1, Generation: 7})
	f := e.expectFault(opJobClaim, jobClaimInput{
		JobID: jobID, ExpectedVersion: 2, Generation: 8,
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "already claimed by generation 7") {
		t.Fatalf("fault message %q does not name the generation owner", f.Message)
	}
}

func TestJobClaimStaleVersion(t *testing.T) {
	e := newEnv(t)
	jobID := mustJobCreate(e, e.ids.New())
	_ = e.expectFault(opJobClaim, jobClaimInput{
		JobID: jobID, ExpectedVersion: 5, Generation: 1,
	}, contract.CodeStaleVersion)
}

func TestJobClaimTerminalConflict(t *testing.T) {
	e := newEnv(t)
	jobID := mustJobCreate(e, e.ids.New())
	e.mustOK(opJobClaim, jobClaimInput{JobID: jobID, ExpectedVersion: 1, Generation: 3})
	e.mustOK(opJobRecord, jobRecordInput{
		JobID: jobID, ExpectedVersion: 2, Generation: 3,
		State: "succeeded", Result: json.RawMessage(`{"ok":true}`), EvidenceIDs: []contract.ID{},
	})
	_ = e.expectFault(opJobClaim, jobClaimInput{
		JobID: jobID, ExpectedVersion: 3, Generation: 4,
	}, contract.CodeConflict)
}

func TestJobRecordGenerationFence(t *testing.T) {
	e := newEnv(t)
	jobID := mustJobCreate(e, e.ids.New())
	e.mustOK(opJobClaim, jobClaimInput{JobID: jobID, ExpectedVersion: 1, Generation: 3})
	f := e.expectFault(opJobRecord, jobRecordInput{
		JobID: jobID, ExpectedVersion: 2, Generation: 9,
		State: "succeeded", Result: json.RawMessage(`{}`), EvidenceIDs: []contract.ID{},
	}, contract.CodeConflict)
	if !strings.Contains(f.Message, "does not match the current owner") {
		t.Fatalf("fault message %q does not name the claim fence", f.Message)
	}
}

func TestJobRecordSuccess(t *testing.T) {
	e := newEnv(t)
	jobID := mustJobCreate(e, e.ids.New())
	e.mustOK(opJobClaim, jobClaimInput{JobID: jobID, ExpectedVersion: 1, Generation: 3})
	payload := e.mustOK(opJobRecord, jobRecordInput{
		JobID: jobID, ExpectedVersion: 2, Generation: 3,
		State: "succeeded", Result: json.RawMessage(`{"artifact":"done"}`),
		EvidenceIDs: []contract.ID{e.ids.New()},
	})
	var body jobBody
	e.decode(payload.Data, &body)
	if body.Resource.State != "succeeded" {
		t.Fatalf("recorded state %q", body.Resource.State)
	}
	var recorded bool
	for _, ev := range e.readEvents() {
		if ev.Kind == eventJobRecorded && ev.ResourceID == string(jobID) {
			if !strings.Contains(ev.Data, "evidence_ids") {
				t.Fatalf("job.recorded event data %q lacks the evidence list", ev.Data)
			}
			recorded = true
		}
	}
	if !recorded {
		t.Fatalf("job.recorded event missing")
	}
}

func TestJobPendingScanBounds(t *testing.T) {
	e := newEnv(t)
	_ = e.expectFault(opJobPending, jobPendingInput{Limit: 0}, contract.CodeInvalidInput)
	_ = e.expectFault(opJobPending, jobPendingInput{Limit: 101}, contract.CodeInvalidInput)

	jobID := mustJobCreate(e, e.ids.New())
	payload := e.mustOK(opJobPending, jobPendingInput{Limit: 10})
	var body jobListBody
	e.decode(payload.Data, &body)
	if len(body.Items) != 1 || body.Items[0].ID != jobID {
		t.Fatalf("pending items %+v, want the created job", body.Items)
	}
}

func TestJobGetFences(t *testing.T) {
	e := newEnv(t)
	_ = e.expectFault(opJobGet, getIDInput{Scope: e.scope, ID: e.ids.New()}, contract.CodeNotFound)
	jobID := mustJobCreate(e, e.ids.New())
	scope := e.scope
	scope.OrganizationID = e.ids.New()
	_ = e.expectFault(opJobGet, getIDInput{Scope: scope, ID: jobID}, contract.CodePermissionDenied)
}
