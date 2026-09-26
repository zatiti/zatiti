package mcpclient

import (
	"net/http"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestEndpointCredentialRoutesRefusedAtConstruction(t *testing.T) {
	for _, tc := range []struct{ name, endpoint, code string }{
		{"credential route", "https://example.invalid/mcp/" + testToken, contract.CodeCapabilityUnsupported},
		{"api credential route", "https://example.invalid/api/mcp/" + testToken, contract.CodeCapabilityUnsupported},
		{"escaped route", "https://example.invalid/api/%6dcp/%6dcp_test_bearer_token_secret_value", contract.CodeCapabilityUnsupported},
		{"escaped slash", "https://example.invalid/api%2fmcp%2f" + testToken, contract.CodeCapabilityUnsupported},
		{"nested escaped route", "https://example.invalid/api/%256dcp/" + testToken, contract.CodeCapabilityUnsupported},
		{"credential before dot traversal", "https://example.invalid/mcp/" + testToken + "/..", contract.CodeCapabilityUnsupported},
		{"credential before nested traversal", "https://example.invalid/api/mcp/./" + testToken + "/../..", contract.CodeCapabilityUnsupported},
		{"dot normalized route", "https://example.invalid/api/other/../mcp/" + testToken, contract.CodeCapabilityUnsupported},
		{"userinfo", "https://user:" + testToken + "@example.invalid/api/mcp", contract.CodeCapabilityUnsupported},
		{"username only", "https://" + testToken + "@example.invalid/api/mcp", contract.CodeCapabilityUnsupported},
		{"api key query", "https://example.invalid/api/mcp?api_key=" + testToken, contract.CodeCapabilityUnsupported},
		{"escaped query key", "https://example.invalid/api/mcp?%61ccess_token=" + testToken, contract.CodeCapabilityUnsupported},
		{"camel query key", "https://example.invalid/api/mcp?apiKey=" + testToken, contract.CodeCapabilityUnsupported},
		{"signed query", "https://example.invalid/api/mcp?X-Amz-Signature=" + testToken, contract.CodeCapabilityUnsupported},
		{"fragment", "https://example.invalid/api/mcp#" + testToken, contract.CodeInvalidInput},
		{"no host", "https:///api/mcp", contract.CodeInvalidInput},
		{"empty host with port", "https://:443/api/mcp", contract.CodeInvalidInput},
		{"invalid port", "https://example.invalid:99999/api/mcp", contract.CodeInvalidInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := buildProfileJSON(t, tc.endpoint, false, []string{"echo"}, []string{"public"}, "bearer")
			_, err := New(contract.AdapterDependencies{HTTP: &http.Client{}, Clock: newFakeClock(), Blobs: newFakeBlobStore()}, profile)
			if err == nil {
				t.Fatal("credential-bearing or invalid endpoint was accepted")
			}
			if f := mustFault(t, err); f.Code != tc.code {
				t.Errorf("fault=%s want=%s", f.Code, tc.code)
			}
			if strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), tc.endpoint) {
				t.Error("constructor fault disclosed endpoint material")
			}
		})
	}
}

func TestOrdinaryOpaqueEndpointsRemainSupported(t *testing.T) {
	for _, endpoint := range []string{
		"https://example.invalid", "https://example.invalid/mcp", "https://example.invalid/api/mcp", "https://example.invalid/api/mcp/", "https://example.invalid/opaque/service/path", "https://example.invalid/v2/service?region=west&version=2", "https://example.invalid:8443/api/mcp", "https://[::1]:8443/api/mcp",
	} {
		t.Run(endpoint, func(t *testing.T) {
			_, err := New(contract.AdapterDependencies{HTTP: &http.Client{}, Clock: newFakeClock(), Blobs: newFakeBlobStore()}, buildProfileJSON(t, endpoint, false, []string{"echo"}, []string{"public"}, "none"))
			if err != nil {
				t.Fatalf("ordinary endpoint rejected: %v", err)
			}
		})
	}
}
