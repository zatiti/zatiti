package client

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// fakeController is a scripted in-process controller implementing the frozen
// HTTP seams the client proves against: POST /v1/operations/{id}, the
// request/result envelopes, and submission-key deduplication (identical
// keyed bytes return the original disposition; changed input refuses with
// submission_conflict). command.get is built in: it resolves the store by
// submission key, or not_found when absent.
type fakeController struct {
	t *testing.T

	mu       sync.Mutex
	requests []capturedRequest
	scripts  map[string][]responder
	store    map[string]storedCommand
	events   []string
	servers  []*http.Server

	// Both endpoint families may serve the same scripted controller at once,
	// so cross-transport equivalence tests see identical state.
	unixPath string
	unixUp   bool
	unixSrv  *http.Server
	tlsBase  string
	tlsUp    bool
	tlsRoots *x509.CertPool
	closed   bool

	// conns counts accepted transport connections (handshake counting).
	conns atomic.Int64

	// unscripted records requests that arrived with no scripted behavior;
	// the default is to fail the test so scripts stay explicit.
	unscripted atomic.Int64
}

type capturedRequest struct {
	Operation string
	Body      []byte
	Header    http.Header
}

type storedCommand struct {
	body     []byte
	envelope contract.Result
}

// responder produces the scripted outcome for one request.
type responder func(fc *fakeController, op string, body []byte) scriptedResponse

type scriptedResponse struct {
	drop      bool
	hang      time.Duration
	beforeRsp func() // runs immediately before the response is written

	// envelope wins over raw; nil envelope with empty raw answers 500.
	envelope  *contract.Result
	raw       string
	rawStatus int
}

func newFakeController(t *testing.T) *fakeController {
	fc := &fakeController{
		t:       t,
		scripts: map[string][]responder{},
		store:   map[string]storedCommand{},
	}
	t.Cleanup(func() { fc.Close() })
	return fc
}

// shortTempSocket returns a socket path short enough for the OS sun_path
// bound (104 bytes on darwin): t.TempDir embeds long test names, so the
// directory is minted with a short prefix and removed at cleanup.
func shortTempSocket(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "zatiti-sock-")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "c.sock")
}

// ListenUnix binds the fake on a private Unix-domain socket and returns the
// socket path. It is safe to call before or after client construction so
// connect-retry tests control exactly when the controller becomes reachable.
func (fc *fakeController) ListenUnix() string {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.unixUp {
		return fc.unixPath
	}
	path := shortTempSocket(fc.t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		fc.t.Fatalf("fake controller listen: %v", err)
	}
	fc.unixPath, fc.unixUp = path, true
	fc.unixSrv = fc.start(ln)
	return path
}

// ListenUnixAt binds the fake at an exact socket path the test chose, so a
// client can be constructed against a path that does not exist yet.
func (fc *fakeController) ListenUnixAt(path string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.unixUp {
		if fc.unixPath != path {
			fc.t.Fatalf("fake controller already listening at %s", fc.unixPath)
		}
		return
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		fc.t.Fatalf("fake controller listen at %s: %v", path, err)
	}
	fc.unixPath, fc.unixUp = path, true
	fc.unixSrv = fc.start(ln)
}

// ListenTLS binds the fake on a loopback TCP port over TLS and returns the
// request base URL and the client root pool trusting its certificate.
func (fc *fakeController) ListenTLS() (base string, roots *x509.CertPool) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.tlsUp {
		return fc.tlsBase, fc.tlsRoots
	}
	cert, err := testCertificate("127.0.0.1")
	if err != nil {
		fc.t.Fatalf("fake controller certificate: %v", err)
	}
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fc.t.Fatalf("fake controller tcp listen: %v", err)
	}
	counted := &countListener{Listener: tcp, count: &fc.conns}
	ln := tls.NewListener(counted, &tls.Config{
		Certificates: []tls.Certificate{cert.server},
		MinVersion:   tls.VersionTLS12,
	})
	fc.tlsRoots = cert.roots
	fc.tlsBase, fc.tlsUp = "https://"+ln.Addr().String(), true
	fc.start(ln)
	return fc.tlsBase, fc.tlsRoots
}

// tlsClientConfig returns a client config trusting the fake's certificate.
func (fc *fakeController) tlsClientConfig() *tls.Config {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return &tls.Config{RootCAs: fc.tlsRoots}
}

// handshakeCount reports how many transport connections were accepted.
func (fc *fakeController) handshakeCount() int64 {
	return fc.conns.Load()
}

// start runs one HTTP server on ln; both endpoint families may run at once.
// The caller holds fc.mu.
func (fc *fakeController) start(ln net.Listener) *http.Server {
	srv := &http.Server{
		Handler:  fc,
		ErrorLog: log.New(io.Discard, "", 0), // handshake noise is expected in tests
	}
	fc.servers = append(fc.servers, srv)
	go func() { _ = srv.Serve(ln) }()
	return srv
}

