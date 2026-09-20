package contract

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Dispatch is one physical provider invocation intent for an adapter.
// Each physical call, including reconciliation and retries, has a distinct
// attempt and its own Dispatch.
//
// CallbackRoute is additive for revision 3 (docs/implementation/contracts.md,
// "Effects callback routing"; the $defs/Dispatch schema in
// docs/implementation/operations.json carries the same optional field): the
// effects owner persists a typed CallbackRoute (worker_turn/job/memory/
// skill/connection plus the relevant turn/step/job id) alongside the action
// and returns it at claim, so the controller resolves callback routing from
// this persisted route instead of inferring it from adapter parameters. Its
// concrete shape is owned by internal/effects, not contract, so it travels
// as json.RawMessage exactly like Action; omitted preserves the revision-2
// wire shape unchanged.
type Dispatch struct {
	OperationID   ID              `json:"operation_id"`
	AttemptID     ID              `json:"attempt_id"`
	Generation    int64           `json:"generation"`
	Adapter       string          `json:"adapter"`
	Action        json.RawMessage `json:"action"`
	CredentialRef string          `json:"credential_ref"`
	ProviderKey   string          `json:"provider_key,omitempty"`
	Deadline      time.Time       `json:"deadline"`
	CallbackRoute json.RawMessage `json:"callback_route,omitempty"`
}

// Observation is an adapter's recorded outcome for one physical attempt.
// Disposition succeeded means authoritative provider evidence exists;
// unknown preserves uncertainty until such evidence arrives.
type Observation struct {
	Disposition       string          `json:"disposition"` // succeeded | failed | accepted | unknown | not_sent
	ProviderReference string          `json:"provider_reference,omitempty"`
	Evidence          json.RawMessage `json:"evidence"`
	Usage             json.RawMessage `json:"usage"`
	ConfirmedAt       *time.Time      `json:"confirmed_at,omitempty"`
}

// Adapter performs provider side effects outside transactions. Adapters are
// constructed as New(AdapterDependencies, json.RawMessage) (Adapter, error);
// config is strictly validated, versioned and secret-free.
type Adapter interface {
	Name() string
	Contract() json.RawMessage
	Invoke(ctx context.Context, dispatch Dispatch) (Observation, error)
	Reconcile(ctx context.Context, dispatch Dispatch) (Observation, error)
}

// AdapterDependencies are the capabilities injected into adapter
// constructors. Credential references resolve outside transactions.
type AdapterDependencies struct {
	HTTP    *http.Client
	Secrets SecretStore
	Clock   Clock
	Blobs   BlobStore
}
