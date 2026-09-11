package connections

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Behavioral tests for the public connection.* handlers: staging through the
// compiler boundary, exact reads, structured listing with bound cursors and
// revocation semantics.

func TestConnectionCreateStagesChangeAndProjectsResource(t *testing.T) {
	env := newEnv(t)
	in := connCreateIn{Scope: env.scope, Definition: connDefIn{
		Scope: env.scope, Provider: "provider-example", AccountIdentity: "acct-example-9",
		CredentialRef: "connections/credentials/staged", Destinations: []string{"api.github.com"},
		AllowedScopes: []string{"repo:read"},
	}}
	payload := env.mustOK("connection.create", in)
	var out createOut
	env.decode(payload.Data, &out)
	if out.Resource.ID == "" || out.Resource.Version != 1 {
		t.Fatalf("resource identity not projected: %+v", out.Resource)
	}
	if out.Resource.ValidationState != connStateUnverified {
		t.Fatalf("staged connection state %q, want unverified", out.Resource.ValidationState)
	}
	if out.Resource.AccountIdentity != "acct-example-9" {
		t.Fatalf("staged account %q", out.Resource.AccountIdentity)
	}
	if out.Draft.ID == "" {
		t.Fatalf("stage returned no draft identity")
	}
	if len(out.Draft.Changes) != 1 {
		t.Fatalf("draft carries %d changes, want 1", len(out.Draft.Changes))
	}
	var staged wireChange
	if err := json.Unmarshal(out.Draft.Changes[0], &staged); err != nil {
		t.Fatalf("decode staged change: %v", err)
	}
	if staged.Kind != kindConnection || staged.Action != actionCreate || staged.ExpectedVersion != 0 {
		t.Fatalf("staged change shape: %+v", staged)
	}
	var def wireConnection
	if err := json.Unmarshal(staged.Definition, &def); err != nil {
		t.Fatalf("decode staged definition: %v", err)
	}
	if def.ID != out.Resource.ID || def.Version != 1 || def.CredentialRef != "connections/credentials/staged" {
		t.Fatalf("staged definition mismatch: %+v", def)
	}
	// The staged connection must not exist until activation.
	if _, err := env.call("connection.get", connGetIn{Scope: env.scope, ID: out.Resource.ID}); err == nil {
		t.Fatalf("connection.get resolved a connection that was only staged")
	}
}

func TestConnectionCreateRefusesDefinitionScopeMismatch(t *testing.T) {
	env := newEnv(t)
	other := env.scope
	other.InstallationID = env.ids.New()
	in := connCreateIn{Scope: env.scope, Definition: connDefIn{
		Scope: other, Provider: "p", AccountIdentity: "a", CredentialRef: "c",
		Destinations: []string{"api.github.com"}, AllowedScopes: []string{"s"},
	}}
	f := env.expectFault("connection.create", in, contract.CodeInvalidInput)
	if !strings.Contains(f.Message, "scope") {
		t.Fatalf("refusal message %q does not name the scope defect", f.Message)
	}
}

func TestConnectionCreateRefusesForeignInstallation(t *testing.T) {
	env := newEnv(t)
	in := connCreateIn{Scope: wireScope{InstallationID: env.ids.New()}, Definition: connDefIn{
		Scope: wireScope{InstallationID: env.ids.New()}, Provider: "p", AccountIdentity: "a", CredentialRef: "c",
		Destinations: []string{"api.github.com"}, AllowedScopes: []string{"s"},
	}}
	_ = env.expectFault("connection.create", in, contract.CodeInvalidInput)
}

