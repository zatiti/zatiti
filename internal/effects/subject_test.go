package effects

import (
	"encoding/json"
	"testing"

	"github.com/zatiti/zatiti/internal/contract"
)

func TestEffectSubjectPersistsPreparingPrincipal(t *testing.T) {
	env := newEnv(t)
	operation := env.staged()
	controller := contract.Actor{PrincipalID: env.ids.New(), Kind: contract.KindService}
	input, err := json.Marshal(map[string]any{"operation_id": operation.ID})
	if err != nil {
		t.Fatal(err)
	}
	err = env.db.Read(env.ctx, controller, env.scope, func(unit contract.Unit) error {
		actor, scope, err := env.svc.ResolveEffectSubject(env.ctx, unit, contract.Invocation{Operation: opAdmit, Input: input})
		if err != nil {
			return err
		}
		if actor != env.actor {
			t.Fatalf("subject = %+v, want preparing actor %+v", actor, env.actor)
		}
		if scope.InstallationID != env.install {
			t.Fatalf("subject installation = %s", scope.InstallationID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLegacyEffectSubjectFailsClosed(t *testing.T) {
	env := newEnv(t)
	operation := env.staged()
	err := env.write(func(unit contract.Unit) error {
		_, err := unit.ExecContext(env.ctx, "UPDATE effects_operations SET subject_json = '' WHERE id = ?", operation.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"operation_id": operation.ID})
	if err != nil {
		t.Fatal(err)
	}
	err = env.db.Read(env.ctx, env.actor, env.scope, func(unit contract.Unit) error {
		_, _, err := env.svc.ResolveEffectSubject(env.ctx, unit, contract.Invocation{Operation: opAdmit, Input: input})
		return err
	})
	if err == nil {
		t.Fatal("legacy effect was assigned an invented authorization subject")
	}
	if f, ok := err.(*contract.Fault); !ok || f.Code != contract.CodePrerequisiteMissing {
		t.Fatalf("legacy subject error = %v", err)
	}
}
