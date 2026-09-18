package responses

import (
	"errors"
	"net"

	"github.com/zatiti/zatiti/internal/contract"
)

// classifyNetworkError distinguishes a transport failure that occurred
// before any bytes reached the provider (DNS resolution or dial failure --
// request_sent "no", disposition not_sent, an authoritative fact) from one
// that occurred after the request may already have been written or after a
// response may already have been generated (request_sent "unknown",
// disposition unknown). A model step that may have run may have been
// billed, so everything that is not provably pre-dial stays unknown and is
// never downgraded to failed.
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
