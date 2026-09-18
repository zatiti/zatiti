package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Z20 and wake replay: a crash immediately before or after wake admission,
// or a lost acknowledgement, admits exactly one cycle and persists exactly
// one next wake.
func TestWakeReplayAdmitsExactlyOneCycle(t *testing.T) {
	busy := &contract.Fault{Code: contract.CodeControllerUnavailable, Message: "busy", Retryable: true}
	cases := []struct {
		name    string
		inject  injection
		restart bool
	}{
		{"crash before admission commits", injection{fail: busy, crash: true}, true},
		{"crash after admission commits", injection{crash: true}, true},
		{"acknowledgement lost, same process", injection{loseAck: true}, false},
		{"admission refused once", injection{fail: busy}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFx(t)
			f.wake("daily-2026-03-01", f.clock.Now().Add(-time.Minute))
			f.wake("not-yet-due", f.clock.Now().Add(time.Hour))
			c, sess := f.started()
			f.arm("_scheduling.wake.admit", tc.inject)
			if err := f.pass(c, sess); err != nil {
				t.Fatalf("tick: %v", err)
			}
			if tc.restart {
				f.restart()
				c, sess = f.started()
			}
			for i := 0; i < 3; i++ {
				if err := f.pass(c, sess); err != nil {
					t.Fatalf("tick: %v", err)
				}
			}
			if got := f.queryInt(`SELECT COUNT(*) FROM scheduling_cycles`); got != 1 {
				t.Fatalf("admitted %d cycles, want exactly 1", got)
			}
			if got := f.queryInt(`SELECT COUNT(*) FROM scheduling_cycles WHERE occurrence_key = 'daily-2026-03-01'`); got != 1 {
				t.Fatalf("occurrence identity was not preserved")
			}
			if got := f.queryInt(`SELECT COUNT(*) FROM scheduling_wakes WHERE occurrence_key = 'daily-2026-03-01+1h'`); got != 1 {
				t.Fatalf("next wake persisted %d times, want exactly 1", got)
			}
			if got := f.queryInt(`SELECT admitted FROM scheduling_wakes WHERE occurrence_key = 'not-yet-due'`); got != 0 {
				t.Fatal("a wake that is not due was admitted")
			}
			// Once the clock passes it, the next wake is admitted once too.
			f.clock.Advance(59*time.Minute + 30*time.Second)
			for i := 0; i < 2; i++ {
				if err := f.pass(c, sess); err != nil {
					t.Fatalf("tick: %v", err)
				}
			}
			if got := f.queryInt(`SELECT COUNT(*) FROM scheduling_cycles`); got != 2 {
				t.Fatalf("after the clock advanced: %d cycles, want 2", got)
			}
		})
	}
}

// The tick passes the injected time and the bounded batch to every owner scan.
func TestTickBoundsEveryScan(t *testing.T) {
	f := newFx(t)
	f.ready = 2
	c, err := New(Config{StateDir: f.dir, MaxDispatch: 7}, f.app, f.db, newOwnership(), nil, f.clock)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Attach(Collaborators{Identity: f.actor}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	sess, err := c.start(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = sess.journal.close() })
	if err := f.pass(c, sess); err != nil {
		t.Fatalf("tick: %v", err)
	}
	for _, op := range []string{"_scheduling.wake.due", "_tasks.ready", "_execution.tick", "_execution.job.pending", "_effects.pending"} {
		if got := f.limits[op]; got != 7 {
			t.Fatalf("%s was scanned with limit %d, want the configured 7", op, got)
		}
	}
	if got := c.Status().ReadyWithoutRun; got != 2 {
		t.Fatalf("ready scan reported %d", got)
	}
}

type countingRunner struct {
	calls   atomic.Int64
	outcome JobOutcome
	err     error
	before  func()
}

func (r *countingRunner) RunJob(_ context.Context, job Job) (JobOutcome, error) {
	r.calls.Add(1)
	if r.before != nil {
		r.before()
	}
	if len(job.Input) == 0 {
		return JobOutcome{}, errors.New("job input was not handed to the runner")
	}
	return r.outcome, r.err
}

