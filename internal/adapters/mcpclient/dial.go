package mcpclient

import (
	"context"
	"net"
)

// ipLookupFunc resolves host to its IP addresses. It is the seam Invoke/
// Reconcile use so tests can supply deterministic addresses without
// depending on a real DNS resolver; New wires it to
// net.DefaultResolver.LookupIPAddr in production.
type ipLookupFunc func(ctx context.Context, host string) ([]net.IPAddr, error)

// validateDialIP reports an error unless ip is a publicly routable unicast
// address, or allowPrivate is true (the profile's explicit
// allow_private_endpoint opt-in). Loopback, RFC1918/RFC4193 private,
// link-local (which covers the 169.254.169.254 cloud metadata address),
// unspecified and multicast addresses are refused unless that explicit
// installation-authorized binding is set.
func validateDialIP(ip net.IP, allowPrivate bool) error {
	if allowPrivate {
		return nil
	}
	switch {
	case ip.IsLoopback():
		return permissionDenied("mcpclient: dial address %s is a loopback address", ip)
	case ip.IsPrivate():
		return permissionDenied("mcpclient: dial address %s is a private address", ip)
	case ip.IsLinkLocalUnicast():
		return permissionDenied("mcpclient: dial address %s is a link-local address", ip)
	case ip.IsLinkLocalMulticast():
		return permissionDenied("mcpclient: dial address %s is a link-local multicast address", ip)
	case ip.IsUnspecified():
		return permissionDenied("mcpclient: dial address %s is unspecified", ip)
	case ip.IsMulticast():
		return permissionDenied("mcpclient: dial address %s is a multicast address", ip)
	default:
		return nil
	}
}

// resolveValidatedIPs resolves host and returns every candidate address
// that passes validateDialIP, in resolver order. A non-nil error means
// resolution itself failed. A nil error with an empty result means
// resolution succeeded but every candidate was refused.
func resolveValidatedIPs(ctx context.Context, lookup ipLookupFunc, host string, allowPrivate bool) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if validateDialIP(ip, allowPrivate) != nil {
			return nil, nil // resolved (it's already a literal address) but refused, not a lookup failure
		}
		return []net.IP{ip}, nil
	}
	addrs, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	validated := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		if validateDialIP(a.IP, allowPrivate) == nil {
			validated = append(validated, a.IP)
		}
	}
	return validated, nil
}

// dialPinKey is the context key call() uses to hand the single validated,
// already-chosen dial address to the adapter's Transport.DialContext.
type dialPinKey struct{}

// withPinnedDial attaches the exact "ip:port" call() validated to ctx.
// pinnedDialContext connects to precisely this address and performs no
// hostname resolution of its own, so no window exists between validation
// and connection for DNS rebinding to exploit.
func withPinnedDial(ctx context.Context, pinned string) context.Context {
	return context.WithValue(ctx, dialPinKey{}, pinned)
}

// pinnedDialContext is the only DialContext the adapter's http.Client uses.
// It never resolves a hostname itself: the caller has already resolved and
// validated the destination and attached the exact address to dial via
// withPinnedDial before issuing the request.
func pinnedDialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	pinned, ok := ctx.Value(dialPinKey{}).(string)
	if !ok || pinned == "" {
		return nil, internalError("mcpclient: request context carries no pinned dial address")
	}
	dialer := &net.Dialer{}
	return dialer.DialContext(ctx, network, pinned)
}

// defaultPortForScheme returns the implied port for a URL with no explicit
// port. The profile schema requires an https:// endpoint (HTTPSURL), so
// this is always 443 in practice; it exists for defensive completeness.
func defaultPortForScheme(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}
