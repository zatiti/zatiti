package contract

import (
	"encoding/json"
	"time"
)

// ID is a stable UUIDv4 identity on the wire (lowercase, hyphenated).
type ID string

// Digest is a SHA-256 digest as lowercase hex.
type Digest string

// Version is a monotonically increasing resource version; >= 1.
type Version int64

// Clock supplies deterministic time to owners and adapters.
type Clock interface {
	Now() time.Time
}

// IDSource mints new UUIDv4 identities.
type IDSource interface {
	New() ID
}

// Scope carries the explicit installation/organization/project/worker/task
// context of a call. Every referenced object is revalidated against it;
// missing scope is an error when ambiguous, never an implicit global.
type Scope struct {
	InstallationID ID `json:"installation_id"`
	OrganizationID ID `json:"organization_id,omitempty"`
	ProjectID      ID `json:"project_id,omitempty"`
	WorkerID       ID `json:"worker_id,omitempty"`
	TaskID         ID `json:"task_id,omitempty"`
}

// Actor is the authenticated caller of an operation. CredentialID references
// identity-owned credential metadata; raw secret material never appears here.
type Actor struct {
	PrincipalID  ID     `json:"principal_id"`
	Kind         string `json:"kind"` // human | client_agent | worker | service
	CredentialID ID     `json:"credential_id"`
}

// Request is the common operation request envelope (zatiti.request/v1).
type Request struct {
	Schema        string          `json:"schema"` // zatiti.request/v1
	SubmissionKey string          `json:"submission_key,omitempty"`
	Input         json.RawMessage `json:"input"`
}

// Fault is the stable error vocabulary shared by every transport.
type Fault struct {
	Code      string          `json:"code"`
	Message   string          `json:"message"`
	Retryable bool            `json:"retryable"`
	Details   json.RawMessage `json:"details,omitempty"`
}

// Error implements the error interface so handler errors carry the fault.
func (f *Fault) Error() string {
	if f == nil {
		return "<nil>"
	}
	if f.Message == "" {
		return f.Code
	}
	return f.Code + ": " + f.Message
}

// Outcome carries a typed handler result. Accepted results and page cursors
// survive typed registration through Outcome[O]; Data is O.
type Outcome[T any] struct {
	Status     string
	Data       T
	NextCursor *string
}

// Payload is the operation result body. Output schemas describe Data, not
// the result envelope.
type Payload struct {
	Status     string          `json:"status"` // completed | accepted | failed
	Data       json.RawMessage `json:"data"`
	Error      *Fault          `json:"error"`
	NextCursor *string         `json:"next_cursor"`
}

// Result is the common operation result envelope (zatiti.result/v1).
type Result struct {
	Schema    string `json:"schema"` // zatiti.result/v1
	CommandID ID     `json:"command_id"`
	Payload
}

// Invocation names one operation call with its expected contract version.
type Invocation struct {
	Operation string
	Version   int64
	Input     json.RawMessage
}

// Event is one observed state transition or fact in the storage event log.
// Delivery is at least once; consumers deduplicate on ID.
type Event struct {
	ID              ID              `json:"id"`
	Sequence        int64           `json:"sequence"`
	At              time.Time       `json:"at"`
	Scope           Scope           `json:"scope"`
	Kind            string          `json:"kind"` // owner.entity.transition
	ResourceID      ID              `json:"resource_id"`
	ResourceVersion Version         `json:"resource_version"`
	Data            json.RawMessage `json:"data"`
}
