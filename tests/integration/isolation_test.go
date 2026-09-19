package integration_test

import (
	"context"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// agent is a second authenticated principal with its own custodied
// credential.
type agent struct {
	id      contract.ID
	version int64
	actor   contract.Actor
	token   string
}

// newAgent creates a client_agent principal scoped as given and provisions a
// synthetic credential the fixture custodies as the local trusted helper.
func (f *fixture) newAgent(name string, scope contract.Scope) agent {
	f.t.Helper()
	created := f.must(f.owner, "principal.create", "agent-"+name, map[string]any{
		"scope": f.scope(), "definition": map[string]any{
			"kind": "client_agent", "name": name, "scope": scope, "revoked": false},
	})
	var out struct {
		Resource struct {
			ID      contract.ID `json:"id"`
			Version int64       `json:"version"`
		} `json:"resource"`
	}
	decode(f.t, created.Data, &out)
	token := "zat_synthetic_agent_credential_" + name
	ref, err := f.secrets.Put(context.Background(), "integration/agent/"+name, []byte(token))
	if err != nil {
		f.t.Fatalf("custody agent credential: %v", err)
	}
	f.must(f.owner, "credential.provision", "agent-credential-"+name, map[string]any{
		"scope": f.scope(), "principal_id": out.Resource.ID, "store_ref": ref,
	})
	return agent{id: out.Resource.ID, version: out.Resource.Version, actor: f.authenticate([]byte(token)), token: token}
}

// seedOrganizationState creates one task in the root organization and
// returns the identities a foreign caller will probe.
func (f *fixture) seedOrganizationState() (scope contract.Scope, task, event contract.ID, binding memoryBinding) {
	f.t.Helper()
	org, chief := f.rootOrganization()
	scope = contract.Scope{InstallationID: f.installationID, OrganizationID: org}
	created := f.must(f.owner, "task.create", "seed-task", map[string]any{
		"scope": scope, "definition": f.taskDefinition(scope, f.owner.PrincipalID, chief, unconfiguredCurrency),
	})
	var t struct {
		Resource struct {
			ID contract.ID `json:"id"`
		} `json:"resource"`
	}
	decode(f.t, created.Data, &t)

	// Events of the organization scope: the task admission just emitted them.
	events := f.must(f.owner, "event.list", "", map[string]any{"scope": scope, "limit": 500})
	var el struct {
		Items []struct {
			ID contract.ID `json:"id"`
		} `json:"items"`
	}
	decode(f.t, events.Data, &el)
	bindings := f.must(f.owner, "memory.binding.list", "", map[string]any{"scope": f.scope()})
	var bl struct {
		Items []memoryBinding `json:"items"`
	}
	decode(f.t, bindings.Data, &bl)
	if len(el.Items) == 0 || len(bl.Items) == 0 {
		f.t.Fatalf("seed state is incomplete: %d events, %d memory bindings", len(el.Items), len(bl.Items))
	}
	return scope, t.Resource.ID, el.Items[len(el.Items)-1].ID, bl.Items[0]
}

// memoryBinding is a binding and the scope it lives in.
type memoryBinding struct {
	ID    contract.ID    `json:"id"`
	Scope contract.Scope `json:"scope"`
}

// probes are the reads a foreign caller attempts against another
// organization's task, event, artifact listing and memory binding.
func probes(scope contract.Scope, task, event contract.ID, binding memoryBinding) []call {
	return []call{
		{name: "task.get", op: "task.get", input: map[string]any{"scope": scope, "id": task}},
		{name: "task.list", op: "task.list", input: map[string]any{"scope": scope}},
		{name: "event.get", op: "event.get", input: map[string]any{"scope": scope, "id": event}},
		{name: "event.list", op: "event.list", input: map[string]any{"scope": scope}},
		{name: "artifact.list", op: "artifact.list", input: map[string]any{"scope": scope}},
		{name: "memory.binding.get", op: "memory.binding.get", input: map[string]any{"scope": binding.Scope, "id": binding.ID}},
		{name: "memory.binding.list", op: "memory.binding.list", input: map[string]any{"scope": scope}},
	}
}

// refusedWithoutData asserts a probe disclosed nothing: a permission or
// not-found refusal and no data.
func refusedWithoutData(t *testing.T, who, name string, res contract.Result, err error) {
	t.Helper()
	code := faultCode(err)
	if code != contract.CodePermissionDenied && code != contract.CodeNotFound {
		t.Errorf("%s: %s returned code %q data %s, want a permission or not-found refusal", who, name, code, res.Data)
	}
	if len(res.Data) != 0 && string(res.Data) != "null" {
		t.Errorf("%s: %s disclosed data with its refusal: %s", who, name, res.Data)
	}
}

// TestPrincipalWithoutGrantsReadsNothing (Z01 scoped identities, Z05 empty
// bindings): an authenticated principal scoped to a different organization,
// holding no grant, cannot read the root organization's task, events,
// artifacts or memory bindings through the real application, under the
// owning scope, the installation scope or its own scope. The owner reads
// every one of them, so the refusals are authorization, not absence.
func TestPrincipalWithoutGrantsReadsNothing(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	scope, task, event, binding := f.seedOrganizationState()
	for _, c := range probes(scope, task, event, binding) {
		if _, err := f.invoke(f.owner, c.op, "", c.input); err != nil {
			t.Fatalf("owner %s: %v", c.name, err)
		}
	}

	foreignScope := contract.Scope{InstallationID: f.installationID, OrganizationID: "00000000-0000-4000-8000-0000000000f0"}
	foreign := f.newAgent("foreign", foreignScope)
	if foreign.actor.Kind != contract.KindClientAgent || foreign.actor.PrincipalID != foreign.id {
		t.Fatalf("foreign agent authenticated as %+v", foreign.actor)
	}
	for _, s := range []contract.Scope{scope, f.scope(), foreignScope} {
		for _, c := range probes(s, task, event, binding) {
			res, err := f.invoke(foreign.actor, c.op, "", c.input)
			refusedWithoutData(t, "foreign agent", c.name, res, err)
		}
	}
	// A refused caller also cannot mutate: no task appears in either scope.
	_, err := f.invoke(foreign.actor, "task.create", "foreign-task", map[string]any{
		"scope": scope, "definition": f.taskDefinition(scope, foreign.id, task, unconfiguredCurrency),
	})
	if faultCode(err) != contract.CodePermissionDenied {
		t.Errorf("foreign task.create: %v, want permission_denied", err)
	}
	if n := f.count("task.list", map[string]any{"scope": f.scope()}); n != 1 {
		t.Errorf("task count %d after the refused foreign mutation, want 1", n)
	}
}

// TestForeignOrganizationGrantDoesNotReachPeerOrganization (Z01
// cross-organization reference, Z17 inherited denial): a principal granted
// read capabilities in a second organization cannot read the root
// organization's task, events, artifacts or memory bindings, while it can
// read its own organization. The whole path is real: a standing policy
// taking grant.create out of the default review class and a second
// organization with its chief are each staged, planned, refused
// review_required naming the candidate digest, decided by the owner and
// applied; then the grant is created and exercised.
func TestForeignOrganizationGrantDoesNotReachPeerOrganization(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	scope, task, event, binding := f.seedOrganizationState()

	policy := f.activate("grant-policy", "policy.create", map[string]any{"definition": map[string]any{
		"scope": f.scope(), "rules": []any{map[string]any{
			"capability": "grant.create", "effect": "local", "destinations": []string{},
			"decision": "allow", "human_required": false, "conditions": map[string]any{}}},
	}})
	if policy.Status != contract.StatusCompleted {
		t.Fatalf("grant policy activation status %q", policy.Status)
	}
	// organization.create stages the organization and its designated chief
	// as one bundle (frozen input requires scope, definition and chief). The
	// chief is a real worker definition with no execution profile or limits,
	// like the bootstrap chief: it exists and cannot run paid work.
	created := f.activate("org-b", "organization.create", map[string]any{
		"definition": map[string]any{"key": "research", "name": "Research"},
		"chief": map[string]any{
			"key": "research-chief", "name": "Research Chief",
			"purpose":        "Chief of the research organization",
			"instructions":   "Coordinate research tasks within the research organization only.",
			"skill_versions": []any{}, "bindings": []string{}, "profile": nil, "limits": nil,
		},
	})
	if created.Status != contract.StatusCompleted {
		t.Fatalf("organization B activation status %q", created.Status)
	}
	orgs := f.must(f.owner, "organization.list", "", map[string]any{"scope": f.scope()})
	var ol struct {
		Items []struct {
			ID  contract.ID `json:"id"`
			Key string      `json:"key"`
		} `json:"items"`
	}
	decode(t, orgs.Data, &ol)
	var orgB contract.ID
	for _, o := range ol.Items {
		if o.Key == "research" {
			orgB = o.ID
		}
	}
	if orgB == "" {
		t.Fatalf("organization B is not effective after apply: %s", orgs.Data)
	}
	foreignScope := contract.Scope{InstallationID: f.installationID, OrganizationID: orgB}
	foreign := f.newAgent("granted", foreignScope)
	f.must(f.owner, "grant.create", "foreign-grant", map[string]any{
		"scope": f.scope(), "definition": map[string]any{
			"principal_id": foreign.id, "scope": foreignScope,
			"capabilities": []string{"task.get", "task.list", "event.get", "event.list", "artifact.list", "memory.binding.get", "memory.binding.list"},
			"destinations": []string{}, "denied": false},
	})
	// Its own organization is readable.
	if _, err := f.invoke(foreign.actor, "task.list", "", map[string]any{"scope": foreignScope}); err != nil {
		t.Fatalf("granted agent reading its own organization: %v", err)
	}
	// The root organization is not, under any scope.
	for _, s := range []contract.Scope{scope, f.scope()} {
		for _, c := range probes(s, task, event, binding) {
			res, err := f.invoke(foreign.actor, c.op, "", c.input)
			refusedWithoutData(t, "granted foreign agent", c.name, res, err)
		}
	}
}

// TestRevokedPrincipalIsDeniedImmediately (Z01 revoked principal): after
// principal.revoke the credential stops authenticating and an actor that
// authenticated earlier is refused on its very next operation.
func TestRevokedPrincipalIsDeniedImmediately(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	a := f.newAgent("revoked", f.scope())
	f.must(f.owner, "principal.revoke", "revoke-1", map[string]any{
		"scope": f.scope(), "id": a.id, "expected_version": a.version,
	})
	if _, err := f.app.Authenticate(context.Background(), []byte(a.token)); err == nil {
		t.Error("a revoked principal's credential still authenticates")
	}
	res, err := f.invoke(a.actor, "installation.status", "", map[string]any{"scope": f.scope()})
	refusedWithoutData(t, "revoked agent", "installation.status", res, err)
}

// TestUnknownCredentialIsRefusedGenerically (Z01 unauthorized credential
// access): an unknown credential and an empty one fail the same generic way
// and never resolve an actor.
func TestUnknownCredentialIsRefusedGenerically(t *testing.T) {
	t.Parallel()
	f := newBootstrappedFixture(t)
	_, unknown := f.app.Authenticate(context.Background(), []byte("zat_synthetic_unknown_credential"))
	_, empty := f.app.Authenticate(context.Background(), nil)
	if unknown == nil || empty == nil {
		t.Fatalf("authentication accepted an unknown (%v) or empty (%v) credential", unknown, empty)
	}
	if faultCode(unknown) != faultCode(empty) || errAs(unknown) == nil || errAs(unknown).Message != errAs(empty).Message {
		t.Fatalf("authentication failures are distinguishable: %v vs %v", unknown, empty)
	}
}
