// Package server owns the controller's private socket HTTP/JSON transport
// and the explicit remote-desktop mutual TLS transport. It exposes the
// versioned POST /v1/operations/{operation_id} endpoint over a private
// Unix-domain socket and, when configured, over an authenticated TLS
// listener. Every request is authenticated before dispatch and forwarded to
// application.Application, which owns authorization, transaction
// composition and the result envelope. The server package never talks to
// storage, a scheduler or a domain module directly.
package server