func TestConnectionUpdateRules(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)

	t.Run("stages-projected-update", func(t *testing.T) {
		in := connUpdateIn{Scope: env.scope, ID: conn.ID, ExpectedVersion: 1, Definition: connDefIn{
			Scope: env.scope, Provider: "provider-example", AccountIdentity: "acct-example-1",
			CredentialRef: "connections/credentials/rotated", Destinations: conn.Destinations,
			AllowedScopes: conn.AllowedScopes,
		}}
		payload := env.mustOK("connection.update", in)
		var out createOut
		env.decode(payload.Data, &out)
		if out.Resource.Version != 2 {
			t.Fatalf("projected version %d, want 2", out.Resource.Version)
		}
		var staged wireChange
		if err := json.Unmarshal(out.Draft.Changes[0], &staged); err != nil {
			t.Fatalf("decode staged change: %v", err)
		}
		if staged.Action != actionUpdate || staged.ExpectedVersion != 1 {
			t.Fatalf("staged change pins wrong base: %+v", staged)
		}
		var def wireConnection
		if err := json.Unmarshal(staged.Definition, &def); err != nil {
			t.Fatalf("decode staged definition: %v", err)
		}
		if def.Version != 2 || def.CredentialRef != "connections/credentials/rotated" {
			t.Fatalf("staged definition mismatch: %+v", def)
		}
	})

	t.Run("stale-version-refused", func(t *testing.T) {
		in := connUpdateIn{Scope: env.scope, ID: conn.ID, ExpectedVersion: 99, Definition: connDefIn{
			Scope: env.scope, Provider: "provider-example", AccountIdentity: "acct-example-1",
			CredentialRef: conn.CredentialRef, Destinations: conn.Destinations, AllowedScopes: conn.AllowedScopes,
		}}
		_ = env.expectFault("connection.update", in, contract.CodeStaleVersion)
	})

	t.Run("scope-move-refused", func(t *testing.T) {
		moved := env.scope
		moved.OrganizationID = env.ids.New()
		in := connUpdateIn{Scope: env.scope, ID: conn.ID, ExpectedVersion: 1, Definition: connDefIn{
			Scope: moved, Provider: "provider-example", AccountIdentity: "acct-example-1",
			CredentialRef: conn.CredentialRef, Destinations: conn.Destinations, AllowedScopes: conn.AllowedScopes,
		}}
		f := env.expectFault("connection.update", in, contract.CodeInvalidInput)
		if !strings.Contains(f.Message, "cannot move") {
			t.Fatalf("refusal message %q does not name the scope move", f.Message)
		}
	})
}

func TestConnectionArchiveStagesUnchangedDefinition(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	in := connArchiveIn{Scope: env.scope, ID: conn.ID, ExpectedVersion: 1}
	payload := env.mustOK("connection.archive", in)
	var out createOut
	env.decode(payload.Data, &out)
	var staged wireChange
	if err := json.Unmarshal(out.Draft.Changes[0], &staged); err != nil {
		t.Fatalf("decode staged change: %v", err)
	}
	if staged.Action != actionArchive || staged.ExpectedVersion != 1 {
		t.Fatalf("staged archive shape: %+v", staged)
	}
	var def wireConnection
	if err := json.Unmarshal(staged.Definition, &def); err != nil {
		t.Fatalf("decode staged definition: %v", err)
	}
	if def.ValidationState != connStateUnverified || def.CredentialRef != conn.CredentialRef {
		t.Fatalf("archive must not alter the definition: %+v", def)
	}
	// Stale archive refuses.
	in.ExpectedVersion = 42
	_ = env.expectFault("connection.archive", in, contract.CodeStaleVersion)
}

func TestConnectionGetScopeRules(t *testing.T) {
	env := newEnv(t)
	org, worker := env.ids.New(), env.ids.New()
	scoped := env.seedExternalConnection(org, worker)

	t.Run("unknown-refuses-not-found", func(t *testing.T) {
		_ = env.expectFault("connection.get", connGetIn{Scope: env.scope, ID: env.ids.New()}, contract.CodeNotFound)
	})

	t.Run("unrelated-scope-refuses-not-found", func(t *testing.T) {
		request := env.scope
		request.OrganizationID = env.ids.New()
		_ = env.expectFault("connection.get", connGetIn{Scope: request, ID: scoped.ID}, contract.CodeNotFound)
	})

	t.Run("narrower-request-reaches-scoped-row", func(t *testing.T) {
		request := env.scope
		request.OrganizationID = org
		payload := env.mustOK("connection.get", connGetIn{Scope: request, ID: scoped.ID})
		var out resourceOut
		env.decode(payload.Data, &out)
		if out.Resource.ID != scoped.ID {
			t.Fatalf("resolved %s, want %s", out.Resource.ID, scoped.ID)
		}
	})
}

