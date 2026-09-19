package github

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// requireAccountingUsage asserts Observation.Usage is exactly the
// $defs/Usage document the controller validates: the six accounting
// fields and nothing else. A nested ProviderUsage (accounting + billing)
// is rejected by the controller and replaced with synthesized unknown
// billing, which is the defect this test pins.
func requireAccountingUsage(t *testing.T, usage json.RawMessage) wireUsage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(usage, &fields); err != nil {
		t.Fatalf("Observation.Usage is not a JSON object: %v: %s", err, usage)
	}
	for _, name := range []string{"currency", "spent", "reserved", "estimated", "unknown", "advisory"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("Observation.Usage lacks %s: %s", name, usage)
		}
	}
	if len(fields) != 6 {
		t.Fatalf("Observation.Usage has %d fields, want exactly the six of $defs/Usage: %s", len(fields), usage)
	}
	var u wireUsage
	if err := contract.DecodeStrict(usage, &u); err != nil {
		t.Fatalf("Observation.Usage does not strict-decode as Usage: %v", err)
	}
	return u
}

func TestObservationUsageIsTheAccountingDocument(t *testing.T) {
	cases := []struct {
		name string
		fn   func(*http.Request) (*http.Response, error)
	}{
		{"success", func(*http.Request) (*http.Response, error) {
			return jsonResponse(200, `{"full_name":"acme/widgets","default_branch":"main"}`), nil
		}},
		{"provider failure", func(*http.Request) (*http.Response, error) {
			return jsonResponse(500, `{"message":"boom"}`), nil
		}},
		{"not sent", func(*http.Request) (*http.Response, error) {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAdapter(t, roundTripFunc(tc.fn), newFakeBlobStore(), defaultProfileJSON(t))
			d := testDispatch(t, wireReadRepository{Schema: "zatiti.github.action/v1", Repository: testRepo(), Kind: kindReadRepository, Resource: "metadata"})
			obs, err := a.Invoke(context.Background(), d)
			if err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			u := requireAccountingUsage(t, obs.Usage)
			if u != (wireUsage{Currency: "USD"}) {
				t.Fatalf("usage = %+v, want the exact no-charge zero", u)
			}
			// The evidence still carries the full ProviderUsage.
			var ev wireGitHubEvidence
			if err := json.Unmarshal(obs.Evidence, &ev); err != nil {
				t.Fatalf("evidence: %v", err)
			}
			if ev.Usage.Billing != "no_charge" || ev.Usage.Accounting != u {
				t.Fatalf("evidence usage = %+v", ev.Usage)
			}
		})
	}
}
