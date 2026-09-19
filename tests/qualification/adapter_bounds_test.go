package qualification_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/adapters/github"
	"github.com/zatiti/zatiti/internal/adapters/httpread"
	"github.com/zatiti/zatiti/internal/adapters/serenity"
	"github.com/zatiti/zatiti/internal/contract"
)

// QUALIFICATION.adapter_bounds: each landed adapter, constructed through
// its public constructor exactly as cmd/zatiti constructs it, against a
// controlled provider simulator that counts physical requests. The
// simulator is the provider under fault injection, not a fake of the
// adapter: bounds, timeouts, refusals and lost acknowledgements are
// observed on real request bytes.

// ---------- shared fakes: clock, secrets, blobs ----------

type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Millisecond)
	return c.now
}

type memorySecrets struct{ refs map[string][]byte }

func (m memorySecrets) Put(_ context.Context, ref string, secret []byte) (string, error) {
	m.refs[ref] = secret
	return ref, nil
}

func (m memorySecrets) Get(_ context.Context, ref string) ([]byte, error) {
	s, ok := m.refs[ref]
	if !ok {
		return nil, &contract.Fault{Code: contract.CodeNotFound, Message: "no secret at " + ref}
	}
	return s, nil
}

func (m memorySecrets) Delete(_ context.Context, ref string) error {
	delete(m.refs, ref)
	return nil
}

// memoryBlobs is the staging store the adapters hand request records and
// responses to; it also serves the artifacts an action names.
type memoryBlobs struct {
	mu      sync.Mutex
	objects map[contract.Digest][]byte
	staged  int
}

func newMemoryBlobs() *memoryBlobs { return &memoryBlobs{objects: map[contract.Digest][]byte{}} }

func (b *memoryBlobs) put(data []byte) contract.Digest {
	d := contract.Hash(data)
	b.mu.Lock()
	b.objects[d] = data
	b.mu.Unlock()
	return d
}

func (b *memoryBlobs) Stage(_ context.Context, r io.Reader, _ int64) (string, contract.Digest, int64, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", "", 0, err
	}
	d := b.put(data)
	b.mu.Lock()
	b.staged++
	ref := fmt.Sprintf("staged-%d", b.staged)
	b.mu.Unlock()
	return ref, d, int64(len(data)), nil
}

func (b *memoryBlobs) Publish(context.Context, string, contract.Digest) error { return nil }

func (b *memoryBlobs) Open(_ context.Context, digest contract.Digest, offset, length int64) (io.ReadCloser, error) {
	b.mu.Lock()
	data, ok := b.objects[digest]
	b.mu.Unlock()
	if !ok {
		return nil, &contract.Fault{Code: contract.CodeArtifactFault, Message: "artifact bytes are unavailable"}
	}
	end := int64(len(data))
	if length > 0 && offset+length < end {
		end = offset + length
	}
	if offset > end {
		offset = end
	}
	return io.NopCloser(bytes.NewReader(data[offset:end])), nil
}

func (b *memoryBlobs) RemoveStaged(context.Context, string) error { return nil }

func (b *memoryBlobs) artifact(data string) map[string]any {
	return map[string]any{"id": string(contract.NewID()), "digest": string(b.put([]byte(data)))}
}

// ---------- profiles ----------

// capabilityEvidence is the profile's self-binding qualification record;
// profile_digest is the canonical digest of the profile without it.
func capabilityEvidence(adapterVersion, sourceRevision, protocol string, capabilities, limitations []string) map[string]any {
	return map[string]any{
		"artifact":          map[string]any{"id": string(contract.NewID()), "digest": string(contract.Hash([]byte("qualification-" + adapterVersion)))},
		"adapter_version":   adapterVersion,
		"source_revision":   sourceRevision,
		"protocol_revision": protocol,
		"profile_digest":    strings.Repeat("0", 64),
		"qualified_at":      "2026-09-19T00:00:00Z",
		"capabilities":      capabilities,
		"limitations":       limitations,
	}
}

// bindProfile fills capability_evidence.profile_digest exactly as the
// adapters compute it: the canonical JSON of the profile with
// capability_evidence removed.
func bindProfile(t *testing.T, profile map[string]any) json.RawMessage {
	t.Helper()
	evidence := profile["capability_evidence"].(map[string]any)
	stripped := map[string]any{}
	for k, v := range profile {
		if k != "capability_evidence" {
			stripped[k] = v
		}
	}
	raw, err := json.Marshal(stripped)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	canon, err := contract.Canonicalize(raw)
	if err != nil {
		t.Fatalf("canonicalize profile: %v", err)
	}
	evidence["profile_digest"] = string(contract.Hash(canon))
	out, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal bound profile: %v", err)
	}
	return out
}

