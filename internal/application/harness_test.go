package application

// Test harness. The fakes here are owner stand-ins: they own real SQLite
// tables (created through storage migrations) and speak only JSON across the
// dispatch boundary, so every application <-> owner exchange in these tests
// exercises the exact wire shapes the dispatcher emits and consumes.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// ---- deterministic infrastructure fakes ----

// fakeIDs mints UUIDv4-shaped identities from a counter so test runs are
// reproducible and reopened environments never collide with earlier ids.
type fakeIDs struct {
	mu sync.Mutex
	n  uint64
}

func newFakeIDs(start uint64) *fakeIDs { return &fakeIDs{n: start} }

func (f *fakeIDs) New() contract.ID {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	return contract.ID(fmt.Sprintf("00000000-0000-4000-8000-%012d", f.n))
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// fakeAuth resolves credentials to actors. Sessions survive dispatch-time
// revocation: authentication resolved the credential once at admission and
// revalidateAuthority rechecks the principal on every dispatch.
type fakeAuth struct {
	creds map[string]contract.Actor
	certs map[contract.Digest]contract.Actor
}

func (f *fakeAuth) Authenticate(ctx context.Context, reader contract.Reader, credential []byte) (contract.Actor, error) {
	actor, ok := f.creds[string(credential)]
	if !ok {
		return contract.Actor{}, &contract.Fault{Code: contract.CodePermissionDenied, Message: "credential is not recognized"}
	}
	return actor, nil
}

func (f *fakeAuth) AuthenticateCertificate(ctx context.Context, reader contract.Reader, fingerprint contract.Digest) (contract.Actor, error) {
	actor, ok := f.certs[fingerprint]
	if !ok {
		return contract.Actor{}, &contract.Fault{Code: contract.CodePermissionDenied, Message: "certificate fingerprint is not recognized"}
	}
	return actor, nil
}

// trackingDB counts transactions in flight so the local IO fakes can prove
// Perform never runs inside one.
type trackingDB struct {
	contract.Database
	active atomic.Int64
}

func (t *trackingDB) Read(ctx context.Context, actor contract.Actor, scope contract.Scope, fn func(contract.Unit) error) error {
	t.active.Add(1)
	defer t.active.Add(-1)
	return t.Database.Read(ctx, actor, scope, fn)
}

func (t *trackingDB) Write(ctx context.Context, actor contract.Actor, scope contract.Scope, fn func(contract.Unit) error) error {
	t.active.Add(1)
	defer t.active.Add(-1)
	return t.Database.Write(ctx, actor, scope, fn)
}

// ---- fake owner wire shapes (independent of the application mirrors) ----

type fakeScope struct {
	InstallationID string `json:"installation_id"`
	OrganizationID string `json:"organization_id,omitempty"`
	ProjectID      string `json:"project_id,omitempty"`
}

type fakeFault struct {
	Code      string          `json:"code"`
	Message   string          `json:"message"`
	Retryable bool            `json:"retryable"`
	Details   json.RawMessage `json:"details,omitempty"`
}

// fakeCommand mirrors $defs/Command as the evidence owner stores it.
type fakeCommand struct {
	ID               string      `json:"id"`
	PrincipalID      string      `json:"principal_id"`
	Operation        string      `json:"operation"`
	OperationVersion int64       `json:"operation_version"`
	SubmissionKey    string      `json:"submission_key"`
	RequestDigest    string      `json:"request_digest"`
	Status           string      `json:"status"`
	Data             interface{} `json:"data"`
	ErrorCode        string      `json:"error_code"`
	Result           *fakeResult `json:"result"`
}

// fakeResult mirrors $defs/Result.
type fakeResult struct {
	Schema     string          `json:"schema"`
	CommandID  string          `json:"command_id"`
	Status     string          `json:"status"`
	Data       json.RawMessage `json:"data"`
	Error      *fakeFault      `json:"error"`
	NextCursor *string         `json:"next_cursor"`
}

type fakeBeginOut struct {
	CommandID string       `json:"command_id"`
	Existing  *fakeCommand `json:"existing,omitempty"`
}

type fakeFinishIn struct {
	CommandID string      `json:"command_id"`
	Result    *fakeResult `json:"result"`
}

// fakeAuthority mirrors $defs/Authority as the identity owner returns it.
type fakeAuthorityOut struct {
	Resource struct {
		Principal    fakePrincipal `json:"principal"`
		Grants       []fakeGrant   `json:"grants"`
		Restrictions []string      `json:"restrictions"`
	} `json:"resource"`
}

type fakePrincipal struct {
	ID      string    `json:"id"`
	Version int64     `json:"version"`
	Kind    string    `json:"kind"`
	Name    string    `json:"name"`
	Scope   fakeScope `json:"scope"`
	Revoked bool      `json:"revoked"`
}

type fakeGrant struct {
	ID           string    `json:"id"`
	Version      int64     `json:"version"`
	PrincipalID  string    `json:"principal_id"`
	Scope        fakeScope `json:"scope"`
	Capabilities []string  `json:"capabilities"`
	Destinations []string  `json:"destinations"`
	Denied       bool      `json:"denied"`
}

type fakePolicyOut struct {
	Resource struct {
		Decision     string            `json:"decision"`
		Reasons      []string          `json:"reasons"`
		Requirements []fakeRequirement `json:"requirements"`
	} `json:"resource"`
}

type fakeRequirement struct {
	ActionDigest       string   `json:"action_digest"`
	HumanRequired      bool     `json:"human_required"`
	EligiblePrincipals []string `json:"eligible_principals"`
	ExpiresAt          *string  `json:"expires_at"`
	SeparateProposer   bool     `json:"separate_proposer"`
}

// ---- fake owner modules ----

// fakeOp pairs a descriptor with its handler the way a registry would.
type fakeOp struct {
	desc contract.Descriptor
	h    contract.Handler
}

type fakeCatalog struct {
	ops map[string]fakeOp
	io  map[string]contract.LocalIO
}

func (c *fakeCatalog) Lookup(id string, version int64) (contract.Descriptor, contract.Handler, error) {
	op, ok := c.ops[id]
	if !ok {
		return contract.Descriptor{}, nil, &contract.Fault{Code: contract.CodeNotFound, Message: "unknown operation " + id}
	}
	if version != 0 && version != op.desc.Version {
		return contract.Descriptor{}, nil, &contract.Fault{
			Code:    contract.CodeCapabilityUnsupported,
			Message: fmt.Sprintf("operation %s version %d is not supported", id, version),
		}
	}
	return op.desc, op.h, nil
}

func (c *fakeCatalog) Public() []contract.Descriptor {
	var out []contract.Descriptor
	for _, op := range c.ops {
		if op.desc.Visibility == contract.VisibilityPublic {
			out = append(out, op.desc)
		}
	}
	return out
}

func (c *fakeCatalog) LocalIOFor(operation string) (contract.LocalIO, bool) {
	io, ok := c.io[operation]
	return io, ok
}

// migrations for the fake owners. Every body stays inside its owner
// namespace; storage validates the prefix and pins the sha256.
func testMigrations() []contract.Migration {
	mig := func(owner string, version int64, sql string) contract.Migration {
		return contract.Migration{Owner: owner, Version: version, SQL: sql, SHA256: contract.Hash([]byte(sql))}
	}
	return []contract.Migration{
		mig("evidence", 1, `
CREATE TABLE evidence_commands (
	id                TEXT PRIMARY KEY,
	principal_id      TEXT NOT NULL,
	operation         TEXT NOT NULL,
	operation_version INTEGER NOT NULL,
	submission_key    TEXT NOT NULL,
	request_digest    TEXT NOT NULL,
	status            TEXT NOT NULL,
	data              BLOB,
	error_code        TEXT NOT NULL DEFAULT '',
	result            BLOB,
	UNIQUE (principal_id, operation, operation_version, submission_key)
);
`),
		mig("identity", 1, `
CREATE TABLE identity_principals (
	id      TEXT PRIMARY KEY,
	kind    TEXT NOT NULL,
	name    TEXT NOT NULL,
	revoked INTEGER NOT NULL DEFAULT 0
);
`),
		mig("policy", 1, `
CREATE TABLE policy_rules (
	capability TEXT PRIMARY KEY,
	decision   TEXT NOT NULL,
	reasons    TEXT NOT NULL DEFAULT '[]'
);
`),
		mig("business", 1, `
CREATE TABLE business_items (
	name    TEXT PRIMARY KEY,
	id      TEXT NOT NULL,
	version INTEGER NOT NULL
);
CREATE TABLE business_journal (
	seq  INTEGER PRIMARY KEY AUTOINCREMENT,
	note TEXT NOT NULL
);
CREATE TABLE business_attempts (
	id     TEXT PRIMARY KEY,
	worker TEXT NOT NULL UNIQUE,
	state  TEXT NOT NULL
);
`),
		mig("sibling", 1, `
CREATE TABLE sibling_log (
	seq  INTEGER PRIMARY KEY AUTOINCREMENT,
	note TEXT NOT NULL
);
`),
	}
}

// fakeModule carries the shared fake-owner plumbing.
type fakeModule struct {
	name  string
	ids   *fakeIDs
	ports contract.Ports
	mu    sync.Mutex
}

func (m *fakeModule) Name() string { return m.name }

func (m *fakeModule) Migrations() []contract.Migration {
	return nil // the harness applies testMigrations() directly
}

func payloadJSON(v any) (contract.Payload, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: "fake encode: " + err.Error()}
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: data}, nil
}

