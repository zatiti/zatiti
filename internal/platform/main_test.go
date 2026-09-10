package platform

import (
	"context"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// ZATITI_TEST_CHILD selects a child-process helper mode. The helpers re-exec
// the test binary so competing-controller and child-environment behavior is
// proven across a real process boundary.
const childEnvVar = "ZATITI_TEST_CHILD"

func TestMain(m *testing.M) {
	switch os.Getenv(childEnvVar) {
	case "env-scan":
		os.Exit(childEnvScan())
	case "duplicate-controller":
		os.Exit(childDuplicateController())
	}
	os.Exit(m.Run())
}

// childEnvScan exits 0 only when no environment entry carries the canary
// secret bytes passed as hex (the secret itself must never be in the
// environment, so the raw value cannot be handed over as one).
func childEnvScan() int {
	raw, err := hex.DecodeString(os.Getenv("ZATITI_TEST_CANARY_HEX"))
	if err != nil || len(raw) == 0 {
		return 2
	}
	for _, kv := range os.Environ() {
		if strings.Contains(kv, string(raw)) {
			return 3 // secret leaked into the child environment
		}
	}
	return 0
}

// childDuplicateController exits 44 when Acquire is refused for an
// installation already served by another controller, 0 if it wrongly acquired
// the lock, and 2 when the platform cannot even be opened.
func childDuplicateController() int {
	p, err := Open(Config{
		StateDir:          os.Getenv("ZATITI_TEST_STATE"),
		CredentialBackend: backendHeadless,
		MasterKeyRef:      os.Getenv("ZATITI_TEST_KEY"),
	})
	if err != nil {
		return 2
	}
	defer func() { _ = p.Close() }()
	own, err := p.Acquire(context.Background())
	if err != nil {
		return 44 // refused: expected under an active first controller
	}
	_ = own.Close()
	return 0 // wrongly admitted: the lock failed to exclude
}
