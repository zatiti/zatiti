package httpread

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestValidateDialIP(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		wantErr bool
	}{
		{"loopback v4", "127.0.0.1", true},
		{"loopback v6", "::1", true},
		{"private 10/8", "10.0.0.5", true},
		{"private 172.16/12", "172.16.5.5", true},
		{"private 192.168/16", "192.168.1.1", true},
		{"private v6 ULA", "fc00::1", true},
		{"link-local v4", "169.254.1.1", true},
		{"cloud metadata", "169.254.169.254", true},
		{"link-local v6", "fe80::1", true},
		{"unspecified v4", "0.0.0.0", true},
		{"unspecified v6", "::", true},
		{"link-local multicast v4", "224.0.0.1", true},
		{"link-local multicast v6", "ff02::1", true},
		{"global multicast v4", "239.1.2.3", true},
		{"global multicast v6", "ff0e::1", true},
		{"public v4", "93.184.216.34", false},
		{"public dns v4", "8.8.8.8", false},
		{"public v6", "2001:4860:4860::8888", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("net.ParseIP(%q) failed", tc.ip)
			}
			err := validateDialIP(ip)
			if tc.wantErr && err == nil {
				t.Errorf("validateDialIP(%s) = nil, want error", tc.ip)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateDialIP(%s) = %v, want nil", tc.ip, err)
			}
			if tc.wantErr && err != nil {
				_ = mustFault(t, err)
			}
		})
	}
}

func TestResolveValidatedIPs_FiltersDisallowedCandidates(t *testing.T) {
	lookup := fixedLookup("10.0.0.5", "93.184.216.34", "169.254.169.254")
	got, err := resolveValidatedIPs(context.Background(), lookup, "example.test")
	if err != nil {
		t.Fatalf("resolveValidatedIPs: %v", err)
	}
	if len(got) != 1 || got[0].String() != "93.184.216.34" {
		t.Fatalf("resolveValidatedIPs = %v, want only the public candidate", got)
	}
}

func TestResolveValidatedIPs_AllDisallowedYieldsEmptyNoError(t *testing.T) {
	lookup := fixedLookup("127.0.0.1", "10.0.0.1")
	got, err := resolveValidatedIPs(context.Background(), lookup, "example.test")
	if err != nil {
		t.Fatalf("resolveValidatedIPs: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("resolveValidatedIPs = %v, want empty", got)
	}
}

func TestResolveValidatedIPs_LiteralPublicIPAllowed(t *testing.T) {
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		t.Fatal("lookup should not be called for a literal IP host")
		return nil, nil
	}
	got, err := resolveValidatedIPs(context.Background(), lookup, "93.184.216.34")
	if err != nil {
		t.Fatalf("resolveValidatedIPs: %v", err)
	}
	if len(got) != 1 || got[0].String() != "93.184.216.34" {
		t.Fatalf("resolveValidatedIPs = %v", got)
	}
}

func TestResolveValidatedIPs_LiteralPrivateIPBlocked(t *testing.T) {
	lookup := func(context.Context, string) ([]net.IPAddr, error) {
		t.Fatal("lookup should not be called for a literal IP host")
		return nil, nil
	}
	got, err := resolveValidatedIPs(context.Background(), lookup, "127.0.0.1")
	if err != nil {
		t.Fatalf("resolveValidatedIPs returned an error for a resolved-but-refused literal address: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("resolveValidatedIPs = %v, want empty", got)
	}
}

func TestResolveValidatedIPs_LookupFailurePropagates(t *testing.T) {
	wantErr := failingLookup("nowhere.test", true)
	_, err := resolveValidatedIPs(context.Background(), wantErr, "nowhere.test")
	if err == nil {
		t.Fatal("expected a lookup error to propagate")
	}
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		t.Fatalf("expected a *net.DNSError, got %T: %v", err, err)
	}
}

// TestPinnedDialContext_ConnectsToThePinnedAddress proves the adapter's
// DialContext connects to the exact address call() validated and pinned,
// never re-resolving the hostname itself -- the mechanism that closes the
// DNS-rebinding window between validation and connection.
func TestPinnedDialContext_ConnectsToThePinnedAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	accepted := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
			close(accepted)
		}
	}()

	ctx := withPinnedDial(context.Background(), ln.Addr().String())
	conn, err := pinnedDialContext(ctx, "tcp", "some-other-host.example:1")
	if err != nil {
		t.Fatalf("pinnedDialContext: %v", err)
	}
	defer func() { _ = conn.Close() }()

	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("listener never accepted a connection to the pinned address")
	}
}

func TestPinnedDialContext_RejectsWithoutAPin(t *testing.T) {
	_, err := pinnedDialContext(context.Background(), "tcp", "example.test:443")
	if err == nil {
		t.Fatal("expected an error when the request context carries no pinned dial address")
	}
	_ = mustFault(t, err)
}
