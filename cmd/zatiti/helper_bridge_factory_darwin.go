//go:build darwin && cgo && zatiti_carchive

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/zatiti/zatiti/internal/client"
	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/platform"
)

// bridgeOwnerCredential reads the complete Authorization header only when a
// request is made. Client.Call owns and zeroes the returned copy.
type bridgeOwnerCredential struct {
	store contract.SecretStore
	ref   string
}

func (c bridgeOwnerCredential) Credential(ctx context.Context) ([]byte, error) {
	return c.store.Get(ctx, c.ref)
}

func installedBridgeDependencies(ctx context.Context, installationID contract.ID) (bridgeDependencies, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return bridgeDependencies{}, err
	}
	state := filepath.Join(base, "zatiti")
	d, err := platform.ReadDesktopDiscovery(state)
	if err != nil {
		return bridgeDependencies{}, err
	}
	if d.InstallationID != installationID || d.KeychainService == "" || d.KeychainAccount == "" {
		return bridgeDependencies{}, errors.New("installed identity is unavailable")
	}
	info, err := os.Lstat(d.SocketPath)
	if err != nil {
		return bridgeDependencies{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || stat.Uid != uint32(os.Geteuid()) {
		return bridgeDependencies{}, errors.New("installed socket is unsafe")
	}
	p, err := platform.Open(platform.Config{StateDir: state, CredentialBackend: "keychain", MasterKeyRef: "secret:master"})
	if err != nil {
		return bridgeDependencies{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = p.Close()
		}
	}()
	ref, err := p.Secrets().Lookup(ctx, d.KeychainAccount)
	if err != nil {
		return bridgeDependencies{}, err
	}
	service, account, err := p.KeychainLocator(ctx, ref)
	if err != nil {
		return bridgeDependencies{}, err
	}
	if service != d.KeychainService || account != d.KeychainAccount {
		return bridgeDependencies{}, errors.New("installed owner locator changed")
	}
	op, err := client.New(client.Config{SocketPath: d.SocketPath, Timeout: 15 * time.Second}, bridgeOwnerCredential{store: p.Secrets(), ref: ref})
	if err != nil {
		return bridgeDependencies{}, err
	}
	status, err := callOperation(ctx, op, "installation.status", map[string]any{"scope": map[string]any{"installation_id": installationID}}, "")
	if err != nil {
		return bridgeDependencies{}, err
	}
	var body struct {
		Resource struct {
			InstallationID contract.ID `json:"installation_id"`
		} `json:"resource"`
	}
	if err = json.Unmarshal(status.Data, &body); err != nil {
		return bridgeDependencies{}, err
	}
	if body.Resource.InstallationID != installationID {
		return bridgeDependencies{}, errors.New("installed controller identity changed")
	}
	keep = true
	return bridgeDependencies{stateDir: state, op: op, secrets: p.Secrets(), close: func() { _ = p.Close() }}, nil
}
