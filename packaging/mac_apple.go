package packaging

// Apple code signing, notarization and stapling for the Mac release
// producer. Every step reads its identity or notary profile from explicit
// configuration and fails closed with CodePrerequisiteMissing, naming the
// missing setting, when that configuration is absent. Nothing here invents
// an identity, skips a step or reports success it did not observe: each
// signing step is followed by the system tool's own verification.

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Environment variables that configure Apple signing. They hold identity
// names and a keychain profile name, never key material: the certificates
// and notary credentials live in the keychain the caller prepared.
const (
	EnvAppleAppIdentity       = "ZATITI_APPLE_APP_IDENTITY"
	EnvAppleInstallerIdentity = "ZATITI_APPLE_INSTALLER_IDENTITY"
	EnvAppleNotaryProfile     = "ZATITI_APPLE_NOTARY_PROFILE"
	EnvAppleKeychain          = "ZATITI_APPLE_KEYCHAIN"
)

// Apple signing steps.
const (
	AppleStepCodesign    = "codesign"
	AppleStepProductsign = "productsign"
	AppleStepNotarize    = "notarize"
	AppleStepStaple      = "staple"
)

// AppleSigningConfig names the signing identities and notary profile. An
// empty field means that step is not configured.
type AppleSigningConfig struct {
	AppIdentity       string `json:"app_identity,omitempty"`
	InstallerIdentity string `json:"installer_identity,omitempty"`
	NotaryProfile     string `json:"notary_profile,omitempty"`
	Keychain          string `json:"keychain,omitempty"`
}

// AppleSigningFromEnv reads the configuration from getenv.
func AppleSigningFromEnv(getenv func(string) string) AppleSigningConfig {
	return AppleSigningConfig{
		AppIdentity:       strings.TrimSpace(getenv(EnvAppleAppIdentity)),
		InstallerIdentity: strings.TrimSpace(getenv(EnvAppleInstallerIdentity)),
		NotaryProfile:     strings.TrimSpace(getenv(EnvAppleNotaryProfile)),
		Keychain:          strings.TrimSpace(getenv(EnvAppleKeychain)),
	}
}

// AppleStepStatus reports whether one step is configured.
type AppleStepStatus struct {
	Step       string   `json:"step"`
	Configured bool     `json:"configured"`
	Missing    []string `json:"missing,omitempty"`
}

// Status reports every step's configuration. Stapling needs no
// configuration of its own, but it is only meaningful after notarization,
// so it is reported as configured only when notarization is.
func (c AppleSigningConfig) Status() []AppleStepStatus {
	steps := []string{AppleStepCodesign, AppleStepProductsign, AppleStepNotarize, AppleStepStaple}
	out := make([]AppleStepStatus, 0, len(steps))
	for _, s := range steps {
		missing := c.missing(s)
		out = append(out, AppleStepStatus{Step: s, Configured: len(missing) == 0, Missing: missing})
	}
	return out
}

func (c AppleSigningConfig) missing(step string) []string {
	switch step {
	case AppleStepCodesign:
		if c.AppIdentity == "" {
			return []string{EnvAppleAppIdentity}
		}
	case AppleStepProductsign:
		if c.InstallerIdentity == "" {
			return []string{EnvAppleInstallerIdentity}
		}
	case AppleStepNotarize, AppleStepStaple:
		if c.NotaryProfile == "" {
			return []string{EnvAppleNotaryProfile}
		}
	}
	return nil
}

// require refuses a step whose configuration is absent.
func (c AppleSigningConfig) require(step string) error {
	if m := c.missing(step); len(m) != 0 {
		return errf(CodePrerequisiteMissing, "Apple %s is not configured: set %s", step, strings.Join(m, ", "))
	}
	return nil
}

// AppleToolRunner runs one Apple system tool and returns its combined
// output. Production uses ExecAppleTools; tests substitute a recorder.
type AppleToolRunner interface {
	Run(ctx context.Context, name string, args []string) ([]byte, error)
}

// ExecAppleTools runs the tools from /usr/bin with a fixed environment.
type ExecAppleTools struct{}

func (ExecAppleTools) Run(ctx context.Context, name string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.Bytes(), err
}

func (c AppleSigningConfig) keychainArgs() []string {
	if c.Keychain == "" {
		return nil
	}
	return []string{"--keychain", c.Keychain}
}

func appleRun(ctx context.Context, r AppleToolRunner, what, name string, args ...string) ([]byte, error) {
	out, err := r.Run(ctx, name, args)
	if err != nil {
		return out, errWrap(CodeVerificationFailed, what+" failed: "+lastLine(out), err)
	}
	return out, nil
}