func TestConnectionListPaginationAndCursorBinding(t *testing.T) {
	env := newEnv(t)
	for i := 0; i < 3; i++ {
		env.seedConnection(nil)
	}
	first := connListIn{Scope: env.scope, Limit: 1}
	payload := env.mustOK("connection.list", first)
	var page1 listOut
	env.decode(payload.Data, &page1)
	if len(page1.Items) != 1 {
		t.Fatalf("page 1 carries %d items, want 1", len(page1.Items))
	}
	if payload.NextCursor == nil || *payload.NextCursor == "" {
		t.Fatalf("page 1 carries no continuation cursor")
	}

	// Same query, second page.
	second := connListIn{Scope: env.scope, Limit: 1, Cursor: *payload.NextCursor}
	payload2 := env.mustOK("connection.list", second)
	var page2 listOut
	env.decode(payload2.Data, &page2)
	if len(page2.Items) != 1 || page2.Items[0].ID == page1.Items[0].ID {
		t.Fatalf("page 2 repeats page 1: %v then %v", page1.Items, page2.Items)
	}

	t.Run("cursor-bound-to-actor", func(t *testing.T) {
		// The cursor was minted for env.actor; replaying under another
		// principal must refuse.
		in := connListIn{Scope: env.scope, Limit: 1, Cursor: *payload.NextCursor}
		_, err := env.callAs(env.scope, env.other, "connection.list", in)
		var f *contract.Fault
		if err == nil {
			t.Fatalf("cursor replayed under another principal succeeded")
		}
		if !errors.As(err, &f) || f.Code != contract.CodeCursorExpired {
			t.Fatalf("cursor replay under another principal: %v, want cursor_expired", err)
		}
	})

	t.Run("cursor-bound-to-query", func(t *testing.T) {
		// Changing the limit changes the query identity; the cursor must not
		// widen or shift the snapshot.
		in := connListIn{Scope: env.scope, Limit: 2, Cursor: *payload.NextCursor}
		f := env.expectFault("connection.list", in, contract.CodeCursorExpired)
		if !strings.Contains(f.Message, "different query") {
			t.Fatalf("refusal message %q does not name the query binding", f.Message)
		}
	})

	t.Run("malformed-cursor-refused", func(t *testing.T) {
		_ = env.expectFault("connection.list", connListIn{Scope: env.scope, Limit: 1, Cursor: "garbage"}, contract.CodeCursorExpired)
	})
}

func TestConnectionListStructuredFilters(t *testing.T) {
	env := newEnv(t)
	org := env.ids.New()
	valid := env.seedConnection(func(w *wireConnection) {
		w.AllowedScopes = []string{"repo:read"}
	})
	scoped := env.seedExternalConnection(org, env.ids.New())

	// Validate one connection so state filters have something to select.
	recordObservation(t, env, valid, obsSucceeded, valid.AccountIdentity, valid.AllowedScopes)

	cases := []struct {
		name   string
		filter listFilter
		want   []contract.ID
	}{
		{"state-valid", listFilter{State: connStateValid}, []contract.ID{valid.ID}},
		{"state-unverified", listFilter{State: connStateUnverified}, []contract.ID{scoped.ID}},
		{"organization", listFilter{OrganizationID: org}, []contract.ID{scoped.ID}},
		{"worker", listFilter{WorkerID: scoped.Scope.WorkerID}, []contract.ID{scoped.ID}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := env.mustOK("connection.list", connListIn{Scope: env.scope, Filter: &tc.filter})
			var out listOut
			env.decode(payload.Data, &out)
			got := make([]contract.ID, 0, len(out.Items))
			for _, item := range out.Items {
				got = append(got, item.ID)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("filter %v returned %v, want %v", tc.filter, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("filter %v returned %v, want %v", tc.filter, got, tc.want)
				}
			}
		})
	}

	t.Run("unsupported-filter-field-refused", func(t *testing.T) {
		filter := listFilter{Key: "nope"}
		f := env.expectFault("connection.list", connListIn{Scope: env.scope, Filter: &filter}, contract.CodeInvalidInput)
		if !strings.Contains(f.Message, "key") {
			t.Fatalf("refusal message %q does not name the field", f.Message)
		}
	})
}