// StopUnix shuts down only the Unix-socket endpoint, simulating a controller
// stop or restart while the TLS endpoint (if any) keeps serving the same
// state; ListenUnix binds a fresh socket afterwards.
func (fc *fakeController) StopUnix() {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.unixSrv != nil {
		_ = fc.unixSrv.Close()
		fc.unixSrv = nil
	}
	fc.unixUp = false
	fc.unixPath = ""
}

func (fc *fakeController) Close() {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if fc.closed {
		return
	}
	fc.closed = true
	for _, srv := range fc.servers {
		_ = srv.Close()
	}
}

// script queues behaviors for one operation; each request pops the next.
func (fc *fakeController) script(operation string, rs ...responder) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.scripts[operation] = append(fc.scripts[operation], rs...)
}

// commit records one durable business mutation and stores the command
// disposition under its submission key together with the exact request
// bytes, so identical replays deduplicate as the frozen controller requires.
func (fc *fakeController) commit(op, key string, body []byte, envelope contract.Result) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.events = append(fc.events, op+"#"+key)
	fc.store[key] = storedCommand{body: append([]byte(nil), body...), envelope: envelope}
}

// requestCount reports how many requests reached the controller, filtered by
// operation when op is non-empty.
func (fc *fakeController) requestCount(op string) int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	n := 0
	for _, r := range fc.requests {
		if op == "" || r.Operation == op {
			n++
		}
	}
	return n
}

// allRequests returns a copy of the captured request log.
func (fc *fakeController) allRequests() []capturedRequest {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return append([]capturedRequest(nil), fc.requests...)
}

// eventCount reports how many durable business mutations were recorded.
func (fc *fakeController) eventCount() int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return len(fc.events)
}

// storeSize reports how many commands the durable store retains.
func (fc *fakeController) storeSize() int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return len(fc.store)
}

// ServeHTTP implements the frozen transport seam for every scripted call.
func (fc *fakeController) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := readBounded(r.Body, maxResponseBytes)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	op := ""
	if len(r.URL.Path) > len(operationsPathPrefix) {
		op = r.URL.Path[len(operationsPathPrefix):]
	}

	fc.mu.Lock()
	fc.requests = append(fc.requests, capturedRequest{
		Operation: op,
		Body:      append([]byte(nil), body...),
		Header:    r.Header.Clone(),
	})
	script := fc.scripts[op]
	var rsp responder
	if len(script) > 0 {
		rsp = script[0]
		fc.scripts[op] = script[1:]
	}
	var req contract.Request
	decodeOK := contract.DecodeStrict(body, &req) == nil
	// Frozen controller semantics: an identical keyed submission returns the
	// original disposition before dispatch; changed input under the same key
	// refuses with submission_conflict.
	if decodeOK && req.SubmissionKey != "" {
		if stored, seen := fc.store[req.SubmissionKey]; seen {
			fc.mu.Unlock()
			if equalBytes(stored.body, body) {
				env := stored.envelope
				writeEnvelope(w, &env)
			} else {
				writeEnvelope(w, failedResult("cmd-conflict", &contract.Fault{
					Code:    contract.CodeSubmissionConflict,
					Message: "submission key reused with different input",
				}))
			}
			return
		}
	}
	if rsp == nil && decodeOK && op == CommandGetOperation && req.SubmissionKey == "" {
		// Built-in command lookup by submission key.
		var lookup struct {
			SubmissionKey string `json:"submission_key"`
		}
		_ = json.Unmarshal(req.Input, &lookup)
		stored, ok := fc.store[lookup.SubmissionKey]
		fc.mu.Unlock()
		if ok {
			env := stored.envelope
			writeEnvelope(w, &env)
		} else {
			writeEnvelope(w, failedResult("cmd-lookup", &contract.Fault{
				Code:    contract.CodeNotFound,
				Message: "no command with that submission key",
			}))
		}
		return
	}
	fc.mu.Unlock()

	if rsp == nil {
		fc.unscripted.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	out := rsp(fc, op, body)
	if out.beforeRsp != nil {
		out.beforeRsp()
	}
	if out.hang > 0 {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(out.hang):
		}
	}
	if out.drop {
		dropConnection(w)
		return
	}
	if out.envelope != nil {
		writeEnvelope(w, out.envelope)
		return
	}
	if out.raw != "" || out.rawStatus != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(out.rawStatus)
		_, _ = w.Write([]byte(out.raw))
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
}

// dropConnection closes the wire without any response, simulating a lost
// acknowledgement after the request bytes were received.
func dropConnection(w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	_ = conn.Close()
}

// writeEnvelope serializes one result envelope with its frozen HTTP status.
func writeEnvelope(w http.ResponseWriter, envelope *contract.Result) {
	if envelope == nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	status := http.StatusOK
	switch envelope.Status {
	case contract.StatusAccepted:
		status = http.StatusAccepted
	case contract.StatusFailed:
		status = contract.HTTPStatus(envelope.Error)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(mustJSON(*envelope)))
}