func dispatch(t *testing.T, adapter string, action any, credentialRef string, deadline time.Time) contract.Dispatch {
	t.Helper()
	raw, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	return contract.Dispatch{
		OperationID: contract.NewID(), AttemptID: contract.NewID(), Generation: 1,
		Adapter: adapter, Action: raw, CredentialRef: credentialRef, Deadline: deadline,
	}
}

func sourceRevision() string {
	root, err := moduleRoot()
	if err != nil {
		return "unavailable"
	}
	rev, err := gitRevision(root)
	if err != nil {
		return "unavailable"
	}
	return rev
}

const validSHA = "0123456789abcdef0123456789abcdef01234567"

// ---------- httpread ----------

// TestAdapterBoundsHTTPRead: the production constructor builds its own
// pinned-dial transport, so its refusals are observed on the simulator's
// hit counter (zero hits) and its Contract on the frozen seam. A bounded
// read that reaches a public origin needs a public destination; none is
// dialed from here, so size and timeout bounds are recorded as not
// exercised through the production dialer.
func TestAdapterBoundsHTTPRead(t *testing.T) {
	c := beginCase(t, "QUALIFICATION.adapter_bounds/httpread", "QUALIFICATION",
		"Record exact adapter/source/protocol/profile versions and observed physical calls.",
		"Unsupported hard caps, safe-idempotency windows or memory operations remain unavailable or explicitly advisory, never inferred.")
	c.version("adapter_source_revision", sourceRevision())
	var hits atomic.Int32
	sim := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "public source")
	}))
	defer sim.Close()
	profile := bindProfile(t, map[string]any{
		"schema":              "zatiti.httpread/v1",
		"allowed_origins":     []string{sim.URL},
		"max_bytes":           1024,
		"timeout_seconds":     5,
		"max_redirects":       0,
		"allowed_media_types": []string{"text/plain"},
		"capability_evidence": capabilityEvidence("qualification-httpread", sourceRevision(), "http/1.1", []string{"read"}, []string{"max_redirects fixed at 0"}),
	})
	a, err := httpread.New(contract.AdapterDependencies{HTTP: &http.Client{}, Clock: &stepClock{now: time.Now().UTC()}, Blobs: newMemoryBlobs()}, profile)
	if err != nil {
		c.fail("httpread.New: %v", err)
	}
	c.attach("contract", json.RawMessage(a.Contract()))
	var doc struct {
		ProfileSchema json.RawMessage `json:"profile_schema"`
	}
	if err := json.Unmarshal(a.Contract(), &doc); err != nil || !strings.Contains(string(doc.ProfileSchema), `"max_redirects"`) {
		c.fail("httpread Contract() does not publish the frozen profile schema: %v", err)
	}
	if !strings.Contains(string(doc.ProfileSchema), `"const":0`) && !strings.Contains(string(doc.ProfileSchema), `"maximum":0`) {
		c.observe("profile schema does not pin max_redirects to 0 by const/maximum; the adapter enforces it at load time instead")
	}

	read := map[string]any{
		"schema": "zatiti.httpread.action/v1", "kind": "read", "url": sim.URL + "/source",
		"method": "GET", "headers": []any{}, "expected_media_type": "text/plain",
	}
	deadline := time.Now().Add(time.Minute)

	// A loopback destination is refused before any bytes move, even though
	// the profile allows the origin: private addresses need an explicit
	// installation-authorized binding the production dialer does not have.
	_, err = a.Invoke(context.Background(), dispatch(t, "httpread", read, "", deadline))
	var f *contract.Fault
	if !errors.As(err, &f) || f.Code != contract.CodePermissionDenied {
		c.fail("loopback read: err=%v, want permission_denied", err)
	}
	if hits.Load() != 0 {
		c.fail("the simulator received %d request(s) for a refused private destination", hits.Load())
	}
	c.observe("read of an allowed origin on a loopback address refused %s with 0 physical requests", f.Code)

	// A non-allowed origin and a non-allowed media type are refused the
	// same way, still with zero physical requests.
	other := map[string]any{}
	for k, v := range read {
		other[k] = v
	}
	other["url"] = "https://example.invalid/x"
	_, err = a.Invoke(context.Background(), dispatch(t, "httpread", other, "", deadline))
	if !errors.As(err, &f) || f.Code != contract.CodePermissionDenied {
		c.fail("foreign origin: err=%v", err)
	}
	other["url"] = read["url"]
	other["expected_media_type"] = "application/octet-stream"
	_, err = a.Invoke(context.Background(), dispatch(t, "httpread", other, "", deadline))
	if !errors.As(err, &f) {
		c.fail("disallowed media type: err=%v", err)
	}
	if hits.Load() != 0 {
		c.fail("refused reads reached the simulator %d time(s)", hits.Load())
	}
	c.observe("foreign origin and disallowed media type refused (%s) with 0 physical requests", f.Code)
	c.observe("size and timeout bounds through the production dialer need a public destination and are not exercised here; the adapter package's own suite exercises them on an injected transport")
}

