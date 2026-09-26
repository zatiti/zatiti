package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/installation"
	"github.com/zatiti/zatiti/internal/platform"
)

// installedMacDiscovery excludes explicit development layouts and headless
// custody. The installed Mac app discovers only the default state/socket.
func installedMacDiscovery(cfg config) bool {
	if runtime.GOOS != "darwin" || cfg.CredentialBackend == "headless" {
		return false
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return false
	}
	state := filepath.Join(base, "zatiti")
	return cfg.StateDir == state && cfg.SocketPath == filepath.Join(state, socketFileName)
}

func (h *installationHandle) publishPrebootstrapDiscovery(ctx context.Context) error {
	if !installedMacDiscovery(h.cfg) {
		return nil
	}
	return h.plat.PublishDesktopDiscovery(ctx, platform.DesktopDiscovery{
		Schema: platform.DesktopDiscoverySchema, ProtocolVersion: 1, SocketPath: h.cfg.SocketPath,
	})
}

func (h *installationHandle) publishInitializedDiscovery(ctx context.Context, installationID contract.ID, owner installation.OwnerCredential) error {
	if !installedMacDiscovery(h.cfg) {
		return nil
	}
	record, err := ownerDesktopDiscovery(ctx, h.plat, h.cfg.SocketPath, installationID, owner)
	if err != nil {
		return err
	}
	return h.plat.PublishDesktopDiscovery(ctx, record)
}

type ownerLocator interface {
	KeychainLocator(context.Context, string) (string, string, error)
}

// ownerDesktopDiscovery accepts an opaque StoreRef and releases only its
// nonsecret Keychain locator. The owner bytes never enter the record.
func ownerDesktopDiscovery(ctx context.Context, locator ownerLocator, socket string, installationID contract.ID, owner installation.OwnerCredential) (platform.DesktopDiscovery, error) {
	if owner.InstallationID != installationID || owner.StoreRef == "" {
		return platform.DesktopDiscovery{}, &contract.Fault{Code: contract.CodePrerequisiteMissing, Message: "committed owner credential does not match desktop discovery"}
	}
	service, account, err := locator.KeychainLocator(ctx, owner.StoreRef)
	if err != nil {
		return platform.DesktopDiscovery{}, fmt.Errorf("owner Keychain locator is unavailable: %w", err)
	}
	return platform.DesktopDiscovery{
		Schema: platform.DesktopDiscoverySchema, ProtocolVersion: 1, SocketPath: socket,
		InstallationID: installationID, KeychainService: service, KeychainAccount: account,
	}, nil
}
