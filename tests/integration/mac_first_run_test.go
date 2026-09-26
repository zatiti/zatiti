package integration_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/installation"
	"github.com/zatiti/zatiti/internal/platform"
	"github.com/zatiti/zatiti/internal/server"
)

// TestMacFirstRunRecoversAfterCommitBeforeDiscoveryPublication exercises the
// production storage, installation, custody, discovery and authenticated
// transport seams without using a login Keychain. The simulated interruption
// is after installation.init committed but before the entrypoint can publish
// an initialized discovery record. A headless fixture cannot claim a Keychain
// locator; the cmd/zatiti entrypoint and native Keychain require separate Mac
// qualification.
func TestMacFirstRunRecoversAfterCommitBeforeDiscoveryPublication(t *testing.T) {
	ctx := context.Background()
	shortDir, err := os.MkdirTemp("", "zt-first-run-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortDir) })
	stateDir := filepath.Join(shortDir, "state")
	keyRef := writeMasterKey(t)
	f, err := assemble(t, fixtureOptions{stateDir: stateDir, keyRef: keyRef})
	if err != nil {
		t.Fatalf("first assembly: %v", err)
	}
	socket := filepath.Join(f.plat.StateDir(), "zatiti.sock")
	preboot := platform.DesktopDiscovery{Schema: platform.DesktopDiscoverySchema, ProtocolVersion: 1, SocketPath: socket}
	if err := f.plat.PublishDesktopDiscovery(ctx, preboot); err != nil {
		t.Fatalf("publish preboot discovery: %v", err)
	}
	if got, err := platform.ReadDesktopDiscovery(f.plat.StateDir()); err != nil || got != preboot {
		t.Fatalf("preboot discovery = %+v, %v", got, err)
	}
	f.bootstrap()
	before := f.eventCount()
	firstOwner := f.owner
	firstInstallation := f.installationID
	owner := committedOwnerMetadata(t, f, firstInstallation)
	if owner.InstallationID != firstInstallation || owner.OwnerID != firstOwner.PrincipalID || owner.CredentialID == "" || owner.StoreRef == "" {
		t.Fatalf("committed owner metadata %+v does not match bootstrap", owner)
	}
	secret, err := f.plat.Secrets().Get(ctx, owner.StoreRef)
	if err != nil {
		t.Fatalf("read committed owner StoreRef: %v", err)
	}
	if actor := f.authenticate(secret); actor.PrincipalID != owner.OwnerID || actor.Kind != contract.KindHuman {
		t.Fatalf("committed credential authenticates as %+v, want owner %s", actor, owner.OwnerID)
	}
	if got, err := platform.ReadDesktopDiscovery(f.plat.StateDir()); err != nil || got != preboot {
		t.Fatalf("discovery changed before publication: %+v, %v", got, err)
	}
	// Stop immediately after the commit. The next assembly must use the same
	// durable owner metadata and credential, without calling init again.
	f.close()
	restarted, err := assemble(t, fixtureOptions{stateDir: stateDir, keyRef: keyRef})
	if err != nil {
		t.Fatalf("restart assembly: %v", err)
	}
	recovered := committedOwnerMetadata(t, restarted, firstInstallation)
	if recovered != owner {
		t.Fatalf("recovered owner metadata %+v, want %+v", recovered, owner)
	}
	if got, err := platform.ReadDesktopDiscovery(restarted.plat.StateDir()); err != nil || got != preboot {
		t.Fatalf("preboot record after restart = %+v, %v", got, err)
	}
	recoveredSecret, err := restarted.plat.Secrets().Get(ctx, recovered.StoreRef)
	if err != nil || !bytes.Equal(recoveredSecret, secret) {
		t.Fatalf("recovered StoreRef differs from committed credential: %v", err)
	}
	restarted.owner = restarted.authenticate(recoveredSecret)
	restarted.installationID = recovered.InstallationID
	if restarted.owner.PrincipalID != firstOwner.PrincipalID || restarted.eventCount() != before {
		t.Fatalf("restart minted owner or changed events: owner=%s events=%d, want %s/%d", restarted.owner.PrincipalID, restarted.eventCount(), firstOwner.PrincipalID, before)
	}
	if _, err := restarted.app.Invoke(ctx, bootstrapActor(), "installation.init", contract.Request{Schema: contract.SchemaRequest, Input: initInput()}); faultCode(err) != contract.CodeConflict {
		t.Fatalf("restart initialization = %v, want conflict", err)
	}
	if restarted.eventCount() != before {
		t.Fatalf("refused reinitialization changed event count: %d -> %d", before, restarted.eventCount())
	}
	if _, _, err := restarted.plat.KeychainLocator(ctx, recovered.StoreRef); platform.Code(err) != contract.CodeCapabilityUnsupported {
		t.Fatalf("headless Keychain locator = %v, want capability_unsupported", err)
	}
	// The same committed credential must work through the real Unix socket.
	srv, err := server.New(server.Config{SocketPath: socket, MaxBodyBytes: 4 << 20}, restarted.app)
	if err != nil {
		t.Fatalf("server on discovery socket: %v", err)
	}
	restarted.runServer(srv)
	result, err := newClient(t, socket, staticCredential(recoveredSecret)).Call(ctx, "installation.status", contract.Request{
		Schema: contract.SchemaRequest, Input: mustJSON(map[string]any{"scope": restarted.scope()}),
	})
	if err != nil {
		t.Fatalf("authenticated status after restart: %v", err)
	}
	var status struct {
		Resource struct {
			InstallationID contract.ID `json:"installation_id"`
			Initialized    bool        `json:"initialized"`
			Generation     int64       `json:"generation"`
		} `json:"resource"`
	}
	decode(t, result.Data, &status)
	if !status.Resource.Initialized || status.Resource.InstallationID != firstInstallation || status.Resource.Generation != restarted.generation {
		t.Fatalf("restarted status = %s", result.Data)
	}
	if raw, err := os.ReadFile(filepath.Join(stateDir, "desktop.json")); err != nil || bytes.Contains(raw, secret) || bytes.Contains(raw, []byte(owner.StoreRef)) {
		t.Fatalf("discovery exposes credential or StoreRef, or is unreadable: %v", err)
	}
	if info, err := os.Stat(filepath.Join(stateDir, "desktop.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("discovery file permissions = %v, %v; want 0600", info, err)
	}
}

func committedOwnerMetadata(t testing.TB, f *fixture, installationID contract.ID) installation.OwnerCredential {
	t.Helper()
	var service *installation.Service
	for _, module := range f.modules {
		if candidate, ok := module.(*installation.Service); ok {
			service = candidate
			break
		}
	}
	if service == nil {
		t.Fatal("installation service absent from real module assembly")
	}
	var owner installation.OwnerCredential
	err := f.db.Read(context.Background(), bootstrapActor(), contract.Scope{InstallationID: installationID}, func(unit contract.Unit) error {
		var err error
		owner, err = service.OwnerCredential(context.Background(), unit)
		return err
	})
	if err != nil {
		t.Fatalf("read committed owner metadata: %v", err)
	}
	return owner
}
