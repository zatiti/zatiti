package contract

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestResultEnvelopeGolden pins the result envelope to the frozen example
// (R8.4-002): snake_case keys, embedded payload flattening, explicit nulls.
func TestResultEnvelopeGolden(t *testing.T) {
	t.Parallel()
	r := Result{
		Schema:    SchemaResult,
		CommandID: "00000000-0000-4000-8000-000000000001",
		Payload: Payload{
			Status: StatusCompleted,
			Data:   json.RawMessage(`{"draft_id":"00000000-0000-4000-8000-000000000002","version":1}`),
		},
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"schema":"zatiti.result/v1","command_id":"00000000-0000-4000-8000-000000000001",` +
		`"status":"completed","data":{"draft_id":"00000000-0000-4000-8000-000000000002","version":1},` +
		`"error":null,"next_cursor":null}`
	if string(raw) != want {
		t.Fatalf("envelope =\n%s\nwant\n%s", raw, want)
	}

	var back Result
	if err := DecodeStrict([]byte(want), &back); err != nil {
		t.Fatalf("strict decode of frozen example: %v", err)
	}
	if back.CommandID != r.CommandID || back.Status != StatusCompleted || back.Error != nil {
		t.Fatalf("round trip mismatch: %+v", back)
	}
}

func TestFailedPayloadCarriesFault(t *testing.T) {
	t.Parallel()
	r := Result{
		Schema:    SchemaResult,
		CommandID: "00000000-0000-4000-8000-000000000003",
		Payload: Payload{
			Status: StatusFailed,
			Data:   json.RawMessage(`null`),
			Error:  &Fault{Code: CodePermissionDenied, Message: "not permitted", Retryable: false},
		},
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Result
	if err := DecodeStrict(raw, &back); err != nil {
		t.Fatalf("strict decode: %v", err)
	}
	if back.Error == nil || back.Error.Code != CodePermissionDenied || back.Error.Message != "not permitted" {
		t.Fatalf("fault lost: %+v", back.Error)
	}
}

func TestPayloadAndEventWireTags(t *testing.T) {
	t.Parallel()
	p := Payload{Status: StatusAccepted, Data: json.RawMessage(`{}`), NextCursor: strPtr("cursor-1")}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"status":"accepted","data":{},"error":null,"next_cursor":"cursor-1"}`
	if string(raw) != want {
		t.Fatalf("payload = %s, want %s", raw, want)
	}

	ev := Event{
		ID: NewID(), Sequence: 7, At: time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC),
		Scope:           Scope{InstallationID: "00000000-0000-4000-8000-000000000001"},
		Kind:            "tasks.task.created",
		ResourceID:      "00000000-0000-4000-8000-00000000000a",
		ResourceVersion: 1,
		Data:            json.RawMessage(`{}`),
	}
	raw, err = json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	keys := map[string]any{}
	if err := DecodeStrict(raw, &keys); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	for _, k := range []string{"id", "sequence", "at", "scope", "kind", "resource_id", "resource_version", "data"} {
		if _, ok := keys[k]; !ok {
			t.Fatalf("event missing key %q in %s", k, raw)
		}
	}
}

// Compile-time proof that the frozen interfaces are implementable by
// external packages: every declaration below is copied from the contract.
var (
	_ Clock            = fakeClock{}
	_ IDSource         = fakeIDs{}
	_ Reader           = fakeReader{}
	_ Unit             = fakeUnit{}
	_ Ownership        = fakeOwnership{}
	_ Database         = fakeDB{}
	_ Module           = fakeModule{}
	_ Ports            = fakePorts{}
	_ SecretStore      = fakeSecrets{}
	_ BlobStore        = fakeBlobs{}
	_ Operator         = fakeOperator{}
	_ CredentialSource = fakeCreds{}
	_ Adapter          = fakeAdapter{}
	_ Authenticator    = fakeAuth{}
	_ LocalIO          = fakeIO{}
	_ Verifier         = fakeVerifier{}
)

type fakeClock struct{}

func (fakeClock) Now() time.Time { return time.Time{} }

type fakeIDs struct{}

func (fakeIDs) New() ID { return NewID() }

type fakeReader struct{}

func (fakeReader) QueryContext(context.Context, string, ...any) (*sql.Rows, error) { return nil, nil }
func (fakeReader) QueryRowContext(context.Context, string, ...any) *sql.Row        { return nil }

type fakeUnit struct{ fakeReader }

func (fakeUnit) ExecContext(context.Context, string, ...any) (sql.Result, error) { return nil, nil }
func (fakeUnit) Actor() Actor                                                    { return Actor{} }
func (fakeUnit) Scope() Scope                                                    { return Scope{} }
func (fakeUnit) Generation() int64                                               { return 1 }
func (fakeUnit) ReadOnly() bool                                                  { return false }
func (fakeUnit) Emit(context.Context, Event) error                               { return nil }

