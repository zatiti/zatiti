package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Environment variables that seed the startup configuration. Flags override
// them. None of them ever carries a credential: the credential profile
// selects a protected file, it is never the secret itself.
const (
	envStateDir            = "ZATITI_STATE_DIR"
	envSocket              = "ZATITI_SOCKET"
	envProfile             = "ZATITI_PROFILE"
	envCredentialBackend   = "ZATITI_CREDENTIAL_BACKEND"
	envMasterKeyRef        = "ZATITI_MASTER_KEY_REF"
	envControllerPrincipal = "ZATITI_CONTROLLER_PRINCIPAL"
)

const (
	defaultProfile     = "owner"
	socketFileName     = "zatiti.sock"
	databaseFileName   = "zatiti.db"
	profilesDirName    = "profiles"
	adaptersDirName    = "adapters"
	defaultMaxBodySize = 2 << 20 // artifact.upload.chunk's 2 MiB encoded cap
	defaultTimeout     = 60 * time.Second
	shutdownGrace      = 15 * time.Second
)

// config is the entrypoint's startup configuration. Everything a client or
// the controller needs to find its state, socket and credential lives here;
// nothing in it is model-visible or an operation argument.
type config struct {
	// StateDir is the installation state directory (0700).
	StateDir string
	// SocketPath is the controller's private Unix socket.
	SocketPath string
	// Profile names the client credential profile under StateDir/profiles.
	Profile string

	// Serve-only settings.
	CredentialBackend   string
	MasterKeyRef        string
	ControllerPrincipal string
	TickInterval        time.Duration
	RemoteAddress       string
	TLSCertFile         string
	TLSKeyFile          string
	TLSClientCAFile     string
}

// lookupEnv is the environment accessor run uses; tests substitute a map.
type lookupEnv func(string) (string, bool)

// osEnv reads the process environment.
func osEnv(key string) (string, bool) { return os.LookupEnv(key) }

// defaultConfig seeds the configuration from the environment and platform
// defaults. Flag binding later overwrites individual fields.
func defaultConfig(env lookupEnv) (config, error) {
	cfg := config{Profile: defaultProfile}
	if v, ok := env(envStateDir); ok && v != "" {
		cfg.StateDir = v
	} else {
		base, err := os.UserConfigDir()
		if err != nil {
			return config{}, fmt.Errorf("resolving the default state directory: %w", err)
		}
		cfg.StateDir = filepath.Join(base, "zatiti")
	}
	if v, ok := env(envSocket); ok && v != "" {
		cfg.SocketPath = v
	}
	if v, ok := env(envProfile); ok && v != "" {
		cfg.Profile = v
	}
	if v, ok := env(envCredentialBackend); ok {
		cfg.CredentialBackend = v
	}
	if v, ok := env(envMasterKeyRef); ok {
		cfg.MasterKeyRef = v
	}
	if v, ok := env(envControllerPrincipal); ok {
		cfg.ControllerPrincipal = v
	}
	return cfg, nil
}

// finalize resolves derived paths after flags are parsed and validates the
// shape every command needs. Serve-specific requirements are checked by the
// serve command itself.
func (c *config) finalize() error {
	if c.StateDir == "" {
		return errors.New("state directory is required (--state-dir or " + envStateDir + ")")
	}
	abs, err := filepath.Abs(c.StateDir)
	if err != nil {
		return fmt.Errorf("resolving state directory: %w", err)
	}
	c.StateDir = abs
	if c.SocketPath == "" {
		c.SocketPath = filepath.Join(c.StateDir, socketFileName)
	}
	if !filepath.IsAbs(c.SocketPath) {
		abs, err := filepath.Abs(c.SocketPath)
		if err != nil {
			return fmt.Errorf("resolving socket path: %w", err)
		}
		c.SocketPath = abs
	}
	if err := validateProfileName(c.Profile); err != nil {
		return err
	}
	return nil
}

// validateServe checks what only the controller needs.
func (c *config) validateServe() error {
	if c.MasterKeyRef == "" {
		return errors.New("serve requires a master key reference (--master-key or " + envMasterKeyRef + "): artifacts and custodied secrets are always encrypted at rest")
	}
	if c.TickInterval < 0 {
		return errors.New("tick interval must not be negative")
	}
	remote := c.RemoteAddress != "" || c.TLSCertFile != "" || c.TLSKeyFile != "" || c.TLSClientCAFile != ""
	if remote && (c.RemoteAddress == "" || c.TLSCertFile == "" || c.TLSKeyFile == "" || c.TLSClientCAFile == "") {
		return errors.New("a remote listener requires --remote-address, --tls-cert, --tls-key and --tls-client-ca together")
	}
	return nil
}

var profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// validateProfileName keeps profile names to one path segment so a profile
// can never select a file outside the profile directory.
func validateProfileName(name string) error {
	if !profileNamePattern.MatchString(name) {
		return fmt.Errorf("profile name %q must be 1-64 characters of letters, digits, '.', '_' or '-'", name)
	}
	return nil
}

func (c *config) profilesDir() string { return filepath.Join(c.StateDir, profilesDirName) }
func (c *config) adaptersDir() string { return filepath.Join(c.StateDir, adaptersDirName) }
func (c *config) databasePath() string {
	return filepath.Join(c.StateDir, databaseFileName)
}
