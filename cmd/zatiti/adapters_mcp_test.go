package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

type mcpAssemblyClock struct{}

func (mcpAssemblyClock) Now() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

type mcpAssemblyBlobs struct{}

func (mcpAssemblyBlobs) Stage(context.Context, io.Reader, int64) (string, contract.Digest, int64, error) {
	return "", "", 0, nil
}
func (mcpAssemblyBlobs) Publish(context.Context, string, contract.Digest) error { return nil }
func (mcpAssemblyBlobs) Open(context.Context, contract.Digest, int64, int64) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}
func (mcpAssemblyBlobs) RemoveStaged(context.Context, string) error { return nil }

func mcpAssemblyProfile(t *testing.T) json.RawMessage {
	t.Helper()
	profile := map[string]any{
		"schema": "zatiti.mcp/v1",
		"transport": map[string]any{
			"kind": "streamable_http", "endpoint": "https://mcp.example.test/mcp",
			"allow_private_endpoint": false, "max_redirects": 0,
		},
		"protocol_version":  "2025-11-25",
		"credential_kind":   "none",
		"allowed_tools":     []string{"echo"},
		"tool_call_cost":    map[string]any{"currency": "USD", "micro_units": 0},
		"max_request_bytes": 65536, "max_response_bytes": 65536,
		"timeout_seconds": 30, "classifications": []string{"restricted"},
		"capability_evidence": map[string]any{
			"artifact": map[string]any{
				"id": string(contract.NewID()), "digest": string(contract.Hash([]byte("synthetic MCP qualification"))),
			},
			"adapter_version": "test-1", "source_revision": "synthetic",
			"protocol_revision": "2025-11-25", "profile_digest": "",
			"qualified_at": "2026-01-01T00:00:00Z",
			"capabilities": []string{"call_tool"}, "limitations": []string{},
		},
	}
	unsigned, err := contract.Canonicalize(mcpAssemblyMustJSON(t, profile))
	if err != nil {
		t.Fatal(err)
	}
	profile["capability_evidence"].(map[string]any)["profile_digest"] = string(contract.Hash(unsigned))
	return mcpAssemblyMustJSON(t, profile)
}

func mcpAssemblyMustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestReadMCPAdmissionProfileBoundsAndRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	if got, err := readMCPAdmissionProfile(dir); err != nil || len(got) != 0 {
		t.Fatalf("missing profile = %q, %v; want absent", got, err)
	}
	want := mcpAssemblyProfile(t)
	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readMCPAdmissionProfile(dir)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("read profile bytes differ from the installation snapshot")
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	secretMarker := "synthetic-profile-secret-marker"
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxAdapterProfileBytes)+secretMarker), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readMCPAdmissionProfile(dir); err == nil || strings.Contains(err.Error(), secretMarker) {
		t.Fatalf("oversize profile error = %v; want bounded redacted error", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(outside, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := readMCPAdmissionProfile(dir); err == nil {
		t.Fatal("symlinked MCP profile was accepted")
	}
}

func TestLoadAdaptersUsesSharedMCPProfileSnapshot(t *testing.T) {
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "mcp.json")
	snapshot := mcpAssemblyProfile(t)
	if err := os.WriteFile(profilePath, snapshot, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := readMCPAdmissionProfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a changed profile file after assembly. Adapter construction
	// must consume the captured bytes, never reread this path.
	marker := "synthetic-profile-secret-marker"
	if err := os.WriteFile(profilePath, []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	deps := contract.AdapterDependencies{
		HTTP: &http.Client{}, Clock: mcpAssemblyClock{}, Blobs: mcpAssemblyBlobs{},
	}
	adapters, _, err := loadAdaptersWithMCPProfile(dir, deps, loaded)
	if err != nil {
		t.Fatalf("construct adapter from snapshot: %v", err)
	}
	if got := adapters["mcp"]; got == nil || got.Name() != "mcp" {
		t.Fatalf("MCP adapter = %#v; want registered adapter", got)
	}

	if _, _, err := loadAdaptersWithMCPProfile(dir, deps, nil); err != nil {
		t.Fatalf("unconfigured MCP should remain unavailable without rereading changed file: %v", err)
	}
}

func TestLoadAdaptersRedactsInvalidMCPProfile(t *testing.T) {
	dir := t.TempDir()
	marker := "synthetic-profile-secret-marker"
	deps := contract.AdapterDependencies{HTTP: &http.Client{}, Clock: mcpAssemblyClock{}, Blobs: mcpAssemblyBlobs{}}
	_, _, err := loadAdaptersWithMCPProfile(dir, deps, json.RawMessage(`{"credential":"`+marker+`"}`))
	if err == nil || strings.Contains(err.Error(), marker) {
		t.Fatalf("invalid MCP profile error = %v; want failure without profile contents", err)
	}
}