// ---------- github ----------

func githubProfile(t *testing.T, apiBase string) json.RawMessage {
	t.Helper()
	automation := map[string]any{
		"allowed_workflows": []string{}, "allowed_deployment_environments": []string{},
		"allow_external_notifications": false, "allow_automatic_merge": false,
		"unknown_automation": "deny", "evidence": []any{},
	}
	return bindProfile(t, map[string]any{
		"schema":               "zatiti.github/v1",
		"api_base":             apiBase,
		"allowed_repositories": []map[string]any{{"owner": "acme", "name": "widgets"}},
		"allowed_actions":      []string{"read_repository", "create_branch", "push_commit", "open_pull_request", "merge_pull_request"},
		"max_response_bytes":   1 << 20,
		"timeout_seconds":      30,
		"idempotency_profile": map[string]any{
			"mode": "none", "retention_seconds": 0, "equivalence_fields": []string{}, "evidence": []any{},
		},
		"automation_constraints": automation,
		"capability_evidence":    capabilityEvidence("qualification-github", sourceRevision(), "2022-11-28", []string{"read_repository"}, []string{"no qualified idempotency key"}),
	})
}

// githubSimulator is the provider under fault injection: it counts every
// physical request and answers by path.
type githubSimulator struct {
	hits  atomic.Int32
	mode  atomic.Value // string
	srv   *httptest.Server
	sleep time.Duration
}

func newGitHubSimulator() *githubSimulator {
	s := &githubSimulator{}
	s.mode.Store("ok")
	s.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		switch s.mode.Load().(string) {
		case "reset":
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, err := hj.Hijack()
				if err == nil {
					_ = conn.Close()
				}
			}
			return
		case "sleep":
			time.Sleep(s.sleep)
			w.WriteHeader(http.StatusOK)
			return
		case "unprocessable":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"message":"Reference already exists"}`)
			return
		case "notfound":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"Not Found"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets":
			_, _ = io.WriteString(w, `{"full_name":"acme/widgets","default_branch":"main"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widgets/pulls":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"number":42,"html_url":"https://github.com/acme/widgets/pull/42","head":{"sha":"`+validSHA+`"},"base":{"sha":"`+validSHA+`"}}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"Not Found"}`)
		}
	}))
	return s
}