type fakeOwnership struct{}

func (fakeOwnership) Held() bool            { return true }
func (fakeOwnership) Lost() <-chan struct{} { return make(chan struct{}) }
func (fakeOwnership) Close() error          { return nil }

type fakeDB struct{}

func (fakeDB) StartGeneration(context.Context) (int64, error)              { return 1, nil }
func (fakeDB) Generation(context.Context) (int64, error)                   { return 1, nil }
func (fakeDB) Read(context.Context, Actor, Scope, func(Unit) error) error  { return nil }
func (fakeDB) Write(context.Context, Actor, Scope, func(Unit) error) error { return nil }
func (fakeDB) Migrate(context.Context, []Migration) error                  { return nil }
func (fakeDB) Events(context.Context, int64, int) ([]Event, error)         { return nil, nil }
func (fakeDB) Backup(context.Context, io.Writer) error                     { return nil }
func (fakeDB) Close() error                                                { return nil }

type fakeModule struct{}

func (fakeModule) Name() string                                              { return "fake" }
func (fakeModule) Migrations() []Migration                                   { return nil }
func (fakeModule) Descriptors() []Descriptor                                 { return nil }
func (fakeModule) Handle(context.Context, Unit, Invocation) (Payload, error) { return Payload{}, nil }

type fakePorts struct{}

func (fakePorts) Call(context.Context, Unit, Invocation) (Payload, error) { return Payload{}, nil }

type fakeSecrets struct{}

func (fakeSecrets) Put(context.Context, string, []byte) (string, error) { return "", nil }
func (fakeSecrets) Get(context.Context, string) ([]byte, error)         { return nil, nil }
func (fakeSecrets) Delete(context.Context, string) error                { return nil }

type fakeBlobs struct{}

func (fakeBlobs) Stage(context.Context, io.Reader, int64) (string, Digest, int64, error) {
	return "", "", 0, nil
}
func (fakeBlobs) Publish(context.Context, string, Digest) error                     { return nil }
func (fakeBlobs) Open(context.Context, Digest, int64, int64) (io.ReadCloser, error) { return nil, nil }
func (fakeBlobs) RemoveStaged(context.Context, string) error                        { return nil }

type fakeOperator struct{}

func (fakeOperator) Call(context.Context, string, Request) (Result, error) { return Result{}, nil }

type fakeCreds struct{}

func (fakeCreds) Credential(context.Context) ([]byte, error) { return nil, nil }

type fakeAdapter struct{}

func (fakeAdapter) Name() string                                          { return "fake" }
func (fakeAdapter) Contract() json.RawMessage                             { return nil }
func (fakeAdapter) Invoke(context.Context, Dispatch) (Observation, error) { return Observation{}, nil }
func (fakeAdapter) Reconcile(context.Context, Dispatch) (Observation, error) {
	return Observation{}, nil
}

type fakeAuth struct{}

func (fakeAuth) Authenticate(context.Context, Reader, []byte) (Actor, error) { return Actor{}, nil }
func (fakeAuth) AuthenticateCertificate(context.Context, Reader, Digest) (Actor, error) {
	return Actor{}, nil
}

type fakeIO struct{}

func (fakeIO) Prepare(context.Context, Unit, Invocation) (IOPlan, error)       { return IOPlan{}, nil }
func (fakeIO) Perform(context.Context, IOPlan) (IOResult, error)               { return IOResult{}, nil }
func (fakeIO) Finish(context.Context, Unit, IOPlan, IOResult) (Payload, error) { return Payload{}, nil }

type fakeVerifier struct{}

func (fakeVerifier) Verify(context.Context, Verification) (VerificationResult, error) {
	return VerificationResult{}, nil
}

// AdapterDependencies must accept the frozen field set with *http.Client.
var _ = AdapterDependencies{HTTP: &http.Client{}}

func TestOutcomeTypedCarrier(t *testing.T) {
	t.Parallel()
	out := Outcome[string]{Status: StatusAccepted, Data: "job-1"}
	if out.Status != StatusAccepted || out.Data != "job-1" {
		t.Fatalf("typed outcome fields broken: %+v", out)
	}
	cursor := "next"
	o2 := Outcome[int64]{Status: StatusCompleted, Data: 5, NextCursor: &cursor}
	if o2.Data != 5 || *o2.NextCursor != "next" {
		t.Fatalf("typed outcome fields broken: %+v", o2)
	}
}

func strPtr(s string) *string { return &s }
