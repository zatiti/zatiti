package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// AuthHeader carries the credential bytes on every operation request. The
// CredentialSource supplies the complete header value (for a bearer
// credential, "Bearer <token>"); the value never appears in the JSON body,
// CLI arguments or MCP tool input.
const AuthHeader = "Authorization"

// CommandGetOperation resolves an unresolvable unknown acknowledgement of a
// keyed submission: the client posts {"submission_key": "<key>"} to this
// operation and treats the returned envelope as the command's original
// disposition. A not_found lookup proves the command never committed. The
// found envelope is the complete original result retained by evidence,
// including a refused command's fault.
const CommandGetOperation = "command.get"

// operationsPathPrefix is the frozen HTTP route prefix for operation calls.
const operationsPathPrefix = "/v1/operations/"

// localBaseURL is the fixed request base for Unix-socket operation calls.
// The transport's DialContext ignores the authority and dials Config.SocketPath.
const localBaseURL = "http://unix"

// maxResponseBytes bounds every response body read. It is a defensive
// ceiling, far above any legitimate page envelope (500 events, 200 list
// entries) and far below unbounded; a larger body is treated as a protocol
// violation because the disposition would be untrustworthy.
const maxResponseBytes = 8 << 20

// Retry bounds. Connection establishment is retried before any request byte
// is sent; a keyed submission whose exchange failed after the bytes were
// sent is replayed identically (the controller deduplicates by submission
// key); an unresolvable unknown acknowledgement is resolved once through
// command.get.
const (
	defaultConnectAttempts = 3
	defaultUnknownReplays  = 2
	defaultBackoffStep     = 100 * time.Millisecond
)

// Config selects the controller endpoint and transport behavior for a
// Client. Exactly one of SocketPath (local Unix-socket transport) and
// RemoteURL (remote TLS transport) must be set. TLSConfig is honored only
// with RemoteURL; remote endpoints are always https. Timeout bounds each
// HTTP exchange (connection through body read); zero means only the caller's
// context applies.
type Config struct {
	SocketPath string
	RemoteURL  string
	TLSConfig  *tls.Config
	Timeout    time.Duration
}

// Client is a reusable, concurrency-safe controller client implementing
// contract.Operator. Construct with New.
type Client struct {
	cfg   Config
	base  *url.URL
	http  *http.Client
	creds contract.CredentialSource

	// Retry policy seams, defaulted by New and narrowed by tests.
	connectAttempts int
	unknownReplays  int
	backoff         func(attempt int) time.Duration
}

// New validates cfg and returns a client. creds may be nil only when every
// call will target an operation that accepts an unauthenticated bootstrap
// principal; otherwise the controller refuses with permission_denied.
func New(cfg Config, creds contract.CredentialSource) (*Client, error) {
	base, transport, err := endpoint(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Timeout < 0 {
		return nil, fmt.Errorf("%w: negative timeout", ErrInvalidRequest)
	}
	httpClient := &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
		// Operation endpoints never redirect; following one could leak the
		// credential header to a different host. Redirect responses fail
		// envelope validation below.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &Client{
		cfg:             cfg,
		base:            base,
		http:            httpClient,
		creds:           creds,
		connectAttempts: defaultConnectAttempts,
		unknownReplays:  defaultUnknownReplays,
		backoff: func(attempt int) time.Duration {
			return defaultBackoffStep << (attempt - 1)
		},
	}, nil
}

