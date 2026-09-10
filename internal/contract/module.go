package contract

import (
	"context"
	"encoding/json"
)

// Envelope schema identifiers for the common request/result envelopes.
const (
	SchemaRequest = "zatiti.request/v1"
	SchemaResult  = "zatiti.result/v1"
)

// Descriptor is the static contract of one operation: visibility, mode,
// effect classification, schemas and transport bindings.
type Descriptor struct {
	ID               string
	Version          int64
	Owner            string
	Visibility       string // public | internal
	Mode             string // query | mutation
	Effect           string // local | disclosure | external_read | external_mutation
	InputSchema      json.RawMessage
	OutputSchema     json.RawMessage
	CompletionSchema json.RawMessage
	CLI              []string
	MCP              string
	ScopeRequired    []string
	Callers          []string
	ExpectedVersion  bool
	SubmissionKey    bool
}

// Handler executes one operation inside a Unit. Returned errors carry a
// *Fault and roll back the entire transaction.
type Handler func(ctx context.Context, unit Unit, invocation Invocation) (Payload, error)

// Module is one domain owner. Every owner constructs as
// New(Dependencies) (*Service, error) and *Service implements Module.
// Constructors must not query peers or start goroutines.
type Module interface {
	Name() string
	Migrations() []Migration
	Descriptors() []Descriptor
	Handle(ctx context.Context, unit Unit, invocation Invocation) (Payload, error)
}

// Ports routes calls between owners. A domain receives an owner-bound view;
// internal cross-owner calls keep the same Unit, actor, scope, generation
// and transaction, cannot mint authority and are not public operations.
type Ports interface {
	Call(ctx context.Context, unit Unit, invocation Invocation) (Payload, error)
}

// Dependencies are the declared capabilities injected into every domain
// owner constructor. Only declared outgoing ports may be used.
type Dependencies struct {
	Clock   Clock
	IDs     IDSource
	Ports   Ports
	Secrets SecretStore
	Blobs   BlobStore
}
