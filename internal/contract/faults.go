package contract

// Stable fault codes. The string values are frozen by the shared contract;
// the constants exist so owners and transports cannot misspell them.
const (
	CodeInvalidInput           = "invalid_input"
	CodeNotFound               = "not_found"
	CodePermissionDenied       = "permission_denied"
	CodeStaleVersion           = "stale_version"
	CodeReviewRequired         = "review_required"
	CodeSubmissionConflict     = "submission_conflict"
	CodeConflict               = "conflict"
	CodeCursorExpired          = "cursor_expired"
	CodePrerequisiteMissing    = "prerequisite_missing"
	CodeExternalActionRequired = "external_action_required"
	CodeBudgetUnavailable      = "budget_unavailable"
	CodeCapabilityUnsupported  = "capability_unsupported"
	CodeVerificationFailed     = "verification_failed"
	CodeArtifactFault          = "artifact_fault"
	CodeOutcomeUnknown         = "outcome_unknown"
	CodeControllerUnavailable  = "controller_unavailable"
	CodeInternalError          = "internal_error"
)

// Payload status values for Outcome and Payload.
const (
	StatusCompleted = "completed"
	StatusAccepted  = "accepted"
	StatusFailed    = "failed"
)

// Observation disposition values for adapter results.
const (
	DispositionSucceeded = "succeeded"
	DispositionFailed    = "failed"
	DispositionAccepted  = "accepted"
	DispositionUnknown   = "unknown"
	DispositionNotSent   = "not_sent"
)

// Actor kind values.
const (
	KindHuman       = "human"
	KindClientAgent = "client_agent"
	KindWorker      = "worker"
	KindService     = "service"
)

// Descriptor visibility, mode and effect values.
const (
	VisibilityPublic   = "public"
	VisibilityInternal = "internal"

	ModeQuery    = "query"
	ModeMutation = "mutation"

	EffectLocal            = "local"
	EffectDisclosure       = "disclosure"
	EffectExternalRead     = "external_read"
	EffectExternalMutation = "external_mutation"
)

// CLIExit maps a fault to the CLI process exit code. A nil fault is a
// completed or accepted command: exit 0. Mapping: invalid_input 2;
// permission_denied/review_required 3; stale_version/submission_conflict/
// conflict 4; prerequisite_missing/external_action_required/
// budget_unavailable/capability_unsupported 5; controller_unavailable/
// outcome_unknown 6; every other failure 1. Machine clients inspect the
// result envelope as well as the exit code.
func CLIExit(f *Fault) int {
	if f == nil {
		return 0
	}
	switch f.Code {
	case CodeInvalidInput:
		return 2
	case CodePermissionDenied, CodeReviewRequired:
		return 3
	case CodeStaleVersion, CodeSubmissionConflict, CodeConflict:
		return 4
	case CodePrerequisiteMissing, CodeExternalActionRequired, CodeBudgetUnavailable, CodeCapabilityUnsupported:
		return 5
	case CodeControllerUnavailable, CodeOutcomeUnknown:
		return 6
	default:
		return 1
	}
}

// HTTPStatus maps a fault to the HTTP status for POST
// /v1/operations/{operation_id}. A nil fault is a completed command: 200
// (accepted responses use 202 and are chosen by the caller from payload
// status, not from the fault). Domain failures always include the result
// envelope regardless of status code.
func HTTPStatus(f *Fault) int {
	if f == nil {
		return 200
	}
	switch f.Code {
	case CodeInvalidInput:
		return 400
	case CodePermissionDenied:
		return 403
	case CodeNotFound:
		return 404
	case CodeStaleVersion, CodeSubmissionConflict, CodeConflict, CodeReviewRequired, CodeOutcomeUnknown, CodeArtifactFault:
		return 409
	case CodeCursorExpired:
		return 410
	case CodePrerequisiteMissing, CodeExternalActionRequired, CodeBudgetUnavailable, CodeCapabilityUnsupported, CodeVerificationFailed:
		return 422
	case CodeControllerUnavailable:
		return 503
	default:
		return 500
	}
}
