// internal/version/version_test.go
package version

import "testing"

func TestUnstampedFails(t *testing.T) {
	// Test binaries are unstamped unless -ldflags reaches them; the
	// default assertion is that empty stamp errors.
	if _, err := String(); err == nil {
		t.Fatal("unstamped build returned a version")
	}
}

func TestMustStringFallsBack(t *testing.T) {
	if MustString() == "" {
		t.Fatal("MustString returned empty")
	}
}
