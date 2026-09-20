package cli_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/cli"
	"github.com/zatiti/zatiti/internal/client"
	"github.com/zatiti/zatiti/internal/contract"
)

// TestExactStdoutAndExit proves the "exact stdout/exit" local proving focus
// item and Z02.wire_errors: in --json mode, stdout carries exactly one
// envelope that round-trips to the exact result the operator returned, and
// the process exit code matches the frozen contract.CLIExit mapping for
// every named fault code.
func TestExactStdoutAndExit(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("attempt.cancel", []string{"attempt", "cancel"}, contract.ModeMutation, true),
	}

	cases := []struct {
		code contract.Fault
		want int
	}{
		{contract.Fault{Code: contract.CodeInvalidInput, Message: "missing field"}, 2},
		{contract.Fault{Code: contract.CodePermissionDenied, Message: "denied"}, 3},
		{contract.Fault{Code: contract.CodeReviewRequired, Message: "needs review"}, 3},
		{contract.Fault{Code: contract.CodeStaleVersion, Message: "stale"}, 4},
		{contract.Fault{Code: contract.CodeSubmissionConflict, Message: "conflict"}, 4},
		{contract.Fault{Code: contract.CodeConflict, Message: "conflict"}, 4},
		{contract.Fault{Code: contract.CodePrerequisiteMissing, Message: "missing"}, 5},
		{contract.Fault{Code: contract.CodeExternalActionRequired, Message: "action"}, 5},
		{contract.Fault{Code: contract.CodeBudgetUnavailable, Message: "budget"}, 5},
		{contract.Fault{Code: contract.CodeCapabilityUnsupported, Message: "unsupported"}, 5},
		{contract.Fault{Code: contract.CodeControllerUnavailable, Message: "down", Retryable: true}, 6},
		{contract.Fault{Code: contract.CodeOutcomeUnknown, Message: "unknown"}, 6},
		{contract.Fault{Code: contract.CodeVerificationFailed, Message: "other"}, 1},
	}

	for _, tc := range cases {
		t.Run(tc.code.Code, func(t *testing.T) {
			fault := tc.code
			op := &fakeOperator{fn: func(context.Context, string, contract.Request) (contract.Result, error) {
				return contract.Result{
					Schema:    "zatiti.result/v1",
					CommandID: "00000000-0000-4000-8000-000000000002",
					Payload:   contract.Payload{Status: contract.StatusFailed, Data: nil, Error: &fault},
				}, &fault
			}}
			stdout, stderr, code := run(t, []string{"attempt", "cancel", "--submission-key", "k1", "--json"}, op, descriptors, nil)
			if code != tc.want {
				t.Fatalf("exit code = %d, want %d; stderr = %q", code, tc.want, stderr)
			}
			if stderr != "" {
				t.Fatalf("a domain failure is not a diagnostic; stderr = %q", stderr)
			}
			lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
			if len(lines) != 1 {
				t.Fatalf("stdout carried %d lines, want exactly 1: %q", len(lines), stdout)
			}
			var got contract.Result
			if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
				t.Fatalf("stdout was not one valid envelope: %v", err)
			}
			if got.Error == nil || got.Error.Code != tc.code.Code {
				t.Fatalf("envelope fault = %+v, want code %q", got.Error, tc.code.Code)
			}
		})
	}
}

// TestAcceptedIsNotFailureOrBareSuccess proves the "accepted long task vs
// failure" local proving focus item: an accepted durable job exits 0 like
// completed, but its human rendering says "accepted", never "completed",
// and a failed result's rendering never says either.
func TestAcceptedIsNotFailureOrBareSuccess(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("artifact.export", []string{"artifact", "export"}, contract.ModeMutation, true),
	}

	t.Run("accepted", func(t *testing.T) {
		op := &fakeOperator{fn: func(context.Context, string, contract.Request) (contract.Result, error) {
			return contract.Result{
				Schema:    "zatiti.result/v1",
				CommandID: "00000000-0000-4000-8000-000000000003",
				Payload:   contract.Payload{Status: contract.StatusAccepted, Data: json.RawMessage(`{"resource":{"id":"job-1"}}`)},
			}, nil
		}}
		stdout, _, code := run(t, []string{"artifact", "export", "--submission-key", "k1"}, op, descriptors, nil)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 for accepted", code)
		}
		if !strings.Contains(stdout, "accepted") {
			t.Fatalf("human rendering did not name the accepted status: %q", stdout)
		}
		if strings.Contains(stdout, "status:   completed") {
			t.Fatalf("accepted must never render as completed: %q", stdout)
		}
	})

	t.Run("failed", func(t *testing.T) {
		fault := contract.Fault{Code: contract.CodeVerificationFailed, Message: "check failed"}
		op := &fakeOperator{fn: func(context.Context, string, contract.Request) (contract.Result, error) {
			return contract.Result{
				Schema:    "zatiti.result/v1",
				CommandID: "00000000-0000-4000-8000-000000000004",
				Payload:   contract.Payload{Status: contract.StatusFailed, Error: &fault},
			}, &fault
		}}
		stdout, _, code := run(t, []string{"artifact", "export", "--submission-key", "k1"}, op, descriptors, nil)
		if code == 0 {
			t.Fatal("a failed result must not exit 0")
		}
		if strings.Contains(stdout, "accepted") || strings.Contains(stdout, "status:   completed") {
			t.Fatalf("a failed result must not render as accepted or completed: %q", stdout)
		}
	})
}

