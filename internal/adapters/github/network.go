package github

import (
	"errors"
	"net"

	"github.com/zatiti/zatiti/internal/contract"
)

// classifyNetworkError distinguishes a transport failure that occurred
// before any bytes reached GitHub (DNS resolution or dial failure --
// request_sent "no", disposition not_sent, an authoritative fact) from one
// that occurred after the request may already have been written or after a
// response may already have been generated (request_sent "unknown",
// disposition unknown -- the physical effect of a mutation cannot be ruled
// out and must not be silently treated as failed).
func classifyNetworkError(err error) (requestSent, disposition, confirmation string) {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "no", contract.DispositionNotSent, "authoritative_nonexecution"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return "no", contract.DispositionNotSent, "authoritative_nonexecution"
	}
	return "unknown", contract.DispositionUnknown, "unknown"
}
