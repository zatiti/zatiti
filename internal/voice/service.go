// Package voice owns bounded, explicitly consented human speech sessions.
package voice

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/narrate-it/narrate/speech/openrouter"
	"github.com/zatiti/zatiti/internal/contract"
)

//go:embed catalog.json
var catalog []byte

type Ref struct {
	ID      contract.ID `json:"id"`
	Version int64       `json:"version"`
}
type Settings struct {
	Input         Ref    `json:"input_connection"`
	Output        Ref    `json:"output_connection"`
	Transcription string `json:"transcription_model"`
	Speech        string `json:"speech_model"`
	Voice         string `json:"voice"`
	Language      string `json:"language"`
	Style         string `json:"style"`
	Budget        int64  `json:"budget_micro_units"`
	Allowance     int64  `json:"call_allowance_micro_units"`
	Advisory      bool   `json:"advisory_cost_acknowledged"`
}
type Session struct {
	ID           contract.ID `json:"id"`
	Conversation contract.ID `json:"conversation_id"`
	State        string      `json:"state"`
	Settings     Settings    `json:"settings"`
	Reserved     int64       `json:"reserved_micro_units"`
	Calls        int64       `json:"calls"`
	Expires      time.Time   `json:"expires_at"`
}
type request struct {
	Scope        contract.Scope `json:"scope"`
	Session      contract.ID    `json:"session_id,omitempty"`
	Conversation contract.ID    `json:"conversation_id,omitempty"`
	Settings     Settings       `json:"settings,omitempty"`
	Audio        string         `json:"audio,omitempty"`
	Message      contract.ID    `json:"message_id,omitempty"`
	Stream       string         `json:"stream_id,omitempty"`
	Phrase       int            `json:"phrase_index,omitempty"`
}
type result struct {
	Session  contract.ID `json:"session_id"`
	Call     contract.ID `json:"call_id"`
	Text     string      `json:"text"`
	Audio    string      `json:"audio"`
	Media    string      `json:"media_type"`
	Billing  string      `json:"billing"`
	Reserved int64       `json:"reserved_micro_units"`
}
type prepared struct {
	Request     request
	Session     Session
	SecretRef   string
	Reservation contract.ID
	Text        string
	Amount      int64
}
type Speech interface {
	Transcribe(context.Context, string, string, []byte) (openrouter.Result, error)
	Speak(context.Context, string, string, string) (openrouter.Result, error)
	Rewrite(context.Context, string, string) (string, error)
}
type Service struct {
	deps         contract.Dependencies
	descriptors  []contract.Descriptor
	inputSchemas map[string]json.RawMessage
	NewSpeech    func(string) Speech
}