// TestOutcomeUnknownIsHonest proves human rendering never presents an
// outcome_unknown fault as an ordinary failure — the contract distinguishes
// "no authoritative disposition" from a proven negative result.
func TestOutcomeUnknownIsHonest(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("attempt.report", []string{"attempt", "report"}, contract.ModeMutation, true),
	}
	fault := contract.Fault{Code: contract.CodeOutcomeUnknown, Message: "no disposition", Retryable: false}
	op := &fakeOperator{fn: func(context.Context, string, contract.Request) (contract.Result, error) {
		return contract.Result{
			Schema:    "zatiti.result/v1",
			CommandID: "00000000-0000-4000-8000-000000000005",
			Payload:   contract.Payload{Status: contract.StatusFailed, Error: &fault},
		}, &fault
	}}
	stdout, _, code := run(t, []string{"attempt", "report", "--submission-key", "k1"}, op, descriptors, nil)
	if code != 6 {
		t.Fatalf("exit code = %d, want 6", code)
	}
	if !strings.Contains(stdout, "outcome unknown") {
		t.Fatalf("rendering did not honestly label the unknown outcome: %q", stdout)
	}

	fault2 := contract.Fault{Code: contract.CodeReviewRequired, Message: "needs a human"}
	op2 := &fakeOperator{fn: func(context.Context, string, contract.Request) (contract.Result, error) {
		return contract.Result{
			Schema:    "zatiti.result/v1",
			CommandID: "00000000-0000-4000-8000-000000000006",
			Payload:   contract.Payload{Status: contract.StatusFailed, Error: &fault2},
		}, &fault2
	}}
	stdout2, _, code2 := run(t, []string{"attempt", "report", "--submission-key", "k2"}, op2, descriptors, nil)
	if code2 != 3 {
		t.Fatalf("exit code = %d, want 3", code2)
	}
	if !strings.Contains(stdout2, "manual review") {
		t.Fatalf("rendering did not honestly label the manual review requirement: %q", stdout2)
	}
}

// TestTransportFailureExitCodes proves a non-domain operator error — no
// envelope exists to render — never reaches stdout and still maps to a
// named exit code when the error is one client identifies precisely.
func TestTransportFailureExitCodes(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("capabilities.list", []string{"capabilities"}, contract.ModeQuery, false),
	}

	t.Run("controller unavailable", func(t *testing.T) {
		op := &fakeOperator{fn: func(context.Context, string, contract.Request) (contract.Result, error) {
			return contract.Result{}, client.ErrControllerUnavailable
		}}
		stdout, stderr, code := run(t, []string{"capabilities", "--json"}, op, descriptors, nil)
		if code != 6 {
			t.Fatalf("exit code = %d, want 6", code)
		}
		if stdout != "" {
			t.Fatalf("no envelope exists for a transport failure; stdout = %q", stdout)
		}
		if stderr == "" {
			t.Fatal("expected a diagnostic on stderr")
		}
	})

	t.Run("unclassified error", func(t *testing.T) {
		op := &fakeOperator{fn: func(context.Context, string, contract.Request) (contract.Result, error) {
			return contract.Result{}, errPlain("boom")
		}}
		stdout, stderr, code := run(t, []string{"capabilities", "--json"}, op, descriptors, nil)
		if code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
		if stdout != "" {
			t.Fatalf("no envelope exists for a transport failure; stdout = %q", stdout)
		}
		if stderr == "" {
			t.Fatal("expected a diagnostic on stderr")
		}
	})
}

type errPlain string

