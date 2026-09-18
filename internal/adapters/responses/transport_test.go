package responses

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// These tests run the adapter over the real net/http transport against a
// controlled TLS server on the loopback interface, so physical call counts
// are observed at the server rather than at a fake RoundTripper.

// controlledServer counts requests, announces each arrival, and blocks
// handlers on release, which the test closes at cleanup. Nothing here
// sleeps or depends on how fast the machine is.
type controlledServer struct {
	*httptest.Server
	hits    atomic.Int64
	arrived chan struct{}
	release chan struct{}
}

func newControlledServer(t *testing.T, handler func(s *controlledServer, w http.ResponseWriter, r *http.Request)) *controlledServer {
	t.Helper()
	s := &controlledServer{arrived: make(chan struct{}, 16), release: make(chan struct{})}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		s.arrived <- struct{}{}
		handler(s, w, r)
	}))
	t.Cleanup(func() {
		close(s.release)
		s.Close()
	})
	return s
}

func (s *controlledServer) endpoint() string { return s.URL + "/v1/steps" }

// abandonOnArrival returns a context that is cancelled only once the server
// has the request in hand: the caller gives up strictly after the bytes
// arrived, which is the lost-acknowledgement case, deterministically.
func abandonOnArrival(t *testing.T, s *controlledServer) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		select {
		case <-s.arrived:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx
}

func worstCaseOf(ev wireResponsesEvidence) int64 {
	return ev.StagedOutputs[0].Size*2 + 3500
}

func TestServerObservesExactlyOneRequestOnSuccess(t *testing.T) {
	t.Parallel()
	s := newControlledServer(t, func(_ *controlledServer, w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/steps" || r.Header.Get(testCredentialHeader) != testToken {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(completedBody))
	})
	h := newHarness(t, nil, withServer(s.Client(), s.endpoint()))
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if obs.Disposition != contract.DispositionSucceeded || s.hits.Load() != 1 {
		t.Fatalf("disposition %q, server hits %d", obs.Disposition, s.hits.Load())
	}
	decodeEvidence(t, obs)
}

// Z06: transient errors, timeouts and resets each cost exactly one physical
// request, and none of them is reported as a failure the caller could
// safely retry.
func TestNoHiddenRetriesAgainstAFaultingServer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		handler func(s *controlledServer, w http.ResponseWriter, r *http.Request)
		abandon bool
		code    string
		status  int64
	}{
		{"transient server error", func(_ *controlledServer, w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}, false, "http_503", 503},
		{"caller gives up after the server accepted the request", func(s *controlledServer, _ http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-s.release:
			}
		}, true, "transport_error", 0},
		{"connection reset after the request was read", func(_ *controlledServer, w http.ResponseWriter, _ *http.Request) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}, false, "transport_error", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newControlledServer(t, tc.handler)
			h := newHarness(t, nil, withServer(s.Client(), s.endpoint()))
			ctx := context.Background()
			if tc.abandon {
				ctx = abandonOnArrival(t, s)
			}
			obs, err := h.adapter.Invoke(ctx, h.dispatch)
			if err != nil {
				t.Fatalf("Invoke returned an error for an attempted call: %v", err)
			}
			if got := s.hits.Load(); got != 1 {
				t.Fatalf("server saw %d requests, want exactly 1", got)
			}
			if obs.Disposition != contract.DispositionUnknown || obs.ConfirmedAt != nil {
				t.Fatalf("disposition = %q confirmed %v; a lost response must stay unknown", obs.Disposition, obs.ConfirmedAt)
			}
			ev := decodeEvidence(t, obs)
			if ev.PhysicalCall.ErrorCode != tc.code || ev.PhysicalCall.HTTPStatus != tc.status || ev.PhysicalCall.Confirmation != "unknown" {
				t.Fatalf("physical call = %+v", ev.PhysicalCall)
			}
			u := ev.Output.Usage
			if u.Billing != "unknown" || u.Accounting.Unknown != worstCaseOf(ev) || u.Accounting.Unknown == 0 {
				t.Fatalf("reservation not kept: %+v", u)
			}
			assertNoSecret(t, h, obs, nil)
		})
	}
}

func TestRedirectIsNeverFollowed(t *testing.T) {
	t.Parallel()
	target := newControlledServer(t, func(_ *controlledServer, w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(completedBody))
	})
	origin := newControlledServer(t, func(_ *controlledServer, w http.ResponseWriter, _ *http.Request) {
		// 303 is the redirect net/http would follow even for a request
		// whose body cannot be replayed, so only CheckRedirect stops it.
		w.Header().Set("Location", target.endpoint())
		w.WriteHeader(http.StatusSeeOther)
	})
	h := newHarness(t, nil, withServer(origin.Client(), origin.endpoint()))
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if origin.hits.Load() != 1 || target.hits.Load() != 0 {
		t.Fatalf("origin hits %d, redirect target hits %d; the context must not reach an undeclared destination", origin.hits.Load(), target.hits.Load())
	}
	ev := decodeEvidence(t, obs)
	if obs.Disposition != contract.DispositionFailed || ev.PhysicalCall.ErrorCode != "redirect_not_permitted" || ev.PhysicalCall.HTTPStatus != 303 {
		t.Fatalf("disposition %q, physical call %+v", obs.Disposition, ev.PhysicalCall)
	}
	if ev.PhysicalCall.ResolvedDestination != origin.endpoint() {
		t.Fatalf("resolved_destination = %q", ev.PhysicalCall.ResolvedDestination)
	}
}

func TestRefusedConnectionIsNotSentAndFree(t *testing.T) {
	t.Parallel()
	// Reserve a loopback port, then close it so the dial is refused.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	endpoint := "https://" + l.Addr().String() + "/v1/steps"
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	h := newHarness(t, nil, withServer(&http.Client{Transport: &http.Transport{}}, endpoint))
	obs, err := h.adapter.Invoke(context.Background(), h.dispatch)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	ev := decodeEvidence(t, obs)
	if obs.Disposition != contract.DispositionNotSent || ev.PhysicalCall.RequestSent != "no" || ev.PhysicalCall.Confirmation != "authoritative_nonexecution" {
		t.Fatalf("disposition %q, physical call %+v", obs.Disposition, ev.PhysicalCall)
	}
	if u := ev.Output.Usage; u.Billing != "no_charge" || u.Accounting != (wireUsage{Currency: "USD"}) {
		t.Fatalf("usage = %+v", u)
	}
}