func New(d contract.Dependencies) (*Service, error) {
	if d.Clock == nil || d.IDs == nil {
		return nil, fault(contract.CodeInternalError, "voice requires clock and IDs")
	}
	s := &Service{deps: d, inputSchemas: make(map[string]json.RawMessage), NewSpeech: func(key string) Speech { return openrouter.Client{Key: key} }}
	var ops []struct {
		ID         string          `json:"id"`
		Version    int64           `json:"version"`
		Mode       string          `json:"mode"`
		Effect     string          `json:"effect"`
		Input      json.RawMessage `json:"input_schema"`
		Output     json.RawMessage `json:"output_schema"`
		CLI        []string        `json:"cli"`
		MCP        string          `json:"mcp"`
		Submission bool            `json:"submission_key"`
		Visibility string          `json:"visibility"`
		Callers    []string        `json:"callers"`
	}
	if err := json.Unmarshal(catalog, &ops); err != nil {
		return nil, err
	}
	for _, o := range ops {
		s.inputSchemas[o.ID] = o.Input
		s.descriptors = append(s.descriptors, contract.Descriptor{ID: o.ID, Version: o.Version, Owner: "voice", Visibility: o.Visibility, Callers: o.Callers, Mode: o.Mode, Effect: o.Effect, InputSchema: withoutDefs(o.Input), OutputSchema: withoutDefs(o.Output), CLI: o.CLI, MCP: o.MCP, SubmissionKey: o.Submission, ScopeRequired: []string{"installation_id"}})
	}
	return s, nil
}
func (s *Service) Name() string                       { return "voice" }
func (s *Service) Descriptors() []contract.Descriptor { return s.descriptors }
func (s *Service) Migrations() []contract.Migration {
	body := `CREATE TABLE voice_sessions (id TEXT PRIMARY KEY, actor_id TEXT NOT NULL, scope_json TEXT NOT NULL, generation INTEGER NOT NULL, data TEXT NOT NULL);
 CREATE TABLE voice_calls (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, operation TEXT NOT NULL, reservation_id TEXT NOT NULL, state TEXT NOT NULL, amount INTEGER NOT NULL, request_digest TEXT NOT NULL, result_json TEXT NOT NULL DEFAULT '{}');`
	return []contract.Migration{{Owner: "voice", Version: 1, SQL: body, SHA256: contract.Hash([]byte(body))}}
}
func withoutDefs(schema json.RawMessage) json.RawMessage {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(schema, &document); err != nil {
		return schema
	}
	delete(document, "$defs")
	body, err := json.Marshal(document)
	if err != nil {
		return schema
	}
	return body
}
func fault(code, message string) *contract.Fault {
	return &contract.Fault{Code: code, Message: message}
}
func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func completed(v any) contract.Payload {
	return contract.Payload{Status: contract.StatusCompleted, Data: raw(v)}
}
func (s *Service) decode(u contract.Unit, inv contract.Invocation) (request, error) {
	var in request
	if u.Actor().Kind != contract.KindHuman {
		return in, fault(contract.CodePermissionDenied, "voice is a human interaction capability")
	}
	for _, d := range s.descriptors {
		if inv.Operation == d.ID && inv.Version == 1 {
			if err := contract.ValidateSchema(s.inputSchemas[d.ID], inv.Input); err != nil {
				return in, err
			}
			if err := contract.DecodeStrict(inv.Input, &in); err != nil {
				return in, err
			}
			if in.Scope != u.Scope() {
				return in, fault(contract.CodePermissionDenied, "voice scope mismatch")
			}
			return in, nil
		}
	}
	return in, fault(contract.CodeInvalidInput, "unknown voice operation")
}
func (s *Service) peer(ctx context.Context, u contract.Unit, op string, in, out any) error {
	if s.deps.Ports == nil {
		return fault(contract.CodePrerequisiteMissing, "voice ports unavailable")
	}
	p, err := s.deps.Ports.Call(ctx, u, contract.Invocation{Operation: op, Version: 1, Input: raw(in)})
	if err != nil {
		return err
	}
	return json.Unmarshal(p.Data, out)
}
func (s *Service) readReply(ctx context.Context, u contract.Unit, conversation, message contract.ID) (string, error) {
	in := map[string]any{"scope": u.Scope(), "conversation_id": conversation}
	if message != "" {
		in["message_id"] = message
	}
	var out struct {
		Text string `json:"text"`
	}
	err := s.peer(ctx, u, "_messaging.voice.read", in, &out)
	return out.Text, err
}
func (s *Service) credential(ctx context.Context, u contract.Unit, ref Ref, path string) (string, error) {
	var out struct {
		Ref string `json:"credential_ref"`
	}
	err := s.peer(ctx, u, "_connections.voice.resolve", map[string]any{"scope": u.Scope(), "connection": ref, "destination": openrouter.BaseURL + path}, &out)
	return out.Ref, err
}
func (s *Service) load(ctx context.Context, u contract.Unit, id contract.ID) (Session, error) {
	var sess Session
	var actor, scope, data string
	var generation int64
	err := u.QueryRowContext(ctx, "SELECT actor_id,scope_json,generation,data FROM voice_sessions WHERE id=?", id).Scan(&actor, &scope, &generation, &data)
	if err == sql.ErrNoRows {
		return sess, fault(contract.CodeNotFound, "voice session unavailable")
	}
	if err != nil {
		return sess, err
	}
	if actor != string(u.Actor().PrincipalID) || scope != string(raw(u.Scope())) {
		return sess, fault(contract.CodePermissionDenied, "voice session belongs to another actor or scope")
	}
	if err = json.Unmarshal([]byte(data), &sess); err != nil {
		return sess, err
	}
	if generation != u.Generation() || !s.deps.Clock.Now().Before(sess.Expires) {
		sess.State = "expired"
	}
	return sess, nil
}
func (s *Service) save(ctx context.Context, u contract.Unit, sess Session) error {
	_, err := u.ExecContext(ctx, "UPDATE voice_sessions SET data=? WHERE id=?", string(raw(sess)), sess.ID)
	return err
}
func (s *Service) Handle(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.Payload, error) {
	if inv.Operation == "_voice.craft" {
		return s.craft(ctx, u, inv)
	}
	in, err := s.decode(u, inv)
	if err != nil {
		return contract.Payload{}, err
	}
	switch inv.Operation {
	case "voice.session.begin":
		if _, err = s.readReply(ctx, u, in.Conversation, ""); err != nil {
			return contract.Payload{}, err
		}
		if _, err = s.credential(ctx, u, in.Settings.Input, "/audio/transcriptions"); err != nil {
			return contract.Payload{}, err
		}
		if _, err = s.credential(ctx, u, in.Settings.Output, "/audio/speech"); err != nil {
			return contract.Payload{}, err
		}
		sess := Session{ID: s.deps.IDs.New(), Conversation: in.Conversation, Settings: in.Settings, State: "active", Expires: s.deps.Clock.Now().Add(time.Hour)}
		_, err = u.ExecContext(ctx, "INSERT INTO voice_sessions(id,actor_id,scope_json,generation,data) VALUES(?,?,?,?,?)", sess.ID, u.Actor().PrincipalID, string(raw(u.Scope())), u.Generation(), string(raw(sess)))
		return completed(map[string]any{"resource": sess}), err
	case "voice.session.get", "voice.session.end":
		sess, err := s.load(ctx, u, in.Session)
		if err != nil {
			return contract.Payload{}, err
		}
		if inv.Operation == "voice.session.end" {
			sess.State = "ended"
			if err = s.save(ctx, u, sess); err != nil {
				return contract.Payload{}, err
			}
		}
		return completed(map[string]any{"resource": sess}), nil
	default:
		return contract.Payload{}, fault(contract.CodeInvalidInput, "voice call requires phased IO")
	}
}
func (s *Service) Prepare(ctx context.Context, u contract.Unit, inv contract.Invocation) (contract.IOPlan, error) {
	var plan contract.IOPlan
	in, err := s.decode(u, inv)
	if err != nil {
		return plan, err
	}
	if inv.Operation != "voice.transcribe" && inv.Operation != "voice.speak" && inv.Operation != "voice.speak.phrase" {
		return plan, fault(contract.CodeInvalidInput, "unsupported voice IO")
	}
	sess, err := s.load(ctx, u, in.Session)
	if err != nil {
		return plan, err
	}
	if sess.State != "active" {
		return plan, fault(contract.CodeConflict, "voice session ended or expired")
	}
	text, err := s.readReply(ctx, u, sess.Conversation, in.Message)
	if err != nil {
		return plan, err
	}
	if inv.Operation == "voice.speak.phrase" {
		text, err = s.phrase(u, sess, in)
		if err != nil {
			return plan, err
		}
	}
	if inv.Operation == "voice.speak.phrase" {
		var seen int
		if err := u.QueryRowContext(ctx, "SELECT COUNT(*) FROM voice_calls WHERE session_id=? AND operation=? AND request_digest=?", sess.ID, inv.Operation, contract.Hash(inv.Input)).Scan(&seen); err != nil {
			return plan, err
		}
		if seen > 0 {
			return plan, fault(contract.CodeConflict, "this phrase already has a speech call; it cannot be retried with a new command")
		}
	}
	cost, count := estimate(sess.Settings, inv.Operation, text)
	ref := sess.Settings.Input
	path := "/audio/transcriptions"
	if inv.Operation == "voice.transcribe" {
		wav, e := base64.StdEncoding.DecodeString(in.Audio)
		if e != nil || !validWAV(wav) {
			return plan, fault(contract.CodeInvalidInput, "audio must be mono 16kHz PCM16 WAV, at most 15 seconds")
		}
	}
	if inv.Operation == "voice.speak" || inv.Operation == "voice.speak.phrase" {
		ref = sess.Settings.Output
		path = "/audio/speech"
		if strings.TrimSpace(text) == "" || len([]rune(text)) > 4000 {
			return plan, fault(contract.CodeInvalidInput, "reply is empty or too long for one spoken turn")
		}
	}
	secret, err := s.credential(ctx, u, ref, path)
	if err != nil {
		return plan, err
	}
	if count == 2 {
		if _, err = s.credential(ctx, u, ref, "/chat/completions"); err != nil {
			return plan, err
		}
	}
	if sess.Reserved > sess.Settings.Budget-cost || sess.Calls+count > 300 {
		return plan, fault(contract.CodeBudgetUnavailable, "voice session allowance exhausted")
	}
	id := s.deps.IDs.New()
	var reservation struct {
		Resource struct {
			ID contract.ID `json:"id"`
		} `json:"resource"`
	}
	limits := map[string]any{"currency": "USD", "spend_micro_units": sess.Settings.Budget, "concurrency": 1, "model_steps": 300, "child_count": 0, "delegation_depth": 0, "attempt_seconds": 120, "root_deadline": sess.Expires}
	if err = s.peer(ctx, u, "_accounting.reserve", map[string]any{"scope": u.Scope(), "operation_id": id, "amount": map[string]any{"currency": "USD", "micro_units": cost}, "limits": limits}, &reservation); err != nil {
		return plan, err
	}
	sess.Reserved += cost
	sess.Calls += count
	if err = s.save(ctx, u, sess); err != nil {
		return plan, err
	}
	_, err = u.ExecContext(ctx, "INSERT INTO voice_calls(id,session_id,operation,reservation_id,state,amount,request_digest) VALUES(?,?,?,?,?,?,?)", id, sess.ID, inv.Operation, reservation.Resource.ID, "pending", cost, contract.Hash(inv.Input))
	if err != nil {
		return plan, err
	}
	p := prepared{Request: in, Session: sess, SecretRef: secret, Reservation: reservation.Resource.ID, Text: text, Amount: cost}
	// Prepared is public pending data, so never put the secret reference or audio there.
	plan = contract.IOPlan{ID: id, Owner: "voice", Actor: u.Actor(), Scope: u.Scope(), Generation: u.Generation(), Invocation: contract.Invocation{Operation: inv.Operation, Version: 1, Input: raw(p)}, Prepared: raw(result{Session: sess.ID, Call: id, Billing: "unknown", Reserved: sess.Reserved})}
	return plan, nil
}
func estimate(settings Settings, operation, text string) (int64, int64) {
	cost, calls := settings.Allowance, int64(1)
	if operation == "voice.transcribe" {
		// OpenRouter lists Whisper Large V3 Turbo at $0.000003/sec; a full
		// 15-second capture is under 50 micro-USD. Keep a small floor.
		if cost < 100 {
			cost = 100
		}
		return cost, calls
	}
	need := int64(0)
	if settings.Speech == "hexgrad/kokoro-82m" {
		// $0.62/M characters = 0.62 micro-USD/character. Round up.
		need += (int64(len([]rune(text)))*62 + 99) / 100
	}
	if cost < need {
		cost = need
	}
	return cost, calls
}
func validWAV(b []byte) bool {
	if len(b) < 44 || len(b) > 480044 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" || string(b[12:16]) != "fmt " || string(b[36:40]) != "data" {
		return false
	}
	return binary.LittleEndian.Uint32(b[16:20]) == 16 && binary.LittleEndian.Uint16(b[20:22]) == 1 && binary.LittleEndian.Uint16(b[22:24]) == 1 && binary.LittleEndian.Uint32(b[24:28]) == 16000 && binary.LittleEndian.Uint32(b[28:32]) == 32000 && binary.LittleEndian.Uint16(b[32:34]) == 2 && binary.LittleEndian.Uint16(b[34:36]) == 16 && int(binary.LittleEndian.Uint32(b[40:44])) == len(b)-44 && len(b)%2 == 0 && int(binary.LittleEndian.Uint32(b[4:8])) == len(b)-8
}
func (s *Service) Perform(ctx context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	var p prepared
	if err := json.Unmarshal(plan.Invocation.Input, &p); err != nil {
		return contract.IOResult{}, err
	}
	out := result{Session: p.Session.ID, Call: plan.ID, Billing: "unknown", Reserved: p.Session.Reserved}
	failed := func(err error) (contract.IOResult, error) {
		return contract.IOResult{Data: raw(out), Fault: fault(contract.CodeOutcomeUnknown, "voice request failed or interrupted; check session usage before starting a new call")}, nil
	}
	if s.deps.Secrets == nil {
		out.Billing = "no_charge"
		return failed(errors.New("secret store missing"))
	}
	key, err := s.deps.Secrets.Get(ctx, p.SecretRef)
	if err != nil {
		out.Billing = "no_charge"
		return failed(err)
	}
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()
	client := s.NewSpeech(string(key))
	ctx, cancel := context.WithTimeout(ctx, 110*time.Second)
	defer cancel()
	if plan.Invocation.Operation == "voice.transcribe" {
		wav, _ := base64.StdEncoding.DecodeString(p.Request.Audio)
		response, e := client.Transcribe(ctx, p.Session.Settings.Transcription, p.Session.Settings.Language, wav)
		if e != nil {
			return failed(e)
		}
		out.Text = response.Text
	} else {
		text := p.Text
		response, e := client.Speak(ctx, p.Session.Settings.Speech, p.Session.Settings.Voice, text)
		if e != nil {
			return failed(e)
		}
		out.Text = text
		out.Audio = base64.StdEncoding.EncodeToString(response.Audio)
		out.Media = response.MediaType
	}
	out.Billing = "estimated"
	// The current caller receives synthesized bytes once, while command replay
	// and evidence retain only non-sensitive call metadata.
	persisted := persistentSpeechResult(out)
	return contract.IOResult{Data: raw(out), PersistentData: raw(persisted)}, nil
}

