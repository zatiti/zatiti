package connections

import (
	"strings"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
)

// Behavioral tests for the internal operations: activation under the compiler
// boundary with its replay fence, exact dispatch resolution and candidate
// validation diagnostics.

func TestActivateAppliesConnectionChanges(t *testing.T) {
	env := newEnv(t)
	w := wireConnection{
		ID: env.ids.New(), Version: 1, Scope: env.scope,
		Provider: "provider-example", AccountIdentity: "acct-example-1",
		CredentialRef: "connections/credentials/x", Destinations: []string{"api.github.com"},
		AllowedScopes: []string{"repo:read"}, ValidationState: connStateUnverified,
	}
	versions := env.activate(connectionDef(w))
	if len(versions) != 1 || versions[0].ID != w.ID || versions[0].Version != 1 {
		t.Fatalf("activation produced %v", versions)
	}
	payload := env.mustOK("connection.get", connGetIn{Scope: env.scope, ID: w.ID})
	var out resourceOut
	env.decode(payload.Data, &out)
	if out.Resource.ValidationState != connStateUnverified || out.Resource.CredentialRef != w.CredentialRef {
		t.Fatalf("activated row mismatch: %+v", out.Resource)
	}
	kinds := env.kindsOf()
	found := false
	for _, k := range kinds {
		if k == "connections.connection.created" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no creation event among %v", kinds)
	}
}

func TestActivateRefusesDuplicateCreate(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	_ = env.expectFault("_connections.activate", candidateInBody{Candidate: candidateBody{
		PlanID: env.ids.New(), BaseRevision: 1, CandidateDigest: testDigest("dup"),
		Changes: []wireChange{connectionDef(conn)}, Dependencies: []wireRef{},
	}}, contract.CodeConflict)
}

func TestActivateUpdateRules(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)

	t.Run("applies-projected-update", func(t *testing.T) {
		updated := conn
		updated.Version = 2
		updated.CredentialRef = "connections/credentials/rotated"
		versions := env.activate(wireChange{
			Kind: kindConnection, Action: actionUpdate, ID: conn.ID, ExpectedVersion: 1,
			Definition: mustRaw(updated),
		})
		if len(versions) != 1 || versions[0].Version != 2 {
			t.Fatalf("update produced %v", versions)
		}
		payload := env.mustOK("connection.get", connGetIn{Scope: env.scope, ID: conn.ID})
		var out resourceOut
		env.decode(payload.Data, &out)
		if out.Resource.Version != 2 || out.Resource.CredentialRef != "connections/credentials/rotated" {
			t.Fatalf("updated row mismatch: %+v", out.Resource)
		}
	})

	t.Run("wrong-expected-version-refused", func(t *testing.T) {
		updated := conn
		updated.Version = 3
		_ = env.expectFault("_connections.activate", candidateInBody{Candidate: candidateBody{
			PlanID: env.ids.New(), BaseRevision: 1, CandidateDigest: testDigest("stale-pin"),
			Changes: []wireChange{{
				Kind: kindConnection, Action: actionUpdate, ID: conn.ID, ExpectedVersion: 1,
				Definition: mustRaw(updated),
			}},
			Dependencies: []wireRef{},
		}}, contract.CodeStaleVersion)
	})

	t.Run("wrong-projected-version-refused", func(t *testing.T) {
		// The row is at version 2; a definition claiming version 5 does not
		// project from it.
		updated := conn
		updated.Version = 5
		_ = env.expectFault("_connections.activate", candidateInBody{Candidate: candidateBody{
			PlanID: env.ids.New(), BaseRevision: 1, CandidateDigest: testDigest("bad-projection"),
			Changes: []wireChange{{
				Kind: kindConnection, Action: actionUpdate, ID: conn.ID, ExpectedVersion: 2,
				Definition: mustRaw(updated),
			}},
			Dependencies: []wireRef{},
		}}, contract.CodeInvalidInput)
	})
}