func (e errPlain) Error() string { return string(e) }

// TestHumanRenderingShowsRetryable proves a retryable fault is named as
// such in the human rendering, not silently dropped.
func TestHumanRenderingShowsRetryable(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("attempt.cancel", []string{"attempt", "cancel"}, contract.ModeMutation, true),
	}
	fault := contract.Fault{Code: contract.CodeControllerUnavailable, Message: "down", Retryable: true}
	op := &fakeOperator{fn: func(context.Context, string, contract.Request) (contract.Result, error) {
		return contract.Result{
			Schema:    "zatiti.result/v1",
			CommandID: "00000000-0000-4000-8000-000000000007",
			Payload:   contract.Payload{Status: contract.StatusFailed, Error: &fault},
		}, &fault
	}}
	stdout, _, code := run(t, []string{"attempt", "cancel", "--submission-key", "k1"}, op, descriptors, nil)
	if code != 6 {
		t.Fatalf("exit code = %d, want 6", code)
	}
	if !strings.Contains(stdout, "retryable") {
		t.Fatalf("human rendering did not name the fault as retryable: %q", stdout)
	}
}

// TestAcceptedJobAndUnknownMutationNeverPrintAsCompletedTask proves that
// task.start — the new revision-3 operation that readies a task and
// enqueues its run — never renders an accepted durable job or a mutation
// whose outcome could not be established as a completed task, in either
// human or JSON mode. The frozen Payload.Status enum has exactly three
// values (completed | accepted | failed); this checks both the human
// "status:" line and the JSON envelope's own status field never carry
// "completed" for either disposition.
func TestAcceptedJobAndUnknownMutationNeverPrintAsCompletedTask(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("task.start", []string{"task", "start"}, contract.ModeMutation, true),
	}

	t.Run("accepted job", func(t *testing.T) {
		op := &fakeOperator{fn: func(context.Context, string, contract.Request) (contract.Result, error) {
			return contract.Result{
				Schema:    "zatiti.result/v1",
				CommandID: "00000000-0000-4000-8000-0000000000b1",
				Payload:   contract.Payload{Status: contract.StatusAccepted, Data: json.RawMessage(`{"task":{"id":"t1"}}`)},
			}, nil
		}}

		stdout, _, code := run(t, []string{"task", "start", "--submission-key", "k1"}, op, descriptors, nil)
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 for accepted", code)
		}
		if strings.Contains(stdout, "status:   completed") {
			t.Fatalf("an accepted task.start must never render as completed: %q", stdout)
		}
		if !strings.Contains(stdout, "accepted") {
			t.Fatalf("expected the accepted status to be named honestly: %q", stdout)
		}

		stdoutJSON, _, codeJSON := run(t, []string{"task", "start", "--submission-key", "k1", "--json"}, op, descriptors, nil)
		if codeJSON != 0 {
			t.Fatalf("json mode exit code = %d, want 0", codeJSON)
		}
		var envelope contract.Result
		if err := json.Unmarshal([]byte(strings.TrimSpace(stdoutJSON)), &envelope); err != nil {
			t.Fatalf("stdout was not one valid envelope: %v", err)
		}
		if envelope.Status == contract.StatusCompleted {
			t.Fatalf("JSON envelope status = %q, must never be completed for an accepted job", envelope.Status)
		}
		if envelope.Status != contract.StatusAccepted {
			t.Fatalf("JSON envelope status = %q, want %q", envelope.Status, contract.StatusAccepted)
		}
	})

	t.Run("unknown mutation outcome", func(t *testing.T) {
		fault := contract.Fault{Code: contract.CodeOutcomeUnknown, Message: "no disposition"}
		op := &fakeOperator{fn: func(context.Context, string, contract.Request) (contract.Result, error) {
			return contract.Result{
				Schema:    "zatiti.result/v1",
				CommandID: "00000000-0000-4000-8000-0000000000b2",
				Payload:   contract.Payload{Status: contract.StatusFailed, Error: &fault},
			}, &fault
		}}

		stdout, _, code := run(t, []string{"task", "start", "--submission-key", "k2"}, op, descriptors, nil)
		if code != 6 {
			t.Fatalf("exit code = %d, want 6", code)
		}
		if strings.Contains(stdout, "status:   completed") || strings.Contains(stdout, "accepted") {
			t.Fatalf("an unknown-outcome task.start must never render as completed or accepted: %q", stdout)
		}
		if !strings.Contains(stdout, "outcome unknown") {
			t.Fatalf("expected the unknown outcome to be named honestly: %q", stdout)
		}

		stdoutJSON, _, codeJSON := run(t, []string{"task", "start", "--submission-key", "k2", "--json"}, op, descriptors, nil)
		if codeJSON != 6 {
			t.Fatalf("json mode exit code = %d, want 6", codeJSON)
		}
		var envelope contract.Result
		if err := json.Unmarshal([]byte(strings.TrimSpace(stdoutJSON)), &envelope); err != nil {
			t.Fatalf("stdout was not one valid envelope: %v", err)
		}
		if envelope.Status == contract.StatusCompleted || envelope.Status == contract.StatusAccepted {
			t.Fatalf("JSON envelope status = %q, must never be completed or accepted for an unknown mutation outcome", envelope.Status)
		}
	})
}

