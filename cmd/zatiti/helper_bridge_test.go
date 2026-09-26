//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestNativeBridgePrepareCancelSingleUse(t *testing.T) {
	state := t.TempDir()
	installation, connection, challenge := contract.NewID(), contract.NewID(), contract.NewID()
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	secrets := newFakeSecretStore()
	beginCalls, closes := 0, 0
	op := &fakeOperator{fn: func(_ context.Context, operation string, req contract.Request) (contract.Result, error) {
		if strings.Contains(string(req.Input), "provider-secret") {
			t.Fatal("secret entered public operation")
		}
		var resource any
		switch operation {
		case "command.get":
			return contract.Result{Payload: contract.Payload{Status: contract.StatusFailed, Error: &contract.Fault{Code: contract.CodeNotFound}}}, nil
		case "connection.get":
			resource = map[string]any{"version": 1, "provider": "provider", "account_identity": "account"}
		case "connection.setup.begin":
			beginCalls++
			if req.SubmissionKey == "" {
				t.Fatal("begin lacks submission key")
			}
			resource = map[string]any{"id": challenge, "version": 1, "expires_at": expires}
		case "connection.setup.status":
			resource = map[string]any{"id": challenge, "version": 1, "connection_id": connection, "state": "external_action_required", "expires_at": expires}
		default:
			t.Fatalf("unexpected operation %s", operation)
		}
		raw, _ := json.Marshal(map[string]any{"resource": resource})
		return contract.Result{Payload: contract.Payload{Status: contract.StatusCompleted, Data: raw}}, nil
	}}
	b := newNativeBridge(func(_ context.Context, id contract.ID) (bridgeDependencies, error) {
		if id != installation {
			t.Fatal("wrong installation")
		}
		return bridgeDependencies{stateDir: state, op: op, secrets: secrets, close: func() { closes++ }}, nil
	})
	first := b.prepare(context.Background(), string(installation), string(connection))
	if first.Status != "ready" || len(first.Handle) != 64 || first.Provider != "provider" || first.AccountIdentity != "account" {
		t.Fatalf("prepare: %+v", first)
	}
	if b.prepare(context.Background(), string(installation), string(connection)).ReasonCode != "capture_busy" {
		t.Fatal("second capture accepted")
	}
	if b.cancel(first.Handle).Status != "cancelled" || closes != 1 {
		t.Fatal("cancel did not close")
	}
	if b.cancel(first.Handle).ReasonCode != "invalid_handle" {
		t.Fatal("handle reused")
	}
	second := b.prepare(context.Background(), string(installation), string(connection))
	if second.Status != "ready" || beginCalls != 1 {
		t.Fatalf("restarted challenge: %+v, begins=%d", second, beginCalls)
	}
	if b.commit(context.Background(), second.Handle, []byte{0xff}).ReasonCode != "invalid_credential" {
		t.Fatal("invalid UTF-8 accepted")
	}
	if b.commit(context.Background(), second.Handle, []byte("provider-secret")).ReasonCode != "invalid_handle" {
		t.Fatal("handle reused after invalid commit")
	}
	if closes != 2 {
		t.Fatalf("close count %d", closes)
	}
	if b.prepare(context.Background(), "invalid", string(connection)).ReasonCode != "invalid_identity" {
		t.Fatal("invalid ID accepted")
	}
}

func TestNativeBridgeExpiredHandleReleasesSlot(t *testing.T) {
	b := newNativeBridge(nil)
	now := time.Now()
	b.now = func() time.Time { return now }
	closed := 0
	b.sessions[strings.Repeat("a", 64)] = bridgeSession{deadline: now.Add(-time.Second), deps: bridgeDependencies{close: func() { closed++ }}}
	_ = b.prepare(context.Background(), string(contract.NewID()), string(contract.NewID()))
	if closed != 1 || b.active() != 0 {
		t.Fatalf("expired session remained: closes=%d active=%d", closed, b.active())
	}
}
