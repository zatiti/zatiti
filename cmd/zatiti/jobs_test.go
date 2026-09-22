package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/controller"
)

// fakeLocalJobRunner is a contract.LocalJobRunner test double that records
// every call it receives.
type fakeLocalJobRunner struct {
	fn func(ctx context.Context, work contract.JobWork) (contract.JobOutcome, error)

	calls []contract.JobWork
}

func (f *fakeLocalJobRunner) RunJob(ctx context.Context, work contract.JobWork) (contract.JobOutcome, error) {
	f.calls = append(f.calls, work)
	return f.fn(ctx, work)
}

func succeedingRunner() *fakeLocalJobRunner {
	return &fakeLocalJobRunner{fn: func(context.Context, contract.JobWork) (contract.JobOutcome, error) {
		return contract.JobOutcome{State: "succeeded", Result: json.RawMessage(`{}`)}, nil
	}}
}

// TestBuildJobRunnersAttachesEveryLandedKind proves supported jobs are
// registered: every landedJobKinds owner/operation pair gets its own
// controller.JobRunner entry when the owner is present, keyed exactly by
// controller.JobKey(owner, operation) -- the same key the controller looks
// up a job's runner by (internal/controller/jobs.go:112).
func TestBuildJobRunnersAttachesEveryLandedKind(t *testing.T) {
	owners := map[string]contract.LocalJobRunner{
		"execution":     succeedingRunner(),
		"skills":        succeedingRunner(),
		"configuration": succeedingRunner(),
	}
	jobs := buildJobRunners(owners)
	if len(jobs) != len(landedJobKinds) {
		t.Fatalf("buildJobRunners produced %d entries, want %d (one per landedJobKinds entry)", len(jobs), len(landedJobKinds))
	}
	for _, k := range landedJobKinds {
		if _, ok := jobs[k.key()]; !ok {
			t.Fatalf("buildJobRunners did not attach a runner for %s", k.key())
		}
	}
}

// TestBuildJobRunnersOmitsAnOwnerNeverConstructed proves an owner
// modules() never discovered as a LocalJobRunner is skipped, never
// invented: the job stays unclaimed and inspectable at the controller
// (internal/controller/jobs.go:112-118's prerequisite_missing obligation),
// rather than this shim silently fabricating a runner.
func TestBuildJobRunnersOmitsAnOwnerNeverConstructed(t *testing.T) {
	jobs := buildJobRunners(map[string]contract.LocalJobRunner{
		"execution": succeedingRunner(),
		// "skills" and "configuration" are deliberately absent.
	})
	if _, ok := jobs[controller.JobKey("skills", "skill.evaluate")]; ok {
		t.Fatal("buildJobRunners attached a runner for an owner that was never constructed")
	}
	if _, ok := jobs[controller.JobKey("execution", "run_export")]; !ok {
		t.Fatal("buildJobRunners dropped the owner that WAS constructed")
	}
}

// TestMissingJobRunnersDetectsTheKnownCatalogGap is P24's required test 2:
// a catalog-supported job kind lacking an attached runner fails an
// assembly-level check, never silently falls through. artifacts/
// artifact.export is a real, verified gap on this tree (internal/artifacts
// commits a durable job through _execution.job.create --
// internal/artifacts/localio.go:749-750 -- but implements no
// contract.LocalJobRunner; confirmed by grep). Attaching every OTHER
// landed job kind and asserting exactly this one gap is reported proves
// the detection mechanism works on the real catalog, not a synthetic one.
func TestMissingJobRunnersDetectsTheKnownCatalogGap(t *testing.T) {
	jobs := buildJobRunners(map[string]contract.LocalJobRunner{
		"execution":     succeedingRunner(),
		"skills":        succeedingRunner(),
		"configuration": succeedingRunner(),
	})
	missing := missingJobRunners(jobs)
	if len(missing) != 1 {
		t.Fatalf("missingJobRunners = %+v, want exactly one gap", missing)
	}
	if missing[0].Owner != "artifacts" || missing[0].Operation != "artifact.export" {
		t.Fatalf("missingJobRunners reported %+v, want {artifacts artifact.export}", missing[0])
	}
}

// TestMissingJobRunnersEmptyWhenEveryCatalogKindIsAttached is the negative
// case: once every catalogJobKinds entry (including the artifacts gap) has
// an attached runner, nothing is reported missing -- proving the check is
// not vacuously non-empty.
func TestMissingJobRunnersEmptyWhenEveryCatalogKindIsAttached(t *testing.T) {
	jobs := map[string]controller.JobRunner{}
	for _, k := range catalogJobKinds {
		jobs[k.key()] = localJobShim{runner: succeedingRunner()}
	}
	if got := missingJobRunners(jobs); len(got) != 0 {
		t.Fatalf("missingJobRunners = %+v, want none once every catalog kind is attached", got)
	}
}

