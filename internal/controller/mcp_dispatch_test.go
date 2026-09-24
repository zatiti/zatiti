package controller

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestMCPDispatchUnwrapsOnlyPinnedParameters(t *testing.T) {
	f := newFx(t)
	c := f.controller(newOwnership())
	adapter := f.adapter("mcp")
	params := json.RawMessage(`{"schema":"zatiti.mcp.action/v1","kind":"call_tool","tool":"alpha","arguments":{"q":"pinned"},"profile_digest":"sha256:pinned"}`)
	outer, err := json.Marshal(map[string]any{"parameters": params, "account_identity": "credential:opaque"})
	if err != nil {
		t.Fatal(err)
	}
	dispatch := contract.Dispatch{Adapter: "mcp", Action: outer, OperationID: contract.NewID(), AttemptID: contract.NewID(), CredentialRef: "opaque", Deadline: time.Now().Add(time.Minute)}
	adapter.reply = func(_ context.Context, got contract.Dispatch) (contract.Observation, error) {
		if string(got.Action) != string(params) {
			t.Fatalf("adapter did not receive exact pinned parameters: %s", got.Action)
		}
		if got.OperationID != dispatch.OperationID || got.AttemptID != dispatch.AttemptID || got.CredentialRef != dispatch.CredentialRef || got.Deadline != dispatch.Deadline {
			t.Fatal("dispatch provenance changed")
		}
		return succeeded(nil), nil
	}
	obs := c.observe(context.Background(), adapter, dispatch, nil)
	if obs.Disposition != contract.DispositionSucceeded || adapter.calls() != 1 {
		t.Fatal("governed dispatch failed")
	}
	dispatch.Action = params
	obs = c.observe(context.Background(), adapter, dispatch, nil)
	if adapter.calls() != 1 || obs.Disposition != contract.DispositionUnknown {
		t.Fatal("raw MCP parameters bypassed governed envelope")
	}
}

func TestMCPMissingLinkedJobDefersAdmission(t *testing.T) {
	f := newFx(t)
	adapter := f.adapter("mcp")
	op := f.prepareRouted("mcp", nil, `{"kind":"connection"}`)
	c, sess := f.started()
	if err := f.pass(c, sess); err != nil {
		t.Fatal(err)
	}
	if f.called("_effects.admit") != 0 || f.called("_effects.claim") != 0 || adapter.calls() != 0 || f.opState(op) != "prepared" {
		t.Fatal("limited job scan dispatched an effect without its callback owner")
	}
	var action wireAction
	if err := json.Unmarshal(f.action(nil, ""), &action); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"connection.validate", "connection.discover"} {
		job := wireJob{ID: contract.NewID(), Owner: ownerConnections, Operation: kind, OperationID: op}
		r := c.routeFor(op, wireOperation{CallbackRoute: json.RawMessage(`{"kind":"connection"}`)}, action, map[contract.ID]wireJob{op: job})
		if r.Owner != ownerConnections || r.JobID != job.ID || r.ProbeKind != kind {
			t.Fatal("linked callback route was lost")
		}
	}
}
