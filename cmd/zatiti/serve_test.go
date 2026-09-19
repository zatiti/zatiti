package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/client"
	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/identity"
)

// startupBudget bounds how long a test waits for serve to bind its socket:
// assembling the registry validates every schema of every module, which
// under the race detector on a loaded machine has taken over 30 seconds.
const startupBudget = 120 * time.Second

// exitBudget bounds every wait for serve to exit after its trigger (a
// cancelled context, a lost lock, a refusal): a serve that does not exit is
// a defect that must turn red in seconds, never hang to the package
// timeout.
const exitBudget = shutdownGrace + 10*time.Second

// awaitExit waits for serve's verdict within exitBudget and fails with a
// named message otherwise.
func awaitExit(t *testing.T, done <-chan error, what string, logs *lockedBuffer) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(exitBudget):
		t.Fatalf("serve kept running for %s after %s; it must have exited (logs:\n%s)", exitBudget, what, logs.String())
		return nil
	}
}

// serveInBackground runs runServe and returns the result channel once the
// socket is bound.
func serveInBackground(t *testing.T, ctx context.Context, cfg config) (<-chan error, *lockedBuffer) {
	t.Helper()
	logs := &lockedBuffer{}
	log := newLogger(logs, slog.LevelInfo)
	listening := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runServe(ctx, cfg, log, serveOptions{pollInterval: 20 * time.Millisecond, listening: func() { close(listening) }})
	}()
	select {
	case <-listening:
	case err := <-done:
		t.Fatalf("serve ended before listening: %v", err)
	case <-time.After(startupBudget):
		t.Fatal("serve did not start listening")
	}
	return done, logs
}

// TestServeRestartsOnInitializedInstallation: a fresh process over an
// already bootstrapped state directory custodies nothing and resolves the
// controller principal bootstrap created through the identity module, then
// runs the controller without any bootstrap step.
func TestServeRestartsOnInitializedInstallation(t *testing.T) {
	cfg := serveConfig(t)
	h := openTestInstallation(t, cfg)
	bootstrapInstallation(t, h)
	h.close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done, logs := serveInBackground(t, ctx, cfg)
	waitFor(t, 15*time.Second, "the controller to run", func() bool {
		select {
		case err := <-done:
			t.Fatalf("serve exited: %v (logs:\n%s)", err, logs.String())
		default:
		}
		return strings.Contains(logs.String(), "controller running")
	})
	if strings.Contains(logs.String(), "bootstrap completed") {
		t.Fatalf("a restart must not complete bootstrap again (logs:\n%s)", logs.String())
	}
	cancel()
	if err := awaitExit(t, done, "context cancellation", logs); err != nil {
		t.Fatalf("serve = %v on shutdown", err)
	}
}

// TestServeRefusesUnknownControllerPrincipalOverride: an explicit
// --controller-principal that identity does not know fails the controller
// closed at its first internal call instead of scheduling as nobody.
func TestServeRefusesUnknownControllerPrincipalOverride(t *testing.T) {
	cfg := serveConfig(t)
	h := openTestInstallation(t, cfg)
	bootstrapInstallation(t, h)
	h.close()
	cfg.ControllerPrincipal = string(contract.NewID())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done, logs := serveInBackground(t, ctx, cfg)
	err := awaitExit(t, done, "an unknown --controller-principal that the controller must refuse", logs)
	if faultCode(err) != contract.CodePermissionDenied {
		t.Fatalf("serve = %v, want permission_denied for an unknown controller principal (logs:\n%s)", err, logs.String())
	}
	if _, statErr := os.Stat(cfg.SocketPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("socket left behind after refusal: %v", statErr)
	}
}

func TestServeStopsOnContextCancel(t *testing.T) {
	cfg := serveConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	done, logs := serveInBackground(t, ctx, cfg)
	cancel()
	if err := awaitExit(t, done, "context cancellation", logs); err != nil {
		t.Fatalf("serve = %v, want nil on orderly shutdown", err)
	}
	if _, err := os.Stat(cfg.SocketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket not removed on shutdown: %v", err)
	}
}

