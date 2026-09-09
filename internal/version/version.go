// internal/version/version.go
//
// Build identity. The stamp is injected at build time:
//
//	go build -ldflags "-X zatiti/internal/version.stamp=v0.1.0-<gitsha>"
//
// An empty stamp means an unstamped build, which `zatiti version`
// reports as an error (exit 1) rather than fabricating a value.
// Version scheme: semver for releases, "<tag>-<gitsha>" for
// intermediate builds. The version participates in the audit journal's
// payload for bootstrap — a controller restart on a different binary
// is visible in the journal via the generation row's creator version.

package version

import "fmt"

// stamp is set by -ldflags at build time. Do not assign at runtime.
var stamp string

// ErrUnstamped: the binary carries no build stamp.
type UnstampedError struct{}

func (UnstampedError) Error() string {
	return "version: binary was built without a version stamp (use -ldflags -X zatiti/internal/version.stamp=...)"
}

// String returns the stamp.
func String() (string, error) {
	if stamp == "" {
		return "", UnstampedError{}
	}
	return stamp, nil
}

// MustString is for tests and logs only — paths that tolerate fallback.
func MustString() string {
	s, err := String()
	if err != nil {
		return "dev-unstamped"
	}
	return s
}

// Attested returns the version line for `zatiti version` output,
// including the RFC revision the scaffold tracks. Both facts together
// are the conformance attestation's identity.
func Attested() (string, error) {
	s, err := String()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("zatiti %s (rfc-conformance: %s)", s, rfcRevision), nil
}

// rfcRevision: which revision of the governing document this scaffold
// was built against. Updated only by the conformance pass itself —
// it is a claim, and claims are tested, not typed in casually.
const rfcRevision = "scaffold-2025-11"
