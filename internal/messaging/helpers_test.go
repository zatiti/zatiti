package messaging

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

// Test harness: a real storage database per test, deterministic fakes for
// clock, ids and the four declared peer operations, and helpers that drive
// every operation through Service.Handle inside storage transactions.

// fakeClock is a deterministic contract.Clock with a controllable instant.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// seqIDs mints syntactically valid UUIDv4 identities in sequence.
type seqIDs struct {
	mu sync.Mutex
	n  int
}

func (s *seqIDs) New() contract.ID {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return contract.ID(fmt.Sprintf("00000000-0000-4000-8000-%012d", s.n))
}

// fakeBinding is one configuration binding served by the snapshot peer.
type fakeBinding struct {
	ID           string   `json:"id"`
	Version      int64    `json:"version"`
	Kind         string   `json:"kind"`
	TargetID     string   `json:"target_id"`
	Permissions  []string `json:"permissions"`
	Destinations []string `json:"destinations"`
}

// fakePorts serves the four declared peer operations with in-memory
// registries and injectable decisions and faults, recording every call.
type fakePorts struct {
	mu sync.Mutex

	calls []contract.Invocation

	// _configuration.snapshot: bindings granted to principals in scope.
	bindings []fakeBinding

	// _policy.check: the frozen disclosure gate decision.
	policyDecision string
	policyReasons  []string

	// _artifacts.metadata: artifact registry. An unregistered reference
	// echoes the requested digest as an available artifact in scope.
	artifacts map[contract.ID]peerArtifact

	// _evidence.snapshot: the current event checkpoint for cursors.
	evidenceSeq int64

	// peerFaults injects a fault payload for one peer operation, decoded
	// exactly as a real peer fault would be.
	peerFaults map[string]*contract.Fault
}

func newFakePorts() *fakePorts {
	return &fakePorts{
		artifacts:      map[contract.ID]peerArtifact{},
		policyDecision: "allow",
		evidenceSeq:    1,
		peerFaults:     map[string]*contract.Fault{},
	}
}

func (p *fakePorts) Call(_ context.Context, _ contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, inv)
	if f, ok := p.peerFaults[inv.Operation]; ok {
		return contract.Payload{Error: f}, nil
	}
	var body any
	switch inv.Operation {
	case "_configuration.snapshot":
		var in peerSnapshotIn
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		body = map[string]any{
			"resource": map[string]any{
				"scope":     in.Scope,
				"revision":  1,
				"ancestors": []any{},
				"bindings":  p.bindings,
			},
		}
	case "_policy.check":
		var in peerPolicyCheckIn
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		body = peerPolicyCheckOut{Resource: struct {
			Decision     string   `json:"decision"`
			Reasons      []string `json:"reasons"`
			Requirements []any    `json:"requirements"`
		}{Decision: p.policyDecision, Reasons: p.policyReasons}}
	case "_artifacts.metadata":
		var in peerMetadataIn
		if err := json.Unmarshal(inv.Input, &in); err != nil {
			return contract.Payload{}, err
		}
		out := peerMetadataOut{Artifacts: []peerArtifact{}}
		for _, ref := range in.Artifacts {
			if a, ok := p.artifacts[ref.ID]; ok {
				out.Artifacts = append(out.Artifacts, a)
				continue
			}
			out.Artifacts = append(out.Artifacts, peerArtifact{
				ID: ref.ID, Version: 1, Scope: in.Scope, Digest: string(ref.Digest),
				Size: 128, MediaType: "application/octet-stream",
				State: "available", CreatedAt: "2026-09-10T12:00:00Z",
				Classification: "internal",
			})
		}
		body = out
	case "_evidence.snapshot":
		body = peerEvidenceOut{LastSequence: p.evidenceSeq}
	default:
		return contract.Payload{}, &contract.Fault{
			Code: contract.CodeInternalError, Message: "fake ports: unexpected peer call " + inv.Operation,
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return contract.Payload{}, err
	}
	return contract.Payload{Status: contract.StatusCompleted, Data: raw}, nil
}