func TestActivateArchiveAndRefusals(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)

	t.Run("archive-flips-lifecycle", func(t *testing.T) {
		versions := env.activate(archiveDef(conn.ID, 1, conn))
		if len(versions) != 1 || versions[0].Version != 2 {
			t.Fatalf("archive produced %v", versions)
		}
		_ = env.expectFault("_connections.resolve", resolveIn{
			Scope: env.scope, Connection: wireRef{ID: conn.ID, Version: 2},
			Tool: wireRef{ID: toolRESTRead, Version: 1}, Destination: "api.github.com",
		}, contract.CodePrerequisiteMissing)
	})

	t.Run("delete-never-allowed", func(t *testing.T) {
		_ = env.expectFault("_connections.activate", candidateInBody{Candidate: candidateBody{
			PlanID: env.ids.New(), BaseRevision: 1, CandidateDigest: testDigest("delete"),
			Changes: []wireChange{{
				Kind: kindConnection, Action: actionDelete, ID: conn.ID, ExpectedVersion: 2,
				Definition: mustRaw(map[string]any{}),
			}},
			Dependencies: []wireRef{},
		}}, contract.CodeInvalidInput)
	})

	t.Run("foreign-installation-update-refused", func(t *testing.T) {
		_ = env.expectFault("_connections.activate", candidateInBody{Candidate: candidateBody{
			PlanID: env.ids.New(), BaseRevision: 1, CandidateDigest: testDigest("foreign"),
			Changes: []wireChange{{
				Kind: kindConnection, Action: actionUpdate, ID: env.ids.New(), ExpectedVersion: 1,
				Definition: mustRaw(wireConnection{
					ID: env.ids.New(), Version: 2, Scope: wireScope{InstallationID: env.ids.New()},
					Provider: "p", AccountIdentity: "a", CredentialRef: "c",
					Destinations: []string{}, AllowedScopes: []string{}, ValidationState: connStateUnverified,
				}),
			}},
			Dependencies: []wireRef{},
		}}, contract.CodeInvalidInput)
	})
}

func TestActivateReplayFence(t *testing.T) {
	env := newEnv(t)
	planID := env.ids.New()
	w := wireConnection{
		ID: env.ids.New(), Version: 1, Scope: env.scope,
		Provider: "provider-example", AccountIdentity: "acct-example-1",
		CredentialRef: "connections/credentials/replay", Destinations: []string{"api.github.com"},
		AllowedScopes: []string{"repo:read"}, ValidationState: connStateUnverified,
	}
	body := candidateBody{
		PlanID: planID, BaseRevision: 7, CandidateDigest: testDigest("replay-plan"),
		Changes: []wireChange{connectionDef(w)}, Dependencies: []wireRef{},
	}
	first := env.mustOK("_connections.activate", candidateInBody{Candidate: body})
	var out1 activateOut
	env.decode(first.Data, &out1)

	t.Run("identical-replay-returns-recorded-versions", func(t *testing.T) {
		replay := env.mustOK("_connections.activate", candidateInBody{Candidate: body})
		var out2 activateOut
		env.decode(replay.Data, &out2)
		if len(out2.Versions) != len(out1.Versions) || out2.Versions[0].ID != out1.Versions[0].ID {
			t.Fatalf("replay produced %v, want recorded %v", out2.Versions, out1.Versions)
		}
		// No second row was created.
		payload := env.mustOK("connection.get", connGetIn{Scope: env.scope, ID: w.ID})
		var got resourceOut
		env.decode(payload.Data, &got)
		if got.Resource.Version != 1 {
			t.Fatalf("replay mutated the row to version %d", got.Resource.Version)
		}
	})

	t.Run("digest-mismatch-refused", func(t *testing.T) {
		tampered := body
		tampered.CandidateDigest = testDigest("different-candidate")
		_ = env.expectFault("_connections.activate", candidateInBody{Candidate: tampered}, contract.CodeStaleVersion)
	})

	t.Run("base-revision-mismatch-refused", func(t *testing.T) {
		tampered := body
		tampered.BaseRevision = 9
		_ = env.expectFault("_connections.activate", candidateInBody{Candidate: tampered}, contract.CodeStaleVersion)
	})
}