func TestJobsAreClaimedOnlyWhenRunnable(t *testing.T) {
	t.Run("no runner: never claimed, reported by name", func(t *testing.T) {
		f := newFx(t)
		job := f.job("artifacts", "artifact.export", "")
		c, sess := f.started()
		for i := 0; i < 3; i++ {
			if err := f.pass(c, sess); err != nil {
				t.Fatalf("tick: %v", err)
			}
		}
		if f.called("_execution.job.claim") != 0 || f.jobState(job) != "pending" {
			t.Fatalf("an unrunnable job was claimed: state %s", f.jobState(job))
		}
		s := c.Status()
		if got := obligationKinds(s); !reflect.DeepEqual(got, []string{"job:prerequisite_missing"}) {
			t.Fatalf("obligations %v", got)
		}
		if s.Obligations[0].ResourceID != job {
			t.Fatalf("obligation names %s, want %s", s.Obligations[0].ResourceID, job)
		}
	})
	t.Run("runner: claimed once under the generation and recorded", func(t *testing.T) {
		f := newFx(t)
		runner := &countingRunner{outcome: JobOutcome{State: jobStateSucceeded, Result: json.RawMessage(`{"exported":true}`)}}
		f.jobs = map[string]JobRunner{JobKey("artifacts", "artifact.export"): runner}
		job := f.job("artifacts", "artifact.export", "")
		c, sess := f.started()
		for i := 0; i < 3; i++ {
			if err := f.pass(c, sess); err != nil {
				t.Fatalf("tick: %v", err)
			}
		}
		if runner.calls.Load() != 1 || f.called("_execution.job.claim") != 1 || f.called("_execution.job.record") != 1 {
			t.Fatalf("runs %d claims %d records %d", runner.calls.Load(), f.called("_execution.job.claim"), f.called("_execution.job.record"))
		}
		if f.jobState(job) != "succeeded" {
			t.Fatalf("job is %s", f.jobState(job))
		}
		if got := f.queryString(`SELECT result FROM execution_jobs WHERE id = ?`, string(job)); got != `{"exported":true}` {
			t.Fatalf("result %s", got)
		}
		if got := f.queryInt(`SELECT claimed_generation FROM execution_jobs WHERE id = ?`, string(job)); got != sess.generation {
			t.Fatalf("claimed generation %d", got)
		}
	})
	t.Run("runner error is retained as unknown, never rerun", func(t *testing.T) {
		f := newFx(t)
		runner := &countingRunner{err: errors.New("disk detail /synthetic/path")}
		f.jobs = map[string]JobRunner{JobKey("artifacts", "artifact.export"): runner}
		job := f.job("artifacts", "artifact.export", "")
		c, sess := f.started()
		for i := 0; i < 3; i++ {
			if err := f.pass(c, sess); err != nil {
				t.Fatalf("tick: %v", err)
			}
		}
		if runner.calls.Load() != 1 || f.jobState(job) != "outcome_unknown" {
			t.Fatalf("runs %d state %s", runner.calls.Load(), f.jobState(job))
		}
	})
	t.Run("crash after claim: recorded unknown under the old generation, never rerun", func(t *testing.T) {
		f := newFx(t)
		runner := &countingRunner{outcome: JobOutcome{State: jobStateSucceeded}}
		runner.before = f.crash
		f.jobs = map[string]JobRunner{JobKey("artifacts", "artifact.export"): runner}
		job := f.job("artifacts", "artifact.export", "")
		c, sess := f.started()
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
		if f.jobState(job) != "running" {
			t.Fatalf("job is %s at the crash", f.jobState(job))
		}
		runner.before = nil
		f.restart()
		c, sess = f.started()
		for i := 0; i < 3; i++ {
			if err := f.pass(c, sess); err != nil {
				t.Fatalf("tick: %v", err)
			}
		}
		if runner.calls.Load() != 1 {
			t.Fatalf("an abandoned claimed job was rerun: %d runs", runner.calls.Load())
		}
		if f.jobState(job) != "outcome_unknown" {
			t.Fatalf("job is %s, want outcome_unknown", f.jobState(job))
		}
	})
	t.Run("restore disposition reaches the installation owner", func(t *testing.T) {
		f := newFx(t)
		runner := &countingRunner{outcome: JobOutcome{
			State:        jobStateFailed,
			Requirements: []Requirement{{Code: contract.CodePrerequisiteMissing, Message: "master key is absent"}},
		}}
		f.jobs = map[string]JobRunner{JobKey(restoreOwner, restoreOperation): runner}
		job := f.job(restoreOwner, restoreOperation, "")
		c, sess := f.started()
		f.arm("_installation.restore.record", injection{loseAck: true})
		for i := 0; i < 3; i++ {
			if err := f.pass(c, sess); err != nil {
				t.Fatalf("tick: %v", err)
			}
		}
		if f.jobState(job) != "failed" || runner.calls.Load() != 1 {
			t.Fatalf("state %s runs %d", f.jobState(job), runner.calls.Load())
		}
		if got := f.queryString(`SELECT state || ':' || requirements FROM installation_restores ORDER BY seq LIMIT 1`); got !=
			`failed:[{"code":"prerequisite_missing","message":"master key is absent"}]` {
			t.Fatalf("restore record %s", got)
		}
		if f.called("_execution.job.record") != 1 {
			t.Fatalf("job recorded %d times", f.called("_execution.job.record"))
		}
	})
}

