package evidence

import (
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Wire DTOs. JSON is snake_case and unknown fields are rejected at the
// decode boundary before these types are filled. Output schemas describe
// Payload.Data, not the result envelope. contract.Scope, contract.Event,
// contract.Fault and contract.Result already mirror their $defs one-for-one
// (Result embeds Payload, so its wire shape is flat), so this package reuses
// them directly instead of declaring redundant local mirrors.

// wireCommand mirrors $defs/Command: the durable disposition record evidence
// retains for every public mutation. It is only ever exposed once finished;
// an in-flight (begin-only) row never survives to be projected.
type wireCommand struct {
	ID               contract.ID     `json:"id"`
	PrincipalID      contract.ID     `json:"principal_id"`
	Operation        string          `json:"operation"`
	OperationVersion int64           `json:"operation_version"`
	SubmissionKey    string          `json:"submission_key"`
	RequestDigest    contract.Digest `json:"request_digest"`
	Status           string          `json:"status"`
	Data             json.RawMessage `json:"data"`
	ErrorCode        string          `json:"error_code,omitempty"`
	Result           contract.Result `json:"result"`
}

// commandBeginInput is the _evidence.command.begin input.
type commandBeginInput struct {
	PrincipalID      contract.ID     `json:"principal_id"`
	Operation        string          `json:"operation"`
	OperationVersion int64           `json:"operation_version"`
	SubmissionKey    string          `json:"submission_key"`
	RequestDigest    contract.Digest `json:"request_digest"`
}

// commandBeginOutput is the _evidence.command.begin output. Existing is
// present only on replay (an identity that already carries a finished
// disposition); a fresh reservation carries only the minted command_id.
type commandBeginOutput struct {
	CommandID contract.ID  `json:"command_id"`
	Existing  *wireCommand `json:"existing,omitempty"`
}

// commandFinishInput is the _evidence.command.finish input.
type commandFinishInput struct {
	CommandID contract.ID     `json:"command_id"`
	Result    contract.Result `json:"result"`
}

// commandResourceBody is the shared output shape of _evidence.command.finish
// and command.get.
type commandResourceBody struct {
	Resource wireCommand `json:"resource"`
}

// snapshotInput is the _evidence.snapshot input.
type snapshotInput struct {
	Scope contract.Scope `json:"scope"`
}

// snapshotOutput is the _evidence.snapshot output.
type snapshotOutput struct {
	LastSequence int64 `json:"last_sequence"`
}

// commandGetInput is the command.get input.
type commandGetInput struct {
	Scope            contract.Scope `json:"scope"`
	SubmissionKey    string         `json:"submission_key"`
	Operation        string         `json:"operation"`
	OperationVersion int64          `json:"operation_version"`
}

// eventGetInput is the event.get input.
type eventGetInput struct {
	Scope contract.Scope `json:"scope"`
	ID    contract.ID    `json:"id"`
}

// eventResourceBody is the event.get output.
type eventResourceBody struct {
	Resource contract.Event `json:"resource"`
}

// eventFilterInput mirrors event.list's structured filter object. Only
// organization_id, worker_id and task_id map onto an event's own fields;
// state, key, parent_id, descendants and needs_you have no analogue on an
// event and are refused as unsupported.
type eventFilterInput struct {
	State          *string      `json:"state,omitempty"`
	Key            *string      `json:"key,omitempty"`
	ParentID       *contract.ID `json:"parent_id,omitempty"`
	WorkerID       *contract.ID `json:"worker_id,omitempty"`
	TaskID         *contract.ID `json:"task_id,omitempty"`
	OrganizationID *contract.ID `json:"organization_id,omitempty"`
	Descendants    *bool        `json:"descendants,omitempty"`
	NeedsYou       *bool        `json:"needs_you,omitempty"`
}

// eventListInput is the event.list input.
type eventListInput struct {
	Scope  contract.Scope    `json:"scope"`
	Cursor *string           `json:"cursor,omitempty"`
	Limit  *int64            `json:"limit,omitempty"`
	Filter *eventFilterInput `json:"filter,omitempty"`
}

// eventListOutput is the event.list output.
type eventListOutput struct {
	Items []contract.Event `json:"items"`
}

// eventFilter is the normalized, validated form of eventFilterInput used to
// build queries and bind cursors.
type eventFilter struct {
	OrganizationID contract.ID `json:"organization_id,omitempty"`
	WorkerID       contract.ID `json:"worker_id,omitempty"`
	TaskID         contract.ID `json:"task_id,omitempty"`
}