// endpoint resolves the request base URL and transport for cfg: local
// Unix-socket HTTP, or remote TLS HTTP. The two endpoint families are
// mutually exclusive and remote is https-only.
func endpoint(cfg Config) (*url.URL, http.RoundTripper, error) {
	switch {
	case cfg.SocketPath != "" && cfg.RemoteURL != "":
		return nil, nil, fmt.Errorf("%w: SocketPath and RemoteURL are mutually exclusive", ErrInvalidRequest)
	case cfg.SocketPath == "" && cfg.RemoteURL == "":
		return nil, nil, fmt.Errorf("%w: exactly one of SocketPath or RemoteURL is required", ErrInvalidRequest)
	case cfg.SocketPath != "":
		if cfg.TLSConfig != nil {
			return nil, nil, fmt.Errorf("%w: TLSConfig applies only to RemoteURL; local sockets are plaintext by design", ErrInvalidRequest)
		}
		base, err := url.Parse(localBaseURL)
		if err != nil {
			return nil, nil, err
		}
		dialer := &net.Dialer{}
		transport := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", cfg.SocketPath)
			},
		}
		return base, transport, nil
	default:
		u, err := url.Parse(cfg.RemoteURL)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: RemoteURL is not a valid URL: %v", ErrInvalidRequest, err)
		}
		if u.Scheme != "https" {
			return nil, nil, fmt.Errorf("%w: remote endpoints require https, got %q", ErrInvalidRequest, u.Scheme)
		}
		if u.Host == "" {
			return nil, nil, fmt.Errorf("%w: RemoteURL is missing a host", ErrInvalidRequest)
		}
		tlsCfg := cfg.TLSConfig
		if tlsCfg == nil {
			tlsCfg = &tls.Config{}
		} else {
			tlsCfg = tlsCfg.Clone()
		}
		// Verification is never disabled by this client; an explicit
		// InsecureSkipVerify in the caller's config is refused outright.
		if tlsCfg.InsecureSkipVerify {
			return nil, nil, fmt.Errorf("%w: InsecureSkipVerify is not permitted", ErrInvalidRequest)
		}
		if tlsCfg.ServerName == "" && u.Hostname() != "" {
			tlsCfg.ServerName = u.Hostname()
		}
		// A fresh transport never routes controller traffic through a proxy.
		transport := &http.Transport{TLSClientConfig: tlsCfg}
		return u, transport, nil
	}
}

// Call performs one controller operation call (contract.Operator). It
// returns the authoritative result envelope and, for a failed envelope, the
// envelope's fault as the error value. Error classes before an envelope:
//
//   - ErrInvalidRequest: the request failed local validation and was not sent,
//   - ErrControllerUnavailable: connection establishment failed before any
//     request byte was sent (bounded retries exhausted),
//   - ErrTLSCertificate: the remote certificate failed verification (no retry),
//   - *UnknownAckError (errors.Is ErrUnknownOutcome): bytes were sent but no
//     authoritative disposition was obtained after bounded identical-key
//     replay and command.get lookup,
//   - *CursorExpiredError: the operation's cursor expired (snapshot_required
//     parsed for the caller),
//   - *contract.Fault: any other failed result envelope.
func (c *Client) Call(ctx context.Context, operation string, req contract.Request) (contract.Result, error) {
	body, err := marshalRequest(operation, req)
	if err != nil {
		return contract.Result{}, err
	}
	cred, err := c.credential(ctx)
	if err != nil {
		return contract.Result{}, fmt.Errorf("reading credential: %w", err)
	}
	defer zeroBuffer(cred)

	return c.dispatch(ctx, operation, req.SubmissionKey, body, cred)
}

// dispatch runs the transport with the frozen retry policy: bounded
// connection-establishment attempts, then — only for keyed submissions —
// identical replay of a lost exchange and command.get resolution.
func (c *Client) dispatch(ctx context.Context, operation, key string, body, cred []byte) (contract.Result, error) {
	var lastErr error
	for attempt := 1; attempt <= c.connectAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return contract.Result{}, fmt.Errorf("%w: %v", ErrControllerUnavailable, err)
		}
		result, err := c.attempt(ctx, operation, body, cred)
		if err == nil {
			return result, nil
		}
		kind, fault := failureOf(err)
		switch kind {
		case failureEnvelope:
			return result, cursorExpired(fault)
		case failureTLS:
			return contract.Result{}, fmt.Errorf("%w: %v", ErrTLSCertificate, err)
		case failureNotSent:
			// Connection establishment failed before any request byte was
			// written: retrying is safe for every request shape.
			lastErr = err
			if attempt == c.connectAttempts {
				return contract.Result{}, fmt.Errorf("%w: %v", ErrControllerUnavailable, err)
			}
			if err := sleepBackoff(ctx, c.backoff(attempt)); err != nil {
				return contract.Result{}, fmt.Errorf("%w: %v", ErrControllerUnavailable, err)
			}
		default:
			return c.resolveUnknown(ctx, operation, key, body, cred, err)
		}
	}
	return contract.Result{}, fmt.Errorf("%w: %v", ErrControllerUnavailable, lastErr)
}