func failPayload(f *fakeFault) contract.Payload {
	return contract.Payload{Status: contract.StatusFailed, Error: &contract.Fault{Code: f.Code, Message: f.Message}}
}

func mustMarshal(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

// decodeIn decodes one fake handler's input through the same strict rules a
// real owner would apply.
func decodeIn(inv contract.Invocation, out any) (contract.Payload, bool) {
	if err := contract.DecodeStrict(inv.Input, out); err != nil {
		return failPayload(&fakeFault{Code: contract.CodeInvalidInput, Message: err.Error()}), false
	}
	return contract.Payload{}, true
}

// ---- evidence owner ----

type evidenceModule struct {
	fakeModule
}

func (m *evidenceModule) Descriptors() []contract.Descriptor {
	return []contract.Descriptor{
		{ID: "_evidence.command.begin", Version: 1, Owner: "evidence", Visibility: contract.VisibilityInternal,
			Mode: contract.ModeMutation, Callers: []string{applicationCaller}},
		{ID: "_evidence.command.finish", Version: 1, Owner: "evidence", Visibility: contract.VisibilityInternal,
			Mode: contract.ModeMutation, Callers: []string{applicationCaller}},
	}
}

func (m *evidenceModule) Handle(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	switch inv.Operation {
	case "_evidence.command.begin":
		return m.begin(ctx, u, inv)
	case "_evidence.command.finish":
		return m.finish(ctx, u, inv)
	}
	return contract.Payload{}, &contract.Fault{Code: contract.CodeNotFound, Message: "unknown evidence operation"}
}

func (m *evidenceModule) begin(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	var in struct {
		PrincipalID      string `json:"principal_id"`
		Operation        string `json:"operation"`
		OperationVersion int64  `json:"operation_version"`
		SubmissionKey    string `json:"submission_key"`
		RequestDigest    string `json:"request_digest"`
	}
	if p, ok := decodeIn(inv, &in); !ok {
		return p, nil
	}
	if row := m.byKey(ctx, u, in.PrincipalID, in.Operation, in.OperationVersion, in.SubmissionKey); row != nil {
		return payloadJSON(fakeBeginOut{CommandID: row.ID, Existing: row})
	}
	id := string(m.ids.New())
	if _, err := u.ExecContext(ctx, `INSERT INTO evidence_commands
		(id, principal_id, operation, operation_version, submission_key, request_digest, status)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, in.PrincipalID, in.Operation, in.OperationVersion, in.SubmissionKey, in.RequestDigest, contract.StatusAccepted); err != nil {
		return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: err.Error()}
	}
	return payloadJSON(fakeBeginOut{CommandID: id})
}

func (m *evidenceModule) finish(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	var in fakeFinishIn
	if p, ok := decodeIn(inv, &in); !ok {
		return p, nil
	}
	row := m.byID(ctx, u, contract.ID(in.CommandID))
	if row == nil {
		return failPayload(&fakeFault{Code: contract.CodeNotFound, Message: "command not found"}), nil
	}
	row.Status = in.Result.Status
	row.Data = in.Result.Data
	if in.Result.Error != nil {
		row.ErrorCode = in.Result.Error.Code
	}
	row.Result = in.Result
	if _, err := u.ExecContext(ctx, `UPDATE evidence_commands
		SET status = ?, data = ?, error_code = ?, result = ? WHERE id = ?`,
		row.Status, row.Data, row.ErrorCode, mustMarshal(row.Result), row.ID); err != nil {
		return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: err.Error()}
	}
	return payloadJSON(struct {
		Resource fakeCommand `json:"resource"`
	}{Resource: *row})
}

const commandColumns = `id, principal_id, operation, operation_version, submission_key,
	request_digest, status, data, error_code, result`

func (m *evidenceModule) scanRow(row interface{ Scan(...any) error }) *fakeCommand {
	var (
		c      fakeCommand
		opVer  int64
		data   []byte
		result []byte
	)
	err := row.Scan(&c.ID, &c.PrincipalID, &c.Operation, &opVer, &c.SubmissionKey,
		&c.RequestDigest, &c.Status, &data, &c.ErrorCode, &result)
	if err != nil {
		return nil
	}
	c.OperationVersion = opVer
	c.Data = json.RawMessage(data)
	if len(result) > 0 {
		var fr fakeResult
		if err := json.Unmarshal(result, &fr); err != nil {
			return nil
		}
		c.Result = &fr
	}
	return &c
}

func (m *evidenceModule) byKey(ctx context.Context, u contract.Unit, principal, operation string, version int64, key string) *fakeCommand {
	row := u.QueryRowContext(ctx, `SELECT `+commandColumns+` FROM evidence_commands
		WHERE principal_id = ? AND operation = ? AND operation_version = ? AND submission_key = ?`,
		principal, operation, version, key)
	return m.scanRow(row)
}

func (m *evidenceModule) byID(ctx context.Context, u contract.Unit, id contract.ID) *fakeCommand {
	row := u.QueryRowContext(ctx, `SELECT `+commandColumns+` FROM evidence_commands WHERE id = ?`, string(id))
	return m.scanRow(row)
}

// ---- identity owner ----

type identityModule struct {
	fakeModule
}

func (m *identityModule) Descriptors() []contract.Descriptor {
	return []contract.Descriptor{
		{ID: "_identity.authority", Version: 1, Owner: "identity", Visibility: contract.VisibilityInternal,
			Mode: contract.ModeQuery, Callers: []string{applicationCaller}},
	}
}

func (m *identityModule) Handle(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	if inv.Operation != "_identity.authority" {
		return contract.Payload{}, &contract.Fault{Code: contract.CodeNotFound, Message: "unknown identity operation"}
	}
	var in struct {
		PrincipalID string    `json:"principal_id"`
		Scope       fakeScope `json:"scope"`
	}
	if p, ok := decodeIn(inv, &in); !ok {
		return p, nil
	}
	var (
		kind    string
		revoked int
	)
	err := u.QueryRowContext(ctx, "SELECT kind, revoked FROM identity_principals WHERE id = ?", in.PrincipalID).Scan(&kind, &revoked)
	if err != nil {
		return failPayload(&fakeFault{Code: contract.CodeNotFound, Message: "principal not found in scope"}), nil
	}
	out := fakeAuthorityOut{}
	out.Resource.Principal = fakePrincipal{
		ID: in.PrincipalID, Version: 1, Kind: kind, Name: "principal-" + in.PrincipalID,
		Scope: fakeScope{InstallationID: in.Scope.InstallationID}, Revoked: revoked == 1,
	}
	out.Resource.Grants = []fakeGrant{}
	out.Resource.Restrictions = []string{}
	return payloadJSON(out)
}

// seed inserts one principal row the authority fake will resolve.
func (m *identityModule) seed(ctx context.Context, db contract.Database, installation contract.ID, id, kind string) {
	_ = db.Write(ctx, contract.Actor{PrincipalID: "seeder", Kind: contract.KindService},
		contract.Scope{InstallationID: installation}, func(u contract.Unit) error {
			_, err := u.ExecContext(ctx, `INSERT INTO identity_principals (id, kind, name, revoked) VALUES (?, ?, ?, 0)`,
				id, kind, "principal-"+id)
			return err
		})
}

func (m *identityModule) setRevoked(ctx context.Context, db contract.Database, installation contract.ID, id string, revoked bool) {
	flag := 0
	if revoked {
		flag = 1
	}
	_ = db.Write(ctx, contract.Actor{PrincipalID: "seeder", Kind: contract.KindService},
		contract.Scope{InstallationID: installation}, func(u contract.Unit) error {
			_, err := u.ExecContext(ctx, "UPDATE identity_principals SET revoked = ? WHERE id = ?", flag, id)
			return err
		})
}

// ---- policy owner ----

type policyModule struct {
	fakeModule
}

func (m *policyModule) Descriptors() []contract.Descriptor {
	return []contract.Descriptor{
		{ID: "_policy.check", Version: 1, Owner: "policy", Visibility: contract.VisibilityInternal,
			Mode: contract.ModeQuery, Callers: []string{applicationCaller}},
	}
}

func (m *policyModule) Handle(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	if inv.Operation != "_policy.check" {
		return contract.Payload{}, &contract.Fault{Code: contract.CodeNotFound, Message: "unknown policy operation"}
	}
	var in struct {
		Scope      fakeScope `json:"scope"`
		Capability string    `json:"capability"`
	}
	if p, ok := decodeIn(inv, &in); !ok {
		return p, nil
	}
	out := fakePolicyOut{}
	out.Resource.Decision = "allow"
	out.Resource.Reasons = []string{}
	out.Resource.Requirements = []fakeRequirement{}
	var decision, reasons string
	err := u.QueryRowContext(ctx, "SELECT decision, reasons FROM policy_rules WHERE capability = ?", in.Capability).Scan(&decision, &reasons)
	if err == nil {
		out.Resource.Decision = decision
		_ = json.Unmarshal([]byte(reasons), &out.Resource.Reasons)
		if out.Resource.Reasons == nil {
			out.Resource.Reasons = []string{}
		}
	}
	return payloadJSON(out)
}

func (m *policyModule) setRule(ctx context.Context, db contract.Database, installation contract.ID, capability, decision string, reasons []string) {
	_ = db.Write(ctx, contract.Actor{PrincipalID: "seeder", Kind: contract.KindService},
		contract.Scope{InstallationID: installation}, func(u contract.Unit) error {
			if decision == "" {
				_, err := u.ExecContext(ctx, "DELETE FROM policy_rules WHERE capability = ?", capability)
				return err
			}
			_, err := u.ExecContext(ctx, `INSERT INTO policy_rules (capability, decision, reasons) VALUES (?, ?, ?)
				ON CONFLICT(capability) DO UPDATE SET decision = excluded.decision, reasons = excluded.reasons`,
				capability, decision, mustMarshal(reasons))
			return err
		})
}

// ---- sibling owner (internal call target) ----

type siblingModule struct {
	fakeModule
	seenActor contract.Actor
	seenScope contract.Scope
	seenGen   int64
}

func (m *siblingModule) Descriptors() []contract.Descriptor {
	return []contract.Descriptor{
		{ID: "sibling.step", Version: 1, Owner: "sibling", Visibility: contract.VisibilityInternal,
			Mode: contract.ModeMutation, Callers: []string{"business"}},
		{ID: "sibling.echo", Version: 1, Owner: "sibling", Visibility: contract.VisibilityInternal,
			Mode: contract.ModeQuery, Callers: []string{"business"}},
		{ID: "sibling.mut", Version: 1, Owner: "sibling", Visibility: contract.VisibilityInternal,
			Mode: contract.ModeMutation, Callers: []string{"business"}},
		{ID: "sibling.private", Version: 1, Owner: "sibling", Visibility: contract.VisibilityInternal,
			Mode: contract.ModeQuery, Callers: []string{}},
		{ID: "sibling.job", Version: 1, Owner: "sibling", Visibility: contract.VisibilityInternal,
			Mode: contract.ModeMutation, Callers: []string{"controller"}},
		{ID: "sibling.loop_a", Version: 1, Owner: "sibling", Visibility: contract.VisibilityInternal,
			Mode: contract.ModeMutation, Callers: []string{"business", "sibling"}},
		{ID: "sibling.loop_b", Version: 1, Owner: "sibling", Visibility: contract.VisibilityInternal,
			Mode: contract.ModeMutation, Callers: []string{"sibling"}},
	}
}

func (m *siblingModule) Handle(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	switch inv.Operation {
	case "sibling.step":
		var in struct {
			Note string `json:"note"`
			Fail bool   `json:"fail"`
		}
		if p, ok := decodeIn(inv, &in); !ok {
			return p, nil
		}
		if _, err := u.ExecContext(ctx, "INSERT INTO sibling_log (note) VALUES (?)", in.Note); err != nil {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: err.Error()}
		}
		if in.Fail {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeConflict, Message: "sibling step refused"}
		}
		return payloadJSON(map[string]any{"stepped": true})
	case "sibling.echo":
		m.mu.Lock()
		m.seenActor = u.Actor()
		m.seenScope = u.Scope()
		m.seenGen = u.Generation()
		m.mu.Unlock()
		return payloadJSON(map[string]any{"echo": true})
	case "sibling.mut":
		return payloadJSON(map[string]any{"mutated": true})
	case "sibling.private":
		return payloadJSON(map[string]any{"private": true})
	case "sibling.job":
		if _, err := u.ExecContext(ctx, "INSERT INTO sibling_log (note) VALUES (?)", "job"); err != nil {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: err.Error()}
		}
		return payloadJSON(map[string]any{"job": true})
	case "sibling.loop_a":
		if _, err := m.ports.Call(ctx, u, contract.Invocation{Operation: "sibling.loop_b", Input: []byte(`{}`)}); err != nil {
			return contract.Payload{}, err
		}
		return payloadJSON(map[string]any{"a": true})
	case "sibling.loop_b":
		if _, err := m.ports.Call(ctx, u, contract.Invocation{Operation: "sibling.loop_a", Input: []byte(`{}`)}); err != nil {
			return contract.Payload{}, err
		}
		return payloadJSON(map[string]any{"b": true})
	}
	return contract.Payload{}, &contract.Fault{Code: contract.CodeNotFound, Message: "unknown sibling operation"}
}

// ---- business owner (public operations under test) ----

type businessModule struct {
	fakeModule
	executions   atomic.Int64
	app          *Application
	retainedUnit contract.Unit
}

func (m *businessModule) Descriptors() []contract.Descriptor {
	obj := func(required []string, props string) json.RawMessage {
		req := ""
		if len(required) > 0 {
			list, _ := json.Marshal(required)
			req = fmt.Sprintf(`,"required":%s`, list)
		}
		return json.RawMessage(fmt.Sprintf(`{"type":"object","additionalProperties":false%s,"properties":%s}`, req, props))
	}
	return []contract.Descriptor{
		{ID: "installation.init", Version: 1, Owner: "installation", Visibility: contract.VisibilityPublic,
			Mode:         contract.ModeMutation,
			InputSchema:  obj(nil, `{"credential_store":{"type":"string"},"owner_name":{"type":"string"}}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`)},
		{ID: "business.apply", Version: 1, Owner: "business", Visibility: contract.VisibilityPublic,
			Mode: contract.ModeMutation, SubmissionKey: true,
			InputSchema:  obj([]string{"name", "expected_version"}, `{"name":{"type":"string"},"expected_version":{"type":"integer","minimum":0}}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`)},
		{ID: "business.query", Version: 1, Owner: "business", Visibility: contract.VisibilityPublic,
			Mode:         contract.ModeQuery,
			InputSchema:  obj(nil, `{}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`)},
		{ID: "business.claim", Version: 1, Owner: "business", Visibility: contract.VisibilityPublic,
			Mode: contract.ModeMutation, SubmissionKey: true,
			InputSchema:  obj([]string{"worker"}, `{"worker":{"type":"string"}}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`)},
		{ID: "business.failing", Version: 1, Owner: "business", Visibility: contract.VisibilityPublic,
			Mode: contract.ModeMutation, SubmissionKey: true,
			InputSchema:  obj([]string{"stage"}, `{"stage":{"type":"string","enum":["before_emit","after_emit","conflict","panic","none"]}}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`)},
		{ID: "business.crossfail", Version: 1, Owner: "business", Visibility: contract.VisibilityPublic,
			Mode: contract.ModeMutation, SubmissionKey: true,
			InputSchema:  obj([]string{"note"}, `{"note":{"type":"string"}}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`)},
		{ID: "business.loop", Version: 1, Owner: "business", Visibility: contract.VisibilityPublic,
			Mode: contract.ModeMutation, SubmissionKey: true,
			InputSchema:  obj(nil, `{}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`)},
		{ID: "business.reentrant", Version: 1, Owner: "business", Visibility: contract.VisibilityPublic,
			Mode: contract.ModeMutation, SubmissionKey: true,
			InputSchema:  obj([]string{"target"}, `{"target":{"type":"string","enum":["invoke","internal"]}}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`)},
		{ID: "business.peek", Version: 1, Owner: "business", Visibility: contract.VisibilityPublic,
			Mode: contract.ModeMutation, SubmissionKey: true,
			InputSchema:  obj(nil, `{}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`)},
		{ID: "business.peekmut", Version: 1, Owner: "business", Visibility: contract.VisibilityPublic,
			Mode:         contract.ModeQuery,
			InputSchema:  obj(nil, `{}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`)},
		{ID: "business.badcall", Version: 1, Owner: "business", Visibility: contract.VisibilityPublic,
			Mode: contract.ModeMutation, SubmissionKey: true,
			InputSchema:  obj(nil, `{}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`)},
		{ID: "business.versioned", Version: 1, Owner: "business", Visibility: contract.VisibilityPublic,
			Mode: contract.ModeMutation, SubmissionKey: true,
			InputSchema:  obj(nil, `{}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`)},
		{ID: "business.stalecall", Version: 1, Owner: "business", Visibility: contract.VisibilityPublic,
			Mode: contract.ModeMutation, SubmissionKey: true,
			InputSchema:  obj(nil, `{}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`)},
		{ID: "business.nilcall", Version: 1, Owner: "business", Visibility: contract.VisibilityPublic,
			Mode: contract.ModeMutation, SubmissionKey: true,
			InputSchema:  obj(nil, `{}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`)},
		{ID: "business.nosub", Version: 1, Owner: "business", Visibility: contract.VisibilityPublic,
			Mode:         contract.ModeMutation,
			InputSchema:  obj(nil, `{}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`)},
		{ID: "business.scoped", Version: 1, Owner: "business", Visibility: contract.VisibilityPublic,
			Mode: contract.ModeQuery, ScopeRequired: []string{"installation_id"},
			InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["scope"],
				"properties":{"scope":{"type":"object","required":["installation_id"],
					"properties":{"installation_id":{"type":"string"},"organization_id":{"type":"string"}}}}}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`)},
		{ID: "command.get", Version: 1, Owner: "evidence", Visibility: contract.VisibilityPublic,
			Mode: contract.ModeQuery,
			InputSchema: obj([]string{"operation", "submission_key"},
				`{"operation":{"type":"string"},"submission_key":{"type":"string"}}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`)},
	}
}