func TestAdapterBoundsGitHub(t *testing.T) {
	c := beginCase(t, "QUALIFICATION.adapter_bounds/github", "QUALIFICATION",
		"Record exact adapter/source/protocol/profile versions and observed physical calls.",
		"Exactly one physical request per claimed attempt; timeouts and resets after bytes are unknown, never invented success or safe nonexecution; eventual not-found never proves nonexecution.")
	c.version("adapter_source_revision", sourceRevision())
	sim := newGitHubSimulator()
	defer sim.srv.Close()
	blobs := newMemoryBlobs()
	secrets := memorySecrets{refs: map[string][]byte{"cred-qual": []byte("ghp_qualification_token_" + string(contract.NewID())[:8])}}
	// The simulator's client trusts its own certificate; the adapter reuses
	// that transport exactly as it reuses the shared production one.
	a, err := github.New(contract.AdapterDependencies{
		HTTP: sim.srv.Client(), Secrets: secrets, Clock: &stepClock{now: time.Now().UTC()}, Blobs: blobs,
	}, githubProfile(t, sim.srv.URL))
	if err != nil {
		c.fail("github.New: %v", err)
	}
	c.attach("contract_bytes", len(a.Contract()))
	repo := map[string]any{"owner": "acme", "name": "widgets"}
	automation := map[string]any{
		"allowed_workflows": []string{}, "allowed_deployment_environments": []string{},
		"allow_external_notifications": false, "allow_automatic_merge": false,
		"unknown_automation": "deny", "evidence": []any{},
	}
	preflight := []any{blobs.artifact("preflight evidence")}
	deadline := func() time.Time { return time.Now().Add(time.Minute) }
	invoke := func(mode string, action map[string]any, deadline time.Time) (contract.Observation, int32, error) {
		sim.mode.Store(mode)
		before := sim.hits.Load()
		obs, err := a.Invoke(context.Background(), dispatch(t, "github", action, "cred-qual", deadline))
		return obs, sim.hits.Load() - before, err
	}

	// 1. One read, one physical request, authoritative success.
	readAction := map[string]any{"schema": "zatiti.github.action/v1", "repository": repo, "kind": "read_repository", "resource": "metadata"}
	obs, n, err := invoke("ok", readAction, deadline())
	if err != nil || obs.Disposition != contract.DispositionSucceeded || n != 1 {
		c.fail("read_repository: disposition=%s evidence=%s err=%v requests=%d", obs.Disposition, obs.Evidence, err, n)
	}
	c.observe("read_repository metadata: disposition %s after exactly %d physical request", obs.Disposition, n)
	if strings.Contains(string(obs.Evidence), "ghp_qualification_token") {
		c.fail("the observation evidence carries the provider token")
	}

	// 2. A consequential mutation: opening a pull request is one request
	// and its 201 is interpreted as the qualified contract says, never as
	// anything beyond what the response established.
	openPR := map[string]any{
		"schema": "zatiti.github.action/v1", "repository": repo, "kind": "open_pull_request",
		"head_branch": "feature/x", "head_sha": validSHA, "base_branch": "main", "base_sha": validSHA,
		"title": blobs.artifact("Qualification PR"), "body": blobs.artifact("body"), "patch": blobs.artifact("patch"),
		"draft": false, "automation_constraints": automation, "preflight_evidence": preflight,
	}
	obs, n, err = invoke("ok", openPR, deadline())
	if err != nil || n != 1 {
		c.fail("open_pull_request: err=%v requests=%d", err, n)
	}
	c.observe("open_pull_request: disposition %s, provider reference %q, %d physical request", obs.Disposition, obs.ProviderReference, n)

	// 3. Connection reset after the request was written: unknown, one
	// request, no hidden retry.
	push := map[string]any{
		"schema": "zatiti.github.action/v1", "repository": repo, "kind": "push_commit",
		"branch": "feature/x", "expected_head_sha": validSHA, "prepared_commit_sha": validSHA, "base_sha": validSHA,
		"patch": blobs.artifact("patch"), "content_artifacts": []any{}, "force": false,
		"automation_constraints": automation, "preflight_evidence": preflight,
	}
	obs, n, err = invoke("reset", push, deadline())
	if err != nil || obs.Disposition != contract.DispositionUnknown || n != 1 {
		c.fail("push_commit under reset: disposition=%s evidence=%s err=%v requests=%d, want unknown after exactly 1", obs.Disposition, obs.Evidence, err, n)
	}
	c.observe("push_commit with the connection reset mid-response: disposition %s, %d physical request (no hidden retry)", obs.Disposition, n)

	// 4. The dispatch deadline passes after the request bytes were sent
	// (provider answers after 4s, deadline in 1s): unknown, one request.
	sim.sleep = 4 * time.Second
	obs, n, err = invoke("sleep", push, time.Now().Add(time.Second))
	if err != nil || obs.Disposition != contract.DispositionUnknown || n != 1 {
		c.fail("push_commit under timeout: disposition=%s evidence=%s err=%v requests=%d, want unknown after exactly 1", obs.Disposition, obs.Evidence, err, n)
	}
	c.observe("push_commit past its dispatch deadline with the provider still answering: disposition %s, %d physical request", obs.Disposition, n)

	// 5. A synchronous provider refusal is an authoritative failure.
	obs, n, err = invoke("unprocessable", push, deadline())
	if err != nil || obs.Disposition != contract.DispositionFailed || obs.ConfirmedAt == nil || n != 1 {
		c.fail("push_commit under 422: disposition=%s evidence=%s err=%v requests=%d", obs.Disposition, obs.Evidence, err, n)
	}
	c.observe("push_commit refused 422: disposition %s confirmed at %s", obs.Disposition, obs.ConfirmedAt.UTC().Format(time.RFC3339))

	// 6. Reconcile against eventual not-found stays unknown.
	sim.mode.Store("notfound")
	before := sim.hits.Load()
	obs, err = a.Reconcile(context.Background(), dispatch(t, "github", push, "cred-qual", deadline()))
	n = sim.hits.Load() - before
	if err != nil || obs.Disposition != contract.DispositionUnknown || n != 1 {
		c.fail("reconcile under 404: disposition=%s evidence=%s err=%v requests=%d, want unknown after 1 bounded read", obs.Disposition, obs.Evidence, err, n)
	}
	c.observe("reconcile answered 404: disposition %s (not-found never proves nonexecution), %d bounded read", obs.Disposition, n)

	// 7. The qualified idempotency profile is none: no safe-retry window is
	// claimed by this profile.
	c.observe("profile idempotency_profile.mode=none: no automatic redispatch window is claimed")
	c.attach("simulator_total_requests", sim.hits.Load())
}

