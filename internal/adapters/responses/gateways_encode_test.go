package responses

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestOpenRouterEncodeRefusesUnencodableRouting proves a price ceiling that
// is not a JSON number fails the encode instead of sending "provider":null,
// which would drop the pinned route, the fallback lock and the ceiling.
func TestOpenRouterEncodeRefusesUnencodableRouting(t *testing.T) {
	routing := func(input string) json.RawMessage {
		return json.RawMessage(`{"only":["DeepInfra"],"allow_fallbacks":false,"require_parameters":true,` +
			`"price_ceiling":{"currency":"USD","input_per_million":"` + input + `","output_per_million":"1.25"}}`)
	}
	req := func(r json.RawMessage) protocolRequest {
		return protocolRequest{
			Profile:         protocolProfile{Endpoint: "https://openrouter.ai/api/v1/responses", Model: "m", Provider: "openrouter", SessionMode: "stateless", Routing: r},
			MaxOutputTokens: 16, Context: &contextDocument{},
		}
	}
	call, err := (gatewayProtocol{provider: "openrouter"}).encode(req(routing("0.2")), "")
	if err != nil {
		t.Fatalf("valid routing refused: %v", err)
	}
	if !strings.Contains(string(call.Body), `"max_price":{"completion":1.25,"prompt":0.2}`) {
		t.Fatalf("valid routing lost its ceiling: %s", call.Body)
	}
	for _, bad := range []string{"+0.2", "0x10", "1_000"} {
		if call, err := (gatewayProtocol{provider: "openrouter"}).encode(req(routing(bad)), ""); err == nil {
			t.Errorf("price %q encoded as %s, want an error", bad, call.Body)
		}
	}
}