// callsOf returns a snapshot of the recorded peer invocations for one op.
func (p *fakePorts) callsOf(op string) []contract.Invocation {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []contract.Invocation
	for _, c := range p.calls {
		if c.Operation == op {
			out = append(out, c)
		}
	}
	return out
}

// policyCalls decodes every recorded _policy.check input.
func (p *fakePorts) policyCalls() []peerPolicyCheckIn {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []peerPolicyCheckIn
	for _, c := range p.calls {
		if c.Operation != "_policy.check" {
			continue
		}
		var in peerPolicyCheckIn
		if err := json.Unmarshal(c.Input, &in); err != nil {
			continue
		}
		out = append(out, in)
	}
	return out
}

// setPolicy injects a policy decision and reasons for subsequent checks.
func (p *fakePorts) setPolicy(decision string, reasons ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.policyDecision = decision
	p.policyReasons = reasons
}

// addReportingBinding grants one worker a reporting binding whose
// destinations name the conversations its reports may surface to.
func (p *fakePorts) addReportingBinding(id, targetID string, destinations ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bindings = append(p.bindings, fakeBinding{
		ID: id, Version: 1, Kind: "reporting", TargetID: targetID,
		Permissions: []string{"messaging.disclosure.deliver"}, Destinations: destinations,
	})
}

// registerArtifact pins an artifact in the fake registry.
func (p *fakePorts) registerArtifact(id contract.ID, scope wireScope, digest, state string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if state == "" {
		state = "available"
	}
	p.artifacts[id] = peerArtifact{
		ID: id, Version: 1, Scope: scope, Digest: digest, Size: 256,
		MediaType: "application/octet-stream", State: state,
		CreatedAt: "2026-09-10T12:00:00Z", Classification: "internal",
	}
}

// testEnv is one installation wired to a real storage database.
type testEnv struct {
	t       *testing.T
	ctx     context.Context
	db      contract.Database
	svc     *Service
	ports   *fakePorts
	clock   *fakeClock
	ids     *seqIDs
	actor   contract.Actor
	install contract.ID
	owner   contract.ID
	chief   contract.ID
	third   contract.ID
	worker  contract.ID
	scope   wireScope
}

// newEnv opens a fresh database, migrates the messaging owner and wires the
// service with deterministic fakes. The default caller is the human owner.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "messaging-test.db")})
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	env := &testEnv{
		t: t, ctx: ctx, db: db,
		ports: newFakePorts(),
		clock: &fakeClock{now: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)},
		ids:   &seqIDs{},
	}
	svc, err := New(contract.Dependencies{Clock: env.clock, IDs: env.ids, Ports: env.ports})
	if err != nil {
		t.Fatalf("messaging.New: %v", err)
	}
	env.svc = svc
	if err := db.Migrate(ctx, svc.Migrations()); err != nil {
		t.Fatalf("migrate messaging: %v", err)
	}
	env.install = env.ids.New()
	env.owner = env.ids.New()
	env.chief = env.ids.New()
	env.third = env.ids.New()
	env.worker = env.ids.New()
	env.actor = contract.Actor{PrincipalID: env.owner, Kind: contract.KindHuman}
	env.scope = wireScope{InstallationID: env.install}
	return env
}

// asOwner acts as the human owner principal.
func (e *testEnv) asOwner() { e.actor = contract.Actor{PrincipalID: e.owner, Kind: contract.KindHuman} }

// asChief acts as the second human principal.
func (e *testEnv) asChief() { e.actor = contract.Actor{PrincipalID: e.chief, Kind: contract.KindHuman} }

// asThird acts as a human principal outside the fixture conversations.
func (e *testEnv) asThird() { e.actor = contract.Actor{PrincipalID: e.third, Kind: contract.KindHuman} }

// asWorker acts as the fixture worker principal.
func (e *testEnv) asWorker() {
	e.actor = contract.Actor{PrincipalID: e.worker, Kind: contract.KindWorker}
}

// asClientAgent acts as a client-agent principal under the owner identity.
func (e *testEnv) asClientAgent() {
	e.actor = contract.Actor{PrincipalID: e.owner, Kind: contract.KindClientAgent}
}

