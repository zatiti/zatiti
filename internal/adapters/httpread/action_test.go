package httpread

import (
	"encoding/json"
	"testing"
)

func TestDecodeAction_Valid(t *testing.T) {
	raw, err := json.Marshal(readAction("https://example.test/path?q=1", "application/json"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	act, err := decodeAction(raw)
	if err != nil {
		t.Fatalf("decodeAction: %v", err)
	}
	if act.origin != "https://example.test" {
		t.Errorf("origin = %q", act.origin)
	}
	if act.parsedURL.Path != "/path" {
		t.Errorf("path = %q", act.parsedURL.Path)
	}
}

func TestDecodeAction_OriginExcludesPathAndQuery(t *testing.T) {
	raw, _ := json.Marshal(readAction("https://example.test:8443/a/b?x=1#frag", "text/plain"))
	act, err := decodeAction(raw)
	if err != nil {
		t.Fatalf("decodeAction: %v", err)
	}
	if act.origin != "https://example.test:8443" {
		t.Errorf("origin = %q, want scheme+host+port only", act.origin)
	}
}

func TestDecodeAction_RejectsWrongSchemaConst(t *testing.T) {
	a := readAction("https://example.test/", "text/plain")
	a.Schema = "wrong/v1"
	raw, _ := json.Marshal(a)
	if _, err := decodeAction(raw); err == nil {
		t.Fatal("expected an error for a wrong schema const")
	}
}

func TestDecodeAction_RejectsWrongMethod(t *testing.T) {
	a := readAction("https://example.test/", "text/plain")
	a.Method = "POST"
	raw, _ := json.Marshal(a)
	if _, err := decodeAction(raw); err == nil {
		t.Fatal("expected an error for a non-GET method")
	}
}

func TestDecodeAction_RejectsDisallowedHeaderName(t *testing.T) {
	a := readAction("https://example.test/", "text/plain", wireReadHeader{Name: "Authorization", Value: "Bearer x"})
	raw, _ := json.Marshal(a)
	if _, err := decodeAction(raw); err == nil {
		t.Fatal("expected an error for a header name outside the permitted, secret-free set")
	}
}

func TestDecodeAction_RejectsCRLFInHeaderValue(t *testing.T) {
	a := readAction("https://example.test/", "text/plain", wireReadHeader{Name: "Accept", Value: "text/plain\r\nX-Injected: 1"})
	raw, _ := json.Marshal(a)
	if _, err := decodeAction(raw); err == nil {
		t.Fatal("expected an error for a header value containing CRLF")
	}
}

func TestDecodeAction_RejectsEmbeddedUserinfo(t *testing.T) {
	a := readAction("https://user:pass@example.test/", "text/plain")
	raw, _ := json.Marshal(a)
	if _, err := decodeAction(raw); err == nil {
		t.Fatal("expected an error for a URL carrying embedded userinfo credentials")
	}
}

func TestDecodeAction_RejectsNonHTTPScheme(t *testing.T) {
	a := readAction("ftp://example.test/file", "text/plain")
	raw, _ := json.Marshal(a)
	if _, err := decodeAction(raw); err == nil {
		t.Fatal("expected an error for a non-http(s) URL scheme")
	}
}

func TestDecodeAction_RejectsUnknownField(t *testing.T) {
	raw := []byte(`{"schema":"zatiti.httpread.action/v1","kind":"read","url":"https://example.test/","method":"GET","headers":[],"expected_media_type":"text/plain","unexpected":true}`)
	if _, err := decodeAction(raw); err == nil {
		t.Fatal("expected an error for an unknown field")
	}
}