func TestConnectionRevoke(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)

	t.Run("immediate-block", func(t *testing.T) {
		payload := env.mustOK("connection.revoke", connRevokeIn{Scope: env.scope, ID: conn.ID, ExpectedVersion: 1})
		var out dispositionOut
		env.decode(payload.Data, &out)
		if out.Resource.State != connStateRevoked || out.Resource.Version != 2 {
			t.Fatalf("disposition %+v, want revoked at version 2", out.Resource)
		}
		_ = env.expectFault("_connections.resolve", resolveIn{
			Scope: env.scope, Connection: wireRef{ID: conn.ID, Version: 2},
			Tool: wireRef{ID: toolRESTRead, Version: 1}, Destination: "api.github.com",
		}, contract.CodePrerequisiteMissing)
	})

	t.Run("idempotent-re-revoke", func(t *testing.T) {
		payload := env.mustOK("connection.revoke", connRevokeIn{Scope: env.scope, ID: conn.ID, ExpectedVersion: 2})
		var out dispositionOut
		env.decode(payload.Data, &out)
		if out.Resource.Version != 2 {
			t.Fatalf("re-revoke bumped the version to %d", out.Resource.Version)
		}
	})

	t.Run("emits-revocation-event", func(t *testing.T) {
		kinds := env.kindsOf()
		found := false
		for _, k := range kinds {
			if k == "connections.connection.revoked" {
				found = true
			}
		}
		if !found {
			t.Fatalf("no connections.connection.revoked event among %v", kinds)
		}
	})
}

func TestRevokeImmediatelyBlocksSetupWork(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	env.mustOK("connection.revoke", connRevokeIn{Scope: env.scope, ID: conn.ID, ExpectedVersion: 1})
	_ = env.expectIOFault("connection.setup.begin", beginInput{
		Scope: env.scope, ConnectionID: conn.ID, ExpectedVersion: 2, Method: methodStoreReference,
	}, contract.CodePrerequisiteMissing)
}

// recordObservation drives one validation record through the internal path.
func recordObservation(t *testing.T, env *testEnv, conn wireConnection, disposition, account string, scopes []string) {
	t.Helper()
	evidence := map[string]any{"account_identity": account, "allowed_scopes": scopes}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatalf("marshal evidence: %v", err)
	}
	env.mustOK("_connections.validation.record", validationRecordIn{
		ConnectionID: conn.ID, ExpectedVersion: conn.Version,
		Observation: wireObservation{
			Disposition: disposition, Evidence: raw,
			Usage: &wireUsage{Currency: "USD", Advisory: true},
		},
	})
}

// validationRecordIn mirrors the internal record input; the alias keeps test
// call sites readable.
type validationRecordIn = struct {
	ConnectionID    contract.ID     `json:"connection_id"`
	ExpectedVersion int64           `json:"expected_version"`
	Observation     wireObservation `json:"observation"`
}

// resolveIn mirrors the internal resolve input.
type resolveIn = struct {
	Scope       wireScope `json:"scope"`
	Connection  wireRef   `json:"connection"`
	Tool        wireRef   `json:"tool"`
	Destination string    `json:"destination"`
}