// resolveUnknown handles an exchange that failed after the request bytes
// were sent: the disposition is unknown. Keyed submissions replay the
// identical bytes under the same key (the controller deduplicates), then
// resolve through command.get; queries are returned to the caller unknown.
// A dead caller context ends resolution immediately: cancelling the local
// wait is not a cancellation of an accepted command, and no network work
// runs on a cancelled context.
func (c *Client) resolveUnknown(ctx context.Context, operation, key string, body, cred []byte, cause error) (contract.Result, error) {
	if key == "" {
		return contract.Result{}, &UnknownAckError{Operation: operation, Err: cause}
	}
	unknown := func(err error) (contract.Result, error) {
		return contract.Result{}, &UnknownAckError{Operation: operation, SubmissionKey: key, Err: err}
	}
	// Identical submission with the same key: the controller binds the key to
	// principal, operation and input hash, so identical bytes return the
	// original disposition instead of repeating the mutation.
	for replay := 0; replay < c.unknownReplays; replay++ {
		if err := ctx.Err(); err != nil {
			return unknown(err)
		}
		result, err := c.attempt(ctx, operation, body, cred)
		if err == nil {
			return result, nil
		}
		kind, fault := failureOf(err)
		switch kind {
		case failureEnvelope:
			return result, cursorExpired(fault)
		case failureTLS:
			return contract.Result{}, fmt.Errorf("%w: %v", ErrTLSCertificate, err)
		case failureNotSent:
			// Replaying into a dead connection is not progress; go straight
			// to command lookup.
			return c.resolveByLookup(ctx, operation, key, body, cred)
		default:
			// The replay itself failed unknown; keep replaying. The last
			// attempt error is superseded by the lookup's own outcome below.
		}
	}
	return c.resolveByLookup(ctx, operation, key, body, cred)
}

// resolveByLookup looks the command up by its original submission key. A
// not_found lookup proves the command never committed and licenses one final
// identical submission; any other found envelope is the command's original
// retained disposition.
func (c *Client) resolveByLookup(ctx context.Context, operation, key string, body, cred []byte) (contract.Result, error) {
	unknown := func(err error) (contract.Result, error) {
		return contract.Result{}, &UnknownAckError{Operation: operation, SubmissionKey: key, Err: err}
	}
	result, found, err := c.lookupCommand(ctx, key)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return unknown(cerr)
		}
		return unknown(err)
	}
	if !found {
		if err := ctx.Err(); err != nil {
			return unknown(err)
		}
		result, err := c.attempt(ctx, operation, body, cred)
		if err == nil {
			return result, nil
		}
		kind, fault := failureOf(err)
		switch kind {
		case failureEnvelope:
			return result, cursorExpired(fault)
		case failureTLS:
			return contract.Result{}, fmt.Errorf("%w: %v", ErrTLSCertificate, err)
		default:
			return unknown(err)
		}
	}
	if result.Status == contract.StatusFailed {
		return result, result.Error
	}
	return result, nil
}

// lookupCommand posts {"submission_key": key} to CommandGetOperation and
// reports whether the command was found. It is a query: no submission key
// and no replay of its own, so resolution terminates. A well-formed failed
// envelope other than not_found is the command's retained original
// disposition, not a lookup failure.
func (c *Client) lookupCommand(ctx context.Context, key string) (contract.Result, bool, error) {
	input, err := json.Marshal(map[string]string{"submission_key": key})
	if err != nil {
		return contract.Result{}, false, err
	}
	body, err := marshalRequest(CommandGetOperation, contract.Request{
		Schema: contract.SchemaRequest,
		Input:  input,
	})
	if err != nil {
		return contract.Result{}, false, err
	}
	cred, err := c.credential(ctx)
	if err != nil {
		return contract.Result{}, false, fmt.Errorf("reading credential: %w", err)
	}
	defer zeroBuffer(cred)

	result, err := c.attempt(ctx, CommandGetOperation, body, cred)
	if err == nil {
		return result, true, nil
	}
	var fault *contract.Fault
	if errors.As(err, &fault) {
		if fault.Code == contract.CodeNotFound {
			return contract.Result{}, false, nil
		}
		return result, true, nil
	}
	return contract.Result{}, false, err
}

