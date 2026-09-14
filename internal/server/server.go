package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/zatiti/zatiti/internal/application"
)

// operationPattern is the single route this transport exposes: a versioned
// POST-only operation endpoint. There is no scheduler, database or
// alternate restore/admin surface behind any other path.
const operationPattern = "POST /v1/operations/{operation_id}"

// readHeaderTimeout bounds how long either listener waits for a client to
// finish sending request headers. It defends both transports against a slow
// client tying up a connection indefinitely; it never bounds the handler's
// own dispatch, which the server otherwise never cancels on its own.
const readHeaderTimeout = 10 * time.Second

// Server owns the controller's HTTP/JSON transports: the mandatory private
// Unix-domain socket and, when RemoteAddress is configured, an authenticated
// TLS listener for explicit remote desktop access. Construct with New,
// start accepting with Serve and stop with Close.
type Server struct {
	cfg Config
	app *application.Application

	localListener  net.Listener
	remoteListener net.Listener
	localServer    *http.Server
	remoteServer   *http.Server

	shutdownOnce sync.Once
}

// New validates cfg, binds the local socket (and, if configured, the remote
// TLS listener) and returns a Server ready for Serve. A bind failure closes
// whatever this call already opened.
func New(cfg Config, app *application.Application) (*Server, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if app == nil {
		return nil, fmt.Errorf("server: application is required")
	}

	localListener, err := listenUnix(cfg.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}

	s := &Server{
		cfg:           cfg,
		app:           app,
		localListener: localListener,
		localServer: &http.Server{
			Handler:           newMux(&operationHandler{app: app, maxBodyBytes: cfg.MaxBodyBytes, origin: originLocal}),
			ReadHeaderTimeout: readHeaderTimeout,
		},
	}

	if cfg.RemoteAddress != "" {
		tlsCfg := cfg.TLSConfig.Clone()
		remoteListener, err := tls.Listen("tcp", cfg.RemoteAddress, tlsCfg)
		if err != nil {
			_ = localListener.Close()
			return nil, fmt.Errorf("server: binding remote listener: %w", err)
		}
		s.remoteListener = remoteListener
		s.remoteServer = &http.Server{
			Handler:           newMux(&operationHandler{app: app, maxBodyBytes: cfg.MaxBodyBytes, origin: originRemote}),
			ReadHeaderTimeout: readHeaderTimeout,
		}
	}

	return s, nil
}

// newMux registers the single operation route behind handler.
func newMux(handler http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(operationPattern, handler)
	return mux
}

// Serve accepts connections on every bound listener until ctx is cancelled
// or a listener fails. Either way it shuts down gracefully and waits for
// in-flight requests to finish before returning: a request already
// dispatched to application.Application runs to completion regardless of
// why Serve is stopping.
func (s *Server) Serve(ctx context.Context) error {
	errCh := make(chan error, 2)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.localServer.Serve(s.localListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("local listener: %w", err)
		}
	}()

	if s.remoteServer != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.remoteServer.Serve(s.remoteListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("remote listener: %w", err)
			}
		}()
	}

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errCh:
	}

	_ = s.Close(context.Background())
	wg.Wait()

	select {
	case err := <-errCh:
		if serveErr == nil {
			serveErr = err
		}
	default:
	}
	if serveErr != nil {
		return serveErr
	}
	return ctx.Err()
}

// Close gracefully shuts down every listener, waiting for in-flight requests
// to finish (bounded by ctx) before returning. It is idempotent: a second
// call, whether from a caller or from Serve's own shutdown on the way out,
// is a no-op. Close also closes the raw listeners directly: Shutdown only
// releases listeners it learned about through Serve, so a Server that was
// never served (for example, a caller that only needed New's bind-time
// validation) would otherwise leak its socket file descriptor.
func (s *Server) Close(ctx context.Context) error {
	var err error
	s.shutdownOnce.Do(func() {
		if s.remoteServer != nil {
			if e := s.remoteServer.Shutdown(ctx); e != nil {
				err = e
			}
			_ = s.remoteListener.Close()
		}
		if e := s.localServer.Shutdown(ctx); e != nil && err == nil {
			err = e
		}
		_ = s.localListener.Close()
		_ = os.Remove(s.cfg.SocketPath)
	})
	return err
}
