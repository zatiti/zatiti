package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// validateInstalledMacServeConfig checks the fixed default-state selectors
// before openInstallation can create or read any state. An explicitly chosen
// nondefault state remains the existing development/headless configuration.
func validateInstalledMacServeConfig(cfg config) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("resolving installed Mac state: %w", err)
	}
	state := filepath.Join(base, "zatiti")
	if cfg.StateDir != state {
		return nil
	}
	if cfg.SocketPath != filepath.Join(state, socketFileName) {
		return errors.New("installed Mac controller requires the default private socket")
	}
	if cfg.CredentialBackend != "keychain" || cfg.MasterKeyRef != "secret:master" {
		return errors.New("installed Mac controller requires Keychain custody and the fixed master-key selector")
	}
	return nil
}
