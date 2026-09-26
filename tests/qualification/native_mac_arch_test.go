package qualification_test

import (
	"errors"
	"strings"
	"testing"
)

func TestNativeMacCaseArchEmitsOnlyPhysicalHostArchitecture(t *testing.T) {
	cases := []struct {
		name, goos, process, hardware, wantArch, wantReason string
		detectErr                                           error
	}{
		{"Intel", "darwin", "amd64", "amd64", "amd64", "", nil},
		{"Apple Silicon", "darwin", "arm64", "arm64", "arm64", "", nil},
		{"Rosetta", "darwin", "amd64", "arm64", "", "native arm64 process", nil},
		{"Linux", "linux", "amd64", "", "", "native macOS host", nil},
		{"unknown hardware", "darwin", "amd64", "", "", "unavailable", errors.New("probe failed")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			gotArch, reason := nativeMacCaseArch(tc.goos, tc.process, func() (string, error) {
				called = true
				return tc.hardware, tc.detectErr
			})
			if gotArch != tc.wantArch || (tc.wantReason == "" && reason != "") || (tc.wantReason != "" && !strings.Contains(reason, tc.wantReason)) || (tc.goos == "linux" && called) {
				t.Fatalf("arch=%q reason=%q detector_called=%t", gotArch, reason, called)
			}
		})
	}
}