func TestResolveAdmissionRules(t *testing.T) {
	env := newEnv(t)
	valid := env.seedConnection(func(w *wireConnection) {
		w.Destinations = []string{"api.github.com"}
	})
	// Grant freshness with one succeeded probe.
	recordObservation(t, env, valid, obsSucceeded, valid.AccountIdentity, valid.AllowedScopes)
	valid.Version = 2

	okIn := resolveIn{
		Scope: env.scope, Connection: wireRef{ID: valid.ID, Version: valid.Version},
		Tool: wireRef{ID: toolRESTRead, Version: 1}, Destination: "api.github.com",
	}
	payload := env.mustOK("_connections.resolve", okIn)
	var out struct {
		Connection wireConnection `json:"connection"`
		Tool       wireTool       `json:"tool"`
	}
	env.decode(payload.Data, &out)
	if out.Connection.ID != valid.ID || out.Tool.ID != toolRESTRead {
		t.Fatalf("resolve returned %+v", out)
	}

	cases := []struct {
		name   string
		mutate func(*resolveIn)
		code   string
	}{
		{"unknown-connection", func(in *resolveIn) { in.Connection.ID = env.ids.New() }, contract.CodeNotFound},
		{"stale-connection-pin", func(in *resolveIn) { in.Connection.Version = 1 }, contract.CodeStaleVersion},
		{"unknown-tool", func(in *resolveIn) { in.Tool.ID = env.ids.New() }, contract.CodeNotFound},
		{"stale-tool-pin", func(in *resolveIn) { in.Tool.Version = 2 }, contract.CodeStaleVersion},
		{"destination-outside-tool", func(in *resolveIn) { in.Destination = "elsewhere.example.test" }, contract.CodePermissionDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := okIn
			tc.mutate(&in)
			_ = env.expectFault("_connections.resolve", in, tc.code)
		})
	}

	t.Run("destination-outside-connection-refused", func(t *testing.T) {
		// The model contract serves api.openai.com, a destination this
		// connection never lists: the connection boundary refuses the pair.
		in := resolveIn{
			Scope: env.scope, Connection: wireRef{ID: valid.ID, Version: valid.Version},
			Tool: wireRef{ID: toolModelRead, Version: 1}, Destination: "api.openai.com",
		}
		_ = env.expectFault("_connections.resolve", in, contract.CodePermissionDenied)
	})

	t.Run("revocation-blocks-dispatch", func(t *testing.T) {
		revoked := env.seedConnection(nil)
		env.mustOK("connection.revoke", connRevokeIn{Scope: env.scope, ID: revoked.ID, ExpectedVersion: 1})
		_ = env.expectFault("_connections.resolve", resolveIn{
			Scope: env.scope, Connection: wireRef{ID: revoked.ID, Version: 2},
			Tool: wireRef{ID: toolRESTRead, Version: 1}, Destination: "api.github.com",
		}, contract.CodePrerequisiteMissing)
	})
}

