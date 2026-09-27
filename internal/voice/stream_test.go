package voice

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zatiti/zatiti/internal/contract"
	"github.com/zatiti/zatiti/internal/storage"
)

type countingIDs struct{ n int }

func (c *countingIDs) New() contract.ID {
	c.n++
	return contract.ID(contract.NewID())
}

// stubPorts answers only the peer calls phrase speech makes before leaving
// the transaction.
type stubPorts struct {
	reserves int
	settles  []map[string]any
}

func (p *stubPorts) Call(_ context.Context, _ contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	var body any
	switch inv.Operation {
	case "_messaging.voice.read":
		body = map[string]any{"text": ""}
	case "_connections.voice.resolve":
		body = map[string]any{"credential_ref": "keychain:voice"}
	case "_accounting.reserve":
		p.reserves++
		body = map[string]any{"resource": map[string]any{"id": "reservation"}}
	case "_accounting.settle":
		var in map[string]any
		_ = json.Unmarshal(inv.Input, &in)
		p.settles = append(p.settles, in)
		version, _ := in["expected_version"].(float64)
		body = map[string]any{"resource": map[string]any{"id": in["reservation_id"], "version": int64(version) + 1}}
	default:
		return contract.Payload{}, errors.New("unexpected peer call " + inv.Operation)
	}
	raw, _ := json.Marshal(body)
	return contract.Payload{Status: contract.StatusCompleted, Data: raw}, nil
}

// scopedStreams is a minimal ReplyStreams holding previews per recipient
// view, the same keying the controller hub uses.
type scopedStreams struct {
	previews map[[3]contract.ID][]contract.ReplyPreview
}

func (s *scopedStreams) Register(contract.Digest, contract.ReplyRoute) {}
func (s *scopedStreams) Commit(contract.ID, string)                    {}
func (s *scopedStreams) Route(contract.Digest) (contract.ReplyRoute, bool) {
	return contract.ReplyRoute{}, false
}
func (s *scopedStreams) Publish(contract.ReplyRoute, string, string, string) {}
func (s *scopedStreams) Watch(contract.ID, contract.ID, contract.ID) (<-chan struct{}, func()) {
	return nil, func() {}
}
func (s *scopedStreams) Snapshot(actor, installation, conversation contract.ID) []contract.ReplyPreview {
	return s.previews[[3]contract.ID{actor, installation, conversation}]
}

type stepClock struct{ now time.Time }

func (c *stepClock) Now() time.Time { return c.now }

type phraseEnv struct {
	clock   *stepClock
	ctx     context.Context
	db      contract.Database
	svc     *Service
	ports   *stubPorts
	streams *scopedStreams
	human   contract.Actor
	scope   contract.Scope
	session contract.ID
	conv    contract.ID
}

func newPhraseEnv(t *testing.T) *phraseEnv {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, storage.Config{Path: filepath.Join(t.TempDir(), "voice.db")})
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e := &phraseEnv{ctx: ctx, db: db, ports: &stubPorts{}, streams: &scopedStreams{previews: map[[3]contract.ID][]contract.ReplyPreview{}}}
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	e.clock = &stepClock{now: now}
	e.svc, err = New(contract.Dependencies{Clock: e.clock, IDs: &countingIDs{}, Ports: e.ports, Streams: e.streams})
	if err != nil {
		t.Fatalf("voice.New: %v", err)
	}
	if err = db.Migrate(ctx, e.svc.Migrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	generation, err := db.StartGeneration(ctx)
	if err != nil {
		t.Fatalf("start generation: %v", err)
	}
	e.human = contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindHuman}
	e.scope = contract.Scope{InstallationID: contract.NewID()}
	e.conv = contract.NewID()
	e.session = contract.NewID()
	sess := Session{ID: e.session, Conversation: e.conv, State: "active", Expires: now.Add(time.Hour),
		Settings: Settings{Speech: "hexgrad/kokoro-82m", Style: "conversational", Budget: 1_000_000, Allowance: 1000}}
	err = db.Write(ctx, e.human, e.scope, func(u contract.Unit) error {
		_, err := u.ExecContext(ctx, "INSERT INTO voice_sessions(id,actor_id,scope_json,generation,data) VALUES(?,?,?,?,?)",
			sess.ID, e.human.PrincipalID, string(raw(e.scope)), generation, string(raw(sess)))
		return err
	})
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return e
}

