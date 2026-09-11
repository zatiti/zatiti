package messaging

import (
	"strings"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

// Module shape: owner identity, frozen wire contract, strict dispatch.

func TestServiceNameIsOwner(t *testing.T) {
	env := newEnv(t)
	if env.svc.Name() != "messaging" {
		t.Fatalf("messaging.New: service name is %s, want %s", env.svc.Name(), "messaging")
	}
}

func TestServiceImplementsModule(t *testing.T) {
	env := newEnv(t)
	var m contract.Module = env.svc
	if m.Name() != "messaging" || m.Migrations() == nil || m.Descriptors() == nil {
		t.Fatalf("messaging module surface incomplete")
	}
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	env := newEnv(t)
	clock := contract.Dependencies{}
	if _, err := New(clock); err == nil {
		t.Fatalf("messaging.New without clock returned no error")
	}
	ids := contract.Dependencies{Clock: env.clock}
	if _, err := New(ids); err == nil {
		t.Fatalf("messaging.New without ids returned no error")
	}
	ports := contract.Dependencies{Clock: env.clock, IDs: env.ids}
	if _, err := New(ports); err == nil {
		t.Fatalf("messaging.New without ports returned no error")
	}
	var f *contract.Fault
	if _, err := New(clock); !asFault(err, &f) || f.Code != contract.CodeInternalError {
		t.Fatalf("messaging.New fault code %v, want internal_error", f)
	}
}

func TestMigrationsShape(t *testing.T) {
	env := newEnv(t)
	migs := env.svc.Migrations()
	if len(migs) != 1 {
		t.Fatalf("migrations count %d, want 1", len(migs))
	}
	m := migs[0]
	if m.Owner != "messaging" || m.Version != 1 {
		t.Fatalf("migration owner/version %s/%d, want messaging/1", m.Owner, m.Version)
	}
	if m.SHA256 == "" || len(m.SHA256) != 64 {
		t.Fatalf("migration sha256 %q is not a digest", m.SHA256)
	}
	for _, table := range []string{
		"CREATE TABLE messaging_conversations", "CREATE TABLE messaging_messages",
		"CREATE TABLE messaging_recipients", "CREATE TABLE messaging_receipts",
		"CREATE TABLE messaging_read_markers",
	} {
		if !strings.Contains(m.SQL, table) {
			t.Fatalf("migration SQL missing %s", table)
		}
	}
	if strings.Contains(m.SQL, "DROP") || strings.Contains(m.SQL, "ALTER") {
		t.Fatalf("migration SQL must be additive only")
	}
}

func TestDescriptorsExactness(t *testing.T) {
	env := newEnv(t)
	descs := env.svc.Descriptors()
	if len(descs) != len(opMetas) {
		t.Fatalf("descriptor count %d, want %d", len(descs), len(opMetas))
	}
	metaByID := map[string]opMeta{}
	for _, m := range opMetas {
		metaByID[m.id] = m
	}
	byID := map[string]contract.Descriptor{}
	for _, d := range descs {
		if _, dup := byID[d.ID]; dup {
			t.Fatalf("duplicate descriptor %s", d.ID)
		}
		byID[d.ID] = d
		meta, ok := metaByID[d.ID]
		if !ok {
			t.Fatalf("descriptor %s has no registration record", d.ID)
		}
		if d.Owner != "messaging" || d.Version != 1 {
			t.Fatalf("%s owner/version %s/%d, want messaging/1", d.ID, d.Owner, d.Version)
		}
		if len(d.ScopeRequired) != 1 || d.ScopeRequired[0] != "installation_id" {
			t.Fatalf("%s scope_required %v, want [installation_id]", d.ID, d.ScopeRequired)
		}
		if len(d.InputSchema) == 0 || len(d.OutputSchema) == 0 {
			t.Fatalf("%s carries empty schemas", d.ID)
		}
		if d.Mode != meta.mode {
			t.Fatalf("%s mode %q, want %q", d.ID, d.Mode, meta.mode)
		}
		if d.SubmissionKey != meta.submission {
			t.Fatalf("%s submission %v, want %v", d.ID, d.SubmissionKey, meta.submission)
		}
		if d.ExpectedVersion != meta.expectedVersion {
			t.Fatalf("%s expected_version %v, want %v", d.ID, d.ExpectedVersion, meta.expectedVersion)
		}
	}
}

func TestDescriptorEffectsAndBindings(t *testing.T) {
	env := newEnv(t)
	byID := map[string]contract.Descriptor{}
	for _, d := range env.svc.Descriptors() {
		byID[d.ID] = d
	}
	// Only the two send paths disclose; every other operation is local.
	for _, op := range []string{"conversation.message.send", "mailbox.send"} {
		if byID[op].Effect != "disclosure" {
			t.Fatalf("%s effect %q, want disclosure", op, byID[op].Effect)
		}
	}
	for _, d := range env.svc.Descriptors() {
		if d.Effect != "disclosure" && d.Effect != "local" {
			t.Fatalf("%s effect %q, want local or disclosure", d.ID, d.Effect)
		}
	}
	// Internal operations carry exact caller allowlists and no transport
	// bindings; public operations carry CLI and MCP names.
	internal := map[string][]string{
		"_messaging.admit":     {"execution", "controller", "scheduling"},
		"_messaging.bootstrap": {"installation"},
		"_messaging.pending":   {"execution", "scheduling"},
	}
	for op, callers := range internal {
		d := byID[op]
		if d.Visibility != "internal" {
			t.Fatalf("%s visibility %q, want internal", op, d.Visibility)
		}
		if strings.Join(d.Callers, ",") != strings.Join(callers, ",") {
			t.Fatalf("%s callers %v, want %v", op, d.Callers, callers)
		}
		if d.CLI != nil || d.MCP != "" {
			t.Fatalf("%s internal operation carries transport bindings", op)
		}
	}
	for _, d := range env.svc.Descriptors() {
		if d.Visibility != "public" {
			continue
		}
		if len(d.Callers) != 0 {
			t.Fatalf("%s public operation carries callers %v", d.ID, d.Callers)
		}
		if len(d.CLI) == 0 || !strings.HasPrefix(strings.Join(d.CLI, " "), "zatiti ") {
			t.Fatalf("%s public operation misses the CLI path %v", d.ID, d.CLI)
		}
		want := "zatiti_" + strings.ReplaceAll(d.ID, ".", "_")
		if d.MCP != want {
			t.Fatalf("%s mcp %q, want %q", d.ID, d.MCP, want)
		}
	}
}

func TestHandleStrictDispatch(t *testing.T) {
	env := newEnv(t)

	t.Run("unknown operation is not_found", func(t *testing.T) {
		f := env.expectFault("conversation.nonsense", map[string]any{}, contract.CodeNotFound)
		if !strings.Contains(f.Message, "unknown operation") {
			t.Fatalf("message %q does not name the unknown operation", f.Message)
		}
	})

	t.Run("version mismatch is invalid_input", func(t *testing.T) {
		var got *contract.Fault
		raw := mustJSON(map[string]any{"scope": env.scope, "id": env.ids.New()})
		err := env.db.Write(env.ctx, env.actor, env.scope.toContract(), func(unit contract.Unit) error {
			_, err := env.svc.Handle(env.ctx, unit, contract.Invocation{Operation: "conversation.get", Version: 2, Input: raw})
			return err
		})
		asFault(err, &got)
		if got == nil || got.Code != contract.CodeInvalidInput {
			t.Fatalf("version mismatch fault %v, want invalid_input", got)
		}
	})

	t.Run("mutation refused in read-only transaction", func(t *testing.T) {
		raw := mustJSON(map[string]any{"scope": env.scope, "conversation_id": env.ids.New(),
			"message_id": env.ids.New(), "body": "x", "attachments": []wireArtifactRef{}, "task_ids": []contract.ID{}})
		var got *contract.Fault
		err := env.db.Read(env.ctx, env.actor, env.scope.toContract(), func(unit contract.Unit) error {
			_, err := env.svc.Handle(env.ctx, unit, contract.Invocation{Operation: "conversation.message.send", Version: 1, Input: raw})
			return err
		})
		asFault(err, &got)
		if got == nil || got.Code != contract.CodeInvalidInput {
			t.Fatalf("read-only mutation fault %v, want invalid_input", got)
		}
	})

	t.Run("query allowed in read-only transaction", func(t *testing.T) {
		raw := mustJSON(map[string]any{"scope": env.scope, "id": env.ids.New()})
		err := env.db.Read(env.ctx, env.actor, env.scope.toContract(), func(unit contract.Unit) error {
			_, err := env.svc.Handle(env.ctx, unit, contract.Invocation{Operation: "conversation.get", Version: 1, Input: raw})
			return err
		})
		var got *contract.Fault
		asFault(err, &got)
		if got == nil || got.Code != contract.CodeNotFound {
			t.Fatalf("read-only query fault %v, want not_found for absent conversation", got)
		}
	})

	t.Run("input outside the operation schema is invalid_input", func(t *testing.T) {
		// conversation.get with an unexpected extra member: additionalProperties=false.
		_ = env.expectFault("conversation.get", map[string]any{
			"scope": env.scope, "id": env.ids.New(), "surprise": 1,
		}, contract.CodeInvalidInput)
	})
}
