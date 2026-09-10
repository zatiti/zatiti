package contract

import "testing"

// TestZ02WireErrorMappings proves the local half of Z02.wire_errors: for
// every fixture class named by the acceptance case (completed, accepted,
// invalid, denied, review-required, stale, unavailable, unknown) the CLI
// exit and HTTP status mappings match the specification. The end-to-end
// emission (one stdout envelope for CLI, structuredContent/text/isError for
// MCP) is owned by the transport packages and the parity suite.
func TestZ02WireErrorMappings(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		fault    *Fault
		wantExit int
		wantHTTP int
	}{
		{"completed", nil, 0, 200},
		{"accepted", nil, 0, 200},
		{"invalid", &Fault{Code: CodeInvalidInput}, 2, 400},
		{"denied", &Fault{Code: CodePermissionDenied}, 3, 403},
		{"review required", &Fault{Code: CodeReviewRequired}, 3, 409},
		{"stale", &Fault{Code: CodeStaleVersion}, 4, 409},
		{"unavailable", &Fault{Code: CodeControllerUnavailable}, 6, 503},
		{"unknown outcome", &Fault{Code: CodeOutcomeUnknown}, 6, 409},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CLIExit(tc.fault); got != tc.wantExit {
				t.Fatalf("CLIExit = %d, want %d", got, tc.wantExit)
			}
			if got := HTTPStatus(tc.fault); got != tc.wantHTTP {
				t.Fatalf("HTTPStatus = %d, want %d", got, tc.wantHTTP)
			}
		})
	}
}

func TestCLIExitFullTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code string
		want int
	}{
		{CodeInvalidInput, 2},
		{CodePermissionDenied, 3},
		{CodeReviewRequired, 3},
		{CodeStaleVersion, 4},
		{CodeSubmissionConflict, 4},
		{CodeConflict, 4},
		{CodePrerequisiteMissing, 5},
		{CodeExternalActionRequired, 5},
		{CodeBudgetUnavailable, 5},
		{CodeCapabilityUnsupported, 5},
		{CodeControllerUnavailable, 6},
		{CodeOutcomeUnknown, 6},
		{CodeNotFound, 1},
		{CodeCursorExpired, 1},
		{CodeArtifactFault, 1},
		{CodeVerificationFailed, 1},
		{CodeInternalError, 1},
		{"", 1},
		{"mystery_code", 1},
	}
	for _, tc := range cases {
		if got := CLIExit(&Fault{Code: tc.code}); got != tc.want {
			t.Errorf("CLIExit(%q) = %d, want %d", tc.code, got, tc.want)
		}
	}
}

func TestHTTPStatusFullTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code string
		want int
	}{
		{CodeInvalidInput, 400},
		{CodePermissionDenied, 403},
		{CodeNotFound, 404},
		{CodeStaleVersion, 409},
		{CodeSubmissionConflict, 409},
		{CodeConflict, 409},
		{CodeReviewRequired, 409},
		{CodeOutcomeUnknown, 409},
		{CodeArtifactFault, 409},
		{CodeCursorExpired, 410},
		{CodePrerequisiteMissing, 422},
		{CodeExternalActionRequired, 422},
		{CodeBudgetUnavailable, 422},
		{CodeCapabilityUnsupported, 422},
		{CodeVerificationFailed, 422},
		{CodeControllerUnavailable, 503},
		{CodeInternalError, 500},
		{"", 500},
		{"mystery_code", 500},
	}
	for _, tc := range cases {
		if got := HTTPStatus(&Fault{Code: tc.code}); got != tc.want {
			t.Errorf("HTTPStatus(%q) = %d, want %d", tc.code, got, tc.want)
		}
	}
}