func persistentSpeechResult(out result) result {
	return result{Session: out.Session, Call: out.Call, Billing: out.Billing, Reserved: out.Reserved}
}

func (s *Service) Finish(ctx context.Context, u contract.Unit, plan contract.IOPlan, ioResult contract.IOResult) (contract.Payload, error) {
	var p prepared
	var out result
	if json.Unmarshal(plan.Invocation.Input, &p) != nil || json.Unmarshal(ioResult.Data, &out) != nil {
		return contract.Payload{}, fault(contract.CodeInternalError, "invalid voice result")
	}
	if u.Generation() != plan.Generation || u.Actor() != plan.Actor || u.Scope() != plan.Scope {
		return contract.Payload{}, fault(contract.CodeConflict, "voice generation changed")
	}
	sess, err := s.load(ctx, u, p.Session.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	usage := map[string]any{"currency": "USD", "spent": 0, "reserved": 0, "estimated": 0, "unknown": 0, "advisory": true}
	switch out.Billing {
	case "estimated":
		usage["estimated"] = p.Amount
	case "unknown":
		usage["unknown"] = p.Amount
	}
	var settled map[string]any
	if err = s.peer(ctx, u, "_accounting.settle", map[string]any{"reservation_id": p.Reservation, "expected_version": 1, "usage": usage, "authoritative_nonexecution": out.Billing == "no_charge"}, &settled); err != nil {
		return contract.Payload{}, err
	}
	if sess.State != "active" {
		out.Audio = ""
		out.Text = ""
	}
	if plan.Invocation.Operation == "voice.speak.phrase" {
		if text, e := s.phrase(u, sess, p.Request); e != nil || text != p.Text {
			out.Audio = ""
			out.Text = ""
		}
	}
	// Recheck disclosure and credential versions before returning any audio/text.
	if _, err = s.readReply(ctx, u, sess.Conversation, p.Request.Message); err != nil {
		out.Audio = ""
		out.Text = ""
	}
	ref, path := sess.Settings.Input, "/audio/transcriptions"
	if plan.Invocation.Operation == "voice.speak" || plan.Invocation.Operation == "voice.speak.phrase" {
		ref = sess.Settings.Output
		path = "/audio/speech"
	}
	if _, e := s.credential(ctx, u, ref, path); e != nil {
		out.Audio = ""
		out.Text = ""
	}
	_, err = u.ExecContext(ctx, "UPDATE voice_calls SET state=?,result_json=? WHERE id=? AND state='pending'", out.Billing, string(raw(map[string]any{"billing": out.Billing, "reserved_micro_units": out.Reserved})), plan.ID)
	if err != nil {
		return contract.Payload{}, err
	}
	// Mutate the result bytes as well: Application uses IOResult.Data on failure.
	return completed(out), nil
}
