package voice

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/narrate-it/narrate/speech/openrouter"
	"github.com/zatiti/zatiti/internal/contract"
)

type failingSpeech struct{ err error }

func (f failingSpeech) Transcribe(context.Context, string, string, []byte) (openrouter.Result, error) {
	return openrouter.Result{}, f.err
}
func (f failingSpeech) Speak(context.Context, string, string, string) (openrouter.Result, error) {
	return openrouter.Result{}, f.err
}
func (f failingSpeech) Rewrite(context.Context, string, string) (string, error) { return "", f.err }

type oneSecret struct{}

func (oneSecret) Put(context.Context, string, []byte) (string, error) { return "", nil }
func (oneSecret) Get(context.Context, string) ([]byte, error)         { return []byte("key"), nil }
func (oneSecret) Delete(context.Context, string) error                { return nil }
func (oneSecret) Lookup(context.Context, string) (string, error)      { return "", nil }

func TestPerformBooksOnlyPossiblySentFailuresAsUnknown(t *testing.T) {
	e := newPhraseEnv(t)
	e.offer(e.human.PrincipalID, contract.ReplyPreview{ID: "s", VoiceSession: e.session, State: "generated", Phrases: []string{"One.", "Two.", "Three."}})
	e.svc.deps.Secrets = oneSecret{}
	for i, c := range []struct {
		name string
		err  error
		want string
	}{
		{"never sent", &openrouter.CallError{MayHaveExecuted: false}, "no_charge"},
		{"validated before any request", errors.New("speech: model, voice and bounded text required"), "no_charge"},
		{"may have reached the provider", &openrouter.CallError{MayHaveExecuted: true}, "unknown"},
	} {
		plan, err := e.prepare(e.human, "s", i)
		if err != nil {
			t.Fatalf("%s: prepare: %v", c.name, err)
		}
		e.svc.NewSpeech = func(string) Speech { return failingSpeech{c.err} }
		res, err := e.svc.Perform(e.ctx, plan)
		if err != nil {
			t.Fatalf("%s: perform: %v", c.name, err)
		}
		var out result
		if err := jsonDecode(res.Data, &out); err != nil || out.Billing != c.want {
			t.Fatalf("%s: billing = %q (%v), want %q", c.name, out.Billing, err, c.want)
		}
	}
}

func TestStaleVoiceCallsReleaseTheirReservationOnNextUse(t *testing.T) {
	e := newPhraseEnv(t)
	e.offer(e.human.PrincipalID, contract.ReplyPreview{ID: "s", VoiceSession: e.session, State: "generated", Phrases: []string{"One.", "Two."}})
	// Finish never runs for the first call: it stays pending and reserved.
	if _, err := e.prepare(e.human, "s", 0); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	// Within the call bound, the next call leaves it alone.
	e.clock.now = e.clock.now.Add(time.Minute)
	if _, err := e.prepare(e.human, "s", 1); err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if len(e.ports.settles) != 0 {
		t.Fatalf("settled %d calls still inside the call bound", len(e.ports.settles))
	}
	// After the bound, the next voice use settles both as advisory
	// estimates of their full reservation, releasing the slots.
	e.clock.now = e.clock.now.Add(staleCallAfter + time.Second)
	err := e.db.Write(e.ctx, e.human, e.scope, func(u contract.Unit) error { return e.svc.resolveStale(e.ctx, u) })
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(e.ports.settles) != 2 {
		t.Fatalf("settled %d stale calls, want 2", len(e.ports.settles))
	}
	for _, in := range e.ports.settles {
		usage, _ := in["usage"].(map[string]any)
		if in["authoritative_nonexecution"] != false || usage["estimated"] != float64(1000) || usage["advisory"] != true {
			t.Fatalf("stale settlement %v, want a conservative advisory estimate of the reservation", in)
		}
	}
	// Resolution is once only.
	err = e.db.Write(e.ctx, e.human, e.scope, func(u contract.Unit) error { return e.svc.resolveStale(e.ctx, u) })
	if err != nil || len(e.ports.settles) != 2 {
		t.Fatalf("second resolve settled again (%d, %v)", len(e.ports.settles), err)
	}
}

func TestDuplicatePhraseRefusalIgnoresRequestByteForm(t *testing.T) {
	e := newPhraseEnv(t)
	e.offer(e.human.PrincipalID, contract.ReplyPreview{ID: "s", VoiceSession: e.session, State: "generated", Phrases: []string{"One."}})
	if _, err := e.prepare(e.human, "s", 0); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	reordered := []byte(`{"phrase_index":0,"stream_id":"s","session_id":"` + string(e.session) + `","scope":{"installation_id":"` + string(e.scope.InstallationID) + `"}}`)
	err := e.db.Write(e.ctx, e.human, e.scope, func(u contract.Unit) error {
		_, err := e.svc.Prepare(e.ctx, u, contract.Invocation{Operation: "voice.speak.phrase", Version: 1, Input: reordered})
		return err
	})
	if faultCode(err) != contract.CodeConflict || e.ports.reserves != 1 {
		t.Fatalf("reordered duplicate = %v with %d reservations, want conflict and 1", err, e.ports.reserves)
	}
}

func TestBeginningASessionEndsTheConversationsEarlierOne(t *testing.T) {
	e := newPhraseEnv(t)
	var begun Session
	err := e.db.Write(e.ctx, e.human, e.scope, func(u contract.Unit) error {
		in := raw(map[string]any{"scope": e.scope, "conversation_id": e.conv, "settings": map[string]any{
			"input_connection": map[string]any{"id": contract.NewID(), "version": 1}, "output_connection": map[string]any{"id": contract.NewID(), "version": 1},
			"transcription_model": "openai/whisper-large-v3-turbo", "speech_model": "hexgrad/kokoro-82m", "voice": "af_heart", "language": "en",
			"style": "conversational", "budget_micro_units": 100000, "call_allowance_micro_units": 1000, "advisory_cost_acknowledged": true}})
		p, err := e.svc.Handle(e.ctx, u, contract.Invocation{Operation: "voice.session.begin", Version: 1, Input: in})
		if err != nil {
			return err
		}
		var out struct {
			Resource Session `json:"resource"`
		}
		if err := jsonDecode(p.Data, &out); err != nil {
			return err
		}
		begun = out.Resource
		return nil
	})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	var old Session
	var craft struct {
		Session contract.ID `json:"session_id"`
	}
	err = e.db.Write(e.ctx, e.human, e.scope, func(u contract.Unit) error {
		if old, err = e.svc.load(e.ctx, u, e.session); err != nil {
			return err
		}
		p, err := e.svc.craft(e.ctx, u, contract.Invocation{Operation: "_voice.craft", Version: 1,
			Input: raw(map[string]any{"scope": e.scope, "conversation_id": e.conv, "recipient_id": e.human.PrincipalID})})
		if err != nil {
			return err
		}
		return jsonDecode(p.Data, &craft)
	})
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if old.State != "ended" {
		t.Fatalf("earlier session state = %q, want ended", old.State)
	}
	if craft.Session != begun.ID {
		t.Fatalf("craft bound previews to %s, want the new session %s", craft.Session, begun.ID)
	}
}

func jsonDecode(b []byte, v any) error { return json.Unmarshal(b, v) }