func lastLine(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// AppleCodesign signs each path with the hardened runtime and a secure
// timestamp, innermost first as given, then verifies each signature
// strictly. entitlements is optional and applies to every path.
func AppleCodesign(ctx context.Context, r AppleToolRunner, c AppleSigningConfig, entitlements string, paths ...string) error {
	if err := c.require(AppleStepCodesign); err != nil {
		return err
	}
	if len(paths) == 0 {
		return errf(CodeInvalidInput, "codesign needs at least one path")
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			return errf(CodeInvalidInput, "codesign paths must be absolute")
		}
		args := []string{"--force", "--options", "runtime", "--timestamp", "--sign", c.AppIdentity}
		args = append(args, c.keychainArgs()...)
		if entitlements != "" {
			args = append(args, "--entitlements", entitlements)
		}
		args = append(args, p)
		if _, err := appleRun(ctx, r, "codesign", "/usr/bin/codesign", args...); err != nil {
			return err
		}
		if _, err := appleRun(ctx, r, "codesign verification", "/usr/bin/codesign", "--verify", "--strict", "--deep", "--verbose=2", p); err != nil {
			return err
		}
	}
	return nil
}

// AppleProductsign signs an installer package into out with a secure
// timestamp and verifies the result's signature.
func AppleProductsign(ctx context.Context, r AppleToolRunner, c AppleSigningConfig, in, out string) error {
	if err := c.require(AppleStepProductsign); err != nil {
		return err
	}
	if !filepath.IsAbs(in) || !filepath.IsAbs(out) || in == out {
		return errf(CodeInvalidInput, "productsign needs distinct absolute input and output paths")
	}
	args := []string{"--timestamp", "--sign", c.InstallerIdentity}
	args = append(args, c.keychainArgs()...)
	args = append(args, in, out)
	if _, err := appleRun(ctx, r, "productsign", "/usr/bin/productsign", args...); err != nil {
		return err
	}
	_, err := appleRun(ctx, r, "package signature verification", "/usr/sbin/pkgutil", "--check-signature", out)
	return err
}

// notaryResult is the part of notarytool's JSON output this step reads.
type notaryResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// AppleNotarize submits path to Apple's notary service with the configured
// keychain profile and waits for the verdict. Only an "Accepted" verdict
// succeeds; any other status, or output that cannot be read, fails.
func AppleNotarize(ctx context.Context, r AppleToolRunner, c AppleSigningConfig, path string, timeout time.Duration) (string, error) {
	if err := c.require(AppleStepNotarize); err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) {
		return "", errf(CodeInvalidInput, "notarize path must be absolute")
	}
	if timeout <= 0 {
		timeout = time.Hour
	}
	args := []string{"notarytool", "submit", path, "--keychain-profile", c.NotaryProfile, "--wait", "--timeout", timeout.String(), "--output-format", "json"}
	args = append(args, c.keychainArgs()...)
	out, err := appleRun(ctx, r, "notarization", "/usr/bin/xcrun", args...)
	if err != nil {
		return "", err
	}
	var res notaryResult
	if err := json.Unmarshal(bytes.TrimSpace(out), &res); err != nil || res.ID == "" {
		return "", errf(CodeVerificationFailed, "notarization output is not a readable verdict")
	}
	if res.Status != "Accepted" {
		return res.ID, errf(CodeVerificationFailed, "notarization submission %s was not accepted: %s", res.ID, res.Status)
	}
	return res.ID, nil
}

// AppleStaple staples the notarization ticket to path and validates it.
// It requires notarization to be configured, because a ticket exists only
// for a notarized artifact.
func AppleStaple(ctx context.Context, r AppleToolRunner, c AppleSigningConfig, path string) error {
	if err := c.require(AppleStepStaple); err != nil {
		return err
	}
	if !filepath.IsAbs(path) {
		return errf(CodeInvalidInput, "staple path must be absolute")
	}
	if _, err := appleRun(ctx, r, "stapling", "/usr/bin/xcrun", "stapler", "staple", path); err != nil {
		return err
	}
	_, err := appleRun(ctx, r, "stapled ticket validation", "/usr/bin/xcrun", "stapler", "validate", path)
	return err
}

// sortedMissing lists every unconfigured setting once, for status output.
func (c AppleSigningConfig) sortedMissing() []string {
	seen := map[string]bool{}
	for _, s := range c.Status() {
		for _, m := range s.Missing {
			seen[m] = true
		}
	}
	out := make([]string, 0, len(seen))
	for m := range seen {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}