const stagedDigest = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func stagedEvidence() map[string]any {
	return map[string]any{
		"schema": "zatiti.synthetic.evidence/v1",
		"output": map[string]any{
			"text_outputs": []any{map[string]any{"kind": "staged", "staging_ref": "staged:one", "digest": stagedDigest}},
			"tokens":       9007199254740993,
		},
		"staged_outputs": []any{map[string]any{
			"staging_ref": "staged:one", "digest": stagedDigest, "size": 42,
			"media_type": "text/plain", "classification": "internal", "purpose": "model_text",
		}},
		"output_artifacts": []any{},
	}
}

// Owner callbacks: the recorded outcome reaches the owner that waits on the
// operation, exactly once across a lost acknowledgement, after staged
// outputs became real artifacts.
func TestOutcomeDeliveryToOwners(t *testing.T) {
	t.Run("hosted loop attempt", func(t *testing.T) {
		f := newFx(t)
		f.blobs = newFakeBlobs()
		provider := f.adapter("synthetic")
		provider.reply = func(context.Context, contract.Dispatch) (contract.Observation, error) {
			return succeeded(stagedEvidence()), nil
		}
		attempt := contract.NewID()
		op := f.prepare("synthetic", map[string]any{"attempt_id": attempt, "run_id": contract.NewID(), "model": "synthetic"})
		c, sess := f.started()
		f.arm("_execution.observation", injection{loseAck: true})
		for i := 0; i < 4; i++ {
			if err := f.pass(c, sess); err != nil {
				t.Fatalf("tick: %v", err)
			}
		}
		if got := f.queryInt(`SELECT COUNT(*) FROM execution_observations WHERE operation_id = ? AND attempt_id = ?`, string(op), string(attempt)); got != 1 {
			t.Fatalf("execution holds %d observations, want 1", got)
		}
		if got := obligationKinds(c.Status()); len(got) != 0 {
			t.Fatalf("a deduplicated redelivery raised %v", got)
		}
		if provider.calls() != 1 || f.blobs.(*fakeBlobs).count() != 1 {
			t.Fatalf("calls %d publishes %d", provider.calls(), f.blobs.(*fakeBlobs).count())
		}
		if got := f.queryInt(`SELECT COUNT(*) FROM artifacts_items WHERE digest = ?`, stagedDigest); got != 1 {
			t.Fatalf("artifact metadata rows %d, want 1", got)
		}
		artifact := f.queryString(`SELECT id FROM artifacts_items WHERE digest = ?`, stagedDigest)

		// The raw observation is what the effects owner recorded first.
		raw := f.queryString(`SELECT evidence FROM effects_observations WHERE operation_id = ?`, string(op))
		if !containsAny(raw, `"kind":"staged"`) {
			t.Fatalf("effects owner must hold the raw adapter observation: %s", raw)
		}
		// The owner sees real artifact references and exact integers.
		var delivered struct {
			Output struct {
				TextOutputs []struct {
					Kind     string `json:"kind"`
					Artifact struct {
						ID     string `json:"id"`
						Digest string `json:"digest"`
					} `json:"artifact"`
				} `json:"text_outputs"`
				Tokens json.Number `json:"tokens"`
			} `json:"output"`
			Staged    []any `json:"staged_outputs"`
			Artifacts []struct {
				ID string `json:"id"`
			} `json:"output_artifacts"`
		}
		got := f.queryString(`SELECT evidence FROM execution_observations WHERE operation_id = ?`, string(op))
		if err := json.Unmarshal([]byte(got), &delivered); err != nil {
			t.Fatalf("delivered evidence: %v", err)
		}
		if len(delivered.Output.TextOutputs) != 1 || delivered.Output.TextOutputs[0].Kind != "artifact" ||
			delivered.Output.TextOutputs[0].Artifact.ID != artifact || delivered.Output.TextOutputs[0].Artifact.Digest != stagedDigest {
			t.Fatalf("staged locator was not replaced: %s", got)
		}
		if len(delivered.Staged) != 0 || len(delivered.Artifacts) != 1 || delivered.Artifacts[0].ID != artifact {
			t.Fatalf("normalized evidence %s", got)
		}
		if delivered.Output.Tokens.String() != "9007199254740993" {
			t.Fatalf("integer precision lost: %s", delivered.Output.Tokens)
		}
	})
	t.Run("no blob store: recorded, publication owed, callback withheld", func(t *testing.T) {
		f := newFx(t)
		provider := f.adapter("synthetic")
		provider.reply = func(context.Context, contract.Dispatch) (contract.Observation, error) {
			return succeeded(stagedEvidence()), nil
		}
		op := f.prepare("synthetic", map[string]any{"attempt_id": contract.NewID()})
		c, sess := f.started()
		for i := 0; i < 3; i++ {
			if err := f.pass(c, sess); err != nil {
				t.Fatalf("tick: %v", err)
			}
		}
		if f.opState(op) != "succeeded" || provider.calls() != 1 {
			t.Fatalf("the confirmed observation must stand: state %s calls %d", f.opState(op), provider.calls())
		}
		if f.called("_execution.observation") != 0 || f.called("_artifacts.publish") != 0 {
			t.Fatal("an unpublished output must block the owner callback")
		}
		if got := obligationKinds(c.Status()); !reflect.DeepEqual(got, []string{"publication:prerequisite_missing"}) {
			t.Fatalf("obligations %v", got)
		}
	})
	t.Run("publication fault is a visible obligation, not a resend", func(t *testing.T) {
		f := newFx(t)
		blobs := newFakeBlobs()
		blobs.fail = &contract.Fault{Code: contract.CodeArtifactFault, Message: "staged bytes are missing"}
		f.blobs = blobs
		provider := f.adapter("synthetic")
		provider.reply = func(context.Context, contract.Dispatch) (contract.Observation, error) {
			return succeeded(stagedEvidence()), nil
		}
		op := f.prepare("synthetic", map[string]any{"attempt_id": contract.NewID()})
		c, sess := f.started()
		for i := 0; i < 3; i++ {
			if err := f.pass(c, sess); err != nil {
				t.Fatalf("tick: %v", err)
			}
		}
		if f.opState(op) != "succeeded" || provider.calls() != 1 || f.called("_execution.observation") != 0 {
			t.Fatalf("state %s calls %d delivered %d", f.opState(op), provider.calls(), f.called("_execution.observation"))
		}
		if got := obligationKinds(c.Status()); !reflect.DeepEqual(got, []string{"publication:artifact_fault"}) {
			t.Fatalf("obligations %v", got)
		}
	})
	t.Run("memory network job", func(t *testing.T) {
		f := newFx(t)
		f.adapter("synthetic")
		op := f.prepare("synthetic", map[string]any{"kind": "remember"})
		job := f.job("memory", "memory.remember", op)
		c, sess := f.started()
		for i := 0; i < 3; i++ {
			if err := f.pass(c, sess); err != nil {
				t.Fatalf("tick: %v", err)
			}
		}
		if got := f.queryString(`SELECT job_id || ':' || disposition FROM memory_records WHERE operation_id = ?`, string(op)); got != string(job)+":succeeded" {
			t.Fatalf("memory record %s", got)
		}
		if f.called("_memory.record") != 1 || f.called("_execution.job.claim") != 0 {
			t.Fatalf("records %d claims %d; a network job waits on its effect and is not claimed", f.called("_memory.record"), f.called("_execution.job.claim"))
		}
	})
	t.Run("connection probe", func(t *testing.T) {
		f := newFx(t)
		f.adapter("synthetic")
		connection := contract.NewID()
		op := contract.NewID()
		f.exec(`INSERT INTO effects_operations (id, version, state, action, adapter) VALUES (?, 1, 'prepared', ?, 'synthetic')`,
			string(op), string(f.action(nil, connection)))
		f.job("connections", "connection.validate", op)
		c, sess := f.started()
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
		if got := f.queryString(`SELECT connection_id || ':' || expected_version || ':' || disposition FROM connections_validations`); got != string(connection)+":3:succeeded" {
			t.Fatalf("validation record %s", got)
		}
	})
	t.Run("durable owner refusal is reported once and not retried", func(t *testing.T) {
		f := newFx(t)
		f.adapter("synthetic")
		f.prepare("synthetic", map[string]any{"attempt_id": contract.NewID()})
		c, sess := f.started()
		f.arm("_execution.observation", injection{fail: &contract.Fault{Code: contract.CodeConflict, Message: "attempt is fenced"}, sticky: true})
		for i := 0; i < 4; i++ {
			if err := f.pass(c, sess); err != nil {
				t.Fatalf("tick: %v", err)
			}
		}
		if f.called("_execution.observation") != 1 {
			t.Fatalf("refused callback retried %d times", f.called("_execution.observation"))
		}
		if got := obligationKinds(c.Status()); !reflect.DeepEqual(got, []string{"delivery:conflict"}) {
			t.Fatalf("obligations %v", got)
		}
	})
	t.Run("operation nobody waits on has no callback", func(t *testing.T) {
		f := newFx(t)
		f.adapter("synthetic")
		f.prepare("synthetic", nil)
		c, sess := f.started()
		if err := f.pass(c, sess); err != nil {
			t.Fatalf("tick: %v", err)
		}
		for _, op := range []string{"_execution.observation", "_memory.record", "_connections.validation.record"} {
			if f.called(op) != 0 {
				t.Fatalf("%s was called for an operation with no waiting owner", op)
			}
		}
		for _, e := range sess.journal.snapshot() {
			if !e.finished() {
				t.Fatalf("entry left in %s", e.Phase)
			}
		}
	})
}