// TestMissingJobRunnersDetectsARemovedLandedKind proves the check also
// catches a regression, not only the one already-known gap: removing a
// currently-attached landed kind must reappear in missingJobRunners.
func TestMissingJobRunnersDetectsARemovedLandedKind(t *testing.T) {
	jobs := map[string]controller.JobRunner{}
	for _, k := range catalogJobKinds {
		if k.Owner == "artifacts" {
			continue
		}
		jobs[k.key()] = localJobShim{runner: succeedingRunner()}
	}
	delete(jobs, controller.JobKey("skills", "skill.evaluate"))
	missing := missingJobRunners(jobs)
	if len(missing) != 2 {
		t.Fatalf("missingJobRunners = %+v, want exactly two gaps (the known artifacts one plus the removed skills one)", missing)
	}
	var sawSkills bool
	for _, k := range missing {
		if k.Owner == "skills" && k.Operation == "skill.evaluate" {
			sawSkills = true
		}
	}
	if !sawSkills {
		t.Fatalf("missingJobRunners did not catch the removed skills/skill.evaluate runner: %+v", missing)
	}
}

// TestLocalJobShimTranslatesJobAndOutcome proves the shim bridges
// controller.Job <-> contract.JobWork and contract.JobOutcome <->
// controller.JobOutcome faithfully: every field the runner receives
// matches the job the controller dispatched, and every field the runner
// returns (including a Requirement's optional ResourceID/ChallengeID
// pointer-to-value conversion) survives the trip back, invoking the
// wrapped runner exactly once -- never doubled, never dropped.
func TestLocalJobShimTranslatesJobAndOutcome(t *testing.T) {
	resourceID := contract.NewID()
	runner := &fakeLocalJobRunner{fn: func(ctx context.Context, w contract.JobWork) (contract.JobOutcome, error) {
		return contract.JobOutcome{
			State:       "succeeded",
			Result:      json.RawMessage(`{"resource":{"id":"x"}}`),
			EvidenceIDs: []contract.ID{contract.NewID()},
			Requirements: []contract.Requirement{
				{Code: "r1", Message: "m1", ResourceID: &resourceID},
				{Code: "r2", Message: "m2"}, // no ResourceID/ChallengeID: must convert to "", not panic.
			},
		}, nil
	}}
	shim := localJobShim{runner: runner}
	job := controller.Job{
		ID: contract.NewID(), Kind: "local", Owner: "execution", Operation: "run_export",
		Input: json.RawMessage(`{"document":"aGVsbG8="}`),
	}

	outcome, err := shim.RunJob(context.Background(), job)
	if err != nil {
		t.Fatalf("RunJob: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("wrapped runner invoked %d times, want exactly 1", len(runner.calls))
	}
	got := runner.calls[0]
	if got.ID != job.ID || got.Owner != job.Owner || got.Operation != job.Operation || string(got.Input) != string(job.Input) {
		t.Fatalf("shim translated controller.Job -> contract.JobWork incorrectly: got %+v from job %+v", got, job)
	}

	if outcome.State != "succeeded" || string(outcome.Result) != `{"resource":{"id":"x"}}` {
		t.Fatalf("shim translated the outcome body incorrectly: %+v", outcome)
	}
	if len(outcome.Requirements) != 2 {
		t.Fatalf("shim dropped a requirement: %+v", outcome.Requirements)
	}
	if outcome.Requirements[0].ResourceID != resourceID {
		t.Fatalf("shim did not carry the requirement's ResourceID through: got %q, want %q", outcome.Requirements[0].ResourceID, resourceID)
	}
	if outcome.Requirements[1].ResourceID != "" || outcome.Requirements[1].ChallengeID != "" {
		t.Fatalf("shim invented an identity for a requirement that carried none: %+v", outcome.Requirements[1])
	}
}

// TestLocalJobShimPropagatesRunnerError proves a wrapped runner's error is
// never swallowed into a false "succeeded" outcome; the controller's own
// runOnce (internal/controller/jobs.go) is what turns an error into
// outcome_unknown, but only if the shim actually returns the error.
func TestLocalJobShimPropagatesRunnerError(t *testing.T) {
	wantErr := errors.New("boom")
	runner := &fakeLocalJobRunner{fn: func(context.Context, contract.JobWork) (contract.JobOutcome, error) {
		return contract.JobOutcome{}, wantErr
	}}
	shim := localJobShim{runner: runner}
	if _, err := shim.RunJob(context.Background(), controller.Job{}); !errors.Is(err, wantErr) {
		t.Fatalf("RunJob error = %v, want %v", err, wantErr)
	}
}