func TestValidateCandidateDiagnostics(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)

	changed := conn
	changed.Version = 2
	changed.AccountIdentity = "acct-other"

	// Validate a candidate substituting the account: warning diagnostic plus
	// a named review requirement, under the old effective authority.
	payload := env.mustOK("_connections.validate", candidateInBody{Candidate: candidateBody{
		PlanID: env.ids.New(), BaseRevision: 1, CandidateDigest: testDigest("validate"),
		Changes: []wireChange{{
			Kind: kindConnection, Action: actionUpdate, ID: conn.ID, ExpectedVersion: 1,
			Definition: mustRaw(changed),
		}},
		Dependencies: []wireRef{},
	}})
	var out validationOut
	env.decode(payload.Data, &out)
	if len(out.Resource.Dependencies) != 1 || out.Resource.Dependencies[0].ID != conn.ID {
		t.Fatalf("validate pinned dependencies %v", out.Resource.Dependencies)
	}
	if len(out.Resource.Requirements) != 1 || out.Resource.Requirements[0].Code != "connections.account_substitution" {
		t.Fatalf("substitution requirements %v", out.Resource.Requirements)
	}
	severity := map[string]string{}
	for _, d := range out.Resource.Diagnostics {
		severity[d.Code] = d.Severity
	}
	if severity["account_substitution"] != "warning" {
		t.Fatalf("substitution diagnostic severities %v", severity)
	}

	// The live row is untouched by validation.
	payload = env.mustOK("connection.get", connGetIn{Scope: env.scope, ID: conn.ID})
	var got resourceOut
	env.decode(payload.Data, &got)
	if got.Resource.AccountIdentity != conn.AccountIdentity || got.Resource.Version != 1 {
		t.Fatalf("validation mutated the row: %+v", got.Resource)
	}
}

func TestValidateCandidateDiagnosticCases(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)

	build := func(ch wireChange) candidateInBody {
		return candidateInBody{Candidate: candidateBody{
			PlanID: env.ids.New(), BaseRevision: 1, CandidateDigest: testDigest(ch.Action + string(ch.ID)),
			Changes: []wireChange{ch}, Dependencies: []wireRef{},
		}}
	}
	validate := func(ch wireChange) wireValidation {
		payload := env.mustOK("_connections.validate", build(ch))
		var out validationOut
		env.decode(payload.Data, &out)
		return out.Resource
	}
	codes := func(v wireValidation) map[string]string {
		m := map[string]string{}
		for _, d := range v.Diagnostics {
			m[d.Code] = d.Severity
		}
		return m
	}

	t.Run("create-non-v1-version", func(t *testing.T) {
		w := conn
		w.ID = env.ids.New()
		w.Version = 3
		v := validate(connectionDef(w))
		if codes(v)["version"] != "error" {
			t.Fatalf("diagnostics %v", v.Diagnostics)
		}
	})

	t.Run("create-existing-conflict", func(t *testing.T) {
		v := validate(connectionDef(conn))
		if codes(v)["conflict"] != "error" {
			t.Fatalf("diagnostics %v", v.Diagnostics)
		}
	})

	t.Run("update-unknown-connection", func(t *testing.T) {
		w := conn
		w.ID = env.ids.New()
		v := validate(wireChange{Kind: kindConnection, Action: actionUpdate, ID: w.ID, ExpectedVersion: 1, Definition: mustRaw(w)})
		if codes(v)["not_found"] != "error" {
			t.Fatalf("diagnostics %v", v.Diagnostics)
		}
	})

	t.Run("update-stale-expected-version", func(t *testing.T) {
		w := conn
		w.Version = 2
		v := validate(wireChange{Kind: kindConnection, Action: actionUpdate, ID: conn.ID, ExpectedVersion: 7, Definition: mustRaw(w)})
		if codes(v)["stale_version"] != "error" {
			t.Fatalf("diagnostics %v", v.Diagnostics)
		}
	})

	t.Run("scope-move-flagged", func(t *testing.T) {
		w := conn
		w.Version = 2
		w.Scope.OrganizationID = env.ids.New()
		v := validate(wireChange{Kind: kindConnection, Action: actionUpdate, ID: conn.ID, ExpectedVersion: 1, Definition: mustRaw(w)})
		if codes(v)["scope_move"] != "error" {
			t.Fatalf("diagnostics %v", v.Diagnostics)
		}
	})

	t.Run("provider-change-same-account", func(t *testing.T) {
		w := conn
		w.Version = 2
		w.Provider = "provider-other"
		v := validate(wireChange{Kind: kindConnection, Action: actionUpdate, ID: conn.ID, ExpectedVersion: 1, Definition: mustRaw(w)})
		if codes(v)["provider_change"] != "warning" {
			t.Fatalf("diagnostics %v", v.Diagnostics)
		}
		if len(v.Requirements) != 1 || v.Requirements[0].Code != "connections.provider_change" {
			t.Fatalf("requirements %v", v.Requirements)
		}
	})

	t.Run("credential-replacement-is-informational", func(t *testing.T) {
		w := conn
		w.Version = 2
		w.CredentialRef = "connections/credentials/new"
		v := validate(wireChange{Kind: kindConnection, Action: actionUpdate, ID: conn.ID, ExpectedVersion: 1, Definition: mustRaw(w)})
		if codes(v)["credential_replacement"] != "info" {
			t.Fatalf("diagnostics %v", v.Diagnostics)
		}
		if len(v.Requirements) != 0 {
			t.Fatalf("credential replacement must not demand review: %v", v.Requirements)
		}
	})

	t.Run("archive-unknown-and-delete", func(t *testing.T) {
		v := validate(archiveDef(env.ids.New(), 1, conn))
		if codes(v)["not_found"] != "error" {
			t.Fatalf("archive diagnostics %v", v.Diagnostics)
		}
		v = validate(wireChange{Kind: kindConnection, Action: actionDelete, ID: conn.ID, ExpectedVersion: 1, Definition: mustRaw(conn)})
		if codes(v)["omission"] != "error" {
			t.Fatalf("delete diagnostics %v", v.Diagnostics)
		}
	})
}

