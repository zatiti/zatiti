package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstalledMacServeUsesFixedDefaultSelectors(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("installed Mac selector test")
	}
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(base, "zatiti")
	good := config{StateDir: state, SocketPath: filepath.Join(state, socketFileName), CredentialBackend: "keychain", MasterKeyRef: "secret:master"}
	if err := validateInstalledMacServeConfig(good); err != nil {
		t.Fatalf("fixed launcher: %v", err)
	}
	cases := []struct {
		name   string
		edit   func(*config)
		phrase string
	}{
		{"custom socket", func(c *config) { c.SocketPath = filepath.Join(state, "other.sock") }, "default private socket"},
		{"implicit backend", func(c *config) { c.CredentialBackend = "" }, "Keychain custody"},
		{"headless backend", func(c *config) { c.CredentialBackend = "headless" }, "Keychain custody"},
		{"file master", func(c *config) { c.MasterKeyRef = "file:/tmp/master" }, "fixed master-key selector"},
		{"wrong secret", func(c *config) { c.MasterKeyRef = "secret:other" }, "fixed master-key selector"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := good
			tc.edit(&cfg)
			if err := validateInstalledMacServeConfig(cfg); err == nil || !strings.Contains(err.Error(), tc.phrase) {
				t.Fatalf("got %v", err)
			}
		})
	}
	dev := good
	dev.StateDir = filepath.Join(t.TempDir(), "custom")
	dev.SocketPath = filepath.Join(dev.StateDir, socketFileName)
	dev.CredentialBackend = "headless"
	dev.MasterKeyRef = "file:/tmp/master"
	if err := validateInstalledMacServeConfig(dev); err != nil {
		t.Fatalf("explicit development state: %v", err)
	}
}
