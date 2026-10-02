package contract

import (
	"context"
	"encoding/json"
	"errors"
)

// CallOrigin identifies the trusted transport through which a call arrived.
// It is set by the session, never accepted as client-supplied authority.
type CallOrigin string

const (
	CallOriginUnix      CallOrigin = "unix"
	CallOriginRemoteWS  CallOrigin = "remote-ws"
	CallOriginBrowserWS CallOrigin = "browser-ws"
)

// StreamResult carries the same envelope as HTTP operations.
type StreamResult = Result

var ErrWSBackpressure = errors.New("websocket outbound queue is full")

// WSSession owns transport authentication and bounded serialized writes.
// Bind takes a private credential copy because authentication may zero it.
// Call reauthenticates and applies ordinary application audit and authority.
type WSSession interface {
	Transport() string
	Principal() ID
	Bind(context.Context, []byte) (ID, error)
	Call(context.Context, CallOrigin, string, json.RawMessage) (StreamResult, error)
	Send(json.RawMessage) error
	Close(int, string)
	Done() <-chan struct{}
}

// WSHandler is injected into server; server and hub never import each other.
// BoundTransport refuses a bound browser principal on other transports.
// Drain stops hand-out and tails within its supplied shutdown deadline.
type WSHandler interface {
	Subscribe(context.Context, WSSession, json.RawMessage) error
	ServeBrowser(context.Context, WSSession, <-chan json.RawMessage)
	BoundTransport(ID) (string, bool)
	Drain(context.Context)
}
