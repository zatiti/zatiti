package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfigFromEnvironment(t *testing.T) {
	t.Parallel()
	cfg, err := defaultConfig(mapEnv(map[string]string{
		envStateDir: "/tmp/zt-state", envSocket: "/tmp/zt.sock", envProfile: "agent",
		envCredentialBackend: "headless", envMasterKeyRef: "file:/tmp/k", envControllerPrincipal: "p1",
	}))
	if err != nil {
		t.Fatalf("defaultConfig: %v", err)
	}
	want := config{StateDir: "/tmp/zt-state", SocketPath: "/tmp/zt.sock", Profile: "agent", CredentialBackend: "headless", MasterKeyRef: "file:/tmp/k", ControllerPrincipal: "p1"}
	if cfg != want {
		t.Fatalf("defaultConfig = %+v, want %+v", cfg, want)
	}
}

func TestDefaultConfigDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := defaultConfig(mapEnv(nil))
	if err != nil {
		t.Fatalf("defaultConfig: %v", err)
	}
	if cfg.Profile != defaultProfile || cfg.StateDir == "" || !strings.HasSuffix(cfg.StateDir, "zatiti") {
		t.Fatalf("defaults = %+v", cfg)
	}
	if err := cfg.finalize(); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if cfg.SocketPath != filepath.Join(cfg.StateDir, socketFileName) {
		t.Fatalf("socket default = %q", cfg.SocketPath)
	}
}

func TestFinalizeRejections(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  config
		want string
	}{
		{"no state dir", config{Profile: "owner"}, "state directory is required"},
		{"bad profile", config{StateDir: "/tmp/x", Profile: "../owner"}, "profile name"},
		{"empty profile", config{StateDir: "/tmp/x"}, "profile name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := tc.cfg
			err := cfg.finalize()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("finalize = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestFinalizeRefusesLongSocketPath pins the early, named refusal of a
// socket path the OS could not bind: the resolved path counts, after the
// default and environment substitution, so a long state directory with the
// default socket is refused too.
func TestFinalizeRefusesLongSocketPath(t *testing.T) {
	t.Parallel()
	long := "/tmp/" + strings.Repeat("d", 100)
	cases := []struct {
		name string
		cfg  config
	}{
		{"explicit socket", config{StateDir: "/tmp/s", SocketPath: long + "/z.sock", Profile: "owner"}},
		{"default socket under a long state dir", config{StateDir: long, Profile: "owner"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := tc.cfg
			err := cfg.finalize()
			if err == nil {
				t.Fatal("finalize accepted an unbindable socket path")
			}
			for _, want := range []string{"socket path", "bytes", "103", "--socket", "--state-dir"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal %q does not name %q", err, want)
				}
			}
		})
	}
	short := config{StateDir: "/tmp/s", Profile: "owner"}
	if err := short.finalize(); err != nil {
		t.Fatalf("a short default socket path was refused: %v", err)
	}
}

func TestValidateServe(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  config
		want string
	}{
		{"no master key", config{}, "master key"},
		{"negative tick", config{MasterKeyRef: "file:/k", TickInterval: -1}, "tick interval"},
		{"partial tls", config{MasterKeyRef: "file:/k", RemoteAddress: "127.0.0.1:0"}, "together"},
		{"ok", config{MasterKeyRef: "file:/k"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.validateServe()
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("validateServe = %v, want nil", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("validateServe = %v, want %q", err, tc.want)
			}
		})
	}
}