// TestServeStopsWhenOwnershipIsLost removes the lock file under a serving
// controller, which the platform watchdog reports as lost ownership; serve
// must stop admitting and exit controller_unavailable.
func TestServeStopsWhenOwnershipIsLost(t *testing.T) {
	cfg := serveConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done, logs := serveInBackground(t, ctx, cfg)
	if err := os.Remove(filepath.Join(cfg.StateDir, "controller.lock")); err != nil {
		t.Fatalf("removing the lock file: %v", err)
	}
	if err := awaitExit(t, done, "lock loss", logs); faultCode(err) != contract.CodeControllerUnavailable {
		t.Fatalf("serve = %v, want controller_unavailable after lock loss (logs:\n%s)", err, logs.String())
	}
}

// TestServeCompletesBootstrapOverTheSocket is the product's first-run path
// in one process: serve on an empty state directory, installation.init over
// the socket without a credential, then the controller hands the owner
// credential to the owner profile, provisions its own service identity and
// starts; the owner then authenticates with that profile.
func TestServeCompletesBootstrapOverTheSocket(t *testing.T) {
	cfg := serveConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done, logs := serveInBackground(t, ctx, cfg)

	anon, err := client.New(client.Config{SocketPath: cfg.SocketPath, Timeout: 10 * time.Second}, nil)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	res, err := anon.Call(ctx, bootstrapOperation, contract.Request{
		Schema: contract.SchemaRequest,
		Input:  json.RawMessage(`{"credential_store":"headless","owner_name":"Socket Owner","headless_key_ref":"installation/owner"}`),
	})
	if err != nil || res.Status != contract.StatusCompleted {
		t.Fatalf("installation.init over the socket: %v (%+v)", err, res)
	}
	var out struct {
		Resource struct {
			InstallationID contract.ID `json:"installation_id"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		t.Fatalf("init data: %v", err)
	}

	store := profileStore{dir: cfg.profilesDir()}
	waitFor(t, 15*time.Second, "the owner profile and the running controller", func() bool {
		select {
		case err := <-done:
			t.Fatalf("serve exited during bootstrap completion: %v (logs:\n%s)", err, logs.String())
		default:
		}
		ok, _ := store.exists(defaultProfile)
		return ok && strings.Contains(logs.String(), "controller running")
	})

	owner, err := client.New(client.Config{SocketPath: cfg.SocketPath, Timeout: 10 * time.Second}, profileCredential{store: store, name: defaultProfile})
	if err != nil {
		t.Fatalf("owner client: %v", err)
	}
	scope := map[string]any{"installation_id": out.Resource.InstallationID}
	listed, err := owner.Call(ctx, "principal.list", contract.Request{Schema: contract.SchemaRequest, Input: mustJSON(t, map[string]any{"scope": scope})})
	if err != nil {
		t.Fatalf("principal.list as the owner: %v", err)
	}
	if !strings.Contains(string(listed.Data), identity.ControllerPrincipalName) {
		t.Fatalf("principal.list does not show the controller principal: %s", listed.Data)
	}
	if _, err := anon.Call(ctx, bootstrapOperation, contract.Request{
		Schema: contract.SchemaRequest,
		Input:  json.RawMessage(`{"credential_store":"headless","owner_name":"Again","headless_key_ref":"installation/owner"}`),
	}); faultCode(err) != contract.CodeConflict {
		t.Fatalf("second bootstrap: %v, want conflict", err)
	}
	if !strings.Contains(logs.String(), "controller running") {
		t.Fatalf("controller did not start after bootstrap (logs:\n%s)", logs.String())
	}
	cancel()
	if err := awaitExit(t, done, "context cancellation", logs); err != nil {
		t.Fatalf("serve = %v on shutdown", err)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}
