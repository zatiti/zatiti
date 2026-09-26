package effects

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestConnectionsResolveAcceptsHostedMemoryGrant(t *testing.T) {
	env := newEnv(t)
	grant := json.RawMessage(`{"issuer":"https://serenity.sire.run","resource":"https://serenity.sire.run/mcp","account_id":"account-1","project_id":"brain-1","scopes":["memory:read"],"verified_at":"2026-09-10T12:00:00Z"}`)
	env.ports.mu.Lock()
	env.ports.connHostedGrant = grant
	env.ports.mu.Unlock()

	var got resolveBody
	err := env.db.Write(env.ctx, env.actor, env.scope, func(unit contract.Unit) error {
		var callErr error
		got, callErr = env.svc.connectionsResolve(env.ctx, unit, connectionsResolveInput{
			Scope:      wireScope{InstallationID: env.install},
			Connection: wireRef{ID: env.ids.New(), Version: 1},
			Tool:       wireRef{ID: env.ids.New(), Version: 1},
		})
		return callErr
	})
	if err != nil {
		t.Fatalf("connections.resolve rejected hosted grant metadata: %v", err)
	}
	if !bytes.Equal(got.Connection.HostedMemoryGrant, grant) {
		t.Fatalf("resolved hosted grant = %s, want %s", got.Connection.HostedMemoryGrant, grant)
	}
}