// attempt performs exactly one HTTP exchange. A failed envelope with a
// well-formed fault is returned as (result, fault); any other failure is a
// transport or protocol error for failureOf to classify.
func (c *Client) attempt(ctx context.Context, operation string, body, cred []byte) (contract.Result, error) {
	target := *c.base
	target.Path = operationsPathPrefix + operation
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return contract.Result{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if len(cred) > 0 {
		req.Header.Set(AuthHeader, string(cred))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return contract.Result{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return contract.Result{}, fmt.Errorf("reading response body: %w", err)
	}
	if len(raw) > maxResponseBytes {
		return contract.Result{}, fmt.Errorf("response body exceeds %d bytes", maxResponseBytes)
	}
	var result contract.Result
	if err := contract.DecodeStrict(raw, &result); err != nil {
		return contract.Result{}, fmt.Errorf("malformed result envelope: %w", err)
	}
	if err := validateResult(resp.StatusCode, &result); err != nil {
		return contract.Result{}, err
	}
	if result.Status == contract.StatusFailed {
		return result, result.Error
	}
	return result, nil
}

// credential fetches the header bytes for one call. The source must hand out
// a private buffer per call; the client zeroes it when the exchange ends.
func (c *Client) credential(ctx context.Context) ([]byte, error) {
	if c.creds == nil {
		return nil, nil
	}
	cred, err := c.creds.Credential(ctx)
	if err != nil {
		return nil, err
	}
	if len(cred) == 0 {
		return nil, nil
	}
	return cred, nil
}

// failureKind classifies one failed HTTP attempt.
type failureKind int

const (
	// failureEnvelope: a well-formed failed envelope arrived; its fault is
	// the authoritative disposition.
	failureEnvelope failureKind = iota
	// failureTLS: certificate verification failed; permanent, no retry.
	failureTLS
	// failureNotSent: connection establishment failed before any request
	// byte was written.
	failureNotSent
	// failureUnknown: bytes may have been written but no authoritative
	// envelope was obtained.
	failureUnknown
)

// failureOf maps one attempt error to its kind. Well-formed faults always
// win: the envelope is the disposition authority even on 4xx/5xx statuses.
func failureOf(err error) (failureKind, *contract.Fault) {
	var fault *contract.Fault
	if errors.As(err, &fault) {
		return failureEnvelope, fault
	}
	if isTLSVerification(err) {
		return failureTLS, nil
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return failureNotSent, nil
	}
	return failureUnknown, nil
}

// isTLSVerification reports whether err is a certificate-chain, hostname or
// record-layer failure. Those are permanent configuration or trust problems:
// the client surfaces them without retry instead of classifying them as
// transient unavailability.
func isTLSVerification(err error) bool {
	var (
		verify    *tls.CertificateVerificationError
		authority x509.UnknownAuthorityError
		invalid   x509.CertificateInvalidError
		hostname  x509.HostnameError
		record    *tls.RecordHeaderError
	)
	switch {
	case errors.As(err, &verify):
		return true
	case errors.As(err, &authority):
		return true
	case errors.As(err, &invalid):
		return true
	case errors.As(err, &hostname):
		return true
	case errors.As(err, &record):
		return true
	}
	return false
}

// sleepBackoff waits for d unless the context ends first.
func sleepBackoff(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// zeroBuffer scrubs a credential buffer the client owns after use. It is
// best-effort hygiene: bytes already copied into HTTP headers cannot be
// recalled, so the client also avoids ever logging credential material.
func zeroBuffer(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