// countListener counts accepted transport connections.
type countListener struct {
	net.Listener
	count *atomic.Int64
}

func (l *countListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		l.count.Add(1)
	}
	return c, err
}

// staticCreds is a contract.CredentialSource handing out a fresh buffer per
// call and remembering the buffer the client most recently received, so
// tests can assert per-request fetch and post-use zeroing. It is
// concurrency-safe: Call is safe from many goroutines at once.
type staticCreds struct {
	mu    sync.Mutex
	value string
	last  []byte
	calls atomic.Int64
}

func (s *staticCreds) Credential(context.Context) ([]byte, error) {
	s.calls.Add(1)
	buf := []byte(s.value)
	s.mu.Lock()
	s.last = buf
	s.mu.Unlock()
	return buf, nil
}

// failingCreds simulates an unavailable credential source.
type failingCreds struct {
	err error
}

func (f failingCreds) Credential(context.Context) ([]byte, error) {
	return nil, f.err
}

// --- envelope builders -----------------------------------------------------

const testCommandID = "00000000-0000-4000-8000-000000000001"

func completedResult(cmd, data string) *contract.Result {
	return &contract.Result{
		Schema:    contract.SchemaResult,
		CommandID: contract.ID(cmd),
		Payload: contract.Payload{
			Status: contract.StatusCompleted,
			Data:   json.RawMessage(data),
		},
	}
}

func acceptedResult(cmd, data string) *contract.Result {
	return &contract.Result{
		Schema:    contract.SchemaResult,
		CommandID: contract.ID(cmd),
		Payload: contract.Payload{
			Status: contract.StatusAccepted,
			Data:   json.RawMessage(data),
		},
	}
}

func failedResult(cmd string, fault *contract.Fault) *contract.Result {
	return &contract.Result{
		Schema:    contract.SchemaResult,
		CommandID: contract.ID(cmd),
		Payload: contract.Payload{
			Status: contract.StatusFailed,
			Data:   json.RawMessage(`null`),
			Error:  fault,
		},
	}
}

// commitAndRespond records the durable mutation and answers with envelope —
// the acknowledged-commit seam the continuation journeys drive.
func commitAndRespond(key string, envelope *contract.Result) responder {
	return func(fc *fakeController, op string, body []byte) scriptedResponse {
		return scriptedResponse{
			beforeRsp: func() { fc.commit(op, key, body, *envelope) },
			envelope:  envelope,
		}
	}
}

// faultEnvelope scripts one failed result envelope carrying fault, without
// committing anything: the named-refusal boundaries of the fault matrix.
func faultEnvelope(commandID string, fault *contract.Fault) responder {
	return func(*fakeController, string, []byte) scriptedResponse {
		return scriptedResponse{envelope: failedResult(commandID, fault)}
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func readBounded(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit+1))
}

// opRequest builds a request with the given submission key and input.
func opRequest(key, input string) contract.Request {
	req := contract.Request{Schema: contract.SchemaRequest, Input: json.RawMessage(input)}
	if key != "" {
		req.SubmissionKey = key
	}
	return req
}

// newLocalClient builds a client on the fake's Unix socket with test-tight
// retry policy (no sleeps).
func newLocalClient(t *testing.T, fc *fakeController, creds contract.CredentialSource) *Client {
	c, err := New(Config{SocketPath: fc.ListenUnix()}, creds)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	c.connectAttempts = 3
	c.unknownReplays = 2
	c.backoff = func(int) time.Duration { return 0 }
	return c
}

// newRemoteClient builds a client on the fake's TLS endpoint.
func newRemoteClient(t *testing.T, fc *fakeController, creds contract.CredentialSource) *Client {
	base, _ := fc.ListenTLS()
	c, err := New(Config{RemoteURL: base, TLSConfig: fc.tlsClientConfig()}, creds)
	if err != nil {
		t.Fatalf("new remote client: %v", err)
	}
	c.connectAttempts = 3
	c.unknownReplays = 2
	c.backoff = func(int) time.Duration { return 0 }
	return c
}

// --- test certificates ------------------------------------------------------

type testCert struct {
	server tls.Certificate
	roots  *x509.CertPool
}

// testCertificate returns a self-signed CA and a leaf certificate valid for
// the given IP host, so tests exercise real chain and hostname verification.
func testCertificate(host string) (testCert, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return testCert{}, err
	}
	caTmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "zatiti test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, &caTmpl, &caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return testCert{}, err
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		return testCert{}, err
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return testCert{}, err
	}
	leafTmpl := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "zatiti test controller"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IPAddresses:  []net.IP{net.ParseIP(host)},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, &leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		return testCert{}, err
	}
	leafCert, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return testCert{}, err
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	return testCert{
		server: tls.Certificate{
			Certificate: [][]byte{leafDER, caDER},
			PrivateKey:  leafKey,
			Leaf:        leafCert,
		},
		roots: roots,
	}, nil
}