// callAs runs one operation inside a write transaction on the given scope.
func (e *testEnv) callAs(scope wireScope, op string, in any) (contract.Payload, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		e.t.Fatalf("marshal input for %s: %v", op, err)
	}
	var payload contract.Payload
	err = e.db.Write(e.ctx, e.actor, scope.toContract(), func(unit contract.Unit) error {
		p, err := e.svc.Handle(e.ctx, unit, contract.Invocation{Operation: op, Version: 1, Input: raw})
		payload = p
		return err
	})
	return payload, err
}

func (e *testEnv) call(op string, in any) (contract.Payload, error) {
	return e.callAs(e.scope, op, in)
}

// mustOK runs an operation and requires a completed payload.
func (e *testEnv) mustOK(op string, in any) contract.Payload {
	e.t.Helper()
	payload, err := e.call(op, in)
	if err != nil {
		e.t.Fatalf("%s failed: %v", op, err)
	}
	if payload.Status != contract.StatusCompleted {
		e.t.Fatalf("%s status %q, want completed (error %v)", op, payload.Status, payload.Error)
	}
	if payload.Error != nil {
		e.t.Fatalf("%s returned fault %s: %s", op, payload.Error.Code, payload.Error.Message)
	}
	return payload
}

// expectFault runs an operation and requires a fault with the exact code.
func (e *testEnv) expectFault(op string, in any, code string) *contract.Fault {
	e.t.Helper()
	payload, err := e.call(op, in)
	var f *contract.Fault
	if err != nil {
		errors.As(err, &f)
	}
	if f == nil {
		f = payload.Error
	}
	if f == nil {
		e.t.Fatalf("%s: expected %s fault, got completed payload %s", op, code, payload.Status)
	}
	if f.Code != code {
		e.t.Fatalf("%s: fault %s (%s), want %s", op, f.Code, f.Message, code)
	}
	return f
}

func (e *testEnv) decode(raw json.RawMessage, out any) {
	e.t.Helper()
	if err := json.Unmarshal(raw, out); err != nil {
		e.t.Fatalf("decode payload: %v", err)
	}
}

// asFault extracts a *contract.Fault from err through errors.As.
func asFault(err error, target **contract.Fault) bool {
	return errors.As(err, target)
}

// readConversation fetches the durable conversation row, bypassing the wire
// layer.
func (e *testEnv) readConversation(id contract.ID) *conversationRow {
	e.t.Helper()
	var row *conversationRow
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		var err error
		row, err = getConversation(e.ctx, unit, id)
		return err
	}); err != nil {
		e.t.Fatalf("read conversation row: %v", err)
	}
	if row == nil {
		e.t.Fatalf("conversation %s not found", id)
	}
	return row
}

// readMessage fetches the durable message row for one installation.
func (e *testEnv) readMessage(id contract.ID) *messageRow {
	e.t.Helper()
	var row *messageRow
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		var err error
		row, err = getMessageInInstallation(e.ctx, unit, e.install, id)
		return err
	}); err != nil {
		e.t.Fatalf("read message row: %v", err)
	}
	if row == nil {
		e.t.Fatalf("message %s not found", id)
	}
	return row
}

// events drains the storage outbox for inspection.
func (e *testEnv) events() []contract.Event {
	e.t.Helper()
	events, err := e.db.Events(e.ctx, 0, 100)
	if err != nil {
		e.t.Fatalf("events: %v", err)
	}
	return events
}

// ---------- fixtures ----------

// digestFixture satisfies the 64-lowercase-hex digest pattern.
func digestFixture(name string) string {
	var h uint64 = 0xcbf29ce484222325
	for i := 0; i < len(name); i++ {
		h ^= uint64(name[i])
		h *= 0x100000001b3
	}
	return fmt.Sprintf("%064x", h)
}

// artifactFixture registers an available artifact with the fake artifacts
// owner and returns its pinned reference.
func (e *testEnv) artifactFixture(name string) wireArtifactRef {
	id := e.ids.New()
	digest := digestFixture(name)
	e.ports.registerArtifact(id, e.scope, digest, "available")
	return wireArtifactRef{ID: id, Digest: contract.Digest(digest)}
}

// setPeerFault injects a fault response for one peer operation.
func (e *testEnv) setPeerFault(op string, f *contract.Fault) {
	e.ports.mu.Lock()
	defer e.ports.mu.Unlock()
	e.ports.peerFaults[op] = f
}