func TestJournalDurability(t *testing.T) {
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	path := filepath.Join(dir, journalDirName, journalFileName)
	j, err := openJournal(dir)
	if err != nil {
		t.Fatalf("openJournal: %v", err)
	}
	obs := succeeded(nil)
	must := func(e entry) {
		t.Helper()
		if err := j.put(e); err != nil {
			t.Fatalf("put: %v", err)
		}
	}
	must(entry{ID: "a", Kind: kindEffect, Phase: phaseClaimed, Generation: 3, OperationID: "op-a", AttemptID: "at-a"})
	must(entry{ID: "a", Kind: kindEffect, Phase: phaseObserved, Generation: 3, OperationID: "op-a", AttemptID: "at-a", Observation: &obs})
	must(entry{ID: "a", Kind: kindEffect, Phase: phaseRecorded, Generation: 3, OperationID: "op-a", AttemptID: "at-a"})
	must(entry{ID: "b", Kind: kindEffect, Phase: phaseDone, Generation: 3, OperationID: "op-b"})
	must(entry{ID: "c", Kind: kindEffect, Phase: phaseRefused, Generation: 3, OperationID: "op-c", Observation: &obs})
	if err := j.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("journal mode %v err %v", info.Mode().Perm(), err)
	}
	if info, _ := os.Stat(filepath.Dir(path)); info.Mode().Perm() != 0o700 {
		t.Fatalf("journal directory mode %v", info.Mode().Perm())
	}

	t.Run("later lines keep the observation", func(t *testing.T) {
		j, err := openJournal(dir)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		defer func() { _ = j.close() }()
		e, ok := j.get("a")
		if !ok || e.Phase != phaseRecorded || e.Observation == nil || e.Observation.Disposition != contract.DispositionSucceeded {
			t.Fatalf("merged entry %+v", e)
		}
	})
	t.Run("torn tail is an append that never happened", func(t *testing.T) {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString(`{"id":"a","kind":"effect","phase":"do`); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		j, err := openJournal(dir)
		if err != nil {
			t.Fatalf("a torn tail must not refuse recovery: %v", err)
		}
		defer func() { _ = j.close() }()
		if e, _ := j.get("a"); e.Phase != phaseRecorded {
			t.Fatalf("torn line was applied: phase %s", e.Phase)
		}
		if err := j.put(entry{ID: "d", Kind: kindEffect, Phase: phaseAdmitting, Generation: 3}); err != nil {
			t.Fatalf("put after repair: %v", err)
		}
	})
	t.Run("compaction keeps open and refused work", func(t *testing.T) {
		j, err := openJournal(dir)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		if err := j.compact(); err != nil {
			t.Fatalf("compact: %v", err)
		}
		if err := j.put(entry{ID: "e", Kind: kindJob, Phase: phaseClaimed, Generation: 3, JobID: "job-e"}); err != nil {
			t.Fatalf("put after compaction: %v", err)
		}
		_ = j.close()
		j, err = openJournal(dir)
		if err != nil {
			t.Fatalf("reopen after compaction: %v", err)
		}
		defer func() { _ = j.close() }()
		var ids []string
		for _, e := range j.snapshot() {
			ids = append(ids, e.ID+":"+string(e.Phase))
		}
		if want := []string{"a:recorded", "c:refused", "d:admitting", "e:claimed"}; !reflect.DeepEqual(ids, want) {
			t.Fatalf("after compaction %v, want %v", ids, want)
		}
		if c, _ := j.get("c"); c.Observation == nil {
			t.Fatal("refused evidence lost its observation in compaction")
		}
	})
	t.Run("corruption refuses to dispatch", func(t *testing.T) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append([]byte("not json\n"), data...), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = openJournal(dir)
		wantFault(t, err, contract.CodeInternalError)
	})
	t.Run("relative state directory is refused", func(t *testing.T) {
		_, err := openJournal("relative/state")
		wantFault(t, err, contract.CodeInvalidInput)
	})
}

// A corrupt journal fails the run closed before any owner call.
func TestCorruptJournalFailsStartClosed(t *testing.T) {
	f := newFx(t)
	provider := f.adapter("synthetic")
	f.prepare("synthetic", nil)
	dir := filepath.Join(f.dir, journalDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, journalFileName), []byte("{\"id\":\"\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := f.controller(nil)
	wantFault(t, c.Run(context.Background()), contract.CodeInternalError)
	if provider.calls() != 0 || f.called("_effects.admit") != 0 {
		t.Fatal("a controller that cannot read its ambiguity must not dispatch")
	}
}
