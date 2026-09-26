package packaging

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMacArchitectureSelector(t *testing.T) {
	script := filepath.Join("templates", "macos", "select_arch.sh")
	for _, tc := range []struct {
		name, status, value, machine, stderr, want string
		ok                                         bool
	}{
		{"Apple Silicon native", "0", "1", "arm64", "", "arm64", true},
		{"Apple Silicon Rosetta shell", "0", "1", "x86_64", "", "arm64", true},
		{"Intel feature disabled", "0", "0", "x86_64", "", "amd64", true},
		{"Intel unknown OID", "1", "", "x86_64", "sysctl: unknown oid 'hw.optional.arm64'", "amd64", true},
		{"unknown OID on arm64", "1", "", "arm64", "sysctl: unknown oid 'hw.optional.arm64'", "", false},
		{"failed probe under translated shell", "1", "", "x86_64", "sysctl: permission denied", "", false},
		{"inconsistent feature and uname", "0", "0", "arm64", "", "", false},
		{"invalid feature value", "0", "2", "x86_64", "", "", false},
		{"invalid machine", "0", "0", "i386", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command("sh", "-c", `. "$1"; zatiti_select_mac_arch "$2" "$3" "$4" "$5"`, "sh", script, tc.status, tc.value, tc.machine, tc.stderr)
			out, err := cmd.Output()
			if (err == nil) != tc.ok || strings.TrimSpace(string(out)) != tc.want {
				t.Fatalf("output %q, error %v; want %q, success %v", out, err, tc.want, tc.ok)
			}
		})
	}
}
