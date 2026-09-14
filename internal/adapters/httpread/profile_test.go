package httpread

import (
	"encoding/json"
	"testing"
)

func TestLoadProfile_Valid(t *testing.T) {
	raw := defaultProfileJSON(t, "https://example.test", "application/json")
	p, err := loadProfile(raw)
	if err != nil {
		t.Fatalf("loadProfile: %v", err)
	}
	if !p.allowsOrigin("https://example.test") {
		t.Error("expected the configured origin to be allowed")
	}
	if p.allowsOrigin("https://other.test") {
		t.Error("expected an unconfigured origin to be refused")
	}
	if !p.allowsMediaType("application/json") {
		t.Error("expected the configured media type to be allowed")
	}
	if p.MaxBytes != 1<<20 {
		t.Errorf("MaxBytes = %d", p.MaxBytes)
	}
}

func TestLoadProfile_RejectsTamperedCapabilityDigest(t *testing.T) {
	raw := defaultProfileJSON(t, "https://example.test", "application/json")
	var w wireHTTPReadProfile
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	w.MaxBytes = w.MaxBytes + 1 // change the profile without recomputing the digest
	tampered, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := loadProfile(tampered); err == nil {
		t.Fatal("expected an error for a profile whose capability_evidence no longer binds its bytes")
	}
}

func TestLoadProfile_RejectsNonZeroMaxRedirects(t *testing.T) {
	raw := defaultProfileJSON(t, "https://example.test", "application/json")
	var w wireHTTPReadProfile
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	w.MaxRedirects = 1
	tampered, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := loadProfile(tampered); err == nil {
		t.Fatal("expected an error for a profile whose max_redirects is not the frozen const 0")
	}
}

func TestLoadProfile_RejectsUnknownField(t *testing.T) {
	raw := []byte(`{"schema":"zatiti.httpread/v1","allowed_origins":["https://example.test"],"max_bytes":1024,"timeout_seconds":30,"max_redirects":0,"allowed_media_types":["text/plain"],"capability_evidence":{"artifact":{"id":"11111111-1111-4111-8111-111111111111","digest":"0000000000000000000000000000000000000000000000000000000000000000000000000"},"adapter_version":"v","source_revision":"v","protocol_revision":"v","profile_digest":"","qualified_at":"2026-01-01T00:00:00Z","capabilities":[],"limitations":[]},"unexpected":true}`)
	if _, err := loadProfile(raw); err == nil {
		t.Fatal("expected an error for an unknown field")
	}
}

func TestLoadProfile_RejectsEmptyAllowedOrigins(t *testing.T) {
	raw := buildProfileJSON(t, []string{}, []string{"text/plain"}, 1024)
	if _, err := loadProfile(raw); err == nil {
		t.Fatal("expected an error for an empty allowed_origins list")
	}
}
