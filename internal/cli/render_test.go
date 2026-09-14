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