func (m *businessModule) Handle(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	switch inv.Operation {
	case "installation.init":
		// The IO path skips the ordinary handler; reaching it is a routing
		// defect the harness must surface, never a fabricated success.
		return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: "installation.init must route through local IO phases"}
	case "business.apply":
		return m.apply(ctx, u, inv)
	case "business.query":
		return m.queryCount(ctx, u)
	case "business.claim":
		return m.claim(ctx, u, inv)
	case "business.failing":
		return m.failing(ctx, u, inv)
	case "business.crossfail":
		return m.crossfail(ctx, u, inv)
	case "business.loop":
		if _, err := m.ports.Call(ctx, u, contract.Invocation{Operation: "sibling.loop_a", Input: []byte(`{}`)}); err != nil {
			return contract.Payload{}, err
		}
		return payloadJSON(map[string]any{"looped": true})
	case "business.reentrant":
		return m.reentrant(ctx, u, inv)
	case "business.peek":
		m.mu.Lock()
		m.retainedUnit = u
		m.mu.Unlock()
		return m.ports.Call(ctx, u, contract.Invocation{Operation: "sibling.echo", Input: []byte(`{}`)})
	case "business.peekmut":
		if _, err := m.ports.Call(ctx, u, contract.Invocation{Operation: "sibling.mut", Input: []byte(`{}`)}); err != nil {
			return contract.Payload{}, err
		}
		return payloadJSON(map[string]any{"peeked": true})
	case "business.badcall":
		if _, err := m.ports.Call(ctx, u, contract.Invocation{Operation: "sibling.private", Input: []byte(`{}`)}); err != nil {
			return contract.Payload{}, err
		}
		return payloadJSON(map[string]any{"badcall": true})
	case "business.versioned":
		if _, err := m.ports.Call(ctx, u, contract.Invocation{Operation: "sibling.echo", Version: 99, Input: []byte(`{}`)}); err != nil {
			return contract.Payload{}, err
		}
		return payloadJSON(map[string]any{"versioned": true})
	case "business.stalecall":
		m.mu.Lock()
		stale := m.retainedUnit
		m.mu.Unlock()
		if _, err := m.ports.Call(ctx, stale, contract.Invocation{Operation: "sibling.echo", Input: []byte(`{}`)}); err != nil {
			return contract.Payload{}, err
		}
		return payloadJSON(map[string]any{"stalecall": true})
	case "business.nilcall":
		if _, err := m.ports.Call(ctx, nil, contract.Invocation{Operation: "sibling.echo", Input: []byte(`{}`)}); err != nil {
			return contract.Payload{}, err
		}
		return payloadJSON(map[string]any{"nilcall": true})
	case "business.nosub":
		return payloadJSON(map[string]any{"ran": true})
	case "business.scoped":
		return payloadJSON(map[string]any{"scoped": true})
	case "command.get":
		return m.commandGet(ctx, u, inv)
	}
	return contract.Payload{}, &contract.Fault{Code: contract.CodeNotFound, Message: "unknown business operation"}
}

