package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/zatiti/zatiti/internal/application"
	"github.com/zatiti/zatiti/internal/contract"
)

// bootstrapOperation is the one operation permitted before the installation
// exists. It is reachable only over the local socket: however well a
// certificate authenticates a remote caller, that caller can never
// initialize an installation it has no authority over yet, because no
// authority exists yet to check.
const bootstrapOperation = "installation.init"

// transportOrigin distinguishes the local private socket from the remote
// mutual TLS listener. The two transports authenticate callers differently
// and grant different capabilities: only the local socket may bootstrap.
type transportOrigin int

const (
	originLocal transportOrigin = iota
	originRemote
)

func (o transportOrigin) String() string {
	if o == originRemote {
		return "remote"
	}
	return "local"
}

// operationHandler serves POST /v1/operations/{operation_id} for one
// transport. It authenticates the caller the way that transport requires,
// forwards the call to application.Application and renders the result or
// fault envelope. Nothing here logs or otherwise surfaces the credential
// bytes it reads.
type operationHandler struct {
	app          *application.Application
	maxBodyBytes int64
	origin       transportOrigin
}

func (h *operationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	operation := r.PathValue("operation_id")

	httpStatus := writeEnvelope(w, h.handle(r, operation))

	slog.InfoContext(r.Context(), "server.operation",
		"operation", operation,
		"transport", h.origin.String(),
		"http_status", httpStatus,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

// handle runs the authenticated dispatch for one request: content type and
// body-size enforcement, strict envelope decoding, transport-appropriate
// authentication and the call into application.Application. It always
// returns one well-formed envelope; the HTTP status is derived from that
// envelope alone. The dispatch context is detached from the request's own
// cancellation: a caller that disconnects mid-request never causes the
// server to assume an in-flight or already-committed command rolled back.
func (h *operationHandler) handle(r *http.Request, operation string) contract.Result {
	if fault := checkContentType(r); fault != nil {
		return faultEnvelope(fault)
	}
	req, fault := decodeRequest(r, h.maxBodyBytes)
	if fault != nil {
		return faultEnvelope(fault)
	}

	ctx := context.WithoutCancel(r.Context())

	if operation == bootstrapOperation {
		if h.origin == originRemote {
			return faultEnvelope(&contract.Fault{
				Code:    contract.CodePermissionDenied,
				Message: "installation bootstrap is local-only",
			})
		}
		// One-time local bootstrap grants its own capability by being local
		// and by the installer lock/state application.Invoke itself enforces
		// (a second bootstrap attempt fails there, not here). No client
		// credential selects or proves this actor; it exists only to satisfy
		// Invoke's non-empty actor requirement for the call that creates the
		// very first principal.
		actor := contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindService}
		return invoke(ctx, h.app, actor, operation, req)
	}

	actor, authFault := h.authenticate(ctx, r)
	if authFault != nil {
		return faultEnvelope(authFault)
	}
	return invoke(ctx, h.app, actor, operation, req)
}

// authenticate resolves the caller's actor for this transport. The local
// socket trusts only the Authorization credential bytes; the remote
// listener trusts only the verified certificate's mapped principal, and a
// bearer credential presented alongside it must name that same principal.
// No header, body field or certificate name other than these two sources
// ever grants authority.
func (h *operationHandler) authenticate(ctx context.Context, r *http.Request) (contract.Actor, *contract.Fault) {
	if h.origin == originLocal {
		actor, err := h.app.Authenticate(ctx, credentialBytes(r))
		if err != nil {
			return contract.Actor{}, faultFromError(err)
		}
		return actor, nil
	}

	fingerprint, ok := certificateFingerprint(r)
	if !ok {
		return contract.Actor{}, &contract.Fault{
			Code:    contract.CodePermissionDenied,
			Message: "remote transport requires a verified client certificate",
		}
	}
	actor, err := h.app.AuthenticateCertificate(ctx, fingerprint)
	if err != nil {
		return contract.Actor{}, faultFromError(err)
	}
	if cred := credentialBytes(r); cred != nil {
		bearerActor, err := h.app.Authenticate(ctx, cred)
		if err != nil {
			return contract.Actor{}, faultFromError(err)
		}
		if bearerActor.PrincipalID != actor.PrincipalID {
			return contract.Actor{}, &contract.Fault{
				Code:    contract.CodePermissionDenied,
				Message: "credential principal does not match the verified certificate principal",
			}
		}
	}
	return actor, nil
}

// invoke calls application.Application.Invoke and reconciles its result and
// error into the one envelope this transport renders.
func invoke(ctx context.Context, app *application.Application, actor contract.Actor, operation string, req contract.Request) contract.Result {
	return resultEnvelope(app.Invoke(ctx, actor, operation, req))
}
