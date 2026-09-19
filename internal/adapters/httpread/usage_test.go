package httpread

import (
	"context"
	"encoding/json"
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
			return jsonResponse(200, "application/json", `{"ok":true}`), nil
		}},
		{"non-success status", func(*http.Request) (*http.Response, error) {
			return jsonResponse(404, "application/json", `{"error":"not found"}`), nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAdapter(t, roundTripFunc(tc.fn), newFakeBlobStore(), fixedLookup("93.184.216.34"), defaultProfileJSON(t, "https://example.test", "application/json"))
			d := testDispatch(t, readAction("https://example.test/data", "application/json"))
			obs, err := a.Invoke(context.Background(), d)
			if err != nil {
				t.Fatalf("Invoke: %v", err)
			}
			u := requireAccountingUsage(t, obs.Usage)
			if u != (wireUsage{Currency: "USD"}) {
				t.Fatalf("usage = %+v, want the exact no-charge zero", u)
			}
			// The evidence still carries the full ProviderUsage.
			var ev wireHTTPReadEvidence
			if err := json.Unmarshal(obs.Evidence, &ev); err != nil {
				t.Fatalf("evidence: %v", err)
			}
			if ev.Usage.Billing != "no_charge" || ev.Usage.Accounting != u {
				t.Fatalf("evidence usage = %+v", ev.Usage)
			}
		})
	}
}