func (m *businessModule) apply(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	m.executions.Add(1)
	var in struct {
		Name            string `json:"name"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if p, ok := decodeIn(inv, &in); !ok {
		return p, nil
	}
	var (
		id      string
		current int64
	)
	err := u.QueryRowContext(ctx, "SELECT id, version FROM business_items WHERE name = ?", in.Name).Scan(&id, &current)
	switch {
	case err != nil: // absent
		if in.ExpectedVersion != 0 {
			return failPayload(&fakeFault{Code: contract.CodeStaleVersion, Message: "item does not exist yet"}), nil
		}
		id = string(m.ids.New())
		if _, err := u.ExecContext(ctx, "INSERT INTO business_items (name, id, version) VALUES (?, ?, 1)", in.Name, id); err != nil {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: err.Error()}
		}
		current = 1
	case in.ExpectedVersion != current:
		return failPayload(&fakeFault{Code: contract.CodeStaleVersion,
			Message: fmt.Sprintf("item version is %d, expected %d", current, in.ExpectedVersion)}), nil
	default:
		current++
		if _, err := u.ExecContext(ctx, "UPDATE business_items SET version = ? WHERE name = ?", current, in.Name); err != nil {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: err.Error()}
		}
	}
	if err := u.Emit(ctx, contract.Event{
		Kind: "business.item.transition", ResourceID: contract.ID(id),
		ResourceVersion: contract.Version(current), Data: mustMarshal(map[string]any{"name": in.Name, "version": current}),
	}); err != nil {
		return contract.Payload{}, err
	}
	return payloadJSON(map[string]any{"name": in.Name, "version": current})
}

func (m *businessModule) queryCount(ctx context.Context, u contract.Unit) (contract.Payload, error) {
	var count int
	if err := u.QueryRowContext(ctx, "SELECT COUNT(*) FROM business_items").Scan(&count); err != nil {
		return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: err.Error()}
	}
	return payloadJSON(map[string]any{"count": count})
}

func (m *businessModule) claim(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	var in struct {
		Worker string `json:"worker"`
	}
	if p, ok := decodeIn(inv, &in); !ok {
		return p, nil
	}
	var existing string
	err := u.QueryRowContext(ctx, "SELECT id FROM business_attempts WHERE worker = ?", in.Worker).Scan(&existing)
	if err == nil {
		return failPayload(&fakeFault{Code: contract.CodeConflict,
			Message: "attempt for worker already exists: " + existing}), nil
	}
	id := string(m.ids.New())
	if _, err := u.ExecContext(ctx, "INSERT INTO business_attempts (id, worker, state) VALUES (?, ?, ?)",
		id, in.Worker, "claimed"); err != nil {
		return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: err.Error()}
	}
	return payloadJSON(map[string]any{"attempt_id": id, "state": "claimed"})
}

func (m *businessModule) failing(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	var in struct {
		Stage string `json:"stage"`
	}
	if p, ok := decodeIn(inv, &in); !ok {
		return p, nil
	}
	if _, err := u.ExecContext(ctx, "INSERT INTO business_journal (note) VALUES (?)", "state-change:"+in.Stage); err != nil {
		return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: err.Error()}
	}
	switch in.Stage {
	case "before_emit":
		return contract.Payload{}, &contract.Fault{Code: contract.CodeConflict, Message: "injected before emit"}
	case "conflict":
		return failPayload(&fakeFault{Code: contract.CodeConflict, Message: "injected failed payload"}), nil
	case "panic":
		panic("injected handler panic")
	}
	if err := u.Emit(ctx, contract.Event{
		Kind: "business.journal.recorded", ResourceID: m.ids.New(),
		ResourceVersion: 1, Data: mustMarshal(map[string]any{"stage": in.Stage}),
	}); err != nil {
		return contract.Payload{}, err
	}
	if in.Stage == "after_emit" {
		return contract.Payload{}, &contract.Fault{Code: contract.CodeConflict, Message: "injected after emit"}
	}
	return payloadJSON(map[string]any{"recorded": in.Stage})
}

func (m *businessModule) crossfail(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	var in struct {
		Note string `json:"note"`
	}
	if p, ok := decodeIn(inv, &in); !ok {
		return p, nil
	}
	if _, err := u.ExecContext(ctx, "INSERT INTO business_journal (note) VALUES (?)", "cross:"+in.Note); err != nil {
		return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: err.Error()}
	}
	if _, err := m.ports.Call(ctx, u, contract.Invocation{
		Operation: "sibling.step",
		Input:     mustMarshal(map[string]any{"note": in.Note, "fail": true}),
	}); err != nil {
		return contract.Payload{}, err
	}
	return payloadJSON(map[string]any{"crossed": true})
}

func (m *businessModule) reentrant(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	var in struct {
		Target string `json:"target"`
	}
	if p, ok := decodeIn(inv, &in); !ok {
		return p, nil
	}
	switch in.Target {
	case "invoke":
		if _, err := m.app.Invoke(ctx, u.Actor(), "business.query", contract.Request{
			Schema: contract.SchemaRequest, Input: []byte(`{}`),
		}); err != nil {
			return contract.Payload{}, err
		}
	case "internal":
		if _, err := m.app.Internal(ctx, u.Actor(), u.Scope(), contract.Invocation{
			Operation: "sibling.echo", Input: []byte(`{}`),
		}); err != nil {
			return contract.Payload{}, err
		}
	}
	return payloadJSON(map[string]any{"reentered": true})
}

func (m *businessModule) commandGet(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	var in struct {
		Operation     string `json:"operation"`
		SubmissionKey string `json:"submission_key"`
	}
	if p, ok := decodeIn(inv, &in); !ok {
		return p, nil
	}
	var (
		id, principal, digest, status, errorCode string
		opVer                                    int64
		data, result                             []byte
	)
	err := u.QueryRowContext(ctx, `SELECT id, principal_id, operation_version, request_digest, status, data, error_code, result
		FROM evidence_commands WHERE operation = ? AND submission_key = ?`, in.Operation, in.SubmissionKey).
		Scan(&id, &principal, &opVer, &digest, &status, &data, &errorCode, &result)
	if err != nil {
		return failPayload(&fakeFault{Code: contract.CodeNotFound, Message: "command not found"}), nil
	}
	var res *fakeResult
	if len(result) > 0 {
		res = &fakeResult{}
		if err := json.Unmarshal(result, res); err != nil {
			return contract.Payload{}, &contract.Fault{Code: contract.CodeInternalError, Message: err.Error()}
		}
	}
	return payloadJSON(struct {
		Resource fakeCommand `json:"resource"`
	}{Resource: fakeCommand{
		ID: id, PrincipalID: principal, Operation: in.Operation, OperationVersion: opVer,
		SubmissionKey: in.SubmissionKey, RequestDigest: digest, Status: status,
		Data: json.RawMessage(data), ErrorCode: errorCode, Result: res,
	}})
}

// ---- fake local IO service ----

type fakeLocalIO struct {
	mu           sync.Mutex
	ids          *fakeIDs
	track        *trackingDB
	prepareCalls int
	performCalls int
	finishCalls  int
	// started is signaled once per Perform entry; gate blocks Perform until
	// closed. Both let tests synchronize without sleeps.
	started chan struct{}
	gate    chan struct{}
	// failPerform makes Perform return an inspectable artifact fault.
	failPerform bool
	sawOpenTx   atomic.Bool
	panicMask   atomic.Bool
}

func (f *fakeLocalIO) Prepare(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.IOPlan, error) {
	f.mu.Lock()
	f.prepareCalls++
	f.mu.Unlock()
	return contract.IOPlan{
		ID:         f.ids.New(),
		Owner:      "artifacts",
		Invocation: inv,
		Actor:      u.Actor(),
		Scope:      u.Scope(),
		Generation: u.Generation(),
		Prepared:   mustMarshal(map[string]any{"prepared": true, "operation": inv.Operation}),
	}, nil
}

func (f *fakeLocalIO) Perform(ctx context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	if f.track.active.Load() != 0 {
		// No transaction may be open while local work runs outside Unit.
		f.sawOpenTx.Store(true)
	}
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	f.performCalls++
	f.mu.Unlock()
	if f.panicMask.Load() {
		panic("injected perform panic")
	}
	if f.failPerform {
		return contract.IOResult{Fault: &contract.Fault{
			Code: contract.CodeArtifactFault, Message: "injected local IO fault",
		}}, nil
	}
	return contract.IOResult{Data: mustMarshal(map[string]any{"performed": true, "plan": string(plan.ID)})}, nil
}

func (f *fakeLocalIO) Finish(ctx context.Context, u contract.Unit, plan contract.IOPlan, result contract.IOResult) (contract.Payload, error) {
	f.mu.Lock()
	f.finishCalls++
	f.mu.Unlock()
	if result.Fault != nil {
		return contract.Payload{Status: contract.StatusFailed, Data: result.Data, Error: result.Fault}, nil
	}
	if plan.Invocation.Operation == "installation.init" {
		// The bootstrap transaction must emit at least one scoped event: it
		// is how a restarted controller recovers the installation identity.
		if err := u.Emit(ctx, contract.Event{
			Kind: "installation.bootstrap.completed", ResourceID: f.ids.New(),
			ResourceVersion: 1, Data: mustMarshal(map[string]any{"initialized": true}),
		}); err != nil {
			return contract.Payload{}, err
		}
	}
	return payloadJSON(map[string]any{"finished": true, "plan": string(plan.ID)})
}

func (f *fakeLocalIO) calls() (int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prepareCalls, f.performCalls, f.finishCalls
}

// ---- environment assembly ----

type testEnv struct {
	dir      string
	db       *trackingDB
	raw      contract.Database
	app      *Application
	router   *PortRouter
	cat      *fakeCatalog
	ids      *fakeIDs
	clock    *fakeClock
	auth     *fakeAuth
	evidence *evidenceModule
	identity *identityModule
	policy   *policyModule
	business *businessModule
	sibling  *siblingModule
	io       *fakeLocalIO
	install  contract.ID
}

const (
	testPrincipal    = "20000000-0000-4000-8000-000000000002"
	servicePrincipal = "30000000-0000-4000-8000-000000000003"
	testCredential   = "test-credential-bearer"
)

func openEnv(t *testing.T, dir string, idStart uint64) *testEnv {
	t.Helper()
	ctx := context.Background()
	raw, err := storage.Open(ctx, storage.Config{Path: filepath.Join(dir, "test.db")})
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	track := &trackingDB{Database: raw}
	if err := raw.Migrate(ctx, testMigrations()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ids := newFakeIDs(idStart)
	clock := newFakeClock()
	auth := &fakeAuth{creds: map[string]contract.Actor{}, certs: map[contract.Digest]contract.Actor{}}

	evidence := &evidenceModule{fakeModule: fakeModule{name: "evidence", ids: ids}}
	identity := &identityModule{fakeModule: fakeModule{name: "identity", ids: ids}}
	policy := &policyModule{fakeModule: fakeModule{name: "policy", ids: ids}}
	business := &businessModule{fakeModule: fakeModule{name: "business", ids: ids}}
	sibling := &siblingModule{fakeModule: fakeModule{name: "sibling", ids: ids}}
	// started is unbuffered: a non-blocking send delivers only when a test is
	// actively waiting, so the bootstrap's Perform entry can never linger and
	// be mistaken for a later call's signal.
	io := &fakeLocalIO{ids: ids, track: track, started: make(chan struct{})}

	cat := &fakeCatalog{ops: map[string]fakeOp{}, io: map[string]contract.LocalIO{}}
	register := func(m interface {
		Descriptors() []contract.Descriptor
		Handle(context.Context, contract.Unit, contract.Invocation) (contract.Payload, error)
	}) {
		for _, d := range m.Descriptors() {
			cat.ops[d.ID] = fakeOp{desc: d, h: m.Handle}
		}
	}
	register(evidence)
	register(identity)
	register(policy)
	register(business)
	register(sibling)

	// Local IO services: one service covers the IO-routed operations the
	// fakes expose, exactly as the registry pairs owners at assembly.
	for _, op := range []string{"artifact.upload.finish", "artifact.read", "installation.init"} {
		cat.io[op] = io
	}
	// Descriptors for the IO-routed operations the fakes exercise. The
	// ordinary handler is never reached: the dispatcher routes them through
	// the local IO phases instead.
	ioSchema := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`)
	cat.ops["artifact.upload.finish"] = fakeOp{desc: contract.Descriptor{
		ID: "artifact.upload.finish", Version: 1, Owner: "artifacts", Visibility: contract.VisibilityPublic,
		Mode: contract.ModeMutation, SubmissionKey: true, InputSchema: ioSchema,
	}}
	cat.ops["artifact.read"] = fakeOp{desc: contract.Descriptor{
		ID: "artifact.read", Version: 1, Owner: "artifacts", Visibility: contract.VisibilityPublic,
		Mode: contract.ModeQuery, InputSchema: ioSchema,
	}}

	app, err := New(track, cat, auth, clock, ids)
	if err != nil {
		t.Fatalf("application.New: %v", err)
	}
	router := NewPorts()
	business.ports = router.For("business")
	business.app = app
	sibling.ports = router.For("sibling")
	if err := router.Bind(app); err != nil {
		t.Fatalf("router.Bind: %v", err)
	}
	if _, err := track.StartGeneration(ctx); err != nil {
		t.Fatalf("StartGeneration: %v", err)
	}

	e := &testEnv{
		dir: dir, db: track, raw: raw, app: app, router: router,
		cat: cat, ids: ids, clock: clock, auth: auth,
		evidence: evidence, identity: identity, policy: policy,
		business: business, sibling: sibling, io: io,
	}
	t.Cleanup(func() { _ = raw.Close() })
	return e
}