// ---------- serenity ----------

func TestAdapterBoundsSerenity(t *testing.T) {
	c := beginCase(t, "QUALIFICATION.adapter_bounds/serenity", "QUALIFICATION",
		"Record exact adapter/source/protocol/profile versions and observed physical calls.",
		"Memory operations without a qualified upstream call path remain unavailable, and a lost acknowledgement retains unknown when no authoritative lookup exists.")
	c.version("adapter_source_revision", sourceRevision())
	var outward atomic.Int32
	probe := probeTransport{n: &outward}
	brain := contract.NewID()
	evidence := capabilityEvidence("zatiti-serenity-adapter/1", "f5a5154e1c4d808e10b495fca3bd50d842f0aa92", "memory_verbs/1+mcp/2025-11-25", []string{}, []string{"no operation is dispatchable at this pin; see PROTOCOL.md"})
	enforcement := capabilityEvidence("zatiti-serenity-adapter/1", "f5a5154e1c4d808e10b495fca3bd50d842f0aa92", "memory_verbs/1+mcp/2025-11-25", []string{}, []string{"unsupported"})
	profile := bindProfile(t, map[string]any{
		"schema":  "zatiti.serenity/v1",
		"version": "v0.1.1-240-gf5a5154",
		"commit":  "f5a5154e1c4d808e10b495fca3bd50d842f0aa92",
		"brain_mappings": []map[string]any{{
			"brain_id": string(brain), "endpoint": "https://serenity.invalid/mcp", "root_ref": "brain:qualification",
			"writer_owner": "qualification-writer", "classification": "internal",
		}},
		"supported_operations": []string{},
		"enforcement": map[string]any{
			"cost": "unsupported", "disclosure": "unsupported",
			"maximum_cost":          map[string]any{"currency": "USD", "micro_units": 5000000},
			"provider_destinations": []string{"https://serenity.invalid/mcp"},
			"classifications":       []string{"public", "internal"},
			"evidence":              enforcement,
		},
		"command_status_lookup": map[string]any{
			"mode": "unsupported", "retention_seconds": 0, "command_identity_supported": false, "evidence": []any{},
		},
		"freshness": map[string]any{
			"source_revision_supported": false, "index_revision_supported": false,
			"minimum_freshness_enforceable": false, "read_facade": "unsupported", "evidence": []any{},
		},
		"timeout_seconds": 30,
		"max_bytes":       1 << 20,
		"backup_revision_protocol": map[string]any{
			"mode": "unsupported", "protocol_profile": "none",
			"immutable_revision_export": false, "restore_supported": false, "evidence": []any{},
		},
		"capability_evidence": evidence,
	})
	a, err := serenity.New(contract.AdapterDependencies{
		HTTP: &http.Client{Transport: probe}, Secrets: memorySecrets{refs: map[string][]byte{"cred-mem": []byte("x")}},
		Clock: &stepClock{now: time.Now().UTC()}, Blobs: countingBlobs{n: &outward},
	}, profile)
	if err != nil {
		c.fail("serenity.New: %v", err)
	}
	var doc struct {
		CapabilityReport json.RawMessage `json:"capability_report"`
	}
	if err := json.Unmarshal(a.Contract(), &doc); err != nil || len(doc.CapabilityReport) == 0 {
		c.fail("serenity Contract() carries no capability_report: %v", err)
	}
	c.attach("capability_report", doc.CapabilityReport)
	if strings.Contains(string(doc.CapabilityReport), `"supported":true`) {
		c.fail("the capability report claims a supported operation at this pin")
	}

	recall := map[string]any{
		"schema": "zatiti.serenity.action/v1", "brain_id": string(brain), "adapter_command_id": string(contract.NewID()),
		"kind": "recall", "query": "what is qualified", "minimum_freshness": "2025-12-31T00:00:00Z", "max_claims": 5,
		"maximum_cost": map[string]any{"currency": "USD", "micro_units": 1000}, "allowed_provider_destinations": []string{"https://serenity.invalid/mcp"},
		"classification": "internal",
	}
	_, err = a.Invoke(context.Background(), dispatch(t, "serenity", recall, "cred-mem", time.Now().Add(time.Minute)))
	var f *contract.Fault
	if !errors.As(err, &f) || f.Code != contract.CodeCapabilityUnsupported {
		c.fail("recall: err=%v, want capability_unsupported", err)
	}
	c.observe("recall refused %s naming the pinned upstream gap: %s", f.Code, f.Message)
	c.attach("recall_refusal_details", json.RawMessage(f.Details))
	_, err = a.Reconcile(context.Background(), dispatch(t, "serenity", recall, "cred-mem", time.Now().Add(time.Minute)))
	if !errors.As(err, &f) || f.Code != contract.CodeCapabilityUnsupported {
		c.fail("reconcile: err=%v, want capability_unsupported", err)
	}
	var details struct {
		OriginalOutcome string   `json:"original_outcome"`
		Missing         []string `json:"missing"`
	}
	if err := json.Unmarshal(f.Details, &details); err != nil || details.OriginalOutcome != "retained_unknown" {
		c.fail("reconcile refusal details %s do not leave the original effect unknown", f.Details)
	}
	c.attach("reconcile_refusal_details", json.RawMessage(f.Details))
	c.observe("reconcile after a lost acknowledgement refused %s naming %v; the original effect stays %s, nothing was repeated", f.Code, details.Missing, details.OriginalOutcome)
	if outward.Load() != 0 {
		c.fail("the serenity adapter reached outside itself %d time(s)", outward.Load())
	}
	c.observe("0 HTTP requests and 0 blob operations across invoke and reconcile")
}