func TestValidationRecordFreshnessWindow(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	recordObservation(t, env, conn, obsSucceeded, conn.AccountIdentity, conn.AllowedScopes)

	payload := env.mustOK("connection.get", connGetIn{Scope: env.scope, ID: conn.ID})
	var out resourceOut
	env.decode(payload.Data, &out)
	if out.Resource.ValidationState != connStateValid {
		t.Fatalf("state after succeeded probe: %q", out.Resource.ValidationState)
	}
	if out.Resource.ValidatedAt == nil || out.Resource.ValidUntil == nil {
		t.Fatalf("freshness timestamps missing: %+v", out.Resource)
	}
	want := out.Resource.ValidatedAt.Add(validationFreshness)
	if !out.Resource.ValidUntil.Equal(want) {
		t.Fatalf("valid_until %s, want validated_at + %s = %s",
			out.Resource.ValidUntil, validationFreshness, want)
	}
	if !out.Resource.ValidUntil.After(env.clock.Now()) {
		t.Fatalf("valid_until %s is not in the future", out.Resource.ValidUntil)
	}
}

func TestValidationRecordObservationStored(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	recordObservation(t, env, conn, obsUnknown, "", nil)

	var stored int64
	if err := env.db.Write(env.ctx, env.actor, env.scope.toContract(), func(unit contract.Unit) error {
		return unit.QueryRowContext(env.ctx,
			`SELECT COUNT(*) FROM connections_validations WHERE connection_id = ? AND disposition = ?`,
			conn.ID, obsUnknown).Scan(&stored)
	}); err != nil {
		t.Fatalf("query observations: %v", err)
	}
	if stored != 1 {
		t.Fatalf("observation rows stored: %d, want 1", stored)
	}
	// Unknown dispositions record without moving the validation state.
	payload := env.mustOK("connection.get", connGetIn{Scope: env.scope, ID: conn.ID})
	var out resourceOut
	env.decode(payload.Data, &out)
	if out.Resource.ValidationState != connStateUnverified {
		t.Fatalf("unknown probe moved state to %q", out.Resource.ValidationState)
	}
}

func TestValidationRecordScopeSubsetEnforced(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	// The probe reports scopes beyond the connection's allowed set: the
	// observation cannot validate the connection.
	recordObservation(t, env, conn, obsSucceeded, conn.AccountIdentity, []string{"repo:read", "repo:admin"})
	payload := env.mustOK("connection.get", connGetIn{Scope: env.scope, ID: conn.ID})
	var out resourceOut
	env.decode(payload.Data, &out)
	if out.Resource.ValidationState != connStateInvalid {
		t.Fatalf("over-scoped probe left state %q, want invalid", out.Resource.ValidationState)
	}
}