// bootstrapInit runs installation.init on an uninitialized environment and
// records the minted installation id.
func bootstrapInit(t *testing.T, e *testEnv) {
	t.Helper()
	actor := contract.Actor{PrincipalID: servicePrincipal, Kind: contract.KindService}
	res, err := e.app.Invoke(context.Background(), actor, "installation.init", contract.Request{
		Schema: contract.SchemaRequest,
		Input:  mustMarshal(map[string]any{"credential_store": "os", "owner_name": "test-owner"}),
	})
	if err != nil {
		t.Fatalf("installation.init: %v", err)
	}
	if res.Status != contract.StatusCompleted {
		t.Fatalf("installation.init status %q, want completed", res.Status)
	}
	got := installationIDForTest(e.app)
	if got == "" {
		t.Fatal("installation id was not remembered after bootstrap")
	}
	e.install = got
}

// newTestEnv builds a fully initialized environment: storage on a temp file,
// migrations, fake owners, bound router and a completed installation.init.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	e := openEnv(t, t.TempDir(), 0)
	bootstrapInit(t, e)
	ctx := context.Background()
	e.identity.seed(ctx, e.db, e.install, testPrincipal, contract.KindHuman)
	e.identity.seed(ctx, e.db, e.install, servicePrincipal, contract.KindService)
	e.auth.creds[testCredential] = contract.Actor{PrincipalID: testPrincipal, Kind: contract.KindHuman}
	return e
}