// TestBlockedFaultsAreLabeledDistinctlyFromUnknownAndReviewRequired proves
// the "accepted versus completed versus blocked/unknown" rendering split:
// the four prerequisite-style faults that share CLI exit 5 (prerequisite
// blocking further progress on a named external condition) are named
// "blocked" and never collapse into the generic "failed" bucket, the
// outcome_unknown/controller_unavailable pair (CLI exit 6, no
// authoritative disposition) is never labeled "blocked", and
// review_required keeps its own distinct label. Every case still carries
// its exact fault code on the "fault:" line regardless of the status
// summary.
func TestBlockedFaultsAreLabeledDistinctlyFromUnknownAndReviewRequired(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("task.start", []string{"task", "start"}, contract.ModeMutation, true),
	}

	cases := []struct {
		code       string
		wantExit   int
		wantSubstr string
		notSubstr  string
	}{
		{contract.CodePrerequisiteMissing, 5, "blocked", "outcome unknown"},
		{contract.CodeExternalActionRequired, 5, "blocked", "outcome unknown"},
		{contract.CodeBudgetUnavailable, 5, "blocked", "outcome unknown"},
		{contract.CodeCapabilityUnsupported, 5, "blocked", "outcome unknown"},
		{contract.CodeOutcomeUnknown, 6, "outcome unknown", "blocked"},
		{contract.CodeControllerUnavailable, 6, "controller unavailable", "blocked"},
		{contract.CodeReviewRequired, 3, "manual review required", "blocked"},
	}

	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			fault := contract.Fault{Code: tc.code, Message: "detail"}
			op := &fakeOperator{fn: func(context.Context, string, contract.Request) (contract.Result, error) {
				return contract.Result{
					Schema:    "zatiti.result/v1",
					CommandID: "00000000-0000-4000-8000-0000000000b3",
					Payload:   contract.Payload{Status: contract.StatusFailed, Error: &fault},
				}, &fault
			}}

			stdout, _, code := run(t, []string{"task", "start", "--submission-key", "k3"}, op, descriptors, nil)
			if code != tc.wantExit {
				t.Fatalf("exit code = %d, want %d", code, tc.wantExit)
			}
			if !strings.Contains(stdout, tc.wantSubstr) {
				t.Fatalf("rendering for %s did not contain %q: %q", tc.code, tc.wantSubstr, stdout)
			}
			if strings.Contains(stdout, tc.notSubstr) {
				t.Fatalf("rendering for %s unexpectedly contained %q: %q", tc.code, tc.notSubstr, stdout)
			}
			if !strings.Contains(stdout, "fault:    "+tc.code) {
				t.Fatalf("rendering for %s did not carry its exact fault code: %q", tc.code, stdout)
			}
		})
	}
}

// failingWriter always errors, standing in for a broken stdout (a closed
// pipe, a full disk) so writeEnvelope's own error path is exercised.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errPlain("write failed") }

// TestWriteEnvelopeFailureIsReported proves a JSON envelope the CLI cannot
// write to stdout still produces a diagnostic and a non-zero exit rather
// than being silently swallowed.
func TestWriteEnvelopeFailureIsReported(t *testing.T) {
	descriptors := []contract.Descriptor{
		descriptor("capabilities.list", []string{"capabilities"}, contract.ModeQuery, false),
	}
	op := &fakeOperator{}
	var errBuf strings.Builder
	code := cli.Execute(context.Background(), []string{"capabilities", "--json"}, op, descriptors, cli.IO{
		In:  strings.NewReader(""),
		Out: failingWriter{},
		Err: &errBuf,
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if errBuf.Len() == 0 {
		t.Fatal("expected a diagnostic on stderr for a failed write")
	}
}