func (e *phraseEnv) offer(recipient contract.ID, p contract.ReplyPreview) {
	k := [3]contract.ID{recipient, e.scope.InstallationID, e.conv}
	e.streams.previews[k] = append(e.streams.previews[k], p)
}

func (e *phraseEnv) prepare(actor contract.Actor, stream string, index int) (contract.IOPlan, error) {
	input := raw(map[string]any{"scope": e.scope, "session_id": e.session, "stream_id": stream, "phrase_index": index})
	var plan contract.IOPlan
	err := e.db.Write(e.ctx, actor, e.scope, func(u contract.Unit) error {
		var err error
		plan, err = e.svc.Prepare(e.ctx, u, contract.Invocation{Operation: "voice.speak.phrase", Version: 1, Input: input})
		return err
	})
	return plan, err
}

func faultCode(err error) string {
	var f *contract.Fault
	if errors.As(err, &f) {
		return f.Code
	}
	return ""
}

func TestSpeakPhraseResolvesControllerTextAndRefusesADuplicateCommand(t *testing.T) {
	e := newPhraseEnv(t)
	e.offer(e.human.PrincipalID, contract.ReplyPreview{ID: "att:fc_1", VoiceSession: e.session, State: "streaming", Phrases: []string{"First phrase.", "Second phrase."}})
	plan, err := e.prepare(e.human, "att:fc_1", 1)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	var p prepared
	if err = json.Unmarshal(plan.Invocation.Input, &p); err != nil || p.Text != "Second phrase." {
		t.Fatalf("prepared text = %q (%v), want the controller-resolved phrase", p.Text, err)
	}
	// A new command for the same phrase must not buy the same speech twice.
	if _, err = e.prepare(e.human, "att:fc_1", 1); faultCode(err) != contract.CodeConflict {
		t.Fatalf("duplicate phrase command = %v, want conflict", err)
	}
	if e.ports.reserves != 1 {
		t.Fatalf("accounting reservations = %d, want exactly 1", e.ports.reserves)
	}
}

func TestSpeakPhraseRefusesUnavailablePhrases(t *testing.T) {
	e := newPhraseEnv(t)
	other := contract.NewID()
	e.offer(e.human.PrincipalID, contract.ReplyPreview{ID: "interrupted", VoiceSession: e.session, State: "interrupted", Phrases: []string{"Said."}})
	e.offer(e.human.PrincipalID, contract.ReplyPreview{ID: "other-session", VoiceSession: contract.NewID(), State: "generated", Phrases: []string{"Elsewhere."}})
	e.offer(other, contract.ReplyPreview{ID: "someone-else", VoiceSession: e.session, State: "generated", Phrases: []string{"Private."}})
	e.offer(e.human.PrincipalID, contract.ReplyPreview{ID: "short", VoiceSession: e.session, State: "streaming", Phrases: []string{"Only one."}})
	for name, c := range map[string]struct {
		stream string
		index  int
	}{
		"interrupted stream":     {"interrupted", 0},
		"another voice session":  {"other-session", 0},
		"another recipient view": {"someone-else", 0},
		"phrase not yet stable":  {"short", 1},
		"unknown stream":         {"missing", 0},
	} {
		if _, err := e.prepare(e.human, c.stream, c.index); faultCode(err) != contract.CodeNotFound {
			t.Errorf("%s: err = %v, want not_found", name, err)
		}
	}
	if e.ports.reserves != 0 {
		t.Fatalf("accounting reservations = %d, want none for refused phrases", e.ports.reserves)
	}
}

func TestSpeakPhraseIsHumanOnlyAndSessionOwned(t *testing.T) {
	e := newPhraseEnv(t)
	e.offer(e.human.PrincipalID, contract.ReplyPreview{ID: "s", VoiceSession: e.session, State: "generated", Phrases: []string{"Hi."}})
	if _, err := e.prepare(contract.Actor{PrincipalID: e.human.PrincipalID, Kind: contract.KindService}, "s", 0); faultCode(err) != contract.CodePermissionDenied {
		t.Fatalf("service actor = %v, want permission_denied", err)
	}
	if _, err := e.prepare(contract.Actor{PrincipalID: contract.NewID(), Kind: contract.KindHuman}, "s", 0); faultCode(err) != contract.CodePermissionDenied {
		t.Fatalf("other human = %v, want permission_denied for another actor's session", err)
	}
}