// reopenEnv closes env's database and rebuilds a fresh environment over the
// same file, with fresh fakes but identical operation registrations. It
// simulates a controller restart: durable state survives, in-memory fakes
// start clean.
func reopenEnv(t *testing.T, e *testEnv) *testEnv {
	t.Helper()
	if err := e.raw.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	next := openEnv(t, e.dir, 1_000_000)
	next.install = e.install
	for k, v := range e.auth.creds {
		next.auth.creds[k] = v
	}
	for k, v := range e.auth.certs {
		next.auth.certs[k] = v
	}
	return next
}

// installationIDForTest reads the cached installation identity.
func installationIDForTest(a *Application) contract.ID {
	a.installMu.RLock()
	defer a.installMu.RUnlock()
	return a.installation
}

// actor returns the seeded human actor.
func (e *testEnv) actor() contract.Actor {
	return contract.Actor{PrincipalID: testPrincipal, Kind: contract.KindHuman}
}

func (e *testEnv) serviceActor() contract.Actor {
	return contract.Actor{PrincipalID: servicePrincipal, Kind: contract.KindService}
}

// invoke runs one public operation through the full dispatch pipeline.
func (e *testEnv) invoke(t *testing.T, actor contract.Actor, op, key string, input any) (contract.Result, error) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return e.app.Invoke(context.Background(), actor, op, contract.Request{
		Schema: contract.SchemaRequest, SubmissionKey: key, Input: raw,
	})
}