type probeTransport struct{ n *atomic.Int32 }

func (p probeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	p.n.Add(1)
	return nil, errors.New("the serenity adapter must not send a request")
}

type countingBlobs struct{ n *atomic.Int32 }

func (b countingBlobs) Stage(context.Context, io.Reader, int64) (string, contract.Digest, int64, error) {
	b.n.Add(1)
	return "", "", 0, errors.New("unexpected stage")
}

func (b countingBlobs) Publish(context.Context, string, contract.Digest) error {
	b.n.Add(1)
	return errors.New("unexpected publish")
}

func (b countingBlobs) Open(context.Context, contract.Digest, int64, int64) (io.ReadCloser, error) {
	b.n.Add(1)
	return nil, errors.New("unexpected open")
}

func (b countingBlobs) RemoveStaged(context.Context, string) error {
	b.n.Add(1)
	return errors.New("unexpected remove")
}

// ---------- responses ----------

// TestAdapterBoundsResponses: the hosted model adapter has no code on this
// tree, so nothing can be constructed or bound; the case is not run and
// every hosted-model claim stays blocked.
func TestAdapterBoundsResponses(t *testing.T) {
	c := beginCase(t, "QUALIFICATION.adapter_bounds/responses", "QUALIFICATION",
		"Record exact adapter/source/protocol/profile versions and observed physical calls for the hosted Responses adapter.")
	root, err := moduleRoot()
	if err != nil {
		c.fail("%v", err)
	}
	dir := filepath.Join(root, "internal", "adapters", "responses")
	entries, err := os.ReadDir(dir)
	if err != nil {
		c.notRun("internal/adapters/responses is absent: %v", err)
	}
	goFiles := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".go") {
			goFiles++
		}
	}
	if goFiles == 0 {
		c.notRun("internal/adapters/responses holds no Go source (%d entries, only the brief); no adapter exists to qualify and no endpoint/model/price is pinned", len(entries))
	}
	c.notRun("internal/adapters/responses has %d Go files but this harness pins no qualified endpoint, model or price to bind them against", goFiles)
}