// bootstrap admits the pinned personal-chief conversation and returns its id.
func (e *testEnv) bootstrap() contract.ID {
	e.t.Helper()
	payload := e.mustOK("_messaging.bootstrap", map[string]any{
		"scope": e.scope, "owner_id": e.owner, "chief_id": e.chief,
	})
	var out struct {
		Resource wireConversation `json:"resource"`
	}
	e.decode(payload.Data, &out)
	if out.Resource.Kind != kindDirect || !out.Resource.Pinned {
		e.t.Fatalf("bootstrap conversation drifted: %+v", out.Resource)
	}
	return out.Resource.ID
}

// createGroup admits a group conversation and returns its id. The caller is
// always a participant.
func (e *testEnv) createGroup(participants ...contract.ID) contract.ID {
	e.t.Helper()
	payload := e.mustOK("conversation.create", map[string]any{
		"scope": e.scope, "kind": kindGroup,
		"participant_ids": participants, "title": "fixture group",
	})
	var out struct {
		Resource wireConversation `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return out.Resource.ID
}

// sendToConversation delivers one message into a conversation and returns
// the admitted wire resource.
func (e *testEnv) sendToConversation(conv contract.ID, messageID contract.ID, body string,
	attachments []wireArtifactRef, taskIDs []contract.ID) *wireMessage {
	e.t.Helper()
	if attachments == nil {
		attachments = []wireArtifactRef{}
	}
	if taskIDs == nil {
		taskIDs = []contract.ID{}
	}
	payload := e.mustOK("conversation.message.send", map[string]any{
		"scope": e.scope, "conversation_id": conv, "message_id": messageID,
		"body": body, "attachments": attachments, "task_ids": taskIDs,
	})
	var out struct {
		Resource wireMessage `json:"resource"`
	}
	e.decode(payload.Data, &out)
	if out.Resource.State != messageStateAdmitted {
		e.t.Fatalf("sent message state %q, want admitted", out.Resource.State)
	}
	return &out.Resource
}

// mailboxSend delivers one point-to-point message to one recipient.
func (e *testEnv) mailboxSend(messageID, recipient contract.ID, body string,
	attachments []wireArtifactRef, taskIDs []contract.ID) *wireMessage {
	e.t.Helper()
	if attachments == nil {
		attachments = []wireArtifactRef{}
	}
	if taskIDs == nil {
		taskIDs = []contract.ID{}
	}
	payload := e.mustOK("mailbox.send", map[string]any{
		"scope": e.scope, "message_id": messageID, "recipient_id": recipient,
		"body": body, "attachments": attachments, "task_ids": taskIDs,
	})
	var out struct {
		Resource wireMessage `json:"resource"`
	}
	e.decode(payload.Data, &out)
	if out.Resource.State != messageStateAdmitted {
		e.t.Fatalf("sent message state %q, want admitted", out.Resource.State)
	}
	return &out.Resource
}

// admitInput builds a full submitted Message wire document for
// _messaging.admit, carrying every field the frozen definition requires.
func (e *testEnv) admitInput(messageID, sender contract.ID, recipients []contract.ID,
	body string, conversationID contract.ID) wireMessage {
	if recipients == nil {
		recipients = []contract.ID{}
	}
	return wireMessage{
		ID: messageID, Version: 1, SenderID: sender, RecipientIDs: recipients,
		Scope: e.scope, TaskIDs: []contract.ID{}, Body: body,
		Attachments: []wireArtifactRef{}, State: messageStateSubmitted,
		CreatedAt: "2026-09-10T12:00:00Z", ConversationID: conversationID,
	}
}

// pendingItems reads one worker's pending inbox through the internal
// operation, carrying the requested limit through to the handler.
func (e *testEnv) pendingItems(worker contract.ID, limit int64) []*wireMessage {
	e.t.Helper()
	payload := e.mustOK("_messaging.pending", map[string]any{
		"worker_id": worker, "limit": limit,
	})
	var out struct {
		Items []*wireMessage `json:"items"`
	}
	e.decode(payload.Data, &out)
	return out.Items
}

// admit delivers one internal message and returns the admitted wire result.
func (e *testEnv) admit(msg wireMessage) *wireMessage {
	e.t.Helper()
	payload := e.mustOK("_messaging.admit", map[string]any{"message": msg})
	var out struct {
		Resource wireMessage `json:"resource"`
	}
	e.decode(payload.Data, &out)
	if out.Resource.State != messageStateAdmitted {
		e.t.Fatalf("admitted message state %q, want admitted", out.Resource.State)
	}
	return &out.Resource
}

// ack acknowledges one inbox row as the recipient.
func (e *testEnv) ack(messageID, recipient contract.ID, expectedVersion int64) *wireMessage {
	e.t.Helper()
	payload := e.mustOK("mailbox.ack", map[string]any{
		"scope": e.scope, "message_id": messageID, "recipient_id": recipient,
		"expected_version": expectedVersion,
	})
	var out struct {
		Resource wireMessage `json:"resource"`
	}
	e.decode(payload.Data, &out)
	return &out.Resource
}

// listConversationsPage runs conversation.list and returns the decoded page.
func (e *testEnv) listConversationsPage(in map[string]any) (items []*wireConversation, next string) {
	e.t.Helper()
	payload := e.mustOK("conversation.list", in)
	var out struct {
		Items      []*wireConversation `json:"items"`
		NextCursor string              `json:"next_cursor"`
	}
	e.decode(payload.Data, &out)
	return out.Items, out.NextCursor
}

// listMailboxPage runs mailbox.list for a recipient and returns the page.
func (e *testEnv) listMailboxPage(recipient contract.ID, in map[string]any) (items []*wireMessage, next string) {
	e.t.Helper()
	in["scope"] = e.scope
	in["recipient_id"] = recipient
	payload := e.mustOK("mailbox.list", in)
	var out struct {
		Items      []*wireMessage `json:"items"`
		NextCursor string         `json:"next_cursor"`
	}
	e.decode(payload.Data, &out)
	return out.Items, out.NextCursor
}

// readMarker reads one read-marker row; ok reports existence.
func (e *testEnv) readMarker(conversationID, principal contract.ID) (messageID contract.ID, ok bool) {
	e.t.Helper()
	if err := e.db.Read(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		row := unit.QueryRowContext(e.ctx,
			`SELECT last_read_message_id FROM messaging_read_markers WHERE conversation_id = ? AND principal_id = ?`,
			string(conversationID), string(principal))
		var raw contract.ID
		switch err := row.Scan(&raw); {
		case err == sql.ErrNoRows:
			return nil
		case err != nil:
			return err
		}
		messageID, ok = raw, true
		return nil
	}); err != nil {
		e.t.Fatalf("read marker query: %v", err)
	}
	return messageID, ok
}

// execSQL runs one statement inside a transaction (tamper simulation).
func (e *testEnv) execSQL(query string, args ...any) {
	e.t.Helper()
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		_, err := unit.ExecContext(e.ctx, query, args...)
		return err
	}); err != nil {
		e.t.Fatalf("exec %q: %v", query, err)
	}
}

// readRecipient fetches one recipient inbox row, bypassing the wire layer.
func (e *testEnv) readRecipient(messageID, recipient contract.ID) *recipientRow {
	e.t.Helper()
	var row *recipientRow
	if err := e.db.Write(e.ctx, e.actor, e.scope.toContract(), func(unit contract.Unit) error {
		var err error
		row, err = getRecipient(e.ctx, unit, messageID, recipient)
		return err
	}); err != nil {
		e.t.Fatalf("read recipient row: %v", err)
	}
	return row
}

// recipientRows lists the durable inbox rows of one recipient.
func recipientRows(env *testEnv, recipient contract.ID) []*messageRow {
	env.t.Helper()
	var rows []*messageRow
	if err := env.db.Read(env.ctx, env.actor, env.scope.toContract(), func(unit contract.Unit) error {
		var err error
		rows, err = listMailbox(env.ctx, unit, env.install, recipient, mailboxFilter{}, 200, 0)
		return err
	}); err != nil {
		env.t.Fatalf("list mailbox rows: %v", err)
	}
	return rows
}