// eventCount reads the durable event log through the real database.
func (e *testEnv) eventCount(t *testing.T) int {
	t.Helper()
	events, err := e.db.Events(context.Background(), 0, 500)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	return len(events)
}

// readColumn returns one column of every row of a table, through the real
// database, for durable-state assertions.
func (e *testEnv) readColumn(t *testing.T, table, column, orderBy string) []string {
	t.Helper()
	var out []string
	err := e.db.Read(context.Background(), e.serviceActor(), contract.Scope{InstallationID: e.install},
		func(u contract.Unit) error {
			rows, err := u.QueryContext(context.Background(),
				fmt.Sprintf("SELECT %s FROM %s ORDER BY %s", column, table, orderBy))
			if err != nil {
				return err
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var v string
				if err := rows.Scan(&v); err != nil {
					return err
				}
				out = append(out, v)
			}
			return rows.Err()
		})
	if err != nil {
		t.Fatalf("readColumn %s.%s: %v", table, column, err)
	}
	return out
}

// requireFault asserts the error is a *contract.Fault with the given code.
func requireFault(t *testing.T, err error, code string) *contract.Fault {
	t.Helper()
	if err == nil {
		t.Fatalf("expected fault %s, got nil error", code)
	}
	f := faultOf(err)
	if f.Code != code {
		t.Fatalf("expected fault code %s, got %s (%s)", code, f.Code, f.Message)
	}
	return f
}