// TestStaleValidationRefusesDispatch is the stale-validation local property:
// a once-valid connection stops resolving the moment its freshness window
// passes, without any writer persisting a terminal state.
func TestStaleValidationRefusesDispatch(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)
	recordObservation(t, env, conn, obsSucceeded, conn.AccountIdentity, conn.AllowedScopes)
	env.clock.Advance(validationFreshness + time.Minute)
	_ = env.expectFault("_connections.resolve", resolveIn{
		Scope: env.scope, Connection: wireRef{ID: conn.ID, Version: 2},
		Tool: wireRef{ID: toolRESTRead, Version: 1}, Destination: "api.github.com",
	}, contract.CodePrerequisiteMissing)

	// A fresh probe renews the window; the stale state did not need a
	// separate terminal writer to recover.
	conn.Version = 2
	recordObservation(t, env, conn, obsSucceeded, conn.AccountIdentity, conn.AllowedScopes)
	payload := env.mustOK("_connections.resolve", resolveIn{
		Scope: env.scope, Connection: wireRef{ID: conn.ID, Version: 3},
		Tool: wireRef{ID: toolRESTRead, Version: 1}, Destination: "api.github.com",
	})
	var out struct {
		Connection wireConnection `json:"connection"`
	}
	env.decode(payload.Data, &out)
	if out.Connection.ValidUntil == nil || !out.Connection.ValidUntil.After(env.clock.Now()) {
		t.Fatalf("renewed valid_until %v is not in the future", out.Connection.ValidUntil)
	}
}

func TestValidationRecordRefusals(t *testing.T) {
	env := newEnv(t)
	conn := env.seedConnection(nil)

	// usage carries the schema-required Usage object; refusals below exercise
	// handler logic, not envelope shape.
	usage := &wireUsage{Currency: "USD", Advisory: true}

	t.Run("stale-version", func(t *testing.T) {
		_ = env.expectFault("_connections.validation.record", validationRecordIn{
			ConnectionID: conn.ID, ExpectedVersion: 9,
			Observation: wireObservation{Disposition: obsFailed, Evidence: mustRaw(map[string]any{}), Usage: usage},
		}, contract.CodeStaleVersion)
	})

	t.Run("unknown-disposition", func(t *testing.T) {
		_ = env.expectFault("_connections.validation.record", validationRecordIn{
			ConnectionID: conn.ID, ExpectedVersion: 1,
			Observation: wireObservation{Disposition: "perhaps", Evidence: mustRaw(map[string]any{}), Usage: usage},
		}, contract.CodeInvalidInput)
	})

	t.Run("account-substitution-in-evidence-refused", func(t *testing.T) {
		f := env.expectFault("_connections.validation.record", validationRecordIn{
			ConnectionID: conn.ID, ExpectedVersion: 1,
			Observation: wireObservation{
				Disposition: obsSucceeded,
				Evidence: mustRaw(map[string]any{
					"account_identity": "acct-impersonated",
					"allowed_scopes":   conn.AllowedScopes,
				}),
				Usage: usage,
			},
		}, contract.CodeVerificationFailed)
		if !strings.Contains(f.Message, "substitution is refused") {
			t.Fatalf("refusal message %q does not name the substitution", f.Message)
		}
	})

	t.Run("unknown-connection", func(t *testing.T) {
		_ = env.expectFault("_connections.validation.record", validationRecordIn{
			ConnectionID: env.ids.New(), ExpectedVersion: 1,
			Observation: wireObservation{Disposition: obsFailed, Evidence: mustRaw(map[string]any{}), Usage: usage},
		}, contract.CodeNotFound)
	})
}
