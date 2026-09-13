package evidence

import (
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Guards test the fences that protect dispatch itself: unknown operations,
// protocol versioning and mutation-only writes.

func TestHandleRejectsUnknownOperation(t *testing.T) {
	env := newEnv(t)
	_ = env.expectFault("evidence.nope", struct{}{}, contract.CodeNotFound)
}

func TestHandleRejectsWrongProtocolVersion(t *testing.T) {
	env := newEnv(t)
	_ = env.expectFaultOnVersion(opEventGet, 2, eventGetInput{Scope: env.scope, ID: env.ids.New()}, contract.CodeInvalidInput)
}

func TestHandleRejectsMutationOnReadOnlyUnit(t *testing.T) {
	env := newEnv(t)
	payload, err := env.callReadOnly(opCommandBegin, commandBeginInput{
		PrincipalID: env.ids.New(), Operation: "widget.create", OperationVersion: 1,
		SubmissionKey: "key-1", RequestDigest: digestOf(t, 1),
	})
	if err == nil {
		if payload.Error == nil {
			t.Fatal("begin on a read-only unit completed; want a fault")
		}
		if payload.Error.Code != contract.CodeInvalidInput {
			t.Fatalf("read-only fault %s, want %s", payload.Error.Code, contract.CodeInvalidInput)
		}
		return
	}
	f := faultFrom(err)
	if f == nil || f.Code != contract.CodeInvalidInput {
		t.Fatalf("read-only error %v, want %s", err, contract.CodeInvalidInput)
	}
}

func TestCommandGetRejectsForeignInstallationScope(t *testing.T) {
	env := newEnv(t)
	foreign := contract.Scope{InstallationID: env.ids.New()}
	_ = env.expectFault(opCommandGet, commandGetInput{
		Scope: foreign, SubmissionKey: "key-1", Operation: "widget.create", OperationVersion: 1,
	}, contract.CodePermissionDenied)
}

func TestCommandGetRejectsEmptyInstallationScope(t *testing.T) {
	env := newEnv(t)
	_ = env.expectFault(opCommandGet, commandGetInput{
		Scope: contract.Scope{}, SubmissionKey: "key-1", Operation: "widget.create", OperationVersion: 1,
	}, contract.CodeInvalidInput)
}
